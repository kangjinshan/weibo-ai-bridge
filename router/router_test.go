package router

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kangjinshan/weibo-ai-bridge/agent"
	"github.com/kangjinshan/weibo-ai-bridge/platform/weibo"
	"github.com/kangjinshan/weibo-ai-bridge/session"
	"github.com/stretchr/testify/assert"
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

type timedStreamChunk struct {
	content string
	done    bool
}

type timedStreamPlatform struct {
	stream *timedReplyStream
}

func (p *timedStreamPlatform) Reply(ctx context.Context, messageID string, content string) error {
	return nil
}

func (p *timedStreamPlatform) OpenReplyStream(ctx context.Context, userID string) (weibo.ChunkSender, error) {
	return p.stream, nil
}

type timedReplyStream struct {
	chunks chan timedStreamChunk
}

func (s *timedReplyStream) SendChunk(ctx context.Context, content string, done bool) error {
	s.chunks <- timedStreamChunk{content: content, done: done}
	return nil
}

type singleStreamGatePlatform struct {
	slot       chan struct{}
	opened     chan struct{}
	mu         sync.Mutex
	openCount  int
	replyTexts []string
}

func newSingleStreamGatePlatform() *singleStreamGatePlatform {
	p := &singleStreamGatePlatform{
		slot:   make(chan struct{}, 1),
		opened: make(chan struct{}, 8),
	}
	p.slot <- struct{}{}
	return p
}

func (p *singleStreamGatePlatform) Reply(ctx context.Context, messageID string, content string) error {
	p.mu.Lock()
	p.replyTexts = append(p.replyTexts, content)
	p.mu.Unlock()
	return nil
}

func (p *singleStreamGatePlatform) OpenReplyStream(ctx context.Context, userID string) (weibo.ChunkSender, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.slot:
	}

	p.mu.Lock()
	p.openCount++
	p.mu.Unlock()

	select {
	case p.opened <- struct{}{}:
	default:
	}

	return &singleStreamGateSender{platform: p}, nil
}

func (p *singleStreamGatePlatform) Replies() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.replyTexts...)
}

func (p *singleStreamGatePlatform) OpenCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.openCount
}

type singleStreamGateSender struct {
	platform *singleStreamGatePlatform
	released bool
	mu       sync.Mutex
}

func (s *singleStreamGateSender) SendChunk(ctx context.Context, content string, done bool) error {
	if strings.TrimSpace(content) != "" {
		s.platform.mu.Lock()
		s.platform.replyTexts = append(s.platform.replyTexts, content)
		s.platform.mu.Unlock()
	}

	if done {
		s.mu.Lock()
		if !s.released {
			s.released = true
			s.platform.slot <- struct{}{}
		}
		s.mu.Unlock()
	}
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

type MockInteractiveAgent struct {
	name      string
	available bool
	session   *MockInteractiveSession
	startFn   func(ctx context.Context, sessionID string) (agent.InteractiveSession, error)

	startCalls      int
	startSessionIDs []string
}

func (m *MockInteractiveAgent) Name() string {
	return m.name
}

func (m *MockInteractiveAgent) Execute(ctx context.Context, sessionID string, input string) (string, error) {
	return "", nil
}

func (m *MockInteractiveAgent) ExecuteStream(ctx context.Context, sessionID string, input string) (<-chan agent.Event, error) {
	stream := make(chan agent.Event)
	close(stream)
	return stream, nil
}

func (m *MockInteractiveAgent) IsAvailable() bool {
	return m.available
}

func (m *MockInteractiveAgent) StartSession(ctx context.Context, sessionID string) (agent.InteractiveSession, error) {
	m.startCalls++
	m.startSessionIDs = append(m.startSessionIDs, sessionID)
	if m.startFn != nil {
		return m.startFn(ctx, sessionID)
	}
	if m.session == nil {
		m.session = NewMockInteractiveSession()
	}
	return m.session, nil
}

type MockInteractiveSession struct {
	events       chan agent.Event
	sendFn       func(input string)
	sendErrFn    func(input string) error
	interruptFn  func()
	approveFn    func(action agent.ApprovalAction)
	approveErrFn func(action agent.ApprovalAction) error
	closeFn      func() error

	sentInputs []string
	interrupts int
	actions    []agent.ApprovalAction
	sessionID  string
}

func NewMockInteractiveSession() *MockInteractiveSession {
	return &MockInteractiveSession{
		events: make(chan agent.Event, 32),
	}
}

func (m *MockInteractiveSession) Send(input string) error {
	m.sentInputs = append(m.sentInputs, input)
	if m.sendErrFn != nil {
		if err := m.sendErrFn(input); err != nil {
			return err
		}
	}
	if m.sendFn != nil {
		m.sendFn(input)
	}
	return nil
}

func (m *MockInteractiveSession) RespondApproval(action agent.ApprovalAction) error {
	m.actions = append(m.actions, action)
	if m.approveErrFn != nil {
		if err := m.approveErrFn(action); err != nil {
			return err
		}
	}
	if m.approveFn != nil {
		m.approveFn(action)
	}
	return nil
}

func (m *MockInteractiveSession) Interrupt() error {
	m.interrupts++
	if m.interruptFn != nil {
		m.interruptFn()
	}
	return nil
}

func (m *MockInteractiveSession) Events() <-chan agent.Event {
	return m.events
}

func (m *MockInteractiveSession) CurrentSessionID() string {
	return m.sessionID
}

func (m *MockInteractiveSession) Close() error {
	if m.closeFn != nil {
		return m.closeFn()
	}
	return nil
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
	assert.Len(t, platform.replies, 2)
	assert.Equal(t, "user-456", platform.replies[0]["message_id"])
	assert.Equal(t, "", platform.replies[1]["content"])
	assert.Equal(t, true, platform.replies[1]["done"])
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
	assert.Equal(t, pendingNativeSessionID("user-1"), sessionMgr.GetActiveSessionID("user-1"))

	err = router.HandleMessage(context.Background(), &weibo.Message{
		ID:        "msg-ai",
		Type:      weibo.MessageTypeText,
		Content:   "Hello AI",
		UserID:    "user-1",
		UserName:  "test-user",
		Timestamp: 1234567891,
	})
	assert.NoError(t, err)
	assert.Len(t, platform.replies, 3)
	assert.Equal(t, "Prepared a new native session (Agent: codex). Send your next message to create it.", platform.replies[0]["content"])
	assert.Equal(t, "Codex response", platform.replies[1]["content"])
	assert.Equal(t, "", platform.replies[2]["content"])
	assert.Equal(t, true, platform.replies[2]["done"])
}

func TestHandleMessage_PassesSessionWorkDirToAgentContext(t *testing.T) {
	platform := &MockPlatform{}
	sessionMgr := session.NewManager(session.ManagerConfig{
		Timeout: 300,
		MaxSize: 10,
	})

	var gotWorkDir string
	mockAgent := &MockAgent{
		name:      "codex",
		available: true,
		streamFn: func(ctx context.Context, sessionID string, input string) (<-chan agent.Event, error) {
			gotWorkDir = agent.WorkDirFromContext(ctx)
			events := make(chan agent.Event, 2)
			events <- agent.Event{Type: agent.EventTypeMessage, Content: "ok"}
			events <- agent.Event{Type: agent.EventTypeDone}
			close(events)
			return events, nil
		},
	}
	agentMgr := agent.NewManager()
	agentMgr.Register(mockAgent)
	agentMgr.SetDefault("codex")

	sess := sessionMgr.Create("session-1", "user-1", "codex")
	assert.NotNil(t, sess)
	sess.Update("work_dir", "/tmp/project-a")
	assert.True(t, sessionMgr.SetActiveSession("user-1", "session-1"))

	router := NewRouter(platform, sessionMgr, agentMgr)
	err := router.HandleMessage(context.Background(), &weibo.Message{
		ID:        "msg-workdir",
		Type:      weibo.MessageTypeText,
		Content:   "hello",
		UserID:    "user-1",
		UserName:  "test-user",
		Timestamp: 1234567890,
	})

	assert.NoError(t, err)
	assert.Equal(t, "/tmp/project-a", gotWorkDir)
}

func TestHandleMessage_RestartsClosedInteractiveSession(t *testing.T) {
	platform := &MockPlatform{}
	sessionMgr := session.NewManager(session.ManagerConfig{
		Timeout: 300,
		MaxSize: 10,
	})

	firstSession := NewMockInteractiveSession()
	firstSession.sessionID = "claude-session-1"
	firstSession.sendFn = func(input string) {
		firstSession.events <- agent.Event{Type: agent.EventTypeSession, SessionID: firstSession.sessionID}
		firstSession.events <- agent.Event{Type: agent.EventTypeMessage, Content: "first reply"}
		firstSession.events <- agent.Event{Type: agent.EventTypeDone}
	}

	secondSession := NewMockInteractiveSession()
	secondSession.sessionID = "claude-session-1"
	secondSession.sendFn = func(input string) {
		secondSession.events <- agent.Event{Type: agent.EventTypeSession, SessionID: secondSession.sessionID}
		secondSession.events <- agent.Event{Type: agent.EventTypeMessage, Content: "second reply"}
		secondSession.events <- agent.Event{Type: agent.EventTypeDone}
	}

	agentMgr := agent.NewManager()
	mockAgent := &MockInteractiveAgent{
		name:      "claude-code",
		available: true,
	}
	mockAgent.startFn = func(ctx context.Context, sessionID string) (agent.InteractiveSession, error) {
		switch mockAgent.startCalls {
		case 1:
			return firstSession, nil
		case 2:
			return secondSession, nil
		default:
			t.Fatalf("unexpected StartSession call %d", mockAgent.startCalls)
			return nil, nil
		}
	}
	agentMgr.Register(mockAgent)
	agentMgr.SetDefault("claude-code")

	router := NewRouter(platform, sessionMgr, agentMgr)

	err := router.HandleMessage(context.Background(), &weibo.Message{
		ID:        "msg-1",
		Type:      weibo.MessageTypeText,
		Content:   "hello",
		UserID:    "user-1",
		UserName:  "test-user",
		Timestamp: 1234567890,
	})
	assert.NoError(t, err)
	assert.Equal(t, []string{""}, mockAgent.startSessionIDs)

	firstSession.sendErrFn = func(input string) error {
		return errors.New("claude session is not running")
	}

	err = router.HandleMessage(context.Background(), &weibo.Message{
		ID:        "msg-2",
		Type:      weibo.MessageTypeText,
		Content:   "follow up",
		UserID:    "user-1",
		UserName:  "test-user",
		Timestamp: 1234567891,
	})
	assert.NoError(t, err)
	assert.Equal(t, 2, mockAgent.startCalls)
	assert.Equal(t, []string{"", "claude-session-1"}, mockAgent.startSessionIDs)
	assert.Equal(t, []string{"follow up"}, secondSession.sentInputs)
	assert.Len(t, platform.streams, 2)
	assert.Equal(t, "second reply", platform.streams[1].chunks[0]["content"])
}

func TestHandleMessage_DirSetRestartsInteractiveSessionWithNewWorkDir(t *testing.T) {
	platform := &MockPlatform{}
	sessionMgr := session.NewManager(session.ManagerConfig{
		Timeout: 300,
		MaxSize: 10,
	})

	oldDir := t.TempDir()
	newDir := t.TempDir()

	sess := sessionMgr.Create("session-1", "user-1", "claude")
	assert.NotNil(t, sess)
	sess.Update("work_dir", oldDir)
	assert.True(t, sessionMgr.SetActiveSession("user-1", "session-1"))

	firstSession := NewMockInteractiveSession()
	firstSession.sessionID = "claude-session-1"
	firstSession.sendFn = func(input string) {
		firstSession.events <- agent.Event{Type: agent.EventTypeMessage, Content: "first reply"}
		firstSession.events <- agent.Event{Type: agent.EventTypeDone}
	}

	secondSession := NewMockInteractiveSession()
	secondSession.sessionID = "claude-session-1"
	secondSession.sendFn = func(input string) {
		secondSession.events <- agent.Event{Type: agent.EventTypeMessage, Content: "second reply"}
		secondSession.events <- agent.Event{Type: agent.EventTypeDone}
	}

	var startWorkDirs []string
	agentMgr := agent.NewManager()
	mockAgent := &MockInteractiveAgent{
		name:      "claude-code",
		available: true,
	}
	mockAgent.startFn = func(ctx context.Context, sessionID string) (agent.InteractiveSession, error) {
		startWorkDirs = append(startWorkDirs, agent.WorkDirFromContext(ctx))
		switch mockAgent.startCalls {
		case 1:
			return firstSession, nil
		case 2:
			return secondSession, nil
		default:
			t.Fatalf("unexpected StartSession call %d", mockAgent.startCalls)
			return nil, nil
		}
	}
	agentMgr.Register(mockAgent)
	agentMgr.SetDefault("claude-code")

	router := NewRouter(platform, sessionMgr, agentMgr)

	err := router.HandleMessage(context.Background(), &weibo.Message{
		ID:        "msg-1",
		Type:      weibo.MessageTypeText,
		Content:   "hello",
		UserID:    "user-1",
		UserName:  "test-user",
		Timestamp: 1234567890,
	})
	assert.NoError(t, err)
	assert.Equal(t, []string{oldDir}, startWorkDirs)

	err = router.HandleMessage(context.Background(), &weibo.Message{
		ID:        "msg-dir",
		Type:      weibo.MessageTypeText,
		Content:   "/dir " + newDir,
		UserID:    "user-1",
		UserName:  "test-user",
		Timestamp: 1234567891,
	})
	assert.NoError(t, err)

	err = router.HandleMessage(context.Background(), &weibo.Message{
		ID:        "msg-2",
		Type:      weibo.MessageTypeText,
		Content:   "again",
		UserID:    "user-1",
		UserName:  "test-user",
		Timestamp: 1234567892,
	})
	assert.NoError(t, err)
	assert.Equal(t, []string{oldDir, newDir}, startWorkDirs)
}

func TestHandleMessage_CommandReplyUsesDirectReplyPath(t *testing.T) {
	platform := &MockPlatform{}
	sessionMgr := session.NewManager(session.ManagerConfig{
		Timeout: 300,
		MaxSize: 10,
	})
	agentMgr := agent.NewManager()
	agentMgr.Register(&MockAgent{
		name:      "codex",
		response:  "Codex response",
		available: true,
	})
	agentMgr.SetDefault("codex")

	router := NewRouter(platform, sessionMgr, agentMgr)

	err := router.HandleMessage(context.Background(), &weibo.Message{
		ID:        "msg-command",
		Type:      weibo.MessageTypeText,
		Content:   "/new codex",
		UserID:    "user-command",
		UserName:  "test-user",
		Timestamp: 1234567890,
	})
	assert.NoError(t, err)
	assert.Len(t, platform.streams, 0)
	assert.Len(t, platform.replies, 1)
	assert.Equal(t, "user-command", platform.replies[0]["message_id"])
	assert.Contains(t, platform.replies[0]["content"], "Prepared a new native session")
}

func TestHandleMessage_AutoCreatesSessionOnFirstUserMessage(t *testing.T) {
	platform := &MockPlatform{}
	sessionMgr := session.NewManager(session.ManagerConfig{
		Timeout: 300,
		MaxSize: 10,
	})
	agentMgr := agent.NewManager()
	agentMgr.Register(&MockAgent{
		name:      "codex",
		response:  "Codex response",
		available: true,
	})
	agentMgr.SetDefault("codex")

	router := NewRouter(platform, sessionMgr, agentMgr)

	err := router.HandleMessage(context.Background(), &weibo.Message{
		ID:        "msg-auto-new",
		Type:      weibo.MessageTypeText,
		Content:   "第一条消息直接开始",
		UserID:    "user-auto",
		UserName:  "test-user",
		Timestamp: 1234567890,
	})
	assert.NoError(t, err)

	activeSession, ok := sessionMgr.GetActiveSession("user-auto")
	assert.True(t, ok)
	assert.Equal(t, pendingNativeSessionID("user-auto"), activeSession.ID)
	assert.Equal(t, "第一条消息直接开始", activeSession.Title)
	assert.Len(t, platform.replies, 2)
	assert.Equal(t, "Codex response", platform.replies[0]["content"])
	assert.Equal(t, "", platform.replies[1]["content"])
	assert.Equal(t, true, platform.replies[1]["done"])
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
	claudeSID, hasClaudeSID := sess.ContextString("claude_session_id")
	assert.True(t, hasClaudeSID)
	assert.Equal(t, "claude-session-1", claudeSID)
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
	sess.SetContext("claude_session_id", "claude-session-1")

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
	codexSID, hasCodexSID := sess.ContextString("codex_session_id")
	assert.True(t, hasCodexSID)
	assert.Equal(t, "codex-thread-1", codexSID)
}

func TestHandleAIMessage_AdoptsPendingOrBridgeSessionIDToNativeSessionID(t *testing.T) {
	platform := &MockPlatform{}
	sessionMgr := session.NewManager(session.ManagerConfig{
		Timeout: 300,
		MaxSize: 10,
	})
	agentMgr := agent.NewManager()
	mockAgent := &MockAgent{
		name:      "codex",
		available: true,
		response:  "Codex response\n\n__SESSION_ID__: codex-thread-9",
	}
	agentMgr.Register(mockAgent)
	agentMgr.SetDefault("codex")

	sessionMgr.Create("user-1-1", "user-1", "codex")

	router := NewRouter(platform, sessionMgr, agentMgr)
	resp, err := router.handleAIMessage(context.Background(), &Message{
		ID:        "msg-1",
		Type:      TypeText,
		Content:   "Hello Codex",
		UserID:    "user-1",
		SessionID: "user-1-1",
	})

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.True(t, resp.Success)
	assert.Equal(t, "Codex response", resp.Content)
	assert.Equal(t, "codex-thread-9", sessionMgr.GetActiveSessionID("user-1"))

	_, oldExists := sessionMgr.Get("user-1-1")
	assert.False(t, oldExists)

	adopted, adoptedExists := sessionMgr.Get("codex-thread-9")
	assert.True(t, adoptedExists)
	adoptedSID, hasAdoptedSID := adopted.ContextString("codex_session_id")
	assert.True(t, hasAdoptedSID)
	assert.Equal(t, "codex-thread-9", adoptedSID)
}

func TestHandleAIMessage_SetsSessionTitleFromFirstQuestionOnly(t *testing.T) {
	platform := &MockPlatform{}
	sessionMgr := session.NewManager(session.ManagerConfig{
		Timeout: 300,
		MaxSize: 10,
	})
	agentMgr := agent.NewManager()
	mockAgent := &MockAgent{
		name:      "codex",
		available: true,
		response:  "Codex response",
	}
	agentMgr.Register(mockAgent)
	agentMgr.SetDefault("codex")

	sessionMgr.Create("session-1", "user-1", "codex")

	router := NewRouter(platform, sessionMgr, agentMgr)
	firstQuestion := strings.Repeat("你", 60)
	_, err := router.handleAIMessage(context.Background(), &Message{
		ID:        "msg-1",
		Type:      TypeText,
		Content:   firstQuestion,
		UserID:    "user-1",
		SessionID: "session-1",
	})
	assert.NoError(t, err)

	_, err = router.handleAIMessage(context.Background(), &Message{
		ID:        "msg-2",
		Type:      TypeText,
		Content:   "第二个问题不会覆盖标题",
		UserID:    "user-1",
		SessionID: "session-1",
	})
	assert.NoError(t, err)

	sess, ok := sessionMgr.Get("session-1")
	assert.True(t, ok)
	assert.Len(t, []rune(sess.Title), 50)
	assert.Equal(t, string([]rune(firstQuestion)[:50]), sess.Title)
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

func TestStreamReplySender_PrependsLineBreakAfterIdleGap(t *testing.T) {
	platform := &MockPlatform{}
	writer := &MockReplyStream{platform: platform, userID: "user-gap", messageID: "stream-msg-user-gap"}
	now := time.Unix(100, 0)

	sender := newStreamReplySender(writer)
	sender.now = func() time.Time { return now }
	sender.idleLineBreakAfter = 5 * time.Second

	err := sender.PushDelta(context.Background(), "第一句。")
	assert.NoError(t, err)

	now = now.Add(6 * time.Second)
	err = sender.PushDelta(context.Background(), "第二句。")
	assert.NoError(t, err)

	assert.Len(t, writer.chunks, 2)
	assert.Equal(t, "第一句。", writer.chunks[0]["content"])
	assert.Equal(t, "\n第二句。", writer.chunks[1]["content"])
}

func TestStreamReplySender_DoesNotPrependLineBreakBeforeIdleGap(t *testing.T) {
	platform := &MockPlatform{}
	writer := &MockReplyStream{platform: platform, userID: "user-no-gap", messageID: "stream-msg-user-no-gap"}
	now := time.Unix(100, 0)

	sender := newStreamReplySender(writer)
	sender.now = func() time.Time { return now }
	sender.idleLineBreakAfter = 5 * time.Second

	err := sender.PushDelta(context.Background(), "第一句。")
	assert.NoError(t, err)

	now = now.Add(4 * time.Second)
	err = sender.PushDelta(context.Background(), "第二句。")
	assert.NoError(t, err)

	assert.Len(t, writer.chunks, 2)
	assert.Equal(t, "第一句。", writer.chunks[0]["content"])
	assert.Equal(t, "第二句。", writer.chunks[1]["content"])
}

func TestStreamReplySender_FlushesShortDeltaAfterBufferDelay(t *testing.T) {
	platform := &MockPlatform{}
	writer := &MockReplyStream{platform: platform, userID: "user-delay", messageID: "stream-msg-user-delay"}
	now := time.Unix(100, 0)

	sender := newStreamReplySender(writer)
	sender.now = func() time.Time { return now }
	sender.maxBufferDelay = 700 * time.Millisecond

	err := sender.PushDelta(context.Background(), "哈")
	assert.NoError(t, err)
	assert.Len(t, writer.chunks, 0)

	now = now.Add(800 * time.Millisecond)
	err = sender.PushDelta(context.Background(), "哈")
	assert.NoError(t, err)

	assert.Len(t, writer.chunks, 1)
	assert.Equal(t, "哈哈", writer.chunks[0]["content"])
	assert.Equal(t, false, writer.chunks[0]["done"])
}

func TestStreamReplySender_FlushesSingleShortDeltaAfterBufferDelay(t *testing.T) {
	platform := &MockPlatform{}
	writer := &MockReplyStream{platform: platform, userID: "user-single-delay", messageID: "stream-msg-user-single-delay"}
	now := time.Unix(100, 0)

	sender := newStreamReplySender(writer)
	sender.now = func() time.Time { return now }
	sender.maxBufferDelay = 700 * time.Millisecond

	err := sender.PushDelta(context.Background(), "哈")
	assert.NoError(t, err)
	assert.Len(t, writer.chunks, 0)

	now = now.Add(800 * time.Millisecond)
	err = sender.FlushPendingIfDelayed(context.Background())
	assert.NoError(t, err)

	assert.Len(t, writer.chunks, 1)
	assert.Equal(t, "哈", writer.chunks[0]["content"])
	assert.Equal(t, false, writer.chunks[0]["done"])
}

func TestForwardStreamToPlatform_FlushesIdleShortDeltaWithoutNextChunk(t *testing.T) {
	platform := &timedStreamPlatform{
		stream: &timedReplyStream{chunks: make(chan timedStreamChunk, 4)},
	}
	router := NewRouter(platform, nil, nil)

	stream := make(chan agent.Event, 2)
	errCh := make(chan error, 1)
	go func() {
		errCh <- router.forwardStreamToPlatform(context.Background(), "user-idle-delay", stream)
	}()

	stream <- agent.Event{Type: agent.EventTypeDelta, Content: "哈"}

	select {
	case chunk := <-platform.stream.chunks:
		assert.Equal(t, "哈", chunk.content)
		assert.False(t, chunk.done)
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("timed out waiting for idle short delta flush")
	}

	stream <- agent.Event{Type: agent.EventTypeDone}
	close(stream)

	select {
	case chunk := <-platform.stream.chunks:
		assert.Equal(t, "", chunk.content)
		assert.True(t, chunk.done)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for final done chunk")
	}

	select {
	case err := <-errCh:
		assert.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for forwardStreamToPlatform to finish")
	}
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

func TestForwardStreamToPlatform_FlushesDeltaWithoutPunctuationAfterThreshold(t *testing.T) {
	platform := &MockPlatform{}
	router := NewRouter(platform, nil, nil)

	stream := make(chan agent.Event, 3)
	stream <- agent.Event{Type: agent.EventTypeDelta, Content: strings.Repeat("你", 12)}
	stream <- agent.Event{Type: agent.EventTypeDelta, Content: strings.Repeat("好", 12)}
	stream <- agent.Event{Type: agent.EventTypeDone}
	close(stream)

	err := router.forwardStreamToPlatform(context.Background(), "user-no-punct", stream)

	assert.NoError(t, err)
	assert.Len(t, platform.streams, 1)
	assert.Len(t, platform.streams[0].chunks, 2)
	assert.Equal(t, strings.Repeat("你", 12)+strings.Repeat("好", 12), platform.streams[0].chunks[0]["content"])
	assert.Equal(t, "", platform.streams[0].chunks[1]["content"])
	assert.Equal(t, true, platform.streams[0].chunks[1]["done"])
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

func TestForwardStreamToPlatform_SendsFinalMessageThenDoneChunk(t *testing.T) {
	platform := &MockPlatform{}
	router := NewRouter(platform, nil, nil)

	stream := make(chan agent.Event, 2)
	stream <- agent.Event{Type: agent.EventTypeMessage, Content: "final answer"}
	stream <- agent.Event{Type: agent.EventTypeDone}
	close(stream)

	err := router.forwardStreamToPlatform(context.Background(), "user-2", stream)

	assert.NoError(t, err)
	assert.Len(t, platform.streams, 1)
	assert.Len(t, platform.streams[0].chunks, 2)
	assert.Equal(t, "final answer", platform.streams[0].chunks[0]["content"])
	assert.Equal(t, false, platform.streams[0].chunks[0]["done"])
	assert.Equal(t, "", platform.streams[0].chunks[1]["content"])
	assert.Equal(t, true, platform.streams[0].chunks[1]["done"])
}

func TestForwardStreamToPlatform_SettlesCurrentStreamOnContextCancel(t *testing.T) {
	platform := &MockPlatform{}
	router := NewRouter(platform, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	stream := make(chan agent.Event, 1)
	stream <- agent.Event{Type: agent.EventTypeDelta, Content: "partial"}

	errCh := make(chan error, 1)
	go func() {
		errCh <- router.forwardStreamToPlatform(ctx, "user-cancel", stream)
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()

	err := <-errCh
	assert.Error(t, err)
	assert.Len(t, platform.streams, 1)
	assert.Len(t, platform.streams[0].chunks, 2)
	assert.Equal(t, "partial", platform.streams[0].chunks[0]["content"])
	assert.Equal(t, "", platform.streams[0].chunks[1]["content"])
	assert.Equal(t, true, platform.streams[0].chunks[1]["done"])
}

func TestForwardStreamToPlatform_IgnoresContextCanceledErrorEvent(t *testing.T) {
	platform := &MockPlatform{}
	router := NewRouter(platform, nil, nil)

	stream := make(chan agent.Event, 2)
	stream <- agent.Event{Type: agent.EventTypeError, Error: context.Canceled.Error()}
	stream <- agent.Event{Type: agent.EventTypeDone}
	close(stream)

	err := router.forwardStreamToPlatform(context.Background(), "user-cancel-error", stream)

	assert.NoError(t, err)
	assert.Len(t, platform.replies, 0)
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
	assert.Len(t, platform.streams[0].chunks, 2)
	assert.Equal(t, "first final", platform.streams[0].chunks[0]["content"])
	assert.Equal(t, false, platform.streams[0].chunks[0]["done"])
	assert.Equal(t, "", platform.streams[0].chunks[1]["content"])
	assert.Equal(t, true, platform.streams[0].chunks[1]["done"])
}

func TestHandleMessage_ApprovalPromptFromInteractiveAgent(t *testing.T) {
	platform := &MockPlatform{}
	sessionMgr := session.NewManager(session.ManagerConfig{Timeout: 300, MaxSize: 10})
	agentMgr := agent.NewManager()

	liveSession := NewMockInteractiveSession()
	liveSession.sendFn = func(input string) {
		liveSession.events <- agent.Event{
			Type:      agent.EventTypeApproval,
			ToolName:  "command_execution",
			ToolInput: "rm -rf ./tmp",
		}
	}

	interactiveAgent := &MockInteractiveAgent{
		name:      "claude-code",
		available: true,
		session:   liveSession,
	}
	agentMgr.Register(interactiveAgent)
	agentMgr.SetDefault("claude-code")

	router := NewRouter(platform, sessionMgr, agentMgr)

	err := router.HandleMessage(context.Background(), &weibo.Message{
		ID:        "msg-approval",
		Type:      weibo.MessageTypeText,
		Content:   "请清理临时目录",
		UserID:    "user-approval",
		UserName:  "tester",
		Timestamp: 1,
	})

	assert.NoError(t, err)
	assert.Len(t, platform.replies, 2)
	assert.Contains(t, platform.replies[0]["content"], "请回复：允许 / 取消 / 允许所有")
	assert.Contains(t, platform.replies[0]["content"], "rm -rf ./tmp")
	assert.Equal(t, "", platform.replies[1]["content"])
	assert.Equal(t, true, platform.replies[1]["done"])
}

func TestHandleMessage_AllowAllContinuesPendingInteractiveSession(t *testing.T) {
	platform := &MockPlatform{}
	sessionMgr := session.NewManager(session.ManagerConfig{Timeout: 300, MaxSize: 10})
	agentMgr := agent.NewManager()

	liveSession := NewMockInteractiveSession()
	liveSession.sendFn = func(input string) {
		liveSession.events <- agent.Event{
			Type:      agent.EventTypeApproval,
			ToolName:  "command_execution",
			ToolInput: "git push origin main",
		}
	}
	liveSession.approveFn = func(action agent.ApprovalAction) {
		liveSession.events <- agent.Event{Type: agent.EventTypeMessage, Content: "继续执行完成"}
		liveSession.events <- agent.Event{Type: agent.EventTypeDone}
	}

	interactiveAgent := &MockInteractiveAgent{
		name:      "codex",
		available: true,
		session:   liveSession,
	}
	agentMgr.Register(interactiveAgent)
	agentMgr.SetDefault("codex")

	router := NewRouter(platform, sessionMgr, agentMgr)

	err := router.HandleMessage(context.Background(), &weibo.Message{
		ID:        "msg-start",
		Type:      weibo.MessageTypeText,
		Content:   "帮我推送代码",
		UserID:    "user-allow-all",
		UserName:  "tester",
		Timestamp: 1,
	})
	assert.NoError(t, err)

	err = router.HandleMessage(context.Background(), &weibo.Message{
		ID:        "msg-allow-all",
		Type:      weibo.MessageTypeText,
		Content:   "允许所有 @bridge",
		UserID:    "user-allow-all",
		UserName:  "tester",
		Timestamp: 2,
	})

	assert.NoError(t, err)
	assert.Equal(t, []agent.ApprovalAction{agent.ApprovalActionAllowAll}, liveSession.actions)
	assert.Len(t, platform.replies, 5)
	assert.Equal(t, "", platform.replies[1]["content"])
	assert.Contains(t, platform.replies[2]["content"], "授权成功，这对话内将不再需要再次授权。")
	assert.Equal(t, "继续执行完成", platform.replies[3]["content"])
	assert.Equal(t, "", platform.replies[4]["content"])
	assert.Equal(t, true, platform.replies[4]["done"])
}

func TestHandleMessage_AllowAndCancelContinuePendingInteractiveSession(t *testing.T) {
	tests := []struct {
		name           string
		agentName      string
		replyContent   string
		approvalInput  string
		expectedAction agent.ApprovalAction
		expectedText   string
	}{
		{
			name:           "claude allow",
			agentName:      "claude-code",
			replyContent:   "允许 @bridge",
			approvalInput:  "rm -rf ./tmp",
			expectedAction: agent.ApprovalActionAllow,
			expectedText:   "已按授权继续执行",
		},
		{
			name:           "codex cancel",
			agentName:      "codex",
			replyContent:   "取消 @bridge",
			approvalInput:  "git push origin main",
			expectedAction: agent.ApprovalActionCancel,
			expectedText:   "已取消本次执行",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			platform := &MockPlatform{}
			sessionMgr := session.NewManager(session.ManagerConfig{Timeout: 300, MaxSize: 10})
			agentMgr := agent.NewManager()

			liveSession := NewMockInteractiveSession()
			liveSession.sendFn = func(input string) {
				liveSession.events <- agent.Event{
					Type:      agent.EventTypeApproval,
					ToolName:  "command_execution",
					ToolInput: tt.approvalInput,
				}
			}
			liveSession.approveFn = func(action agent.ApprovalAction) {
				liveSession.events <- agent.Event{Type: agent.EventTypeMessage, Content: tt.expectedText}
				liveSession.events <- agent.Event{Type: agent.EventTypeDone}
			}

			interactiveAgent := &MockInteractiveAgent{
				name:      tt.agentName,
				available: true,
				session:   liveSession,
			}
			agentMgr.Register(interactiveAgent)
			agentMgr.SetDefault(tt.agentName)

			router := NewRouter(platform, sessionMgr, agentMgr)

			err := router.HandleMessage(context.Background(), &weibo.Message{
				ID:        "msg-start",
				Type:      weibo.MessageTypeText,
				Content:   "先执行这个操作",
				UserID:    "user-" + strings.ReplaceAll(tt.name, " ", "-"),
				UserName:  "tester",
				Timestamp: 1,
			})
			assert.NoError(t, err)

			err = router.HandleMessage(context.Background(), &weibo.Message{
				ID:        "msg-approval-reply",
				Type:      weibo.MessageTypeText,
				Content:   tt.replyContent,
				UserID:    "user-" + strings.ReplaceAll(tt.name, " ", "-"),
				UserName:  "tester",
				Timestamp: 2,
			})

			assert.NoError(t, err)
			assert.Equal(t, []agent.ApprovalAction{tt.expectedAction}, liveSession.actions)
			assert.Contains(t, platform.replies[0]["content"], "请回复：允许 / 取消 / 允许所有")
			assert.Equal(t, tt.expectedText, platform.replies[2]["content"])
			assert.Equal(t, "", platform.replies[3]["content"])
			assert.Equal(t, true, platform.replies[3]["done"])
		})
	}
}

func TestHandleMessage_ApprovalReplyRestartsClosedInteractiveSession(t *testing.T) {
	platform := &MockPlatform{}
	sessionMgr := session.NewManager(session.ManagerConfig{Timeout: 300, MaxSize: 10})
	agentMgr := agent.NewManager()

	firstSession := NewMockInteractiveSession()
	firstSession.sessionID = "claude-session-approval-1"
	firstSession.sendFn = func(input string) {
		firstSession.events <- agent.Event{Type: agent.EventTypeSession, SessionID: firstSession.sessionID}
		firstSession.events <- agent.Event{
			Type:      agent.EventTypeApproval,
			ToolName:  "Read",
			ToolInput: "/tmp/reference.md",
		}
	}
	firstSession.approveFn = func(action agent.ApprovalAction) {}

	secondSession := NewMockInteractiveSession()
	secondSession.sessionID = "claude-session-approval-1"
	secondSession.approveFn = func(action agent.ApprovalAction) {
		secondSession.events <- agent.Event{Type: agent.EventTypeMessage, Content: "已按授权继续执行"}
		secondSession.events <- agent.Event{Type: agent.EventTypeDone}
	}

	mockAgent := &MockInteractiveAgent{
		name:      "claude-code",
		available: true,
	}
	mockAgent.startFn = func(ctx context.Context, sessionID string) (agent.InteractiveSession, error) {
		switch mockAgent.startCalls {
		case 1:
			return firstSession, nil
		case 2:
			return secondSession, nil
		default:
			t.Fatalf("unexpected StartSession call %d", mockAgent.startCalls)
			return nil, nil
		}
	}
	agentMgr.Register(mockAgent)
	agentMgr.SetDefault("claude-code")

	router := NewRouter(platform, sessionMgr, agentMgr)

	err := router.HandleMessage(context.Background(), &weibo.Message{
		ID:        "msg-start",
		Type:      weibo.MessageTypeText,
		Content:   "先执行这个操作",
		UserID:    "user-approval-restart",
		UserName:  "tester",
		Timestamp: 1,
	})
	assert.NoError(t, err)
	assert.Equal(t, []string{""}, mockAgent.startSessionIDs)

	firstSession.approveErrFn = func(action agent.ApprovalAction) error {
		return errors.New("claude session is not running")
	}

	err = router.HandleMessage(context.Background(), &weibo.Message{
		ID:        "msg-approval-reply",
		Type:      weibo.MessageTypeText,
		Content:   "允许",
		UserID:    "user-approval-restart",
		UserName:  "tester",
		Timestamp: 2,
	})

	assert.NoError(t, err)
	assert.Equal(t, 2, mockAgent.startCalls)
	assert.Equal(t, []string{"", "claude-session-approval-1"}, mockAgent.startSessionIDs)
	assert.Equal(t, []agent.ApprovalAction{agent.ApprovalActionAllow}, secondSession.actions)
	assert.Equal(t, "已按授权继续执行", platform.replies[2]["content"])
	assert.Equal(t, "", platform.replies[3]["content"])
	assert.Equal(t, true, platform.replies[3]["done"])
}

func TestHandleMessage_RestartsInteractiveSessionOnClosedNetworkConnection(t *testing.T) {
	platform := &MockPlatform{}
	sessionMgr := session.NewManager(session.ManagerConfig{
		Timeout: 300,
		MaxSize: 10,
	})

	firstSession := NewMockInteractiveSession()
	firstSession.sessionID = "codex-thread-1"
	firstSession.sendFn = func(input string) {
		firstSession.events <- agent.Event{Type: agent.EventTypeSession, SessionID: firstSession.sessionID}
		firstSession.events <- agent.Event{Type: agent.EventTypeMessage, Content: "first reply"}
		firstSession.events <- agent.Event{Type: agent.EventTypeDone}
	}

	secondSession := NewMockInteractiveSession()
	secondSession.sessionID = "codex-thread-1"
	secondSession.sendFn = func(input string) {
		secondSession.events <- agent.Event{Type: agent.EventTypeSession, SessionID: secondSession.sessionID}
		secondSession.events <- agent.Event{Type: agent.EventTypeMessage, Content: "second reply"}
		secondSession.events <- agent.Event{Type: agent.EventTypeDone}
	}

	agentMgr := agent.NewManager()
	mockAgent := &MockInteractiveAgent{
		name:      "codex",
		available: true,
	}
	mockAgent.startFn = func(ctx context.Context, sessionID string) (agent.InteractiveSession, error) {
		switch mockAgent.startCalls {
		case 1:
			return firstSession, nil
		case 2:
			return secondSession, nil
		default:
			t.Fatalf("unexpected StartSession call %d", mockAgent.startCalls)
			return nil, nil
		}
	}
	agentMgr.Register(mockAgent)
	agentMgr.SetDefault("codex")

	router := NewRouter(platform, sessionMgr, agentMgr)

	err := router.HandleMessage(context.Background(), &weibo.Message{
		ID:        "msg-1",
		Type:      weibo.MessageTypeText,
		Content:   "hello",
		UserID:    "user-closed-network",
		UserName:  "test-user",
		Timestamp: 1234567890,
	})
	assert.NoError(t, err)
	assert.Equal(t, []string{""}, mockAgent.startSessionIDs)

	firstSession.sendErrFn = func(input string) error {
		return errors.New("write tcp 127.0.0.1:56566->127.0.0.1:40503: use of closed network connection")
	}

	err = router.HandleMessage(context.Background(), &weibo.Message{
		ID:        "msg-2",
		Type:      weibo.MessageTypeText,
		Content:   "follow up",
		UserID:    "user-closed-network",
		UserName:  "test-user",
		Timestamp: 1234567891,
	})
	assert.NoError(t, err)
	assert.Equal(t, 2, mockAgent.startCalls)
	assert.Equal(t, []string{"", "codex-thread-1"}, mockAgent.startSessionIDs)
	assert.Equal(t, []string{"follow up"}, secondSession.sentInputs)
	assert.Len(t, platform.streams, 2)
	assert.Equal(t, "second reply", platform.streams[1].chunks[0]["content"])
}

func TestHandleMessage_RecreatesHermesSessionOnProvider404(t *testing.T) {
	platform := &MockPlatform{}
	sessionMgr := session.NewManager(session.ManagerConfig{
		Timeout: 300,
		MaxSize: 10,
	})
	active := sessionMgr.Create("session-hermes", "user-hermes-404", "hermes")
	sessionMgr.UpdateSession(active.ID, "hermes_session_id", "bad-hermes-native")

	firstSession := NewMockInteractiveSession()
	firstSession.sessionID = "bad-hermes-native"
	firstSession.sendFn = func(input string) {
		firstSession.events <- agent.Event{
			Type:  agent.EventTypeError,
			Error: "API call failed after 3 retries: HTTP 404: Resource not found",
		}
		firstSession.events <- agent.Event{Type: agent.EventTypeDone}
	}

	secondSession := NewMockInteractiveSession()
	secondSession.sessionID = "good-hermes-native"
	secondSession.sendFn = func(input string) {
		secondSession.events <- agent.Event{Type: agent.EventTypeSession, SessionID: secondSession.sessionID}
		secondSession.events <- agent.Event{Type: agent.EventTypeMessage, Content: "fresh reply"}
		secondSession.events <- agent.Event{Type: agent.EventTypeDone}
	}

	agentMgr := agent.NewManager()
	mockAgent := &MockInteractiveAgent{
		name:      "hermes",
		available: true,
	}
	mockAgent.startFn = func(ctx context.Context, sessionID string) (agent.InteractiveSession, error) {
		switch mockAgent.startCalls {
		case 1:
			return firstSession, nil
		case 2:
			return secondSession, nil
		default:
			t.Fatalf("unexpected StartSession call %d", mockAgent.startCalls)
			return nil, nil
		}
	}
	agentMgr.Register(mockAgent)
	agentMgr.SetDefault("hermes")

	router := NewRouter(platform, sessionMgr, agentMgr)

	err := router.HandleMessage(context.Background(), &weibo.Message{
		ID:        "msg-hermes-404",
		Type:      weibo.MessageTypeText,
		Content:   "hello",
		UserID:    "user-hermes-404",
		UserName:  "test-user",
		Timestamp: 1234567890,
	})

	assert.NoError(t, err)
	assert.Equal(t, []string{"bad-hermes-native", ""}, mockAgent.startSessionIDs)
	assert.Equal(t, []string{"hello"}, secondSession.sentInputs)
	assert.Equal(t, "good-hermes-native", sessionContextString(active, "hermes_session_id"))
	assert.Len(t, platform.streams, 1)
	assert.Equal(t, "fresh reply", platform.streams[0].chunks[0]["content"])
}

func TestHandleMessage_ApprovalReplySurvivesOriginalRequestContextCancellation(t *testing.T) {
	platform := &MockPlatform{}
	sessionMgr := session.NewManager(session.ManagerConfig{Timeout: 300, MaxSize: 10})
	agentMgr := agent.NewManager()

	var firstSessionCtx context.Context
	firstSession := NewMockInteractiveSession()
	firstSession.sendFn = func(input string) {
		firstSession.events <- agent.Event{
			Type:      agent.EventTypeApproval,
			ToolName:  "Read",
			ToolInput: "/tmp/reference.md",
		}
	}
	firstSession.approveErrFn = func(action agent.ApprovalAction) error {
		select {
		case <-firstSessionCtx.Done():
			return errors.New("claude session is not running")
		default:
		}

		firstSession.events <- agent.Event{Type: agent.EventTypeMessage, Content: "已按授权继续执行"}
		firstSession.events <- agent.Event{Type: agent.EventTypeDone}
		return nil
	}

	secondSession := NewMockInteractiveSession()
	secondSession.approveErrFn = func(action agent.ApprovalAction) error {
		return errors.New("no pending claude approval")
	}

	mockAgent := &MockInteractiveAgent{
		name:      "claude-code",
		available: true,
	}
	mockAgent.startFn = func(ctx context.Context, sessionID string) (agent.InteractiveSession, error) {
		switch mockAgent.startCalls {
		case 1:
			firstSessionCtx = ctx
			return firstSession, nil
		case 2:
			return secondSession, nil
		default:
			t.Fatalf("unexpected StartSession call %d", mockAgent.startCalls)
			return nil, nil
		}
	}
	agentMgr.Register(mockAgent)
	agentMgr.SetDefault("claude-code")

	router := NewRouter(platform, sessionMgr, agentMgr)

	firstCtx, firstCancel := context.WithCancel(context.Background())
	err := router.HandleMessage(firstCtx, &weibo.Message{
		ID:        "msg-start",
		Type:      weibo.MessageTypeText,
		Content:   "先执行这个操作",
		UserID:    "user-approval-context",
		UserName:  "tester",
		Timestamp: 1,
	})
	assert.NoError(t, err)

	firstCancel()

	err = router.HandleMessage(context.Background(), &weibo.Message{
		ID:        "msg-approval-reply",
		Type:      weibo.MessageTypeText,
		Content:   "允许",
		UserID:    "user-approval-context",
		UserName:  "tester",
		Timestamp: 2,
	})

	assert.NoError(t, err)
	assert.Equal(t, 1, mockAgent.startCalls)
	assert.Equal(t, []agent.ApprovalAction{agent.ApprovalActionAllow}, firstSession.actions)
	assert.Equal(t, "已按授权继续执行", platform.replies[2]["content"])
	assert.Equal(t, "", platform.replies[3]["content"])
	assert.Equal(t, true, platform.replies[3]["done"])
}

func TestHandleMessage_ByTheWayInjectsIntoExistingInteractiveSession(t *testing.T) {
	platform := &MockPlatform{}
	sessionMgr := session.NewManager(session.ManagerConfig{Timeout: 300, MaxSize: 10})
	agentMgr := agent.NewManager()

	liveSession := NewMockInteractiveSession()
	liveSession.sendFn = func(input string) {
		liveSession.events <- agent.Event{Type: agent.EventTypeMessage, Content: "收到补充: " + input}
		liveSession.events <- agent.Event{Type: agent.EventTypeDone}
	}

	interactiveAgent := &MockInteractiveAgent{
		name:      "codex",
		available: true,
		session:   liveSession,
	}
	agentMgr.Register(interactiveAgent)
	agentMgr.SetDefault("codex")

	router := NewRouter(platform, sessionMgr, agentMgr)

	err := router.HandleMessage(context.Background(), &weibo.Message{
		ID:        "msg-start",
		Type:      weibo.MessageTypeText,
		Content:   "先做第一步",
		UserID:    "user-btw",
		UserName:  "tester",
		Timestamp: 1,
	})
	assert.NoError(t, err)

	err = router.HandleMessage(context.Background(), &weibo.Message{
		ID:        "msg-btw",
		Type:      weibo.MessageTypeText,
		Content:   "/btw 顺便检查一下日志",
		UserID:    "user-btw",
		UserName:  "tester",
		Timestamp: 2,
	})

	assert.NoError(t, err)
	assert.Equal(t, []string{"先做第一步", "顺便检查一下日志"}, liveSession.sentInputs)
	assert.Equal(t, 0, liveSession.interrupts)
	assert.Len(t, platform.replies, 4)
	assert.Equal(t, "收到补充: 顺便检查一下日志", platform.replies[2]["content"])
	assert.Equal(t, "", platform.replies[3]["content"])
	assert.Equal(t, true, platform.replies[3]["done"])
}

func TestHandleMessage_ByTheWayInjectsIntoExistingClaudeInteractiveSession(t *testing.T) {
	platform := &MockPlatform{}
	sessionMgr := session.NewManager(session.ManagerConfig{Timeout: 300, MaxSize: 10})
	agentMgr := agent.NewManager()

	liveSession := NewMockInteractiveSession()
	liveSession.sendFn = func(input string) {
		liveSession.events <- agent.Event{Type: agent.EventTypeMessage, Content: "Claude 收到补充: " + input}
		liveSession.events <- agent.Event{Type: agent.EventTypeDone}
	}

	interactiveAgent := &MockInteractiveAgent{
		name:      "claude-code",
		available: true,
		session:   liveSession,
	}
	agentMgr.Register(interactiveAgent)
	agentMgr.SetDefault("claude-code")

	router := NewRouter(platform, sessionMgr, agentMgr)

	err := router.HandleMessage(context.Background(), &weibo.Message{
		ID:        "msg-start",
		Type:      weibo.MessageTypeText,
		Content:   "先做第一步",
		UserID:    "user-btw-claude",
		UserName:  "tester",
		Timestamp: 1,
	})
	assert.NoError(t, err)

	err = router.HandleMessage(context.Background(), &weibo.Message{
		ID:        "msg-btw",
		Type:      weibo.MessageTypeText,
		Content:   "/btw 顺便检查一下日志",
		UserID:    "user-btw-claude",
		UserName:  "tester",
		Timestamp: 2,
	})

	assert.NoError(t, err)
	assert.Equal(t, []string{"先做第一步", "顺便检查一下日志"}, liveSession.sentInputs)
	assert.Len(t, platform.replies, 4)
	assert.Equal(t, "Claude 收到补充: 顺便检查一下日志", platform.replies[2]["content"])
	assert.Equal(t, "", platform.replies[3]["content"])
	assert.Equal(t, true, platform.replies[3]["done"])
}

func TestHandleMessage_ByTheWayDoesNotConsumeTrailingEventsFromPreviousTurn(t *testing.T) {
	platform := &MockPlatform{}
	sessionMgr := session.NewManager(session.ManagerConfig{Timeout: 300, MaxSize: 10})
	agentMgr := agent.NewManager()

	liveSession := NewMockInteractiveSession()
	liveSession.sendFn = func(input string) {
		switch input {
		case "先做第一步":
			liveSession.events <- agent.Event{Type: agent.EventTypeDelta, Content: "第一段"}
			liveSession.events <- agent.Event{Type: agent.EventTypeDone}
			go func() {
				time.Sleep(20 * time.Millisecond)
				liveSession.events <- agent.Event{Type: agent.EventTypeDelta, Content: "尾巴"}
				liveSession.events <- agent.Event{Type: agent.EventTypeDone}
			}()
		case "顺便检查一下日志":
			liveSession.events <- agent.Event{Type: agent.EventTypeMessage, Content: "收到补充: " + input}
			liveSession.events <- agent.Event{Type: agent.EventTypeDone}
		}
	}

	interactiveAgent := &MockInteractiveAgent{
		name:      "codex",
		available: true,
		session:   liveSession,
	}
	agentMgr.Register(interactiveAgent)
	agentMgr.SetDefault("codex")

	router := NewRouter(platform, sessionMgr, agentMgr)

	err := router.HandleMessage(context.Background(), &weibo.Message{
		ID:        "msg-start",
		Type:      weibo.MessageTypeText,
		Content:   "先做第一步",
		UserID:    "user-btw-tail",
		UserName:  "tester",
		Timestamp: 1,
	})
	assert.NoError(t, err)
	assert.Len(t, platform.replies, 2)
	assert.Equal(t, "第一段尾巴", platform.replies[0]["content"])
	assert.Equal(t, "", platform.replies[1]["content"])
	assert.Equal(t, true, platform.replies[1]["done"])

	err = router.HandleMessage(context.Background(), &weibo.Message{
		ID:        "msg-btw",
		Type:      weibo.MessageTypeText,
		Content:   "/btw 顺便检查一下日志",
		UserID:    "user-btw-tail",
		UserName:  "tester",
		Timestamp: 2,
	})

	assert.NoError(t, err)
	assert.Equal(t, []string{"先做第一步", "顺便检查一下日志"}, liveSession.sentInputs)
	assert.Len(t, platform.replies, 4)
	assert.Equal(t, "收到补充: 顺便检查一下日志", platform.replies[2]["content"])
	assert.Equal(t, "", platform.replies[3]["content"])
	assert.Equal(t, true, platform.replies[3]["done"])
}

func TestHandleMessage_ByTheWayIgnoresBufferedDoneBeforeSendingNewTurn(t *testing.T) {
	platform := &MockPlatform{}
	sessionMgr := session.NewManager(session.ManagerConfig{Timeout: 300, MaxSize: 10})
	agentMgr := agent.NewManager()

	liveSession := NewMockInteractiveSession()
	liveSession.events <- agent.Event{Type: agent.EventTypeDone}
	liveSession.sendFn = func(input string) {
		if input == "顺便检查一下日志" {
			go func() {
				time.Sleep(20 * time.Millisecond)
				liveSession.events <- agent.Event{Type: agent.EventTypeMessage, Content: "收到补充: " + input}
				liveSession.events <- agent.Event{Type: agent.EventTypeDone}
			}()
		}
	}

	interactiveAgent := &MockInteractiveAgent{
		name:      "codex",
		available: true,
		session:   liveSession,
	}
	agentMgr.Register(interactiveAgent)
	agentMgr.SetDefault("codex")

	active := sessionMgr.Create("user-btw-buffered-1", "user-btw-buffered", "codex")
	assert.NotNil(t, active)
	sessionMgr.SetActiveSession("user-btw-buffered", active.ID)

	router := NewRouter(platform, sessionMgr, agentMgr)
	router.liveSessions[active.ID] = &interactiveSessionState{
		agentType: "codex",
		session:   liveSession,
	}

	err := router.HandleMessage(context.Background(), &weibo.Message{
		ID:        "msg-btw",
		Type:      weibo.MessageTypeText,
		Content:   "/btw 顺便检查一下日志",
		UserID:    "user-btw-buffered",
		UserName:  "tester",
		Timestamp: 1,
	})

	assert.NoError(t, err)
	assert.Equal(t, []string{"顺便检查一下日志"}, liveSession.sentInputs)
	assert.Len(t, platform.replies, 2)
	assert.Equal(t, "收到补充: 顺便检查一下日志", platform.replies[0]["content"])
	assert.Equal(t, "", platform.replies[1]["content"])
	assert.Equal(t, true, platform.replies[1]["done"])
}

func TestHandleMessage_IgnoresLateDoneFromPreviousTurnBeforeSendingNewTurn(t *testing.T) {
	platform := &MockPlatform{}
	sessionMgr := session.NewManager(session.ManagerConfig{Timeout: 300, MaxSize: 10})
	agentMgr := agent.NewManager()

	liveSession := NewMockInteractiveSession()
	liveSession.sendFn = func(input string) {
		if input == "继续处理这个请求" {
			go func() {
				time.Sleep(10 * time.Millisecond)
				liveSession.events <- agent.Event{Type: agent.EventTypeMessage, Content: "这是新的回复"}
				liveSession.events <- agent.Event{Type: agent.EventTypeDone}
			}()
		}
	}

	go func() {
		time.Sleep(10 * time.Millisecond)
		liveSession.events <- agent.Event{Type: agent.EventTypeDone}
	}()

	interactiveAgent := &MockInteractiveAgent{
		name:      "codex",
		available: true,
		session:   liveSession,
	}
	agentMgr.Register(interactiveAgent)
	agentMgr.SetDefault("codex")

	active := sessionMgr.Create("user-late-done-1", "user-late-done", "codex")
	assert.NotNil(t, active)
	sessionMgr.SetActiveSession("user-late-done", active.ID)

	router := NewRouter(platform, sessionMgr, agentMgr)
	router.liveSessions[active.ID] = &interactiveSessionState{
		agentType: "codex",
		session:   liveSession,
	}

	err := router.HandleMessage(context.Background(), &weibo.Message{
		ID:        "msg-late-done",
		Type:      weibo.MessageTypeText,
		Content:   "继续处理这个请求",
		UserID:    "user-late-done",
		UserName:  "tester",
		Timestamp: 1,
	})

	assert.NoError(t, err)
	assert.Equal(t, []string{"继续处理这个请求"}, liveSession.sentInputs)
	assert.Len(t, platform.replies, 2)
	assert.Equal(t, "这是新的回复", platform.replies[0]["content"])
	assert.Equal(t, "", platform.replies[1]["content"])
	assert.Equal(t, true, platform.replies[1]["done"])
}

func TestHandleMessage_IgnoresLeadingDoneAndWaitsForSlowNewTurnOutput(t *testing.T) {
	platform := &MockPlatform{}
	sessionMgr := session.NewManager(session.ManagerConfig{Timeout: 300, MaxSize: 10})
	agentMgr := agent.NewManager()

	liveSession := NewMockInteractiveSession()
	liveSession.sendFn = func(input string) {
		if input == "继续处理这个请求" {
			liveSession.events <- agent.Event{Type: agent.EventTypeDone}
			go func() {
				time.Sleep(2 * time.Second)
				liveSession.events <- agent.Event{Type: agent.EventTypeMessage, Content: "慢回复也要收到"}
				liveSession.events <- agent.Event{Type: agent.EventTypeDone}
			}()
		}
	}

	interactiveAgent := &MockInteractiveAgent{
		name:      "codex",
		available: true,
		session:   liveSession,
	}
	agentMgr.Register(interactiveAgent)
	agentMgr.SetDefault("codex")

	active := sessionMgr.Create("user-late-done-slow-1", "user-late-done-slow", "codex")
	assert.NotNil(t, active)
	sessionMgr.SetActiveSession("user-late-done-slow", active.ID)

	router := NewRouter(platform, sessionMgr, agentMgr)
	router.liveSessions[active.ID] = &interactiveSessionState{
		agentType: "codex",
		session:   liveSession,
	}

	err := router.HandleMessage(context.Background(), &weibo.Message{
		ID:        "msg-late-done-slow",
		Type:      weibo.MessageTypeText,
		Content:   "继续处理这个请求",
		UserID:    "user-late-done-slow",
		UserName:  "tester",
		Timestamp: 1,
	})

	assert.NoError(t, err)
	assert.Equal(t, []string{"继续处理这个请求"}, liveSession.sentInputs)
	assert.Len(t, platform.replies, 2)
	assert.Equal(t, "慢回复也要收到", platform.replies[0]["content"])
	assert.Equal(t, "", platform.replies[1]["content"])
	assert.Equal(t, true, platform.replies[1]["done"])
}

func TestHandleMessage_RestartsInteractiveSessionWhenLeadingDoneHasNoFollowingSignal(t *testing.T) {
	platform := &MockPlatform{}
	sessionMgr := session.NewManager(session.ManagerConfig{Timeout: 300, MaxSize: 10})
	agentMgr := agent.NewManager()

	firstSession := NewMockInteractiveSession()
	firstSession.sendFn = func(input string) {
		if input == "继续处理这个请求" {
			firstSession.events <- agent.Event{Type: agent.EventTypeDone}
		}
	}

	secondSession := NewMockInteractiveSession()
	secondSession.sendFn = func(input string) {
		if input == "继续处理这个请求" {
			secondSession.events <- agent.Event{Type: agent.EventTypeMessage, Content: "重连后恢复输出"}
			secondSession.events <- agent.Event{Type: agent.EventTypeDone}
		}
	}

	interactiveAgent := &MockInteractiveAgent{
		name:      "codex",
		available: true,
	}
	interactiveAgent.startFn = func(ctx context.Context, sessionID string) (agent.InteractiveSession, error) {
		return secondSession, nil
	}
	agentMgr.Register(interactiveAgent)
	agentMgr.SetDefault("codex")

	active := sessionMgr.Create("user-leading-done-restart-1", "user-leading-done-restart", "codex")
	assert.NotNil(t, active)
	sessionMgr.SetActiveSession("user-leading-done-restart", active.ID)

	router := NewRouter(platform, sessionMgr, agentMgr)
	router.liveSessions[active.ID] = &interactiveSessionState{
		agentType: "codex",
		session:   firstSession,
	}

	err := router.HandleMessage(context.Background(), &weibo.Message{
		ID:        "msg-leading-done-restart",
		Type:      weibo.MessageTypeText,
		Content:   "继续处理这个请求",
		UserID:    "user-leading-done-restart",
		UserName:  "tester",
		Timestamp: 1,
	})

	assert.NoError(t, err)
	assert.Equal(t, []string{"继续处理这个请求"}, firstSession.sentInputs)
	assert.Equal(t, []string{"继续处理这个请求"}, secondSession.sentInputs)
	assert.Equal(t, 1, interactiveAgent.startCalls)
	assert.Len(t, platform.replies, 2)
	assert.Equal(t, "重连后恢复输出", platform.replies[0]["content"])
	assert.Equal(t, "", platform.replies[1]["content"])
	assert.Equal(t, true, platform.replies[1]["done"])
}

func TestHandleMessage_InteractiveSessionCloseAfterDoneDoesNotFail(t *testing.T) {
	platform := &MockPlatform{}
	sessionMgr := session.NewManager(session.ManagerConfig{Timeout: 300, MaxSize: 10})
	agentMgr := agent.NewManager()

	liveSession := NewMockInteractiveSession()
	liveSession.sendFn = func(input string) {
		liveSession.events <- agent.Event{Type: agent.EventTypeMessage, Content: "这是正常回复"}
		liveSession.events <- agent.Event{Type: agent.EventTypeDone}
		close(liveSession.events)
	}

	interactiveAgent := &MockInteractiveAgent{
		name:      "codex",
		available: true,
		session:   liveSession,
	}
	agentMgr.Register(interactiveAgent)
	agentMgr.SetDefault("codex")

	router := NewRouter(platform, sessionMgr, agentMgr)

	err := router.HandleMessage(context.Background(), &weibo.Message{
		ID:        "msg-close-after-done",
		Type:      weibo.MessageTypeText,
		Content:   "继续执行",
		UserID:    "user-close-after-done",
		UserName:  "tester",
		Timestamp: 1,
	})

	assert.NoError(t, err)
	assert.Len(t, platform.replies, 2)
	assert.Equal(t, "这是正常回复", platform.replies[0]["content"])
	assert.Equal(t, "", platform.replies[1]["content"])
	assert.Equal(t, true, platform.replies[1]["done"])
}

func TestHandleMessage_ByTheWayRequiresExistingInteractiveSession(t *testing.T) {
	platform := &MockPlatform{}
	sessionMgr := session.NewManager(session.ManagerConfig{Timeout: 300, MaxSize: 10})
	agentMgr := agent.NewManager()
	agentMgr.Register(&MockInteractiveAgent{name: "codex", available: true})
	agentMgr.SetDefault("codex")

	router := NewRouter(platform, sessionMgr, agentMgr)

	err := router.HandleMessage(context.Background(), &weibo.Message{
		ID:        "msg-btw",
		Type:      weibo.MessageTypeText,
		Content:   "/btw 补一句",
		UserID:    "user-btw-missing",
		UserName:  "tester",
		Timestamp: 1,
	})

	assert.NoError(t, err)
	assert.Len(t, platform.replies, 2)
	assert.Equal(t, "No active session found. Use /new to create or activate a session first.", platform.replies[0]["content"])
	assert.Equal(t, "", platform.replies[1]["content"])
	assert.Equal(t, true, platform.replies[1]["done"])
}

func TestParseApprovalAction_SupportsMentionSuffix(t *testing.T) {
	action, ok := parseApprovalAction("允许 @bridge")
	assert.True(t, ok)
	assert.Equal(t, agent.ApprovalActionAllow, action)

	action, ok = parseApprovalAction("@bridge 允许所有")
	assert.True(t, ok)
	assert.Equal(t, agent.ApprovalActionAllowAll, action)

	action, ok = parseApprovalAction("取消 @bridge")
	assert.True(t, ok)
	assert.Equal(t, agent.ApprovalActionCancel, action)
}

func TestHandleMessage_SuperModeActsAsAllowAll(t *testing.T) {
	platform := &MockPlatform{}
	sessionMgr := session.NewManager(session.ManagerConfig{Timeout: 300, MaxSize: 10})
	agentMgr := agent.NewManager()

	sess := sessionMgr.Create("session-super-allow", "user-super-allow", "claude")
	assert.NotNil(t, sess)
	assert.True(t, sessionMgr.SetActiveSession("user-super-allow", sess.ID))
	sessionMgr.UpdateSession(sess.ID, superModeContextKey, true)
	sessionMgr.UpdateSession(sess.ID, superAutoApproveContextKey, true)

	liveSession := NewMockInteractiveSession()
	liveSession.sendFn = func(input string) {
		liveSession.events <- agent.Event{
			Type:     agent.EventTypeApproval,
			ToolName: "shell",
		}
	}
	liveSession.approveFn = func(action agent.ApprovalAction) {
		assert.Equal(t, agent.ApprovalActionAllow, action)
		liveSession.events <- agent.Event{Type: agent.EventTypeMessage, Content: "已自动继续执行"}
		liveSession.events <- agent.Event{Type: agent.EventTypeDone}
	}

	interactiveAgent := &MockInteractiveAgent{
		name:      "claude-code",
		available: true,
		session:   liveSession,
	}
	agentMgr.Register(interactiveAgent)
	agentMgr.SetDefault("claude-code")

	router := NewRouter(platform, sessionMgr, agentMgr)

	err := router.HandleMessage(context.Background(), &weibo.Message{
		ID:        "msg-super-allow",
		Type:      weibo.MessageTypeText,
		Content:   "继续",
		UserID:    "user-super-allow",
		UserName:  "tester",
		Timestamp: 1,
	})

	assert.NoError(t, err)
	assert.Equal(t, []agent.ApprovalAction{agent.ApprovalActionAllow}, liveSession.actions)
	assert.Len(t, platform.replies, 2)
	assert.Equal(t, "已自动继续执行", platform.replies[0]["content"])
}

func TestHandleMessage_SuperOnDuringPendingApproval_DoesNotSwallowNextInput(t *testing.T) {
	platform := &MockPlatform{}
	sessionMgr := session.NewManager(session.ManagerConfig{Timeout: 300, MaxSize: 10})
	agentMgr := agent.NewManager()

	sess := sessionMgr.Create("session-super-pending", "user-super-pending", "claude")
	assert.NotNil(t, sess)
	assert.True(t, sessionMgr.SetActiveSession("user-super-pending", sess.ID))

	liveSession := NewMockInteractiveSession()
	liveSession.sendFn = func(input string) {
		switch strings.TrimSpace(input) {
		case "需要审批":
			liveSession.events <- agent.Event{
				Type:     agent.EventTypeApproval,
				ToolName: "shell",
			}
		case "新的问题":
			// 模拟旧 turn 的延迟 done 在新 turn 开始后先到达。
			liveSession.events <- agent.Event{Type: agent.EventTypeDone}
			go func() {
				time.Sleep(120 * time.Millisecond)
				liveSession.events <- agent.Event{Type: agent.EventTypeMessage, Content: "新回合已执行"}
				liveSession.events <- agent.Event{Type: agent.EventTypeDone}
			}()
		}
	}
	liveSession.approveFn = func(action agent.ApprovalAction) {
		assert.Equal(t, agent.ApprovalActionAllow, action)
		liveSession.events <- agent.Event{Type: agent.EventTypeDone}
		// 模拟交互会话尾部延迟 done（旧 turn 尾事件晚到）。
		go func() {
			time.Sleep(250 * time.Millisecond)
			liveSession.events <- agent.Event{Type: agent.EventTypeDone}
		}()
	}

	interactiveAgent := &MockInteractiveAgent{
		name:      "claude-code",
		available: true,
		session:   liveSession,
	}
	agentMgr.Register(interactiveAgent)
	agentMgr.SetDefault("claude-code")

	router := NewRouter(platform, sessionMgr, agentMgr)

	err := router.HandleMessage(context.Background(), &weibo.Message{
		ID:        "msg-super-pending-1",
		Type:      weibo.MessageTypeText,
		Content:   "需要审批",
		UserID:    "user-super-pending",
		UserName:  "tester",
		Timestamp: 1,
	})
	assert.NoError(t, err)

	err = router.HandleMessage(context.Background(), &weibo.Message{
		ID:        "msg-super-pending-super-on",
		Type:      weibo.MessageTypeText,
		Content:   "/super on",
		UserID:    "user-super-pending",
		UserName:  "tester",
		Timestamp: 2,
	})
	assert.NoError(t, err)

	err = router.HandleMessage(context.Background(), &weibo.Message{
		ID:        "msg-super-pending-2",
		Type:      weibo.MessageTypeText,
		Content:   "新的问题",
		UserID:    "user-super-pending",
		UserName:  "tester",
		Timestamp: 3,
	})
	assert.NoError(t, err)

	assert.Equal(t, []agent.ApprovalAction{agent.ApprovalActionAllow}, liveSession.actions)
	assert.Equal(t, []string{"需要审批", "新的问题"}, liveSession.sentInputs)

	found := false
	for _, reply := range platform.replies {
		if strings.Contains(reply["content"].(string), "新回合已执行") {
			found = true
			break
		}
	}
	assert.True(t, found, "expected new turn output after auto-approving pending request")
}

func TestHandleMessage_BusySuperSlashCommandDoesNotBlock(t *testing.T) {
	platform := newSingleStreamGatePlatform()
	sessionMgr := session.NewManager(session.ManagerConfig{Timeout: 300, MaxSize: 10})
	agentMgr := agent.NewManager()

	sess := sessionMgr.Create("session-super-busy", "user-super-busy", "claude")
	assert.NotNil(t, sess)
	assert.True(t, sessionMgr.SetActiveSession("user-super-busy", sess.ID))
	sessionMgr.UpdateSession(sess.ID, superModeContextKey, true)
	sessionMgr.UpdateSession(sess.ID, superAutoApproveContextKey, true)

	releaseMain := make(chan struct{})
	mainAgent := &MockAgent{
		name:      "claude-code",
		available: true,
		streamFn: func(ctx context.Context, sessionID string, input string) (<-chan agent.Event, error) {
			stream := make(chan agent.Event, 2)
			go func() {
				defer close(stream)
				stream <- agent.Event{Type: agent.EventTypeDelta, Content: "主流程处理中。"}
				<-releaseMain
				stream <- agent.Event{Type: agent.EventTypeDone}
			}()
			return stream, nil
		},
	}

	agentMgr.Register(mainAgent)
	agentMgr.SetDefault("claude-code")

	router := NewRouter(platform, sessionMgr, agentMgr)

	mainDone := make(chan error, 1)
	go func() {
		mainDone <- router.HandleMessage(context.Background(), &weibo.Message{
			ID:        "msg-super-busy-main",
			Type:      weibo.MessageTypeText,
			Content:   "先执行主流程",
			UserID:    "user-super-busy",
			UserName:  "tester",
			Timestamp: 1,
		})
	}()

	select {
	case <-platform.opened:
	case <-time.After(time.Second):
		t.Fatal("main flow did not open reply stream")
	}

	cmdDone := make(chan error, 1)
	go func() {
		cmdDone <- router.HandleMessage(context.Background(), &weibo.Message{
			ID:        "msg-super-busy-off",
			Type:      weibo.MessageTypeText,
			Content:   "/super off",
			UserID:    "user-super-busy",
			UserName:  "tester",
			Timestamp: 2,
		})
	}()

	select {
	case err := <-cmdDone:
		assert.NoError(t, err)
	case <-time.After(400 * time.Millisecond):
		t.Fatal("/super off should return immediately even when another stream is in-flight")
	}

	assert.Equal(t, 1, platform.OpenCount(), "slash command should not open a second reply stream")
	assert.Eventually(t, func() bool {
		for _, text := range platform.Replies() {
			if strings.Contains(text, "Super mode disabled") {
				return true
			}
		}
		return false
	}, time.Second, 20*time.Millisecond)

	close(releaseMain)
	select {
	case err := <-mainDone:
		assert.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("main flow did not finish")
	}
}

func TestHandleMessage_SuperModePeerApprovalShowsNotice(t *testing.T) {
	t.Parallel()

	platform := newSingleStreamGatePlatform()
	sessionMgr := session.NewManager(session.ManagerConfig{Timeout: 300, MaxSize: 10})
	agentMgr := agent.NewManager()

	sess := sessionMgr.Create("session-super-peer", "user-super-peer", "claude")
	assert.NotNil(t, sess)
	assert.True(t, sessionMgr.SetActiveSession("user-super-peer", sess.ID))
	sessionMgr.UpdateSession(sess.ID, superModeContextKey, true)
	sessionMgr.UpdateSession(sess.ID, superAutoApproveContextKey, true)

	mainAgent := &MockAgent{
		name:      "claude-code",
		available: true,
		streamFn: func(ctx context.Context, sessionID string, input string) (<-chan agent.Event, error) {
			stream := make(chan agent.Event, 2)
			stream <- agent.Event{Type: agent.EventTypeMessage, Content: "主代理输出"}
			stream <- agent.Event{Type: agent.EventTypeDone}
			close(stream)
			return stream, nil
		},
	}

	peerSession := NewMockInteractiveSession()
	peerSession.sendFn = func(input string) {
		peerSession.events <- agent.Event{
			Type:     agent.EventTypeApproval,
			ToolName: "shell",
		}
	}
	peerSession.approveFn = func(action agent.ApprovalAction) {
		assert.Equal(t, agent.ApprovalActionAllow, action)
		peerSession.events <- agent.Event{Type: agent.EventTypeMessage, Content: "对侧复盘结论"}
		peerSession.events <- agent.Event{Type: agent.EventTypeDone}
	}
	peerAgent := &MockInteractiveAgent{
		name:      "codex",
		available: true,
		session:   peerSession,
	}

	agentMgr.Register(mainAgent)
	agentMgr.Register(peerAgent)
	agentMgr.SetDefault("claude-code")

	router := NewRouter(platform, sessionMgr, agentMgr)

	err := router.HandleMessage(context.Background(), &weibo.Message{
		ID:        "msg-super-peer",
		Type:      weibo.MessageTypeText,
		Content:   "请优化",
		UserID:    "user-super-peer",
		UserName:  "tester",
		Timestamp: 1,
	})
	assert.NoError(t, err)

	assert.Eventually(t, func() bool {
		for _, reply := range platform.Replies() {
			if strings.Contains(reply, superAutoApprovalNotice) {
				return true
			}
		}
		return false
	}, time.Second, 20*time.Millisecond)

	assert.Eventually(t, func() bool {
		updated, ok := sessionMgr.Get(sess.ID)
		if !ok || updated == nil {
			return false
		}
		feedback, _ := updated.ContextString(superFeedbackForClaudeKey)
		ready, _ := updated.ContextBool(superFeedbackReadyForClaudeKey)
		return feedback == "对侧复盘结论" && ready
	}, time.Second, 20*time.Millisecond)
}

func TestHandleMessage_SuperModeInjectsPeerFeedbackNextTurn(t *testing.T) {
	t.Parallel()

	platform := &MockPlatform{}
	sessionMgr := session.NewManager(session.ManagerConfig{Timeout: 300, MaxSize: 10})
	agentMgr := agent.NewManager()

	sess := sessionMgr.Create("session-super-inject", "user-super-inject", "claude")
	assert.NotNil(t, sess)
	assert.True(t, sessionMgr.SetActiveSession("user-super-inject", sess.ID))
	sessionMgr.UpdateSession(sess.ID, superModeContextKey, true)
	sessionMgr.UpdateSession(sess.ID, superAutoApproveContextKey, true)

	var mainInputs []string
	mainAgent := &MockAgent{
		name:      "claude-code",
		available: true,
		streamFn: func(ctx context.Context, sessionID string, input string) (<-chan agent.Event, error) {
			mainInputs = append(mainInputs, input)
			stream := make(chan agent.Event, 2)
			stream <- agent.Event{Type: agent.EventTypeMessage, Content: "主输出"}
			stream <- agent.Event{Type: agent.EventTypeDone}
			close(stream)
			return stream, nil
		},
	}

	peerCall := 0
	peerAgent := &MockAgent{
		name:      "codex",
		available: true,
		streamFn: func(ctx context.Context, sessionID string, input string) (<-chan agent.Event, error) {
			peerCall++
			stream := make(chan agent.Event, 2)
			if peerCall == 1 {
				stream <- agent.Event{Type: agent.EventTypeMessage, Content: "peer-feedback-1"}
			} else {
				stream <- agent.Event{Type: agent.EventTypeMessage, Content: "peer-feedback-2"}
			}
			stream <- agent.Event{Type: agent.EventTypeDone}
			close(stream)
			return stream, nil
		},
	}

	agentMgr.Register(mainAgent)
	agentMgr.Register(peerAgent)
	agentMgr.SetDefault("claude-code")

	router := NewRouter(platform, sessionMgr, agentMgr)

	err := router.HandleMessage(context.Background(), &weibo.Message{
		ID:        "msg-super-inject-1",
		Type:      weibo.MessageTypeText,
		Content:   "第一问",
		UserID:    "user-super-inject",
		UserName:  "tester",
		Timestamp: 1,
	})
	assert.NoError(t, err)
	assert.Len(t, mainInputs, 1)
	assert.Equal(t, "第一问", mainInputs[0])
	assert.Eventually(t, func() bool {
		updated, ok := sessionMgr.Get(sess.ID)
		if !ok || updated == nil {
			return false
		}
		feedback, _ := updated.ContextString(superFeedbackForClaudeKey)
		ready, _ := updated.ContextBool(superFeedbackReadyForClaudeKey)
		return feedback == "peer-feedback-1" && ready
	}, time.Second, 20*time.Millisecond)

	err = router.HandleMessage(context.Background(), &weibo.Message{
		ID:        "msg-super-inject-2",
		Type:      weibo.MessageTypeText,
		Content:   "第二问",
		UserID:    "user-super-inject",
		UserName:  "tester",
		Timestamp: 2,
	})
	assert.NoError(t, err)
	assert.Len(t, mainInputs, 2)
	assert.Contains(t, mainInputs[1], "peer-feedback-1")
	assert.Contains(t, mainInputs[1], "Super 对侧已审批结论")
}

func TestRouter_CustomizeProcessingAck_AppendsSuperNotice(t *testing.T) {
	sessionMgr := session.NewManager(session.ManagerConfig{Timeout: 300, MaxSize: 10})
	agentMgr := agent.NewManager()
	agentMgr.Register(&MockAgent{name: "claude-code", available: true})
	agentMgr.SetDefault("claude-code")

	sess := sessionMgr.Create("session-ack-super", "user-ack-super", "claude")
	assert.NotNil(t, sess)
	assert.True(t, sessionMgr.SetActiveSession("user-ack-super", sess.ID))
	sessionMgr.UpdateSession(sess.ID, superModeContextKey, true)
	setSuperFeedbackForAgent(sessionMgr, sess.ID, "claude", "approved-feedback")

	router := NewRouter(&MockPlatform{}, sessionMgr, agentMgr)
	got := router.CustomizeProcessingAck(&weibo.Message{
		ID:      "msg-ack",
		Type:    weibo.MessageTypeText,
		Content: "hello",
		UserID:  "user-ack-super",
	}, "已收到消息，正在处理中，请稍候。")

	assert.Contains(t, got, "Super：本轮将注入对侧已审批结论")
}

func TestHandleMessage_SuperPeerInteractiveUsesDeadlineContext(t *testing.T) {
	platform := &MockPlatform{}
	sessionMgr := session.NewManager(session.ManagerConfig{Timeout: 300, MaxSize: 10})
	agentMgr := agent.NewManager()

	sess := sessionMgr.Create("session-super-deadline", "user-super-deadline", "claude")
	assert.NotNil(t, sess)
	assert.True(t, sessionMgr.SetActiveSession("user-super-deadline", sess.ID))
	sessionMgr.UpdateSession(sess.ID, superModeContextKey, true)
	sessionMgr.UpdateSession(sess.ID, superAutoApproveContextKey, true)

	mainAgent := &MockAgent{
		name:      "claude-code",
		available: true,
		streamFn: func(ctx context.Context, sessionID string, input string) (<-chan agent.Event, error) {
			stream := make(chan agent.Event, 2)
			stream <- agent.Event{Type: agent.EventTypeMessage, Content: "主代理输出"}
			stream <- agent.Event{Type: agent.EventTypeDone}
			close(stream)
			return stream, nil
		},
	}

	peerStarted := make(chan bool, 1)
	peerAgent := &MockInteractiveAgent{
		name:      "codex",
		available: true,
		startFn: func(ctx context.Context, sessionID string) (agent.InteractiveSession, error) {
			_, hasDeadline := ctx.Deadline()
			peerStarted <- hasDeadline
			return NewMockInteractiveSession(), nil
		},
	}

	agentMgr.Register(mainAgent)
	agentMgr.Register(peerAgent)
	agentMgr.SetDefault("claude-code")

	router := NewRouter(platform, sessionMgr, agentMgr)

	err := router.HandleMessage(context.Background(), &weibo.Message{
		ID:        "msg-super-deadline",
		Type:      weibo.MessageTypeText,
		Content:   "请优化",
		UserID:    "user-super-deadline",
		UserName:  "tester",
		Timestamp: 1,
	})
	assert.NoError(t, err)

	select {
	case hasDeadline := <-peerStarted:
		assert.True(t, hasDeadline)
	case <-time.After(time.Second):
		t.Fatal("peer review did not start")
	}
}

func TestHandleMessage_SuperFeedbackNotConsumedOnMainFailure(t *testing.T) {
	platform := &MockPlatform{}
	sessionMgr := session.NewManager(session.ManagerConfig{Timeout: 300, MaxSize: 10})
	agentMgr := agent.NewManager()

	sess := sessionMgr.Create("session-super-main-fail", "user-super-main-fail", "claude")
	assert.NotNil(t, sess)
	assert.True(t, sessionMgr.SetActiveSession("user-super-main-fail", sess.ID))
	sessionMgr.UpdateSession(sess.ID, superModeContextKey, true)
	setSuperFeedbackForAgent(sessionMgr, sess.ID, "claude", "must-keep-feedback")

	mainAgent := &MockAgent{
		name:      "claude-code",
		available: true,
		streamFn: func(ctx context.Context, sessionID string, input string) (<-chan agent.Event, error) {
			stream := make(chan agent.Event, 2)
			stream <- agent.Event{Type: agent.EventTypeError, Error: "main failed"}
			stream <- agent.Event{Type: agent.EventTypeDone}
			close(stream)
			return stream, nil
		},
	}
	agentMgr.Register(mainAgent)
	agentMgr.SetDefault("claude-code")

	router := NewRouter(platform, sessionMgr, agentMgr)
	err := router.HandleMessage(context.Background(), &weibo.Message{
		ID:        "msg-super-main-fail",
		Type:      weibo.MessageTypeText,
		Content:   "触发失败",
		UserID:    "user-super-main-fail",
		UserName:  "tester",
		Timestamp: 1,
	})
	assert.Error(t, err)

	updated, ok := sessionMgr.Get(sess.ID)
	assert.True(t, ok)
	feedback, _ := updated.ContextString(superFeedbackForClaudeKey)
	ready, _ := updated.ContextBool(superFeedbackReadyForClaudeKey)
	assert.Equal(t, "must-keep-feedback", feedback)
	assert.True(t, ready)
}

func TestHandleMessage_SuperPeerReviewRunsAsync(t *testing.T) {
	platform := &MockPlatform{}
	sessionMgr := session.NewManager(session.ManagerConfig{Timeout: 300, MaxSize: 10})
	agentMgr := agent.NewManager()

	sess := sessionMgr.Create("session-super-async", "user-super-async", "claude")
	assert.NotNil(t, sess)
	assert.True(t, sessionMgr.SetActiveSession("user-super-async", sess.ID))
	sessionMgr.UpdateSession(sess.ID, superModeContextKey, true)
	sessionMgr.UpdateSession(sess.ID, superAutoApproveContextKey, true)

	mainAgent := &MockAgent{
		name:      "claude-code",
		available: true,
		streamFn: func(ctx context.Context, sessionID string, input string) (<-chan agent.Event, error) {
			stream := make(chan agent.Event, 2)
			stream <- agent.Event{Type: agent.EventTypeMessage, Content: "主代理输出"}
			stream <- agent.Event{Type: agent.EventTypeDone}
			close(stream)
			return stream, nil
		},
	}
	peerAgent := &MockInteractiveAgent{
		name:      "codex",
		available: true,
		startFn: func(ctx context.Context, sessionID string) (agent.InteractiveSession, error) {
			time.Sleep(250 * time.Millisecond)
			peerSession := NewMockInteractiveSession()
			peerSession.sendFn = func(input string) {
				peerSession.events <- agent.Event{Type: agent.EventTypeMessage, Content: "peer"}
				peerSession.events <- agent.Event{Type: agent.EventTypeDone}
			}
			return peerSession, nil
		},
	}
	agentMgr.Register(mainAgent)
	agentMgr.Register(peerAgent)
	agentMgr.SetDefault("claude-code")

	router := NewRouter(platform, sessionMgr, agentMgr)
	start := time.Now()
	err := router.HandleMessage(context.Background(), &weibo.Message{
		ID:        "msg-super-async",
		Type:      weibo.MessageTypeText,
		Content:   "请优化",
		UserID:    "user-super-async",
		UserName:  "tester",
		Timestamp: 1,
	})
	elapsed := time.Since(start)
	assert.NoError(t, err)
	assert.Less(t, elapsed, 200*time.Millisecond)
}

func TestHandleMessage_SuperPeerReviewIgnoresStaleCanceledReview(t *testing.T) {
	platform := &MockPlatform{}
	sessionMgr := session.NewManager(session.ManagerConfig{Timeout: 300, MaxSize: 10})
	agentMgr := agent.NewManager()

	sess := sessionMgr.Create("session-super-stale", "user-super-stale", "claude")
	assert.NotNil(t, sess)
	assert.True(t, sessionMgr.SetActiveSession("user-super-stale", sess.ID))
	sessionMgr.UpdateSession(sess.ID, superModeContextKey, true)
	sessionMgr.UpdateSession(sess.ID, superAutoApproveContextKey, true)

	mainAgent := &MockAgent{
		name:      "claude-code",
		available: true,
		streamFn: func(ctx context.Context, sessionID string, input string) (<-chan agent.Event, error) {
			stream := make(chan agent.Event, 2)
			stream <- agent.Event{Type: agent.EventTypeMessage, Content: "主代理输出"}
			stream <- agent.Event{Type: agent.EventTypeDone}
			close(stream)
			return stream, nil
		},
	}

	firstReviewStarted := make(chan struct{}, 1)
	releaseFirstReview := make(chan struct{})
	firstReviewClosed := make(chan struct{})
	var reviewMu sync.Mutex
	reviewCalls := 0
	peerAgent := &MockAgent{
		name:      "codex",
		available: true,
		streamFn: func(ctx context.Context, sessionID string, input string) (<-chan agent.Event, error) {
			reviewMu.Lock()
			reviewCalls++
			call := reviewCalls
			reviewMu.Unlock()

			stream := make(chan agent.Event, 2)
			if call == 1 {
				firstReviewStarted <- struct{}{}
				go func() {
					defer close(firstReviewClosed)
					<-releaseFirstReview
					stream <- agent.Event{Type: agent.EventTypeMessage, Content: "old-feedback"}
					stream <- agent.Event{Type: agent.EventTypeDone}
					close(stream)
				}()
				return stream, nil
			}

			stream <- agent.Event{Type: agent.EventTypeMessage, Content: "new-feedback"}
			stream <- agent.Event{Type: agent.EventTypeDone}
			close(stream)
			return stream, nil
		},
	}
	agentMgr.Register(mainAgent)
	agentMgr.Register(peerAgent)
	agentMgr.SetDefault("claude-code")

	router := NewRouter(platform, sessionMgr, agentMgr)
	err := router.HandleMessage(context.Background(), &weibo.Message{
		ID:        "msg-super-stale-1",
		Type:      weibo.MessageTypeText,
		Content:   "第一轮",
		UserID:    "user-super-stale",
		UserName:  "tester",
		Timestamp: 1,
	})
	assert.NoError(t, err)

	select {
	case <-firstReviewStarted:
	case <-time.After(time.Second):
		t.Fatal("first peer review did not start")
	}

	err = router.HandleMessage(context.Background(), &weibo.Message{
		ID:        "msg-super-stale-2",
		Type:      weibo.MessageTypeText,
		Content:   "第二轮",
		UserID:    "user-super-stale",
		UserName:  "tester",
		Timestamp: 2,
	})
	assert.NoError(t, err)

	assert.Eventually(t, func() bool {
		updated, _ := sessionMgr.Get(sess.ID)
		feedback, _ := updated.ContextString(superFeedbackForClaudeKey)
		return feedback == "new-feedback"
	}, time.Second, 20*time.Millisecond)

	close(releaseFirstReview)
	select {
	case <-firstReviewClosed:
	case <-time.After(time.Second):
		t.Fatal("first peer review did not close")
	}
	time.Sleep(100 * time.Millisecond)

	updated, _ := sessionMgr.Get(sess.ID)
	feedback, _ := updated.ContextString(superFeedbackForClaudeKey)
	assert.Equal(t, "new-feedback", feedback)
}
