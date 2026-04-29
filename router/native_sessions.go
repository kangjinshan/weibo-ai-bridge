package router

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
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

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		projectDir := filepath.Join(projectsDir, entry.Name())
		projectPath := decodeProjectPath(entry.Name())

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

			ns, ok := parseClaudeSessionFile(filepath.Join(projectDir, fe.Name()), sessionID, projectPath, bridgeNativeIDs)
			if !ok {
				continue
			}
			sessions = append(sessions, ns)
		}
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].StartedAt.After(sessions[j].StartedAt)
	})

	if len(sessions) > maxNativeSessions {
		sessions = sessions[:maxNativeSessions]
	}

	return sessions, nil
}

// parseClaudeSessionFile 解析单个 .jsonl 文件的首行，提取会话元数据
func parseClaudeSessionFile(filePath, sessionID, projectPath string, bridgeNativeIDs map[string]bool) (NativeSession, bool) {
	f, err := os.Open(filePath)
	if err != nil {
		return NativeSession{}, false
	}
	defer f.Close()

	// 只读取前 4KB
	buf := make([]byte, 4096)
	n, err := f.Read(buf)
	if err != nil || n == 0 {
		return NativeSession{}, false
	}

	// 取第一行
	content := string(buf[:n])
	lineEnd := strings.Index(content, "\n")
	var firstLine string
	if lineEnd > 0 {
		firstLine = content[:lineEnd]
	} else {
		firstLine = content
	}

	var op claudeQueueOp
	if err := json.Unmarshal([]byte(firstLine), &op); err != nil {
		return NativeSession{}, false
	}

	if op.Type != "queue-operation" || op.SessionID == "" {
		return NativeSession{}, false
	}

	// 解析时间戳
	startedAt, err := time.Parse(time.RFC3339Nano, op.Timestamp)
	if err != nil {
		startedAt = time.Time{}
	}

	// 截断标题
	title := strings.Join(strings.Fields(op.Content), " ")
	if runes := []rune(title); len(runes) > 40 {
		title = string(runes[:40]) + "..."
	}

	return NativeSession{
		ID:        op.SessionID,
		AgentType: "claude",
		Project:   projectPath,
		Title:     title,
		StartedAt: startedAt,
		InBridge:  bridgeNativeIDs[op.SessionID],
	}, true
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
	sessionsDir := filepath.Join(codexHome, "sessions")

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
		sessions = append(sessions, ns)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("cannot walk codex sessions directory: %w", err)
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].StartedAt.After(sessions[j].StartedAt)
	})

	if len(sessions) > maxNativeSessions {
		sessions = sessions[:maxNativeSessions]
	}

	return sessions, nil
}

// parseCodexSessionFile 解析单个 Codex .jsonl 文件，提取会话元数据
func parseCodexSessionFile(filePath string, bridgeNativeIDs map[string]bool) (NativeSession, bool) {
	f, err := os.Open(filePath)
	if err != nil {
		return NativeSession{}, false
	}
	defer f.Close()

	// 读取前 16KB，需要多读一些来找到首条用户消息
	buf := make([]byte, 16*1024)
	n, err := f.Read(buf)
	if err != nil || n == 0 {
		return NativeSession{}, false
	}

	content := string(buf[:n])
	lines := strings.Split(content, "\n")

	var meta codexSessionMeta
	title := ""
	var startedAt time.Time

	for _, line := range lines {
		line = strings.TrimSpace(line)
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
						title = strings.Join(strings.Fields(c.Text), " ")
						if runes := []rune(title); len(runes) > 40 {
							title = string(runes[:40]) + "..."
						}
						break
					}
				}
				if title != "" {
					break
				}
			}
		}
	}

	if meta.Payload.ID == "" {
		return NativeSession{}, false
	}

	return NativeSession{
		ID:        meta.Payload.ID,
		AgentType: "codex",
		Project:   meta.Payload.Cwd,
		Title:     title,
		StartedAt: startedAt,
		InBridge:  bridgeNativeIDs[meta.Payload.ID],
	}, true
}
