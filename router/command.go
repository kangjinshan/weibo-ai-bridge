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
	selfUpdater    selfUpdater
}

type agentAvailabilityRepairer interface {
	EnsureAvailable(agentType string) (bool, error)
}

type selfUpdater interface {
	Run(args []string) (selfUpdateResult, error)
}

type selfUpdateResult struct {
	Output           string
	RestartScheduled bool
	AlreadyUpToDate  bool
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

func (h *CommandHandler) SetSelfUpdater(updater selfUpdater) {
	h.selfUpdater = updater
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
	case "/hermes":
		return h.handleSwitch(msg.UserID, msg.SessionID, append([]string{"hermes"}, args...))
	case "/gemini":
		return h.handleSwitch(msg.UserID, msg.SessionID, append([]string{"gemini"}, args...))
	case "/model":
		return h.handleModel(msg.UserID, msg.SessionID, args)
	case "/dir":
		return h.handleDir(msg.UserID, msg.SessionID, args)
	case "/status":
		return h.handleStatus(msg.UserID, msg.SessionID)
	case "/super":
		return h.handleSuper(msg.UserID, msg.SessionID, args)
	case "/simple":
		return h.handleSimple(msg.UserID, msg.SessionID, args)
	case "/upgrade":
		return h.handleUpgrade(args)
	default:
		return &Response{
			Success: false,
			Content: fmt.Sprintf("Unknown command: %s. Use /help to see available commands.", command),
		}, nil
	}
}

// CommandMeta 描述一个 slash 命令的对外可见信息。
// 当前 platform/local 用它向 msghub 上报命令清单，供客户端做 `/`
// 触发的命令补全；其它路径不依赖这个类型。
type CommandMeta struct {
	Name        string
	Description string
	Args        []string
}

// ListCommands 返回 bridge 当前支持的 slash 命令清单。顺序固定为代码声明顺序，
// 与 `/help` 文案保持一致，方便客户端做 UI 排序。
func (h *CommandHandler) ListCommands() []CommandMeta {
	return []CommandMeta{
		{Name: "/help", Description: "显示帮助信息"},
		{Name: "/new", Description: "准备一个新的原生会话", Args: []string{"agent_type"}},
		{Name: "/list", Description: "查看当前用户的原生会话"},
		{Name: "/switch", Description: "切换当前活跃会话或 Agent", Args: []string{"index_or_agent"}},
		{Name: "/claude", Description: "切换到 Claude agent"},
		{Name: "/codex", Description: "切换到 Codex agent"},
		{Name: "/hermes", Description: "切换到 Hermes agent"},
		{Name: "/gemini", Description: "切换到 Gemini agent"},
		{Name: "/btw", Description: "向当前正在进行的交互会话插入一条补充消息", Args: []string{"content"}},
		{Name: "/listen", Description: "监听当前或指定编号的原生会话日志", Args: []string{"index"}},
		{Name: "/unlisten", Description: "停止当前监听"},
		{Name: "/model", Description: "显示当前使用的模型"},
		{Name: "/dir", Description: "显示或设置当前工作目录", Args: []string{"path"}},
		{Name: "/status", Description: "显示当前会话状态"},
		{Name: "/super", Description: "管理 Super 模式", Args: []string{"on_off_status"}},
		{Name: "/simple", Description: "管理简洁模式", Args: []string{"on_off_status"}},
		{Name: "/upgrade", Description: "从 GitHub 升级 bridge", Args: []string{"--ref"}},
	}
}

// handleHelp 处理帮助命令
func (h *CommandHandler) handleHelp() (*Response, error) {
	helpText := strings.Join([]string{
		"## 可用命令",
		"",
		"### 会话",
		"- `/new [claude|codex|hermes|gemini]`：准备一个新的原生会话，不传参数时沿用当前 Agent。",
		"- `/list`：查看当前用户的原生会话。",
		"- `/switch <编号>`：切换到 `/list` 中对应编号的会话。",
		"- `/switch <agent>`：切换当前会话的 Agent 类型。",
		"",
		"### Agent 快捷切换",
		"- `/claude`：等价于 `/switch claude`。",
		"- `/codex`：等价于 `/switch codex`。",
		"- `/hermes`：等价于 `/switch hermes`。",
		"- `/gemini`：等价于 `/switch gemini`。",
		"",
		"### 监听与注入",
		"- `/btw <内容>`：向当前正在进行的交互会话插入一条补充消息。",
		"- `/listen [编号]`：监听当前或 `/list` 编号对应的原生会话日志。",
		"- `/unlisten`：停止当前监听。",
		"",
		"### 工具",
		"- `/help`：显示这份帮助。",
		"- `/model`：显示当前使用的模型。",
		"- `/dir [path]`：显示或设置当前工作目录。",
		"- `/status`：显示当前会话状态。",
		"- `/super [on|off|status]`：管理 Super 模式，`on` 等价于 Allow All。",
		"- `/simple [on|off|status]`：管理简洁模式，不带参数时切换开关。",
		"- `/upgrade [--ref branch|tag]`：从 GitHub 升级 bridge，并在回复后延迟重启服务。",
	}, "\n")
	return &Response{
		Success: true,
		Content: helpText,
	}, nil
}

func (h *CommandHandler) handleUpgrade(args []string) (*Response, error) {
	updater := h.selfUpdater
	if updater == nil {
		updater = newShellSelfUpdater()
	}

	result, err := updater.Run(args)
	output := strings.TrimSpace(result.Output)
	if err != nil {
		content := fmt.Sprintf("升级失败: %v", err)
		if output != "" {
			content += "\n\n输出:\n" + output
		}
		return &Response{Success: false, Content: content}, nil
	}

	restartUncertain := isSelfUpdateRestartUncertain(output)
	content := "升级已完成。"
	if result.AlreadyUpToDate {
		content = "已经是最新版本，无需升级。"
	} else if restartUncertain {
		content = "已更新二进制，但未确认自动重启已可靠安排。请手动重启服务以加载新版本。"
	} else if result.RestartScheduled {
		content += " 已安排服务延迟重启，当前回复发出后再切换到新版本。"
	} else {
		content += " 未能安排自动重启，请手动重启服务以运行新版本。"
	}
	if output != "" {
		content += "\n\n输出:\n" + output
	}

	return &Response{Success: true, Content: content}, nil
}

func isSelfUpdateRestartUncertain(output string) bool {
	output = strings.ToLower(output)
	uncertainSignals := []string{
		"systemd-run 安排延迟重启失败",
		"退回到后台进程方式",
		"可能只完成停止",
		"无法以非 root 用户安排 system scope 重启",
	}
	for _, signal := range uncertainSignals {
		if strings.Contains(output, strings.ToLower(signal)) {
			return true
		}
	}
	return false
}

// handleNew 处理创建新会话命令
func (h *CommandHandler) handleNew(userID string, args []string) (*Response, error) {
	agentType := h.defaultNewSessionAgentType(userID)
	if len(args) > 0 {
		agentType = args[0]
	}
	agentType = strings.ToLower(strings.TrimSpace(agentType))

	if strings.TrimSpace(agentType) == "" {
		return &Response{
			Success: false,
			Content: "No agent available to create a new session.",
		}, nil
	}

	// 验证 agent 类型
	if !isSupportedAgentType(agentType) {
		return &Response{
			Success: false,
			Content: "Invalid agent type. Use claude, codex, hermes, or gemini.",
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
	h.sessionManager.UpdateSessionAgentAndContextAtomically(newSession.ID, agentType, func(ctx map[string]interface{}) bool {
		changed := false
		for _, key := range []string{"claude_session_id", "codex_session_id", "hermes_session_id", "gemini_session_id"} {
			if current, _ := ctx[key].(string); current != "" {
				ctx[key] = ""
				changed = true
				continue
			}
			if _, ok := ctx[key]; !ok {
				ctx[key] = ""
				changed = true
			}
		}
		return changed
	})

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

func isSupportedAgentType(agentType string) bool {
	switch strings.ToLower(strings.TrimSpace(agentType)) {
	case "claude", "codex", "hermes", "gemini":
		return true
	default:
		return false
	}
}

func (h *CommandHandler) handleList(userID string) (*Response, error) {
	managedNativeSessions, allNative, _, activeNativeID := h.collectSwitchCandidates(userID)

	type listRow struct {
		Number  string
		Title   string
		Project string
		When    time.Time
	}
	rows := make([]listRow, 0, len(allNative))

	if len(allNative) > 0 {
		for i, ns := range allNative {
			title := nativeListTitle(ns)
			if strings.TrimSpace(ns.ID) != "" && strings.TrimSpace(ns.ID) == strings.TrimSpace(activeNativeID) {
				title += "（当前）"
			}
			rows = append(rows, listRow{
				Number:  strconv.Itoa(i + 1),
				Title:   title,
				Project: shortProjectName(ns.Project),
				When:    ns.StartedAt,
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
			project := ""
			if workDir, ok := sess.ContextString("work_dir"); ok {
				project = shortProjectName(workDir)
			}
			rows = append(rows, listRow{
				Number:  strconv.Itoa(i + 1),
				Title:   title,
				Project: project,
				When:    sess.UpdatedAt,
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
		"| 编号 | 标题 | 目录 | 时间 |",
		"| --- | --- | --- | --- |",
	}
	for _, row := range rows {
		lines = append(lines, fmt.Sprintf(
			"| %s | %s | %s | %s |",
			escapeMarkdownTableCell(row.Number),
			escapeMarkdownTableCell(row.Title),
			escapeMarkdownTableCell(row.Project),
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
			Content: "Please specify a session number or agent type: /switch 1, /switch claude, /switch codex, /switch hermes, or /switch gemini",
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
	if !isSupportedAgentType(agentType) {
		return &Response{
			Success: false,
			Content: "Invalid agent type. Use claude, codex, hermes, or gemini.",
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
		activeNativeID = nativeSessionIDFromSession(activeSess.AgentType, activeSess)
		if strings.TrimSpace(activeNativeID) == "" && !strings.HasPrefix(activeSess.ID, pendingNativeSessionPrefix) {
			activeNativeID = strings.TrimSpace(activeSess.ID)
		}
	}

	managedNativeSessions := make([]*session.Session, 0, len(sessions))
	bridgeNativeIDs := make(map[string]bool)
	bridgeNativeToSessionID := make(map[string]string)
	for _, sess := range sessions {
		if nativeID := nativeSessionIDFromSession(sess.AgentType, sess); nativeID != "" {
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
		if claudeNatives, err := ListNativeClaudeSessions(bridgeNativeIDs); err == nil {
			allNative = append(allNative, claudeNatives...)
		}
	}
	if activeAgentType == "codex" || activeAgentType == "" {
		if codexNatives, err := ListNativeCodexSessions(bridgeNativeIDs); err == nil {
			allNative = append(allNative, codexNatives...)
		}
	}
	if activeAgentType == "hermes" || activeAgentType == "" {
		if hermesNatives, err := ListNativeHermesSessions(bridgeNativeIDs); err == nil {
			allNative = append(allNative, hermesNatives...)
		}
	}
	if activeAgentType == "gemini" || activeAgentType == "" {
		if geminiNatives, err := ListNativeGeminiSessions(bridgeNativeIDs); err == nil {
			allNative = append(allNative, geminiNatives...)
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

func shortProjectName(projectPath string) string {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return ""
	}
	base := filepath.Base(projectPath)
	if base == "" || base == "." || base == "/" {
		return ""
	}
	return base
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

func nativeSessionIDFromSession(agentType string, sess *session.Session) string {
	if sess == nil {
		return ""
	}
	key := agentSessionContextKey(agentType)
	if key == "" {
		return ""
	}
	val, ok := sess.ContextString(key)
	if !ok {
		return ""
	}
	return val
}

func currentClaudeNativeProjectPath(activeSess *session.Session) string {
	if activeSess != nil {
		if workDir, ok := activeSess.ContextString("work_dir"); ok {
			if normalized := normalizeNativeProjectPath(workDir); normalized != "" {
				return normalized
			}
		}
		if nativeID := nativeSessionIDFromSession("claude", activeSess); nativeID != "" {
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
	if !isSupportedAgentType(agentType) {
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
	case "hermes":
		return agent.NewHermesAgent("", "", "")
	case "gemini":
		return agent.NewGeminiAgent("")
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

func (h *CommandHandler) handleSuper(userID, sessionID string, args []string) (*Response, error) {
	sessID := strings.TrimSpace(sessionID)
	if sessID == "" {
		if activeID := strings.TrimSpace(h.sessionManager.GetActiveSessionID(userID)); activeID != "" {
			sessID = activeID
		}
	}

	sess, exists := h.sessionManager.Get(sessID)
	if !exists {
		return &Response{
			Success: false,
			Content: "Session not found. Use /new to create a new session.",
		}, nil
	}

	showStatus := func() *Response {
		claudeFeedback, claudeReady := superFeedbackReadyForAgent(sess, "claude")
		codexFeedback, codexReady := superFeedbackReadyForAgent(sess, "codex")
		if !isSuperModeEnabled(sess) {
			return &Response{
				Success: true,
				Content: "Super mode: OFF",
			}
		}

		return &Response{
			Success: true,
			Content: fmt.Sprintf(
				"Super mode: ON (Allow All)\nAuto approval: %s\nPending feedback (claude): %s\nPending feedback (codex): %s",
				boolToOnOff(isSuperAutoApproveEnabled(sess)),
				boolToYesNo(claudeReady && strings.TrimSpace(claudeFeedback) != ""),
				boolToYesNo(codexReady && strings.TrimSpace(codexFeedback) != ""),
			),
		}
	}

	if len(args) == 0 {
		return showStatus(), nil
	}

	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "status":
		return showStatus(), nil
	case "on":
		setSuperMode(h.sessionManager, sess.ID, true)
		return &Response{
			Success: true,
			Content: "Super mode enabled. Allow All is ON for this session.",
		}, nil
	case "off":
		setSuperMode(h.sessionManager, sess.ID, false)
		return &Response{
			Success: true,
			Content: "Super mode disabled. Pending Super feedback cleared.",
		}, nil
	default:
		return &Response{
			Success: false,
			Content: "Invalid super command. Use /super on, /super off, or /super status.",
		}, nil
	}
}

func (h *CommandHandler) handleSimple(userID, sessionID string, args []string) (*Response, error) {
	sessID := strings.TrimSpace(sessionID)
	if sessID == "" {
		if activeID := strings.TrimSpace(h.sessionManager.GetActiveSessionID(userID)); activeID != "" {
			sessID = activeID
		}
	}

	sess, exists := h.sessionManager.Get(sessID)
	if !exists {
		return &Response{
			Success: false,
			Content: "Session not found. Use /new to create a new session.",
		}, nil
	}

	showStatus := func() *Response {
		return &Response{
			Success: true,
			Content: fmt.Sprintf("Simple mode: %s", boolToOnOff(isSimpleModeEnabled(sess))),
		}
	}
	setEnabled := func(enabled bool) *Response {
		setSimpleMode(h.sessionManager, sess.ID, enabled)
		if enabled {
			return &Response{
				Success: true,
				Content: "Simple mode enabled. Only approval prompts, choice prompts, and final replies will be sent.",
			}
		}
		return &Response{
			Success: true,
			Content: "Simple mode disabled.",
		}
	}

	if len(args) == 0 {
		return setEnabled(!isSimpleModeEnabled(sess)), nil
	}

	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "status":
		return showStatus(), nil
	case "on":
		return setEnabled(true), nil
	case "off":
		return setEnabled(false), nil
	default:
		return &Response{
			Success: false,
			Content: "Invalid simple command. Use /simple on, /simple off, or /simple status.",
		}, nil
	}
}

func boolToOnOff(enabled bool) string {
	if enabled {
		return "ON"
	}
	return "OFF"
}

func boolToYesNo(enabled bool) string {
	if enabled {
		return "yes"
	}
	return "no"
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

	if workDir, ok := sess.ContextString("work_dir"); ok {
		if normalized := strings.TrimSpace(workDir); normalized != "" {
			return normalized
		}
	}

	nativeID := strings.TrimSpace(nativeSessionIDFromSession(sess.AgentType, sess))
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
	case "hermes":
		if resolved := resolveHermesProjectPathBySessionID(nativeID); resolved != "" {
			return resolved
		}
	case "gemini":
		if resolved := resolveGeminiProjectPathBySessionID(nativeID); resolved != "" {
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

func resolveGeminiProjectPathBySessionID(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ""
	}

	natives, err := ListNativeGeminiSessions(map[string]bool{})
	if err != nil {
		return ""
	}
	for _, ns := range natives {
		if strings.TrimSpace(ns.ID) != sessionID {
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
		path = expandHomePath(path, homeDir)
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

func expandHomePath(path, homeDir string) string {
	path = strings.TrimSpace(path)
	homeDir = strings.TrimSpace(homeDir)
	if path == "~" {
		if homeDir != "" {
			return homeDir
		}
		return path
	}
	if homeDir == "" {
		return path
	}
	for _, prefix := range []string{"~/", `~\`} {
		if strings.HasPrefix(path, prefix) {
			return joinHomePath(homeDir, strings.TrimPrefix(path, prefix))
		}
	}
	return path
}

func joinHomePath(homeDir, rest string) string {
	rest = strings.TrimLeft(rest, `/\`)
	if strings.Contains(homeDir, `\`) && !strings.Contains(homeDir, "/") {
		return strings.TrimRight(homeDir, `/\`) + `\` + strings.ReplaceAll(rest, "/", `\`)
	}
	return filepath.Join(homeDir, rest)
}

// handleStatus 处理显示状态命令
func (h *CommandHandler) handleStatus(userID, sessionID string) (*Response, error) {
	sessID := strings.TrimSpace(sessionID)
	if sessID == "" {
		if activeID := strings.TrimSpace(h.sessionManager.GetActiveSessionID(userID)); activeID != "" {
			sessID = activeID
		}
	}

	// 获取会话
	sess, exists := h.sessionManager.Get(sessID)
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
	if isSuperModeEnabled(sess) {
		statusText += "\nSuper mode: ON (Allow All)"
	} else {
		statusText += "\nSuper mode: OFF"
	}
	statusText += fmt.Sprintf("\nSimple mode: %s", boolToOnOff(isSimpleModeEnabled(sess)))

	return &Response{
		Success: true,
		Content: statusText,
	}, nil
}
