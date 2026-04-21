package agent

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
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
	mu       sync.Mutex
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
	response, err := a.readCodexOutput(session, stdout)
	if err != nil {
		return "", err
	}

	// 等待命令完成
	if err := cmd.Wait(); err != nil {
		return "", fmt.Errorf("codex CLI failed: %w, stderr: %s", err, stderrBuf.String())
	}

	return response, nil
}

func (a *CodeXAgent) buildCommand(session *codexSession, input string) *exec.Cmd {
	threadID := session.CurrentSessionID()
	isResume := threadID != ""

	var args []string
	if isResume {
		// 恢复现有会话，用 - 从 stdin 读取 prompt
		args = []string{
			"-a", "never",
			"-m", a.model,
			"exec", "resume",
			"--skip-git-repo-check",
			"--json",
			threadID,
			"-",
		}
	} else {
		// 创建新会话，用 - 从 stdin 读取 prompt
		args = []string{
			"-a", "never",
			"-m", a.model,
			"exec",
			"--skip-git-repo-check",
			"--json",
			"-",
		}
	}

	cmd := exec.Command("codex", args...)
	cmd.Stdin = strings.NewReader(input)

	return cmd
}

func (a *CodeXAgent) readCodexOutput(session *codexSession, stdout io.ReadCloser) (string, error) {
	reader := bufio.NewReader(stdout)
	var responseParts []string

	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return "", fmt.Errorf("failed to read output: %w", err)
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

		// 处理事件
		eventType, _ := raw["type"].(string)
		switch eventType {
		case "session_meta":
			// 从 session_meta 中捕获 thread_id (payload.id)
			if payload, ok := raw["payload"].(map[string]any); ok {
				if tid, ok := payload["id"].(string); ok {
					session.threadID.Store(tid)
				}
			}

		case "event_msg":
			// 从 event_msg 中提取消息
			if payload, ok := raw["payload"].(map[string]any); ok {
				msgType, _ := payload["type"].(string)
				if msgType == "agent_message" {
					if text, ok := payload["message"].(string); ok && text != "" {
						responseParts = append(responseParts, text)
					}
				}
			}
		}
	}

	return strings.Join(responseParts, "\n"), nil
}

// extractItemText 从 Codex 输出中提取文本内容
func extractItemText(raw map[string]any, arrayField, elementType string) string {
	// 尝试从数组字段中提取
	if arr, ok := raw[arrayField].([]any); ok {
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
		if len(parts) > 0 {
			return strings.Join(parts, "\n")
		}
	}

	// 回退到顶层 text 字段
	if text, ok := raw["text"].(string); ok {
		return text
	}

	return ""
}

// IsAvailable 检查 Agent 是否可用
func (a *CodeXAgent) IsAvailable() bool {
	// 检查 codex 命令是否存在
	_, err := exec.LookPath("codex")
	return err == nil
}
