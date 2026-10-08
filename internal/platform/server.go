package platform

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type Server struct {
	DB        *pgxpool.Pool
	Redis     *redis.Client
	Config    Config
	clientsMu sync.RWMutex
	clients   map[*socketClient]struct{}
}

func NewServer(db *pgxpool.Pool, redisClient *redis.Client, cfg Config) *Server {
	return &Server{DB: db, Redis: redisClient, Config: cfg, clients: make(map[*socketClient]struct{})}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.registerAuth(mux)
	s.registerChat(mux)
	s.registerMedia(mux)
	s.registerAdmin(mux)
	s.registerKnowledge(mux)
	mux.HandleFunc("GET /api/ws", s.handleWS)
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /", s.serveSPA)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		if isMutation(r.Method) && (!s.validOrigin(r, false) || r.Header.Get("X-Requested-With") != "support-agent") {
			writeError(w, http.StatusForbidden, "请求来源校验失败")
			return
		}
		defer func() {
			if err := recover(); err != nil {
				slog.Error("request panic", "error", err)
				writeError(w, 500, "服务器暂时无法处理请求")
			}
		}()
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) Start(ctx context.Context) {
	go s.subscribe(ctx)
	s.startAIWorkers(ctx)
}

func isMutation(method string) bool {
	return method == "POST" || method == "PATCH" || method == "PUT" || method == "DELETE"
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, value any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(value); err != nil {
		return errors.New("请求 JSON 格式无效")
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return errors.New("请求只能包含一个 JSON 对象")
	}
	return nil
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	dbOK := s.DB.Ping(ctx) == nil
	redisOK := s.Redis != nil && s.Redis.Ping(ctx).Err() == nil
	status := http.StatusOK
	if !dbOK {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, map[string]any{"ok": dbOK, "db": dbOK, "redis": redisOK, "instance_id": s.Config.InstanceID})
}

func (s *Server) serveSPA(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeError(w, 404, "接口不存在")
		return
	}
	clean := filepath.Clean(filepath.FromSlash("/" + r.URL.Path))
	path := filepath.Join(s.Config.StaticDir, strings.TrimLeft(clean, `/\`))
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		http.ServeFile(w, r, path)
		return
	}
	http.ServeFile(w, r, filepath.Join(s.Config.StaticDir, "index.html"))
}
