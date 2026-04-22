package router

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/yourusername/weibo-ai-bridge/agent"
	"github.com/yourusername/weibo-ai-bridge/platform/weibo"
	"github.com/yourusername/weibo-ai-bridge/session"
)

const (
	streamReplyChunkSize = 1000
)

// MessageType 消息类型
type MessageType string

const (
	TypeText     MessageType = "text"
	TypeImage    MessageType = "image"
	TypeVoice    MessageType = "voice"
	TypeLocation MessageType = "location"
)

// Message 消息结构
type Message struct {
	ID        string
	Type      MessageType
	Content   string
	UserID    string
	SessionID string
	Metadata  map[string]interface{}
}

// Response 响应结构
type Response struct {
	Success bool
	Content string
	Error   error
}

// Handler 消息处理器接口
type Handler interface {
	Handle(msg *Message) (*Response, error)
}

// Router 消息路由器
type Router struct {
	handlers       map[MessageType]Handler
	defaultHandler Handler
	platform       PlatformInterface
	sessionMgr     *session.Manager
	agentMgr       *agent.Manager
	commandHandler *CommandHandler
}

// PlatformInterface 平台接口
type PlatformInterface interface {
	Reply(ctx context.Context, messageID string, content string) error
}

type streamReplyWriter interface {
	SendChunk(ctx context.Context, content string, done bool) error
}

type streamingPlatformInterface interface {
	OpenReplyStream(ctx context.Context, userID string) (weibo.ChunkSender, error)
}

// NewRouter 创建路由器
func NewRouter(platform PlatformInterface, sessionMgr *session.Manager, agentMgr *agent.Manager) *Router {
	router := &Router{
		handlers:   make(map[MessageType]Handler),
		platform:   platform,
		sessionMgr: sessionMgr,
		agentMgr:   agentMgr,
	}

	if sessionMgr != nil && agentMgr != nil {
		router.commandHandler = NewCommandHandler(sessionMgr, agentMgr)
		router.Register(TypeText, router)
		router.Register(TypeImage, router)
		router.Register(TypeVoice, router)
		router.Register(TypeLocation, router)
	}

	return router
}

// Register 注册处理器
func (r *Router) Register(msgType MessageType, handler Handler) {
	r.handlers[msgType] = handler
}

// SetDefault 设置默认处理器
func (r *Router) SetDefault(handler Handler) {
	r.defaultHandler = handler
}

// Handle 处理消息（实现 Handler 接口）
func (r *Router) Handle(msg *Message) (*Response, error) {
	if msg == nil {
		return nil, errors.New("message cannot be nil")
	}

	content := strings.TrimSpace(msg.Content)

	if strings.HasPrefix(content, "/") && r.commandHandler != nil {
		return r.commandHandler.Handle(msg)
	}

	return r.handleAIMessage(context.Background(), msg)
}

// Route 路由消息
func (r *Router) Route(msg *Message) (*Response, error) {
	if msg == nil {
		return nil, errors.New("message cannot be nil")
	}

	handler, exists := r.handlers[msg.Type]
	if !exists {
		return nil, errors.New("no handler for message type: " + string(msg.Type))
	}

	return handler.Handle(msg)
}

// StreamMessage 处理消息并返回结构化事件流。
func (r *Router) StreamMessage(ctx context.Context, msg *weibo.Message) (<-chan agent.Event, error) {
	if msg == nil {
		return nil, errors.New("message cannot be nil")
	}

	return r.Stream(ctx, r.toRouterMessage(msg))
}

// Stream 处理路由层消息并返回结构化事件流。
func (r *Router) Stream(ctx context.Context, msg *Message) (<-chan agent.Event, error) {
	if msg == nil {
		return nil, errors.New("message cannot be nil")
	}

	return r.streamRouterMessage(ctx, msg)
}

// HandleMessage 处理消息（主入口）
func (r *Router) HandleMessage(ctx context.Context, msg *weibo.Message) error {
	if msg == nil {
		return errors.New("message cannot be nil")
	}

	stream, err := r.StreamMessage(ctx, msg)
	if err != nil {
		return err
	}

	return r.forwardStreamToPlatform(ctx, msg.UserID, stream)
}

func (r *Router) toRouterMessage(msg *weibo.Message) *Message {
	sessionID := msg.UserID
	if r.sessionMgr != nil {
		if activeSessionID := r.sessionMgr.GetActiveSessionID(msg.UserID); activeSessionID != "" {
			sessionID = activeSessionID
		}
	}

	return &Message{
		ID:        msg.ID,
		Type:      mapWeiboMessageType(msg.Type),
		Content:   msg.Content,
		UserID:    msg.UserID,
		SessionID: sessionID,
		Metadata: map[string]interface{}{
			"user_name": msg.UserName,
			"timestamp": msg.Timestamp,
			"msg_type":  string(msg.Type),
		},
	}
}

func (r *Router) streamRouterMessage(ctx context.Context, msg *Message) (<-chan agent.Event, error) {
	if msg == nil {
		return nil, errors.New("message cannot be nil")
	}

	events := make(chan agent.Event, 32)

	go func() {
		defer close(events)

		content := strings.TrimSpace(msg.Content)
		if strings.HasPrefix(content, "/") && r.commandHandler != nil {
			r.emitCommandEvents(events, msg)
			return
		}

		if err := r.streamAIMessage(ctx, msg, events); err != nil {
			events <- agent.Event{Type: agent.EventTypeError, Error: err.Error()}
		}

		events <- agent.Event{Type: agent.EventTypeDone}
	}()

	return events, nil
}

func (r *Router) emitCommandEvents(events chan<- agent.Event, msg *Message) {
	resp, err := r.commandHandler.Handle(msg)
	if err != nil {
		events <- agent.Event{Type: agent.EventTypeError, Error: err.Error()}
		events <- agent.Event{Type: agent.EventTypeDone}
		return
	}

	if resp == nil {
		events <- agent.Event{Type: agent.EventTypeError, Error: "response is nil"}
		events <- agent.Event{Type: agent.EventTypeDone}
		return
	}

	if resp.Content != "" {
		events <- agent.Event{Type: agent.EventTypeMessage, Content: resp.Content}
	}
	if !resp.Success && resp.Error != nil {
		events <- agent.Event{Type: agent.EventTypeError, Error: resp.Error.Error()}
	}
	events <- agent.Event{Type: agent.EventTypeDone}
}

func (r *Router) forwardStreamToPlatform(ctx context.Context, userID string, stream <-chan agent.Event) error {
	if r.platform == nil {
		return errors.New("platform is not set")
	}

	writer, err := r.openStreamWriter(ctx, userID)
	if err != nil {
		return err
	}
	sender := newStreamReplySender(writer, r.splitMessage)

	var streamErr error

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-stream:
			if !ok {
				if err := sender.Settle(ctx); err != nil {
					return err
				}
				return streamErr
			}

			switch event.Type {
			case agent.EventTypeDelta:
				if err := sender.PushPartialSnapshot(ctx, event.Content); err != nil {
					return err
				}
			case agent.EventTypeMessage:
				if err := sender.PushDeliverText(ctx, event.Content, true); err != nil {
					return err
				}
			case agent.EventTypeError:
				if err := sender.Settle(ctx); err != nil {
					return err
				}
				if strings.TrimSpace(event.Error) != "" {
					if err := r.sendReply(ctx, userID, "AI execution failed: "+event.Error); err != nil {
						return err
					}
					streamErr = errors.New(event.Error)
				}
			case agent.EventTypeDone:
				if err := sender.Settle(ctx); err != nil {
					return err
				}
			}
		}
	}
}

func (r *Router) openStreamWriter(ctx context.Context, userID string) (streamReplyWriter, error) {
	if streamer, ok := r.platform.(streamingPlatformInterface); ok {
		return streamer.OpenReplyStream(ctx, userID)
	}

	return &legacyStreamReplyWriter{
		send: func(content string) error {
			return r.sendReply(ctx, userID, content)
		},
	}, nil
}

// sendReply 发送回复（支持分块）
func (r *Router) sendReply(ctx context.Context, userID string, content string) error {
	if r.platform == nil {
		return errors.New("platform is not set")
	}

	chunks := r.splitMessage(content, 1000)

	for i, chunk := range chunks {
		if err := r.platform.Reply(ctx, userID, chunk); err != nil {
			return fmt.Errorf("send reply chunk %d failed: %w", i, err)
		}
	}

	return nil
}

// splitMessage 分割消息为多个块
func (r *Router) splitMessage(content string, maxSize int) []string {
	if len(content) <= maxSize {
		return []string{content}
	}

	var chunks []string
	var buffer strings.Builder

	lines := strings.Split(content, "\n")

	for _, line := range lines {
		if len(line) > maxSize {
			if buffer.Len() > 0 {
				chunks = append(chunks, buffer.String())
				buffer.Reset()
			}

			for len(line) > maxSize {
				chunks = append(chunks, line[:maxSize])
				line = line[maxSize:]
			}
			if len(line) > 0 {
				buffer.WriteString(line)
				buffer.WriteString("\n")
			}
			continue
		}

		if buffer.Len()+len(line)+1 > maxSize {
			chunks = append(chunks, buffer.String())
			buffer.Reset()
		}

		buffer.WriteString(line)
		buffer.WriteString("\n")
	}

	if buffer.Len() > 0 {
		chunks = append(chunks, strings.TrimSuffix(buffer.String(), "\n"))
	}

	return chunks
}

// GetHandler 获取处理器
func (r *Router) GetHandler(msgType MessageType) (Handler, bool) {
	handler, exists := r.handlers[msgType]
	return handler, exists
}

func mapWeiboMessageType(msgType weibo.MessageType) MessageType {
	switch msgType {
	case weibo.MessageTypeImage:
		return TypeImage
	default:
		return TypeText
	}
}

// handleAIMessage 处理 AI 消息（非命令消息）
func (r *Router) handleAIMessage(ctx context.Context, msg *Message) (*Response, error) {
	if r.agentMgr == nil {
		return &Response{
			Success: false,
			Content: "Agent manager is not available",
		}, nil
	}

	if r.sessionMgr == nil {
		return &Response{
			Success: false,
			Content: "Session manager is not available",
		}, nil
	}

	session, sessionKey, agentSessionID, currentAgent, err := r.resolveAgentExecution(msg)
	if err != nil {
		return &Response{
			Success: false,
			Content: err.Error(),
		}, nil
	}

	response, err := currentAgent.Execute(ctx, agentSessionID, msg.Content)
	if err != nil {
		return &Response{
			Success: false,
			Content: fmt.Sprintf("AI execution failed: %v", err),
		}, nil
	}

	if sessionKey != "" {
		if newSessionID := extractSessionID(response); newSessionID != "" {
			if session.Context == nil {
				session.Context = make(map[string]interface{})
			}
			session.Context[sessionKey] = newSessionID
			response = removeSessionIDMarker(response)
			r.sessionMgr.UpdateSession(session.ID, sessionKey, newSessionID)
		}
	}

	return &Response{
		Success: true,
		Content: response,
	}, nil
}

func (r *Router) streamAIMessage(ctx context.Context, msg *Message, events chan<- agent.Event) error {
	if r.agentMgr == nil {
		return errors.New("Agent manager is not available")
	}
	if r.sessionMgr == nil {
		return errors.New("Session manager is not available")
	}

	session, sessionKey, agentSessionID, currentAgent, err := r.resolveAgentExecution(msg)
	if err != nil {
		return err
	}

	stream, err := currentAgent.ExecuteStream(ctx, agentSessionID, msg.Content)
	if err != nil {
		return err
	}

	for event := range stream {
		if event.Type == agent.EventTypeDone {
			continue
		}

		if event.Type == agent.EventTypeSession && sessionKey != "" && strings.TrimSpace(event.SessionID) != "" {
			if session.Context == nil {
				session.Context = make(map[string]interface{})
			}
			session.Context[sessionKey] = event.SessionID
			r.sessionMgr.UpdateSession(session.ID, sessionKey, event.SessionID)
		}

		events <- event
	}

	return nil
}

func (r *Router) resolveAgentExecution(msg *Message) (*session.Session, string, string, agent.Agent, error) {
	var currentSession *session.Session
	var agentType string

	defaultAgent := r.agentMgr.GetDefaultAgent()
	if defaultAgent == nil {
		return nil, "", "", nil, errors.New("No default agent configured")
	}
	agentType = mapAgentName(defaultAgent.Name())

	if strings.TrimSpace(msg.SessionID) != "" {
		currentSession = r.sessionMgr.GetOrCreateSession(msg.SessionID, msg.UserID, agentType)
	} else {
		currentSession = r.sessionMgr.GetOrCreateActiveSession(msg.UserID, agentType)
	}
	if currentSession == nil {
		return nil, "", "", nil, errors.New("Failed to create or get session")
	}

	currentAgent := r.agentMgr.ResolveAgent(currentSession.AgentType)
	if currentAgent == nil {
		return nil, "", "", nil, fmt.Errorf("No agent available for session type: %s", currentSession.AgentType)
	}

	sessionKey := agentSessionContextKey(currentSession.AgentType)
	agentSessionID := ""
	if sessionKey != "" {
		if sid, ok := currentSession.Context[sessionKey].(string); ok {
			agentSessionID = sid
		}
	}

	return currentSession, sessionKey, agentSessionID, currentAgent, nil
}

// mapAgentName 将 Agent 名称映射到会话类型
func mapAgentName(agentName string) string {
	switch agentName {
	case "claude-code":
		return "claude"
	case "codex":
		return "codex"
	default:
		return agentName
	}
}

func agentSessionContextKey(agentType string) string {
	switch strings.ToLower(strings.TrimSpace(agentType)) {
	case "claude":
		return "claude_session_id"
	case "codex":
		return "codex_session_id"
	default:
		return ""
	}
}

// extractSessionID 从响应中提取 session ID
func extractSessionID(response string) string {
	prefix := "\n\n__SESSION_ID__: "
	idx := strings.LastIndex(response, prefix)
	if idx == -1 {
		return ""
	}
	return strings.TrimSpace(response[idx+len(prefix):])
}

// removeSessionIDMarker 从响应中移除 session ID 标记
func removeSessionIDMarker(response string) string {
	prefix := "\n\n__SESSION_ID__: "
	idx := strings.LastIndex(response, prefix)
	if idx == -1 {
		return response
	}
	return response[:idx]
}

type streamReplySender struct {
	writer              streamReplyWriter
	splitter            func(content string, maxSize int) []string
	lastPartialSnapshot string
	hasSeenPartial      bool
	hasEmittedChunks    bool
	hasEmittedDone      bool
}

func newStreamReplySender(writer streamReplyWriter, splitter func(content string, maxSize int) []string) *streamReplySender {
	return &streamReplySender{
		writer:   writer,
		splitter: splitter,
	}
}

func (s *streamReplySender) PushPartialSnapshot(ctx context.Context, snapshot string) error {
	if s.hasEmittedDone {
		return nil
	}
	if snapshot == "" {
		return nil
	}

	s.hasSeenPartial = true
	delta, nextSnapshot := resolveDeltaFromSnapshot(s.lastPartialSnapshot, snapshot)
	s.lastPartialSnapshot = nextSnapshot
	if delta == "" {
		return nil
	}

	return s.emitText(ctx, delta, false)
}

func (s *streamReplySender) PushDeliverText(ctx context.Context, text string, isFinal bool) error {
	if s.hasEmittedDone {
		return nil
	}
	if !isFinal {
		return nil
	}

	if s.hasSeenPartial {
		if text != "" {
			if err := s.PushPartialSnapshot(ctx, text); err != nil {
				return err
			}
		}
		return s.finalize(ctx)
	}

	if strings.TrimSpace(text) == "" {
		return nil
	}

	if err := s.emitText(ctx, text, true); err != nil {
		return err
	}
	return s.finalize(ctx)
}

func (s *streamReplySender) Settle(ctx context.Context) error {
	return s.finalize(ctx)
}

func (s *streamReplySender) finalize(ctx context.Context) error {
	if s.hasEmittedDone {
		return nil
	}
	if !s.hasEmittedChunks {
		return nil
	}
	if err := s.writer.SendChunk(ctx, "", true); err != nil {
		return err
	}
	s.hasEmittedDone = true
	return nil
}

func (s *streamReplySender) emitText(ctx context.Context, content string, markLastDone bool) error {
	chunks := s.splitter(content, streamReplyChunkSize)
	normalized := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		if chunk == "" {
			continue
		}
		normalized = append(normalized, chunk)
	}

	for idx, chunk := range normalized {
		done := markLastDone && idx == len(normalized)-1
		if err := s.writer.SendChunk(ctx, chunk, done); err != nil {
			return err
		}
		s.hasEmittedChunks = true
		if done {
			s.hasEmittedDone = true
		}
	}

	return nil
}

type legacyStreamReplyWriter struct {
	send func(content string) error
}

func (w *legacyStreamReplyWriter) SendChunk(ctx context.Context, content string, done bool) error {
	if done && content == "" {
		return nil
	}
	if content == "" {
		return nil
	}

	return w.send(content)
}

func resolveDeltaFromSnapshot(previous, next string) (string, string) {
	if next == "" || next == previous {
		return "", next
	}
	if strings.HasPrefix(next, previous) {
		return next[len(previous):], next
	}
	if strings.HasPrefix(previous, next) {
		return "", next
	}

	prefixLen := 0
	maxLen := len(previous)
	if len(next) < maxLen {
		maxLen = len(next)
	}
	for prefixLen < maxLen && previous[prefixLen] == next[prefixLen] {
		prefixLen++
	}

	return next[prefixLen:], next
}
