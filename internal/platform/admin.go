package platform

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/bcrypt"
)

func (s *Server) registerAdmin(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/staff", s.handleStaff)
	mux.HandleFunc("POST /api/staff", s.handleCreateStaff)
	mux.HandleFunc("PATCH /api/staff/{id}", s.handleUpdateStaff)
	mux.HandleFunc("GET /api/stats", s.handleStats)
	mux.HandleFunc("GET /api/ai-calls", s.handleAICalls)
}

func (s *Server) handleStaff(w http.ResponseWriter, r *http.Request) {
	if _, err := s.require(r, "admin"); err != nil {
		writeError(w, 403, "需要管理员身份")
		return
	}
	rows, err := s.DB.Query(r.Context(), `SELECT `+actorColumns+` FROM actors a WHERE a.role IN ('agent','admin') ORDER BY a.created_at,a.id`)
	if err != nil {
		sendError(w, err)
		return
	}
	defer rows.Close()
	items := make([]Actor, 0)
	for rows.Next() {
		a, e := scanActor(rows)
		if e != nil {
			sendError(w, e)
			return
		}
		items = append(items, a)
	}
	if rows.Err() != nil {
		sendError(w, rows.Err())
		return
	}
	if s.Redis != nil && len(items) > 0 {
		ids := make([]string, len(items))
		for i, a := range items {
			ids[i] = a.ID
		}
		scores, e := s.Redis.ZMScore(r.Context(), "luma:online", ids...).Result()
		if e == nil {
			for i, score := range scores {
				items[i].Online = items[i].Enabled && score > float64(time.Now().Unix())
			}
		}
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func validateStaffCredentials(name, email, password string) error {
	if strings.TrimSpace(name) == "" || utf8.RuneCountInString(name) > 40 {
		return badRequest("姓名须为 1 到 40 字")
	}
	if !validEmail(email) {
		return badRequest("邮箱格式无效")
	}
	if len(password) < 10 || len(password) > 72 {
		return badRequest("密码须为 10 到 72 字节")
	}
	return nil
}

func (s *Server) handleCreateStaff(w http.ResponseWriter, r *http.Request) {
	admin, err := s.require(r, "admin")
	if err != nil {
		writeError(w, 403, "需要管理员身份")
		return
	}
	var input struct {
		Name     string `json:"name"`
		Email    string `json:"email"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err = decodeJSON(w, r, &input); err != nil {
		sendError(w, badRequest(err.Error()))
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	if input.Role != "agent" {
		writeError(w, 400, "此接口仅创建客服账号")
		return
	}
	if err = validateStaffCredentials(input.Name, input.Email, input.Password); err != nil {
		sendError(w, err)
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
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
	if err = checkActorTx(r.Context(), tx, admin); err != nil {
		sendError(w, err)
		return
	}
	a, err := scanActor(tx.QueryRow(r.Context(), `INSERT INTO actors(id,role,name,email,password_hash) VALUES($1,'agent',$2,$3,$4) RETURNING id,role,name,email,enabled,available,created_at`, newID(), input.Name, input.Email, string(hash)))
	if err != nil {
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23505" {
			writeError(w, 409, "该邮箱已存在")
		} else {
			sendError(w, err)
		}
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		sendError(w, err)
		return
	}
	writeJSON(w, 201, a)
}

func (s *Server) handleUpdateStaff(w http.ResponseWriter, r *http.Request) {
	admin, err := s.require(r, "admin")
	if err != nil {
		writeError(w, 403, "需要管理员身份")
		return
	}
	id := r.PathValue("id")
	if !validID(id) {
		writeError(w, 400, "客服编号无效")
		return
	}
	var input struct {
		Name     *string `json:"name"`
		Enabled  *bool   `json:"enabled"`
		Password *string `json:"password"`
	}
	if err = decodeJSON(w, r, &input); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if input.Name == nil && input.Enabled == nil && input.Password == nil {
		writeError(w, 400, "请提供要修改的字段")
		return
	}
	if input.Name != nil {
		*input.Name = strings.TrimSpace(*input.Name)
		if *input.Name == "" || utf8.RuneCountInString(*input.Name) > 40 {
			writeError(w, 400, "姓名须为 1 到 40 字")
			return
		}
	}
	var hash *string
	if input.Password != nil {
		if len(*input.Password) < 10 || len(*input.Password) > 72 {
			writeError(w, 400, "密码须为 10 到 72 字节")
			return
		}
		encoded, e := bcrypt.GenerateFromPassword([]byte(*input.Password), bcrypt.DefaultCost)
		if e != nil {
			sendError(w, e)
			return
		}
		v := string(encoded)
		hash = &v
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		sendError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if err = checkActorTx(r.Context(), tx, admin); err != nil {
		sendError(w, err)
		return
	}
	// 管理后台只管理客服，管理员由部署配置维护，避免误禁用最后一个管理员。
	a, err := scanActor(tx.QueryRow(r.Context(), `UPDATE actors a SET name=COALESCE($2,name),enabled=COALESCE($3,enabled),password_hash=COALESCE($4,password_hash),available=CASE WHEN $3=false THEN false ELSE available END WHERE id=$1 AND role='agent' RETURNING `+actorColumns, id, input.Name, input.Enabled, hash))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, 404, "客服账号不存在或为管理员账号")
		} else {
			sendError(w, err)
		}
		return
	}
	if (input.Enabled != nil && !*input.Enabled) || input.Password != nil {
		if _, err = tx.Exec(r.Context(), `DELETE FROM sessions WHERE actor_id=$1`, id); err != nil {
			sendError(w, err)
			return
		}
	}
	if err = tx.Commit(r.Context()); err != nil {
		sendError(w, err)
		return
	}
	if !a.Enabled {
		if s.Redis != nil {
			_ = s.Redis.ZRem(r.Context(), "luma:online", id).Err()
		}
		s.disconnectActor(id)
	}
	if input.Password != nil {
		s.disconnectActor(id)
	}
	writeJSON(w, 200, a)
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	if _, err := s.require(r, "admin"); err != nil {
		writeError(w, 403, "需要管理员身份")
		return
	}
	// 每个平均值使用同一个 handoff 周期，忽略尚未接单/回应的记录。
	var raw []byte
	err := s.DB.QueryRow(r.Context(), `SELECT jsonb_build_object(
	 'conversations',(SELECT count(*) FROM conversations),
	 'waiting',(SELECT count(*) FROM conversations WHERE status='waiting'),
	 'human',(SELECT count(*) FROM conversations WHERE status='human'),
	 'closed',(SELECT count(*) FROM conversations WHERE status='closed'),
	 'handoffs',(SELECT count(*) FROM handoffs),
	 'ai_solved',(SELECT count(*) FROM conversations c WHERE feedback='solved' AND NOT handed_off AND EXISTS(SELECT 1 FROM messages m WHERE m.conversation_id=c.id AND m.sender_role='ai')),
	 'solved',(SELECT count(*) FROM conversations WHERE feedback='solved'),
	 'unsolved',(SELECT count(*) FROM conversations WHERE feedback='unsolved'),
	 'avg_wait_seconds',(SELECT COALESCE(avg(EXTRACT(EPOCH FROM claimed_at-created_at)),0) FROM handoffs WHERE claimed_at IS NOT NULL),
	 'avg_first_response_seconds',(SELECT COALESCE(avg(EXTRACT(EPOCH FROM first_response_at-claimed_at)),0) FROM handoffs WHERE claimed_at IS NOT NULL AND first_response_at IS NOT NULL),
	 'model_calls',(SELECT count(*) FROM ai_calls),
	 'total_tokens',(SELECT COALESCE(sum(input_tokens+output_tokens),0) FROM ai_calls))`).Scan(&raw)
	if err != nil {
		sendError(w, err)
		return
	}
	writeJSON(w, 200, json.RawMessage(raw))
}

func (s *Server) handleAICalls(w http.ResponseWriter, r *http.Request) {
	if _, err := s.require(r, "admin"); err != nil {
		writeError(w, 403, "需要管理员身份")
		return
	}
	limit, err := parseNonnegative(r.URL.Query().Get("limit"), 50)
	if err != nil || limit < 1 || limit > 200 {
		writeError(w, 400, "记录数量须为 1 到 200")
		return
	}
	rows, err := s.DB.Query(r.Context(), `SELECT to_jsonb(a) FROM ai_calls a ORDER BY created_at DESC,id DESC LIMIT $1`, limit)
	if err != nil {
		sendError(w, err)
		return
	}
	defer rows.Close()
	items := make([]json.RawMessage, 0)
	for rows.Next() {
		var value json.RawMessage
		if err = rows.Scan(&value); err != nil {
			sendError(w, err)
			return
		}
		items = append(items, value)
	}
	if rows.Err() != nil {
		sendError(w, rows.Err())
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}
