package platform

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Addr, DatabaseURL, RedisURL, InstanceID, UploadDir, StaticDir string
	AllowedOrigins                                                []string
	CookieSecure                                                  bool
	SessionTTL, VisitorTTL                                        time.Duration
	ChatBaseURL, ChatAPIKey, ChatModel                            string
	EmbeddingBaseURL, EmbeddingAPIKey, EmbeddingModel             string
	EmbeddingDimensions                                           int
	ModelTimeout, WorkerInterval                                  time.Duration
	MaxToolCalls                                                  int
	SearchMinScore                                                float64
	AdminEmail, AdminPassword, AgentEmail, AgentPassword          string
	SeedDemo                                                      bool
}

func (c Config) AIEnabled() bool { return c.ChatAPIKey != "" && c.EmbeddingAPIKey != "" }

func LoadConfig() (Config, error) {
	get := func(key, fallback string) string {
		if v := os.Getenv(key); v != "" {
			return v
		}
		return fallback
	}
	timeout, err := time.ParseDuration(get("MODEL_TIMEOUT", "45s"))
	if err != nil {
		return Config{}, fmt.Errorf("MODEL_TIMEOUT: %w", err)
	}
	score, err := strconv.ParseFloat(get("SEARCH_MIN_SCORE", "0.25"), 64)
	if err != nil || score < 0 || score > 1 {
		return Config{}, fmt.Errorf("SEARCH_MIN_SCORE must be between 0 and 1")
	}
	c := Config{Addr: get("APP_ADDR", ":8090"), DatabaseURL: os.Getenv("DATABASE_URL"), RedisURL: get("REDIS_URL", "redis://127.0.0.1:6379/0"), InstanceID: get("INSTANCE_ID", "local"), UploadDir: get("UPLOAD_DIR", ".local/uploads"), StaticDir: get("STATIC_DIR", "frontend/dist"), AllowedOrigins: strings.Split(get("APP_ORIGINS", "http://localhost:8090,http://127.0.0.1:8090,http://localhost:5173,http://127.0.0.1:5173"), ","), CookieSecure: get("COOKIE_SECURE", "false") == "true", SessionTTL: 8 * time.Hour, VisitorTTL: 30 * 24 * time.Hour, ChatBaseURL: get("CHAT_BASE_URL", "https://dashscope.aliyuncs.com/compatible-mode/v1"), ChatAPIKey: os.Getenv("CHAT_API_KEY"), ChatModel: get("CHAT_MODEL", "qwen3-vl-plus"), EmbeddingBaseURL: get("EMBEDDING_BASE_URL", "https://dashscope.aliyuncs.com/compatible-mode/v1"), EmbeddingAPIKey: os.Getenv("EMBEDDING_API_KEY"), EmbeddingModel: get("EMBEDDING_MODEL", "text-embedding-v4"), EmbeddingDimensions: 1024, ModelTimeout: timeout, WorkerInterval: time.Second, MaxToolCalls: 3, SearchMinScore: score, AdminEmail: get("ADMIN_EMAIL", "admin@luma.local"), AdminPassword: os.Getenv("ADMIN_PASSWORD"), AgentEmail: get("AGENT_EMAIL", "agent@luma.local"), AgentPassword: os.Getenv("AGENT_PASSWORD"), SeedDemo: get("SEED_DEMO", "false") == "true"}
	if c.DatabaseURL == "" {
		return c, fmt.Errorf("DATABASE_URL is required; see .env.example and README")
	}
	if c.ModelTimeout < time.Second || c.ModelTimeout > 2*time.Minute {
		return c, fmt.Errorf("MODEL_TIMEOUT must be 1s..2m")
	}
	for i := range c.AllowedOrigins {
		c.AllowedOrigins[i] = strings.TrimSpace(c.AllowedOrigins[i])
	}
	return c, nil
}
