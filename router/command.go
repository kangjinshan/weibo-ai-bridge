package router

import (
	"fmt"
	"strconv"
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
	case "/list":
		return h.handleList(msg.UserID)
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
/list - 查看当前用户的所有会话
/switch [number] - 切换当前活跃会话
/switch [agent_type] - 切换当前会话的 Agent 类型
/btw [content] - 向当前正在进行的交互会话插入一条补充消息
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
	agentType := h.defaultNewSessionAgentType(userID)
	if len(args) > 0 {
		agentType = args[0]
	}

	if strings.TrimSpace(agentType) == "" {
		return &Response{
			Success: false,
			Content: "No agent available to create a new session.",
		}, nil
	}

	// 验证 agent 类型
	if agentType != "claude" && agentType != "codex" {
		return &Response{
			Success: false,
			Content: "Invalid agent type. Use claude or codex.",
		}, nil
	}
	if !h.isAgentTypeAvailable(agentType) {
		return &Response{
			Success: false,
			Content: fmt.Sprintf("Requested agent is not available: %s", agentType),
		}, nil
	}

	// 创建新会话
	newSession := h.sessionManager.CreateNext(userID, agentType)

	if newSession == nil {
		return &Response{
			Success: false,
			Content: "Failed to create new session. Maximum sessions reached.",
		}, nil
	}

	return &Response{
		Success: true,
		Content: fmt.Sprintf("New session created: %s (Agent: %s)", newSession.ID, agentType),
	}, nil
}

func (h *CommandHandler) defaultNewSessionAgentType(userID string) string {
	if h.sessionManager != nil {
		if activeSession, ok := h.sessionManager.GetActiveSession(userID); ok {
			if strings.TrimSpace(activeSession.AgentType) != "" && h.isAgentTypeAvailable(activeSession.AgentType) {
				return activeSession.AgentType
			}
		}
	}

	if h.agentManager != nil {
		if defaultAgent := h.agentManager.GetDefaultAgent(); defaultAgent != nil {
			return mapAgentName(defaultAgent.Name())
		}
	}

	return ""
}

func (h *CommandHandler) handleList(userID string) (*Response, error) {
	sessions := h.sessionManager.ListByUser(userID)
	if len(sessions) == 0 {
		return &Response{
			Success: false,
			Content: "No sessions found. Use /new to create a new session.",
		}, nil
	}

	activeSessionID := h.sessionManager.GetActiveSessionID(userID)
	lines := []string{"Sessions:"}
	for i, sess := range sessions {
		marker := ""
		if sess.ID == activeSessionID {
			marker = ", active"
		}
		title := displaySessionTitle(sess)
		lines = append(lines, fmt.Sprintf(
			"【%d】%s (id=%s, agent=%s, state=%s%s)",
			i+1,
			title,
			sess.ID,
			sess.AgentType,
			sess.State,
			marker,
		))
	}

	return &Response{
		Success: true,
		Content: strings.Join(lines, "\n"),
	}, nil
}

// handleSwitch 处理切换会话或 Agent 命令
func (h *CommandHandler) handleSwitch(userID, sessionID string, args []string) (*Response, error) {
	if len(args) == 0 {
		return &Response{
			Success: false,
			Content: "Please specify a session number or agent type: /switch 1, /switch claude, or /switch codex",
		}, nil
	}

	target := strings.TrimSpace(args[0])
	if index, err := strconv.Atoi(target); err == nil {
		return h.handleSwitchSession(userID, index)
	}

	agentType := target

	// 验证 agent 类型
	if agentType != "claude" && agentType != "codex" {
		return &Response{
			Success: false,
			Content: "Invalid agent type. Use claude or codex.",
		}, nil
	}
	if !h.isAgentTypeAvailable(agentType) {
		return &Response{
			Success: false,
			Content: fmt.Sprintf("Requested agent is not available: %s", agentType),
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
	sess.SetAgentType(agentType)

	return &Response{
		Success: true,
		Content: fmt.Sprintf("Switched to %s agent", agentType),
	}, nil
}

func (h *CommandHandler) handleSwitchSession(userID string, index int) (*Response, error) {
	sessions := h.sessionManager.ListByUser(userID)
	if len(sessions) == 0 {
		return &Response{
			Success: false,
			Content: "No sessions found. Use /new to create a new session.",
		}, nil
	}

	if index < 1 || index > len(sessions) {
		return &Response{
			Success: false,
			Content: fmt.Sprintf("Invalid session number. Use /list to see valid sessions (1-%d).", len(sessions)),
		}, nil
	}

	selected := sessions[index-1]
	if ok := h.sessionManager.SetActiveSession(userID, selected.ID); !ok {
		return &Response{
			Success: false,
			Content: "Failed to switch active session.",
		}, nil
	}

	return &Response{
		Success: true,
		Content: fmt.Sprintf("Switched to session %d: %s (id=%s, Agent: %s)", index, displaySessionTitle(selected), selected.ID, selected.AgentType),
	}, nil
}

func displaySessionTitle(sess *session.Session) string {
	if sess == nil {
		return "未命名会话"
	}
	if strings.TrimSpace(sess.Title) != "" {
		return sess.Title
	}
	return "未命名会话"
}

func (h *CommandHandler) isAgentTypeAvailable(agentType string) bool {
	if h.agentManager == nil {
		return false
	}

	return h.agentManager.ResolveAgent(agentType) != nil
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
	currentAgent := h.agentManager.ResolveAgent(sess.AgentType)
	if currentAgent == nil {
		return &Response{
			Success: false,
			Content: fmt.Sprintf("Selected agent is not available: %s", sess.AgentType),
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
	currentAgent := h.agentManager.ResolveAgent(sess.AgentType)

	statusText := fmt.Sprintf(`Session Status:
ID: %s
Title: %s
User ID: %s
Agent Type: %s
State: %s
Created At: %s
Updated At: %s
Total Sessions: %d`,
		sess.ID,
		displaySessionTitle(sess),
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
		statusText += fmt.Sprintf("\nCurrent Agent: Unavailable (%s)", sess.AgentType)
	}

	return &Response{
		Success: true,
		Content: statusText,
	}, nil
}
