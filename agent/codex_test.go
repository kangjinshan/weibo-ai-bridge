package agent

import (
	"context"
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
	_, err := agent.Execute(context.Background(), "", "test input")
	if err != nil {
		t.Logf("Execute failed (expected if codex CLI is not configured): %v", err)
	}
}

func TestCodeXAgent_buildCommand_NewSession(t *testing.T) {
	agent := NewCodeXAgent("gpt-4.5")

	cmd := agent.buildCommand(context.Background(), &codexSession{}, "hello")

	want := []string{"codex", "-a", "never", "-m", "gpt-4.5", "exec", "--skip-git-repo-check", "--json", "-"}
	if strings.Join(cmd.Args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("unexpected args: got %v want %v", cmd.Args, want)
	}
	if cmd.Stdin == nil {
		t.Fatal("expected stdin to be set")
	}
	stdinBytes, err := io.ReadAll(cmd.Stdin)
	if err != nil {
		t.Fatalf("failed to read stdin: %v", err)
	}
	if got := string(stdinBytes); got != wrapUserPrompt("hello") {
		t.Fatalf("unexpected stdin: got %q", got)
	}
}

func TestCodeXAgent_buildCommand_NewSessionWithoutModelOverride(t *testing.T) {
	agent := NewCodeXAgent("")

	cmd := agent.buildCommand(context.Background(), &codexSession{}, "hello")

	want := []string{"codex", "-a", "never", "exec", "--skip-git-repo-check", "--json", "-"}
	if strings.Join(cmd.Args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("unexpected args: got %v want %v", cmd.Args, want)
	}
}

func TestCodeXAgent_buildCommand_ResumeSession(t *testing.T) {
	agent := NewCodeXAgent("gpt-4.5")
	session := &codexSession{}
	session.threadID.Store("thread-123")

	cmd := agent.buildCommand(context.Background(), session, "hello again")

	want := []string{"codex", "-a", "never", "-m", "gpt-4.5", "exec", "resume", "--skip-git-repo-check", "--json", "thread-123", "-"}
	if strings.Join(cmd.Args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("unexpected args: got %v want %v", cmd.Args, want)
	}
	stdinBytes, err := io.ReadAll(cmd.Stdin)
	if err != nil {
		t.Fatalf("failed to read stdin: %v", err)
	}
	if got := string(stdinBytes); got != wrapUserPrompt("hello again") {
		t.Fatalf("unexpected stdin: got %q", got)
	}
}

func TestCodeXAgent_streamCodexOutput_CurrentJSONL(t *testing.T) {
	agent := NewCodeXAgent("gpt-4.5")
	session := &codexSession{}
	stdout := io.NopCloser(strings.NewReader(strings.Join([]string{
		`{"type":"thread.started","thread_id":"thread-123"}`,
		`{"type":"item.completed","item":{"type":"agent_message","text":"hello from codex"}}`,
		`{"type":"turn.completed"}`,
		"",
	}, "\n")))

	events := make(chan Event, 8)
	errorParts, err := agent.streamCodexOutput(session, stdout, events)
	close(events)
	if err != nil {
		t.Fatalf("streamCodexOutput returned error: %v", err)
	}
	if len(errorParts) != 0 {
		t.Fatalf("unexpected errors: %v", errorParts)
	}
	var got []Event
	for event := range events {
		got = append(got, event)
	}
	if len(got) != 2 {
		t.Fatalf("unexpected events: %+v", got)
	}
	if got[0].Type != EventTypeSession || got[0].SessionID != "thread-123" {
		t.Fatalf("unexpected session event: %+v", got[0])
	}
	if got[1].Type != EventTypeMessage || got[1].Content != "hello from codex" {
		t.Fatalf("unexpected message event: %+v", got[1])
	}
	if session.CurrentSessionID() != "thread-123" {
		t.Fatalf("unexpected session id: %q", session.CurrentSessionID())
	}
}

func TestCodeXAgent_streamCodexOutput_CurrentJSONLContentArray(t *testing.T) {
	agent := NewCodeXAgent("gpt-4.5")
	session := &codexSession{}
	stdout := io.NopCloser(strings.NewReader(strings.Join([]string{
		`{"type":"thread.started","thread_id":"thread-456"}`,
		`{"type":"item.completed","item":{"type":"assistant_message","content":[{"type":"output_text","text":"line 1"},{"type":"output_text","text":"line 2"}]}}`,
		"",
	}, "\n")))

	events := make(chan Event, 8)
	_, err := agent.streamCodexOutput(session, stdout, events)
	close(events)
	if err != nil {
		t.Fatalf("streamCodexOutput returned error: %v", err)
	}
	var got []Event
	for event := range events {
		got = append(got, event)
	}
	if len(got) != 2 || got[1].Content != "line 1\nline 2" {
		t.Fatalf("unexpected events: %+v", got)
	}
}

func TestCodeXAgent_streamCodexOutput_LegacyJSONL(t *testing.T) {
	agent := NewCodeXAgent("gpt-4.5")
	session := &codexSession{}
	stdout := io.NopCloser(strings.NewReader(strings.Join([]string{
		`{"type":"session_meta","payload":{"id":"legacy-thread"}}`,
		`{"type":"event_msg","payload":{"type":"agent_message","message":"legacy response"}}`,
		"",
	}, "\n")))

	events := make(chan Event, 8)
	_, err := agent.streamCodexOutput(session, stdout, events)
	close(events)
	if err != nil {
		t.Fatalf("streamCodexOutput returned error: %v", err)
	}
	var got []Event
	for event := range events {
		got = append(got, event)
	}
	if len(got) != 2 || got[1].Content != "legacy response" {
		t.Fatalf("unexpected events: %+v", got)
	}
	if session.CurrentSessionID() != "legacy-thread" {
		t.Fatalf("unexpected session id: %q", session.CurrentSessionID())
	}
}

func TestCodeXAgent_streamCodexOutput_CapturesCLIErrorEvents(t *testing.T) {
	agent := NewCodeXAgent("gpt-4.5")
	session := &codexSession{}
	stdout := io.NopCloser(strings.NewReader(strings.Join([]string{
		`{"type":"thread.started","thread_id":"thread-789"}`,
		`{"type":"error","message":"upstream failed"}`,
		`{"type":"turn.failed","error":{"message":"upstream failed"}}`,
		"",
	}, "\n")))

	events := make(chan Event, 8)
	errorParts, err := agent.streamCodexOutput(session, stdout, events)
	close(events)
	if err != nil {
		t.Fatalf("streamCodexOutput returned error: %v", err)
	}
	if len(errorParts) != 1 || errorParts[0] != "upstream failed" {
		t.Fatalf("unexpected errors: %v", errorParts)
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

func TestParseCodexAppServerMessage_Delta(t *testing.T) {
	session := &codexSession{}
	deltaSeen := make(map[string]bool)

	events := parseCodexAppServerMessage(session, map[string]any{
		"method": "item/agentMessage/delta",
		"params": map[string]any{
			"threadId": "thread-1",
			"turnId":   "turn-1",
			"itemId":   "msg-1",
			"delta":    "你好",
		},
	}, deltaSeen)

	if len(events) != 1 || events[0].Type != EventTypeDelta || events[0].Content != "你好" {
		t.Fatalf("unexpected events: %+v", events)
	}
	if !deltaSeen["msg-1"] {
		t.Fatalf("expected deltaSeen to track item id")
	}
}

func TestParseCodexAppServerMessage_FinalMessageSkippedAfterDelta(t *testing.T) {
	session := &codexSession{}
	deltaSeen := map[string]bool{"msg-1": true}

	events := parseCodexAppServerMessage(session, map[string]any{
		"method": "item/completed",
		"params": map[string]any{
			"threadId": "thread-1",
			"turnId":   "turn-1",
			"item": map[string]any{
				"type": "agentMessage",
				"id":   "msg-1",
				"text": "完整正文",
			},
		},
	}, deltaSeen)

	if len(events) != 0 {
		t.Fatalf("expected no events, got %+v", events)
	}
}

func TestParseCodexAppServerMessage_FinalMessageWithoutDelta(t *testing.T) {
	session := &codexSession{}
	deltaSeen := make(map[string]bool)

	events := parseCodexAppServerMessage(session, map[string]any{
		"method": "item/completed",
		"params": map[string]any{
			"threadId": "thread-1",
			"turnId":   "turn-1",
			"item": map[string]any{
				"type": "agentMessage",
				"id":   "msg-2",
				"text": "完整正文",
			},
		},
	}, deltaSeen)

	if len(events) != 1 || events[0].Type != EventTypeMessage || events[0].Content != "完整正文" {
		t.Fatalf("unexpected events: %+v", events)
	}
}
