package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestAIProcessRecoveryHelper is an entry point for the disposable OS process
// below. It never creates a schema or runs migrations, and is skipped by normal
// test runs. Its only job is running the production AI worker until it is killed.
func TestAIProcessRecoveryHelper(t *testing.T) {
	if os.Getenv("LUMA_AI_PROCESS_HELPER") != "1" {
		t.Skip("subprocess helper; run through TestIntegrationAIWorkerProcessRecovery")
	}
	searchPath := os.Getenv("LUMA_AI_PROCESS_SEARCH_PATH")
	if !regexp.MustCompile(`^luma_test_[a-f0-9]{32},public$`).MatchString(searchPath) {
		t.Fatal("helper requires the parent test's isolated schema")
	}
	pc, err := pgxpool.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal("helper database configuration is invalid")
	}
	pc.ConnConfig.RuntimeParams["search_path"] = searchPath
	ctx := context.Background()
	db, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		t.Fatal("helper could not open database pool")
	}
	defer db.Close()
	var schema string
	if err = db.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil || schema != strings.TrimSuffix(searchPath, ",public") {
		t.Fatal("helper did not connect to the parent's isolated schema")
	}
	cfg := Config{
		InstanceID: "process-worker-before-kill",
		ChatAPIKey: "http-test-double", ChatModel: "process-test-chat",
		ChatBaseURL:     os.Getenv("LUMA_AI_PROCESS_MODEL_URL"),
		EmbeddingAPIKey: "unused-test-key", EmbeddingDimensions: 1024,
		ModelTimeout: 2 * time.Minute, WorkerInterval: 25 * time.Millisecond,
	}
	s := NewServer(db, nil, cfg)
	s.aiWorkerLoop(ctx)
	t.Fatal("helper worker unexpectedly exited without being killed")
}

// This test intentionally costs about 60 seconds: it must use the real lease
// duration and never UPDATE lease_until. PostgreSQL and the worker processes are
// real; the HTTP model is a deterministic test double, not a cloud-model test.
func TestIntegrationAIWorkerProcessRecovery(t *testing.T) {
	if os.Getenv("TEST_PROCESS_RECOVERY") != "1" {
		t.Skip("set TEST_PROCESS_RECOVERY=1 to run the real process-kill / 60-second lease recovery test")
	}
	db, cfg := integrationDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 105*time.Second)
	defer cancel()
	entered := make(chan struct{})
	release := make(chan struct{})
	var modelRequests atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.Error(w, "unexpected model endpoint", http.StatusNotFound)
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
		if modelRequests.Add(1) == 1 {
			close(entered)
			// Hold the first response so the original worker cannot commit before
			// Process.Kill. Client disconnection or cleanup releases the handler.
			select {
			case <-r.Context().Done():
			case <-release:
			}
			return
		}
		reply, _ := json.Marshal(agentReply{Kind: "answer", Body: "您好，请问有什么可以帮助您？"})
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]string{"content": string(reply)}}},
			"usage":   map[string]int{"prompt_tokens": 10, "completion_tokens": 8},
		})
	}))
	defer provider.Close()
	defer close(release)
	cfg.InstanceID = "process-worker-after-kill"
	cfg.ChatAPIKey, cfg.EmbeddingAPIKey = "http-test-double", "unused-test-key"
	cfg.ChatBaseURL, cfg.ChatModel = provider.URL, "process-test-chat"
	s := NewServer(db, nil, cfg)
	defer aiRuntimes.Delete(s)
	conversation, _ := aiFixtureConversation(t, s, "你好")
	var originalID string
	if err := db.QueryRow(ctx, `SELECT id FROM ai_jobs WHERE conversation_id=$1`, conversation).Scan(&originalID); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(executable, "-test.run=^TestAIProcessRecoveryHelper$", "-test.timeout=2m")
	child.Env = append(os.Environ(),
		"LUMA_AI_PROCESS_HELPER=1",
		"LUMA_AI_PROCESS_SEARCH_PATH="+db.Config().ConnConfig.RuntimeParams["search_path"],
		"LUMA_AI_PROCESS_MODEL_URL="+provider.URL,
	)
	var output bytes.Buffer
	child.Stdout, child.Stderr = &output, &output
	if err = child.Start(); err != nil {
		t.Fatalf("start worker OS process: %v", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- child.Wait() }()
	reaped := false
	defer func() {
		if !reaped {
			_ = child.Process.Kill()
			<-exited
		}
	}()
	select {
	case <-entered:
	case err = <-exited:
		reaped = true
		t.Fatalf("worker exited before issuing model request: %v; %s", err, output.String())
	case <-time.After(15 * time.Second):
		t.Fatal("worker never issued the first model request")
	}
	var beforeGeneration int64
	var remaining float64
	var owner string
	if err = db.QueryRow(ctx, `SELECT lease_owner,lease_generation,extract(epoch FROM lease_until-now())::double precision FROM ai_jobs WHERE id=$1`, originalID).Scan(&owner, &beforeGeneration, &remaining); err != nil {
		t.Fatal(err)
	}
	if owner != "process-worker-before-kill" || beforeGeneration != 1 || remaining < 45 {
		t.Fatalf("unexpected original lease: owner=%s generation=%d remaining=%.2fs", owner, beforeGeneration, remaining)
	}
	if err = child.Process.Kill(); err != nil {
		t.Fatalf("kill worker OS process: %v", err)
	}
	select {
	case err = <-exited:
		reaped = true
		if err == nil || child.ProcessState == nil || child.ProcessState.Success() {
			t.Fatal("worker was expected to terminate unsuccessfully after Process.Kill")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("killed worker did not exit")
	}
	killedAt := time.Now()
	workerCtx, stopWorker := context.WithCancel(ctx)
	workerDone := make(chan struct{})
	go func() { defer close(workerDone); s.aiWorkerLoop(workerCtx) }()
	defer func() { stopWorker(); <-workerDone }()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var status string
	var generation int64
	var attempts int
	for {
		if err = db.QueryRow(ctx, `SELECT status,lease_owner,lease_generation,attempts FROM ai_jobs WHERE id=$1`, originalID).Scan(&status, &owner, &generation, &attempts); err != nil {
			t.Fatal(err)
		}
		if status == "completed" {
			break
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatalf("recovery timed out: status=%s generation=%d attempts=%d", status, generation, attempts)
		}
	}
	if elapsed := time.Since(killedAt); elapsed < 40*time.Second {
		t.Fatalf("recovery finished too early for the natural lease expiration: %s", elapsed)
	}
	if owner != cfg.InstanceID || generation != 2 || attempts != 2 {
		t.Fatalf("task was not reclaimed exactly once: owner=%s generation=%d attempts=%d", owner, generation, attempts)
	}
	// Leave the replacement worker polling briefly: completed work must not be
	// claimed again, and the surviving reply must belong to the original task.
	select {
	case <-time.After(250 * time.Millisecond):
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var replies, jobs int
	var body, clientID string
	if err = db.QueryRow(ctx, `SELECT count(*) FROM messages WHERE conversation_id=$1 AND sender_role='ai'`, conversation).Scan(&replies); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(ctx, `SELECT count(*) FROM ai_jobs WHERE conversation_id=$1`, conversation).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if replies != 1 || jobs != 1 || modelRequests.Load() != 2 {
		t.Fatalf("recovery duplicated or lost work: replies=%d jobs=%d model_requests=%d", replies, jobs, modelRequests.Load())
	}
	if err = db.QueryRow(ctx, `SELECT body,client_id FROM messages WHERE conversation_id=$1 AND sender_role='ai'`, conversation).Scan(&body, &clientID); err != nil {
		t.Fatal(err)
	}
	if body != "您好，请问有什么可以帮助您？" || clientID != "ai-job:"+originalID {
		t.Fatalf("unexpected recovered reply: body=%q client_id=%q", body, clientID)
	}
	t.Logf("OS worker killed during HTTP test-model request; original task completed after natural lease expiry in %s; generation=2, attempts=2, one persisted AI reply", time.Since(killedAt).Round(time.Millisecond))
}
