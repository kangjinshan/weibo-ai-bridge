package agent

import (
	"testing"
)

func TestCodeXAgent_Name(t *testing.T) {
	agent := NewCodeXAgent()
	if agent.Name() != "codex" {
		t.Errorf("Expected name 'codex', got '%s'", agent.Name())
	}
}

func TestCodeXAgent_IsAvailable(t *testing.T) {
	agent := NewCodeXAgent()
	// 这个测试取决于 codex CLI 是否已安装
	// 我们只是测试方法不会 panic
	_ = agent.IsAvailable()
}

func TestCodeXAgent_Execute(t *testing.T) {
	agent := NewCodeXAgent()
	// 这个测试取决于 codex CLI 是否已安装且可用
	// 跳过集成测试
	t.Skip("跳过集成测试 - 需要 codex CLI")
	_, err := agent.Execute("test input", "")
	if err != nil {
		t.Logf("Execute failed (expected if codex CLI not installed): %v", err)
	}
}
