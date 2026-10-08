package platform

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/ledongthuc/pdf"
)

type textPart struct {
	Text    string
	Page    int
	Section string
}

// Rune boundaries preserve Chinese characters. Chunks never cross PDF pages so
// source locations remain truthful, including when neighbouring text overlaps.
func chunkParts(parts []textPart) []textPart {
	result := make([]textPart, 0)
	for _, part := range parts {
		runes := []rune(strings.TrimSpace(strings.ReplaceAll(part.Text, "\x00", "")))
		for start := 0; start < len(runes); {
			end := start + 700
			if end > len(runes) {
				end = len(runes)
			}
			content := strings.TrimSpace(string(runes[start:end]))
			if content != "" {
				section := part.Section
				if section == "" {
					section = fmt.Sprintf("段落 %d", len(result)+1)
				}
				result = append(result, textPart{content, part.Page, section})
			}
			if end == len(runes) {
				break
			}
			start = end - 100
		}
	}
	return result
}

type documentJob struct {
	ID, DocumentID, Path, Filename, Owner string
	Generation                            int64
}

func (s *Server) claimDocumentJob(ctx context.Context) (documentJob, error) {
	var job documentJob
	err := s.DB.QueryRow(ctx, `WITH candidate AS (
 SELECT v.id FROM document_versions v JOIN documents d ON d.id=v.document_id
 WHERE v.status='processing' AND NOT d.deleted AND (v.lease_until IS NULL OR v.lease_until<now())
 ORDER BY v.created_at FOR UPDATE OF v SKIP LOCKED LIMIT 1)
 UPDATE document_versions v SET lease_owner=$1,lease_generation=lease_generation+1,
 lease_until=now()+interval '60 seconds',attempts=attempts+1,updated_at=now()
 FROM candidate WHERE v.id=candidate.id
 RETURNING v.id,v.document_id,v.path,v.filename,v.lease_owner,v.lease_generation`, s.Config.InstanceID).Scan(&job.ID, &job.DocumentID, &job.Path, &job.Filename, &job.Owner, &job.Generation)
	return job, err
}
func (s *Server) documentWorkerLoop(ctx context.Context) {
	for ctx.Err() == nil {
		job, err := s.claimDocumentJob(ctx)
		if err == nil {
			s.processDocumentJob(ctx, job)
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) && ctx.Err() == nil {
			slog.Warn("document poll failed", "error", err)
		}
		if !s.workerWait(ctx) {
			return
		}
	}
}
func (s *Server) documentHeartbeat(ctx context.Context, j documentJob, cancel context.CancelFunc) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			checkCtx, done := context.WithTimeout(ctx, 5*time.Second)
			tag, err := s.DB.Exec(checkCtx, `UPDATE document_versions v SET lease_until=now()+interval '60 seconds',updated_at=now() WHERE id=$1 AND status='processing' AND lease_owner=$2 AND lease_generation=$3 AND lease_until>now() AND EXISTS(SELECT 1 FROM documents d WHERE d.id=v.document_id AND NOT d.deleted)`, j.ID, j.Owner, j.Generation)
			done()
			if err != nil || tag.RowsAffected() != 1 {
				cancel()
				return
			}
		}
	}
}
func (s *Server) processDocumentJob(parent context.Context, j documentJob) {
	ctx, cancel := context.WithTimeout(parent, 20*time.Minute)
	defer cancel()
	go s.documentHeartbeat(ctx, j, cancel)
	chunks, err := parseDocument(j.Path, strings.ToLower(filepath.Ext(j.Filename)))
	vectors := make([][]float32, 0, len(chunks))
	if err == nil && s.Config.EmbeddingAPIKey == "" {
		err = errors.New("向量模型 API Key 未配置，配置后可重试")
	}
	for start := 0; err == nil && start < len(chunks); start += 10 {
		end := start + 10
		if end > len(chunks) {
			end = len(chunks)
		}
		texts := make([]string, 0, end-start)
		for _, chunk := range chunks[start:end] {
			texts = append(texts, chunk.Text)
		}
		var batch [][]float32
		batch, err = s.modelProvider("embedding", "").embed(ctx, texts)
		vectors = append(vectors, batch...)
	}
	if parent.Err() != nil {
		return
	}
	commitCtx, done := context.WithTimeout(context.Background(), 20*time.Second)
	defer done()
	if err != nil {
		_, saveErr := s.DB.Exec(commitCtx, `UPDATE document_versions SET status='failed',error=$4,lease_until=NULL,updated_at=now() WHERE id=$1 AND status='processing' AND lease_owner=$2 AND lease_generation=$3 AND lease_until>now()`, j.ID, j.Owner, j.Generation, clipRunes(err.Error(), 300))
		if saveErr != nil {
			slog.Warn("document failure could not be saved", "error", saveErr)
		}
		return
	}
	if err = s.finishDocumentJob(commitCtx, j, chunks, vectors); err != nil {
		slog.Warn("document completion failed", "error", err)
	}
}
func (s *Server) finishDocumentJob(ctx context.Context, j documentJob, chunks []textPart, vectors [][]float32) error {
	if len(chunks) == 0 || len(chunks) != len(vectors) {
		return errors.New("文档向量数量异常")
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var deleted bool
	if err = tx.QueryRow(ctx, `SELECT deleted FROM documents WHERE id=$1 FOR UPDATE`, j.DocumentID).Scan(&deleted); err != nil {
		return err
	}
	var valid bool
	err = tx.QueryRow(ctx, `SELECT status='processing' AND lease_owner=$2 AND lease_generation=$3 AND lease_until>now() FROM document_versions WHERE id=$1 FOR UPDATE`, j.ID, j.Owner, j.Generation).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid || deleted {
		return nil
	}
	_, err = tx.Exec(ctx, `DELETE FROM chunks WHERE version_id=$1`, j.ID)
	if err != nil {
		return err
	}
	for i, chunk := range chunks {
		if len(vectors[i]) != 1024 {
			return errors.New("文档向量维度异常")
		}
		_, err = tx.Exec(ctx, `INSERT INTO chunks(document_id,version_id,page,section,ordinal,content,embedding) VALUES($1,$2,$3,$4,$5,$6,$7::vector)`, j.DocumentID, j.ID, chunk.Page, chunk.Section, i, chunk.Text, vectorLiteral(vectors[i]))
		if err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE document_versions SET status='ready',error='',chunk_count=$2,embedding_fingerprint=$3,lease_until=NULL,updated_at=now() WHERE id=$1`, j.ID, len(chunks), s.embeddingFingerprint())
	if err != nil {
		return err
	}
	// The original index stays queryable until every replacement chunk commits.
	_, err = tx.Exec(ctx, `UPDATE documents SET active_version_id=$2,name=$3,updated_at=now() WHERE id=$1`, j.DocumentID, j.ID, j.Filename)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func parseDocument(path, ext string) (parts []textPart, err error) {
	defer func() {
		if recover() != nil {
			parts = nil
			err = errors.New("文档结构损坏，无法解析")
		}
	}()
	if ext != ".pdf" {
		raw, e := os.ReadFile(path)
		if e != nil {
			return nil, e
		}
		if !utf8.Valid(raw) {
			return nil, errors.New("文本必须使用 UTF-8 编码")
		}
		parts = []textPart{{Text: string(raw)}}
	} else {
		file, reader, e := pdf.Open(path)
		if e != nil {
			return nil, errors.New("PDF 无法解析或已加密，请上传可提取文字的 PDF")
		}
		defer file.Close()
		if reader.NumPage() > 50 {
			return nil, errors.New("PDF 最多支持 50 页")
		}
		total := 0
		for i := 1; i <= reader.NumPage(); i++ {
			p := reader.Page(i)
			if p.V.IsNull() {
				continue
			}
			text, e := p.GetPlainText(nil)
			if e != nil {
				return nil, fmt.Errorf("无法解析 PDF 第 %d 页", i)
			}
			total += len(text)
			if total > 10<<20 {
				return nil, errors.New("PDF 提取文字过多")
			}
			parts = append(parts, textPart{Text: text, Page: i})
		}
	}
	chunks := chunkParts(parts)
	if len(chunks) == 0 {
		return nil, errors.New("未找到可提取文字，不支持扫描版 PDF 或空文档")
	}
	if len(chunks) > 1000 {
		return nil, errors.New("文档超过 1000 个片段，请拆分后上传")
	}
	return chunks, nil
}

func vectorLiteral(values []float32) string { b, _ := json.Marshal(values); return string(b) }

func (s *Server) embeddingFingerprint() string {
	sum := sha256.Sum256([]byte(strings.TrimRight(s.Config.EmbeddingBaseURL, "/") + "|" + s.Config.EmbeddingModel + "|1024"))
	return hex.EncodeToString(sum[:])
}

const documentSelect = `SELECT d.id,d.name,d.enabled,CASE WHEN NOT d.enabled THEN 'disabled' ELSE COALESCE(v.status,'processing') END,COALESCE(d.active_version_id,''),COALESCE(v.id,''),COALESCE(v.error,''),COALESCE(v.chunk_count,0),d.created_at,d.updated_at FROM documents d LEFT JOIN LATERAL (SELECT * FROM document_versions dv WHERE dv.document_id=d.id ORDER BY dv.created_at DESC,dv.id DESC LIMIT 1) v ON true`

func scanDocument(row pgx.Row) (Document, error) {
	var d Document
	err := row.Scan(&d.ID, &d.Name, &d.Enabled, &d.Status, &d.ActiveVersionID, &d.LatestVersionID, &d.Error, &d.ChunkCount, &d.CreatedAt, &d.UpdatedAt)
	return d, err
}
func (s *Server) document(ctx context.Context, id string) (Document, error) {
	return scanDocument(s.DB.QueryRow(ctx, documentSelect+` WHERE d.id=$1 AND NOT d.deleted`, id))
}
func (s *Server) registerKnowledge(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/documents", s.listDocuments)
	mux.HandleFunc("POST /api/documents", s.uploadDocument)
	mux.HandleFunc("GET /api/documents/{id}", s.getDocument)
	mux.HandleFunc("POST /api/documents/{id}/retry", s.retryDocument)
	mux.HandleFunc("POST /api/documents/{id}/toggle", s.toggleDocument)
	mux.HandleFunc("DELETE /api/documents/{id}", s.deleteDocument)
	mux.HandleFunc("POST /api/knowledge/search", s.previewSearch)
}
func (s *Server) listDocuments(w http.ResponseWriter, r *http.Request) {
	if _, err := s.require(r, "admin"); err != nil {
		writeError(w, 403, "需要管理员身份")
		return
	}
	rows, err := s.DB.Query(r.Context(), documentSelect+` WHERE NOT d.deleted ORDER BY d.created_at DESC LIMIT 200`)
	if err != nil {
		writeError(w, 500, "读取知识库失败")
		return
	}
	defer rows.Close()
	items := []Document{}
	for rows.Next() {
		d, err := scanDocument(rows)
		if err != nil {
			writeError(w, 500, "读取文档失败")
			return
		}
		items = append(items, d)
	}
	if rows.Err() != nil {
		writeError(w, 500, "读取文档失败")
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}
func (s *Server) uploadDocument(w http.ResponseWriter, r *http.Request) {
	actor, err := s.require(r, "admin")
	if err != nil {
		writeError(w, 403, "需要管理员身份")
		return
	}
	if !s.allowRate(r.Context(), "document-upload:"+actor.ID, 5, time.Minute) {
		writeError(w, 429, "上传过于频繁，请稍后重试")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 11<<20)
	if r.ParseMultipartForm(11<<20) != nil {
		writeError(w, 400, "文件不能超过 10 MB")
		return
	}
	defer r.MultipartForm.RemoveAll()
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, 400, "请选择文档")
		return
	}
	defer file.Close()
	name := clipRunes(filepath.Base(header.Filename), 180)
	ext := strings.ToLower(filepath.Ext(name))
	if ext != ".txt" && ext != ".md" && ext != ".pdf" {
		writeError(w, 400, "仅支持 TXT、Markdown 和可提取文字的 PDF")
		return
	}
	if header.Size <= 0 || header.Size > 10<<20 {
		writeError(w, 400, "文档必须为 1 字节至 10 MB")
		return
	}
	dir := filepath.Join(s.Config.UploadDir, "knowledge")
	if os.MkdirAll(dir, 0700) != nil {
		writeError(w, 500, "无法保存文档")
		return
	}
	versionID := newID()
	path := filepath.Join(dir, versionID+ext)
	dest, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		writeError(w, 500, "无法保存文档")
		return
	}
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(dest, hash), io.LimitReader(file, (10<<20)+1))
	closeErr := dest.Close()
	if err != nil || closeErr != nil || n > 10<<20 {
		os.Remove(path)
		writeError(w, 400, "文档过大或保存失败")
		return
	}
	saved := false
	defer func() {
		if !saved {
			os.Remove(path)
		}
	}()
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "保存文档失败")
		return
	}
	defer tx.Rollback(r.Context())
	id := r.FormValue("replace_id")
	if id == "" {
		id = newID()
		_, err = tx.Exec(r.Context(), `INSERT INTO documents(id,name) VALUES($1,$2)`, id, name)
	} else {
		var exists string
		err = tx.QueryRow(r.Context(), `SELECT id FROM documents WHERE id=$1 AND NOT deleted FOR UPDATE`, id).Scan(&exists)
		if err != nil {
			writeError(w, 404, "文档不存在")
			return
		}
		var processing bool
		err = tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM document_versions WHERE document_id=$1 AND status='processing')`, id).Scan(&processing)
		if err != nil {
			writeError(w, 500, "读取处理状态失败")
			return
		}
		if processing {
			writeError(w, 409, "当前文档正在处理，请等待完成后再替换")
			return
		}
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO document_versions(id,document_id,filename,path,mime,content_hash) VALUES($1,$2,$3,$4,$5,$6)`, versionID, id, name, path, header.Header.Get("Content-Type"), hex.EncodeToString(hash.Sum(nil)))
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `UPDATE documents SET updated_at=now() WHERE id=$1`, id)
	}
	if err != nil || tx.Commit(r.Context()) != nil {
		writeError(w, 500, "保存文档失败")
		return
	}
	saved = true
	doc, err := s.document(r.Context(), id)
	if err != nil {
		writeError(w, 500, "读取文档失败")
		return
	}
	writeJSON(w, 201, doc)
}
func (s *Server) getDocument(w http.ResponseWriter, r *http.Request) {
	if _, err := s.require(r, "admin"); err != nil {
		writeError(w, 403, "需要管理员身份")
		return
	}
	doc, err := s.document(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, 404, "文档不存在")
		return
	}
	version := doc.ActiveVersionID
	if version == "" {
		version = doc.LatestVersionID
	}
	rows, err := s.DB.Query(r.Context(), `SELECT id,document_id,page,section,content FROM chunks WHERE version_id=$1 ORDER BY ordinal`, version)
	if err != nil {
		writeError(w, 500, "读取片段失败")
		return
	}
	defer rows.Close()
	chunks := []Citation{}
	for rows.Next() {
		var c Citation
		if rows.Scan(&c.ID, &c.DocumentID, &c.Page, &c.Section, &c.Text) != nil {
			writeError(w, 500, "读取片段失败")
			return
		}
		c.ChunkID = c.ID
		c.Title = doc.Name
		chunks = append(chunks, c)
	}
	if rows.Err() != nil {
		writeError(w, 500, "读取片段失败")
		return
	}
	writeJSON(w, 200, map[string]any{"document": doc, "chunks": chunks})
}
func (s *Server) toggleDocument(w http.ResponseWriter, r *http.Request) {
	if _, err := s.require(r, "admin"); err != nil {
		writeError(w, 403, "需要管理员身份")
		return
	}
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if decodeJSON(w, r, &body) != nil {
		writeError(w, 400, "请求格式无效")
		return
	}
	tag, err := s.DB.Exec(r.Context(), `UPDATE documents SET enabled=$2,updated_at=now() WHERE id=$1 AND NOT deleted`, r.PathValue("id"), body.Enabled)
	if err != nil {
		writeError(w, 500, "更新文档失败")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, 404, "文档不存在")
		return
	}
	doc, err := s.document(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, 500, "读取文档失败")
		return
	}
	writeJSON(w, 200, doc)
}
func (s *Server) deleteDocument(w http.ResponseWriter, r *http.Request) {
	if _, err := s.require(r, "admin"); err != nil {
		writeError(w, 403, "需要管理员身份")
		return
	}
	tag, err := s.DB.Exec(r.Context(), `UPDATE documents SET deleted=true,enabled=false,updated_at=now() WHERE id=$1 AND NOT deleted`, r.PathValue("id"))
	if err != nil {
		writeError(w, 500, "删除文档失败")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, 404, "文档不存在")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (s *Server) retryDocument(w http.ResponseWriter, r *http.Request) {
	if _, err := s.require(r, "admin"); err != nil {
		writeError(w, 403, "需要管理员身份")
		return
	}
	ctx := r.Context()
	id := r.PathValue("id")
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		writeError(w, 500, "无法重试")
		return
	}
	defer tx.Rollback(ctx)
	var found string
	if tx.QueryRow(ctx, `SELECT id FROM documents WHERE id=$1 AND NOT deleted FOR UPDATE`, id).Scan(&found) != nil {
		writeError(w, 404, "文档不存在")
		return
	}
	var version, status, fingerprint string
	err = tx.QueryRow(ctx, `SELECT id,status,embedding_fingerprint FROM document_versions WHERE document_id=$1 ORDER BY created_at DESC,id DESC LIMIT 1`, id).Scan(&version, &status, &fingerprint)
	if err != nil {
		writeError(w, 404, "文档版本不存在")
		return
	}
	if status == "processing" {
		writeError(w, 409, "文档正在处理")
		return
	}
	if status == "ready" {
		if fingerprint == s.embeddingFingerprint() {
			writeError(w, 409, "文档已就绪，无需重试")
			return
		}
		_, err = tx.Exec(ctx, `INSERT INTO document_versions(document_id,filename,path,mime,content_hash) SELECT document_id,filename,path,mime,content_hash FROM document_versions WHERE id=$1`, version)
	} else {
		_, err = tx.Exec(ctx, `UPDATE document_versions SET status='processing',error='',lease_owner='',lease_until=NULL,lease_generation=lease_generation+1,attempts=0,updated_at=now() WHERE id=$1`, version)
	}
	if err != nil || tx.Commit(ctx) != nil {
		writeError(w, 500, "重试失败")
		return
	}
	doc, err := s.document(ctx, id)
	if err != nil {
		writeError(w, 500, "读取文档失败")
		return
	}
	writeJSON(w, 200, doc)
}
func (s *Server) previewSearch(w http.ResponseWriter, r *http.Request) {
	if _, err := s.require(r, "admin"); err != nil {
		writeError(w, 403, "需要管理员身份")
		return
	}
	var body struct {
		Query string `json:"query"`
	}
	if decodeJSON(w, r, &body) != nil || strings.TrimSpace(body.Query) == "" || len([]rune(body.Query)) > 1500 {
		writeError(w, 400, "请输入 1 至 1500 字的检索问题")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*s.modelTimeout())
	defer cancel()
	items, err := s.searchKnowledge(ctx, body.Query, "")
	if err != nil {
		writeError(w, 503, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}
func (s *Server) searchKnowledge(ctx context.Context, query, conversationID string) ([]Citation, error) {
	fingerprint := s.embeddingFingerprint()
	var mismatch bool
	var count int
	err := s.DB.QueryRow(ctx, `SELECT count(*),COALESCE(bool_or(v.embedding_fingerprint<>$1),false) FROM documents d JOIN document_versions v ON v.id=d.active_version_id WHERE d.enabled AND NOT d.deleted AND v.status='ready'`, fingerprint).Scan(&count, &mismatch)
	if err != nil {
		return nil, errors.New("暂时无法读取知识库")
	}
	if mismatch {
		return nil, errors.New("向量模型配置已变化，请重建现有文档索引")
	}
	if count == 0 {
		return []Citation{}, nil
	}
	vectors, err := s.modelProvider("embedding", conversationID).embed(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	rows, err := s.DB.Query(ctx, `SELECT c.id,c.document_id,d.name,c.page,c.section,c.content,1-(c.embedding <=> $1::vector) AS score FROM chunks c JOIN documents d ON d.id=c.document_id JOIN document_versions v ON v.id=c.version_id WHERE d.enabled AND NOT d.deleted AND d.active_version_id=c.version_id AND v.status='ready' AND v.embedding_fingerprint=$2 AND (1-(c.embedding <=> $1::vector)) >= $3 ORDER BY c.embedding <=> $1::vector LIMIT 5`, vectorLiteral(vectors[0]), fingerprint, s.Config.SearchMinScore)
	if err != nil {
		return nil, errors.New("知识库检索失败")
	}
	defer rows.Close()
	items := []Citation{}
	for rows.Next() {
		var c Citation
		if rows.Scan(&c.ID, &c.DocumentID, &c.Title, &c.Page, &c.Section, &c.Text, &c.Score) != nil {
			return nil, errors.New("知识库片段读取失败")
		}
		c.ChunkID = c.ID
		items = append(items, c)
	}
	if rows.Err() != nil {
		return nil, errors.New("知识库检索中断")
	}
	return items, nil
}
