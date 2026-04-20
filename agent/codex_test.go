package agent

import (
	"reflect"
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
	// 我们只是测试方法签名
	_, err := agent.Execute("test input")
	if err != nil {
		// 如果 codex CLI 不可用，这是预期的
		t.Logf("Execute failed (expected if codex CLI not installed): %v", err)
	}
}

func TestCodeXAgent_BuildCommand(t *testing.T) {
	agent := NewCodeXAgent()

	cmd := agent.buildCommand("hello", "/tmp/codex-output.txt")

	expectedArgs := []string{
		"codex",
		"-a", "never",
		"exec",
		"--sandbox", "workspace-write",
		"--color", "never",
		"--skip-git-repo-check",
		"--ephemeral",
		"--output-last-message", "/tmp/codex-output.txt",
		"hello",
	}

	if !reflect.DeepEqual(cmd.Args, expectedArgs) {
		t.Fatalf("unexpected command args: got %#v want %#v", cmd.Args, expectedArgs)
	}
}
