package platform

import (
	"context"
	"crypto/sha256"
	"embed"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Migrate 使用事务级 advisory lock，让两个实例同时启动时只执行一份迁移。
func Migrate(ctx context.Context, db *pgxpool.Pool) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(742631890)`); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations(name text PRIMARY KEY,checksum text NOT NULL,applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	files, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name() < files[j].Name() })
	for _, f := range files {
		if !strings.HasSuffix(f.Name(), ".sql") {
			continue
		}
		data, err := migrationFiles.ReadFile("migrations/" + f.Name())
		if err != nil {
			return err
		}
		sum := fmt.Sprintf("%x", sha256.Sum256(data))
		var old string
		rows, err := tx.Query(ctx, `SELECT checksum FROM schema_migrations WHERE name=$1`, f.Name())
		if err != nil {
			return err
		}
		if rows.Next() {
			err = rows.Scan(&old)
		}
		rows.Close()
		if err != nil {
			return err
		}
		if old != "" {
			if old != sum {
				return fmt.Errorf("migration %s changed after application; add a new migration instead", f.Name())
			}
			continue
		}
		if _, err = tx.Exec(ctx, string(data)); err != nil {
			return fmt.Errorf("migration %s: %w", f.Name(), err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO schema_migrations(name,checksum) VALUES($1,$2)`, f.Name(), sum); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// SeedAccounts 只创建缺失账号，不在重启时重置密码；演示客服需要显式开启。
func SeedAccounts(ctx context.Context, db *pgxpool.Pool, cfg Config) error {
	accounts := []struct{ email, password, name, role string }{{cfg.AdminEmail, cfg.AdminPassword, "管理员", "admin"}}
	if cfg.SeedDemo {
		accounts = append(accounts, struct{ email, password, name, role string }{cfg.AgentEmail, cfg.AgentPassword, "林晓 · 客服", "agent"})
	}
	for _, a := range accounts {
		var exists bool
		if err := db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM actors WHERE email=$1)`, strings.ToLower(a.email)).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		if a.password == "" {
			if a.role == "agent" {
				return fmt.Errorf("AGENT_PASSWORD is required with SEED_DEMO=true")
			}
			return fmt.Errorf("ADMIN_PASSWORD is required to create the first administrator")
		}
		if !validEmail(a.email) || len(a.password) < 12 || len(a.password) > 72 {
			return fmt.Errorf("%s requires a valid email and a password of 12..72 bytes", a.role)
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(a.password), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		if _, err = db.Exec(ctx, `INSERT INTO actors(id,role,name,email,password_hash,enabled) VALUES($1,$2,$3,$4,$5,true) ON CONFLICT(email) DO NOTHING`, newID(), a.role, a.name, strings.ToLower(a.email), string(hash)); err != nil {
			return err
		}
	}
	return nil
}
