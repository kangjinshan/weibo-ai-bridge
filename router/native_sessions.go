package router

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// NativeSession 代表一个原生 Claude/Codex 会话
type NativeSession struct {
	ID        string    // session UUID / thread ID
	AgentType string    // "claude" or "codex"
	Project   string    // 解码后的工作目录
	Title     string    // 首条用户消息（截断）
	StartedAt time.Time // 创建时间
	InBridge  bool      // 是否已被 bridge 管理
}

type codexSessionIndexEntry struct {
	ID        string `json:"id"`
	Thread    string `json:"thread_name"`
	UpdatedAt string `json:"updated_at"`
}

type codexThreadRecord struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Cwd       string `json:"cwd"`
	UpdatedAt int64  `json:"updated_at"`
}

type claudeSessionIndex struct {
	Entries []claudeSessionIndexEntry `json:"entries"`
}

type claudeSessionIndexEntry struct {
	SessionID   string `json:"sessionId"`
	FileMtime   int64  `json:"fileMtime"`
	FirstPrompt string `json:"firstPrompt"`
	Summary     string `json:"summary"`
	Created     string `json:"created"`
	Modified    string `json:"modified"`
	ProjectPath string `json:"projectPath"`
}

// claudeQueueOp 是 Claude Code .jsonl 文件首行的 JSON 结构
type claudeQueueOp struct {
	Type      string `json:"type"`
	Operation string `json:"operation"`
	Timestamp string `json:"timestamp"`
	SessionID string `json:"sessionId"`
	Content   string `json:"content"`
}

const maxNativeSessions = 20

// ListNativeClaudeSessions 扫描 ~/.claude/projects/ 目录，列出原生 Claude 会话
func ListNativeClaudeSessions(bridgeNativeIDs map[string]bool) ([]NativeSession, error) {
	return listNativeClaudeSessions(bridgeNativeIDs, "")
}

// ListNativeClaudeSessionsForProject 扫描并仅返回指定项目目录的 Claude 原生会话
func ListNativeClaudeSessionsForProject(bridgeNativeIDs map[string]bool, projectPath string) ([]NativeSession, error) {
	return listNativeClaudeSessions(bridgeNativeIDs, projectPath)
}

func listNativeClaudeSessions(bridgeNativeIDs map[string]bool, projectPath string) ([]NativeSession, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("cannot determine home directory: %w", err)
	}

	projectsDir := filepath.Join(homeDir, ".claude", "projects")
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("cannot read projects directory: %w", err)
	}

	var sessions []NativeSession
	projectFilter := normalizeNativeProjectPath(projectPath)

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		projectDir := filepath.Join(projectsDir, entry.Name())
		projectPath := decodeProjectPath(entry.Name())
		indexedIDs := make(map[string]struct{})

		indexSessions, idsFromIndex := listClaudeSessionsFromIndex(projectDir, projectPath, bridgeNativeIDs)
		if len(indexSessions) > 0 {
			indexedIDs = idsFromIndex
			for _, ns := range indexSessions {
				if !matchesNativeProjectFilter(ns.Project, projectFilter) {
					continue
				}
				sessions = append(sessions, ns)
			}
		}

		fileEntries, err := os.ReadDir(projectDir)
		if err != nil {
			continue
		}

		for _, fe := range fileEntries {
			if fe.IsDir() {
				continue
			}
			if filepath.Ext(fe.Name()) != ".jsonl" {
				continue
			}

			sessionID := strings.TrimSuffix(fe.Name(), ".jsonl")
			if !isValidUUID(sessionID) {
				continue
			}
			if _, exists := indexedIDs[sessionID]; exists {
				continue
			}

			ns, ok := parseClaudeSessionFile(filepath.Join(projectDir, fe.Name()), sessionID, projectPath, bridgeNativeIDs)
			if !ok {
				continue
			}
			if !matchesNativeProjectFilter(ns.Project, projectFilter) {
				continue
			}
			sessions = append(sessions, ns)
		}
	}

	// 补充 history.jsonl 中记录的会话（覆盖无 .jsonl 文件的新版会话）
	historySessions := listClaudeSessionsFromHistory(homeDir, bridgeNativeIDs)
	sessions = append(sessions, historySessions...)

	sessions = dedupeNativeSessionsByID(sessions)
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].StartedAt.After(sessions[j].StartedAt)
	})

	if len(sessions) > maxNativeSessions {
		sessions = sessions[:maxNativeSessions]
	}

	return sessions, nil
}

// claudeHistoryEntry 是 ~/.claude/history.jsonl 中每行的 JSON 结构
type claudeHistoryEntry struct {
	Display   string `json:"display"`
	Timestamp int64  `json:"timestamp"`
	Project   string `json:"project"`
	SessionID string `json:"sessionId"`
}

// listClaudeSessionsFromHistory 从 ~/.claude/history.jsonl 提取会话元数据
// 用于补充无 .jsonl 文件的新版 Claude Code 会话
func listClaudeSessionsFromHistory(homeDir string, bridgeNativeIDs map[string]bool) []NativeSession {
	historyPath := filepath.Join(homeDir, ".claude", "history.jsonl")
	f, err := os.Open(historyPath)
	if err != nil {
		return nil
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	type sessionInfo struct {
		project   string
		lastTitle string
		startedAt time.Time
		lastAt    time.Time
	}
	sessions := make(map[string]*sessionInfo)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var entry claudeHistoryEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}

		sessionID := strings.TrimSpace(entry.SessionID)
		if !isValidUUID(sessionID) {
			continue
		}

		display := strings.TrimSpace(entry.Display)
		project := strings.TrimSpace(entry.Project)
		ts := time.UnixMilli(entry.Timestamp)

		info, exists := sessions[sessionID]
		if !exists {
			info = &sessionInfo{
				project:   project,
				startedAt: ts,
				lastAt:    ts,
			}
			sessions[sessionID] = info
		} else {
			if ts.After(info.lastAt) {
				info.lastAt = ts
			}
		}

		// 记录最后一条非命令消息，用作标题（与 resume 行为一致）
		if display != "" && !strings.HasPrefix(display, "/") && !isCommandWord(display) {
			info.lastTitle = display
		}
	}

	result := make([]NativeSession, 0, len(sessions))
	for sid, info := range sessions {
		// 跳过只有斜杠命令、没有真实用户输入的会话
		if info.lastTitle == "" {
			continue
		}
		result = append(result, NativeSession{
			ID:        sid,
			AgentType: "claude",
			Project:   info.project,
			Title:     ensureNativeTitle(info.lastTitle, info.project, sid),
			StartedAt: info.lastAt,
			InBridge:  bridgeNativeIDs[sid],
		})
	}
	return result
}


// isCommandWord 判断是否为短命令式词汇（非真实对话内容）
func isCommandWord(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "exit", "quit", "new", "help", "status", "list", "clear", "cancel", "done", "yes", "no", "ok", "y", "n":
		return true
	}
	return false
}

func normalizeNativeProjectPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}

	cleaned := filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(cleaned); err == nil {
		cleaned = filepath.Clean(resolved)
	}
	return cleaned
}

func matchesNativeProjectFilter(sessionProjectPath, projectFilter string) bool {
	if projectFilter == "" {
		return true
	}
	normalized := normalizeNativeProjectPath(sessionProjectPath)
	if normalized == "" {
		return false
	}
	return normalized == projectFilter
}

func listClaudeSessionsFromIndex(projectDir, fallbackProjectPath string, bridgeNativeIDs map[string]bool) ([]NativeSession, map[string]struct{}) {
	indexPath := filepath.Join(projectDir, "sessions-index.json")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		return nil, map[string]struct{}{}
	}

	var index claudeSessionIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, map[string]struct{}{}
	}

	sessions := make([]NativeSession, 0, len(index.Entries))
	indexedIDs := make(map[string]struct{}, len(index.Entries))
	for _, item := range index.Entries {
		sessionID := strings.TrimSpace(item.SessionID)
		if !isValidUUID(sessionID) {
			continue
		}

		projectPath := strings.TrimSpace(item.ProjectPath)
		if projectPath == "" {
			projectPath = fallbackProjectPath
		}

		title := strings.TrimSpace(item.Summary)
		if title == "" {
			title = strings.TrimSpace(item.FirstPrompt)
		}

		startedAt := claudeIndexTimestamp(item)
		sessions = append(sessions, NativeSession{
			ID:        sessionID,
			AgentType: "claude",
			Project:   projectPath,
			Title:     ensureNativeTitle(title, projectPath, sessionID),
			StartedAt: startedAt,
			InBridge:  bridgeNativeIDs[sessionID],
		})
		indexedIDs[sessionID] = struct{}{}
	}

	return sessions, indexedIDs
}

func claudeIndexTimestamp(entry claudeSessionIndexEntry) time.Time {
	if ts, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(entry.Modified)); err == nil {
		return ts
	}
	if ts, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(entry.Created)); err == nil {
		return ts
	}
	if entry.FileMtime > 0 {
		return time.UnixMilli(entry.FileMtime)
	}
	return time.Time{}
}

// parseClaudeSessionFile 解析单个 .jsonl 文件，提取会话元数据
// parseClaudeSessionFile 解析单个 .jsonl 文件，提取会话元数据
// 标题优先级与 Claude Code resume 一致：customTitle > aiTitle > summary > lastPrompt > content
// 过滤规则与 Claude Code resume 一致：isSidechain、sessionKind(daemon/bg)
func parseClaudeSessionFile(filePath, sessionID, projectPath string, bridgeNativeIDs map[string]bool) (NativeSession, bool) {
	f, err := os.Open(filePath)
	if err != nil {
		return NativeSession{}, false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	var (
		startedAt      time.Time
		lastPrompt     string
		customTitle    string
		aiTitle        string
		summary        string
		contentTitle   string
		firstNonEmpty  bool
		foundFirstOp   bool
		firstSessionID string
		resolvedCwd    string
		entrypoint     string
		isSidechain    bool
		sessionKind    string
	)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		if resolvedCwd == "" {
			var cwdPayload struct {
				SessionID string `json:"sessionId"`
				Cwd       string `json:"cwd"`
			}
			if err := json.Unmarshal([]byte(line), &cwdPayload); err == nil {
				if strings.TrimSpace(cwdPayload.Cwd) != "" {
					if strings.TrimSpace(cwdPayload.SessionID) == "" || strings.TrimSpace(cwdPayload.SessionID) == sessionID {
						resolvedCwd = normalizeNativeProjectPath(cwdPayload.Cwd)
					}
				}
			}
		}

		// 读取 Claude Code 写入的结构化元数据事件
		var meta struct {
			Type        string `json:"type"`
			CustomTitle string `json:"customTitle"`
			AiTitle     string `json:"aiTitle"`
			Summary     string `json:"summary"`
			LastPrompt  string `json:"lastPrompt"`
			SessionID   string `json:"sessionId"`
			Entrypoint  string `json:"entrypoint"`
			IsSidechain bool   `json:"isSidechain"`
			SessionKind string `json:"sessionKind"`
		}
		if err := json.Unmarshal([]byte(line), &meta); err == nil {
			sid := strings.TrimSpace(meta.SessionID)
			if sid != "" && sid != sessionID {
				goto parseQueueOp
			}

			// 收集过滤属性
			if ep := strings.TrimSpace(meta.Entrypoint); ep != "" && entrypoint == "" {
				entrypoint = ep
			}
			if meta.IsSidechain {
				isSidechain = true
			}
			if sk := strings.TrimSpace(meta.SessionKind); sk != "" && sessionKind == "" {
				sessionKind = sk
			}

			switch meta.Type {
			case "custom-title":
				if t := strings.TrimSpace(meta.CustomTitle); t != "" {
					customTitle = t
				}
				continue
			case "ai-title":
				if t := strings.TrimSpace(meta.AiTitle); t != "" {
					aiTitle = t
				}
				continue
			case "summary":
				if t := strings.TrimSpace(meta.Summary); t != "" {
					summary = t
				}
				continue
			case "last-prompt":
				if t := normalizeNativeTitle(meta.LastPrompt); t != "" {
					lastPrompt = t
				}
				continue
			}
		}

	parseQueueOp:
		var op claudeQueueOp
		if err := json.Unmarshal([]byte(line), &op); err != nil {
			if !firstNonEmpty {
				return NativeSession{}, false
			}
			continue
		}

		if !firstNonEmpty {
			firstNonEmpty = true
			if op.Type != "queue-operation" || strings.TrimSpace(op.SessionID) == "" {
				return NativeSession{}, false
			}
		}

		if op.Type != "queue-operation" || strings.TrimSpace(op.SessionID) == "" {
			continue
		}
		if op.SessionID != sessionID {
			continue
		}

		if !foundFirstOp {
			foundFirstOp = true
			firstSessionID = op.SessionID
		}

		if ts, err := time.Parse(time.RFC3339Nano, op.Timestamp); err == nil {
			if startedAt.IsZero() || ts.After(startedAt) {
				startedAt = ts
			}
		}

		if normalized := normalizeNativeTitle(op.Content); normalized != "" {
			contentTitle = normalized
		}
	}
	if scanner.Err() != nil {
		return NativeSession{}, false
	}
	if !foundFirstOp {
		return NativeSession{}, false
	}

	// 过滤规则与 Claude Code resume 一致
	if isSidechain {
		return NativeSession{}, false
	}
	if sessionKind == "bg" || sessionKind == "daemon" || sessionKind == "daemon-worker" {
		return NativeSession{}, false
	}

	// 标题优先级与 Claude Code resume 一致：customTitle > aiTitle > summary > lastPrompt > content
	title := firstNonEmptyString(customTitle, aiTitle, summary, lastPrompt, contentTitle)
	proj := firstNonEmptyString(resolvedCwd, projectPath)

	return NativeSession{
		ID:        firstSessionID,
		AgentType: "claude",
		Project:   proj,
		Title:     ensureNativeTitle(title, proj, firstSessionID),
		StartedAt: startedAt,
		InBridge:  bridgeNativeIDs[firstSessionID],
	}, true
}



func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// decodeProjectPath 将 Claude 项目目录名解码为实际路径
// 例如: -home-ubuntu-workspace → /home/ubuntu/workspace
func decodeProjectPath(encoded string) string {
	if encoded == "" {
		return ""
	}

	parts := strings.Split(encoded, "-")
	var result string
	for _, part := range parts {
		if part == "" {
			continue
		}
		if result != "" {
			result += "/"
		}
		result += part
	}

	return "/" + result
}

// isValidUUID 检查字符串是否像 UUID 格式
func isValidUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// --- Codex 原生会话扫描 ---

// codexSessionMeta 是 Codex .jsonl 文件首行 session_meta 的 JSON 结构
type codexSessionMeta struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Payload   struct {
		ID         string `json:"id"`
		Source     string `json:"source"`
		Originator string `json:"originator"`
		Cwd        string `json:"cwd"`
	} `json:"payload"`
}

// codexResponseItem 是 Codex .jsonl 中 response_item 的 JSON 结构
type codexResponseItem struct {
	Type    string `json:"type"`
	Payload struct {
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"payload"`
}

// ListNativeCodexSessions 扫描 ~/.codex/sessions/ 目录，列出原生 Codex 会话
func ListNativeCodexSessions(bridgeNativeIDs map[string]bool) ([]NativeSession, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("cannot determine home directory: %w", err)
	}

	codexHome := os.Getenv("CODEX_HOME")
	if strings.TrimSpace(codexHome) == "" {
		codexHome = filepath.Join(homeDir, ".codex")
	}

	// 优先读取 Codex 客户端自身的 thread 标题数据源，保证与客户端一致
	if nativeFromDB, err := listCodexSessionsFromStateDB(codexHome, bridgeNativeIDs); err == nil && len(nativeFromDB) > 0 {
		return nativeFromDB, nil
	}

	sessionsDir := filepath.Join(codexHome, "sessions")
	threadNames, threadUpdatedAt := loadCodexSessionIndex(codexHome)

	// Codex sessions 存储在 ~/.codex/sessions/YYYY/MM/DD/ 嵌套目录中，需要递归遍历
	var sessions []NativeSession

	err = filepath.WalkDir(sessionsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() || filepath.Ext(d.Name()) != ".jsonl" {
			return nil
		}

		ns, ok := parseCodexSessionFile(path, bridgeNativeIDs)
		if !ok {
			return nil
		}
		if title, exists := threadNames[ns.ID]; exists {
			if normalized := normalizeNativeTitle(title); normalized != "" {
				ns.Title = normalized
			}
		}
		if updatedAt, exists := threadUpdatedAt[ns.ID]; exists && !updatedAt.IsZero() {
			ns.StartedAt = updatedAt
		}
		sessions = append(sessions, ns)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("cannot walk codex sessions directory: %w", err)
	}

	sessions = dedupeNativeSessionsByID(sessions)
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].StartedAt.After(sessions[j].StartedAt)
	})

	if len(sessions) > maxNativeSessions {
		sessions = sessions[:maxNativeSessions]
	}

	return sessions, nil
}

func listCodexSessionsFromStateDB(codexHome string, bridgeNativeIDs map[string]bool) ([]NativeSession, error) {
	dbPath := filepath.Join(codexHome, "state_5.sqlite")
	if _, err := os.Stat(dbPath); err != nil {
		return nil, nil
	}

	sqlitePath, err := exec.LookPath("sqlite3")
	if err != nil {
		return nil, nil
	}

	// 注意：这里读取 threads.title，和 Codex 客户端标题同源。
	query := `SELECT json_object('id', id, 'title', title, 'cwd', cwd, 'updated_at', updated_at)
FROM threads
WHERE archived = 0
ORDER BY updated_at DESC
LIMIT 500;`

	out, err := exec.Command(sqlitePath, dbPath, query).Output()
	if err != nil {
		return nil, err
	}

	records := parseCodexThreadRecordsJSONL(out)
	if len(records) == 0 {
		return nil, nil
	}

	natives := make([]NativeSession, 0, len(records))
	for _, rec := range records {
		id := strings.TrimSpace(rec.ID)
		if id == "" {
			continue
		}

		title := strings.TrimSpace(rec.Title)
		if title == "" {
			title = ensureNativeTitle("", rec.Cwd, id)
		}

		ts := time.Unix(rec.UpdatedAt, 0)
		natives = append(natives, NativeSession{
			ID:        id,
			AgentType: "codex",
			Project:   strings.TrimSpace(rec.Cwd),
			Title:     title,
			StartedAt: ts,
			InBridge:  bridgeNativeIDs[id],
		})
	}

	natives = dedupeNativeSessionsByID(natives)
	sort.Slice(natives, func(i, j int) bool {
		return natives[i].StartedAt.After(natives[j].StartedAt)
	})
	if len(natives) > maxNativeSessions {
		natives = natives[:maxNativeSessions]
	}

	return natives, nil
}

func parseCodexThreadRecordsJSONL(data []byte) []codexThreadRecord {
	lines := strings.Split(string(data), "\n")
	records := make([]codexThreadRecord, 0, len(lines))

	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}

		var rec codexThreadRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if strings.TrimSpace(rec.ID) == "" {
			continue
		}
		records = append(records, rec)
	}

	return records
}

func dedupeNativeSessionsByID(sessions []NativeSession) []NativeSession {
	if len(sessions) <= 1 {
		return sessions
	}

	sort.SliceStable(sessions, func(i, j int) bool {
		return sessions[i].StartedAt.After(sessions[j].StartedAt)
	})

	seen := make(map[string]struct{}, len(sessions))
	deduped := make([]NativeSession, 0, len(sessions))
	for _, s := range sessions {
		if _, exists := seen[s.ID]; exists {
			continue
		}
		seen[s.ID] = struct{}{}
		deduped = append(deduped, s)
	}

	return deduped
}

func loadCodexSessionIndex(codexHome string) (map[string]string, map[string]time.Time) {
	path := filepath.Join(codexHome, "session_index.jsonl")
	f, err := os.Open(path)
	if err != nil {
		return map[string]string{}, map[string]time.Time{}
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	titles := make(map[string]string)
	updatedAt := make(map[string]time.Time)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var entry codexSessionIndexEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if strings.TrimSpace(entry.ID) == "" {
			continue
		}
		if normalized := normalizeNativeTitle(entry.Thread); normalized != "" {
			titles[entry.ID] = normalized
		}
		if ts, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(entry.UpdatedAt)); err == nil {
			updatedAt[entry.ID] = ts
		}
	}

	return titles, updatedAt
}

// parseCodexSessionFile 解析单个 Codex .jsonl 文件，提取会话元数据
func parseCodexSessionFile(filePath string, bridgeNativeIDs map[string]bool) (NativeSession, bool) {
	f, err := os.Open(filePath)
	if err != nil {
		return NativeSession{}, false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 128*1024), 8*1024*1024)

	var meta codexSessionMeta
	title := ""
	var startedAt time.Time

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		// 解析 session_meta（首行）
		if meta.Type == "" {
			if err := json.Unmarshal([]byte(line), &meta); err != nil {
				return NativeSession{}, false
			}
			if meta.Type != "session_meta" || meta.Payload.ID == "" {
				return NativeSession{}, false
			}
			if meta.Timestamp != "" {
				startedAt, _ = time.Parse(time.RFC3339Nano, meta.Timestamp)
			}
			continue
		}

		// 解析首条用户消息作为标题
		if title == "" {
			var item codexResponseItem
			if err := json.Unmarshal([]byte(line), &item); err != nil {
				continue
			}
			if item.Type == "response_item" && item.Payload.Role == "user" {
				for _, c := range item.Payload.Content {
					if c.Type == "input_text" && strings.TrimSpace(c.Text) != "" {
						title = normalizeNativeTitle(c.Text)
						break
					}
				}
				if title != "" {
					break
				}
			}
		}
	}
	if scanner.Err() != nil {
		return NativeSession{}, false
	}

	if meta.Payload.ID == "" {
		return NativeSession{}, false
	}

	return NativeSession{
		ID:        meta.Payload.ID,
		AgentType: "codex",
		Project:   meta.Payload.Cwd,
		Title:     ensureNativeTitle(title, meta.Payload.Cwd, meta.Payload.ID),
		StartedAt: startedAt,
		InBridge:  bridgeNativeIDs[meta.Payload.ID],
	}, true
}

func normalizeNativeTitle(raw string) string {
	title := strings.Join(strings.Fields(strings.TrimSpace(raw)), " ")
	if title == "" {
		return ""
	}

	runes := []rune(title)
	if len(runes) > 40 {
		return string(runes[:40]) + "..."
	}

	return title
}

func fallbackNativeTitle(projectPath, sessionID string) string {
	if base := strings.TrimSpace(filepath.Base(strings.TrimSpace(projectPath))); base != "" && base != "." && base != "/" {
		return "会话@" + base
	}
	if strings.TrimSpace(sessionID) != "" {
		if len(sessionID) > 8 {
			return "会话-" + sessionID[:8]
		}
		return "会话-" + sessionID
	}
	return "未命名会话"
}

func ensureNativeTitle(title, projectPath, sessionID string) string {
	normalized := normalizeNativeTitle(title)
	if normalized != "" {
		return normalized
	}
	return fallbackNativeTitle(projectPath, sessionID)
}
