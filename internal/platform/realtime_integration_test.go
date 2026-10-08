package platform

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/redis/go-redis/v9"
)

func TestIntegrationRedisCrossInstanceAndReconnect(t *testing.T) {
	db, cfg := integrationDB(t)
	redisURL := os.Getenv("TEST_REDIS_URL")
	if redisURL == "" {
		t.Skip("set TEST_REDIS_URL for real Redis cross-instance test")
	}
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(opts)
	defer rdb.Close()
	if err = rdb.Ping(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg.InstanceID = "node-a"
	a := NewServer(db, rdb, cfg)
	cfg.InstanceID = "node-b"
	b := NewServer(db, rdb, cfg)
	go a.subscribe(ctx)
	go b.subscribe(ctx)
	defer func() {
		a.clientsMu.RLock()
		for c := range a.clients {
			c.stop()
		}
		a.clientsMu.RUnlock()
		b.clientsMu.RLock()
		for c := range b.clients {
			c.stop()
		}
		b.clientsMu.RUnlock()
	}()
	first := httptest.NewServer(a.Handler())
	defer first.Close()
	second := httptest.NewServer(b.Handler())
	defer second.Close()
	client := testClient(first.URL, "visitor")
	client.must(t, "POST", "/api/visitor", map[string]any{}, 201, nil)
	var conv Conversation
	client.must(t, "POST", "/api/conversations", map[string]any{}, 200, &conv)
	u, _ := url.Parse(first.URL)
	headers := http.Header{"Origin": []string{"http://test.local"}}
	for _, cookie := range client.client.Jar.Cookies(u) {
		headers.Add("Cookie", cookie.String())
	}
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(second.URL, "http")+"/api/ws?surface=visitor", &websocket.DialOptions{HTTPHeader: headers})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	readCtx, readCancel := context.WithTimeout(ctx, 5*time.Second)
	defer readCancel()
	var event map[string]any
	if err = wsjson.Read(readCtx, ws, &event); err != nil || event["type"] != "ready" {
		t.Fatalf("ready: %v %v", event, err)
	}
	// Confirm both listeners have subscribed before creating the notification.
	deadline := time.Now().Add(3 * time.Second)
	for {
		n, e := rdb.PubSubNumSub(ctx, notificationChannel).Result()
		if e != nil {
			t.Fatal(e)
		}
		if n[notificationChannel] >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("subscriber did not become ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	var saved Message
	client.must(t, "POST", "/api/conversations/"+conv.ID+"/messages", map[string]string{"client_id": newID(), "body": "跨实例消息"}, 200, &saved)
	for {
		if err = wsjson.Read(readCtx, ws, &event); err != nil {
			t.Fatal(err)
		}
		if event["type"] == "changed" && event["conversation_id"] == conv.ID {
			break
		}
	}
	// Close the original HTTP instance. The same identity must recover on B.
	first.Close()
	client.url = second.URL
	var items struct {
		Items []Message `json:"items"`
	}
	client.must(t, "GET", fmt.Sprintf("/api/conversations/%s/messages?after_seq=0", conv.ID), nil, 200, &items)
	if len(items.Items) != 1 || items.Items[0].ID != saved.ID {
		t.Fatalf("reconnect did not recover stored message: %+v", items)
	}
	// Simulate a missed Pub/Sub notification: database truth is still queryable.
	var visitor Actor
	client.must(t, "GET", "/api/me", nil, 200, &visitor)
	noNotifications := NewServer(db, nil, cfg)
	_, err = noNotifications.sendMessage(ctx, visitor, conv.ID, sendMessageInput{ClientID: newID(), Body: "没有广播的消息"})
	if err != nil {
		t.Fatal(err)
	}
	client.must(t, "GET", fmt.Sprintf("/api/conversations/%s/messages?after_seq=%d", conv.ID, saved.Seq), nil, 200, &items)
	if len(items.Items) != 1 || items.Items[0].Body != "没有广播的消息" {
		t.Fatal("reconciliation failed")
	}
}
