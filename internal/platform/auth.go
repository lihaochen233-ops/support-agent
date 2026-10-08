package platform

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
)

var idPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func validID(id string) bool { return idPattern.MatchString(id) }
func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (s *Server) validOrigin(r *http.Request, required bool) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return !required
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	for _, allowed := range s.Config.AllowedOrigins {
		if strings.EqualFold(origin, strings.TrimRight(allowed, "/")) {
			return true
		}
	}
	return false
}

func surface(r *http.Request) string {
	if r.Header.Get("X-Surface") == "visitor" || r.URL.Query().Get("surface") == "visitor" {
		return "visitor"
	}
	return "staff"
}
func cookieName(r *http.Request) string {
	if surface(r) == "visitor" {
		return "assist_visitor"
	}
	return "assist_staff"
}

const actorColumns = `a.id::text,a.role,a.name,COALESCE(a.email,''),a.enabled,a.available,a.created_at`

func scanActor(row pgx.Row) (Actor, error) {
	var a Actor
	err := row.Scan(&a.ID, &a.Role, &a.Name, &a.Email, &a.Enabled, &a.Available, &a.CreatedAt)
	return a, err
}

func (s *Server) actor(r *http.Request) (Actor, error) {
	cookie, err := r.Cookie(cookieName(r))
	if err != nil || len(cookie.Value) < 32 {
		return Actor{}, errors.New("未登录")
	}
	a, err := scanActor(s.DB.QueryRow(r.Context(), `SELECT `+actorColumns+` FROM sessions ss JOIN actors a ON a.id=ss.actor_id WHERE ss.token_hash=$1 AND ss.expires_at>now() AND a.enabled=true`, hashToken(cookie.Value)))
	if err != nil {
		return Actor{}, errors.New("登录已失效")
	}
	if (surface(r) == "visitor") != (a.Role == "visitor") {
		return Actor{}, errors.New("身份不匹配")
	}
	return a, nil
}

func (s *Server) require(r *http.Request, roles ...string) (Actor, error) {
	a, err := s.actor(r)
	if err != nil {
		return a, err
	}
	for _, role := range roles {
		if role == a.Role {
			return a, nil
		}
	}
	return Actor{}, errors.New("没有操作权限")
}

// Mutations recheck enabled/role under a shared actor lock so disabling an account
// cannot commit between the authorization check and the business mutation.
func checkActorTx(ctx context.Context, tx pgx.Tx, a Actor) error {
	var enabled bool
	var role string
	if err := tx.QueryRow(ctx, `SELECT enabled,role FROM actors WHERE id=$1 FOR SHARE`, a.ID).Scan(&enabled, &role); err != nil {
		return forbidden("登录身份已失效")
	}
	if !enabled || role != a.Role {
		return forbidden("登录身份已失效")
	}
	return nil
}

func (s *Server) createSession(ctx context.Context, w http.ResponseWriter, a Actor) error {
	var data [32]byte
	if _, err := rand.Read(data[:]); err != nil {
		return err
	}
	token := base64.RawURLEncoding.EncodeToString(data[:])
	ttl := s.Config.SessionTTL
	name := "assist_staff"
	if a.Role == "visitor" {
		ttl = s.Config.VisitorTTL
		name = "assist_visitor"
	}
	if ttl <= 0 {
		ttl = 8 * time.Hour
	}
	_, err := s.DB.Exec(ctx, `INSERT INTO sessions(token_hash,actor_id,expires_at) VALUES($1,$2,$3)`, hashToken(token), a.ID, time.Now().Add(ttl))
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: name, Value: token, Path: "/", HttpOnly: true, Secure: s.Config.CookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: int(ttl.Seconds())})
	return nil
}

func (s *Server) registerAuth(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/bootstrap", func(w http.ResponseWriter, r *http.Request) {
		var online int
		if s.Redis != nil {
			ids, err := s.Redis.ZRangeByScore(r.Context(), "luma:online", &redis.ZRangeBy{Min: fmt.Sprint(time.Now().Unix()), Max: "+inf"}).Result()
			if err == nil && len(ids) > 0 {
				_ = s.DB.QueryRow(r.Context(), `SELECT count(*) FROM actors WHERE id=ANY($1::text[]) AND enabled AND available AND role IN ('agent','admin')`, ids).Scan(&online)
			}
		}
		writeJSON(w, 200, map[string]any{"ai_enabled": s.Config.AIEnabled(), "instance_id": s.Config.InstanceID, "staff_online": online})
	})
	mux.HandleFunc("GET /api/me", func(w http.ResponseWriter, r *http.Request) {
		a, err := s.actor(r)
		if err != nil {
			writeError(w, 401, "请先登录")
			return
		}
		writeJSON(w, 200, a)
	})
	mux.HandleFunc("POST /api/visitor", s.handleVisitor)
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("POST /api/logout", s.handleLogout)
	mux.HandleFunc("POST /api/presence", s.handlePresence)
}

func (s *Server) handleVisitor(w http.ResponseWriter, r *http.Request) {
	r.Header.Set("X-Surface", "visitor")
	if a, err := s.actor(r); err == nil {
		writeJSON(w, 200, a)
		return
	}
	var input struct {
		Name string `json:"name"`
	}
	if decodeJSON(w, r, &input) != nil {
		writeError(w, 400, "请求格式无效")
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		input.Name = "访客"
	}
	if utf8.RuneCountInString(input.Name) > 40 {
		writeError(w, 400, "名称不能超过 40 字")
		return
	}
	a, err := scanActor(s.DB.QueryRow(r.Context(), `INSERT INTO actors(id,role,name,enabled,available) VALUES($1,'visitor',$2,true,false) RETURNING id::text,role,name,COALESCE(email,''),enabled,available,created_at`, newID(), input.Name))
	if err != nil {
		writeError(w, 500, "创建访客失败")
		return
	}
	if err = s.createSession(r.Context(), w, a); err != nil {
		writeError(w, 500, "创建会话失败")
		return
	}
	writeJSON(w, 201, a)
}

func validEmail(email string) bool {
	parsed, err := mail.ParseAddress(email)
	return err == nil && parsed.Address == email && len(email) <= 254
}
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if decodeJSON(w, r, &input) != nil {
		writeError(w, 400, "请求格式无效")
		return
	}
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	if !validEmail(input.Email) || len(input.Password) > 72 {
		writeError(w, 401, "邮箱或密码错误")
		return
	}
	if !s.allowRate(r.Context(), "login:"+input.Email, 10, time.Minute) {
		writeError(w, 429, "尝试过于频繁，请稍后重试")
		return
	}
	var a Actor
	var passwordHash string
	err := s.DB.QueryRow(r.Context(), `SELECT `+actorColumns+`,a.password_hash FROM actors a WHERE lower(a.email)=$1 AND a.role IN ('agent','admin')`, input.Email).Scan(&a.ID, &a.Role, &a.Name, &a.Email, &a.Enabled, &a.Available, &a.CreatedAt, &passwordHash)
	if err != nil || !a.Enabled || bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(input.Password)) != nil {
		writeError(w, 401, "邮箱或密码错误")
		return
	}
	if err = s.createSession(r.Context(), w, a); err != nil {
		writeError(w, 500, "登录失败")
		return
	}
	writeJSON(w, 200, a)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(cookieName(r)); err == nil {
		tokenHash := hashToken(cookie.Value)
		_, _ = s.DB.Exec(r.Context(), `DELETE FROM sessions WHERE token_hash=$1`, tokenHash)
		s.disconnectSession(tokenHash)
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName(r), Path: "/", Value: "", MaxAge: -1, HttpOnly: true, Secure: s.Config.CookieSecure, SameSite: http.SameSiteLaxMode})
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handlePresence(w http.ResponseWriter, r *http.Request) {
	a, err := s.require(r, "agent", "admin")
	if err != nil {
		writeError(w, 403, "需要客服身份")
		return
	}
	var input struct {
		Available bool `json:"available"`
	}
	if decodeJSON(w, r, &input) != nil {
		writeError(w, 400, "请求格式无效")
		return
	}
	a, err = scanActor(s.DB.QueryRow(r.Context(), `UPDATE actors a SET available=$2 WHERE id=$1 AND enabled RETURNING `+actorColumns, a.ID, input.Available))
	if err != nil {
		writeError(w, 500, "更新状态失败")
		return
	}
	s.touchPresence(r.Context(), a)
	a.Online = true
	writeJSON(w, 200, a)
}

func (s *Server) touchPresence(ctx context.Context, a Actor) {
	if s.Redis == nil || a.Role == "visitor" {
		return
	}
	// ZSET 的分数就是租约截止时间；旧连接退出不删除新连接的在线状态。
	_, _ = s.Redis.ZAdd(ctx, "luma:online", redis.Z{Member: a.ID, Score: float64(time.Now().Add(65 * time.Second).Unix())}).Result()
	_, _ = s.Redis.ZRemRangeByScore(ctx, "luma:online", "-inf", fmt.Sprint(time.Now().Unix())).Result()
}

func (s *Server) allowRate(ctx context.Context, key string, limit int, window time.Duration) bool {
	if s.Redis == nil {
		return true
	}
	value, err := s.Redis.Eval(ctx, `local n=redis.call('INCR',KEYS[1]); if n==1 then redis.call('EXPIRE',KEYS[1],ARGV[1]) end; return n`, []string{"luma:rate:" + key}, int(window.Seconds())).Int()
	return err != nil || value <= limit
}
