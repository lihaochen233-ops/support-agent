package platform

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

const notificationChannel = "luma:conversation:changed"

type socketClient struct {
	actor     Actor
	request   *http.Request
	tokenHash string
	conn      *websocket.Conn
	send      chan any
	cancel    context.CancelFunc
	once      sync.Once
}

func (c *socketClient) stop() { c.once.Do(func() { c.cancel(); _ = c.conn.CloseNow() }) }
func (c *socketClient) enqueue(event any) {
	// 慢客户端必须自行重连补拉，不能阻塞其他连接或 Redis 订阅者。
	select {
	case c.send <- event:
	default:
		c.stop()
	}
}

type socketCommand struct {
	Type           string `json:"type"`
	ConversationID string `json:"conversation_id"`
	ClientID       string `json:"client_id"`
	Body           string `json:"body"`
	ImageID        string `json:"image_id"`
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	if !s.validOrigin(r, true) {
		writeError(w, 403, "连接来源校验失败")
		return
	}
	a, err := s.actor(r)
	if err != nil {
		writeError(w, 401, "请先登录")
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	cookie, _ := r.Cookie(cookieName(r))
	c := &socketClient{actor: a, request: r, tokenHash: hashToken(cookie.Value), conn: conn, send: make(chan any, 64), cancel: cancel}
	conn.SetReadLimit(64 << 10)
	s.clientsMu.Lock()
	s.clients[c] = struct{}{}
	s.clientsMu.Unlock()
	defer func() { s.clientsMu.Lock(); delete(s.clients, c); s.clientsMu.Unlock(); c.stop() }()
	go s.socketWriter(ctx, c)
	c.enqueue(map[string]string{"type": "ready"})
	s.touchPresence(ctx, a)
	for {
		var command socketCommand
		if err := wsjson.Read(ctx, conn, &command); err != nil {
			return
		}
		// 不信任升级时缓存的权限：退出登录或禁用账号后不得继续发送。
		current, err := s.actor(r)
		if err != nil {
			_ = conn.Close(websocket.StatusPolicyViolation, "登录已失效")
			return
		}
		if command.Type == "ping" {
			s.touchPresence(ctx, current)
			c.enqueue(map[string]string{"type": "pong"})
			continue
		}
		if command.Type != "send" {
			c.enqueue(map[string]string{"type": "error", "error": "不支持的消息类型"})
			continue
		}
		message, err := s.sendMessage(ctx, current, command.ConversationID, sendMessageInput{ClientID: command.ClientID, Body: command.Body, ImageID: command.ImageID})
		if err != nil {
			c.enqueue(map[string]string{"type": "error", "client_id": command.ClientID, "error": publicError(err)})
			continue
		}
		c.enqueue(map[string]any{"type": "ack", "client_id": command.ClientID, "message": message})
	}
}

func (s *Server) socketWriter(ctx context.Context, c *socketClient) {
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	defer c.stop()
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-c.send:
			writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := wsjson.Write(writeCtx, c.conn, event)
			cancel()
			if err != nil {
				return
			}
		case <-ticker.C:
			current, err := s.actor(c.request)
			if err != nil {
				return
			}
			pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err = c.conn.Ping(pingCtx)
			cancel()
			if err != nil {
				return
			}
			s.touchPresence(ctx, current)
		}
	}
}

func (s *Server) notify(ctx context.Context, id string) {
	s.broadcast(ctx, id)
	if s.Redis != nil {
		if err := s.Redis.Publish(ctx, notificationChannel, id).Err(); err != nil {
			slog.Warn("realtime notification degraded", "error", err)
		}
	}
}

func (s *Server) invalidate(ctx context.Context, id string) { s.cancelAI(id); s.notify(ctx, id) }

func (s *Server) disconnectActor(id string) {
	s.clientsMu.RLock()
	var clients []*socketClient
	for c := range s.clients {
		if c.actor.ID == id {
			clients = append(clients, c)
		}
	}
	s.clientsMu.RUnlock()
	for _, c := range clients {
		c.stop()
	}
}
func (s *Server) disconnectSession(tokenHash string) {
	s.clientsMu.RLock()
	var clients []*socketClient
	for c := range s.clients {
		if c.tokenHash == tokenHash {
			clients = append(clients, c)
		}
	}
	s.clientsMu.RUnlock()
	for _, c := range clients {
		c.stop()
	}
}

func (s *Server) subscribe(ctx context.Context) {
	if s.Redis == nil {
		return
	}
	pubsub := s.Redis.Subscribe(ctx, notificationChannel)
	defer pubsub.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-pubsub.Channel():
			if !ok {
				return
			}
			if conv, err := s.conversation(ctx, event.Payload); err == nil && conv.Status != "ai" {
				s.cancelAI(conv.ID)
			}
			s.broadcast(ctx, event.Payload)
		}
	}
}

func (s *Server) broadcast(ctx context.Context, id string) {
	conv, err := s.conversation(ctx, id)
	if err != nil {
		return
	}
	s.clientsMu.RLock()
	clients := make([]*socketClient, 0, len(s.clients))
	for c := range s.clients {
		clients = append(clients, c)
	}
	s.clientsMu.RUnlock()
	for _, c := range clients {
		if s.canRead(ctx, c.actor, conv) || (c.actor.Role != "visitor" && conv.Status == "waiting") {
			c.enqueue(map[string]string{"type": "changed", "conversation_id": id})
		}
	}
}

// JSON 编解码保留为独立函数，便于验证消息协议而不启动网络服务。
func decodeSocketCommand(data []byte) (socketCommand, error) {
	var command socketCommand
	err := json.Unmarshal(data, &command)
	return command, err
}
