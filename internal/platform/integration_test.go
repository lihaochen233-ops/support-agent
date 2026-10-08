package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// integrationDB creates one isolated schema per test. It never drops a database
// or uses public application tables, even when TEST_DATABASE_URL is mispointed.
func integrationDB(t *testing.T) (*pgxpool.Pool, Config) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run real PostgreSQL/pgvector integration tests")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err = admin.Ping(ctx); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	if _, err = admin.Exec(ctx, `CREATE EXTENSION IF NOT EXISTS vector WITH SCHEMA public`); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	schema := "luma_test_" + strings.ReplaceAll(newID(), "-", "")
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	pc, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	pc.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pc.MaxConns = 16
	db, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Close()
		_, e := admin.Exec(context.Background(), "DROP SCHEMA "+identifier+" CASCADE")
		if e != nil {
			t.Errorf("cleanup schema: %v", e)
		}
		admin.Close()
	})
	if err = Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	cfg := Config{InstanceID: newID(), UploadDir: t.TempDir(), StaticDir: t.TempDir(), AllowedOrigins: []string{"http://test.local"}, SessionTTL: time.Hour, VisitorTTL: 24 * time.Hour, AdminEmail: "admin@test.local", AdminPassword: "TestAdminPassword123!", AgentEmail: "agent@test.local", AgentPassword: "TestAgentPassword123!", SeedDemo: true, EmbeddingDimensions: 1024, EmbeddingModel: "test-embedding", ChatModel: "test-chat", ModelTimeout: 10 * time.Second, WorkerInterval: 25 * time.Millisecond, MaxToolCalls: 3, SearchMinScore: .25}
	if err = SeedAccounts(ctx, db, cfg); err != nil {
		t.Fatal(err)
	}
	return db, cfg
}

type integrationClient struct {
	client       *http.Client
	url, surface string
}

func testClient(url, surface string) *integrationClient {
	jar, _ := cookiejar.New(nil)
	return &integrationClient{client: &http.Client{Jar: jar, Timeout: 15 * time.Second}, url: url, surface: surface}
}
func (c *integrationClient) request(method, path string, body any) (int, []byte, error) {
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
	}
	r, err := http.NewRequest(method, c.url+path, bytes.NewReader(data))
	if err != nil {
		return 0, nil, err
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://test.local")
	r.Header.Set("X-Surface", c.surface)
	r.Header.Set("X-Requested-With", "support-agent")
	resp, err := c.client.Do(r)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	return resp.StatusCode, raw, err
}
func (c *integrationClient) must(t *testing.T, method, path string, body any, status int, out any) {
	t.Helper()
	code, raw, err := c.request(method, path, body)
	if err != nil {
		t.Fatal(err)
	}
	if code != status {
		t.Fatalf("%s %s = %d want %d: %s", method, path, code, status, raw)
	}
	if out != nil {
		if err = json.Unmarshal(raw, out); err != nil {
			t.Fatal(err)
		}
	}
}

func TestIntegrationChatLifecycleAndConcurrentClaims(t *testing.T) {
	db, cfg := integrationDB(t)
	app := NewServer(db, nil, cfg)
	ts := httptest.NewServer(app.Handler())
	defer ts.Close()
	visitor := testClient(ts.URL, "visitor")
	var owner Actor
	visitor.must(t, "POST", "/api/visitor", map[string]string{"name": "测试访客"}, 201, &owner)
	var c Conversation
	visitor.must(t, "POST", "/api/conversations", map[string]any{}, 200, &c)
	var again Conversation
	visitor.must(t, "POST", "/api/conversations", map[string]any{}, 200, &again)
	if c.ID != again.ID {
		t.Fatal("more than one active conversation")
	}
	base := "/api/conversations/" + c.ID
	var first Message
	input := map[string]string{"client_id": newID(), "body": "耳机如何配对？"}
	visitor.must(t, "POST", base+"/messages", input, 200, &first)
	var duplicate Message
	visitor.must(t, "POST", base+"/messages", input, 200, &duplicate)
	if duplicate.ID != first.ID || duplicate.Seq != first.Seq {
		t.Fatal("duplicate persisted")
	}
	visitor.must(t, "POST", base+"/handoff", map[string]string{"reason": "需要人工"}, 200, &c)
	admin := testClient(ts.URL, "staff")
	agent := testClient(ts.URL, "staff")
	admin.must(t, "POST", "/api/login", map[string]string{"email": cfg.AdminEmail, "password": cfg.AdminPassword}, 200, nil)
	agent.must(t, "POST", "/api/login", map[string]string{"email": cfg.AgentEmail, "password": cfg.AgentPassword}, 200, nil)
	admin.must(t, "POST", "/api/presence", map[string]bool{"available": true}, 200, nil)
	agent.must(t, "POST", "/api/presence", map[string]bool{"available": true}, 200, nil)
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	errs := make(chan error, 2)
	for _, client := range []*integrationClient{admin, agent} {
		wg.Add(1)
		go func(client *integrationClient) {
			defer wg.Done()
			code, _, err := client.request("POST", base+"/claim", map[string]any{})
			codes <- code
			errs <- err
		}(client)
	}
	wg.Wait()
	close(codes)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	winners := 0
	for code := range codes {
		if code == 200 {
			winners++
		} else if code != 409 {
			t.Fatalf("unexpected claim code %d", code)
		}
	}
	if winners != 1 {
		t.Fatalf("claim winners=%d", winners)
	}
	visitor.must(t, "GET", base, nil, 200, &c)
	var staff Actor
	admin.must(t, "GET", "/api/me", nil, 200, &staff)
	winner, loser := agent, admin
	if c.AgentID == staff.ID {
		winner, loser = admin, agent
	}
	var response Message
	winner.must(t, "POST", base+"/messages", map[string]string{"client_id": newID(), "body": "您好，请长按配对键。"}, 200, &response)
	loser.must(t, "POST", base+"/messages", map[string]string{"client_id": newID(), "body": "不允许发送"}, 403, nil)
	other := testClient(ts.URL, "visitor")
	other.must(t, "POST", "/api/visitor", map[string]any{}, 201, nil)
	other.must(t, "GET", base+"/messages", nil, 403, nil)
	visitor.must(t, "POST", base+"/read", map[string]int64{"seq": response.Seq}, 200, nil)
	visitor.must(t, "POST", base+"/close", map[string]any{}, 200, nil)
	visitor.must(t, "POST", base+"/messages", input, 200, &duplicate)
	visitor.must(t, "POST", base+"/messages", map[string]string{"client_id": newID(), "body": "closed"}, 409, nil)
	visitor.must(t, "POST", base+"/feedback", map[string]string{"value": "solved"}, 200, nil)
	var stats map[string]any
	admin.must(t, "GET", "/api/stats", nil, 200, &stats)
	if stats["ai_solved"] != float64(0) {
		t.Fatalf("human-resolved miscounted as AI: %v", stats)
	}
}

func TestIntegrationConcurrentSendAndPagination(t *testing.T) {
	db, cfg := integrationDB(t)
	app := NewServer(db, nil, cfg)
	ctx := context.Background()
	var visitor Actor
	if err := db.QueryRow(ctx, `INSERT INTO actors(role,name) VALUES('visitor','并发访客') RETURNING id,role,name`).Scan(&visitor.ID, &visitor.Role, &visitor.Name); err != nil {
		t.Fatal(err)
	}
	id := newID()
	if _, err := db.Exec(ctx, `INSERT INTO conversations(id,visitor_id,status) VALUES($1,$2,'waiting')`, id, visitor.ID); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errorsCh := make(chan error, 40)
	for i := 0; i < 20; i++ {
		clientID := newID()
		for retry := 0; retry < 2; retry++ {
			wg.Add(1)
			go func(i int, key string) {
				defer wg.Done()
				_, err := app.sendMessage(ctx, visitor, id, sendMessageInput{ClientID: key, Body: fmt.Sprintf("message %d", i)})
				errorsCh <- err
			}(i, clientID)
		}
	}
	wg.Wait()
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	var count, last int
	if err := db.QueryRow(ctx, `SELECT count(*),COALESCE(max(seq),0) FROM messages WHERE conversation_id=$1`, id).Scan(&count, &last); err != nil {
		t.Fatal(err)
	}
	if count != 20 || last != 20 {
		t.Fatalf("count=%d seq=%d", count, last)
	}
}
