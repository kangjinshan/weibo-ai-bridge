package router

import (
	"context"
	"errors"
	"strings"
	"sync"

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
	handlers       map[MessageType]Handler
	defaultHandler Handler
	platform       PlatformInterface
	sessionMgr     *session.Manager
	agentMgr       *agent.Manager
	commandHandler *CommandHandler
	liveMu         sync.Mutex
	liveSessions   map[string]*interactiveSessionState
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
		handlers:     make(map[MessageType]Handler),
		platform:     platform,
		sessionMgr:   sessionMgr,
		agentMgr:     agentMgr,
		liveSessions: make(map[string]*interactiveSessionState),
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
