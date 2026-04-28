package main

import (
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/kangjinshan/weibo-ai-bridge/agent"
	"github.com/kangjinshan/weibo-ai-bridge/config"
	"github.com/kangjinshan/weibo-ai-bridge/platform/weibo"
	"github.com/kangjinshan/weibo-ai-bridge/router"
	"github.com/kangjinshan/weibo-ai-bridge/session"
)

type processorTestPlatform struct {
	messages chan *weibo.Message

	mu      sync.Mutex
	replies []string
}

func newProcessorTestPlatform() *processorTestPlatform {
	return &processorTestPlatform{
		messages: make(chan *weibo.Message, 10),
	}
}

func (p *processorTestPlatform) Messages() <-chan *weibo.Message {
	return p.messages
}

func (p *processorTestPlatform) Reply(ctx context.Context, userID string, content string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.replies = append(p.replies, userID+":"+content)
	return nil
}

func (p *processorTestPlatform) Replies() []string {
	p.mu.Lock()
	defer p.mu.Unlock()

	return append([]string(nil), p.replies...)
}

type processorTestRouter struct {
	delay    time.Duration
	started  chan string
	canceled chan string
	injected chan string
	release  <-chan struct{}
}

type sseTestAgent struct {
	name      string
	available bool
	streamFn  func(ctx context.Context, sessionID string, input string) (<-chan agent.Event, error)
}

func (a *sseTestAgent) Name() string {
	return a.name
}

func (a *sseTestAgent) Execute(ctx context.Context, sessionID string, input string) (string, error) {
	stream, err := a.ExecuteStream(ctx, sessionID, input)
	if err != nil {
		return "", err
	}

	var parts []string
	var latestSessionID string
	for event := range stream {
		switch event.Type {
		case agent.EventTypeSession:
			latestSessionID = event.SessionID
		case agent.EventTypeMessage:
			parts = append(parts, event.Content)
		case agent.EventTypeError:
			return "", assert.AnError
		}
	}

	response := strings.Join(parts, "\n")
	if latestSessionID != "" {
		response += "\n\n__SESSION_ID__: " + latestSessionID
	}
	return response, nil
}

func (a *sseTestAgent) ExecuteStream(ctx context.Context, sessionID string, input string) (<-chan agent.Event, error) {
	if a.streamFn != nil {
		return a.streamFn(ctx, sessionID, input)
	}

	events := make(chan agent.Event, 2)
	go func() {
		defer close(events)
		events <- agent.Event{Type: agent.EventTypeMessage, Content: "ok"}
		events <- agent.Event{Type: agent.EventTypeDone}
	}()
	return events, nil
}

func (a *sseTestAgent) IsAvailable() bool {
	return a.available
}

func (r *processorTestRouter) HandleMessage(ctx context.Context, msg *weibo.Message) error {
	if r.started != nil {
		r.started <- msg.UserID + ":" + msg.ID
	}

	if r.release != nil {
		select {
		case <-r.release:
		case <-ctx.Done():
			if r.canceled != nil {
				r.canceled <- msg.UserID + ":" + msg.ID
			}
			return ctx.Err()
		}
	}

	if r.delay > 0 {
		select {
		case <-time.After(r.delay):
		case <-ctx.Done():
			if r.canceled != nil {
				r.canceled <- msg.UserID + ":" + msg.ID
			}
			return ctx.Err()
		}
	}

	return nil
}

func (r *processorTestRouter) InjectByTheWay(ctx context.Context, msg *weibo.Message) (bool, error) {
	if msg == nil || !strings.HasPrefix(strings.TrimSpace(msg.Content), "/btw") {
		return false, nil
	}
	if r.injected != nil {
		r.injected <- msg.UserID + ":" + msg.ID
	}
	return true, nil
}

func TestMainInitialization(t *testing.T) {
	// 设置测试环境变量
	os.Setenv("WEIBO_APP_ID", "test-app-id")
	os.Setenv("WEIBO_APP_Secret", "test-Secret")
	os.Setenv("CLAUDE_ENABLED", "true")
	os.Setenv("LOG_LEVEL", "debug")
	defer func() {
		os.Unsetenv("WEIBO_APP_ID")
		os.Unsetenv("WEIBO_APP_Secret")
		os.Unsetenv("CLAUDE_ENABLED")
		os.Unsetenv("LOG_LEVEL")
	}()

	// 测试配置加载
	cfg := config.Load()
	assert.NotNil(t, cfg, "config.Load() returned nil")

	// 验证配置
	assert.Equal(t, "test-app-id", cfg.Platform.Weibo.AppID)
	assert.Equal(t, "test-Secret", cfg.Platform.Weibo.Appsecret)
	assert.Equal(t, "debug", cfg.Log.Level)
}

func TestComponentInitialization(t *testing.T) {
	// 测试会话管理器初始化
	sessionMgr := session.NewManager(session.ManagerConfig{
		Timeout: 3600,
		MaxSize: 1000,
	})
	assert.NotNil(t, sessionMgr, "Failed to create session manager")

	// 测试创建会话
	testSession := sessionMgr.Create("test-session-id", "test-user-id", "claude")
	assert.NotNil(t, testSession, "Failed to create session")
	assert.Equal(t, "test-session-id", testSession.ID)

	// 测试 Agent 管理器初始化
	agentMgr := agent.NewManager()
	assert.NotNil(t, agentMgr, "Failed to create agent manager")
	assert.Equal(t, 0, agentMgr.Count())
}

func TestNewSessionManager_UsesConfiguredStoragePath(t *testing.T) {
	storagePath := filepath.Join(t.TempDir(), "sessions")
	cfg := &config.Config{
		Session: config.SessionConfig{
			Timeout:     3600,
			MaxSize:     1000,
			StoragePath: storagePath,
		},
	}

	sessionMgr := newSessionManager(cfg)
	created := sessionMgr.Create("user-1-1", "user-1", "codex")
	assert.NotNil(t, created)

	reloaded := session.NewManager(session.ManagerConfig{
		Timeout:     cfg.Session.Timeout,
		MaxSize:     cfg.Session.MaxSize,
		StoragePath: cfg.Session.StoragePath,
	})

	restored, exists := reloaded.Get(created.ID)
	assert.True(t, exists)
	assert.Equal(t, created.ID, restored.ID)
}

func TestHealthHandler(t *testing.T) {
	tests := []struct {
		name           string
		method         string
		expectedStatus int
		expectedBody   string
	}{
		{
			name:           "GET 请求成功",
			method:         http.MethodGet,
			expectedStatus: http.StatusOK,
			expectedBody:   `{"status":"ok","service":"weibo-ai-bridge"}`,
		},
		{
			name:           "POST 请求失败",
			method:         http.MethodPost,
			expectedStatus: http.StatusMethodNotAllowed,
			expectedBody:   "Method not allowed\n",
		},
		{
			name:           "PUT 请求失败",
			method:         http.MethodPut,
			expectedStatus: http.StatusMethodNotAllowed,
			expectedBody:   "Method not allowed\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/health", nil)
			w := httptest.NewRecorder()

			healthHandler(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			assert.Equal(t, tt.expectedBody, w.Body.String())
		})
	}
}

func TestGracefulShutdown(t *testing.T) {
	// 模拟优雅关闭场景
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	shutdownCalled := false
	shutdownChan := make(chan struct{})

	// 模拟 shutdown 函数
	shutdown := func() {
		shutdownCalled = true
		close(shutdownChan)
	}

	// 模拟服务器启动后立即关闭
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	// 等待关闭信号
	select {
	case <-ctx.Done():
		shutdown()
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for shutdown")
	}

	assert.True(t, shutdownCalled, "Shutdown was not called")
}

func TestConfigValidation(t *testing.T) {
	// 测试缺少必需配置的情况
	t.Setenv("CONFIG_PATH", "/tmp/non-existent-weibo-ai-bridge-config.toml")
	os.Unsetenv("WEIBO_APP_ID")
	os.Unsetenv("WEIBO_APP_Secret")
	os.Unsetenv("WEIBO_APP_SECRET")
	defer func() {
		os.Setenv("WEIBO_APP_ID", "test-app-id")
		os.Setenv("WEIBO_APP_Secret", "test-Secret")
	}()

	cfg := config.Load()
	err := cfg.Validate()

	assert.Error(t, err, "Expected validation error for missing required config")
	assert.Contains(t, err.Error(), "weibo.app_id")
}

func TestServerErrorHandling(t *testing.T) {
	// 测试服务器错误处理
	// 使用无效端口启动服务器应该失败
	server := &http.Server{
		Addr:    "invalid-address",
		Handler: nil,
	}

	errChan := make(chan error, 1)
	go func() {
		errChan <- server.ListenAndServe()
	}()

	select {
	case err := <-errChan:
		assert.Error(t, err, "Expected error for invalid address")
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for server error")
	}
}

func TestComponentIntegration(t *testing.T) {
	// 测试组件之间的集成
	cfg := config.Load()

	// 创建会话管理器
	sessionMgr := session.NewManager(session.ManagerConfig{
		Timeout: cfg.Session.Timeout,
		MaxSize: cfg.Session.MaxSize,
	})

	// 创建 Agent 管理器
	agentMgr := agent.NewManager()

	// 创建会话
	session := sessionMgr.Create("test-session", "test-user", "claude")
	assert.NotNil(t, session, "Failed to create session")
	assert.Equal(t, "claude", session.AgentType)
	assert.Equal(t, "active", string(session.State))

	// 验证 Agent 管理器
	assert.Equal(t, 0, agentMgr.Count())

	// 获取会话
	retrieved, exists := sessionMgr.Get("test-session")
	assert.True(t, exists, "Failed to retrieve session")
	assert.Equal(t, session.ID, retrieved.ID)
}

func TestLogLevels(t *testing.T) {
	testCases := []struct {
		level    string
		expected bool
	}{
		{"debug", true},
		{"info", true},
		{"warn", true},
		{"error", true},
		{"invalid", false},
	}

	for _, tc := range testCases {
		t.Run(tc.level, func(t *testing.T) {
			os.Setenv("LOG_LEVEL", tc.level)
			cfg := config.Load()
			err := cfg.Validate()

			if tc.expected {
				if err != nil && strings.Contains(err.Error(), "log level") {
					t.Errorf("Expected valid log level %s, got error: %v", tc.level, err)
				}
			} else {
				assert.Error(t, err, "Expected validation error for invalid log level %s", tc.level)
			}
		})
	}
}

func TestMessageProcessor_QueuesRepeatedUserMessages(t *testing.T) {
	platform := newProcessorTestPlatform()
	release := make(chan struct{}, 2)
	router := &processorTestRouter{
		started: make(chan string, 2),
		release: release,
	}

	processor := newMessageProcessor(platform, router, log.New(os.Stdout, "", 0))
	processor.queueNoticeCooldown = time.Millisecond

	ctx := context.Background()
	processor.dispatch(ctx, &weibo.Message{ID: "msg-1", UserID: "user-1"})

	select {
	case started := <-router.started:
		assert.Equal(t, "user-1:msg-1", started)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("first message did not start")
	}

	processor.dispatch(ctx, &weibo.Message{ID: "msg-2", UserID: "user-1"})

	assert.Eventually(t, func() bool {
		replies := platform.Replies()
		return len(replies) >= 2 &&
			strings.Contains(replies[0], processingAckMessage) &&
			strings.Contains(replies[len(replies)-1], messageQueuedHint)
	}, time.Second, 20*time.Millisecond)

	select {
	case started := <-router.started:
		t.Fatalf("second message should remain queued before the first finishes, got %s", started)
	case <-time.After(150 * time.Millisecond):
	}

	release <- struct{}{}

	select {
	case started := <-router.started:
		assert.Equal(t, "user-1:msg-2", started)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("queued message did not start after the first finished")
	}

	assert.Eventually(t, func() bool {
		replies := platform.Replies()
		return len(replies) >= 3 &&
			strings.Contains(replies[2], processingAckMessage)
	}, time.Second, 20*time.Millisecond)

	release <- struct{}{}
}

func TestMessageProcessor_SendsImmediateAckForSlowRequests(t *testing.T) {
	platform := newProcessorTestPlatform()
	router := &processorTestRouter{
		delay: 120 * time.Millisecond,
	}

	processor := newMessageProcessor(platform, router, log.New(os.Stdout, "", 0))

	processor.dispatch(context.Background(), &weibo.Message{ID: "msg-slow", UserID: "user-slow"})

	assert.Eventually(t, func() bool {
		replies := platform.Replies()
		return len(replies) == 1 && strings.Contains(replies[0], processingAckMessage)
	}, 200*time.Millisecond, 10*time.Millisecond)
}

func TestMessageProcessor_SendsImmediateAckForFastRequests(t *testing.T) {
	platform := newProcessorTestPlatform()
	router := &processorTestRouter{
		delay: 20 * time.Millisecond,
	}

	processor := newMessageProcessor(platform, router, log.New(os.Stdout, "", 0))

	processor.dispatch(context.Background(), &weibo.Message{ID: "msg-fast", UserID: "user-fast"})

	assert.Eventually(t, func() bool {
		replies := platform.Replies()
		return len(replies) == 1 && strings.Contains(replies[0], processingAckMessage)
	}, 200*time.Millisecond, 10*time.Millisecond)
}

func TestMessageProcessor_DoesNotSendAckForSlashCommands(t *testing.T) {
	platform := newProcessorTestPlatform()
	router := &processorTestRouter{
		delay: 20 * time.Millisecond,
	}

	processor := newMessageProcessor(platform, router, log.New(os.Stdout, "", 0))

	processor.dispatch(context.Background(), &weibo.Message{
		ID:      "msg-help",
		UserID:  "user-help",
		Content: "/help",
	})

	time.Sleep(100 * time.Millisecond)
	assert.Empty(t, platform.Replies())
}

func TestMessageProcessor_ByTheWayInjectsIntoBusySessionWithoutInterrupt(t *testing.T) {
	platform := newProcessorTestPlatform()
	release := make(chan struct{})
	router := &processorTestRouter{
		started:  make(chan string, 2),
		canceled: make(chan string, 1),
		injected: make(chan string, 1),
		release:  release,
	}

	processor := newMessageProcessor(platform, router, log.New(os.Stdout, "", 0))
	ctx := context.Background()

	processor.dispatch(ctx, &weibo.Message{
		ID:      "msg-1",
		UserID:  "user-btw",
		Content: "先执行第一条",
	})

	select {
	case started := <-router.started:
		assert.Equal(t, "user-btw:msg-1", started)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("first message did not start")
	}

	processor.dispatch(ctx, &weibo.Message{
		ID:      "msg-btw",
		UserID:  "user-btw",
		Content: "/btw 顺便继续",
	})

	select {
	case injected := <-router.injected:
		assert.Equal(t, "user-btw:msg-btw", injected)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("/btw message was not injected")
	}

	select {
	case canceled := <-router.canceled:
		t.Fatalf("first message should not be interrupted, got %s", canceled)
	case started := <-router.started:
		t.Fatalf("/btw should not start a second processor turn, got %s", started)
	case <-time.After(150 * time.Millisecond):
	}

	replies := platform.Replies()
	assert.Len(t, replies, 1)
	assert.True(t, strings.Contains(replies[0], processingAckMessage))
	close(release)
}

func TestMessageProcessor_SlashCommandBypassesBusyQueue(t *testing.T) {
	platform := newProcessorTestPlatform()
	release := make(chan struct{})
	router := &processorTestRouter{
		started: make(chan string, 2),
		release: release,
	}

	processor := newMessageProcessor(platform, router, log.New(os.Stdout, "", 0))
	ctx := context.Background()

	processor.dispatch(ctx, &weibo.Message{
		ID:      "msg-1",
		UserID:  "user-slash",
		Content: "先执行第一条",
	})

	select {
	case started := <-router.started:
		assert.Equal(t, "user-slash:msg-1", started)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("first message did not start")
	}

	processor.dispatch(ctx, &weibo.Message{
		ID:      "msg-help",
		UserID:  "user-slash",
		Content: "/help",
	})

	select {
	case started := <-router.started:
		assert.Equal(t, "user-slash:msg-help", started)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("slash command did not bypass busy queue")
	}

	replies := platform.Replies()
	assert.Len(t, replies, 1)
	assert.True(t, strings.Contains(replies[0], processingAckMessage))
	close(release)
}

func TestMessageProcessor_AllowsDifferentUsersInParallel(t *testing.T) {
	platform := newProcessorTestPlatform()
	router := &processorTestRouter{
		started: make(chan string, 2),
		release: make(chan struct{}),
	}

	processor := newMessageProcessor(platform, router, log.New(os.Stdout, "", 0))

	processor.dispatch(context.Background(), &weibo.Message{ID: "msg-1", UserID: "user-1"})
	processor.dispatch(context.Background(), &weibo.Message{ID: "msg-2", UserID: "user-2"})

	var started []string
	assert.Eventually(t, func() bool {
		for len(started) < 2 {
			select {
			case event := <-router.started:
				started = append(started, event)
			default:
				return false
			}
		}
		return true
	}, time.Second, 20*time.Millisecond)

	assert.ElementsMatch(t, []string{"user-1:msg-1", "user-2:msg-2"}, started)
}

func TestParseChatStreamRequest_GET(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/chat/stream?user_id=user-1&content=hello&session_id=session-1", nil)

	parsed, err := parseChatStreamRequest(req)

	assert.NoError(t, err)
	assert.Equal(t, "user-1", parsed.UserID)
	assert.Equal(t, "hello", parsed.Content)
	assert.Equal(t, "session-1", parsed.SessionID)
}

func TestParseChatStreamRequest_POST(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/chat/stream", strings.NewReader(`{"user_id":"user-1","content":"hello","session_id":"session-1"}`))

	parsed, err := parseChatStreamRequest(req)

	assert.NoError(t, err)
	assert.Equal(t, "user-1", parsed.UserID)
	assert.Equal(t, "hello", parsed.Content)
	assert.Equal(t, "session-1", parsed.SessionID)
}

func TestChatStreamHandler_StreamsEvents(t *testing.T) {
	sessionMgr := session.NewManager(session.ManagerConfig{
		Timeout: 300,
		MaxSize: 10,
	})
	agentMgr := agent.NewManager()
	agentMgr.Register(&sseTestAgent{
		name:      "codex",
		available: true,
		streamFn: func(ctx context.Context, sessionID string, input string) (<-chan agent.Event, error) {
			events := make(chan agent.Event, 4)
			go func() {
				defer close(events)
				events <- agent.Event{Type: agent.EventTypeSession, SessionID: "thread-1"}
				events <- agent.Event{Type: agent.EventTypeMessage, Content: "hello"}
				events <- agent.Event{Type: agent.EventTypeDone}
			}()
			return events, nil
		},
	})
	agentMgr.SetDefault("codex")

	msgRouter := router.NewRouter(nil, sessionMgr, agentMgr)
	handler := chatStreamHandler(msgRouter)

	req := httptest.NewRequest(http.MethodGet, "/chat/stream?user_id=user-1&content=hello&session_id=session-1", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
	body := w.Body.String()
	assert.Contains(t, body, "event: session")
	assert.Contains(t, body, `"session_id":"thread-1"`)
	assert.Contains(t, body, "event: message")
	assert.Contains(t, body, `"content":"hello"`)

	sess, ok := sessionMgr.Get("session-1")
	assert.True(t, ok)
	assert.Equal(t, "thread-1", sess.Context["codex_session_id"])
}

func TestChatStreamHandler_RejectsMissingContent(t *testing.T) {
	msgRouter := router.NewRouter(nil, session.NewManager(session.ManagerConfig{
		Timeout: 300,
		MaxSize: 10,
	}), agent.NewManager())
	handler := chatStreamHandler(msgRouter)

	req := httptest.NewRequest(http.MethodGet, "/chat/stream?user_id=user-1", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "content is required")
}
