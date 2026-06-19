package agent

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGeminiAgent_Name(t *testing.T) {
	agent := NewGeminiAgent("gemini-3-flash-preview")
	if agent.Name() != "gemini" {
		t.Fatalf("expected name gemini, got %q", agent.Name())
	}
}

func TestGeminiAgent_buildCommand_NewSession(t *testing.T) {
	agent := NewGeminiAgent("gemini-3-flash-preview")
	cmd := agent.buildCommand(context.Background(), &geminiSession{}, "hello")

	want := []string{
		"gemini",
		"--skip-trust",
		"--output-format", "stream-json",
		"--include-directories", string(filepath.Separator),
		"--model", "gemini-3-flash-preview",
		"--prompt", wrapUserPrompt("hello"),
	}
	if strings.Join(cmd.Args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("unexpected args: got %v want %v", cmd.Args, want)
	}
}

func TestGeminiAgent_buildCommand_ResumeSession(t *testing.T) {
	agent := NewGeminiAgent("")
	session := &geminiSession{}
	session.SetCurrentSessionID("24431794-9579-4b40-a08d-0c2467122e96")

	cmd := agent.buildCommand(context.Background(), session, "hello again")

	want := []string{
		"gemini",
		"--skip-trust",
		"--output-format", "stream-json",
		"--include-directories", string(filepath.Separator),
		"--resume", "24431794-9579-4b40-a08d-0c2467122e96",
		"--prompt", wrapUserPrompt("hello again"),
	}
	if strings.Join(cmd.Args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("unexpected args: got %v want %v", cmd.Args, want)
	}
}

func TestGeminiAgent_buildCommand_UsesWorkDirFromContext(t *testing.T) {
	agent := NewGeminiAgent("")
	workDir := t.TempDir()
	ctx := WithWorkDir(context.Background(), workDir)

	cmd := agent.buildCommand(ctx, &geminiSession{}, "hello")

	if cmd.Dir != workDir {
		t.Fatalf("unexpected cmd dir: got %q want %q", cmd.Dir, workDir)
	}
}

func TestGeminiAgent_buildCommand_AllowAllAddsYoloMode(t *testing.T) {
	agent := NewGeminiAgent("")
	ctx := WithAllowAll(context.Background(), true)

	cmd := agent.buildCommand(ctx, &geminiSession{}, "hello")

	want := []string{
		"gemini",
		"--skip-trust",
		"--output-format", "stream-json",
		"--include-directories", string(filepath.Separator),
		"-y",
		"--prompt", wrapUserPrompt("hello"),
	}
	if strings.Join(cmd.Args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("unexpected args: got %v want %v", cmd.Args, want)
	}
}

func TestGeminiCommandEnv_AddsPayloadPatchPreload(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheDir)

	env := geminiCommandEnv([]string{
		"HOME=/tmp/home",
		"NODE_OPTIONS=--trace-warnings",
	})

	nodeOptions := envValue(env, "NODE_OPTIONS")
	if !strings.Contains(nodeOptions, "--trace-warnings") {
		t.Fatalf("NODE_OPTIONS should preserve existing values, got %q", nodeOptions)
	}
	if !strings.Contains(nodeOptions, "--import=file://") {
		t.Fatalf("NODE_OPTIONS should preload the Gemini payload sanitizer, got %q", nodeOptions)
	}
	if strings.Contains(nodeOptions, "--approval-mode") {
		t.Fatalf("NODE_OPTIONS should not affect Gemini approval mode, got %q", nodeOptions)
	}

	userCacheDir, err := os.UserCacheDir()
	if err != nil {
		t.Fatalf("UserCacheDir failed: %v", err)
	}
	patchPath := filepath.Join(userCacheDir, "weibo-ai-bridge", "gemini-payload-sanitizer.cjs")
	data, err := os.ReadFile(patchPath)
	if err != nil {
		t.Fatalf("expected preload script at %s: %v", patchPath, err)
	}
	if !strings.Contains(string(data), "functionResponse") || !strings.Contains(string(data), "functionCall") {
		t.Fatalf("preload script should sanitize Gemini tool parts, got %q", string(data))
	}
}

func TestGeminiCommandEnv_WindowsDoesNotInjectUnixShell(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheDir)

	env := geminiCommandEnvForGOOS([]string{
		"HOME=C:\\Users\\alice",
	}, "windows")

	if got := envValue(env, "SHELL"); got != "" {
		t.Fatalf("Windows Gemini env should not inject a Unix shell, got %q", got)
	}
}

func TestGeminiPayloadPatchImportArg_WindowsPathUsesValidFileURL(t *testing.T) {
	got := geminiPayloadPatchImportArg(`C:\Users\alice\AppData\Local\weibo-ai-bridge\gemini-payload-sanitizer.cjs`)
	want := "--import=file:///C:/Users/alice/AppData/Local/weibo-ai-bridge/gemini-payload-sanitizer.cjs"
	if got != want {
		t.Fatalf("unexpected import arg: got %q want %q", got, want)
	}
	if strings.Contains(got, `%5C`) || strings.Contains(got, `\`) {
		t.Fatalf("Windows file URL should not encode path separators as backslashes: %q", got)
	}
}

func TestGeminiAgent_sanitizeResumeSessionHistory_RemovesGeminiPreviewUnsupportedToolIDs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GEMINI_HOME", home)

	sessionID := "24431794-9579-4b40-a08d-0c2467122e96"
	chatsDir := filepath.Join(home, "tmp", "weibo-ai-bridge", "chats")
	if err := os.MkdirAll(chatsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	sessionPath := filepath.Join(chatsDir, "session-2026-05-12T12-14-e34b6961.jsonl")
	content := strings.Join([]string{
		`{"session_id":"24431794-9579-4b40-a08d-0c2467122e96","start_time":"2026-05-12T12:14:32.211Z"}`,
		`{"id":"message-1","type":"gemini","content":[{"functionCall":{"id":"content_call_1","name":"update_topic","args":{"title":"Updating Personal Memory"}}}],"toolCalls":[{"id":"update_topic_1","name":"update_topic","result":[{"functionResponse":{"id":"update_topic_1","name":"update_topic","response":{"output":"OK"}}}],"status":"success"}]}`,
		"",
	}, "\n")
	if err := os.WriteFile(sessionPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	changed, err := sanitizeGeminiResumeSessionHistory(sessionID)
	if err != nil {
		t.Fatalf("sanitizeGeminiResumeSessionHistory returned error: %v", err)
	}
	if !changed {
		t.Fatalf("expected sanitizer to update session history")
	}

	data, err := os.ReadFile(sessionPath)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("unexpected lines: %q", string(data))
	}

	var msg map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &msg); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if msg["id"] != "message-1" {
		t.Fatalf("message id should be preserved, got %v", msg["id"])
	}
	contentParts := msg["content"].([]any)
	contentCall := contentParts[0].(map[string]any)["functionCall"].(map[string]any)
	if _, ok := contentCall["id"]; ok {
		t.Fatalf("functionCall id should be removed: %+v", contentCall)
	}
	toolCalls := msg["toolCalls"].([]any)
	toolCall := toolCalls[0].(map[string]any)
	if _, ok := toolCall["id"]; ok {
		t.Fatalf("tool call id should be removed before Gemini CLI converts it to functionCall id: %+v", toolCall)
	}
	result := toolCall["result"].([]any)
	responsePart := result[0].(map[string]any)
	functionResponse := responsePart["functionResponse"].(map[string]any)
	if _, ok := functionResponse["id"]; ok {
		t.Fatalf("functionResponse id should be removed: %+v", functionResponse)
	}
	if functionResponse["name"] != "update_topic" {
		t.Fatalf("functionResponse name should be preserved: %+v", functionResponse)
	}
}

func TestGeminiAgent_streamGeminiOutput(t *testing.T) {
	agent := NewGeminiAgent("")
	session := &geminiSession{}
	stdout := io.NopCloser(strings.NewReader(strings.Join([]string{
		`{"type":"init","timestamp":"2026-05-12T11:17:04.002Z","session_id":"24431794-9579-4b40-a08d-0c2467122e96","model":"gemini-3-flash-preview"}`,
		`{"type":"message","role":"user","content":"hello"}`,
		`{"type":"message","timestamp":"2026-05-12T11:17:05.000Z","role":"assistant","content":"你好","delta":true}`,
		`{"type":"tool_use","tool_name":"run_shell_command","tool_id":"tool-1","parameters":{"command":"pwd"}}`,
		`{"type":"tool_result","tool_id":"tool-1","status":"success","output":"/tmp"}`,
		`{"type":"result","status":"success"}`,
		"",
	}, "\n")))

	events := make(chan Event, 8)
	errorParts, err := agent.streamGeminiOutput(context.Background(), session, stdout, events)
	close(events)
	if err != nil {
		t.Fatalf("streamGeminiOutput returned error: %v", err)
	}
	if len(errorParts) != 0 {
		t.Fatalf("unexpected errors: %v", errorParts)
	}

	var got []Event
	for event := range events {
		got = append(got, event)
	}
	if len(got) != 5 {
		t.Fatalf("unexpected events: %+v", got)
	}
	if got[0].Type != EventTypeSession || got[0].SessionID != "24431794-9579-4b40-a08d-0c2467122e96" {
		t.Fatalf("unexpected session event: %+v", got[0])
	}
	if got[1].Type != EventTypeDelta || got[1].Content != "你好" {
		t.Fatalf("unexpected message event: %+v", got[1])
	}
	if got[2].Type != EventTypeToolStart || got[2].ToolName != "run_shell_command" {
		t.Fatalf("unexpected tool start event: %+v", got[2])
	}
	if got[3].Type != EventTypeToolEnd {
		t.Fatalf("unexpected tool end event: %+v", got[3])
	}
	if got[4].Type != EventTypeDone {
		t.Fatalf("unexpected done event: %+v", got[4])
	}
	if session.CurrentSessionID() != "24431794-9579-4b40-a08d-0c2467122e96" {
		t.Fatalf("unexpected session id: %q", session.CurrentSessionID())
	}
}

func TestGeminiAgent_streamGeminiOutput_ResultError(t *testing.T) {
	agent := NewGeminiAgent("")
	session := &geminiSession{}
	stdout := io.NopCloser(strings.NewReader(strings.Join([]string{
		`{"type":"result","status":"error","error":{"type":"FatalAuthenticationError","message":"missing key"}}`,
		"",
	}, "\n")))

	events := make(chan Event, 4)
	errorParts, err := agent.streamGeminiOutput(context.Background(), session, stdout, events)
	close(events)
	if err != nil {
		t.Fatalf("streamGeminiOutput returned error: %v", err)
	}
	if len(errorParts) != 1 || errorParts[0] != "missing key" {
		t.Fatalf("unexpected errors: %v", errorParts)
	}
}
