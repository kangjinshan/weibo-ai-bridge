package agent

import (
	"testing"
)

func TestClaudeCodeAgent_Name(t *testing.T) {
	agent := NewClaudeCodeAgent()
	if agent.Name() != "claude-code" {
		t.Errorf("Expected name 'claude-code', got '%s'", agent.Name())
	}
}

func TestClaudeCodeAgent_IsAvailable(t *testing.T) {
	agent := NewClaudeCodeAgent()
	// 这个测试取决于 claude CLI 是否已安装
	// 我们只是测试方法不会 panic
	_ = agent.IsAvailable()
}

func TestClaudeCodeAgent_Execute(t *testing.T) {
	agent := NewClaudeCodeAgent()
	// 这个测试取决于 claude CLI 是否已安装且可用
	// 跳过集成测试
	t.Skip("跳过集成测试 - 需要 claude CLI")
	_, err := agent.Execute("test input", "")
	if err != nil {
		t.Logf("Execute failed (expected if claude CLI not installed): %v", err)
	}
}