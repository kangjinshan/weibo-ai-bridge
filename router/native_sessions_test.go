package router

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestListNativeClaudeSessions_UsesSessionsIndexMetadata(t *testing.T) {
	tmpDir := t.TempDir()
	projectsDir := filepath.Join(tmpDir, ".claude", "projects")
	projectDir := filepath.Join(projectsDir, "-home-ubuntu-testproject")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}

	sessionID := "80b2c6c6-273a-49a7-bcab-8333d6582276"
	sessionFile := filepath.Join(projectDir, sessionID+".jsonl")
	sessionContent := `{"type":"queue-operation","operation":"enqueue","timestamp":"2026-04-20T07:29:43.967Z","sessionId":"80b2c6c6-273a-49a7-bcab-8333d6582276","content":"jsonl 里的老标题"}
`
	if err := os.WriteFile(sessionFile, []byte(sessionContent), 0o644); err != nil {
		t.Fatal(err)
	}

	indexFile := filepath.Join(projectDir, "sessions-index.json")
	indexContent := `{
  "version": 1,
  "entries": [
    {
      "sessionId": "80b2c6c6-273a-49a7-bcab-8333d6582276",
      "fullPath": "/tmp/fake.jsonl",
      "fileMtime": 1776652200123,
      "firstPrompt": "index 里的 firstPrompt",
      "summary": "index 里的摘要标题",
      "messageCount": 12,
      "created": "2026-04-20T07:00:00.000Z",
      "modified": "2026-04-20T09:10:00.000Z",
      "gitBranch": "main",
      "projectPath": "/home/ubuntu/testproject",
      "isSidechain": false
    }
  ],
  "originalPath": "/home/ubuntu/testproject"
}
`
	if err := os.WriteFile(indexFile, []byte(indexContent), 0o644); err != nil {
		t.Fatal(err)
	}

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	sessions, err := ListNativeClaudeSessions(map[string]bool{})
	if err != nil {
		t.Fatalf("ListNativeClaudeSessions error: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}

	if sessions[0].Title != "index 里的摘要标题" {
		t.Fatalf("title = %q, want %q", sessions[0].Title, "index 里的摘要标题")
	}
	expectedModified, _ := time.Parse(time.RFC3339Nano, "2026-04-20T09:10:00.000Z")
	if !sessions[0].StartedAt.Equal(expectedModified) {
		t.Fatalf("StartedAt = %v, want %v", sessions[0].StartedAt, expectedModified)
	}
	if sessions[0].Project != "/home/ubuntu/testproject" {
		t.Fatalf("project = %q, want %q", sessions[0].Project, "/home/ubuntu/testproject")
	}
}

func TestListNativeClaudeSessionsForProject_FiltersByProjectPath(t *testing.T) {
	tmpDir := t.TempDir()
	projectsDir := filepath.Join(tmpDir, ".claude", "projects")
	projectA := filepath.Join(projectsDir, "-home-ubuntu-project-a")
	projectB := filepath.Join(projectsDir, "-home-ubuntu-project-b")
	if err := os.MkdirAll(projectA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(projectB, 0o755); err != nil {
		t.Fatal(err)
	}

	sessionA := "80b2c6c6-273a-49a7-bcab-8333d6582276"
	sessionB := "0a8ea231-4406-4dd3-8065-0510acbbc071"
	if err := os.WriteFile(filepath.Join(projectA, sessionA+".jsonl"), []byte(`{"type":"queue-operation","operation":"enqueue","timestamp":"2026-04-20T07:29:43.967Z","sessionId":"80b2c6c6-273a-49a7-bcab-8333d6582276","content":"A"}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectB, sessionB+".jsonl"), []byte(`{"type":"queue-operation","operation":"enqueue","timestamp":"2026-04-20T07:29:43.967Z","sessionId":"0a8ea231-4406-4dd3-8065-0510acbbc071","content":"B"}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	indexA := `{"version":1,"entries":[{"sessionId":"80b2c6c6-273a-49a7-bcab-8333d6582276","summary":"项目A","projectPath":"/home/ubuntu/project-a","modified":"2026-04-20T09:10:00.000Z"}]}`
	indexB := `{"version":1,"entries":[{"sessionId":"0a8ea231-4406-4dd3-8065-0510acbbc071","summary":"项目B","projectPath":"/home/ubuntu/project-b","modified":"2026-04-20T09:10:00.000Z"}]}`
	if err := os.WriteFile(filepath.Join(projectA, "sessions-index.json"), []byte(indexA), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectB, "sessions-index.json"), []byte(indexB), 0o644); err != nil {
		t.Fatal(err)
	}

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	sessions, err := ListNativeClaudeSessionsForProject(map[string]bool{}, "/home/ubuntu/project-a")
	if err != nil {
		t.Fatalf("ListNativeClaudeSessionsForProject error: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].ID != sessionA {
		t.Fatalf("session id = %q, want %q", sessions[0].ID, sessionA)
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

func TestParseClaudeSessionFile_ReadsTitleFromLaterLine(t *testing.T) {
	tmpDir := t.TempDir()
	sessionID := "80b2c6c6-273a-49a7-bcab-8333d6582276"
	file := filepath.Join(tmpDir, sessionID+".jsonl")
	content := fmt.Sprintf(`{"type":"queue-operation","operation":"enqueue","timestamp":"2026-04-20T07:29:43.967Z","sessionId":"%s","content":""}
{"type":"queue-operation","operation":"enqueue","timestamp":"2026-04-20T07:30:00.000Z","sessionId":"%s","content":"后续补充标题"}
`, sessionID, sessionID)
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	ns, ok := parseClaudeSessionFile(file, sessionID, "/home/ubuntu/testproject", nil)
	if !ok {
		t.Fatal("expected parseClaudeSessionFile to parse successfully")
	}
	if ns.Title != "后续补充标题" {
		t.Fatalf("title = %q, want %q", ns.Title, "后续补充标题")
	}
}

func TestParseClaudeSessionFile_UsesLastPromptEventTitle(t *testing.T) {
	tmpDir := t.TempDir()
	sessionID := "80b2c6c6-273a-49a7-bcab-8333d6582276"
	file := filepath.Join(tmpDir, sessionID+".jsonl")
	content := fmt.Sprintf(`{"type":"queue-operation","operation":"enqueue","timestamp":"2026-04-20T07:29:43.967Z","sessionId":"%s","content":"旧标题"}
{"type":"last-prompt","lastPrompt":"最新标题","sessionId":"%s"}
`, sessionID, sessionID)
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	ns, ok := parseClaudeSessionFile(file, sessionID, "/home/ubuntu/testproject", nil)
	if !ok {
		t.Fatal("expected parseClaudeSessionFile to parse successfully")
	}
	if ns.Title != "最新标题" {
		t.Fatalf("title = %q, want %q", ns.Title, "最新标题")
	}
}

func TestParseClaudeSessionFile_UsesLatestQueueTimestamp(t *testing.T) {
	tmpDir := t.TempDir()
	sessionID := "80b2c6c6-273a-49a7-bcab-8333d6582276"
	file := filepath.Join(tmpDir, sessionID+".jsonl")
	content := fmt.Sprintf(`{"type":"queue-operation","operation":"enqueue","timestamp":"2026-04-20T07:29:43.967Z","sessionId":"%s","content":"早期"}
{"type":"queue-operation","operation":"enqueue","timestamp":"2026-04-20T09:29:43.967Z","sessionId":"%s","content":"后续"}
`, sessionID, sessionID)
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	ns, ok := parseClaudeSessionFile(file, sessionID, "/home/ubuntu/testproject", nil)
	if !ok {
		t.Fatal("expected parseClaudeSessionFile to parse successfully")
	}
	expected, _ := time.Parse(time.RFC3339Nano, "2026-04-20T09:29:43.967Z")
	if !ns.StartedAt.Equal(expected) {
		t.Fatalf("StartedAt = %v, want %v", ns.StartedAt, expected)
	}
}

func TestParseClaudeSessionFile_UsesCwdFromTranscript(t *testing.T) {
	tmpDir := t.TempDir()
	sessionID := "80b2c6c6-273a-49a7-bcab-8333d6582276"
	file := filepath.Join(tmpDir, sessionID+".jsonl")
	content := fmt.Sprintf(`{"type":"queue-operation","operation":"enqueue","timestamp":"2026-04-20T07:29:43.967Z","sessionId":"%s","content":"测试"}
{"type":"user","message":{"role":"user","content":"测试"},"sessionId":"%s","cwd":"/Users/kanayama/Desktop/AI/weibo-ai-bridge"}
`, sessionID, sessionID)
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	ns, ok := parseClaudeSessionFile(file, sessionID, "/Users/kanayama/Desktop/AI/weibo/ai/bridge", nil)
	if !ok {
		t.Fatal("expected parseClaudeSessionFile to parse successfully")
	}
	if ns.Project != "/Users/kanayama/Desktop/AI/weibo-ai-bridge" {
		t.Fatalf("project = %q, want %q", ns.Project, "/Users/kanayama/Desktop/AI/weibo-ai-bridge")
	}
}



func TestParseClaudeSessionFile_FiltersSidechain(t *testing.T) {
	tmpDir := t.TempDir()
	sessionID := "80b2c6c6-273a-49a7-bcab-8333d6582276"
	file := filepath.Join(tmpDir, sessionID+".jsonl")
	content := fmt.Sprintf(`{"type":"queue-operation","operation":"enqueue","timestamp":"2026-04-20T07:29:43.967Z","sessionId":"%s","content":"子会话"}
{"type":"attachment","sessionId":"%s","isSidechain":true}
`, sessionID, sessionID)
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	_, ok := parseClaudeSessionFile(file, sessionID, "/home/ubuntu/testproject", nil)
	if ok {
		t.Error("expected sidechain session to be filtered out")
	}
}

func TestParseClaudeSessionFile_FiltersDaemonSessionKind(t *testing.T) {
	tmpDir := t.TempDir()
	sessionID := "80b2c6c6-273a-49a7-bcab-8333d6582276"
	file := filepath.Join(tmpDir, sessionID+".jsonl")
	content := fmt.Sprintf(`{"type":"queue-operation","operation":"enqueue","timestamp":"2026-04-20T07:29:43.967Z","sessionId":"%s","content":"后台会话"}
{"type":"attachment","sessionId":"%s","sessionKind":"daemon"}
`, sessionID, sessionID)
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	_, ok := parseClaudeSessionFile(file, sessionID, "/home/ubuntu/testproject", nil)
	if ok {
		t.Error("expected daemon sessionKind to be filtered out")
	}
}

func TestParseClaudeSessionFile_CustomTitleTakesPriority(t *testing.T) {
	tmpDir := t.TempDir()
	sessionID := "80b2c6c6-273a-49a7-bcab-8333d6582276"
	file := filepath.Join(tmpDir, sessionID+".jsonl")
	content := fmt.Sprintf(`{"type":"queue-operation","operation":"enqueue","timestamp":"2026-04-20T07:29:43.967Z","sessionId":"%s","content":"内容标题"}
{"type":"last-prompt","lastPrompt":"最后提问","sessionId":"%s"}
{"type":"summary","summary":"AI摘要","sessionId":"%s"}
{"type":"ai-title","aiTitle":"AI标题","sessionId":"%s"}
{"type":"custom-title","customTitle":"自定义标题","sessionId":"%s"}
`, sessionID, sessionID, sessionID, sessionID, sessionID)
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	ns, ok := parseClaudeSessionFile(file, sessionID, "/home/ubuntu/testproject", nil)
	if !ok {
		t.Fatal("expected parseClaudeSessionFile to parse successfully")
	}
	if ns.Title != "自定义标题" {
		t.Fatalf("title = %q, want %q (customTitle takes priority)", ns.Title, "自定义标题")
	}
}

func TestParseClaudeSessionFile_AiTitleOverSummary(t *testing.T) {
	tmpDir := t.TempDir()
	sessionID := "80b2c6c6-273a-49a7-bcab-8333d6582276"
	file := filepath.Join(tmpDir, sessionID+".jsonl")
	content := fmt.Sprintf(`{"type":"queue-operation","operation":"enqueue","timestamp":"2026-04-20T07:29:43.967Z","sessionId":"%s","content":"内容"}
{"type":"summary","summary":"AI摘要","sessionId":"%s"}
{"type":"ai-title","aiTitle":"AI标题","sessionId":"%s"}
`, sessionID, sessionID, sessionID)
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	ns, ok := parseClaudeSessionFile(file, sessionID, "/home/ubuntu/testproject", nil)
	if !ok {
		t.Fatal("expected parseClaudeSessionFile to parse successfully")
	}
	if ns.Title != "AI标题" {
		t.Fatalf("title = %q, want %q (aiTitle over summary)", ns.Title, "AI标题")
	}
}

func TestParseCodexSessionFile_ReadsTitleBeyondInitialChunk(t *testing.T) {
	tmpDir := t.TempDir()
	file := filepath.Join(tmpDir, "codex.jsonl")
	sessionID := "thread-123"
	longText := strings.Repeat("x", 20000)
	content := fmt.Sprintf(`{"type":"session_meta","timestamp":"2026-04-20T07:29:43.967Z","payload":{"id":"%s","source":"codex_cli","originator":"codex_cli_go","cwd":"/home/ubuntu/testproject"}}
{"type":"response_item","payload":{"role":"assistant","content":[{"type":"output_text","text":"%s"}]}}
{"type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"这是真实标题"}]}}
`, sessionID, longText)
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	ns, ok := parseCodexSessionFile(file, map[string]bool{})
	if !ok {
		t.Fatal("expected parseCodexSessionFile to parse successfully")
	}
	if ns.Title != "这是真实标题" {
		t.Fatalf("title = %q, want %q", ns.Title, "这是真实标题")
	}
}

func TestParseCodexSessionFile_FallbackTitleWhenNoUserInput(t *testing.T) {
	tmpDir := t.TempDir()
	file := filepath.Join(tmpDir, "codex-no-title.jsonl")
	content := `{"type":"session_meta","timestamp":"2026-04-20T07:29:43.967Z","payload":{"id":"thread-abc","source":"codex_cli","originator":"codex_cli_go","cwd":"/home/ubuntu/testproject"}}
`
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	ns, ok := parseCodexSessionFile(file, map[string]bool{})
	if !ok {
		t.Fatal("expected parseCodexSessionFile to parse successfully")
	}
	if ns.Title != "会话@testproject" {
		t.Fatalf("title = %q, want %q", ns.Title, "会话@testproject")
	}
}

func TestListNativeCodexSessions_UsesSessionIndexThreadName(t *testing.T) {
	tmpDir := t.TempDir()
	codexHome := filepath.Join(tmpDir, ".codex")
	sessionsDir := filepath.Join(codexHome, "sessions", "2026", "04", "29")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	sessionID := "019dd23f-daf4-7190-bcc7-51384af37dbe"
	sessionFile := filepath.Join(sessionsDir, "rollout-2026-04-29T00-00-00-"+sessionID+".jsonl")
	sessionContent := fmt.Sprintf(`{"type":"session_meta","timestamp":"2026-04-29T01:00:00.000Z","payload":{"id":"%s","source":"codex_cli","originator":"codex_cli_go","cwd":"/tmp/project-a"}}
{"type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"文件里的旧标题"}]}}
`, sessionID)
	if err := os.WriteFile(sessionFile, []byte(sessionContent), 0o644); err != nil {
		t.Fatal(err)
	}

	indexFile := filepath.Join(codexHome, "session_index.jsonl")
	indexContent := fmt.Sprintf(`{"id":"%s","thread_name":"索引里的新标题","updated_at":"2026-04-29T08:00:00.000Z"}
`, sessionID)
	if err := os.WriteFile(indexFile, []byte(indexContent), 0o644); err != nil {
		t.Fatal(err)
	}

	origCodexHome := os.Getenv("CODEX_HOME")
	_ = os.Setenv("CODEX_HOME", codexHome)
	defer os.Setenv("CODEX_HOME", origCodexHome)

	sessions, err := ListNativeCodexSessions(map[string]bool{})
	if err != nil {
		t.Fatalf("ListNativeCodexSessions error: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}

	if sessions[0].Title != "索引里的新标题" {
		t.Fatalf("title = %q, want %q", sessions[0].Title, "索引里的新标题")
	}
	expectedTime, _ := time.Parse(time.RFC3339Nano, "2026-04-29T08:00:00.000Z")
	if !sessions[0].StartedAt.Equal(expectedTime) {
		t.Fatalf("StartedAt = %v, want %v", sessions[0].StartedAt, expectedTime)
	}
}

func TestParseCodexThreadRecordsJSONL(t *testing.T) {
	data := []byte(`
{"id":"t-1","title":"标题1","cwd":"/tmp/a","updated_at":100}
{"id":"t-2","title":"标题2","cwd":"/tmp/b","updated_at":200}
invalid-json
{"id":"","title":"bad","cwd":"/tmp/c","updated_at":300}
`)

	records := parseCodexThreadRecordsJSONL(data)
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
	if records[0].ID != "t-1" || records[1].ID != "t-2" {
		t.Fatalf("unexpected record IDs: %+v", records)
	}
}

func TestDedupeNativeSessionsByID(t *testing.T) {
	sessions := []NativeSession{
		{ID: "a", StartedAt: time.Unix(200, 0), Title: "new"},
		{ID: "a", StartedAt: time.Unix(100, 0), Title: "old"},
		{ID: "b", StartedAt: time.Unix(150, 0), Title: "b"},
	}

	deduped := dedupeNativeSessionsByID(sessions)
	if len(deduped) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(deduped))
	}
	if deduped[0].ID != "a" || deduped[0].Title != "new" {
		t.Fatalf("unexpected first session after dedupe: %+v", deduped[0])
	}
}

func TestListClaudeSessionsFromHistory(t *testing.T) {
	tmpDir := t.TempDir()
	claudeDir := filepath.Join(tmpDir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}

	historyContent := `{"display":"第一个会话的消息","pastedContents":"","timestamp":1774944752288,"project":"/home/ubuntu/project-a","sessionId":"80b2c6c6-273a-49a7-bcab-8333d6582276"}
{"display":"第二个会话的消息","pastedContents":"","timestamp":1774944808122,"project":"/home/ubuntu/project-b","sessionId":"0a8ea231-4406-4dd3-8065-0510acbbc071"}
{"display":"/resume","pastedContents":"","timestamp":1774944900000,"project":"/home/ubuntu/project-b","sessionId":"0a8ea231-4406-4dd3-8065-0510acbbc071"}
`
	historyFile := filepath.Join(claudeDir, "history.jsonl")
	if err := os.WriteFile(historyFile, []byte(historyContent), 0o644); err != nil {
		t.Fatal(err)
	}

	sessions := listClaudeSessionsFromHistory(tmpDir, map[string]bool{})
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}

	byID := map[string]NativeSession{}
	for _, s := range sessions {
		byID[s.ID] = s
	}

	s1, ok := byID["80b2c6c6-273a-49a7-bcab-8333d6582276"]
	if !ok {
		t.Fatal("session 80b2c6c6 not found")
	}
	if s1.Title != "第一个会话的消息" {
		t.Errorf("title = %q, want %q", s1.Title, "第一个会话的消息")
	}
	if s1.Project != "/home/ubuntu/project-a" {
		t.Errorf("project = %q, want %q", s1.Project, "/home/ubuntu/project-a")
	}

	s2, ok := byID["0a8ea231-4406-4dd3-8065-0510acbbc071"]
	if !ok {
		t.Fatal("session 0a8ea231 not found")
	}
	// 标题应为最后一条非斜杠命令消息
	if s2.Title != "第二个会话的消息" {
		t.Errorf("title = %q, want %q", s2.Title, "第二个会话的消息")
	}
	if s2.Project != "/home/ubuntu/project-b" {
		t.Errorf("project = %q, want %q", s2.Project, "/home/ubuntu/project-b")
	}
}

func TestListNativeClaudeSessions_MergesHistory(t *testing.T) {
	tmpDir := t.TempDir()
	claudeDir := filepath.Join(tmpDir, ".claude")
	projectsDir := filepath.Join(claudeDir, "projects")
	projectDir := filepath.Join(projectsDir, "-home-ubuntu-testproject")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// 创建一个 .jsonl 文件会话
	sessionContent := `{"type":"queue-operation","operation":"enqueue","timestamp":"2026-04-20T07:29:43.967Z","sessionId":"80b2c6c6-273a-49a7-bcab-8333d6582276","content":"jsonl会话"}
`
	if err := os.WriteFile(filepath.Join(projectDir, "80b2c6c6-273a-49a7-bcab-8333d6582276.jsonl"), []byte(sessionContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// 创建 history.jsonl，包含 jsonl 会话 + 一个无文件的新会话
	historyContent := `{"display":"jsonl会话","pastedContents":"","timestamp":1774944752288,"project":"/home/ubuntu/testproject","sessionId":"80b2c6c6-273a-49a7-bcab-8333d6582276"}
{"display":"无文件的新会话","pastedContents":"","timestamp":1774944900000,"project":"/home/ubuntu/testproject","sessionId":"0a8ea231-4406-4dd3-8065-0510acbbc071"}
`
	if err := os.WriteFile(filepath.Join(claudeDir, "history.jsonl"), []byte(historyContent), 0o644); err != nil {
		t.Fatal(err)
	}

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	sessions, err := ListNativeClaudeSessions(map[string]bool{})
	if err != nil {
		t.Fatalf("ListNativeClaudeSessions error: %v", err)
	}

	// 应该有 2 个去重后的会话
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}

	byID := map[string]NativeSession{}
	for _, s := range sessions {
		byID[s.ID] = s
	}

	if _, ok := byID["80b2c6c6-273a-49a7-bcab-8333d6582276"]; !ok {
		t.Error("jsonl session not found")
	}
	if _, ok := byID["0a8ea231-4406-4dd3-8065-0510acbbc071"]; !ok {
		t.Error("history-only session not found")
	}
}
