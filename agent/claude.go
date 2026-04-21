package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// ClaudeCodeAgent Claude Code CLI Agent 实现
type ClaudeCodeAgent struct {
	name string
}

type claudePrintResult struct {
	Result    string `json:"result"`
	SessionID string `json:"session_id"`
	IsError   bool   `json:"is_error"`
}

// NewClaudeCodeAgent 创建新的 Claude Code Agent
func NewClaudeCodeAgent() *ClaudeCodeAgent {
	return &ClaudeCodeAgent{
		name: "claude-code",
	}
}

// Name 返回 Agent 名称
func (a *ClaudeCodeAgent) Name() string {
	return a.name
}

// Execute 执行 AI 任务
func (a *ClaudeCodeAgent) Execute(sessionID string, input string) (string, error) {
	command, err := resolveClaudeCommand()
	if err != nil {
		return "", fmt.Errorf("claude CLI is not available")
	}

	cmd := exec.Command(command, a.buildArgs(sessionID, input)...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	result, parseErr := parseClaudePrintOutput(stdout.String())
	if parseErr != nil {
		if runErr != nil {
			return "", fmt.Errorf("failed to execute claude CLI: %w, stderr: %s", runErr, strings.TrimSpace(stderr.String()))
		}

		text := strings.TrimSpace(stdout.String())
		if text == "" {
			return "", fmt.Errorf("empty response from claude CLI")
		}
		return text, nil
	}

	if runErr != nil || result.IsError {
		details := strings.TrimSpace(result.Result)
		if details == "" {
			details = strings.TrimSpace(stderr.String())
		}
		if details == "" {
			details = "unknown claude CLI error"
		}
		if runErr != nil {
			return "", fmt.Errorf("failed to execute claude CLI: %w, stderr: %s", runErr, details)
		}
		return "", fmt.Errorf("claude CLI returned error: %s", details)
	}

	response := strings.TrimSpace(result.Result)
	if response == "" {
		return "", fmt.Errorf("empty response from claude CLI")
	}
	if strings.TrimSpace(result.SessionID) != "" {
		response += "\n\n__SESSION_ID__: " + strings.TrimSpace(result.SessionID)
	}

	return response, nil
}

func (a *ClaudeCodeAgent) buildArgs(sessionID string, input string) []string {
	args := []string{"--print", "--output-format", "json"}
	if strings.TrimSpace(sessionID) != "" {
		args = append(args, "--resume", strings.TrimSpace(sessionID))
	}
	args = append(args, input)
	return args
}

func parseClaudePrintOutput(output string) (*claudePrintResult, error) {
	var result claudePrintResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func resolveClaudeCommand() (string, error) {
	for _, candidate := range []string{"claude", "cc"} {
		if _, err := exec.LookPath(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", exec.ErrNotFound
}

// IsAvailable 检查 Agent 是否可用
func (a *ClaudeCodeAgent) IsAvailable() bool {
	_, err := resolveClaudeCommand()
	return err == nil
}
