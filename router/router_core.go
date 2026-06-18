package router

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/kangjinshan/weibo-ai-bridge/agent"
	"github.com/kangjinshan/weibo-ai-bridge/platform/weibo"
	"github.com/kangjinshan/weibo-ai-bridge/session"
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
	handlersMu     sync.RWMutex
	handlers       map[MessageType]Handler
	defaultHandler Handler
	platform       Platform
	sessionMgr     *session.Manager
	agentMgr       *agent.Manager
	commandHandler *CommandHandler
	rootCtx        context.Context
	rootCancel     context.CancelFunc
	closeOnce      sync.Once
	liveMu         sync.Mutex
	liveSessions   map[string]*interactiveSessionState
	superReviewMu  sync.Mutex
	superReviews   map[string]superReviewRun
	nextReviewID   int64
	listenMu       sync.Mutex
	listenRuns     map[string]listenRun
	nextListenID   int64
}

type superReviewRun struct {
	id     int64
	cancel context.CancelFunc
}

type listenRun struct {
	id     int64
	cancel context.CancelFunc
	target NativeSession
}

type streamReplyWriter interface {
	SendChunk(ctx context.Context, content string, done bool) error
}

// NewRouter 创建路由器
func NewRouter(platform Platform, sessionMgr *session.Manager, agentMgr *agent.Manager) *Router {
	rootCtx, rootCancel := context.WithCancel(context.Background())
	router := &Router{
		handlers:     make(map[MessageType]Handler),
		platform:     platform,
		sessionMgr:   sessionMgr,
		agentMgr:     agentMgr,
		rootCtx:      rootCtx,
		rootCancel:   rootCancel,
		liveSessions: make(map[string]*interactiveSessionState),
		superReviews: make(map[string]superReviewRun),
		listenRuns:   make(map[string]listenRun),
	}

	if sessionMgr != nil && agentMgr != nil {
		router.commandHandler = NewCommandHandler(sessionMgr, agentMgr)
		router.commandHandler.SetAgentAvailabilityRepairer(newConfigBackedAgentAvailabilityRepairer(agentMgr, ""))
		router.Register(TypeText, router)
		router.Register(TypeImage, router)
		router.Register(TypeVoice, router)
		router.Register(TypeLocation, router)
	}

	return router
}

// Close cancels in-flight router work and closes live interactive sessions.
func (r *Router) Close() {
	if r == nil {
		return
	}
	r.closeOnce.Do(func() {
		// Cancel listen runs and super peer reviews before root cancel.
		r.listenMu.Lock()
		for _, run := range r.listenRuns {
			if run.cancel != nil {
				run.cancel()
			}
		}
		r.listenRuns = make(map[string]listenRun)
		r.listenMu.Unlock()

		r.superReviewMu.Lock()
		for _, run := range r.superReviews {
			if run.cancel != nil {
				run.cancel()
			}
		}
		r.superReviews = make(map[string]superReviewRun)
		r.superReviewMu.Unlock()

		if r.rootCancel != nil {
			r.rootCancel()
		}

		r.liveMu.Lock()
		liveSessions := make([]*interactiveSessionState, 0, len(r.liveSessions))
		for _, state := range r.liveSessions {
			liveSessions = append(liveSessions, state)
		}
		r.liveSessions = make(map[string]*interactiveSessionState)
		r.liveMu.Unlock()

		for _, state := range liveSessions {
			if state != nil && state.session != nil {
				_ = state.session.Close()
			}
		}
	})
}

func (r *Router) lifecycleContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if r == nil || r.rootCtx == nil {
		return context.WithCancel(ctx)
	}

	runCtx, cancel := context.WithCancel(ctx)
	stopRootCancel := context.AfterFunc(r.rootCtx, cancel)
	return runCtx, func() {
		stopRootCancel()
		cancel()
	}
}

// Register 注册处理器
func (r *Router) Register(msgType MessageType, handler Handler) {
	r.handlersMu.Lock()
	defer r.handlersMu.Unlock()
	r.handlers[msgType] = handler
}

// SetDefault 设置默认处理器
func (r *Router) SetDefault(handler Handler) {
	r.handlersMu.Lock()
	defer r.handlersMu.Unlock()
	r.defaultHandler = handler
}

// Handle 处理消息（实现 Handler 接口）
func (r *Router) Handle(msg *Message) (*Response, error) {
	if msg == nil {
		return nil, errors.New("message cannot be nil")
	}

	content := strings.TrimSpace(msg.Content)

	if strings.HasPrefix(content, "/") && r.commandHandler != nil {
		if isSpecialRouterCommand(content) {
			ctx, cancel := r.lifecycleContext(context.Background())
			defer cancel()
			return r.handleByTheWaySync(ctx, msg)
		}
		return r.commandHandler.Handle(msg)
	}

	baseCtx, baseCancel := r.lifecycleContext(context.Background())
	defer baseCancel()

	ctx, cancel := context.WithTimeout(baseCtx, 5*time.Minute)
	defer cancel()

	stream, err := r.streamRouterMessage(ctx, msg)
	if err != nil {
		return &Response{Success: false, Content: err.Error()}, nil
	}

	var parts []string
	var errMsg string
	for event := range stream {
		switch event.Type {
		case agent.EventTypeDelta, agent.EventTypeMessage:
			if strings.TrimSpace(event.Content) != "" {
				parts = append(parts, event.Content)
			}
		case agent.EventTypeError:
			if strings.TrimSpace(event.Error) != "" {
				errMsg = event.Error
			}
		}
	}

	if errMsg != "" {
		return &Response{Success: false, Content: errMsg}, nil
	}

	return &Response{Success: true, Content: strings.Join(parts, "")}, nil
}

// Route 路由消息
func (r *Router) Route(msg *Message) (*Response, error) {
	if msg == nil {
		return nil, errors.New("message cannot be nil")
	}

	r.handlersMu.RLock()
	handler, exists := r.handlers[msg.Type]
	r.handlersMu.RUnlock()
	if !exists {
		return nil, errors.New("no handler for message type: " + string(msg.Type))
	}

	return handler.Handle(msg)
}

// GetHandler 获取处理器
func (r *Router) GetHandler(msgType MessageType) (Handler, bool) {
	r.handlersMu.RLock()
	defer r.handlersMu.RUnlock()
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

// CustomizeProcessingAck 根据会话状态定制“处理中”提示文案。
func (r *Router) CustomizeProcessingAck(msg *weibo.Message, defaultMessage string) string {
	if msg == nil || r.sessionMgr == nil {
		return defaultMessage
	}

	content := strings.TrimSpace(msg.Content)
	if strings.HasPrefix(content, "/") {
		return defaultMessage
	}

	sess, ok := r.sessionMgr.GetActiveSession(msg.UserID)
	if !ok || sess == nil {
		return defaultMessage
	}
	if !isSuperModeEnabled(sess) {
		return defaultMessage
	}

	agentType := strings.TrimSpace(sess.AgentType)
	if agentType == "" && r.agentMgr != nil {
		if defaultAgent := r.agentMgr.GetDefaultAgent(); defaultAgent != nil {
			agentType = mapAgentName(defaultAgent.Name())
		}
	}

	feedback, ready := superFeedbackReadyForAgent(sess, agentType)
	if !ready || strings.TrimSpace(feedback) == "" {
		return defaultMessage
	}

	return defaultMessage + "（Super：本轮将注入对侧已审批结论）"
}
