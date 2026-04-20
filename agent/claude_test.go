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
	// 我们只是测试方法签名
	_, err := agent.Execute("test input")
	if err != nil {
		// 如果 claude CLI 不可用，这是预期的
		t.Logf("Execute failed (expected if claude CLI not installed): %v", err)
	}
}