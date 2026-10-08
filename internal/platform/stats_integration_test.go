package platform

import (
	"context"
	"net/http/httptest"
	"testing"
)

func TestIntegrationAISolvedRequiresAIReplyAndFeedback(t *testing.T) {
	cases := []struct {
		name      string
		aiReply   bool
		handoff   bool
		wantCount int
	}{
		{name: "empty conversation is not AI solved", wantCount: 0},
		{name: "AI reply with solved feedback counts", aiReply: true, wantCount: 1},
		{name: "handoff after AI reply does not count", aiReply: true, handoff: true, wantCount: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, cfg := integrationDB(t)
			app := NewServer(db, nil, cfg)
			ts := httptest.NewServer(app.Handler())
			defer ts.Close()
			visitor := testClient(ts.URL, "visitor")
			visitor.must(t, "POST", "/api/visitor", map[string]any{}, 201, nil)
			var conversation Conversation
			visitor.must(t, "POST", "/api/conversations", map[string]any{}, 200, &conversation)
			base := "/api/conversations/" + conversation.ID
			if tc.aiReply {
				visitor.must(t, "POST", base+"/messages", map[string]string{"client_id": newID(), "body": "你好"}, 200, nil)
				job, err := app.claimAIJob(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				// This is a test answer, persisted through the real completion
				// transaction. A greeting legitimately has no knowledge citation.
				if err = app.finishAIJob(context.Background(), job, agentReply{Kind: "answer", Body: "您好，请问有什么可以帮助您？"}); err != nil {
					t.Fatal(err)
				}
			}
			if tc.handoff {
				visitor.must(t, "POST", base+"/handoff", map[string]string{"reason": "请人工继续帮助"}, 200, nil)
			}
			visitor.must(t, "POST", base+"/close", map[string]any{}, 200, nil)
			admin := testClient(ts.URL, "staff")
			admin.must(t, "POST", "/api/login", map[string]string{"email": cfg.AdminEmail, "password": cfg.AdminPassword}, 200, nil)
			var stats struct {
				AISolved int `json:"ai_solved"`
				Solved   int `json:"solved"`
			}
			admin.must(t, "GET", "/api/stats", nil, 200, &stats)
			if stats.AISolved != 0 || stats.Solved != 0 {
				t.Fatalf("conversation counted before explicit feedback: %+v", stats)
			}
			visitor.must(t, "POST", base+"/feedback", map[string]string{"value": "solved"}, 200, nil)
			admin.must(t, "GET", "/api/stats", nil, 200, &stats)
			if stats.AISolved != tc.wantCount || stats.Solved != 1 {
				t.Fatalf("stats = %+v; want ai_solved=%d, solved=1", stats, tc.wantCount)
			}
		})
	}
}
