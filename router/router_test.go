package router

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/yourusername/weibo-ai-bridge/agent"
	"github.com/yourusername/weibo-ai-bridge/platform/weibo"
	"github.com/yourusername/weibo-ai-bridge/session"
)

// MockPlatform 模拟平台
type MockPlatform struct {
	replies []map[string]interface{}
	err     error
}

func (m *MockPlatform) Reply(ctx context.Context, messageID string, content string) error {
	if m.err != nil {
		return m.err
	}
	m.replies = append(m.replies, map[string]interface{}{
		"message_id": messageID,
		"content":    content,
	})
	return nil
}

// MockAgent 模拟 Agent
type MockAgent struct {
	name      string
	response  string
	available bool
}

func (m *MockAgent) Name() string {
	return m.name
}

func (m *MockAgent) Execute(sessionID string, input string) (string, error) {
	return m.response, nil
}

func (m *MockAgent) IsAvailable() bool {
	return m.available
}

func TestNewRouterWithDependencies(t *testing.T) {
	platform := &MockPlatform{}
	sessionMgr := session.NewManager(session.ManagerConfig{
		Timeout: 300,
		MaxSize: 10,
	})
	agentMgr := agent.NewManager()

	router := NewRouter(platform, sessionMgr, agentMgr)

	assert.NotNil(t, router)
	assert.NotNil(t, router.platform)
	assert.NotNil(t, router.sessionMgr)
	assert.NotNil(t, router.agentMgr)
	assert.NotNil(t, router.commandHandler)
}

func TestHandleMessage(t *testing.T) {
	tests := []struct {
		name        string
		msg         *weibo.Message
		expectReply bool
		expectError bool
	}{
		{
			name: "处理文本消息",
			msg: &weibo.Message{
				ID:        "msg-1",
				Type:      weibo.MessageTypeText,
				Content:   "Hello",
				UserID:    "user-1",
				UserName:  "test-user",
				Timestamp: 1234567890,
			},
			expectReply: true,
			expectError: false,
		},
		{
			name:        "处理 nil 消息",
			msg:         nil,
			expectReply: false,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			platform := &MockPlatform{}
			sessionMgr := session.NewManager(session.ManagerConfig{
				Timeout: 300,
				MaxSize: 10,
			})
			agentMgr := agent.NewManager()
			agentMgr.Register(&MockAgent{
				name:      "claude-code",
				response:  "AI response",
				available: true,
			})
			agentMgr.SetDefault("claude-code")

			router := NewRouter(platform, sessionMgr, agentMgr)

			err := router.HandleMessage(context.Background(), tt.msg)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			if tt.expectReply {
				assert.Greater(t, len(platform.replies), 0)
			}
		})
	}
}

func TestHandleMessage_RepliesToUserID(t *testing.T) {
	platform := &MockPlatform{}
	sessionMgr := session.NewManager(session.ManagerConfig{
		Timeout: 300,
		MaxSize: 10,
	})
	agentMgr := agent.NewManager()
	agentMgr.Register(&MockAgent{
		name:      "claude-code",
		response:  "AI response",
		available: true,
	})
	agentMgr.SetDefault("claude-code")

	router := NewRouter(platform, sessionMgr, agentMgr)

	msg := &weibo.Message{
		ID:        "msg-123",
		Type:      weibo.MessageTypeText,
		Content:   "Hello",
		UserID:    "user-456",
		UserName:  "test-user",
		Timestamp: 1234567890,
	}

	err := router.HandleMessage(context.Background(), msg)

	assert.NoError(t, err)
	assert.Len(t, platform.replies, 1)
	assert.Equal(t, "user-456", platform.replies[0]["message_id"])
}

func TestHandleMessage_UsesActiveSessionCreatedByNewCommand(t *testing.T) {
	platform := &MockPlatform{}
	sessionMgr := session.NewManager(session.ManagerConfig{
		Timeout: 300,
		MaxSize: 10,
	})
	agentMgr := agent.NewManager()
	agentMgr.Register(&MockAgent{
		name:      "claude-code",
		response:  "Claude response",
		available: true,
	})
	agentMgr.Register(&MockAgent{
		name:      "codex",
		response:  "Codex response",
		available: true,
	})
	agentMgr.SetDefault("claude-code")

	router := NewRouter(platform, sessionMgr, agentMgr)

	err := router.HandleMessage(context.Background(), &weibo.Message{
		ID:        "msg-new",
		Type:      weibo.MessageTypeText,
		Content:   "/new codex",
		UserID:    "user-1",
		UserName:  "test-user",
		Timestamp: 1234567890,
	})
	assert.NoError(t, err)
	assert.Equal(t, "user-1-1", sessionMgr.GetActiveSessionID("user-1"))

	err = router.HandleMessage(context.Background(), &weibo.Message{
		ID:        "msg-ai",
		Type:      weibo.MessageTypeText,
		Content:   "Hello AI",
		UserID:    "user-1",
		UserName:  "test-user",
		Timestamp: 1234567891,
	})
	assert.NoError(t, err)
	assert.Len(t, platform.replies, 2)
	assert.Equal(t, "Codex response", platform.replies[1]["content"])
}

func TestHandleAIMessage(t *testing.T) {
	tests := []struct {
		name         string
		setupAgent   bool
		setupSession bool
		expectError  bool
		errorMsg     string
	}{
		{
			name:         "成功处理 AI 消息",
			setupAgent:   true,
			setupSession: true,
			expectError:  false,
		},
		{
			name:         "Agent 管理器未设置",
			setupAgent:   false,
			setupSession: true,
			expectError:  false, // 不返回 error，返回错误消息
		},
		{
			name:         "Session 管理器未设置",
			setupAgent:   true,
			setupSession: false,
			expectError:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			platform := &MockPlatform{}
			var sessionMgr *session.Manager
			var agentMgr *agent.Manager

			if tt.setupSession {
				sessionMgr = session.NewManager(session.ManagerConfig{
					Timeout: 300,
					MaxSize: 10,
				})
			}

			if tt.setupAgent {
				agentMgr = agent.NewManager()
				mockAgent := &MockAgent{
					name:      "test-agent",
					response:  "AI response",
					available: true,
				}
				agentMgr.Register(mockAgent)
				agentMgr.SetDefault("test-agent")
			}

			router := NewRouter(platform, sessionMgr, agentMgr)

			msg := &Message{
				ID:        "msg-1",
				Type:      TypeText,
				Content:   "Hello AI",
				UserID:    "user-1",
				SessionID: "session-1",
			}

			resp, err := router.handleAIMessage(context.Background(), msg)

			assert.NoError(t, err)
			assert.NotNil(t, resp)

			if tt.expectError {
				assert.False(t, resp.Success)
				assert.Contains(t, resp.Content, tt.errorMsg)
			}
		})
	}
}

func TestHandleAIMessage_UsesSessionAgentType(t *testing.T) {
	platform := &MockPlatform{}
	sessionMgr := session.NewManager(session.ManagerConfig{
		Timeout: 300,
		MaxSize: 10,
	})
	agentMgr := agent.NewManager()
	agentMgr.Register(&MockAgent{
		name:      "claude-code",
		response:  "Claude response",
		available: true,
	})
	agentMgr.Register(&MockAgent{
		name:      "codex",
		response:  "Codex response",
		available: true,
	})
	agentMgr.SetDefault("claude-code")

	sessionMgr.Create("session-1", "user-1", "codex")

	router := NewRouter(platform, sessionMgr, agentMgr)
	resp, err := router.handleAIMessage(context.Background(), &Message{
		ID:        "msg-1",
		Type:      TypeText,
		Content:   "Hello AI",
		UserID:    "user-1",
		SessionID: "session-1",
	})

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.True(t, resp.Success)
	assert.Equal(t, "Codex response", resp.Content)
}

func TestSendReply(t *testing.T) {
	tests := []struct {
		name         string
		content      string
		expectError  bool
		expectChunks int
	}{
		{
			name:         "发送短消息",
			content:      "Hello",
			expectError:  false,
			expectChunks: 1,
		},
		{
			name:         "发送长消息（需要分块）",
			content:      strings.Repeat("这是一条测试消息。\n", 100), // 约 900 字符，超过 1000 需要分块
			expectError:  false,
			expectChunks: 3,
		},
		{
			name:         "平台未设置",
			content:      "Test",
			expectError:  true,
			expectChunks: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			platform := &MockPlatform{}
			sessionMgr := session.NewManager(session.ManagerConfig{
				Timeout: 300,
				MaxSize: 10,
			})
			agentMgr := agent.NewManager()

			router := NewRouter(platform, sessionMgr, agentMgr)

			// 对于"平台未设置"的测试，移除 platform
			if tt.name == "平台未设置" {
				router.platform = nil
			}

			err := router.sendReply(context.Background(), "msg-1", tt.content)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectChunks, len(platform.replies))
			}
		})
	}
}

func TestSplitMessage(t *testing.T) {
	tests := []struct {
		name         string
		content      string
		maxSize      int
		expectChunks int
	}{
		{
			name:         "短消息不分块",
			content:      "Hello",
			maxSize:      100,
			expectChunks: 1,
		},
		{
			name:         "长消息分块",
			content:      strings.Repeat("a", 250),
			maxSize:      100,
			expectChunks: 3,
		},
		{
			name:         "按行分割",
			content:      "line1\nline2\nline3\nline4\nline5",
			maxSize:      10,
			expectChunks: 5,
		},
		{
			name:         "单行超长强制分割",
			content:      strings.Repeat("b", 300),
			maxSize:      100,
			expectChunks: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := NewRouter(nil, nil, nil)
			chunks := router.splitMessage(tt.content, tt.maxSize)

			assert.Equal(t, tt.expectChunks, len(chunks))

			// 验证每个块都不超过最大长度
			for i, chunk := range chunks {
				if len(chunk) > tt.maxSize {
					t.Errorf("chunk %d exceeds max size: %d > %d", i, len(chunk), tt.maxSize)
				}
			}

			// 验证所有块拼接后等于原内容
			reconstructed := strings.Join(chunks, "")
			assert.Equal(t, tt.content, reconstructed)
		})
	}
}
