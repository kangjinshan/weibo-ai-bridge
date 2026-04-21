package agent

import (
	"io"
	"strings"
	"testing"
)

func TestCodeXAgent_Name(t *testing.T) {
	agent := NewCodeXAgent("gpt-4.5")
	if agent.Name() != "codex" {
		t.Fatalf("expected name codex, got %q", agent.Name())
	}
}

func TestCodeXAgent_IsAvailable(t *testing.T) {
	agent := NewCodeXAgent("gpt-4.5")
	_ = agent.IsAvailable()
}

func TestCodeXAgent_Execute(t *testing.T) {
	agent := NewCodeXAgent("gpt-4.5")
	_, err := agent.Execute("", "test input")
	if err != nil {
		t.Logf("Execute failed (expected if codex CLI is not configured): %v", err)
	}
}

func TestCodeXAgent_buildCommand_NewSession(t *testing.T) {
	agent := NewCodeXAgent("gpt-4.5")

	cmd := agent.buildCommand(&codexSession{}, "hello")

	want := []string{"codex", "-a", "never", "-m", "gpt-4.5", "exec", "--skip-git-repo-check", "--json", "-"}
	if strings.Join(cmd.Args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("unexpected args: got %v want %v", cmd.Args, want)
	}
	if cmd.Stdin == nil {
		t.Fatal("expected stdin to be set")
	}
}

func TestCodeXAgent_buildCommand_NewSessionWithoutModelOverride(t *testing.T) {
	agent := NewCodeXAgent("")

	cmd := agent.buildCommand(&codexSession{}, "hello")

	want := []string{"codex", "-a", "never", "exec", "--skip-git-repo-check", "--json", "-"}
	if strings.Join(cmd.Args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("unexpected args: got %v want %v", cmd.Args, want)
	}
}

func TestCodeXAgent_buildCommand_ResumeSession(t *testing.T) {
	agent := NewCodeXAgent("gpt-4.5")
	session := &codexSession{}
	session.threadID.Store("thread-123")

	cmd := agent.buildCommand(session, "hello again")

	want := []string{"codex", "-a", "never", "-m", "gpt-4.5", "exec", "resume", "--skip-git-repo-check", "--json", "thread-123", "-"}
	if strings.Join(cmd.Args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("unexpected args: got %v want %v", cmd.Args, want)
	}
}

func TestCodeXAgent_readCodexOutput_CurrentJSONL(t *testing.T) {
	agent := NewCodeXAgent("gpt-4.5")
	session := &codexSession{}
	stdout := io.NopCloser(strings.NewReader(strings.Join([]string{
		`{"type":"thread.started","thread_id":"thread-123"}`,
		`{"type":"item.completed","item":{"type":"agent_message","text":"hello from codex"}}`,
		`{"type":"turn.completed"}`,
		"",
	}, "\n")))

	output, err := agent.readCodexOutput(session, stdout)
	if err != nil {
		t.Fatalf("readCodexOutput returned error: %v", err)
	}
	if output.response != "hello from codex" {
		t.Fatalf("unexpected response: %q", output.response)
	}
	if len(output.errors) != 0 {
		t.Fatalf("unexpected errors: %v", output.errors)
	}
	if session.CurrentSessionID() != "thread-123" {
		t.Fatalf("unexpected session id: %q", session.CurrentSessionID())
	}
}

func TestCodeXAgent_readCodexOutput_CurrentJSONLContentArray(t *testing.T) {
	agent := NewCodeXAgent("gpt-4.5")
	session := &codexSession{}
	stdout := io.NopCloser(strings.NewReader(strings.Join([]string{
		`{"type":"thread.started","thread_id":"thread-456"}`,
		`{"type":"item.completed","item":{"type":"assistant_message","content":[{"type":"output_text","text":"line 1"},{"type":"output_text","text":"line 2"}]}}`,
		"",
	}, "\n")))

	output, err := agent.readCodexOutput(session, stdout)
	if err != nil {
		t.Fatalf("readCodexOutput returned error: %v", err)
	}
	if output.response != "line 1\nline 2" {
		t.Fatalf("unexpected response: %q", output.response)
	}
}

func TestCodeXAgent_readCodexOutput_LegacyJSONL(t *testing.T) {
	agent := NewCodeXAgent("gpt-4.5")
	session := &codexSession{}
	stdout := io.NopCloser(strings.NewReader(strings.Join([]string{
		`{"type":"session_meta","payload":{"id":"legacy-thread"}}`,
		`{"type":"event_msg","payload":{"type":"agent_message","message":"legacy response"}}`,
		"",
	}, "\n")))

	output, err := agent.readCodexOutput(session, stdout)
	if err != nil {
		t.Fatalf("readCodexOutput returned error: %v", err)
	}
	if output.response != "legacy response" {
		t.Fatalf("unexpected response: %q", output.response)
	}
	if session.CurrentSessionID() != "legacy-thread" {
		t.Fatalf("unexpected session id: %q", session.CurrentSessionID())
	}
}

func TestCodeXAgent_readCodexOutput_CapturesCLIErrorEvents(t *testing.T) {
	agent := NewCodeXAgent("gpt-4.5")
	session := &codexSession{}
	stdout := io.NopCloser(strings.NewReader(strings.Join([]string{
		`{"type":"thread.started","thread_id":"thread-789"}`,
		`{"type":"error","message":"upstream failed"}`,
		`{"type":"turn.failed","error":{"message":"upstream failed"}}`,
		"",
	}, "\n")))

	output, err := agent.readCodexOutput(session, stdout)
	if err != nil {
		t.Fatalf("readCodexOutput returned error: %v", err)
	}
	if len(output.errors) != 1 || output.errors[0] != "upstream failed" {
		t.Fatalf("unexpected errors: %v", output.errors)
	}
}

func TestCleanCodexStderr(t *testing.T) {
	stderr := "Reading additional input from stdin...\nreal error\n"
	if got := cleanCodexStderr(stderr); got != "real error" {
		t.Fatalf("unexpected stderr: %q", got)
	}
}

func TestJoinNonEmpty(t *testing.T) {
	got := joinNonEmpty([]string{"first", "", "first", "second"}, "second")
	if got != "first\nsecond" {
		t.Fatalf("unexpected joined output: %q", got)
	}
}
