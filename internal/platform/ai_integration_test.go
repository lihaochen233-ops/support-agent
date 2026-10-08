package platform

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func aiFixtureConversation(t *testing.T, s *Server, body string) (string, string) {
	t.Helper()
	ctx := context.Background()
	visitor, conversation := newID(), newID()
	if _, err := s.DB.Exec(ctx, `INSERT INTO actors(id,role,name) VALUES($1,'visitor','AI测试访客')`, visitor); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `INSERT INTO conversations(id,visitor_id,last_seq) VALUES($1,$2,1)`, conversation, visitor); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `INSERT INTO messages(conversation_id,seq,sender_id,sender_role,client_id,body) VALUES($1,1,$2,'visitor',$3,$4)`, conversation, visitor, newID(), body); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `INSERT INTO ai_jobs(conversation_id) VALUES($1)`, conversation); err != nil {
		t.Fatal(err)
	}
	return conversation, visitor
}
func aiFixtureDocument(t *testing.T, s *Server) (string, string) {
	t.Helper()
	id, version := newID(), newID()
	ctx := context.Background()
	if _, err := s.DB.Exec(ctx, `INSERT INTO documents(id,name) VALUES($1,'policy.txt')`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `INSERT INTO document_versions(id,document_id,filename,path) VALUES($1,$2,'policy.txt','fixture')`, version, id); err != nil {
		t.Fatal(err)
	}
	return id, version
}
func aiFixtureVector() []float32 { vector := make([]float32, 1024); vector[0] = 1; return vector }

func TestIntegrationAILeaseFencingAndFollowup(t *testing.T) {
	db, cfg := integrationDB(t)
	cfg.InstanceID = "worker-a"
	s := NewServer(db, nil, cfg)
	ctx := context.Background()
	conversation, visitor := aiFixtureConversation(t, s, "第一条问题")
	cfg.InstanceID = "worker-b"
	other := NewServer(db, nil, cfg)
	var wg sync.WaitGroup
	wg.Add(2)
	type claim struct {
		job aiJob
		err error
	}
	claims := make(chan claim, 2)
	for _, server := range []*Server{s, other} {
		go func(server *Server) { defer wg.Done(); job, err := server.claimAIJob(ctx); claims <- claim{job, err} }(server)
	}
	wg.Wait()
	close(claims)
	var first aiJob
	success := 0
	for result := range claims {
		if result.err == nil {
			success++
			first = result.job
		} else if result.err != pgx.ErrNoRows {
			t.Fatal(result.err)
		}
	}
	if success != 1 {
		t.Fatalf("claimed %d concurrent tasks", success)
	}
	if _, err := db.Exec(ctx, `UPDATE ai_jobs SET lease_until=now()-interval '1 second' WHERE id=$1`, first.ID); err != nil {
		t.Fatal(err)
	}
	replacement, err := other.claimAIJob(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if replacement.Generation <= first.Generation {
		t.Fatal("lease generation did not increase")
	}
	if err = s.finishAIJob(ctx, first, agentReply{Kind: "answer", Body: "旧工作者迟到答案"}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM messages WHERE sender_role='ai'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("old lease wrote a reply: %d %v", count, err)
	}
	// A visitor adds another message while the current task is running.
	if _, err = db.Exec(ctx, `UPDATE conversations SET last_seq=2 WHERE id=$1`, conversation); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO messages(conversation_id,seq,sender_id,sender_role,client_id,body) VALUES($1,2,$2,'visitor',$3,'补充问题')`, conversation, visitor, newID()); err != nil {
		t.Fatal(err)
	}
	if err = other.finishAIJob(ctx, replacement, agentReply{Kind: "answer", Body: "新工作者的答案"}); err != nil {
		t.Fatal(err)
	}
	if err = other.finishAIJob(ctx, replacement, agentReply{Kind: "answer", Body: "重复提交"}); err != nil {
		t.Fatal(err)
	}
	var replies, pending int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM messages WHERE sender_role='ai'`).Scan(&replies); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(ctx, `SELECT count(*) FROM ai_jobs WHERE status='pending'`).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if replies != 1 || pending != 1 {
		t.Fatalf("replies=%d pending=%d; expected one committed reply and one follow-up", replies, pending)
	}
	next, err := s.claimAIJob(ctx)
	if err != nil || next.InputSeq != 3 {
		t.Fatalf("follow-up did not include new visitor input: %+v %v", next, err)
	}
}
func TestIntegrationAIHandoffRejectsLateReply(t *testing.T) {
	db, cfg := integrationDB(t)
	s := NewServer(db, nil, cfg)
	ctx := context.Background()
	conversation, _ := aiFixtureConversation(t, s, "我有一个问题")
	job, err := s.claimAIJob(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SELECT id FROM conversations WHERE id=$1 FOR UPDATE`, conversation); err != nil {
		t.Fatal(err)
	}
	if err = s.handoffTx(ctx, tx, conversation, "主动请求人工"); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.finishAIJob(ctx, job, agentReply{Kind: "answer", Body: "不应该出现"}); err != nil {
		t.Fatal(err)
	}
	var state string
	var count int
	if err = db.QueryRow(ctx, `SELECT status FROM conversations WHERE id=$1`, conversation).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(ctx, `SELECT count(*) FROM messages WHERE sender_role='ai'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if state != "waiting" || count != 0 {
		t.Fatalf("state=%s late replies=%d", state, count)
	}
	if s.renewAILease(ctx, job) {
		t.Fatal("cancelled task renewed its lease")
	}
}
func TestIntegrationDocumentSwapAndSearchAvailability(t *testing.T) {
	db, cfg := integrationDB(t)
	cfg.EmbeddingAPIKey = "fixture"
	cfg.EmbeddingModel = "fixture-1024"
	ctx := context.Background()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"index": 0, "embedding": aiFixtureVector()}}, "usage": map[string]int{"total_tokens": 5}})
	}))
	defer provider.Close()
	cfg.EmbeddingBaseURL = provider.URL
	s := NewServer(db, nil, cfg)
	document, version := aiFixtureDocument(t, s)
	first, err := s.claimDocumentJob(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.finishDocumentJob(ctx, first, []textPart{{Text: "旧版七日退货", Page: 2}}, [][]float32{aiFixtureVector()}); err != nil {
		t.Fatal(err)
	}
	replacementID := newID()
	if _, err = db.Exec(ctx, `INSERT INTO document_versions(id,document_id,filename,path) VALUES($1,$2,'new.txt','fixture')`, replacementID, document); err != nil {
		t.Fatal(err)
	}
	replacement, err := s.claimDocumentJob(ctx)
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.searchKnowledge(ctx, "退货", "")
	if err != nil || len(before) != 1 || before[0].Text != "旧版七日退货" {
		t.Fatalf("old revision vanished while processing: %+v %v", before, err)
	}
	if _, err = db.Exec(ctx, `UPDATE document_versions SET lease_until=now()-interval '1 second' WHERE id=$1`, replacement.ID); err != nil {
		t.Fatal(err)
	}
	reclaimed, err := s.claimDocumentJob(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.finishDocumentJob(ctx, replacement, []textPart{{Text: "旧worker错误索引"}}, [][]float32{aiFixtureVector()}); err != nil {
		t.Fatal(err)
	}
	var active string
	if err = db.QueryRow(ctx, `SELECT active_version_id FROM documents WHERE id=$1`, document).Scan(&active); err != nil || active != version {
		t.Fatalf("unfenced document commit active=%s err=%v", active, err)
	}
	if err = s.finishDocumentJob(ctx, reclaimed, []textPart{{Text: "新版十四天退货", Page: 3}}, [][]float32{aiFixtureVector()}); err != nil {
		t.Fatal(err)
	}
	after, err := s.searchKnowledge(ctx, "退货", "")
	if err != nil || len(after) != 1 || after[0].Text != "新版十四天退货" || after[0].Page != 3 {
		t.Fatalf("revision swap failed: %+v %v", after, err)
	}
	if _, err = db.Exec(ctx, `UPDATE documents SET enabled=false WHERE id=$1`, document); err != nil {
		t.Fatal(err)
	}
	disabled, err := s.searchKnowledge(ctx, "退货", "")
	if err != nil || len(disabled) != 0 {
		t.Fatalf("disabled document was retrieved: %+v %v", disabled, err)
	}
	if _, err = db.Exec(ctx, `UPDATE documents SET enabled=true WHERE id=$1`, document); err != nil {
		t.Fatal(err)
	}
	changed := cfg
	changed.EmbeddingModel = "new-model"
	_, err = NewServer(db, nil, changed).searchKnowledge(ctx, "退货", "")
	if err == nil || !strings.Contains(err.Error(), "重建") {
		t.Fatalf("model fingerprint mismatch was not caught: %v", err)
	}
}
func TestIntegrationCitationDisabledDuringGeneration(t *testing.T) {
	db, cfg := integrationDB(t)
	s := NewServer(db, nil, cfg)
	ctx := context.Background()
	document, _ := aiFixtureDocument(t, s)
	docJob, err := s.claimDocumentJob(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.finishDocumentJob(ctx, docJob, []textPart{{Text: "七天内可退货"}}, [][]float32{aiFixtureVector()}); err != nil {
		t.Fatal(err)
	}
	var chunkID string
	if err = db.QueryRow(ctx, `SELECT id FROM chunks WHERE document_id=$1`, document).Scan(&chunkID); err != nil {
		t.Fatal(err)
	}
	conversation, _ := aiFixtureConversation(t, s, "可退货吗")
	job, err := s.claimAIJob(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `UPDATE documents SET enabled=false WHERE id=$1`, document); err != nil {
		t.Fatal(err)
	}
	if err = s.finishAIJob(ctx, job, agentReply{Kind: "answer", Body: "七天内可退货", Citations: []Citation{{ID: chunkID, DocumentID: document, Text: "七天内可退货"}}}); err != nil {
		t.Fatal(err)
	}
	var state, body string
	if err = db.QueryRow(ctx, `SELECT status FROM conversations WHERE id=$1`, conversation).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(ctx, `SELECT body FROM messages WHERE sender_role='ai'`).Scan(&body); err != nil {
		t.Fatal(err)
	}
	if state != "waiting" || body == "七天内可退货" {
		t.Fatalf("disabled citation used: state=%s body=%s", state, body)
	}
}
func TestIntegrationWorkerRAGAndAuthenticatedImage(t *testing.T) {
	db, cfg := integrationDB(t)
	cfg.ChatAPIKey = "test-cloud"
	cfg.EmbeddingAPIKey = "test-cloud"
	cfg.ModelTimeout = 2 * time.Second
	var sawImage atomic.Bool
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/embeddings" {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"index": 0, "embedding": aiFixtureVector()}}, "usage": map[string]int{"total_tokens": 9}})
			return
		}
		var payload struct {
			Messages []modelMessage `json:"messages"`
		}
		if json.NewDecoder(r.Body).Decode(&payload) != nil {
			t.Error("invalid model body")
		}
		encoded, _ := json.Marshal(payload.Messages)
		if strings.Contains(string(encoded), "data:image/png;base64,") {
			sawImage.Store(true)
		}
		last := payload.Messages[len(payload.Messages)-1]
		if last.Role != "tool" {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"tool_calls":[{"id":"tool-1","type":"function","function":{"name":"search_knowledge","arguments":"{\"query\":\"退货政策\"}"}}]}}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`))
			return
		}
		var result struct {
			Sources []Citation `json:"sources"`
		}
		if err := json.Unmarshal([]byte(last.Content.(string)), &result); err != nil || len(result.Sources) == 0 {
			t.Error("missing retrieved evidence")
		}
		reply, _ := json.Marshal(agentReply{Kind: "answer", Body: "按资料可以申请七日退货。", CitationIDs: []string{result.Sources[0].ID}})
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": string(reply)}}}, "usage": map[string]int{"prompt_tokens": 20, "completion_tokens": 10}})
	}))
	defer provider.Close()
	cfg.ChatBaseURL = provider.URL
	cfg.EmbeddingBaseURL = provider.URL
	s := NewServer(db, nil, cfg)
	ctx := context.Background()
	aiFixtureDocument(t, s)
	documentJob, err := s.claimDocumentJob(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.finishDocumentJob(ctx, documentJob, []textPart{{Text: "七天内可以申请退货。"}}, [][]float32{aiFixtureVector()}); err != nil {
		t.Fatal(err)
	}
	conversation, visitor := aiFixtureConversation(t, s, "截图中的商品能退吗？")
	imagePath := filepath.Join(t.TempDir(), "fixture.png")
	if err = os.WriteFile(imagePath, []byte("test-image-bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	imageID := newID()
	if _, err = db.Exec(ctx, `INSERT INTO attachments(id,conversation_id,uploader_id,path,mime,name,size) VALUES($1,$2,$3,$4,'image/png','fixture.png',16)`, imageID, conversation, visitor, imagePath); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `UPDATE messages SET image_id=$2 WHERE conversation_id=$1`, conversation, imageID); err != nil {
		t.Fatal(err)
	}
	job, err := s.claimAIJob(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s.processAIJob(ctx, job)
	var state, body string
	var citations, calls, tokens int
	if err = db.QueryRow(ctx, `SELECT status FROM conversations WHERE id=$1`, conversation).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(ctx, `SELECT body,jsonb_array_length(citations) FROM messages WHERE conversation_id=$1 AND sender_role='ai'`, conversation).Scan(&body, &citations); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(ctx, `SELECT count(*),sum(input_tokens+output_tokens) FROM ai_calls`).Scan(&calls, &tokens); err != nil {
		t.Fatal(err)
	}
	if !sawImage.Load() || state != "ai" || citations != 1 || calls != 3 || tokens != 54 {
		t.Fatalf("image=%v state=%s citations=%d calls=%d tokens=%d body=%s", sawImage.Load(), state, citations, calls, tokens, body)
	}
}

func TestIntegrationDocumentProcessingFailureKeepsPreviousIndex(t *testing.T) {
	db, cfg := integrationDB(t)
	ctx := context.Background()
	s := NewServer(db, nil, cfg)
	document, version := aiFixtureDocument(t, s)
	first, err := s.claimDocumentJob(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.finishDocumentJob(ctx, first, []textPart{{Text: "已发布内容"}}, [][]float32{aiFixtureVector()}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "broken.pdf")
	if err = os.WriteFile(path, []byte("not PDF"), 0600); err != nil {
		t.Fatal(err)
	}
	replacement := newID()
	if _, err = db.Exec(ctx, `INSERT INTO document_versions(id,document_id,filename,path) VALUES($1,$2,'broken.pdf',$3)`, replacement, document, path); err != nil {
		t.Fatal(err)
	}
	job, err := s.claimDocumentJob(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s.processDocumentJob(ctx, job)
	var status, detail, active string
	if err = db.QueryRow(ctx, `SELECT status,error FROM document_versions WHERE id=$1`, replacement).Scan(&status, &detail); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(ctx, `SELECT active_version_id FROM documents WHERE id=$1`, document).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || detail == "" || active != version {
		t.Fatalf("failure state=%s error=%s active=%s", status, detail, active)
	}
}

func TestIntegrationAgentClarifiesAtMostOnce(t *testing.T) {
	db, cfg := integrationDB(t)
	ctx := context.Background()
	s := NewServer(db, nil, cfg)
	conversation, visitor := aiFixtureConversation(t, s, "有问题")
	first, err := s.claimAIJob(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.finishAIJob(ctx, first, agentReply{Kind: "clarify", Body: "请描述问题的具体表现？"}); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `UPDATE conversations SET last_seq=3 WHERE id=$1`, conversation); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO messages(conversation_id,seq,sender_id,sender_role,client_id,body) VALUES($1,3,$2,'visitor',$3,'还是有问题')`, conversation, visitor, newID()); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO ai_jobs(conversation_id) VALUES($1)`, conversation); err != nil {
		t.Fatal(err)
	}
	second, err := s.claimAIJob(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.finishAIJob(ctx, second, agentReply{Kind: "clarify", Body: "能否再描述一下？"}); err != nil {
		t.Fatal(err)
	}
	var state string
	var count int
	if err = db.QueryRow(ctx, `SELECT status,clarification_count FROM conversations WHERE id=$1`, conversation).Scan(&state, &count); err != nil {
		t.Fatal(err)
	}
	if state != "waiting" || count != 1 {
		t.Fatalf("state=%s clarification_count=%d", state, count)
	}
}
