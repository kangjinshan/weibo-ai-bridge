package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/yourusername/weibo-ai-bridge/agent"
	"github.com/yourusername/weibo-ai-bridge/config"
	"github.com/yourusername/weibo-ai-bridge/platform/weibo"
	"github.com/yourusername/weibo-ai-bridge/router"
	"github.com/yourusername/weibo-ai-bridge/session"
)

var (
	logger *log.Logger
)

const (
	busyNoticeCooldown        = 10 * time.Second
	processingAckMessage      = "已收到消息，正在处理中，请稍候。"
	messageAlreadyRunningHint = "上一条消息还在处理中，这条消息先不排队处理；请等当前回复结束后再发送。"
)

type messageSource interface {
	Messages() <-chan *weibo.Message
}

type replyPlatform interface {
	Reply(ctx context.Context, userID string, content string) error
}

type messagePlatform interface {
	messageSource
	replyPlatform
}

type messageHandler interface {
	HandleMessage(ctx context.Context, msg *weibo.Message) error
}

type prefixedMessageHandler interface {
	HandleMessageWithPrefix(ctx context.Context, msg *weibo.Message, prefix string) error
}

type chatStreamRequest struct {
	UserID    string `json:"user_id"`
	Content   string `json:"content"`
	SessionID string `json:"session_id"`
}

type messageProcessor struct {
	platform replyPlatform
	router   messageHandler
	logger   *log.Logger

	busyCooldown time.Duration

	mu             sync.Mutex
	inFlightUsers  map[string]struct{}
	lastBusyNotice map[string]time.Time
}

func newMessageProcessor(platform replyPlatform, router messageHandler, logger *log.Logger) *messageProcessor {
	if logger == nil {
		logger = log.Default()
	}

	return &messageProcessor{
		platform:       platform,
		router:         router,
		logger:         logger,
		busyCooldown:   busyNoticeCooldown,
		inFlightUsers:  make(map[string]struct{}),
		lastBusyNotice: make(map[string]time.Time),
	}
}

func main() {
	// 初始化日志
	initLogger()

	// 加载配置
	cfg := config.Load()
	logger.Printf("Configuration loaded: log_level=%s, log_format=%s", cfg.Log.Level, cfg.Log.Format)

	// 验证配置
	if err := cfg.Validate(); err != nil {
		logger.Fatalf("Configuration validation failed: %v", err)
	}

	// 创建上下文和取消函数
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 创建会话管理器
	sessionMgr := session.NewManager(session.ManagerConfig{
		Timeout: cfg.Session.Timeout,
		MaxSize: cfg.Session.MaxSize,
	})
	logger.Printf("Session manager initialized: timeout=%ds, max_size=%d", cfg.Session.Timeout, cfg.Session.MaxSize)

	// 创建 Agent 管理器并注册 Agent
	agentMgr := agent.NewManager()
	var defaultAgent string

	// 注册 Claude Agent
	if cfg.Agent.Claude.Enabled {
		claudeAgent := agent.NewClaudeCodeAgent()
		agentMgr.Register(claudeAgent)
		defaultAgent = "claude-code"
		agentMgr.SetDefault("claude-code")
		logger.Printf("Claude agent registered: model=%s", cfg.Agent.Claude.Model)
	}

	// 注册 Codex Agent（如果启用）
	if cfg.Agent.Codex.Enabled {
		codexAgent := agent.NewCodeXAgent(cfg.Agent.Codex.Model)
		agentMgr.Register(codexAgent)
		if defaultAgent == "" {
			defaultAgent = "codex"
			agentMgr.SetDefault("codex")
		}
		logger.Printf("Codex agent registered: model=%s", cfg.Agent.Codex.Model)
	}

	if defaultAgent == "" {
		logger.Fatalf("No agent enabled, please enable at least one agent (claude or codex)")
	}

	logger.Printf("Agent manager initialized: count=%d, default=%s", agentMgr.Count(), defaultAgent)

	// 创建微博平台适配器
	platform, err := weibo.NewPlatform(cfg.Platform.Weibo.AppID, cfg.Platform.Weibo.AppSecret)
	if err != nil {
		logger.Fatalf("Failed to create platform: %v", err)
	}
	platform.Configure(
		cfg.Platform.Weibo.TokenURL,
		cfg.Platform.Weibo.WSURL,
		time.Duration(cfg.Platform.Weibo.Timeout)*time.Second,
	)
	logger.Printf("Platform created: app_id=%s", cfg.Platform.Weibo.AppID)

	// 创建消息路由器
	msgRouter := router.NewRouter(platform, sessionMgr, agentMgr)
	logger.Printf("Message router created")

	// 启动平台
	if err := platform.Start(ctx); err != nil {
		logger.Fatalf("Failed to start platform: %v", err)
	}
	logger.Printf("Platform started successfully")

	// 启动消息处理循环
	go processMessages(ctx, platform, msgRouter)

	// 设置 HTTP 路由
	mux := http.NewServeMux()
	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("/stats", statsHandler(sessionMgr, agentMgr))
	mux.HandleFunc("/chat/stream", chatStreamHandler(msgRouter))

	// 获取服务端口
	port := os.Getenv("SERVER_PORT")
	if port == "" {
		port = "5533" // 默认端口
	}

	// 创建 HTTP 服务器
	server := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// 启动 HTTP 服务器（在 goroutine 中）
	go func() {
		logger.Printf("HTTP server starting on %s", server.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatalf("HTTP server error: %v", err)
		}
	}()

	// 等待中断信号
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Println("Shutdown signal received, shutting down gracefully...")

	// 创建超时上下文用于优雅关闭
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	// 停止平台
	platform.Stop()
	logger.Println("Platform stopped")

	// 关闭 HTTP 服务器
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Printf("HTTP server shutdown error: %v", err)
	} else {
		logger.Println("HTTP server shutdown completed")
	}

	// 取消主上下文
	cancel()

	logger.Println("Server shutdown completed")
}

// initLogger 初始化日志
func initLogger() {
	logger = log.New(os.Stdout, "[weibo-ai-bridge] ", log.LstdFlags|log.Lshortfile)
}

// processMessages 处理消息循环
func processMessages(ctx context.Context, platform messagePlatform, msgRouter messageHandler) {
	processor := newMessageProcessor(platform, msgRouter, logger)

	for {
		select {
		case <-ctx.Done():
			logger.Println("Message processing stopped")
			return
		case msg, ok := <-platform.Messages():
			if !ok {
				logger.Println("Message channel closed")
				return
			}

			processor.dispatch(ctx, msg)
		}
	}
}

func (p *messageProcessor) dispatch(ctx context.Context, msg *weibo.Message) {
	if msg == nil {
		return
	}

	if !p.tryStart(msg.UserID) {
		p.sendBusyNotice(ctx, msg.UserID, msg.ID)
		return
	}

	go p.handle(ctx, msg)
}

func (p *messageProcessor) tryStart(userID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, exists := p.inFlightUsers[userID]; exists {
		return false
	}

	p.inFlightUsers[userID] = struct{}{}
	return true
}

func (p *messageProcessor) finish(userID string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	delete(p.inFlightUsers, userID)
}

func (p *messageProcessor) handle(ctx context.Context, msg *weibo.Message) {
	defer p.finish(msg.UserID)

	p.logger.Printf("Processing message: id=%s, type=%s, user=%s", msg.ID, msg.Type, msg.UserID)

	if prefixedRouter, ok := p.router.(prefixedMessageHandler); ok {
		if err := prefixedRouter.HandleMessageWithPrefix(ctx, msg, processingAckMessage); err != nil {
			p.logger.Printf("Failed to handle message: id=%s, error=%v", msg.ID, err)
			return
		}
		p.logger.Printf("Message processed successfully: id=%s", msg.ID)
		return
	}

	if p.platform != nil {
		if err := p.platform.Reply(ctx, msg.UserID, processingAckMessage); err != nil {
			p.logger.Printf("Failed to send processing ack: id=%s, user=%s, error=%v", msg.ID, msg.UserID, err)
		}
	}

	if err := p.router.HandleMessage(ctx, msg); err != nil {
		p.logger.Printf("Failed to handle message: id=%s, error=%v", msg.ID, err)
		return
	}

	p.logger.Printf("Message processed successfully: id=%s", msg.ID)
}

func (p *messageProcessor) sendBusyNotice(ctx context.Context, userID, messageID string) {
	if p.platform == nil {
		return
	}

	now := time.Now()

	p.mu.Lock()
	lastNoticeAt := p.lastBusyNotice[userID]
	if !lastNoticeAt.IsZero() && now.Sub(lastNoticeAt) < p.busyCooldown {
		p.mu.Unlock()
		return
	}
	p.lastBusyNotice[userID] = now
	p.mu.Unlock()

	go func() {
		if err := p.platform.Reply(ctx, userID, messageAlreadyRunningHint); err != nil {
			p.logger.Printf("Failed to send busy notice: id=%s, user=%s, error=%v", messageID, userID, err)
		}
	}()
}

// healthHandler 健康检查处理器
func healthHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"status":"ok","service":"weibo-ai-bridge"}`)
}

// statsHandler 统计信息处理器
func statsHandler(sessionMgr *session.Manager, agentMgr *agent.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		stats := map[string]interface{}{
			"sessions": map[string]interface{}{
				"count": sessionMgr.Count(),
			},
			"agents": map[string]interface{}{
				"count": agentMgr.Count(),
				"list":  getAgentNames(agentMgr),
			},
			"timestamp": time.Now().Unix(),
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(stats); err != nil {
			logger.Printf("Failed to encode stats: %v", err)
		}
	}
}

func chatStreamHandler(msgRouter *router.Router) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		req, err := parseChatStreamRequest(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if strings.TrimSpace(req.UserID) == "" {
			http.Error(w, "user_id is required", http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(req.Content) == "" {
			http.Error(w, "content is required", http.StatusBadRequest)
			return
		}

		stream, err := msgRouter.Stream(r.Context(), &router.Message{
			ID:        generateHTTPMessageID(),
			Type:      router.TypeText,
			Content:   req.Content,
			UserID:    req.UserID,
			SessionID: req.SessionID,
			Metadata: map[string]interface{}{
				"source": "http_sse",
			},
		})
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to start stream: %v", err), http.StatusInternalServerError)
			return
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)

		for event := range stream {
			if err := writeSSEEvent(w, event); err != nil {
				logger.Printf("Failed to write SSE event: %v", err)
				return
			}
			flusher.Flush()
		}
	}
}

func parseChatStreamRequest(r *http.Request) (*chatStreamRequest, error) {
	switch r.Method {
	case http.MethodGet:
		query := r.URL.Query()
		return &chatStreamRequest{
			UserID:    query.Get("user_id"),
			Content:   query.Get("content"),
			SessionID: query.Get("session_id"),
		}, nil
	case http.MethodPost:
		defer r.Body.Close()
		var req chatStreamRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return nil, fmt.Errorf("invalid request body: %w", err)
		}
		return &req, nil
	default:
		return nil, fmt.Errorf("unsupported method")
	}
}

func writeSSEEvent(w http.ResponseWriter, event agent.Event) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}

	if _, err := fmt.Fprintf(w, "event: %s\n", event.Type); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
		return err
	}

	return nil
}

func generateHTTPMessageID() string {
	return fmt.Sprintf("http_%d", time.Now().UnixNano())
}

// getAgentNames 获取所有 Agent 名称
func getAgentNames(agentMgr *agent.Manager) []string {
	agents := agentMgr.ListAgents()
	names := make([]string, 0, len(agents))
	for _, a := range agents {
		names = append(names, a.Name())
	}
	return names
}
