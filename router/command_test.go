package router

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/yourusername/weibo-ai-bridge/agent"
	"github.com/yourusername/weibo-ai-bridge/session"
)

func TestNewCommandHandler(t *testing.T) {
	sessionManager := session.NewManager(session.ManagerConfig{
		Timeout: 3600,
		MaxSize: 100,
	})
	agentManager := agent.NewManager()

	handler := NewCommandHandler(sessionManager, agentManager)

	assert.NotNil(t, handler)
	assert.NotNil(t, handler.sessionManager)
	assert.NotNil(t, handler.agentManager)
}

func TestCommandHandler_Handle_Help(t *testing.T) {
	sessionManager := session.NewManager(session.ManagerConfig{})
	agentManager := agent.NewManager()
	handler := NewCommandHandler(sessionManager, agentManager)

	msg := &Message{
		ID:      "msg-1",
		Type:    TypeText,
		Content: "/help",
		UserID:  "user-1",
	}

	resp, err := handler.Handle(msg)

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.True(t, resp.Success)
	assert.Contains(t, resp.Content, "/help")
	assert.Contains(t, resp.Content, "/new")
	assert.Contains(t, resp.Content, "/switch")
	assert.Contains(t, resp.Content, "/model")
	assert.Contains(t, resp.Content, "/dir")
	assert.Contains(t, resp.Content, "/status")
}

func TestCommandHandler_Handle_New(t *testing.T) {
	sessionManager := session.NewManager(session.ManagerConfig{
		Timeout: 3600,
		MaxSize: 100,
	})
	agentManager := agent.NewManager()
	handler := NewCommandHandler(sessionManager, agentManager)

	tests := []struct {
		name        string
		content     string
		expectedErr bool
		checkResult func(t *testing.T, resp *Response)
	}{
		{
			name:    "创建默认 claude 会话",
			content: "/new",
			checkResult: func(t *testing.T, resp *Response) {
				assert.True(t, resp.Success)
				assert.Contains(t, resp.Content, "New session created")
				assert.Contains(t, resp.Content, "claude")
			},
		},
		{
			name:    "创建 claude 会话",
			content: "/new claude",
			checkResult: func(t *testing.T, resp *Response) {
				assert.True(t, resp.Success)
				assert.Contains(t, resp.Content, "claude")
			},
		},
		{
			name:    "创建 codex 会话",
			content: "/new codex",
			checkResult: func(t *testing.T, resp *Response) {
				assert.True(t, resp.Success)
				assert.Contains(t, resp.Content, "codex")
			},
		},
		{
			name:    "无效的 agent 类型",
			content: "/new invalid",
			checkResult: func(t *testing.T, resp *Response) {
				assert.False(t, resp.Success)
				assert.Contains(t, resp.Content, "Invalid agent type")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := &Message{
				ID:      "msg-1",
				Type:    TypeText,
				Content: tt.content,
				UserID:  "user-1",
			}

			resp, err := handler.Handle(msg)

			if tt.expectedErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, resp)
				tt.checkResult(t, resp)
			}
		})
	}
}

func TestCommandHandler_Handle_Switch(t *testing.T) {
	sessionManager := session.NewManager(session.ManagerConfig{
		Timeout: 3600,
		MaxSize: 100,
	})
	agentManager := agent.NewManager()
	handler := NewCommandHandler(sessionManager, agentManager)

	// 先创建一个会话
	sess := sessionManager.Create("session-1", "user-1", "claude")
	assert.NotNil(t, sess)

	tests := []struct {
		name        string
		content     string
		sessionID   string
		expectedErr bool
		checkResult func(t *testing.T, resp *Response)
	}{
		{
			name:      "切换到 codex",
			content:   "/switch codex",
			sessionID: "session-1",
			checkResult: func(t *testing.T, resp *Response) {
				assert.True(t, resp.Success)
				assert.Contains(t, resp.Content, "Switched to codex agent")
			},
		},
		{
			name:      "缺少参数",
			content:   "/switch",
			sessionID: "session-1",
			checkResult: func(t *testing.T, resp *Response) {
				assert.False(t, resp.Success)
				assert.Contains(t, resp.Content, "Please specify agent type")
			},
		},
		{
			name:      "无效的 agent 类型",
			content:   "/switch invalid",
			sessionID: "session-1",
			checkResult: func(t *testing.T, resp *Response) {
				assert.False(t, resp.Success)
				assert.Contains(t, resp.Content, "Invalid agent type")
			},
		},
		{
			name:      "会话不存在",
			content:   "/switch claude",
			sessionID: "non-existent",
			checkResult: func(t *testing.T, resp *Response) {
				assert.False(t, resp.Success)
				assert.Contains(t, resp.Content, "Session not found")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := &Message{
				ID:        "msg-1",
				Type:      TypeText,
				Content:   tt.content,
				UserID:    "user-1",
				SessionID: tt.sessionID,
			}

			resp, err := handler.Handle(msg)

			if tt.expectedErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, resp)
				tt.checkResult(t, resp)
			}
		})
	}
}

func TestCommandHandler_Handle_Model(t *testing.T) {
	sessionManager := session.NewManager(session.ManagerConfig{
		Timeout: 3600,
		MaxSize: 100,
	})
	agentManager := agent.NewManager()
	agentManager.Register(&MockAgent{name: "claude-code", available: true})
	agentManager.Register(&MockAgent{name: "codex", available: true})
	agentManager.SetDefault("claude-code")
	handler := NewCommandHandler(sessionManager, agentManager)

	// 创建一个会话
	sess := sessionManager.Create("session-1", "user-1", "claude")
	assert.NotNil(t, sess)

	// 测试显示模型（没有注册 agent）
	msg := &Message{
		ID:        "msg-1",
		Type:      TypeText,
		Content:   "/model",
		UserID:    "user-1",
		SessionID: "session-1",
	}

	resp, err := handler.Handle(msg)

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.True(t, resp.Success)
	assert.Contains(t, resp.Content, "Model: claude-code")
}

func TestCommandHandler_Handle_Model_UsesSessionAgentType(t *testing.T) {
	sessionManager := session.NewManager(session.ManagerConfig{
		Timeout: 3600,
		MaxSize: 100,
	})
	agentManager := agent.NewManager()
	agentManager.Register(&MockAgent{name: "claude-code", available: true})
	agentManager.Register(&MockAgent{name: "codex", available: true})
	agentManager.SetDefault("claude-code")
	handler := NewCommandHandler(sessionManager, agentManager)

	sess := sessionManager.Create("session-1", "user-1", "codex")
	assert.NotNil(t, sess)

	msg := &Message{
		ID:        "msg-1",
		Type:      TypeText,
		Content:   "/model",
		UserID:    "user-1",
		SessionID: "session-1",
	}

	resp, err := handler.Handle(msg)

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.True(t, resp.Success)
	assert.Contains(t, resp.Content, "Model: codex")
}

func TestCommandHandler_Handle_Dir(t *testing.T) {
	sessionManager := session.NewManager(session.ManagerConfig{
		Timeout: 3600,
		MaxSize: 100,
	})
	agentManager := agent.NewManager()
	handler := NewCommandHandler(sessionManager, agentManager)

	// 创建一个会话
	sess := sessionManager.Create("session-1", "user-1", "claude")
	assert.NotNil(t, sess)

	// 设置工作目录
	sess.Update("work_dir", "/home/user/project")

	// 测试显示目录
	msg := &Message{
		ID:        "msg-1",
		Type:      TypeText,
		Content:   "/dir",
		UserID:    "user-1",
		SessionID: "session-1",
	}

	resp, err := handler.Handle(msg)

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.True(t, resp.Success)
	assert.Contains(t, resp.Content, "Current working directory")
	assert.Contains(t, resp.Content, "/home/user/project")
}

func TestCommandHandler_Handle_Status(t *testing.T) {
	sessionManager := session.NewManager(session.ManagerConfig{
		Timeout: 3600,
		MaxSize: 100,
	})
	agentManager := agent.NewManager()
	handler := NewCommandHandler(sessionManager, agentManager)

	// 创建一个会话
	sess := sessionManager.Create("session-1", "user-1", "claude")
	assert.NotNil(t, sess)

	// 测试显示状态
	msg := &Message{
		ID:        "msg-1",
		Type:      TypeText,
		Content:   "/status",
		UserID:    "user-1",
		SessionID: "session-1",
	}

	resp, err := handler.Handle(msg)

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.True(t, resp.Success)
	assert.Contains(t, resp.Content, "Session Status")
	assert.Contains(t, resp.Content, "session-1")
	assert.Contains(t, resp.Content, "user-1")
	assert.Contains(t, resp.Content, "claude")
	assert.Contains(t, resp.Content, "active")
}

func TestCommandHandler_Handle_UnknownCommand(t *testing.T) {
	sessionManager := session.NewManager(session.ManagerConfig{})
	agentManager := agent.NewManager()
	handler := NewCommandHandler(sessionManager, agentManager)

	msg := &Message{
		ID:      "msg-1",
		Type:    TypeText,
		Content: "/unknown",
		UserID:  "user-1",
	}

	resp, err := handler.Handle(msg)

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.False(t, resp.Success)
	assert.Contains(t, resp.Content, "Unknown command")
}

func TestCommandHandler_Handle_NonCommand(t *testing.T) {
	sessionManager := session.NewManager(session.ManagerConfig{})
	agentManager := agent.NewManager()
	handler := NewCommandHandler(sessionManager, agentManager)

	msg := &Message{
		ID:      "msg-1",
		Type:    TypeText,
		Content: "Hello, this is not a command",
		UserID:  "user-1",
	}

	resp, err := handler.Handle(msg)

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.False(t, resp.Success)
	assert.Contains(t, resp.Content, "Unknown command")
}

func TestCommandHandler_Handle_NilMessage(t *testing.T) {
	sessionManager := session.NewManager(session.ManagerConfig{})
	agentManager := agent.NewManager()
	handler := NewCommandHandler(sessionManager, agentManager)

	resp, err := handler.Handle(nil)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "message cannot be nil")
	assert.Nil(t, resp)
}

func TestCommandHandler_Handle_EmptyCommand(t *testing.T) {
	sessionManager := session.NewManager(session.ManagerConfig{})
	agentManager := agent.NewManager()
	handler := NewCommandHandler(sessionManager, agentManager)

	msg := &Message{
		ID:      "msg-1",
		Type:    TypeText,
		Content: "   ", // 只有空格
		UserID:  "user-1",
	}

	resp, err := handler.Handle(msg)

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.False(t, resp.Success)
	assert.Contains(t, resp.Content, "Unknown command")
}
