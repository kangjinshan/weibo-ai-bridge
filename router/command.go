package router

import (
	"fmt"
	"strings"

	"github.com/yourusername/weibo-ai-bridge/agent"
	"github.com/yourusername/weibo-ai-bridge/session"
)

// CommandHandler 命令处理器
type CommandHandler struct {
	sessionManager *session.Manager
	agentManager   *agent.Manager
}

// NewCommandHandler 创建命令处理器
func NewCommandHandler(sessionManager *session.Manager, agentManager *agent.Manager) *CommandHandler {
	return &CommandHandler{
		sessionManager: sessionManager,
		agentManager:   agentManager,
	}
}

// Handle 处理消息
func (h *CommandHandler) Handle(msg *Message) (*Response, error) {
	if msg == nil {
		return nil, fmt.Errorf("message cannot be nil")
	}

	content := strings.TrimSpace(msg.Content)

	// 检查是否是命令
	if !strings.HasPrefix(content, "/") {
		return &Response{
			Success: false,
			Content: "Unknown command. Use /help to see available commands.",
		}, nil
	}

	// 解析命令
	parts := strings.Fields(content)
	if len(parts) == 0 {
		return &Response{
			Success: false,
			Content: "Empty command. Use /help to see available commands.",
		}, nil
	}

	command := parts[0]
	args := parts[1:]

	// 路由到不同的处理函数
	switch command {
	case "/help":
		return h.handleHelp()
	case "/new":
		return h.handleNew(msg.UserID, args)
	case "/switch":
		return h.handleSwitch(msg.UserID, msg.SessionID, args)
	case "/model":
		return h.handleModel(msg.UserID, msg.SessionID, args)
	case "/dir":
		return h.handleDir(msg.UserID, msg.SessionID)
	case "/status":
		return h.handleStatus(msg.UserID, msg.SessionID)
	default:
		return &Response{
			Success: false,
			Content: fmt.Sprintf("Unknown command: %s. Use /help to see available commands.", command),
		}, nil
	}
}

// handleHelp 处理帮助命令
func (h *CommandHandler) handleHelp() (*Response, error) {
	helpText := `可用命令：
/help - 显示帮助信息
/new [agent_type] - 创建新会话（可选参数：claude/codex）
/switch [agent_type] - 切换当前会话的 Agent 类型
/model - 显示当前使用的模型
/dir - 显示当前工作目录
/status - 显示当前会话状态`
	return &Response{
		Success: true,
		Content: helpText,
	}, nil
}

// handleNew 处理创建新会话命令
func (h *CommandHandler) handleNew(userID string, args []string) (*Response, error) {
	// 默认使用 claude
	agentType := "claude"
	if len(args) > 0 {
		agentType = args[0]
	}

	// 验证 agent 类型
	if agentType != "claude" && agentType != "codex" {
		return &Response{
			Success: false,
			Content: "Invalid agent type. Use claude or codex.",
		}, nil
	}

	// 创建新会话
	sessionID := fmt.Sprintf("%s-%d", userID, h.sessionManager.Count()+1)
	newSession := h.sessionManager.Create(sessionID, userID, agentType)

	if newSession == nil {
		return &Response{
			Success: false,
			Content: "Failed to create new session. Maximum sessions reached.",
		}, nil
	}

	return &Response{
		Success: true,
		Content: fmt.Sprintf("New session created: %s (Agent: %s)", sessionID, agentType),
	}, nil
}

// handleSwitch 处理切换 Agent 命令
func (h *CommandHandler) handleSwitch(userID, sessionID string, args []string) (*Response, error) {
	if len(args) == 0 {
		return &Response{
			Success: false,
			Content: "Please specify agent type: /switch claude or /switch codex",
		}, nil
	}

	agentType := args[0]

	// 验证 agent 类型
	if agentType != "claude" && agentType != "codex" {
		return &Response{
			Success: false,
			Content: "Invalid agent type. Use claude or codex.",
		}, nil
	}

	// 获取会话
	sess, exists := h.sessionManager.Get(sessionID)
	if !exists {
		return &Response{
			Success: false,
			Content: "Session not found. Use /new to create a new session.",
		}, nil
	}

	// 更新会话的 Agent 类型
	sess.AgentType = agentType

	return &Response{
		Success: true,
		Content: fmt.Sprintf("Switched to %s agent", agentType),
	}, nil
}

// handleModel 处理显示模型命令
func (h *CommandHandler) handleModel(userID, sessionID string, args []string) (*Response, error) {
	// 获取会话
	sess, exists := h.sessionManager.Get(sessionID)
	if !exists {
		return &Response{
			Success: false,
			Content: "Session not found. Use /new to create a new session.",
		}, nil
	}

	// 获取当前 Agent
	currentAgent := h.agentManager.GetDefaultAgent()
	if currentAgent == nil {
		return &Response{
			Success: false,
			Content: "No agent available.",
		}, nil
	}

	return &Response{
		Success: true,
		Content: fmt.Sprintf("Current session: %s\nAgent type: %s\nModel: %s", sess.ID, sess.AgentType, currentAgent.Name()),
	}, nil
}

// handleDir 处理显示目录命令
func (h *CommandHandler) handleDir(userID, sessionID string) (*Response, error) {
	// 获取会话
	sess, exists := h.sessionManager.Get(sessionID)
	if !exists {
		return &Response{
			Success: false,
			Content: "Session not found. Use /new to create a new session.",
		}, nil
	}

	// 从会话上下文中获取工作目录
	workDir, ok := sess.Context["work_dir"].(string)
	if !ok {
		return &Response{
			Success: false,
			Content: "No working directory set for this session.",
		}, nil
	}

	return &Response{
		Success: true,
		Content: fmt.Sprintf("Current working directory: %s", workDir),
	}, nil
}

// handleStatus 处理显示状态命令
func (h *CommandHandler) handleStatus(userID, sessionID string) (*Response, error) {
	// 获取会话
	sess, exists := h.sessionManager.Get(sessionID)
	if !exists {
		return &Response{
			Success: false,
			Content: "Session not found. Use /new to create a new session.",
		}, nil
	}

	// 获取当前 Agent
	currentAgent := h.agentManager.GetDefaultAgent()

	statusText := fmt.Sprintf(`Session Status:
ID: %s
User ID: %s
Agent Type: %s
State: %s
Created At: %s
Updated At: %s
Total Sessions: %d`,
		sess.ID,
		sess.UserID,
		sess.AgentType,
		sess.State,
		sess.CreatedAt.Format("2006-01-02 15:04:05"),
		sess.UpdatedAt.Format("2006-01-02 15:04:05"),
		h.sessionManager.Count(),
	)

	if currentAgent != nil {
		statusText += fmt.Sprintf("\nCurrent Agent: %s", currentAgent.Name())
	} else {
		statusText += "\nCurrent Agent: None"
	}

	return &Response{
		Success: true,
		Content: statusText,
	}, nil
}