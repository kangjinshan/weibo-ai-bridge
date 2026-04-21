package agent

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync/atomic"
)

// CodeXAgent CodeX CLI Agent 实现
type CodeXAgent struct {
	name  string
	model string
}

// NewCodeXAgent 创建新的 CodeX Agent
func NewCodeXAgent(model string) *CodeXAgent {
	return &CodeXAgent{
		name:  "codex",
		model: model,
	}
}

// Name 返回 Agent 名称
func (a *CodeXAgent) Name() string {
	return a.name
}

// codexSession 用于管理 Codex 会话状态
type codexSession struct {
	threadID atomic.Value // 存储 Codex 返回的 thread_id
}

type codexOutput struct {
	response string
	errors   []string
}

// Execute 执行 AI 任务（带会话 ID 支持）
// sessionID 参数现在用于恢复会话，返回值中包含新的 session ID
func (a *CodeXAgent) Execute(sessionID string, input string) (string, error) {
	// 检查 codex CLI 是否可用
	if !a.IsAvailable() {
		return "", fmt.Errorf("codex CLI is not available")
	}

	// 创建临时会话状态
	session := &codexSession{}
	if sessionID != "" {
		session.threadID.Store(sessionID)
	}

	// 执行命令并获取响应
	response, err := a.executeCodex(session, input)
	if err != nil {
		return "", err
	}

	// 返回响应内容，如果成功获取到 thread_id，则附加在响应中
	// 格式: <response_content>\n\n__SESSION_ID__: <thread_id>
	newThreadID := session.CurrentSessionID()
	if newThreadID != "" {
		response = response + "\n\n__SESSION_ID__: " + newThreadID
	}

	return response, nil
}

// CurrentSessionID 获取当前会话 ID
func (cs *codexSession) CurrentSessionID() string {
	v, _ := cs.threadID.Load().(string)
	return v
}

func (a *CodeXAgent) executeCodex(session *codexSession, input string) (string, error) {
	cmd := a.buildCommand(session, input)

	// 获取 stdout pipe
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("failed to get stdout pipe: %w", err)
	}

	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	// 启动命令
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("failed to start codex CLI: %w", err)
	}

	// 读取并解析 JSON 输出
	output, err := a.readCodexOutput(session, stdout)
	if err != nil {
		return "", err
	}

	// 等待命令完成
	if err := cmd.Wait(); err != nil {
		details := joinNonEmpty(output.errors, cleanCodexStderr(stderrBuf.String()))
		if details == "" {
			return "", fmt.Errorf("codex CLI failed: %w", err)
		}
		return "", fmt.Errorf("codex CLI failed: %w, details: %s", err, details)
	}

	return output.response, nil
}

func (a *CodeXAgent) buildCommand(session *codexSession, input string) *exec.Cmd {
	threadID := session.CurrentSessionID()
	isResume := threadID != ""

	args := []string{"-a", "never"}
	if strings.TrimSpace(a.model) != "" {
		args = append(args, "-m", strings.TrimSpace(a.model))
	}

	if isResume {
		// 恢复现有会话，用 - 从 stdin 读取 prompt
		args = append(args,
			"exec", "resume",
			"--skip-git-repo-check",
			"--json",
			threadID,
			"-",
		)
	} else {
		// 创建新会话，用 - 从 stdin 读取 prompt
		args = append(args,
			"exec",
			"--skip-git-repo-check",
			"--json",
			"-",
		)
	}

	cmd := exec.Command("codex", args...)
	cmd.Stdin = strings.NewReader(input)

	return cmd
}

func (a *CodeXAgent) readCodexOutput(session *codexSession, stdout io.ReadCloser) (*codexOutput, error) {
	reader := bufio.NewReader(stdout)
	var responseParts []string
	var errorParts []string

	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("failed to read output: %w", err)
		}

		line = bytes.TrimRight(line, "\r\n")
		if len(line) == 0 {
			continue
		}

		// 解析 JSON 行
		var raw map[string]any
		if err := json.Unmarshal(line, &raw); err != nil {
			// 非 JSON 行，跳过
			continue
		}

		responseText, errorText := parseCodexEvent(session, raw)
		if responseText != "" {
			responseParts = append(responseParts, responseText)
		}
		if errorText != "" {
			errorParts = append(errorParts, errorText)
		}
	}

	return &codexOutput{
		response: strings.Join(responseParts, "\n"),
		errors:   uniqueNonEmpty(errorParts),
	}, nil
}

func parseCodexEvent(session *codexSession, raw map[string]any) (string, string) {
	eventType, _ := raw["type"].(string)

	switch eventType {
	case "thread.started":
		if tid, ok := raw["thread_id"].(string); ok && tid != "" {
			session.threadID.Store(tid)
		}

	case "session_meta":
		if payload, ok := raw["payload"].(map[string]any); ok {
			if tid, ok := payload["id"].(string); ok && tid != "" {
				session.threadID.Store(tid)
			}
		}

	case "item.completed":
		if item, ok := raw["item"].(map[string]any); ok {
			return extractMessageText(item), ""
		}

	case "event_msg":
		if payload, ok := raw["payload"].(map[string]any); ok {
			msgType, _ := payload["type"].(string)
			if msgType == "agent_message" {
				if text, ok := payload["message"].(string); ok && text != "" {
					return text, ""
				}
			}
		}

	case "error":
		if message, ok := raw["message"].(string); ok && message != "" {
			return "", message
		}

	case "turn.failed":
		if turnErr, ok := raw["error"].(map[string]any); ok {
			if message, ok := turnErr["message"].(string); ok && message != "" {
				return "", message
			}
		}
	}

	return "", ""
}

func extractMessageText(item map[string]any) string {
	itemType, _ := item["type"].(string)
	switch itemType {
	case "agent_message", "assistant_message", "message":
	default:
		return ""
	}

	if text, ok := item["text"].(string); ok && text != "" {
		return text
	}

	if text := extractItemText(item, "content", "output_text"); text != "" {
		return text
	}

	if text := extractItemText(item, "content", "text"); text != "" {
		return text
	}

	if text := extractItemText(item, "parts", "text"); text != "" {
		return text
	}

	if text, ok := item["message"].(string); ok && text != "" {
		return text
	}

	return ""
}

// extractItemText 从 Codex 输出中提取文本内容
func extractItemText(raw map[string]any, arrayField, elementType string) string {
	arr, ok := raw[arrayField].([]any)
	if !ok {
		return ""
	}

	var parts []string
	for _, elem := range arr {
		m, ok := elem.(map[string]any)
		if !ok {
			continue
		}
		if elementType != "" {
			if t, _ := m["type"].(string); t != elementType {
				continue
			}
		}
		if t, _ := m["text"].(string); t != "" {
			parts = append(parts, t)
		}
	}

	return strings.Join(parts, "\n")
}

func cleanCodexStderr(stderr string) string {
	var lines []string
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "Reading additional input from stdin..." {
			continue
		}
		lines = append(lines, line)
	}

	return strings.Join(lines, "\n")
}

func joinNonEmpty(parts []string, extra string) string {
	combined := append([]string{}, parts...)
	if extra != "" {
		combined = append(combined, extra)
	}

	return strings.Join(uniqueNonEmpty(combined), "\n")
}

func uniqueNonEmpty(parts []string) []string {
	seen := make(map[string]struct{}, len(parts))
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		result = append(result, part)
	}

	return result
}

// IsAvailable 检查 Agent 是否可用
func (a *CodeXAgent) IsAvailable() bool {
	// 检查 codex 命令是否存在
	_, err := exec.LookPath("codex")
	return err == nil
}
