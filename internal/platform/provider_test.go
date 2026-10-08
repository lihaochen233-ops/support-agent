package platform

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestProviderRetriesTransientAndRecordsActualCalls(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-secret" {
			t.Error("missing authorization")
		}
		if calls.Add(1) == 1 {
			http.Error(w, "never expose secret response", 503)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": "{\"kind\":\"clarify\",\"body\":\"请描述问题\"}"}}}, "usage": map[string]int{"prompt_tokens": 12, "completion_tokens": 7}})
	}))
	defer server.Close()
	var statuses []string
	client := providerClient{BaseURL: server.URL, APIKey: "test-secret", Model: "fixture", Record: func(status string, _ time.Duration, in, out int, detail string) {
		statuses = append(statuses, status)
		if strings.Contains(detail, "secret") {
			t.Error("provider response leaked")
		}
		if status == "ok" && (in != 12 || out != 7) {
			t.Error("incorrect usage")
		}
	}}
	_, err := client.chat(context.Background(), []modelMessage{{Role: "user", Content: "hello"}}, true)
	if err != nil || calls.Load() != 2 || strings.Join(statuses, ",") != "error,ok" {
		t.Fatalf("calls=%d logs=%v err=%v", calls.Load(), statuses, err)
	}
}
func TestProviderDoesNotRetryCredentials(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); http.Error(w, "private", 401) }))
	defer server.Close()
	_, err := (providerClient{BaseURL: server.URL, APIKey: "x"}).chat(context.Background(), nil, true)
	if err == nil || calls.Load() != 1 || strings.Contains(err.Error(), "private") {
		t.Fatal("credential error incorrectly retried or leaked")
	}
}
func TestEmbeddingRejectsWrongDimensions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"index":0,"embedding":[1,2]}]}`))
	}))
	defer server.Close()
	_, err := (providerClient{BaseURL: server.URL, APIKey: "x"}).embed(context.Background(), []string{"hello"})
	if err == nil {
		t.Fatal("incorrect vector dimensions accepted")
	}
}
func TestEmbeddingReordersResponseByIndex(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a := make([]float32, 1024)
		a[0] = 1
		b := make([]float32, 1024)
		b[1] = 1
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"index": 1, "embedding": b}, map[string]any{"index": 0, "embedding": a}}})
	}))
	defer server.Close()
	vectors, err := (providerClient{BaseURL: server.URL, APIKey: "x"}).embed(context.Background(), []string{"a", "b"})
	if err != nil || vectors[0][0] != 1 || vectors[1][1] != 1 {
		t.Fatalf("out of order vectors: %v", err)
	}
}
func TestChunkerChineseOverlapAndSources(t *testing.T) {
	source := strings.Repeat("知", 1300)
	chunks := chunkParts([]textPart{{Text: source, Page: 7}, {Text: "另一页", Page: 8}})
	if len(chunks) != 3 || len([]rune(chunks[0].Text)) != 700 || len([]rune(chunks[1].Text)) != 700 || chunks[1].Page != 7 || chunks[2].Page != 8 {
		t.Fatalf("unexpected chunks: %#v", chunks)
	}
}
