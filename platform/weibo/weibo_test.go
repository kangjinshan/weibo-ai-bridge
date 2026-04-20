package weibo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockWebSocketServer 创建一个模拟的 WebSocket 服务器
func mockWebSocketServer(t *testing.T, handler func(*testing.T, http.ResponseWriter, *http.Request)) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if handler != nil {
			handler(t, w, r)
		}
	}))
}

// TestNewPlatform 测试构造函数
func TestNewPlatform(t *testing.T) {
	tests := []struct {
		name      string
		appID     string
		appSecret string
		wantErr   bool
	}{
		{
			name:      "valid config",
			appID:     "test-app-id",
			appSecret: "test-Secret",
			wantErr:   false,
		},
		{
			name:      "empty appID",
			appID:     "",
			appSecret: "test-Secret",
			wantErr:   true,
		},
		{
			name:      "empty appSecret",
			appID:     "test-app-id",
			appSecret: "",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			platform, err := NewPlatform(tt.appID, tt.appSecret)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, platform)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, platform)
				assert.Equal(t, tt.appID, platform.appID)
				assert.Equal(t, tt.appSecret, platform.appSecret)
			}
		})
	}
}

func TestPlatform_Configure(t *testing.T) {
	platform, err := NewPlatform("test-app-id", "test-secret")
	require.NoError(t, err)

	platform.Configure("http://example.com/token", "ws://example.com/stream", 45*time.Second)

	assert.Equal(t, "http://example.com/token", platform.tokenURL)
	assert.Equal(t, "ws://example.com/stream", platform.wsURL)
	assert.Equal(t, 45*time.Second, platform.httpClient.Timeout)
}

// TestPlatform_Start 测试启动方法
func TestPlatform_Start(t *testing.T) {
	// 这个测试主要验证 Start 方法能够被调用
	// 由于没有真实的 WebSocket 服务器，我们主要测试错误处理

	platform, err := NewPlatform("ws://example.com/ws", "test-token")
	require.NoError(t, err)

	// 测试启动（预期会失败，因为没有真实的 WebSocket 服务器）
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err = platform.Start(ctx)
	// 连接会失败，但这是预期的
	assert.Error(t, err)

	// 验证状态
	assert.False(t, platform.IsRunning())
}

// TestPlatform_Stop 测试停止方法
func TestPlatform_Stop(t *testing.T) {
	platform, err := NewPlatform("ws://example.com/ws", "test-token")
	require.NoError(t, err)

	// 测试停止（即使未启动也不应该 panic）
	platform.Stop()

	// 再次停止（应该幂等）
	platform.Stop()
}

// TestPlatform_Reply 测试回复方法
func TestPlatform_Reply(t *testing.T) {
	// 创建一个模拟服务器来捕获回复
	var receivedRequest bool
	var mu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		receivedRequest = true
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	platform, err := NewPlatform(wsURL, "test-token")
	require.NoError(t, err)

	// 测试回复消息
	ctx := context.Background()
	err = platform.Reply(ctx, "msg-123", "这是一条测试回复")

	// 注意：由于连接未建立，预期会返回错误
	assert.Error(t, err)

	_ = receivedRequest // 避免未使用变量警告
}

// TestPlatform_MessageLoop 测试消息循环
func TestPlatform_MessageLoop(t *testing.T) {
	// 这个测试主要验证消息循环能够启动和停止
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	platform, err := NewPlatform(wsURL, "test-token")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())

	// 启动消息循环
	go func() {
		platform.messageLoop(ctx)
	}()

	// 等待一小段时间
	time.Sleep(100 * time.Millisecond)

	// 取消上下文以停止消息循环
	cancel()

	// 等待消息循环停止
	time.Sleep(100 * time.Millisecond)
}

// TestPlatform_refreshToken 测试 Token 刷新
func TestPlatform_refreshToken(t *testing.T) {
	platform, err := NewPlatform("test-app-id", "test-Secret")
	require.NoError(t, err)

	ctx := context.Background()

	// 测试 token 刷新
	// 注意：在没有真实微博 API 的情况下，这个测试主要验证方法不会崩溃
	err = platform.refreshToken(ctx)

	// 在 mock 实现中，我们可能返回空或特定错误
	// 这里主要验证方法能够被调用
	t.Logf("refreshToken returned: err=%v", err)
}

// TestPlatform_ConcurrentAccess 测试并发访问
func TestPlatform_ConcurrentAccess(t *testing.T) {
	platform, err := NewPlatform("test-app-id", "test-Secret")
	require.NoError(t, err)

	var wg sync.WaitGroup
	numGoroutines := 10

	// 并发调用 Reply
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			ctx := context.Background()
			err := platform.Reply(ctx, "msg-"+string(rune(id)), "测试消息")
			if err != nil {
				t.Logf("Reply error in goroutine %d: %v", id, err)
			}
		}(i)
	}

	// 并发调用 Stop
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			platform.Stop()
		}()
	}

	wg.Wait()
}

// TestPlatform_InvalidMessage 测试无效消息处理
func TestPlatform_InvalidMessage(t *testing.T) {
	platform, err := NewPlatform("ws://example.com/ws", "test-token")
	require.NoError(t, err)

	// 测试回复空消息
	ctx := context.Background()
	err = platform.Reply(ctx, "", "")
	// 应该返回错误
	assert.Error(t, err)
}

// TestPlatform_ContextCancellation 测试上下文取消
func TestPlatform_ContextCancellation(t *testing.T) {
	platform, err := NewPlatform("ws://example.com/ws", "test-token")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())

	// 立即取消上下文
	cancel()

	// 尝试启动（应该快速失败）
	err = platform.Start(ctx)
	// 由于上下文已取消或连接失败，应该返回错误
	assert.Error(t, err)
}
