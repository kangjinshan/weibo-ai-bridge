package agent

import "testing"

func TestClaudeCodeAgent_Name(t *testing.T) {
	agent := NewClaudeCodeAgent()
	if agent.Name() != "claude-code" {
		t.Fatalf("expected name 'claude-code', got %q", agent.Name())
	}
}

func TestClaudeCodeAgent_IsAvailable(t *testing.T) {
	agent := NewClaudeCodeAgent()
	_ = agent.IsAvailable()
}

func TestClaudeCodeAgent_Execute(t *testing.T) {
	agent := NewClaudeCodeAgent()
	_, err := agent.Execute("", "test input")
	if err != nil {
		t.Logf("Execute failed (expected if claude CLI is not logged in or not installed): %v", err)
	}
}

func TestClaudeCodeAgent_buildArgs_NewSession(t *testing.T) {
	agent := NewClaudeCodeAgent()
	got := agent.buildArgs("", "hello")
	want := []string{"--print", "--output-format", "json", "hello"}
	assertSliceEqual(t, got, want)
}

func TestClaudeCodeAgent_buildArgs_ResumeSession(t *testing.T) {
	agent := NewClaudeCodeAgent()
	got := agent.buildArgs("11111111-1111-1111-1111-111111111111", "hello again")
	want := []string{"--print", "--output-format", "json", "--resume", "11111111-1111-1111-1111-111111111111", "hello again"}
	assertSliceEqual(t, got, want)
}

func TestParseClaudePrintOutput_Success(t *testing.T) {
	result, err := parseClaudePrintOutput(`{"type":"result","subtype":"success","is_error":false,"result":"hello","session_id":"abc-123"}`)
	if err != nil {
		t.Fatalf("parseClaudePrintOutput returned error: %v", err)
	}
	if result.Result != "hello" {
		t.Fatalf("unexpected result text: %q", result.Result)
	}
	if result.SessionID != "abc-123" {
		t.Fatalf("unexpected session id: %q", result.SessionID)
	}
	if result.IsError {
		t.Fatal("expected non-error result")
	}
}

func TestParseClaudePrintOutput_Error(t *testing.T) {
	result, err := parseClaudePrintOutput(`{"type":"result","subtype":"success","is_error":true,"result":"Not logged in · Please run /login","session_id":"abc-123"}`)
	if err != nil {
		t.Fatalf("parseClaudePrintOutput returned error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result")
	}
	if result.Result != "Not logged in · Please run /login" {
		t.Fatalf("unexpected result text: %q", result.Result)
	}
}

func TestParseClaudePrintOutput_InvalidJSON(t *testing.T) {
	if _, err := parseClaudePrintOutput("not json"); err == nil {
		t.Fatal("expected parse error for invalid json")
	}
}

func assertSliceEqual(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("unexpected slice length: got %d want %d, values=%v", len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("unexpected slice value at %d: got %q want %q, full=%v", i, got[i], want[i], got)
		}
	}
}
