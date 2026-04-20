package agent

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// ClaudeCodeAgent Claude Code CLI Agent 实现
type ClaudeCodeAgent struct {
	name string
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
	// 检查 claude CLI 是否可用
	if !a.IsAvailable() {
		return "", fmt.Errorf("claude CLI is not available")
	}

	// 准备命令
	// TODO: 添加 --session-id 支持（当 Claude CLI 支持时）
	cmd := exec.Command("claude", "--print", input)

	// 捕获输出
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// 执行命令
	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("failed to execute claude CLI: %w, stderr: %s", err, stderr.String())
	}

	// 返回结果
	result := strings.TrimSpace(stdout.String())
	if result == "" {
		return "", fmt.Errorf("empty response from claude CLI")
	}

	return result, nil
}

// IsAvailable 检查 Agent 是否可用
func (a *ClaudeCodeAgent) IsAvailable() bool {
	// 检查 claude 命令是否存在
	_, err := exec.LookPath("claude")
	return err == nil
}