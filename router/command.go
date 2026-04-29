package router

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kangjinshan/weibo-ai-bridge/agent"
	"github.com/kangjinshan/weibo-ai-bridge/session"
)

// CommandHandler 命令处理器
type CommandHandler struct {
	sessionManager *session.Manager
	agentManager   *agent.Manager
	agentRepairer  agentAvailabilityRepairer
}

type agentAvailabilityRepairer interface {
	EnsureAvailable(agentType string) (bool, error)
}

// NewCommandHandler 创建命令处理器
func NewCommandHandler(sessionManager *session.Manager, agentManager *agent.Manager) *CommandHandler {
	return &CommandHandler{
		sessionManager: sessionManager,
		agentManager:   agentManager,
	}
}

func (h *CommandHandler) SetAgentAvailabilityRepairer(repairer agentAvailabilityRepairer) {
	h.agentRepairer = repairer
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

	command := strings.ToLower(parts[0])
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
	case "/claude":
		return h.handleSwitch(msg.UserID, msg.SessionID, append([]string{"claude"}, args...))
	case "/codex":
		return h.handleSwitch(msg.UserID, msg.SessionID, append([]string{"codex"}, args...))
	case "/model":
		return h.handleModel(msg.UserID, msg.SessionID, args)
	case "/dir":
		return h.handleDir(msg.UserID, msg.SessionID, args)
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
/new [agent_type] - 准备一个新的原生会话（可选参数：claude/codex）
/list - 查看当前用户的原生会话
/switch [number] - 切换当前活跃会话
/switch [agent_type] - 切换当前会话的 Agent 类型
/claude - 等价于 /switch claude（大小写不敏感）
/codex - 等价于 /switch codex（大小写不敏感）
/btw [content] - 向当前正在进行的交互会话插入一条补充消息
/model - 显示当前使用的模型
/dir [path] - 显示或设置当前工作目录
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
	available, err := h.ensureAgentTypeAvailable(agentType)
	if err != nil {
		return &Response{
			Success: false,
			Content: fmt.Sprintf("Failed to prepare requested agent %s: %v", agentType, err),
		}, nil
	}
	if !available {
		return &Response{
			Success: false,
			Content: fmt.Sprintf("Requested agent is not available: %s", agentType),
		}, nil
	}

	// /new 不再创建 bridge 自增会话，改为准备一个待创建的 native 会话锚点。
	newSession := h.sessionManager.GetOrCreateSession(pendingNativeSessionID(userID), userID, agentType)
	if newSession == nil {
		return &Response{
			Success: false,
			Content: "Failed to create new session. Maximum sessions reached.",
		}, nil
	}
	h.sessionManager.SetSessionAgentType(newSession.ID, agentType)
	h.sessionManager.UpdateSession(newSession.ID, "claude_session_id", "")
	h.sessionManager.UpdateSession(newSession.ID, "codex_session_id", "")

	return &Response{
		Success: true,
		Content: fmt.Sprintf("Prepared a new native session (Agent: %s). Send your next message to create it.", agentType),
	}, nil
}

func (h *CommandHandler) defaultNewSessionAgentType(userID string) string {
	if h.sessionManager != nil {
		if activeSession, ok := h.sessionManager.GetActiveSession(userID); ok {
			if strings.TrimSpace(activeSession.AgentType) != "" {
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
	managedNativeSessions, allNative, _, activeNativeID := h.collectSwitchCandidates(userID)

	type listRow struct {
		Number string
		Title  string
		When   time.Time
	}
	rows := make([]listRow, 0, len(allNative))

	if len(allNative) > 0 {
		for i, ns := range allNative {
			title := nativeListTitle(ns)
			if strings.TrimSpace(ns.ID) != "" && strings.TrimSpace(ns.ID) == strings.TrimSpace(activeNativeID) {
				title += "（当前）"
			}
			rows = append(rows, listRow{
				Number: strconv.Itoa(i + 1),
				Title:  title,
				When:   ns.StartedAt,
			})
		}
	} else {
		// native 列表不可用时，回退到已绑定 native ID 的会话，避免空列表。
		activeSessionID := h.sessionManager.GetActiveSessionID(userID)
		for i, sess := range managedNativeSessions {
			title := truncateListTitle(displaySessionTitle(sess))
			if sess.ID == activeSessionID {
				title += "（当前）"
			}
			rows = append(rows, listRow{
				Number: strconv.Itoa(i + 1),
				Title:  title,
				When:   sess.UpdatedAt,
			})
		}
	}

	if len(rows) == 0 {
		return &Response{
			Success: false,
			Content: "No sessions found. Use /new to create a new session.",
		}, nil
	}

	lines := []string{
		"| 编号 | 标题 | 时间 |",
		"| --- | --- | --- |",
	}
	for _, row := range rows {
		lines = append(lines, fmt.Sprintf(
			"| %s | %s | %s |",
			escapeMarkdownTableCell(row.Number),
			escapeMarkdownTableCell(row.Title),
			escapeMarkdownTableCell(formatListTime(row.When)),
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

	// 兼容旧写法 N1 / N2 ...
	if strings.HasPrefix(strings.ToUpper(target), "N") && len(target) > 1 {
		if index, err := strconv.Atoi(target[1:]); err == nil {
			return h.handleSwitchByListNumber(userID, index)
		}
	}

	if index, err := strconv.Atoi(target); err == nil {
		return h.handleSwitchByListNumber(userID, index)
	}

	agentType := strings.ToLower(target)

	// 验证 agent 类型
	if agentType != "claude" && agentType != "codex" {
		return &Response{
			Success: false,
			Content: "Invalid agent type. Use claude or codex.",
		}, nil
	}
	available, err := h.ensureAgentTypeAvailable(agentType)
	if err != nil {
		return &Response{
			Success: false,
			Content: fmt.Sprintf("Failed to prepare requested agent %s: %v", agentType, err),
		}, nil
	}
	if !available {
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
	h.sessionManager.SetSessionAgentType(sess.ID, agentType)

	return &Response{
		Success: true,
		Content: fmt.Sprintf("Switched to %s agent", agentType),
	}, nil
}

func (h *CommandHandler) handleSwitchByListNumber(userID string, index int) (*Response, error) {
	managedNativeSessions, allNative, bridgeNativeToSessionID, _ := h.collectSwitchCandidates(userID)

	if len(allNative) > 0 {
		return h.handleSwitchNativeSessionWithCandidates(userID, index, allNative, bridgeNativeToSessionID)
	}

	total := len(managedNativeSessions)
	if total == 0 {
		return &Response{
			Success: false,
			Content: "No sessions found. Use /new to create a new session.",
		}, nil
	}

	if index < 1 || index > total {
		return &Response{
			Success: false,
			Content: fmt.Sprintf("Invalid session number. Use /list to see valid sessions (1-%d).", total),
		}, nil
	}

	selected := managedNativeSessions[index-1]
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

// handleSwitchNativeSession 领养原生会话并切换到它
func (h *CommandHandler) handleSwitchNativeSession(userID string, index int) (*Response, error) {
	_, allNative, bridgeNativeToSessionID, _ := h.collectSwitchCandidates(userID)
	return h.handleSwitchNativeSessionWithCandidates(userID, index, allNative, bridgeNativeToSessionID)
}

func (h *CommandHandler) handleSwitchNativeSessionWithCandidates(userID string, index int, allNative []NativeSession, bridgeNativeToSessionID map[string]string) (*Response, error) {
	if len(allNative) == 0 {
		return &Response{
			Success: false,
			Content: "No native sessions found. Use /list to see available sessions.",
		}, nil
	}

	if index < 1 || index > len(allNative) {
		return &Response{
			Success: false,
			Content: fmt.Sprintf("Invalid session number. Use /list to see valid sessions (1-%d).", len(allNative)),
		}, nil
	}

	selected := allNative[index-1]

	// 如果已被 bridge 管理，直接切换到对应的 bridge session
	if selected.InBridge {
		existingID := bridgeNativeToSessionID[selected.ID]
		if ok := h.sessionManager.SetActiveSession(userID, existingID); !ok {
			return &Response{
				Success: false,
				Content: "Failed to switch to existing bridge session.",
			}, nil
		}
		return &Response{
			Success: true,
			Content: fmt.Sprintf("Switched to existing session: %s (id=%s)", truncateListTitle(selected.Title), existingID),
		}, nil
	}

	// 确保 agent 可用
	agentType := selected.AgentType
	available, err := h.ensureAgentTypeAvailable(agentType)
	if err != nil || !available {
		return &Response{
			Success: false,
			Content: fmt.Sprintf("%s agent is not available.", agentType),
		}, nil
	}

	if existing, exists := h.sessionManager.Get(selected.ID); exists && existing.UserID != userID {
		return &Response{
			Success: false,
			Content: "Failed to switch native session: it belongs to another user.",
		}, nil
	}

	contextKey := agentSessionContextKey(agentType)
	newSession := h.sessionManager.GetOrCreateSession(selected.ID, userID, agentType)
	h.sessionManager.SetSessionAgentType(newSession.ID, agentType)
	h.sessionManager.UpdateSession(newSession.ID, contextKey, selected.ID)
	if strings.TrimSpace(selected.Project) != "" {
		h.sessionManager.UpdateSession(newSession.ID, "work_dir", selected.Project)
	}
	if selected.Title != "" {
		h.sessionManager.SetSessionTitleIfEmpty(newSession.ID, selected.Title)
	}

	short := selected.ID
	if len(short) > 12 {
		short = short[:12] + "..."
	}

	return &Response{
		Success: true,
		Content: fmt.Sprintf("Switched to native session: %s (%s, native=%s)", truncateListTitle(selected.Title), agentType, short),
	}, nil
}

func (h *CommandHandler) collectSwitchCandidates(userID string) ([]*session.Session, []NativeSession, map[string]string, string) {
	sessions := h.sessionManager.ListByUser(userID)

	activeSess, hasActive := h.sessionManager.GetActiveSession(userID)
	activeAgentType := ""
	activeNativeID := ""
	if hasActive {
		activeAgentType = activeSess.AgentType
		activeNativeID = nativeSessionIDFromContext(activeSess.AgentType, activeSess.Context)
		if strings.TrimSpace(activeNativeID) == "" && !strings.HasPrefix(activeSess.ID, pendingNativeSessionPrefix) {
			activeNativeID = strings.TrimSpace(activeSess.ID)
		}
	}

	managedNativeSessions := make([]*session.Session, 0, len(sessions))
	bridgeNativeIDs := make(map[string]bool)
	bridgeNativeToSessionID := make(map[string]string)
	for _, sess := range sessions {
		if nativeID := nativeSessionIDFromContext(sess.AgentType, sess.Context); nativeID != "" {
			if activeAgentType != "" && sess.AgentType != activeAgentType {
				continue
			}
			managedNativeSessions = append(managedNativeSessions, sess)
			bridgeNativeIDs[nativeID] = true
			bridgeNativeToSessionID[nativeID] = sess.ID
		}
	}

	sort.Slice(managedNativeSessions, func(i, j int) bool {
		if managedNativeSessions[i].UpdatedAt.Equal(managedNativeSessions[j].UpdatedAt) {
			return managedNativeSessions[i].ID < managedNativeSessions[j].ID
		}
		return managedNativeSessions[i].UpdatedAt.After(managedNativeSessions[j].UpdatedAt)
	})

	allNative := make([]NativeSession, 0, 20)
	if activeAgentType == "claude" || activeAgentType == "" {
		claudeProjectPath := currentClaudeNativeProjectPath(activeSess)
		if claudeNatives, err := ListNativeClaudeSessionsForProject(bridgeNativeIDs, claudeProjectPath); err == nil {
			allNative = append(allNative, claudeNatives...)
		}
	}
	if activeAgentType == "codex" || activeAgentType == "" {
		if codexNatives, err := ListNativeCodexSessions(bridgeNativeIDs); err == nil {
			allNative = append(allNative, codexNatives...)
		}
	}
	sort.Slice(allNative, func(i, j int) bool {
		return allNative[i].StartedAt.After(allNative[j].StartedAt)
	})

	return managedNativeSessions, allNative, bridgeNativeToSessionID, activeNativeID
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

func nativeListTitle(ns NativeSession) string {
	title := strings.TrimSpace(ns.Title)
	if title == "" {
		title = "未命名原生会话"
	}
	return truncateListTitle(title)
}

func truncateListTitle(title string) string {
	title = strings.Join(strings.Fields(strings.TrimSpace(title)), " ")
	if title == "" {
		return "未命名会话"
	}

	runes := []rune(title)
	if len(runes) > 30 {
		return string(runes[:30]) + "..."
	}

	return title
}

func formatListTime(ts time.Time) string {
	if ts.IsZero() {
		return "-"
	}
	return ts.Local().Format("01-02 15:04")
}

func escapeMarkdownTableCell(v string) string {
	v = strings.Join(strings.Fields(strings.TrimSpace(v)), " ")
	if v == "" {
		return "-"
	}
	return strings.ReplaceAll(v, "|", "\\|")
}

func nativeSessionIDFromContext(agentType string, ctx map[string]interface{}) string {
	if ctx == nil {
		return ""
	}
	key := agentSessionContextKey(agentType)
	if key == "" {
		return ""
	}
	val, ok := ctx[key].(string)
	if !ok {
		return ""
	}
	return val
}

func currentClaudeNativeProjectPath(activeSess *session.Session) string {
	if activeSess != nil {
		if workDir, ok := activeSess.Context["work_dir"].(string); ok {
			if normalized := normalizeNativeProjectPath(workDir); normalized != "" {
				return normalized
			}
		}
		if nativeID := nativeSessionIDFromContext("claude", activeSess.Context); nativeID != "" {
			if resolved := resolveClaudeProjectPathBySessionID(nativeID); resolved != "" {
				return resolved
			}
		}
	}
	return ""
}

type claudeRuntimeSession struct {
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd"`
}

func resolveClaudeProjectPathBySessionID(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ""
	}

	if runtimeCwd := resolveClaudeRuntimeSessionCwd(sessionID); runtimeCwd != "" {
		return runtimeCwd
	}

	return resolveClaudeProjectFromProjectsDir(sessionID)
}

func resolveClaudeRuntimeSessionCwd(sessionID string) string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	sessionsDir := filepath.Join(homeDir, ".claude", "sessions")
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return ""
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(sessionsDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		var runtime claudeRuntimeSession
		if err := json.Unmarshal(data, &runtime); err != nil {
			continue
		}
		if strings.TrimSpace(runtime.SessionID) != sessionID {
			continue
		}
		if normalized := normalizeNativeProjectPath(runtime.Cwd); normalized != "" {
			return normalized
		}
	}
	return ""
}

func resolveClaudeProjectFromProjectsDir(sessionID string) string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	projectsDir := filepath.Join(homeDir, ".claude", "projects")
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return ""
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		projectDir := filepath.Join(projectsDir, entry.Name())
		indexPath := filepath.Join(projectDir, "sessions-index.json")
		data, err := os.ReadFile(indexPath)
		if err == nil {
			var index claudeSessionIndex
			if json.Unmarshal(data, &index) == nil {
				for _, item := range index.Entries {
					if strings.TrimSpace(item.SessionID) != sessionID {
						continue
					}
					if normalized := normalizeNativeProjectPath(item.ProjectPath); normalized != "" {
						return normalized
					}
				}
			}
		}

		if _, err := os.Stat(filepath.Join(projectDir, sessionID+".jsonl")); err == nil {
			sessionFile := filepath.Join(projectDir, sessionID+".jsonl")
			fallbackProject := decodeProjectPath(entry.Name())
			if ns, ok := parseClaudeSessionFile(sessionFile, sessionID, fallbackProject, nil); ok {
				if normalized := normalizeNativeProjectPath(ns.Project); normalized != "" {
					return normalized
				}
			}
		}
	}
	return ""
}

func (h *CommandHandler) isAgentTypeAvailable(agentType string) bool {
	if h.agentManager == nil {
		return false
	}

	return h.agentManager.ResolveAgent(agentType) != nil
}

func (h *CommandHandler) ensureAgentTypeAvailable(agentType string) (bool, error) {
	if h.isAgentTypeAvailable(agentType) {
		return true, nil
	}
	if h.agentRepairer == nil {
		return false, nil
	}
	return h.agentRepairer.EnsureAvailable(agentType)
}

type configBackedAgentAvailabilityRepairer struct {
	agentManager *agent.Manager
	configPath   string
}

func newConfigBackedAgentAvailabilityRepairer(agentManager *agent.Manager, configPath string) *configBackedAgentAvailabilityRepairer {
	return &configBackedAgentAvailabilityRepairer{
		agentManager: agentManager,
		configPath:   strings.TrimSpace(configPath),
	}
}

func (r *configBackedAgentAvailabilityRepairer) EnsureAvailable(agentType string) (bool, error) {
	agentType = strings.ToLower(strings.TrimSpace(agentType))
	if agentType != "claude" && agentType != "codex" {
		return false, nil
	}
	if r.agentManager == nil {
		return false, nil
	}
	if r.agentManager.ResolveAgent(agentType) != nil {
		return true, nil
	}

	candidate := reparableAgent(agentType)
	if candidate == nil || !candidate.IsAvailable() {
		return false, nil
	}

	if err := setAgentEnabledInConfig(resolveConfigPathForRepair(r.configPath), agentType, true); err != nil {
		return false, err
	}

	r.agentManager.Register(candidate)
	return r.agentManager.ResolveAgent(agentType) != nil, nil
}

func reparableAgent(agentType string) agent.Agent {
	switch agentType {
	case "claude":
		return agent.NewClaudeCodeAgent()
	case "codex":
		return agent.NewCodeXAgent("")
	default:
		return nil
	}
}

func resolveConfigPathForRepair(path string) string {
	if strings.TrimSpace(path) != "" {
		return strings.TrimSpace(path)
	}
	if envPath := strings.TrimSpace(os.Getenv("CONFIG_PATH")); envPath != "" {
		return envPath
	}
	return filepath.Join("config", "config.toml")
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
func (h *CommandHandler) handleDir(userID, sessionID string, args []string) (*Response, error) {
	// 获取会话
	sess, exists := h.sessionManager.Get(sessionID)
	if !exists {
		return &Response{
			Success: false,
			Content: "Session not found. Use /new to create a new session.",
		}, nil
	}

	if len(args) > 0 {
		targetDir := strings.TrimSpace(strings.Join(args, " "))
		normalizedDir, err := normalizeWorkDirPath(targetDir)
		if err != nil {
			return &Response{
				Success: false,
				Content: fmt.Sprintf("Invalid working directory: %v", err),
			}, nil
		}
		h.sessionManager.UpdateSession(sess.ID, "work_dir", normalizedDir)
		return &Response{
			Success: true,
			Content: fmt.Sprintf("Working directory updated: %s", normalizedDir),
		}, nil
	}

	workDir := strings.TrimSpace(resolveSessionWorkDir(sess))
	if workDir == "" {
		return &Response{
			Success: false,
			Content: "No working directory set for this session.",
		}, nil
	}

	h.sessionManager.UpdateSession(sess.ID, "work_dir", workDir)

	return &Response{
		Success: true,
		Content: fmt.Sprintf("Current working directory: %s", workDir),
	}, nil
}

func resolveSessionWorkDir(sess *session.Session) string {
	if sess == nil {
		return ""
	}

	if workDir, ok := sess.Context["work_dir"].(string); ok {
		if normalized := strings.TrimSpace(workDir); normalized != "" {
			return normalized
		}
	}

	nativeID := strings.TrimSpace(nativeSessionIDFromContext(sess.AgentType, sess.Context))
	if nativeID == "" && !strings.HasPrefix(strings.TrimSpace(sess.ID), pendingNativeSessionPrefix) {
		nativeID = strings.TrimSpace(sess.ID)
	}

	switch strings.ToLower(strings.TrimSpace(sess.AgentType)) {
	case "claude":
		if resolved := resolveClaudeProjectPathBySessionID(nativeID); resolved != "" {
			return resolved
		}
	case "codex":
		if resolved := resolveCodexProjectPathByThreadID(nativeID); resolved != "" {
			return resolved
		}
	}

	if cwd, err := os.Getwd(); err == nil {
		if absCwd, err := filepath.Abs(cwd); err == nil {
			return strings.TrimSpace(absCwd)
		}
		return strings.TrimSpace(cwd)
	}

	return ""
}

func resolveCodexProjectPathByThreadID(threadID string) string {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return ""
	}

	natives, err := ListNativeCodexSessions(map[string]bool{})
	if err != nil {
		return ""
	}
	for _, ns := range natives {
		if strings.TrimSpace(ns.ID) != threadID {
			continue
		}
		if normalized := normalizeNativeProjectPath(ns.Project); normalized != "" {
			return normalized
		}
		break
	}
	return ""
}

func normalizeWorkDirPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("path is empty")
	}

	if strings.HasPrefix(path, "~") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if path == "~" {
			path = homeDir
		} else if strings.HasPrefix(path, "~/") {
			path = filepath.Join(homeDir, path[2:])
		}
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}

	stat, err := os.Stat(absPath)
	if err != nil {
		return "", err
	}
	if !stat.IsDir() {
		return "", fmt.Errorf("not a directory")
	}

	return absPath, nil
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
