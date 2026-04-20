package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/yourusername/weibo-ai-bridge/config"
	"github.com/yourusername/weibo-ai-bridge/agent"
	"github.com/yourusername/weibo-ai-bridge/session"
)

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
	assert.Equal(t, "test-Secret", cfg.Platform.Weibo.AppSecret)
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
	os.Unsetenv("WEIBO_APP_ID")
	os.Unsetenv("WEIBO_APP_Secret")
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