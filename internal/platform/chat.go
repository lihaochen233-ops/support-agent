package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

type httpError struct {
	status  int
	message string
}

func (e *httpError) Error() string    { return e.message }
func badRequest(message string) error { return &httpError{400, message} }
func forbidden(message string) error  { return &httpError{403, message} }
func conflict(message string) error   { return &httpError{409, message} }
func publicError(err error) string {
	var h *httpError
	if errors.As(err, &h) {
		return h.message
	}
	return "操作暂时失败，请稍后重试"
}
func sendError(w http.ResponseWriter, err error) {
	var h *httpError
	if errors.As(err, &h) {
		writeError(w, h.status, h.message)
		return
	}
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "记录不存在")
		return
	}
	writeError(w, 500, "操作暂时失败，请稍后重试")
}

// 以 JSON 读取共享模型，新增展示字段不会改变事务中的扫描顺序。
func scanConversation(row pgx.Row) (Conversation, error) {
	var raw []byte
	err := row.Scan(&raw)
	if err != nil {
		return Conversation{}, err
	}
	var c Conversation
	err = json.Unmarshal(raw, &c)
	return c, err
}
func scanMessage(row pgx.Row) (Message, error) {
	var raw []byte
	err := row.Scan(&raw)
	if err != nil {
		return Message{}, err
	}
	var m Message
	err = json.Unmarshal(raw, &m)
	return m, err
}

func (s *Server) conversation(ctx context.Context, id string) (Conversation, error) {
	if !validID(id) {
		return Conversation{}, badRequest("会话编号无效")
	}
	return scanConversation(s.DB.QueryRow(ctx, `SELECT to_jsonb(c) || jsonb_build_object('visitor_name',v.name,'agent_name',COALESCE(a.name,'')) FROM conversations c JOIN actors v ON v.id=c.visitor_id LEFT JOIN actors a ON a.id=c.agent_id WHERE c.id=$1`, id))
}
func (s *Server) canRead(ctx context.Context, a Actor, c Conversation) bool {
	return a.Role == "admin" || (a.Role == "visitor" && c.VisitorID == a.ID) || (a.Role == "agent" && c.AgentID == a.ID)
}

func (s *Server) registerChat(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/conversations", s.handleConversations)
	mux.HandleFunc("POST /api/conversations", s.handleCreateConversation)
	mux.HandleFunc("GET /api/conversations/{id}", s.handleConversation)
	mux.HandleFunc("GET /api/conversations/{id}/messages", s.handleMessages)
	mux.HandleFunc("POST /api/conversations/{id}/messages", s.handleSendMessage)
	for _, action := range []string{"handoff", "claim", "release", "assign", "close"} {
		mux.HandleFunc("POST /api/conversations/{id}/"+action, s.handleTransition(action))
	}
	mux.HandleFunc("POST /api/conversations/{id}/feedback", s.handleFeedback)
	mux.HandleFunc("POST /api/conversations/{id}/read", s.handleRead)
}

func (s *Server) handleConversations(w http.ResponseWriter, r *http.Request) {
	a, err := s.actor(r)
	if err != nil {
		writeError(w, 401, "请先登录")
		return
	}
	scope := r.URL.Query().Get("scope")
	if scope == "" {
		scope = "mine"
	}
	if scope != "mine" && scope != "waiting" && scope != "history" && scope != "all" {
		writeError(w, 400, "会话范围无效")
		return
	}
	args := []any{a.ID}
	filters := []string{"true"}
	if a.Role == "visitor" {
		filters = append(filters, "c.visitor_id=$1")
	} else if a.Role == "agent" {
		if scope == "waiting" {
			filters = append(filters, "c.status='waiting'")
		} else {
			filters = append(filters, "c.agent_id=$1")
		}
	}
	if scope == "waiting" {
		filters = append(filters, "c.status='waiting'")
	} else if scope == "history" {
		filters = append(filters, "c.status='closed'")
	} else if scope == "mine" {
		filters = append(filters, "c.status<>'closed'")
		if a.Role == "admin" {
			filters = append(filters, "c.agent_id=$1")
		}
	}
	if status := r.URL.Query().Get("status"); status != "" {
		if status != "ai" && status != "waiting" && status != "human" && status != "closed" {
			writeError(w, 400, "会话状态无效")
			return
		}
		args = append(args, status)
		filters = append(filters, fmt.Sprintf("c.status=$%d", len(args)))
	}
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		args = append(args, "%"+q+"%")
		filters = append(filters, fmt.Sprintf("(v.name ILIKE $%d OR c.id::text ILIKE $%d)", len(args), len(args)))
	}
	if before := r.URL.Query().Get("before"); before != "" {
		parsed, e := time.Parse(time.RFC3339Nano, before)
		if e != nil {
			writeError(w, 400, "分页时间无效")
			return
		}
		args = append(args, parsed)
		filters = append(filters, fmt.Sprintf("c.updated_at<$%d", len(args)))
	}
	// 等待队列仅暴露摘要，完整消息接口仍要求接单后的权限。
	query := `SELECT to_jsonb(c) || jsonb_build_object('visitor_name',v.name,'agent_name',COALESCE(st.name,''),'unread_count',(SELECT count(*) FROM messages m WHERE m.conversation_id=c.id AND m.seq>COALESCE(rc.seq,0) AND (m.sender_id IS NULL OR m.sender_id<>$1))) FROM conversations c JOIN actors v ON v.id=c.visitor_id LEFT JOIN actors st ON st.id=c.agent_id LEFT JOIN read_cursors rc ON rc.conversation_id=c.id AND rc.actor_id=$1 WHERE ` + strings.Join(filters, " AND ") + ` ORDER BY c.updated_at DESC,c.id DESC LIMIT 100`
	rows, err := s.DB.Query(r.Context(), query, args...)
	if err != nil {
		sendError(w, err)
		return
	}
	defer rows.Close()
	items := make([]Conversation, 0)
	for rows.Next() {
		c, e := scanConversation(rows)
		if e != nil {
			sendError(w, e)
			return
		}
		items = append(items, c)
	}
	if rows.Err() != nil {
		sendError(w, rows.Err())
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *Server) handleCreateConversation(w http.ResponseWriter, r *http.Request) {
	a, err := s.require(r, "visitor")
	if err != nil {
		writeError(w, 403, "需要访客身份")
		return
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		sendError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	// 对访客行加锁，与部分唯一索引一起防止多标签页创建两个活动会话。
	var enabled bool
	if err = tx.QueryRow(r.Context(), `SELECT enabled FROM actors WHERE id=$1 AND role='visitor' FOR UPDATE`, a.ID).Scan(&enabled); err != nil || !enabled {
		writeError(w, 403, "访客身份已失效")
		return
	}
	c, err := scanConversation(tx.QueryRow(r.Context(), `SELECT to_jsonb(c) FROM conversations c WHERE visitor_id=$1 AND status<>'closed'`, a.ID))
	if errors.Is(err, pgx.ErrNoRows) {
		c, err = scanConversation(tx.QueryRow(r.Context(), `INSERT INTO conversations(id,visitor_id,status) VALUES($1,$2,'ai') RETURNING to_jsonb(conversations)`, newID(), a.ID))
	}
	if err != nil {
		sendError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		sendError(w, err)
		return
	}
	writeJSON(w, 200, c)
}

func (s *Server) handleConversation(w http.ResponseWriter, r *http.Request) {
	a, err := s.actor(r)
	if err != nil {
		writeError(w, 401, "请先登录")
		return
	}
	c, err := s.conversation(r.Context(), r.PathValue("id"))
	if err != nil {
		sendError(w, err)
		return
	}
	if !s.canRead(r.Context(), a, c) {
		writeError(w, 403, "无权查看会话")
		return
	}
	writeJSON(w, 200, c)
}

func parseNonnegative(raw string, defaultValue int64) (int64, error) {
	if raw == "" {
		return defaultValue, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return 0, badRequest("消息序号无效")
	}
	return n, nil
}
func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	a, err := s.actor(r)
	if err != nil {
		writeError(w, 401, "请先登录")
		return
	}
	c, err := s.conversation(r.Context(), r.PathValue("id"))
	if err != nil {
		sendError(w, err)
		return
	}
	if !s.canRead(r.Context(), a, c) {
		writeError(w, 403, "接单后才可查看消息")
		return
	}
	after, err := parseNonnegative(r.URL.Query().Get("after_seq"), 0)
	if err != nil {
		sendError(w, err)
		return
	}
	before, err := parseNonnegative(r.URL.Query().Get("before_seq"), 0)
	if err != nil {
		sendError(w, err)
		return
	}
	limit64, err := parseNonnegative(r.URL.Query().Get("limit"), 50)
	if err != nil || limit64 < 1 || limit64 > 100 {
		writeError(w, 400, "每页消息数量须为 1 到 100")
		return
	}
	limit := int(limit64)
	order := "ASC"
	predicate := "m.seq>$2"
	cursor := after
	descending := before > 0 || !r.URL.Query().Has("after_seq")
	if descending {
		order = "DESC"
	}
	if before > 0 {
		predicate = "m.seq<$2"
		cursor = before
	}
	rows, err := s.DB.Query(r.Context(), `SELECT to_jsonb(m) || jsonb_build_object('sender_name',COALESCE(a.name,CASE WHEN m.sender_role='ai' THEN 'Luma AI' ELSE '系统' END),'image_url',CASE WHEN m.image_id IS NULL THEN '' ELSE '/api/attachments/'||m.image_id::text END) FROM messages m LEFT JOIN actors a ON a.id=m.sender_id WHERE m.conversation_id=$1 AND `+predicate+` ORDER BY m.seq `+order+` LIMIT $3`, c.ID, cursor, limit+1)
	if err != nil {
		sendError(w, err)
		return
	}
	defer rows.Close()
	items := make([]Message, 0)
	for rows.Next() {
		m, e := scanMessage(rows)
		if e != nil {
			sendError(w, e)
			return
		}
		items = append(items, m)
	}
	if rows.Err() != nil {
		sendError(w, rows.Err())
		return
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	if descending {
		for l, h := 0, len(items)-1; l < h; l, h = l+1, h-1 {
			items[l], items[h] = items[h], items[l]
		}
	}
	writeJSON(w, 200, map[string]any{"items": items, "has_more": hasMore})
}

type sendMessageInput struct {
	ClientID string `json:"client_id"`
	Body     string `json:"body"`
	ImageID  string `json:"image_id"`
}

func validateMessage(input sendMessageInput) error {
	if !validID(input.ClientID) {
		return badRequest("客户端消息编号必须为 UUID")
	}
	if utf8.RuneCountInString(input.Body) > 4000 {
		return badRequest("消息不能超过 4000 字")
	}
	if strings.TrimSpace(input.Body) == "" && input.ImageID == "" {
		return badRequest("请填写消息或上传图片")
	}
	if input.ImageID != "" && !validID(input.ImageID) {
		return badRequest("图片编号无效")
	}
	return nil
}
func explicitHandoff(body string) bool {
	normalized := strings.ToLower(strings.TrimSpace(body))
	for _, negative := range []string{"不要转人工", "不用转人工", "不需要人工", "不想转人工", "不找人工", "不转人工", "don't transfer", "do not transfer"} {
		if strings.Contains(normalized, negative) {
			return false
		}
	}
	if normalized == "人工客服" || normalized == "人工服务" || normalized == "真人客服" {
		return true
	}
	for _, phrase := range []string{"转人工", "找人工", "我要人工", "需要人工客服", "请人工客服", "talk to a human", "human agent"} {
		if strings.Contains(normalized, phrase) {
			return true
		}
	}
	return false
}

func (s *Server) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	a, err := s.actor(r)
	if err != nil {
		writeError(w, 401, "请先登录")
		return
	}
	var input sendMessageInput
	if err = decodeJSON(w, r, &input); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	m, err := s.sendMessage(r.Context(), a, r.PathValue("id"), input)
	if err != nil {
		sendError(w, err)
		return
	}
	writeJSON(w, 200, m)
}

func (s *Server) sendMessage(ctx context.Context, a Actor, id string, input sendMessageInput) (Message, error) {
	if !validID(id) {
		return Message{}, badRequest("会话编号无效")
	}
	if err := validateMessage(input); err != nil {
		return Message{}, err
	}
	if !s.allowRate(ctx, "send:"+a.ID, 40, time.Minute) {
		return Message{}, &httpError{429, "消息发送过于频繁"}
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return Message{}, err
	}
	defer tx.Rollback(ctx)
	if err = checkActorTx(ctx, tx, a); err != nil {
		return Message{}, err
	}
	c, err := scanConversation(tx.QueryRow(ctx, `SELECT to_jsonb(c) FROM conversations c WHERE id=$1 FOR UPDATE`, id))
	if err != nil {
		return Message{}, err
	}
	// 先检查幂等记录，再检查最新状态：响应丢失后重发，即使会话已结束也返回原结果。
	m, err := scanMessage(tx.QueryRow(ctx, `SELECT to_jsonb(m) FROM messages m WHERE conversation_id=$1 AND sender_id=$2 AND client_id=$3`, id, a.ID, input.ClientID))
	if err == nil {
		if m.ImageID != "" {
			m.ImageURL = "/api/attachments/" + m.ImageID
		}
		return m, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Message{}, err
	}
	if !s.canRead(ctx, a, c) {
		return Message{}, forbidden("无权发送消息")
	}
	if c.Status == "closed" {
		return Message{}, conflict("会话已结束，请新建咨询")
	}
	if a.Role != "visitor" && (c.Status != "human" || c.AgentID != a.ID) {
		return Message{}, forbidden("只能在自己接待的会话中发送消息")
	}
	if input.ImageID != "" {
		var exists bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM attachments WHERE id=$1 AND conversation_id=$2 AND uploader_id=$3)`, input.ImageID, id, a.ID).Scan(&exists); err != nil {
			return Message{}, err
		}
		if !exists {
			return Message{}, forbidden("图片不属于当前会话")
		}
	}
	preview := strings.TrimSpace(input.Body)
	if preview == "" {
		preview = "[图片]"
	}
	if len([]rune(preview)) > 120 {
		preview = string([]rune(preview)[:120])
	}
	var seq int64
	if err = tx.QueryRow(ctx, `UPDATE conversations SET last_seq=last_seq+1,updated_at=now(),last_message=$2,title=CASE WHEN last_seq=0 THEN left($2,40) ELSE title END WHERE id=$1 RETURNING last_seq`, id, preview).Scan(&seq); err != nil {
		return Message{}, err
	}
	m, err = scanMessage(tx.QueryRow(ctx, `INSERT INTO messages(id,conversation_id,seq,sender_id,sender_role,client_id,body,image_id,sender_name) VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8,''),$9) RETURNING to_jsonb(messages)`, newID(), id, seq, a.ID, a.Role, input.ClientID, strings.TrimSpace(input.Body), input.ImageID, a.Name))
	if err != nil {
		return Message{}, err
	}
	if m.ImageID != "" {
		m.ImageURL = "/api/attachments/" + m.ImageID
	}
	handoff := a.Role == "visitor" && c.Status == "ai" && explicitHandoff(input.Body)
	if handoff {
		if err = s.handoffTx(ctx, tx, id, "访客主动请求人工服务"); err != nil {
			return Message{}, err
		}
	} else if a.Role == "visitor" && c.Status == "ai" {
		_, err = tx.Exec(ctx, `INSERT INTO ai_jobs(id,conversation_id,status,conversation_version) VALUES($1,$2,'pending',$3) ON CONFLICT DO NOTHING`, newID(), id, c.Version)
		if err != nil {
			return Message{}, err
		}
		_, err = tx.Exec(ctx, `UPDATE conversations SET ai_phase='queued' WHERE id=$1 AND ai_phase='idle'`, id)
		if err != nil {
			return Message{}, err
		}
	}
	if a.Role != "visitor" {
		_, err = tx.Exec(ctx, `UPDATE conversations SET first_response_at=COALESCE(first_response_at,now()) WHERE id=$1`, id)
		if err != nil {
			return Message{}, err
		}
		_, err = tx.Exec(ctx, `UPDATE handoffs SET first_response_at=COALESCE(first_response_at,now()) WHERE id=(SELECT id FROM handoffs WHERE conversation_id=$1 ORDER BY created_at DESC LIMIT 1)`, id)
		if err != nil {
			return Message{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return Message{}, err
	}
	if handoff {
		s.invalidate(ctx, id)
	} else {
		s.notify(ctx, id)
	}
	return m, nil
}

func (s *Server) handoffTx(ctx context.Context, tx pgx.Tx, id, reason string, initiatedBy ...string) error {
	initiator := "visitor"
	if len(initiatedBy) > 0 {
		initiator = initiatedBy[0]
	}
	_, err := tx.Exec(ctx, `UPDATE conversations SET status='waiting',agent_id=NULL,version=version+1,waiting_at=now(),claimed_at=NULL,first_response_at=NULL,handoff_reason=$2,handed_off=true,ai_phase='idle',updated_at=now(),summary=COALESCE((SELECT left(string_agg(CASE WHEN sender_role='visitor' THEN '访客：' WHEN sender_role='ai' THEN 'AI：' ELSE '客服：' END || CASE WHEN body='' THEN '[图片]' ELSE body END,E'\n' ORDER BY seq),1800) FROM (SELECT body,seq,sender_role FROM messages WHERE conversation_id=$1 AND sender_role IN ('visitor','ai','agent','admin') ORDER BY seq DESC LIMIT 6) recent),'') WHERE id=$1`, id, reason)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO handoffs(conversation_id,reason,summary,initiated_by) SELECT id,$2,summary,$3 FROM conversations WHERE id=$1`, id, reason, initiator)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE ai_jobs SET status='cancelled',updated_at=now() WHERE conversation_id=$1 AND status IN ('pending','running')`, id)
	return err
}

func (s *Server) handleTransition(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a, err := s.actor(r)
		if err != nil {
			writeError(w, 401, "请先登录")
			return
		}
		id := r.PathValue("id")
		if !validID(id) {
			writeError(w, 400, "会话编号无效")
			return
		}
		var input struct {
			Reason  string `json:"reason"`
			AgentID string `json:"agent_id"`
		}
		if err = decodeJSON(w, r, &input); err != nil {
			writeError(w, 400, err.Error())
			return
		}
		if utf8.RuneCountInString(input.Reason) > 500 {
			writeError(w, 400, "原因不能超过 500 字")
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
		ctx := r.Context()
		switch action {
		case "handoff":
			if a.Role != "visitor" || c.VisitorID != a.ID {
				writeError(w, 403, "只有会话访客可以请求人工")
				return
			}
			if c.Status == "closed" {
				writeError(w, 409, "会话已结束")
				return
			}
			if c.Status == "ai" {
				reason := strings.TrimSpace(input.Reason)
				if reason == "" {
					reason = "访客主动请求人工服务"
				}
				err = s.handoffTx(ctx, tx, id, reason)
			}
		case "claim":
			if a.Role != "agent" && a.Role != "admin" {
				writeError(w, 403, "需要客服身份")
				return
			}
			if c.Status == "human" && c.AgentID == a.ID {
				break
			}
			if c.Status != "waiting" {
				writeError(w, 409, "会话已被接待或状态已变化")
				return
			}
			var available bool
			err = tx.QueryRow(ctx, `SELECT available AND enabled FROM actors WHERE id=$1`, a.ID).Scan(&available)
			if err != nil {
				sendError(w, err)
				return
			}
			if !available {
				writeError(w, 409, "请先切换为可接待状态")
				return
			}
			_, err = tx.Exec(ctx, `UPDATE conversations SET status='human',agent_id=$2,claimed_at=now(),updated_at=now(),version=version+1 WHERE id=$1`, id, a.ID)
		case "release":
			if a.Role != "admin" && (a.Role != "agent" || c.AgentID != a.ID) {
				writeError(w, 403, "只能释放自己接待的会话")
				return
			}
			if c.Status != "human" {
				writeError(w, 409, "会话当前没有人工接待")
				return
			}
			err = s.handoffTx(ctx, tx, id, "客服将会话退回公共队列", a.ID)
		case "assign":
			if a.Role != "admin" {
				writeError(w, 403, "需要管理员身份")
				return
			}
			if !validID(input.AgentID) {
				writeError(w, 400, "客服编号无效")
				return
			}
			if c.Status != "waiting" && c.Status != "human" {
				writeError(w, 409, "只能分配等待或人工接待中的会话")
				return
			}
			var enabled bool
			err = tx.QueryRow(ctx, `SELECT enabled FROM actors WHERE id=$1 AND role IN ('agent','admin') FOR SHARE`, input.AgentID).Scan(&enabled)
			if err != nil || !enabled {
				writeError(w, 400, "目标客服不可用")
				return
			}
			_, err = tx.Exec(ctx, `UPDATE conversations SET status='human',agent_id=$2,claimed_at=now(),updated_at=now(),version=version+1 WHERE id=$1`, id, input.AgentID)
		case "close":
			if !s.canRead(ctx, a, c) {
				writeError(w, 403, "无权结束会话")
				return
			}
			if c.Status != "closed" {
				_, err = tx.Exec(ctx, `UPDATE conversations SET status='closed',closed_at=now(),updated_at=now(),version=version+1,ai_phase='idle' WHERE id=$1`, id)
				if err == nil {
					_, err = tx.Exec(ctx, `UPDATE ai_jobs SET status='cancelled',updated_at=now() WHERE conversation_id=$1 AND status IN ('pending','running')`, id)
				}
			}
		}
		if err == nil && (action == "claim" || action == "assign") {
			assignee := a.ID
			if action == "assign" {
				assignee = input.AgentID
			}
			_, err = tx.Exec(ctx, `UPDATE handoffs SET agent_id=$2,claimed_at=COALESCE(claimed_at,now()) WHERE id=(SELECT id FROM handoffs WHERE conversation_id=$1 ORDER BY created_at DESC,id DESC LIMIT 1)`, id, assignee)
		}
		if err != nil {
			sendError(w, err)
			return
		}
		c, err = scanConversation(tx.QueryRow(ctx, `SELECT to_jsonb(c) FROM conversations c WHERE id=$1`, id))
		if err != nil {
			sendError(w, err)
			return
		}
		if err = tx.Commit(ctx); err != nil {
			sendError(w, err)
			return
		}
		s.invalidate(ctx, id)
		writeJSON(w, 200, c)
	}
}

func (s *Server) handleFeedback(w http.ResponseWriter, r *http.Request) {
	a, err := s.require(r, "visitor")
	if err != nil {
		writeError(w, 403, "需要访客身份")
		return
	}
	var input struct {
		Value string `json:"value"`
	}
	if decodeJSON(w, r, &input) != nil || (input.Value != "solved" && input.Value != "unsolved") {
		writeError(w, 400, "评价须为 solved 或 unsolved")
		return
	}
	id := r.PathValue("id")
	if !validID(id) {
		writeError(w, 400, "会话编号无效")
		return
	}
	tag, err := s.DB.Exec(r.Context(), `UPDATE conversations SET feedback=$3 WHERE id=$1 AND visitor_id=$2 AND status='closed'`, id, a.ID, input.Value)
	if err != nil {
		sendError(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, 409, "请先结束自己的会话再评价")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleRead(w http.ResponseWriter, r *http.Request) {
	a, err := s.actor(r)
	if err != nil {
		writeError(w, 401, "请先登录")
		return
	}
	var input struct {
		Seq int64 `json:"seq"`
	}
	if decodeJSON(w, r, &input) != nil || input.Seq < 0 {
		writeError(w, 400, "消息序号无效")
		return
	}
	c, err := s.conversation(r.Context(), r.PathValue("id"))
	if err != nil {
		sendError(w, err)
		return
	}
	if !s.canRead(r.Context(), a, c) {
		writeError(w, 403, "无权查看会话")
		return
	}
	if input.Seq > c.LastSeq {
		writeError(w, 400, "已读序号超过最新消息")
		return
	}
	_, err = s.DB.Exec(r.Context(), `INSERT INTO read_cursors(conversation_id,actor_id,seq) VALUES($1,$2,$3) ON CONFLICT(conversation_id,actor_id) DO UPDATE SET seq=GREATEST(read_cursors.seq,EXCLUDED.seq)`, c.ID, a.ID, input.Seq)
	if err != nil {
		sendError(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
