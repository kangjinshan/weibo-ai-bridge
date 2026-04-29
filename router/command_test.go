package router

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kangjinshan/weibo-ai-bridge/agent"
	"github.com/kangjinshan/weibo-ai-bridge/session"
	"github.com/stretchr/testify/assert"
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
	assert.Contains(t, resp.Content, "/list")
	assert.Contains(t, resp.Content, "/switch")
	assert.Contains(t, resp.Content, "/btw")
	assert.Contains(t, resp.Content, "/model")
	assert.Contains(t, resp.Content, "/dir")
	assert.Contains(t, resp.Content, "/status")
}

func TestCommandHandler_Handle_List(t *testing.T) {
	sessionManager := session.NewManager(session.ManagerConfig{
		Timeout: 3600,
		MaxSize: 100,
	})
	agentManager := agent.NewManager()
	handler := NewCommandHandler(sessionManager, agentManager)

	first := sessionManager.Create("session-1", "user-1", "claude")
	assert.NotNil(t, first)
	first.SetTitleIfEmpty("第一个问题")
	second := sessionManager.Create("session-2", "user-1", "codex")
	assert.NotNil(t, second)
	sessionManager.Create("session-3", "user-2", "claude")

	resp, err := handler.Handle(&Message{
		ID:      "msg-list",
		Type:    TypeText,
		Content: "/list",
		UserID:  "user-1",
	})

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.True(t, resp.Success)
	assert.Contains(t, resp.Content, "| 编号 | 标题 | 时间 |")
	// 活跃 session 是 codex (session-2)，所以只显示 codex 的 session
	assert.Contains(t, resp.Content, "| 1 | 未命名会话（当前） |")
}

func TestCommandHandler_Handle_List_SortsBridgeSessionsByUpdatedAtDesc(t *testing.T) {
	sessionManager := session.NewManager(session.ManagerConfig{
		Timeout: 3600,
		MaxSize: 100,
	})
	agentManager := agent.NewManager()
	handler := NewCommandHandler(sessionManager, agentManager)

	first := sessionManager.Create("session-1", "user-1", "codex")
	assert.NotNil(t, first)
	first.SetTitleIfEmpty("会话A")

	time.Sleep(10 * time.Millisecond)

	second := sessionManager.Create("session-2", "user-1", "codex")
	assert.NotNil(t, second)
	second.SetTitleIfEmpty("会话B")

	resp, err := handler.Handle(&Message{
		ID:      "msg-list-order",
		Type:    TypeText,
		Content: "/list",
		UserID:  "user-1",
	})

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.True(t, resp.Success)

	rowA := "| 1 | 会话B（当前） |"
	rowB := "| 2 | 会话A |"
	assert.Contains(t, resp.Content, rowA)
	assert.Contains(t, resp.Content, rowB)
	assert.Less(t, strings.Index(resp.Content, rowA), strings.Index(resp.Content, rowB))
}

func TestCommandHandler_Handle_New(t *testing.T) {
	sessionManager := session.NewManager(session.ManagerConfig{
		Timeout: 3600,
		MaxSize: 100,
	})
	agentManager := agent.NewManager()
	agentManager.Register(&MockAgent{name: "claude-code", available: true})
	agentManager.Register(&MockAgent{name: "codex", available: true})
	agentManager.SetDefault("claude-code")
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

func TestCommandHandler_Handle_New_DefaultsToCodexWhenOnlyCodexAvailable(t *testing.T) {
	sessionManager := session.NewManager(session.ManagerConfig{
		Timeout: 3600,
		MaxSize: 100,
	})
	agentManager := agent.NewManager()
	agentManager.Register(&MockAgent{name: "codex", available: true})
	agentManager.SetDefault("codex")
	handler := NewCommandHandler(sessionManager, agentManager)

	resp, err := handler.Handle(&Message{
		ID:      "msg-1",
		Type:    TypeText,
		Content: "/new",
		UserID:  "user-1",
	})

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.True(t, resp.Success)
	assert.Contains(t, resp.Content, "Agent: codex")

	activeSession, ok := sessionManager.GetActiveSession("user-1")
	assert.True(t, ok)
	assert.Equal(t, "codex", activeSession.AgentType)
}

func TestCommandHandler_Handle_New_RepairsUnavailableAgent(t *testing.T) {
	sessionManager := session.NewManager(session.ManagerConfig{
		Timeout: 3600,
		MaxSize: 100,
	})
	agentManager := agent.NewManager()
	agentManager.Register(&MockAgent{name: "claude-code", available: true})
	agentManager.SetDefault("claude-code")
	handler := NewCommandHandler(sessionManager, agentManager)

	repairCalls := 0
	handler.agentRepairer = &testAgentAvailabilityRepairer{
		ensureAvailableFn: func(agentType string) (bool, error) {
			repairCalls++
			assert.Equal(t, "codex", agentType)
			agentManager.Register(&MockAgent{name: "codex", available: true})
			return true, nil
		},
	}

	resp, err := handler.Handle(&Message{
		ID:      "msg-1",
		Type:    TypeText,
		Content: "/new codex",
		UserID:  "user-1",
	})

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.True(t, resp.Success)
	assert.Equal(t, 1, repairCalls)
	assert.Contains(t, resp.Content, "Agent: codex")

	activeSession, ok := sessionManager.GetActiveSession("user-1")
	assert.True(t, ok)
	assert.Equal(t, "codex", activeSession.AgentType)
}

func TestCommandHandler_Handle_New_DefaultsToActiveSessionAgentType(t *testing.T) {
	sessionManager := session.NewManager(session.ManagerConfig{
		Timeout: 3600,
		MaxSize: 100,
	})
	agentManager := agent.NewManager()
	agentManager.Register(&MockAgent{name: "claude-code", available: true})
	agentManager.Register(&MockAgent{name: "codex", available: true})
	agentManager.SetDefault("claude-code")
	handler := NewCommandHandler(sessionManager, agentManager)

	sessionManager.Create("session-1", "user-1", "codex")

	resp, err := handler.Handle(&Message{
		ID:      "msg-1",
		Type:    TypeText,
		Content: "/new",
		UserID:  "user-1",
	})

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.True(t, resp.Success)
	assert.Contains(t, resp.Content, "Agent: codex")

	activeSession, ok := sessionManager.GetActiveSession("user-1")
	assert.True(t, ok)
	assert.Equal(t, "codex", activeSession.AgentType)
	assert.Equal(t, "user-1-1", activeSession.ID)
}

func TestCommandHandler_Handle_Switch(t *testing.T) {
	sessionManager := session.NewManager(session.ManagerConfig{
		Timeout: 3600,
		MaxSize: 100,
	})
	agentManager := agent.NewManager()
	agentManager.Register(&MockAgent{name: "claude-code", available: true})
	agentManager.Register(&MockAgent{name: "codex", available: true})
	agentManager.SetDefault("claude-code")
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
				assert.Contains(t, resp.Content, "Please specify a session number or agent type")
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

func TestCommandHandler_Handle_SwitchSessionByNumber(t *testing.T) {
	sessionManager := session.NewManager(session.ManagerConfig{
		Timeout: 3600,
		MaxSize: 100,
	})
	agentManager := agent.NewManager()
	handler := NewCommandHandler(sessionManager, agentManager)

	first := sessionManager.Create("session-1", "user-1", "claude")
	second := sessionManager.Create("session-2", "user-1", "codex")
	assert.NotNil(t, first)
	assert.NotNil(t, second)
	assert.Equal(t, "session-2", sessionManager.GetActiveSessionID("user-1"))

	resp, err := handler.Handle(&Message{
		ID:        "msg-switch-session",
		Type:      TypeText,
		Content:   "/switch 1",
		UserID:    "user-1",
		SessionID: "session-2",
	})

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.True(t, resp.Success)
	assert.Contains(t, resp.Content, "Switched to session 1: 未命名会话")
	assert.Contains(t, resp.Content, "id=session-1")
	assert.Equal(t, "session-1", sessionManager.GetActiveSessionID("user-1"))
}

func TestCommandHandler_Handle_SwitchSessionByNumber_RejectsInvalidIndex(t *testing.T) {
	sessionManager := session.NewManager(session.ManagerConfig{
		Timeout: 3600,
		MaxSize: 100,
	})
	agentManager := agent.NewManager()
	handler := NewCommandHandler(sessionManager, agentManager)

	sessionManager.Create("session-1", "user-1", "claude")

	resp, err := handler.Handle(&Message{
		ID:      "msg-switch-session",
		Type:    TypeText,
		Content: "/switch 3",
		UserID:  "user-1",
	})

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.False(t, resp.Success)
	assert.Contains(t, resp.Content, "Invalid session number")
}

func TestCommandHandler_Handle_Switch_RejectsUnavailableAgent(t *testing.T) {
	sessionManager := session.NewManager(session.ManagerConfig{
		Timeout: 3600,
		MaxSize: 100,
	})
	agentManager := agent.NewManager()
	agentManager.Register(&MockAgent{name: "claude-code", available: true})
	agentManager.SetDefault("claude-code")
	handler := NewCommandHandler(sessionManager, agentManager)

	sess := sessionManager.Create("session-1", "user-1", "claude")
	assert.NotNil(t, sess)

	resp, err := handler.Handle(&Message{
		ID:        "msg-1",
		Type:      TypeText,
		Content:   "/switch codex",
		UserID:    "user-1",
		SessionID: "session-1",
	})

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.False(t, resp.Success)
	assert.Contains(t, resp.Content, "Requested agent is not available: codex")

	sess, _ = sessionManager.Get("session-1")
	assert.Equal(t, "claude", sess.AgentType)
}

func TestCommandHandler_Handle_Switch_RepairsUnavailableAgent(t *testing.T) {
	sessionManager := session.NewManager(session.ManagerConfig{
		Timeout: 3600,
		MaxSize: 100,
	})
	agentManager := agent.NewManager()
	agentManager.Register(&MockAgent{name: "claude-code", available: true})
	agentManager.SetDefault("claude-code")
	handler := NewCommandHandler(sessionManager, agentManager)

	repairCalls := 0
	handler.agentRepairer = &testAgentAvailabilityRepairer{
		ensureAvailableFn: func(agentType string) (bool, error) {
			repairCalls++
			assert.Equal(t, "codex", agentType)
			agentManager.Register(&MockAgent{name: "codex", available: true})
			return true, nil
		},
	}

	sess := sessionManager.Create("session-1", "user-1", "claude")
	assert.NotNil(t, sess)

	resp, err := handler.Handle(&Message{
		ID:        "msg-1",
		Type:      TypeText,
		Content:   "/switch codex",
		UserID:    "user-1",
		SessionID: "session-1",
	})

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.True(t, resp.Success)
	assert.Equal(t, 1, repairCalls)
	assert.Contains(t, resp.Content, "Switched to codex agent")

	sess, _ = sessionManager.Get("session-1")
	assert.Equal(t, "codex", sess.AgentType)
}

func TestConfigBackedAgentAvailabilityRepairer_EnsureAvailable_EnablesCodex(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.toml")
	err := os.WriteFile(configPath, []byte(`
[platform.weibo]
app_id = "app-id"
app_secret = "app-secret"

[agent.claude]
enabled = true

[agent.codex]
enabled = false
`), 0o644)
	assert.NoError(t, err)

	binDir := filepath.Join(tmpDir, "bin")
	assert.NoError(t, os.MkdirAll(binDir, 0o755))
	codexPath := filepath.Join(binDir, "codex")
	err = os.WriteFile(codexPath, []byte("#!/bin/sh\nexit 0\n"), 0o755)
	assert.NoError(t, err)

	t.Setenv("PATH", binDir)

	agentManager := agent.NewManager()
	agentManager.Register(&MockAgent{name: "claude-code", available: true})
	agentManager.SetDefault("claude-code")

	repairer := newConfigBackedAgentAvailabilityRepairer(agentManager, configPath)
	available, err := repairer.EnsureAvailable("codex")

	assert.NoError(t, err)
	assert.True(t, available)
	assert.NotNil(t, agentManager.ResolveAgent("codex"))

	updated, err := os.ReadFile(configPath)
	assert.NoError(t, err)
	assert.Contains(t, string(updated), "[agent.codex]")
	assert.Contains(t, string(updated), "enabled = true")
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

type testAgentAvailabilityRepairer struct {
	ensureAvailableFn func(agentType string) (bool, error)
}

func (r *testAgentAvailabilityRepairer) EnsureAvailable(agentType string) (bool, error) {
	return r.ensureAvailableFn(agentType)
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
	assert.Contains(t, resp.Content, "Title: 未命名会话")
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
