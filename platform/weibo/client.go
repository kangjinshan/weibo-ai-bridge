package weibo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/websocket"
)

const (
	defaultTokenURL = "http://open-im.api.weibo.com/open/auth/ws_token"
	defaultWSURL    = "ws://open-im.api.weibo.com/ws/stream"
	maxWeiboChunk   = 4000
)

// Platform 微博平台适配器
type Platform struct {
	appID     string
	appSecret string
	tokenURL  string
	wsURL     string

	httpClient *http.Client

	conn      *websocket.Conn
	connMutex sync.Mutex

	token       string
	tokenExpire time.Time
	tokenMutex  sync.Mutex

	running bool
	ctx     context.Context
	cancel  context.CancelFunc

	messageChan chan *Message
	logger      *log.Logger

	dedupMu sync.Mutex
	dedup   map[string]time.Time
}

// NewPlatform 创建微博平台实例
func NewPlatform(appID, appSecret string) (*Platform, error) {
	if appID == "" {
		return nil, fmt.Errorf("weibo: app_id is required")
	}
	if appSecret == "" {
		return nil, fmt.Errorf("weibo: app_secret is required")
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &Platform{
		appID:       appID,
		appSecret:   appSecret,
		tokenURL:    defaultTokenURL,
		wsURL:       defaultWSURL,
		httpClient:  &http.Client{Timeout: 30 * time.Second},
		messageChan: make(chan *Message, 100),
		ctx:         ctx,
		cancel:      cancel,
		logger:      log.Default(),
		dedup:       make(map[string]time.Time),
	}, nil
}

// Configure 使用外部配置覆盖默认平台参数
func (p *Platform) Configure(tokenURL, wsURL string, timeout time.Duration) {
	if strings.TrimSpace(tokenURL) != "" {
		p.tokenURL = tokenURL
	}
	if strings.TrimSpace(wsURL) != "" {
		p.wsURL = wsURL
	}
	if timeout > 0 {
		p.httpClient.Timeout = timeout
	}
}

// refreshToken 刷新 Token
func (p *Platform) refreshToken(ctx context.Context) error {
	// 构建请求体（JSON 格式）
	payload := map[string]string{
		"app_id":     p.appID,
		"app_secret": p.appSecret,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal token request: %w", err)
	}

	// POST 请求获取 token
	resp, err := p.httpClient.Post(p.tokenURL, "application/json", strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("fetch token: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	// 解析响应
	var tokenResp struct {
		Code int    `json:"code"`
		Msg  string `json:"message"`
		Data struct {
			UID      int64  `json:"uid"`
			Token    string `json:"token"`
			ExpireIn int    `json:"expire_in"`
		} `json:"data"`
	}

	if err := json.Unmarshal(respBody, &tokenResp); err != nil {
		return fmt.Errorf("parse token response: %w", err)
	}

	if tokenResp.Code != 0 {
		return fmt.Errorf("token error: %s (code: %d)", tokenResp.Msg, tokenResp.Code)
	}

	p.tokenMutex.Lock()
	p.token = tokenResp.Data.Token
	p.tokenExpire = time.Now().Add(time.Duration(tokenResp.Data.ExpireIn-60) * time.Second)
	p.tokenMutex.Unlock()

	p.logger.Printf("✅ Token refreshed successfully")
	if len(p.token) > 20 {
		p.logger.Printf("   Token: %s...", p.token[:20])
	} else {
		p.logger.Printf("   Token: %s", p.token)
	}
	p.logger.Printf("   Expires in: %d seconds", tokenResp.Data.ExpireIn)
	return nil
}

// connect 建立 WebSocket 连接
func (p *Platform) connect() error {
	p.connMutex.Lock()
	defer p.connMutex.Unlock()

	// 构建连接 URL（需要 app_id 和 token）
	url := fmt.Sprintf("%s?app_id=%s&token=%s", p.wsURL, p.appID, p.token)

	// 配置 WebSocket
	config, err := websocket.NewConfig(url, "http://localhost/")
	if err != nil {
		return err
	}

	// 建立 WebSocket 连接
	conn, err := websocket.DialConfig(config)
	if err != nil {
		return err
	}

	p.conn = conn
	p.logger.Printf("✅ WebSocket connected to: %s", p.wsURL)

	return nil
}

// Start 启动平台
func (p *Platform) Start(ctx context.Context) error {
	p.logger.Printf("🚀 Starting platform with app_id: %s", p.appID)

	// 获取 token
	if err := p.refreshToken(ctx); err != nil {
		return fmt.Errorf("failed to get token: %w", err)
	}

	// 连接 WebSocket
	if err := p.connect(); err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}

	p.running = true
	p.logger.Printf("✅ Platform started successfully")

	// 启动心跳
	go p.heartbeatLoop(ctx)

	// 启动消息循环
	go p.messageLoop(ctx)

	return nil
}

// Stop 停止平台
func (p *Platform) Stop() error {
	p.connMutex.Lock()
	defer p.connMutex.Unlock()

	if p.conn != nil {
		if err := p.conn.Close(); err != nil {
			return err
		}
	}

	p.cancel()
	p.running = false
	p.logger.Printf("Platform stopped")
	return nil
}

// IsRunning 检查运行状态
func (p *Platform) IsRunning() bool {
	return p.running
}

// Messages 获取消息通道
func (p *Platform) Messages() <-chan *Message {
	return p.messageChan
}

// Reply 回复消息
func (p *Platform) Reply(ctx context.Context, userID string, content string) error {
	return p.sendChunks(ctx, userID, content)
}

// sendChunks 分块发送消息
func (p *Platform) sendChunks(ctx context.Context, userID string, content string) error {
	p.connMutex.Lock()
	defer p.connMutex.Unlock()

	if p.conn == nil {
		return fmt.Errorf("connection not established")
	}

	// 分块发送（微博限制 4000 字符）
	chunks := splitContent(content, maxWeiboChunk)

	for i, chunk := range chunks {
		done := i == len(chunks)-1

		// 使用微博要求的格式
		msg := map[string]interface{}{
			"type": "send_message",
			"payload": map[string]interface{}{
				"toUserId":  userID,
				"text":      chunk,
				"messageId": generateMessageID(),
				"chunkId":   i,
				"done":      done,
			},
		}

		data, err := json.Marshal(msg)
		if err != nil {
			return err
		}

		if err := websocket.Message.Send(p.conn, string(data)); err != nil {
			return err
		}

		// 避免发送太快
		time.Sleep(100 * time.Millisecond)
	}

	return nil
}

// generateMessageID 生成消息 ID
func generateMessageID() string {
	return fmt.Sprintf("msg_%d", time.Now().UnixNano())
}

// heartbeatLoop 心跳循环
func (p *Platform) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.connMutex.Lock()
			if p.conn != nil {
				pingMsg := map[string]interface{}{"type": "ping"}
				data, err := json.Marshal(pingMsg)
				if err == nil {
					if err := websocket.Message.Send(p.conn, string(data)); err != nil {
						p.logger.Printf("❌ Send ping failed: %v", err)
					}
				}
			}
			p.connMutex.Unlock()
		}
	}
}

// messageLoop 消息循环
func (p *Platform) messageLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			p.connMutex.Lock()
			conn := p.conn
			p.connMutex.Unlock()

			if conn == nil {
				select {
				case <-ctx.Done():
					return
				case <-time.After(100 * time.Millisecond):
					continue
				}
			}

			var data string
			err := websocket.Message.Receive(conn, &data)
			if err != nil {
				p.logger.Printf("❌ Read message error: %v", err)
				// 尝试重连
				if err := p.reconnect(ctx); err != nil {
					p.logger.Printf("❌ Reconnect failed: %v", err)
					time.Sleep(5 * time.Second)
				}
				continue
			}

			// 跳过心跳响应（纯文本格式）
			if data == "pong" {
				continue
			}

			// 解析 JSON
			var rawMsg map[string]interface{}
			if err := json.Unmarshal([]byte(data), &rawMsg); err != nil {
				p.logger.Printf("❌ Parse JSON error: %v (data: %s)", err, data)
				continue
			}

			// 跳过系统消息
			if msgType, ok := rawMsg["type"].(string); ok {
				if msgType == "connected" {
					continue
				}
			}

			// 解析消息
			msg, err := ParseMessage(rawMsg)
			if err != nil {
				p.logger.Printf("❌ Parse message error: %v", err)
				continue
			}

			// 去重
			if p.isDuplicate(msg) {
				continue
			}

			// 发送到消息通道
			select {
			case p.messageChan <- msg:
				p.logger.Printf("📨 Received message from user: %s", msg.UserID)
			default:
				p.logger.Printf("⚠️  Message channel full, dropping message")
			}
		}
	}
}

// isDuplicate 检查消息是否重复
func (p *Platform) isDuplicate(msg *Message) bool {
	p.dedupMu.Lock()
	defer p.dedupMu.Unlock()

	key := fmt.Sprintf("%s-%d", msg.UserID, msg.Timestamp)
	if last, exists := p.dedup[key]; exists && time.Since(last) < 5*time.Minute {
		return true
	}

	p.dedup[key] = time.Now()

	// 清理旧记录
	for k, t := range p.dedup {
		if time.Since(t) > 5*time.Minute {
			delete(p.dedup, k)
		}
	}

	return false
}

// reconnect 重新连接
func (p *Platform) reconnect(ctx context.Context) error {
	// 刷新 token
	if err := p.refreshToken(ctx); err != nil {
		return err
	}

	// 重新连接
	return p.connect()
}

// splitContent 分割内容
func splitContent(content string, maxLen int) []string {
	if len(content) <= maxLen {
		return []string{content}
	}

	var chunks []string
	for len(content) > 0 {
		if len(content) <= maxLen {
			chunks = append(chunks, content)
			break
		}

		// 尝试在合适的点分割（如换行符）
		split := maxLen
		for i := maxLen - 1; i > maxLen-100 && i >= 0; i-- {
			if content[i] == '\n' {
				split = i + 1
				break
			}
		}

		chunks = append(chunks, content[:split])
		content = content[split:]
	}

	return chunks
}
