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
	streams []*MockReplyStream
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

func (m *MockPlatform) OpenReplyStream(ctx context.Context, userID string) (weibo.ChunkSender, error) {
	if m.err != nil {
		return nil, m.err
	}

	stream := &MockReplyStream{platform: m, userID: userID, messageID: "stream-msg-" + userID}
	m.streams = append(m.streams, stream)
	return stream, nil
}

type MockReplyStream struct {
	platform  *MockPlatform
	userID    string
	messageID string
	chunks    []map[string]interface{}
}

func (s *MockReplyStream) SendChunk(ctx context.Context, content string, done bool) error {
	record := map[string]interface{}{
		"user_id":    s.userID,
		"message_id": s.messageID,
		"chunk_id":   len(s.chunks),
		"content":    content,
		"done":       done,
	}
	s.chunks = append(s.chunks, record)
	s.platform.replies = append(s.platform.replies, map[string]interface{}{
		"message_id": s.userID,
		"content":    content,
		"done":       done,
	})
	return nil
}

// MockAgent 模拟 Agent
type MockAgent struct {
	name      string
	response  string
	available bool
	executeFn func(ctx context.Context, sessionID string, input string) (string, error)
	streamFn  func(ctx context.Context, sessionID string, input string) (<-chan agent.Event, error)
	lastInput string
	lastSID   string
}

func (m *MockAgent) Name() string {
	return m.name
}

func (m *MockAgent) Execute(ctx context.Context, sessionID string, input string) (string, error) {
	m.lastSID = sessionID
	m.lastInput = input
	if m.executeFn != nil {
		return m.executeFn(ctx, sessionID, input)
	}
	return m.response, nil
}

func (m *MockAgent) ExecuteStream(ctx context.Context, sessionID string, input string) (<-chan agent.Event, error) {
	m.lastSID = sessionID
	m.lastInput = input
	if m.streamFn != nil {
		return m.streamFn(ctx, sessionID, input)
	}

	events := make(chan agent.Event, 2)
	go func() {
		defer close(events)
		if strings.TrimSpace(m.response) != "" {
			response := m.response
			if newSessionID := extractSessionID(response); newSessionID != "" {
				events <- agent.Event{Type: agent.EventTypeSession, SessionID: newSessionID}
				response = removeSessionIDMarker(response)
			}
			events <- agent.Event{Type: agent.EventTypeMessage, Content: response}
		}
		events <- agent.Event{Type: agent.EventTypeDone}
	}()

	return events, nil
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

func TestHandleAIMessage_PersistsClaudeSessionID(t *testing.T) {
	platform := &MockPlatform{}
	sessionMgr := session.NewManager(session.ManagerConfig{
		Timeout: 300,
		MaxSize: 10,
	})
	agentMgr := agent.NewManager()
	mockAgent := &MockAgent{
		name:      "claude-code",
		available: true,
		response:  "Claude response\n\n__SESSION_ID__: claude-session-1",
	}
	agentMgr.Register(mockAgent)
	agentMgr.SetDefault("claude-code")

	sessionMgr.Create("session-1", "user-1", "claude")

	router := NewRouter(platform, sessionMgr, agentMgr)
	resp, err := router.handleAIMessage(context.Background(), &Message{
		ID:        "msg-1",
		Type:      TypeText,
		Content:   "Hello Claude",
		UserID:    "user-1",
		SessionID: "session-1",
	})

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.True(t, resp.Success)
	assert.Equal(t, "Claude response", resp.Content)
	assert.Equal(t, "", mockAgent.lastSID)

	sess, ok := sessionMgr.Get("session-1")
	assert.True(t, ok)
	assert.Equal(t, "claude-session-1", sess.Context["claude_session_id"])
}

func TestHandleAIMessage_ResumesClaudeSessionID(t *testing.T) {
	platform := &MockPlatform{}
	sessionMgr := session.NewManager(session.ManagerConfig{
		Timeout: 300,
		MaxSize: 10,
	})
	agentMgr := agent.NewManager()
	mockAgent := &MockAgent{
		name:      "claude-code",
		available: true,
		response:  "Claude response",
	}
	agentMgr.Register(mockAgent)
	agentMgr.SetDefault("claude-code")

	sess := sessionMgr.Create("session-1", "user-1", "claude")
	sess.Context["claude_session_id"] = "claude-session-1"

	router := NewRouter(platform, sessionMgr, agentMgr)
	resp, err := router.handleAIMessage(context.Background(), &Message{
		ID:        "msg-2",
		Type:      TypeText,
		Content:   "Continue",
		UserID:    "user-1",
		SessionID: "session-1",
	})

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.True(t, resp.Success)
	assert.Equal(t, "claude-session-1", mockAgent.lastSID)
}

func TestHandleAIMessage_PersistsCodexSessionID(t *testing.T) {
	platform := &MockPlatform{}
	sessionMgr := session.NewManager(session.ManagerConfig{
		Timeout: 300,
		MaxSize: 10,
	})
	agentMgr := agent.NewManager()
	mockAgent := &MockAgent{
		name:      "codex",
		available: true,
		response:  "Codex response\n\n__SESSION_ID__: codex-thread-1",
	}
	agentMgr.Register(mockAgent)
	agentMgr.SetDefault("codex")

	sessionMgr.Create("session-1", "user-1", "codex")

	router := NewRouter(platform, sessionMgr, agentMgr)
	resp, err := router.handleAIMessage(context.Background(), &Message{
		ID:        "msg-1",
		Type:      TypeText,
		Content:   "Hello Codex",
		UserID:    "user-1",
		SessionID: "session-1",
	})

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.True(t, resp.Success)
	assert.Equal(t, "Codex response", resp.Content)
	assert.Equal(t, "", mockAgent.lastSID)

	sess, ok := sessionMgr.Get("session-1")
	assert.True(t, ok)
	assert.Equal(t, "codex-thread-1", sess.Context["codex_session_id"])
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
			name:         "发送长消息（按字符限制分块）",
			content:      strings.Repeat("这是一条测试消息。\n", 100), // 约 900 字符，超过 1000 需要分块
			expectError:  false,
			expectChunks: 1,
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
		{
			name:         "中文按字符分割不乱码",
			content:      strings.Repeat("你好", 120),
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
				if len([]rune(chunk)) > tt.maxSize {
					t.Errorf("chunk %d exceeds max size: %d > %d", i, len([]rune(chunk)), tt.maxSize)
				}
			}

			// 验证所有块拼接后等于原内容
			reconstructed := strings.Join(chunks, "")
			assert.Equal(t, tt.content, reconstructed)
		})
	}
}

func TestForwardStreamToPlatform_DoesNotArtificiallySplitDelta(t *testing.T) {
	platform := &MockPlatform{}
	router := NewRouter(platform, nil, nil)

	delta := strings.Repeat("你", 500)
	stream := make(chan agent.Event, 2)
	stream <- agent.Event{Type: agent.EventTypeDelta, Content: delta}
	stream <- agent.Event{Type: agent.EventTypeDone}
	close(stream)

	err := router.forwardStreamToPlatform(context.Background(), "user-delta", stream)

	assert.NoError(t, err)
	assert.Len(t, platform.streams, 1)
	assert.Len(t, platform.streams[0].chunks, 2)
	assert.Equal(t, delta, platform.streams[0].chunks[0]["content"])
	assert.Equal(t, "", platform.streams[0].chunks[1]["content"])
	assert.Equal(t, true, platform.streams[0].chunks[1]["done"])
}

func TestForwardStreamToPlatform_DoesNotDropRepeatedDelta(t *testing.T) {
	platform := &MockPlatform{}
	router := NewRouter(platform, nil, nil)

	stream := make(chan agent.Event, 3)
	stream <- agent.Event{Type: agent.EventTypeDelta, Content: "哈"}
	stream <- agent.Event{Type: agent.EventTypeDelta, Content: "哈"}
	stream <- agent.Event{Type: agent.EventTypeDone}
	close(stream)

	err := router.forwardStreamToPlatform(context.Background(), "user-repeat", stream)

	assert.NoError(t, err)
	assert.Len(t, platform.streams, 1)
	assert.Len(t, platform.streams[0].chunks, 2)
	assert.Equal(t, "哈哈", platform.streams[0].chunks[0]["content"])
	assert.Equal(t, "", platform.streams[0].chunks[1]["content"])
	assert.Equal(t, true, platform.streams[0].chunks[1]["done"])
}

func TestForwardStreamToPlatform_BuffersDeltaUntilSentenceBoundary(t *testing.T) {
	platform := &MockPlatform{}
	router := NewRouter(platform, nil, nil)

	stream := make(chan agent.Event, 4)
	stream <- agent.Event{Type: agent.EventTypeDelta, Content: "第一句"}
	stream <- agent.Event{Type: agent.EventTypeDelta, Content: "。"}
	stream <- agent.Event{Type: agent.EventTypeDelta, Content: "第二句"}
	stream <- agent.Event{Type: agent.EventTypeDone}
	close(stream)

	err := router.forwardStreamToPlatform(context.Background(), "user-boundary", stream)

	assert.NoError(t, err)
	assert.Len(t, platform.streams, 1)
	assert.Len(t, platform.streams[0].chunks, 3)
	assert.Equal(t, "第一句。", platform.streams[0].chunks[0]["content"])
	assert.Equal(t, "第二句", platform.streams[0].chunks[1]["content"])
	assert.Equal(t, "", platform.streams[0].chunks[2]["content"])
	assert.Equal(t, true, platform.streams[0].chunks[2]["done"])
}

func TestForwardStreamToPlatform_UsesSingleMessageIDForBufferedDeltas(t *testing.T) {
	platform := &MockPlatform{}
	router := NewRouter(platform, nil, nil)

	stream := make(chan agent.Event, 5)
	stream <- agent.Event{Type: agent.EventTypeDelta, Content: "a"}
	stream <- agent.Event{Type: agent.EventTypeDelta, Content: "b"}
	stream <- agent.Event{Type: agent.EventTypeDelta, Content: "c"}
	stream <- agent.Event{Type: agent.EventTypeDone}
	close(stream)

	err := router.forwardStreamToPlatform(context.Background(), "user-1", stream)

	assert.NoError(t, err)
	assert.Len(t, platform.streams, 1)
	assert.Len(t, platform.streams[0].chunks, 2)
	assert.Equal(t, "abc", platform.streams[0].chunks[0]["content"])
	assert.Equal(t, 0, platform.streams[0].chunks[0]["chunk_id"])
	assert.Equal(t, false, platform.streams[0].chunks[0]["done"])
	assert.Equal(t, "", platform.streams[0].chunks[1]["content"])
	assert.Equal(t, 1, platform.streams[0].chunks[1]["chunk_id"])
	assert.Equal(t, true, platform.streams[0].chunks[1]["done"])
}

func TestForwardStreamToPlatform_SendsFinalMessageAsSingleDoneChunk(t *testing.T) {
	platform := &MockPlatform{}
	router := NewRouter(platform, nil, nil)

	stream := make(chan agent.Event, 2)
	stream <- agent.Event{Type: agent.EventTypeMessage, Content: "final answer"}
	stream <- agent.Event{Type: agent.EventTypeDone}
	close(stream)

	err := router.forwardStreamToPlatform(context.Background(), "user-2", stream)

	assert.NoError(t, err)
	assert.Len(t, platform.streams, 1)
	assert.Len(t, platform.streams[0].chunks, 1)
	assert.Equal(t, "final answer", platform.streams[0].chunks[0]["content"])
	assert.Equal(t, true, platform.streams[0].chunks[0]["done"])
}

func TestForwardStreamToPlatform_IgnoresLateMessageAfterDone(t *testing.T) {
	platform := &MockPlatform{}
	router := NewRouter(platform, nil, nil)

	stream := make(chan agent.Event, 3)
	stream <- agent.Event{Type: agent.EventTypeMessage, Content: "first final"}
	stream <- agent.Event{Type: agent.EventTypeMessage, Content: "duplicate final"}
	stream <- agent.Event{Type: agent.EventTypeDone}
	close(stream)

	err := router.forwardStreamToPlatform(context.Background(), "user-3", stream)

	assert.NoError(t, err)
	assert.Len(t, platform.streams, 1)
	assert.Len(t, platform.streams[0].chunks, 1)
	assert.Equal(t, "first final", platform.streams[0].chunks[0]["content"])
	assert.Equal(t, true, platform.streams[0].chunks[0]["done"])
}
