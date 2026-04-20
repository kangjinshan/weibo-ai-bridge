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

// NewRouter 创建路由器
func NewRouter(platform PlatformInterface, sessionMgr *session.Manager, agentMgr *agent.Manager) *Router {
	router := &Router{
		handlers:   make(map[MessageType]Handler),
		platform:   platform,
		sessionMgr: sessionMgr,
		agentMgr:   agentMgr,
	}

	// 创建命令处理器
	if sessionMgr != nil && agentMgr != nil {
		router.commandHandler = NewCommandHandler(sessionMgr, agentMgr)
		// 已声明的消息类型默认都走同一套 AI/命令路由，避免非文本类型直接报无处理器。
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

	// 如果消息以 / 开头，使用命令处理器
	if strings.HasPrefix(content, "/") && r.commandHandler != nil {
		return r.commandHandler.Handle(msg)
	}

	// 否则使用 AI 处理器
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

// HandleMessage 处理消息（主入口）
func (r *Router) HandleMessage(ctx context.Context, msg *weibo.Message) error {
	if msg == nil {
		return errors.New("message cannot be nil")
	}

	// 转换消息格式
	sessionID := msg.UserID
	if r.sessionMgr != nil {
		if activeSessionID := r.sessionMgr.GetActiveSessionID(msg.UserID); activeSessionID != "" {
			sessionID = activeSessionID
		}
	}

	routerMsg := &Message{
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

	// 路由消息
	resp, err := r.Route(routerMsg)
	if err != nil {
		return fmt.Errorf("route message failed: %w", err)
	}

	if resp == nil {
		return errors.New("response is nil")
	}

	// 如果有错误，返回错误消息
	if !resp.Success && resp.Error != nil {
		return resp.Error
	}

	// 如果有内容，发送回复
	if resp.Content != "" {
		return r.sendReply(ctx, msg.UserID, resp.Content)
	}

	return nil
}

// sendReply 发送回复（支持分块）
func (r *Router) sendReply(ctx context.Context, userID string, content string) error {
	if r.platform == nil {
		return errors.New("platform is not set")
	}

	// 分块发送（每块最大 1000 字符）
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

	// 按行分割，确保不超过最大长度
	lines := strings.Split(content, "\n")

	for _, line := range lines {
		// 如果当前行本身就超过最大长度，需要强制分割
		if len(line) > maxSize {
			// 先将缓冲区内容加入 chunks
			if buffer.Len() > 0 {
				chunks = append(chunks, buffer.String())
				buffer.Reset()
			}

			// 强制分割长行
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

		// 检查加入当前行是否会超过限制
		if buffer.Len()+len(line)+1 > maxSize {
			// 保存当前缓冲区
			chunks = append(chunks, buffer.String())
			buffer.Reset()
		}

		buffer.WriteString(line)
		buffer.WriteString("\n")
	}

	// 保存剩余内容
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

	// 获取或创建会话
	var session *session.Session
	if strings.TrimSpace(msg.SessionID) != "" {
		session = r.sessionMgr.GetOrCreateSession(msg.SessionID, msg.UserID, "claude")
	} else {
		session = r.sessionMgr.GetOrCreateActiveSession(msg.UserID, "claude")
	}
	if session == nil {
		return &Response{
			Success: false,
			Content: "Failed to create or get session",
		}, nil
	}

	// 获取 Agent
	currentAgent := r.agentMgr.ResolveAgent(session.AgentType)
	if currentAgent == nil {
		return &Response{
			Success: false,
			Content: fmt.Sprintf("No agent available for session type: %s", session.AgentType),
		}, nil
	}

	// 执行 AI 任务
	response, err := currentAgent.Execute(msg.Content)
	if err != nil {
		return &Response{
			Success: false,
			Content: fmt.Sprintf("AI execution failed: %v", err),
		}, nil
	}

	return &Response{
		Success: true,
		Content: response,
	}, nil
}
