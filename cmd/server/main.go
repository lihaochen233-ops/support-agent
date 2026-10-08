package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"support-agent/internal/platform"
)

func main() {
	if err := run(); err != nil {
		slog.Error("服务启动失败", "error", err)
		os.Exit(1)
	}
}

func run() error {
	envFile := flag.String("env", ".env", "optional local environment file")
	initOnly := flag.Bool("init", false, "apply migrations and seed missing accounts, then exit")
	check := flag.Bool("check", false, "check the local HTTP health endpoint, then exit")
	flag.Parse()
	if err := loadEnv(*envFile); err != nil {
		return err
	}
	if *check {
		addr := os.Getenv("APP_ADDR")
		if addr == "" {
			addr = ":8090"
		}
		if strings.HasPrefix(addr, ":") {
			addr = "127.0.0.1" + addr
		}
		addr = strings.Replace(addr, "0.0.0.0:", "127.0.0.1:", 1)
		client := http.Client{Timeout: 3 * time.Second}
		resp, err := client.Get("http://" + addr + "/api/health")
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return fmt.Errorf("health status %d", resp.StatusCode)
		}
		return nil
	}
	cfg, err := platform.LoadConfig()
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return errors.New("DATABASE_URL 格式错误")
	}
	poolCfg.MaxConns = 16
	poolCfg.MinConns = 1
	poolCfg.ConnConfig.ConnectTimeout = 5 * time.Second
	db, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return err
	}
	defer db.Close()
	startup, startCancel := context.WithTimeout(ctx, 45*time.Second)
	defer startCancel()
	if err = db.Ping(startup); err != nil {
		return fmt.Errorf("连接 PostgreSQL 失败: %w", err)
	}
	if err = platform.Migrate(startup, db); err != nil {
		return err
	}
	if err = platform.SeedAccounts(startup, db, cfg); err != nil {
		return err
	}
	if *initOnly {
		slog.Info("数据库迁移与账号初始化完成")
		return nil
	}
	redisOptions, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return errors.New("REDIS_URL 格式错误")
	}
	redisOptions.DialTimeout = 2 * time.Second
	redisOptions.ReadTimeout = 2 * time.Second
	redisOptions.WriteTimeout = 2 * time.Second
	redisOptions.MaxRetries = 1
	rdb := redis.NewClient(redisOptions)
	defer rdb.Close()
	if err = rdb.Ping(startup).Err(); err != nil {
		slog.Warn("Redis 不可用，跨实例实时通知降级为数据库补拉")
	}
	if err = os.MkdirAll(cfg.UploadDir, 0700); err != nil {
		return err
	}
	app := platform.NewServer(db, rdb, cfg)
	app.Start(ctx)
	server := http.Server{Addr: cfg.Addr, Handler: app.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 90 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 16 << 10}
	errCh := make(chan error, 1)
	go func() { errCh <- server.ListenAndServe() }()
	slog.Info("Luma 智能客服已启动", "address", cfg.Addr, "instance", cfg.InstanceID, "ai_enabled", cfg.AIEnabled())
	select {
	case err = <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
	}
	cancel()
	shutdown, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	return server.Shutdown(shutdown)
}

func loadEnv(path string) error {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "\ufeff"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("环境文件中的配置缺少等号")
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
			value = value[1 : len(value)-1]
		}
		if _, exists := os.LookupEnv(key); !exists {
			if err = os.Setenv(key, value); err != nil {
				return err
			}
		}
	}
	return scanner.Err()
}
