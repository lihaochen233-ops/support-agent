package platform

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "golang.org/x/image/webp"
)

func (s *Server) registerMedia(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/attachments", s.handleUpload)
	mux.HandleFunc("GET /api/attachments/{id}", s.handleAttachment)
}

func validateImage(data []byte) (string, string, error) {
	if len(data) > 5<<20 {
		return "", "", badRequest("图片不能超过 5 MB")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", "", badRequest("图片无效，仅支持 JPEG、PNG、WebP")
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 20_000_000 {
		return "", "", badRequest("图片不能超过 2000 万像素")
	}
	if _, _, err = image.Decode(bytes.NewReader(data)); err != nil {
		return "", "", badRequest("图片文件已损坏或不完整")
	}
	switch format {
	case "jpeg":
		return "image/jpeg", ".jpg", nil
	case "png":
		return "image/png", ".png", nil
	case "webp":
		return "image/webp", ".webp", nil
	default:
		return "", "", badRequest("仅支持 JPEG、PNG、WebP 图片")
	}
}

func safeFilename(name string) string {
	clean := strings.ReplaceAll(name, "\\", "/")
	clean = filepath.Base(clean)
	clean = strings.Map(func(r rune) rune {
		if r < ' ' || r == 127 {
			return -1
		}
		return r
	}, clean)
	runes := []rune(clean)
	if len(runes) > 120 {
		clean = string(runes[:120])
	}
	if clean == "" || clean == "." {
		return "image"
	}
	return clean
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	a, err := s.actor(r)
	if err != nil {
		writeError(w, 401, "请先登录")
		return
	}
	if !s.allowRate(r.Context(), "upload:"+a.ID, 15, time.Minute) {
		writeError(w, 429, "上传过于频繁")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 6<<20)
	if err = r.ParseMultipartForm(6 << 20); err != nil {
		writeError(w, 400, "文件无效或超过 5 MB")
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	id := r.FormValue("conversation_id")
	if !validID(id) {
		writeError(w, 400, "会话编号无效")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, 400, "请选择图片")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (5<<20)+1))
	if err != nil {
		writeError(w, 400, "读取图片失败")
		return
	}
	mimeType, ext, err := validateImage(data)
	if err != nil {
		sendError(w, err)
		return
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		sendError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if err = checkActorTx(r.Context(), tx, a); err != nil {
		sendError(w, err)
		return
	}
	c, err := scanConversation(tx.QueryRow(r.Context(), `SELECT to_jsonb(c) FROM conversations c WHERE id=$1 FOR UPDATE`, id))
	if err != nil {
		sendError(w, err)
		return
	}
	if !s.canRead(r.Context(), a, c) || (a.Role != "visitor" && (c.Status != "human" || c.AgentID != a.ID)) {
		writeError(w, 403, "无权在此会话上传图片")
		return
	}
	if c.Status == "closed" {
		writeError(w, 409, "会话已结束")
		return
	}
	attachmentID := newID()
	name := safeFilename(header.Filename)
	path := filepath.Join(s.Config.UploadDir, "images", attachmentID+ext)
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		sendError(w, err)
		return
	}
	// 文件名完全由服务端生成，用户文件名只用于展示。
	if err = os.WriteFile(path, data, 0600); err != nil {
		sendError(w, err)
		return
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(path)
		}
	}()
	_, err = tx.Exec(r.Context(), `INSERT INTO attachments(id,conversation_id,uploader_id,path,mime,name,size) VALUES($1,$2,$3,$4,$5,$6,$7)`, attachmentID, id, a.ID, path, mimeType, name, len(data))
	if err != nil {
		sendError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		sendError(w, err)
		return
	}
	committed = true
	writeJSON(w, 201, map[string]any{"id": attachmentID, "url": "/api/attachments/" + attachmentID, "mime": mimeType, "name": name})
}

func (s *Server) handleAttachment(w http.ResponseWriter, r *http.Request) {
	a, err := s.actor(r)
	if err != nil {
		writeError(w, 401, "请先登录")
		return
	}
	id := r.PathValue("id")
	if !validID(id) {
		writeError(w, 400, "图片编号无效")
		return
	}
	var conversationID, path, mimeType, name string
	err = s.DB.QueryRow(r.Context(), `SELECT conversation_id::text,path,mime,name FROM attachments WHERE id=$1`, id).Scan(&conversationID, &path, &mimeType, &name)
	if err != nil {
		sendError(w, err)
		return
	}
	c, err := s.conversation(r.Context(), conversationID)
	if err != nil {
		sendError(w, err)
		return
	}
	if !s.canRead(r.Context(), a, c) {
		writeError(w, 403, "无权查看图片")
		return
	}
	root, err := filepath.Abs(s.Config.UploadDir)
	if err != nil {
		sendError(w, err)
		return
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		sendError(w, err)
		return
	}
	relative, err := filepath.Rel(root, absolute)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		writeError(w, 404, "图片不存在")
		return
	}
	file, err := os.Open(absolute)
	if err != nil {
		writeError(w, 404, "图片不存在")
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.IsDir() {
		writeError(w, 404, "图片不存在")
		return
	}
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": name}))
	w.Header().Set("Content-Length", fmt.Sprint(info.Size()))
	http.ServeContent(w, r, name, info.ModTime(), file)
}
