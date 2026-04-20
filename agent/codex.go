package agent

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// CodeXAgent CodeX CLI Agent 实现
type CodeXAgent struct {
	name string
}

// NewCodeXAgent 创建新的 CodeX Agent
func NewCodeXAgent() *CodeXAgent {
	return &CodeXAgent{
		name: "codex",
	}
}

// Name 返回 Agent 名称
func (a *CodeXAgent) Name() string {
	return a.name
}

// Execute 执行 AI 任务
func (a *CodeXAgent) Execute(input string) (string, error) {
	// 检查 codex CLI 是否可用
	if !a.IsAvailable() {
		return "", fmt.Errorf("codex CLI is not available")
	}

	// 准备命令
	cmd := exec.Command("codex", "--print", input)

	// 捕获输出
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// 执行命令
	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("failed to execute codex CLI: %w, stderr: %s", err, stderr.String())
	}

	// 返回结果
	result := strings.TrimSpace(stdout.String())
	if result == "" {
		return "", fmt.Errorf("empty response from codex CLI")
	}

	return result, nil
}

// IsAvailable 检查 Agent 是否可用
func (a *CodeXAgent) IsAvailable() bool {
	// 检查 codex 命令是否存在
	_, err := exec.LookPath("codex")
	return err == nil
}
