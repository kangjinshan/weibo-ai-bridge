package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/kangjinshan/weibo-ai-bridge/agent"
	"github.com/kangjinshan/weibo-ai-bridge/config"
	"github.com/kangjinshan/weibo-ai-bridge/platform/weibo"
	"github.com/kangjinshan/weibo-ai-bridge/router"
	"github.com/kangjinshan/weibo-ai-bridge/session"
)

var (
	logger *log.Logger

	version   = "dev"
	gitCommit = "unknown"
	buildTime = "unknown"
)

const (
	queueNoticeCooldown  = 10 * time.Second
	processingAckMessage = "已收到消息，正在处理中，请稍候。"
	messageQueuedHint    = "上一条消息还在处理中，这条消息已加入队列，会在当前回复结束后继续处理。"
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

type byTheWayInjector interface {
	InjectByTheWay(ctx context.Context, msg *weibo.Message) (bool, error)
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

	queueNoticeCooldown time.Duration

	mu              sync.Mutex
	inFlightUsers   map[string]struct{}
	activeRuns      map[string]activeRun
	queuedMessages  map[string][]*weibo.Message
	lastQueueNotice map[string]time.Time
	nextRunID       int64
}

type activeRun struct {
	id     int64
	cancel context.CancelFunc
}

type buildInfo struct {
	Version   string `json:"version"`
	GitCommit string `json:"git_commit"`
	BuildTime string `json:"build_time"`
}

func newMessageProcessor(platform replyPlatform, router messageHandler, logger *log.Logger) *messageProcessor {
	if logger == nil {
		logger = log.Default()
	}

	return &messageProcessor{
		platform:            platform,
		router:              router,
		logger:              logger,
		queueNoticeCooldown: queueNoticeCooldown,
		inFlightUsers:       make(map[string]struct{}),
		activeRuns:          make(map[string]activeRun),
		queuedMessages:      make(map[string][]*weibo.Message),
		lastQueueNotice:     make(map[string]time.Time),
	}
}

func newSessionManager(cfg *config.Config) *session.Manager {
	return session.NewManager(session.ManagerConfig{
		Timeout:     cfg.Session.Timeout,
		MaxSize:     cfg.Session.MaxSize,
		StoragePath: cfg.Session.StoragePath,
	})
}

func main() {
	// 加载配置
	cfg := config.Load()

	// 初始化日志
	initLogger(cfg.Log)

	logger.Printf("Build info: version=%s, git_commit=%s, build_time=%s", version, gitCommit, buildTime)
	logger.Printf("Configuration loaded: log_level=%s, log_format=%s", cfg.Log.Level, cfg.Log.Format)

	// 验证配置
	if err := cfg.Validate(); err != nil {
		logger.Fatalf("Configuration validation failed: %v", err)
	}

	// 创建上下文和取消函数
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 创建会话管理器
	sessionMgr := newSessionManager(cfg)
	logger.Printf("Session manager initialized: timeout=%ds, max_size=%d, storage_path=%s", cfg.Session.Timeout, cfg.Session.MaxSize, cfg.Session.StoragePath)

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
	platform, err := weibo.NewPlatform(cfg.Platform.Weibo.AppID, cfg.Platform.Weibo.Appsecret)
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
		Addr:         "127.0.0.1:" + port,
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
func initLogger(logCfg config.LogConfig) {
	var output io.Writer = os.Stdout

	switch strings.ToLower(strings.TrimSpace(logCfg.Output)) {
	case "stderr":
		output = os.Stderr
	case "stdout", "":
		output = os.Stdout
	default:
		if f, err := os.OpenFile(logCfg.Output, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			output = f
		} else {
			fmt.Fprintf(os.Stderr, "failed to open log file %q, falling back to stdout: %v\n", logCfg.Output, err)
			output = os.Stdout
		}
	}

	if strings.ToLower(strings.TrimSpace(logCfg.Format)) == "json" {
		output = &jsonLogWriter{w: output}
	}

	logger = log.New(output, "[weibo-ai-bridge] ", log.LstdFlags|log.Lshortfile)
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

	if p.tryInjectByTheWay(ctx, msg) {
		return
	}
	if p.tryHandleBusySlashCommand(ctx, msg) {
		return
	}

	startNow, queued := p.enqueue(msg)
	if queued {
		p.sendQueueNotice(ctx, msg.UserID, msg.ID)
		return
	}

	if startNow {
		go p.handle(ctx, msg)
	}
}

func (p *messageProcessor) enqueue(msg *weibo.Message) (startNow bool, queued bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, exists := p.inFlightUsers[msg.UserID]; exists {
		p.queuedMessages[msg.UserID] = append(p.queuedMessages[msg.UserID], msg)
		return false, true
	}

	p.inFlightUsers[msg.UserID] = struct{}{}
	return true, false
}

func (p *messageProcessor) clearUser(userID string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	delete(p.inFlightUsers, userID)
	delete(p.activeRuns, userID)
	delete(p.queuedMessages, userID)
	delete(p.lastQueueNotice, userID)
}

func (p *messageProcessor) nextQueued(userID string) *weibo.Message {
	p.mu.Lock()
	defer p.mu.Unlock()

	queue := p.queuedMessages[userID]
	if len(queue) == 0 {
		delete(p.inFlightUsers, userID)
		delete(p.activeRuns, userID)
		delete(p.lastQueueNotice, userID)
		return nil
	}

	next := queue[0]
	if len(queue) == 1 {
		delete(p.queuedMessages, userID)
		return next
	}

	p.queuedMessages[userID] = queue[1:]
	return next
}

func (p *messageProcessor) handle(ctx context.Context, msg *weibo.Message) {
	for current := msg; current != nil; current = p.nextQueued(current.UserID) {
		if ctx.Err() != nil {
			p.clearUser(current.UserID)
			return
		}

		runCtx, cancel, runID := p.beginRun(ctx, current.UserID)

		p.logger.Printf("Processing message: id=%s, type=%s, user=%s", current.ID, current.Type, current.UserID)

		if p.platform != nil && shouldSendProcessingAck(current) {
			if err := p.platform.Reply(runCtx, current.UserID, processingAckMessage); err != nil {
				p.logger.Printf("Failed to send processing ack: id=%s, user=%s, error=%v", current.ID, current.UserID, err)
			}
		}

		if err := p.router.HandleMessage(runCtx, current); err != nil {
			if !router.IsBenignCancellation(err) {
				p.logger.Printf("Failed to handle message: id=%s, error=%v", current.ID, err)
			}
			cancel()
			p.endRun(current.UserID, runID)
			continue
		}

		cancel()
		p.endRun(current.UserID, runID)
		p.logger.Printf("Message processed successfully: id=%s", current.ID)
	}
}

func (p *messageProcessor) beginRun(parent context.Context, userID string) (context.Context, context.CancelFunc, int64) {
	runCtx, cancel := context.WithCancel(parent)

	p.mu.Lock()
	defer p.mu.Unlock()

	p.nextRunID++
	runID := p.nextRunID
	p.activeRuns[userID] = activeRun{
		id:     runID,
		cancel: cancel,
	}

	return runCtx, cancel, runID
}

func (p *messageProcessor) tryInjectByTheWay(ctx context.Context, msg *weibo.Message) bool {
	if msg == nil || !isByTheWayMessage(msg.Content) {
		return false
	}

	p.mu.Lock()
	_, busy := p.inFlightUsers[msg.UserID]
	p.mu.Unlock()
	if !busy {
		return false
	}

	injector, ok := p.router.(byTheWayInjector)
	if !ok {
		return false
	}

	handled, err := injector.InjectByTheWay(ctx, msg)
	if err != nil {
		p.logger.Printf("Failed to inject /btw message: id=%s, error=%v", msg.ID, err)
		if p.platform != nil {
			if replyErr := p.platform.Reply(context.WithoutCancel(ctx), msg.UserID, err.Error()); replyErr != nil {
				p.logger.Printf("Failed to send /btw error reply: id=%s, error=%v", msg.ID, replyErr)
			}
		}
	}
	return handled
}

func (p *messageProcessor) tryHandleBusySlashCommand(ctx context.Context, msg *weibo.Message) bool {
	if msg == nil || !isSlashCommandMessage(msg.Content) || isByTheWayMessage(msg.Content) {
		return false
	}

	p.mu.Lock()
	_, busy := p.inFlightUsers[msg.UserID]
	p.mu.Unlock()
	if !busy {
		return false
	}

	go func() {
		if err := p.router.HandleMessage(ctx, msg); err != nil && !router.IsBenignCancellation(err) {
			p.logger.Printf("Failed to handle slash command immediately: id=%s, error=%v", msg.ID, err)
		}
	}()

	return true
}

func (p *messageProcessor) endRun(userID string, runID int64) {
	p.mu.Lock()
	defer p.mu.Unlock()

	run, ok := p.activeRuns[userID]
	if !ok || run.id != runID {
		return
	}

	delete(p.activeRuns, userID)
}

func (p *messageProcessor) sendQueueNotice(ctx context.Context, userID, messageID string) {
	if p.platform == nil {
		return
	}

	now := time.Now()

	p.mu.Lock()
	lastNoticeAt := p.lastQueueNotice[userID]
	if !lastNoticeAt.IsZero() && now.Sub(lastNoticeAt) < p.queueNoticeCooldown {
		p.mu.Unlock()
		return
	}
	p.lastQueueNotice[userID] = now
	p.mu.Unlock()

	go func() {
		if err := p.platform.Reply(ctx, userID, messageQueuedHint); err != nil {
			p.logger.Printf("Failed to send queue notice: id=%s, user=%s, error=%v", messageID, userID, err)
		}
	}()
}

func shouldSendProcessingAck(msg *weibo.Message) bool {
	if msg == nil {
		return false
	}

	content := strings.TrimSpace(msg.Content)
	if content == "" {
		return true
	}

	first, _ := utf8.DecodeRuneInString(content)
	return first != '/'
}

func isByTheWayMessage(content string) bool {
	content = strings.TrimSpace(content)
	return strings.HasPrefix(content, "/btw")
}

func isSlashCommandMessage(content string) bool {
	content = strings.TrimSpace(content)
	return strings.HasPrefix(content, "/")
}

// healthHandler 健康检查处理器
func healthHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	payload, err := json.Marshal(map[string]interface{}{
		"status":  "ok",
		"service": "weibo-ai-bridge",
		"build":   currentBuildInfo(),
	})
	if err != nil {
		http.Error(w, "Failed to build health response", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(payload)
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
			"build":     currentBuildInfo(),
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

func currentBuildInfo() buildInfo {
	info := buildInfo{
		Version:   strings.TrimSpace(version),
		GitCommit: strings.TrimSpace(gitCommit),
		BuildTime: strings.TrimSpace(buildTime),
	}

	if info.Version == "" {
		info.Version = "dev"
	}
	if info.GitCommit == "" {
		info.GitCommit = "unknown"
	}
	if info.BuildTime == "" {
		info.BuildTime = "unknown"
	}

	return info
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

type jsonLogWriter struct {
	w io.Writer
}

func (j *jsonLogWriter) Write(p []byte) (int, error) {
	entry := map[string]string{
		"ts":  time.Now().Format(time.RFC3339Nano),
		"msg": strings.TrimRight(string(p), "\n"),
		"app": "weibo-ai-bridge",
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return j.w.Write(p)
	}
	data = append(data, '\n')
	n, err := j.w.Write(data)
	if err != nil {
		return n, err
	}
	return len(p), nil
}
