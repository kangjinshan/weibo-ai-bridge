package router

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/yourusername/weibo-ai-bridge/agent"
	"github.com/yourusername/weibo-ai-bridge/platform/weibo"
	"github.com/yourusername/weibo-ai-bridge/session"
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
	liveMu         sync.Mutex
	liveSessions   map[string]*interactiveSessionState
}

type interactiveSessionState struct {
	agentType        string
	session          agent.InteractiveSession
	awaitingApproval bool
	allowAll         bool
}

var approvalMentionPattern = regexp.MustCompile(`@\S+`)

const interactiveDoneGracePeriod = 200 * time.Millisecond

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
		handlers:     make(map[MessageType]Handler),
		platform:     platform,
		sessionMgr:   sessionMgr,
		agentMgr:     agentMgr,
		liveSessions: make(map[string]*interactiveSessionState),
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
		if isByTheWayCommand(content) {
			return r.handleByTheWaySync(context.Background(), msg)
		}
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

func (r *Router) InjectByTheWay(ctx context.Context, msg *weibo.Message) (bool, error) {
	if msg == nil {
		return false, errors.New("message cannot be nil")
	}

	routerMsg := r.toRouterMessage(msg)
	if !isByTheWayCommand(routerMsg.Content) {
		return false, nil
	}

	_, err := r.injectByTheWay(routerMsg)
	return true, err
}

func (r *Router) toRouterMessage(msg *weibo.Message) *Message {
	sessionID := ""
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
			if handled, err := r.emitSpecialCommandEvents(ctx, events, msg); handled {
				if err != nil && !isBenignCancellation(err) {
					events <- agent.Event{Type: agent.EventTypeError, Error: err.Error()}
				}
				events <- agent.Event{Type: agent.EventTypeDone}
				return
			}
			r.emitCommandEvents(events, msg)
			return
		}

		if err := r.streamAIMessage(ctx, msg, events); err != nil && !isBenignCancellation(err) {
			events <- agent.Event{Type: agent.EventTypeError, Error: err.Error()}
		}

		events <- agent.Event{Type: agent.EventTypeDone}
	}()

	return events, nil
}

func isByTheWayCommand(content string) bool {
	parts := strings.Fields(strings.TrimSpace(content))
	return len(parts) > 0 && parts[0] == "/btw"
}

func (r *Router) handleByTheWaySync(ctx context.Context, msg *Message) (*Response, error) {
	stream, err := r.streamRouterMessage(ctx, msg)
	if err != nil {
		return nil, err
	}

	var contentParts []string
	var responseErr error
	success := true

	for event := range stream {
		switch event.Type {
		case agent.EventTypeMessage, agent.EventTypeApproval:
			if text := strings.TrimSpace(event.Content); text != "" {
				contentParts = append(contentParts, text)
			}
		case agent.EventTypeError:
			success = false
			if strings.TrimSpace(event.Error) != "" {
				responseErr = errors.New(event.Error)
				if len(contentParts) == 0 {
					contentParts = append(contentParts, event.Error)
				}
			}
		}
	}

	return &Response{
		Success: success,
		Content: strings.Join(contentParts, "\n"),
		Error:   responseErr,
	}, nil
}

func (r *Router) emitSpecialCommandEvents(ctx context.Context, events chan<- agent.Event, msg *Message) (bool, error) {
	if !isByTheWayCommand(msg.Content) {
		return false, nil
	}

	return true, r.handleByTheWayCommand(ctx, msg, events)
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

func (r *Router) handleByTheWayCommand(ctx context.Context, msg *Message, events chan<- agent.Event) error {
	sess, liveState, content, err := r.resolveByTheWayTarget(msg)
	if err != nil {
		events <- agent.Event{Type: agent.EventTypeMessage, Content: err.Error()}
		return nil
	}

	if err := liveState.session.Send(content); err != nil {
		r.removeInteractiveSession(sess.ID)
		return err
	}

	return r.drainInteractiveSession(ctx, sess, agentSessionContextKey(sess.AgentType), liveState, events)
}

func (r *Router) injectByTheWay(msg *Message) (string, error) {
	_, liveState, content, err := r.resolveByTheWayTarget(msg)
	if err != nil {
		return "", err
	}

	if err := liveState.session.Send(content); err != nil {
		return "", err
	}

	return "已注入补充说明，当前回复会继续输出。", nil
}

func (r *Router) resolveByTheWayTarget(msg *Message) (*session.Session, *interactiveSessionState, string, error) {
	if r.sessionMgr == nil {
		return nil, nil, "", errors.New("Session manager is not available.")
	}

	parts := strings.Fields(strings.TrimSpace(msg.Content))
	if len(parts) < 2 {
		return nil, nil, "", errors.New("Please provide content to insert: /btw <message>")
	}

	content := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(msg.Content), parts[0]))
	if content == "" {
		return nil, nil, "", errors.New("Please provide content to insert: /btw <message>")
	}

	sess, ok := r.resolveByTheWaySession(msg)
	if !ok {
		return nil, nil, "", errors.New("No active session found. Use /new to create or activate a session first.")
	}

	liveState, ok := r.getInteractiveSession(sess.ID)
	if !ok {
		return nil, nil, "", errors.New("No live interactive session is running for the current session yet.")
	}

	if liveState.awaitingApproval {
		return nil, nil, "", errors.New("Current session is waiting for approval. Reply with 允许 / 取消 / 允许所有 first.")
	}

	return sess, liveState, content, nil
}

func (r *Router) resolveByTheWaySession(msg *Message) (*session.Session, bool) {
	sessionIDs := []string{}
	if trimmed := strings.TrimSpace(msg.SessionID); trimmed != "" {
		sessionIDs = append(sessionIDs, trimmed)
	}
	if r.sessionMgr != nil {
		if activeSessionID := strings.TrimSpace(r.sessionMgr.GetActiveSessionID(msg.UserID)); activeSessionID != "" {
			sessionIDs = append(sessionIDs, activeSessionID)
		}
	}

	for _, sessionID := range slices.Compact(sessionIDs) {
		sess, exists := r.sessionMgr.Get(sessionID)
		if exists && sess.UserID == msg.UserID {
			return sess, true
		}
	}

	return nil, false
}

func (r *Router) getInteractiveSession(sessionID string) (*interactiveSessionState, bool) {
	r.liveMu.Lock()
	defer r.liveMu.Unlock()

	state, ok := r.liveSessions[sessionID]
	return state, ok
}

func (r *Router) forwardStreamToPlatform(ctx context.Context, userID string, stream <-chan agent.Event) error {
	if r.platform == nil {
		return errors.New("platform is not set")
	}

	writer, err := r.openStreamWriter(ctx, userID)
	if err != nil {
		return err
	}
	sender := newStreamReplySender(writer)

	var streamErr error

	for {
		select {
		case <-ctx.Done():
			if err := sender.Settle(context.WithoutCancel(ctx)); err != nil {
				return err
			}
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
				if err := sender.PushDelta(ctx, event.Content); err != nil {
					return err
				}
			case agent.EventTypeApproval:
				if strings.TrimSpace(event.Content) != "" {
					if err := sender.PushInformationalText(ctx, event.Content); err != nil {
						return err
					}
				}
			case agent.EventTypeMessage:
				if err := sender.PushDeliverText(ctx, event.Content, true); err != nil {
					return err
				}
			case agent.EventTypeError:
				if strings.TrimSpace(event.Error) != "" && !isBenignCancellation(errors.New(event.Error)) {
					if err := sender.PushDeliverText(ctx, "AI execution failed: "+event.Error, true); err != nil {
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

func isBenignCancellation(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	text := strings.TrimSpace(err.Error())
	return text == context.Canceled.Error() || text == context.DeadlineExceeded.Error()
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
	runes := []rune(content)
	if len(runes) <= maxSize {
		return []string{content}
	}

	var chunks []string
	var buffer strings.Builder

	lines := strings.Split(content, "\n")

	for _, line := range lines {
		lineRunes := []rune(line)
		if len(lineRunes) > maxSize {
			if buffer.Len() > 0 {
				chunks = append(chunks, buffer.String())
				buffer.Reset()
			}

			for len(lineRunes) > maxSize {
				chunks = append(chunks, string(lineRunes[:maxSize]))
				lineRunes = lineRunes[maxSize:]
			}
			if len(lineRunes) > 0 {
				buffer.WriteString(string(lineRunes))
				buffer.WriteString("\n")
			}
			continue
		}

		if utf8RuneCount(buffer.String())+len(lineRunes)+1 > maxSize {
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

func utf8RuneCount(value string) int {
	return len([]rune(value))
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

	if interactiveAgent, ok := currentAgent.(agent.InteractiveAgent); ok {
		return r.streamInteractiveAIMessage(ctx, msg, session, sessionKey, agentSessionID, interactiveAgent, events)
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

func (r *Router) streamInteractiveAIMessage(ctx context.Context, msg *Message, sess *session.Session, sessionKey, agentSessionID string, interactiveAgent agent.InteractiveAgent, events chan<- agent.Event) error {
	liveState, created, err := r.getOrCreateInteractiveSession(ctx, sess, sessionKey, agentSessionID, interactiveAgent)
	if err != nil {
		return err
	}

	if liveState.awaitingApproval {
		action, ok := parseApprovalAction(msg.Content)
		if !ok {
			events <- agent.Event{
				Type:    agent.EventTypeApproval,
				Content: approvalHintMessage(),
			}
			return nil
		}

		if action == agent.ApprovalActionAllowAll {
			liveState.allowAll = true
		}
		liveState.awaitingApproval = false

		if err := liveState.session.RespondApproval(action); err != nil {
			if action == agent.ApprovalActionAllowAll {
				liveState.allowAll = false
			}
			return err
		}

		if action == agent.ApprovalActionAllowAll {
			events <- agent.Event{
				Type:    agent.EventTypeApproval,
				Content: "授权成功，这对话内将不再需要再次授权。",
			}
		}

		return r.drainInteractiveSession(ctx, sess, sessionKey, liveState, events)
	}

	if !created {
		r.waitInteractiveEventsQuiesced(sess, sessionKey, liveState, interactiveDoneGracePeriod)
	}
	r.discardBufferedInteractiveEvents(sess, sessionKey, liveState)

	if err := liveState.session.Send(msg.Content); err != nil {
		r.removeInteractiveSession(sess.ID)
		return err
	}

	return r.drainInteractiveSession(ctx, sess, sessionKey, liveState, events)
}

func (r *Router) discardBufferedInteractiveEvents(sess *session.Session, sessionKey string, liveState *interactiveSessionState) {
	for {
		select {
		case event, ok := <-liveState.session.Events():
			if !ok {
				r.removeInteractiveSession(sess.ID)
				return
			}

			if event.Type == agent.EventTypeSession && sessionKey != "" && strings.TrimSpace(event.SessionID) != "" {
				if sess.Context == nil {
					sess.Context = make(map[string]interface{})
				}
				sess.Context[sessionKey] = event.SessionID
				r.sessionMgr.UpdateSession(sess.ID, sessionKey, event.SessionID)
			}
		default:
			return
		}
	}
}

func (r *Router) waitInteractiveEventsQuiesced(sess *session.Session, sessionKey string, liveState *interactiveSessionState, quietPeriod time.Duration) {
	timer := time.NewTimer(quietPeriod)
	defer timer.Stop()

	for {
		select {
		case <-timer.C:
			return
		case event, ok := <-liveState.session.Events():
			if !ok {
				r.removeInteractiveSession(sess.ID)
				return
			}

			if event.Type == agent.EventTypeSession && sessionKey != "" && strings.TrimSpace(event.SessionID) != "" {
				if sess.Context == nil {
					sess.Context = make(map[string]interface{})
				}
				sess.Context[sessionKey] = event.SessionID
				r.sessionMgr.UpdateSession(sess.ID, sessionKey, event.SessionID)
			}

			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(quietPeriod)
		}
	}
}

func (r *Router) getOrCreateInteractiveSession(ctx context.Context, sess *session.Session, sessionKey, agentSessionID string, interactiveAgent agent.InteractiveAgent) (*interactiveSessionState, bool, error) {
	r.liveMu.Lock()
	defer r.liveMu.Unlock()

	if existing, ok := r.liveSessions[sess.ID]; ok {
		if existing.agentType == sess.AgentType {
			return existing, false, nil
		}
		_ = existing.session.Close()
		delete(r.liveSessions, sess.ID)
	}

	liveSession, err := interactiveAgent.StartSession(ctx, agentSessionID)
	if err != nil {
		return nil, false, err
	}

	state := &interactiveSessionState{
		agentType: sess.AgentType,
		session:   liveSession,
	}
	r.liveSessions[sess.ID] = state

	if sessionKey != "" {
		if sid := strings.TrimSpace(liveSession.CurrentSessionID()); sid != "" {
			if sess.Context == nil {
				sess.Context = make(map[string]interface{})
			}
			sess.Context[sessionKey] = sid
			r.sessionMgr.UpdateSession(sess.ID, sessionKey, sid)
		}
	}

	return state, true, nil
}

func (r *Router) drainInteractiveSession(ctx context.Context, sess *session.Session, sessionKey string, liveState *interactiveSessionState, events chan<- agent.Event) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-liveState.session.Events():
			if !ok {
				r.removeInteractiveSession(sess.ID)
				return errors.New("agent session closed unexpectedly")
			}

			if event.Type == agent.EventTypeSession && sessionKey != "" && strings.TrimSpace(event.SessionID) != "" {
				if sess.Context == nil {
					sess.Context = make(map[string]interface{})
				}
				sess.Context[sessionKey] = event.SessionID
				r.sessionMgr.UpdateSession(sess.ID, sessionKey, event.SessionID)
			}

			switch event.Type {
			case agent.EventTypeApproval:
				if liveState.allowAll {
					if err := liveState.session.RespondApproval(agent.ApprovalActionAllow); err != nil {
						r.removeInteractiveSession(sess.ID)
						return err
					}
					continue
				}

				liveState.awaitingApproval = true
				events <- agent.Event{
					Type:    agent.EventTypeApproval,
					Content: formatApprovalPrompt(event.ToolName, event.ToolInput),
				}
				return nil
			case agent.EventTypeDone:
				return r.drainInteractiveTailAfterDone(ctx, sess, sessionKey, liveState, events)
			case agent.EventTypeError:
				events <- event
				r.removeInteractiveSession(sess.ID)
				return nil
			default:
				events <- event
			}
		}
	}
}

func (r *Router) drainInteractiveTailAfterDone(ctx context.Context, sess *session.Session, sessionKey string, liveState *interactiveSessionState, events chan<- agent.Event) error {
	timer := time.NewTimer(interactiveDoneGracePeriod)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		case event, ok := <-liveState.session.Events():
			if !ok {
				r.removeInteractiveSession(sess.ID)
				return errors.New("agent session closed unexpectedly")
			}

			if event.Type == agent.EventTypeSession && sessionKey != "" && strings.TrimSpace(event.SessionID) != "" {
				if sess.Context == nil {
					sess.Context = make(map[string]interface{})
				}
				sess.Context[sessionKey] = event.SessionID
				r.sessionMgr.UpdateSession(sess.ID, sessionKey, event.SessionID)
			}

			switch event.Type {
			case agent.EventTypeDone:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(interactiveDoneGracePeriod)
			case agent.EventTypeError:
				events <- event
				r.removeInteractiveSession(sess.ID)
				return nil
			default:
				events <- event
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(interactiveDoneGracePeriod)
			}
		}
	}
}

func (r *Router) removeInteractiveSession(sessionID string) {
	r.liveMu.Lock()
	defer r.liveMu.Unlock()

	state, ok := r.liveSessions[sessionID]
	if !ok {
		return
	}

	_ = state.session.Close()
	delete(r.liveSessions, sessionID)
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

	currentSession.SetTitleIfEmpty(msg.Content)

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

func formatApprovalPrompt(toolName, toolInput string) string {
	toolName = strings.TrimSpace(toolName)
	toolInput = strings.TrimSpace(toolInput)

	if toolName == "" && toolInput == "" {
		return approvalHintMessage()
	}

	if toolInput == "" {
		return fmt.Sprintf("⚠️ 需要授权\n\nAgent 想执行：`%s`\n\n请回复：允许 / 取消 / 允许所有\n允许所有表示本对话内后续授权将自动通过。", toolName)
	}

	return fmt.Sprintf("⚠️ 需要授权\n\nAgent 想执行：`%s`\n\n```text\n%s\n```\n\n请回复：允许 / 取消 / 允许所有\n允许所有表示本对话内后续授权将自动通过。", toolName, toolInput)
}

func approvalHintMessage() string {
	return "当前正在等待授权，请回复：取消 / 允许 / 允许所有。"
}

func parseApprovalAction(content string) (agent.ApprovalAction, bool) {
	normalized := normalizeApprovalResponse(content)

	for _, word := range []string{
		"allow all", "allowall", "approve all", "yes all",
		"允许所有", "允许全部", "全部允许", "所有允许", "都允许", "全部同意",
	} {
		if normalized == word {
			return agent.ApprovalActionAllowAll, true
		}
	}

	for _, word := range []string{"allow", "yes", "y", "ok", "允许", "同意", "可以", "好", "好的", "是", "确认", "approve"} {
		if normalized == word {
			return agent.ApprovalActionAllow, true
		}
	}

	for _, word := range []string{"deny", "no", "n", "reject", "拒绝", "不允许", "不行", "不", "否", "取消", "cancel"} {
		if normalized == word {
			return agent.ApprovalActionCancel, true
		}
	}

	return "", false
}

func normalizeApprovalResponse(content string) string {
	content = strings.ReplaceAll(content, "\u3000", " ")
	content = strings.TrimSpace(strings.ToLower(content))
	content = approvalMentionPattern.ReplaceAllString(content, " ")
	content = strings.Join(strings.Fields(content), " ")
	content = strings.Trim(content, " \t\r\n,.!?;:，。！？；：")
	return content
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
	lastPartialSnapshot string
	pendingDelta        strings.Builder
	hasSeenPartial      bool
	hasEmittedChunks    bool
	hasEmittedDone      bool
}

func newStreamReplySender(writer streamReplyWriter) *streamReplySender {
	return &streamReplySender{
		writer: writer,
	}
}

func (s *streamReplySender) PushDelta(ctx context.Context, delta string) error {
	if s.hasEmittedDone {
		return nil
	}
	if delta == "" {
		return nil
	}

	s.hasSeenPartial = true
	s.pendingDelta.WriteString(delta)

	return s.flushBufferedDelta(ctx, false)
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
		if err := s.flushBufferedDelta(ctx, true); err != nil {
			return err
		}
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

	if err := s.emitText(ctx, text, false); err != nil {
		return err
	}
	return s.finalize(ctx)
}

func (s *streamReplySender) PushInformationalText(ctx context.Context, text string) error {
	if s.hasEmittedDone {
		return nil
	}
	if strings.TrimSpace(text) == "" {
		return nil
	}

	if err := s.flushBufferedDelta(ctx, true); err != nil {
		return err
	}

	return s.emitText(ctx, text, false)
}

func (s *streamReplySender) Settle(ctx context.Context) error {
	if err := s.flushBufferedDelta(ctx, true); err != nil {
		return err
	}
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
	if content == "" {
		return nil
	}

	if err := s.writer.SendChunk(ctx, content, markLastDone); err != nil {
		return err
	}
	s.hasEmittedChunks = true
	if markLastDone {
		s.hasEmittedDone = true
	}

	return nil
}

func (s *streamReplySender) flushBufferedDelta(ctx context.Context, force bool) error {
	buffered := s.pendingDelta.String()
	if buffered == "" {
		return nil
	}

	flushLen := findDeltaFlushBoundary(buffered, force)
	if flushLen <= 0 {
		return nil
	}

	flushText := buffered[:flushLen]
	remainText := buffered[flushLen:]
	s.pendingDelta.Reset()
	s.pendingDelta.WriteString(remainText)

	return s.emitText(ctx, flushText, false)
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

func findDeltaFlushBoundary(buffered string, force bool) int {
	if buffered == "" {
		return 0
	}
	if force {
		return len(buffered)
	}

	if idx := strings.LastIndex(buffered, "\n\n"); idx >= 0 {
		return idx + len("\n\n")
	}

	lastBoundary := 0
	runeCount := 0
	for idx, r := range buffered {
		runeCount++
		switch r {
		case '\n':
			lastBoundary = idx + utf8.RuneLen(r)
		case '。', '！', '？', '；', '：', '，', '.', '!', '?', ';':
			lastBoundary = idx + utf8.RuneLen(r)
		}
	}

	if lastBoundary == len(buffered) && runeCount >= 4 {
		return lastBoundary
	}
	if runeCount >= 12 && lastBoundary > 0 {
		return lastBoundary
	}
	if runeCount >= 220 {
		return len(buffered)
	}

	return 0
}
