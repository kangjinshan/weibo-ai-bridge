package router

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDecodeProjectPath(t *testing.T) {
	tests := []struct {
		encoded  string
		expected string
	}{
		{"-home-ubuntu-workspace", "/home/ubuntu/workspace"},
		{"-home-ubuntu", "/home/ubuntu"},
		{"-home", "/home"},
		{"", ""},
	}

	for _, tt := range tests {
		result := decodeProjectPath(tt.encoded)
		if result != tt.expected {
			t.Errorf("decodeProjectPath(%q) = %q, want %q", tt.encoded, result, tt.expected)
		}
	}
}

func TestIsValidUUID(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"80b2c6c6-273a-49a7-bcab-8333d6582276", true},
		{"0a8ea231-4406-4dd3-8065-0510acbbc071", true},
		{"not-a-uuid", false},
		{"", false},
		{"80b2c6c6-273a-49a7-bcab", false},
	}

	for _, tt := range tests {
		result := isValidUUID(tt.input)
		if result != tt.expected {
			t.Errorf("isValidUUID(%q) = %v, want %v", tt.input, result, tt.expected)
		}
	}
}

func TestListNativeClaudeSessions(t *testing.T) {
	// 创建临时目录模拟 ~/.claude/projects/
	tmpDir := t.TempDir()
	projectsDir := filepath.Join(tmpDir, ".claude", "projects")
	projectDir := filepath.Join(projectsDir, "-home-ubuntu-testproject")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// 创建一个模拟的 .jsonl 文件
	sessionContent := `{"type":"queue-operation","operation":"enqueue","timestamp":"2026-04-20T07:29:43.967Z","sessionId":"80b2c6c6-273a-49a7-bcab-8333d6582276","content":"测试消息"}
`
	sessionFile := filepath.Join(projectDir, "80b2c6c6-273a-49a7-bcab-8333d6582276.jsonl")
	if err := os.WriteFile(sessionFile, []byte(sessionContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// 创建一个非 UUID 文件（应被忽略）
	nonUUIDFile := filepath.Join(projectDir, "not-a-session.jsonl")
	if err := os.WriteFile(nonUUIDFile, []byte("invalid"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 覆盖 home 目录
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	bridgeIDs := map[string]bool{}
	sessions, err := ListNativeClaudeSessions(bridgeIDs)
	if err != nil {
		t.Fatalf("ListNativeClaudeSessions error: %v", err)
	}

	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}

	s := sessions[0]
	if s.ID != "80b2c6c6-273a-49a7-bcab-8333d6582276" {
		t.Errorf("session ID = %q, want 80b2c6c6-273a-49a7-bcab-8333d6582276", s.ID)
	}
	if s.Title != "测试消息" {
		t.Errorf("session Title = %q, want 测试消息", s.Title)
	}
	if s.Project != "/home/ubuntu/testproject" {
		t.Errorf("session Project = %q, want /home/ubuntu/testproject", s.Project)
	}
	if s.InBridge {
		t.Error("session should not be in bridge")
	}
}

func TestListNativeClaudeSessions_InBridge(t *testing.T) {
	tmpDir := t.TempDir()
	projectsDir := filepath.Join(tmpDir, ".claude", "projects")
	projectDir := filepath.Join(projectsDir, "-home-ubuntu-testproject")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}

	sessionContent := `{"type":"queue-operation","operation":"enqueue","timestamp":"2026-04-20T07:29:43.967Z","sessionId":"80b2c6c6-273a-49a7-bcab-8333d6582276","content":"测试消息"}
`
	sessionFile := filepath.Join(projectDir, "80b2c6c6-273a-49a7-bcab-8333d6582276.jsonl")
	if err := os.WriteFile(sessionFile, []byte(sessionContent), 0o644); err != nil {
		t.Fatal(err)
	}

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	bridgeIDs := map[string]bool{"80b2c6c6-273a-49a7-bcab-8333d6582276": true}
	sessions, err := ListNativeClaudeSessions(bridgeIDs)
	if err != nil {
		t.Fatalf("ListNativeClaudeSessions error: %v", err)
	}

	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}

	if !sessions[0].InBridge {
		t.Error("session should be marked as in bridge")
	}
}

func TestParseClaudeSessionFile_InvalidFirstLine(t *testing.T) {
	tmpDir := t.TempDir()
	// 创建一个首行不是 queue-operation 的文件
	badFile := filepath.Join(tmpDir, "bad.jsonl")
	if err := os.WriteFile(badFile, []byte("this is not json\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, ok := parseClaudeSessionFile(badFile, "test-id", "/test", nil)
	if ok {
		t.Error("expected parseClaudeSessionFile to return false for invalid file")
	}
}
