package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestHermesAgent_Name(t *testing.T) {
	agent := NewHermesAgent("gpt-5.4", "bridge", "custom")
	if agent.Name() != "hermes" {
		t.Fatalf("expected name hermes, got %q", agent.Name())
	}
}

func TestHermesAgent_buildCommand_NewSession(t *testing.T) {
	agent := NewHermesAgent("gpt-5.4", "bridge", "custom")

	cmd := agent.buildCommand(nil, "hello")

	want := []string{
		"hermes",
		"--profile", "bridge",
		"chat",
		"--quiet",
		"--source", "tool",
		"--model", "gpt-5.4",
		"--provider", "custom",
		"--query", wrapUserPrompt("hello"),
	}
	if got := cmd.Args; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected command:\nwant: %#v\n got: %#v", want, got)
	}
}

func TestHermesAgent_buildCommand_ResumeSession(t *testing.T) {
	agent := NewHermesAgent("", "", "")
	session := &hermesSession{}
	session.SetCurrentSessionID("20260509_165837_579738")

	cmd := agent.buildCommand(session, "hello")

	want := []string{
		"hermes",
		"chat",
		"--quiet",
		"--source", "tool",
		"--resume", "20260509_165837_579738",
		"--query", wrapUserPrompt("hello"),
	}
	if got := cmd.Args; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected command:\nwant: %#v\n got: %#v", want, got)
	}
}

func TestHermesAgent_StartSessionUsesACP(t *testing.T) {
	binDir := t.TempDir()
	hermesPath := filepath.Join(binDir, "hermes")
	script := `#!/bin/sh
while IFS= read -r line; do
  case "$line" in
    *\"method\":\"initialize\"*)
      printf '%s\n' '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1}}'
      ;;
    *\"method\":\"session/new\"*)
      printf '%s\n' '{"jsonrpc":"2.0","id":2,"result":{"sessionId":"hermes-acp-session"}}'
      ;;
    *\"method\":\"session/prompt\"*)
      printf '%s\n' '{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"hermes-acp-session","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"hello from acp"}}}}'
      printf '%s\n' '{"jsonrpc":"2.0","id":3,"result":{"stopReason":"end_turn"}}'
      ;;
  esac
done
`
	if err := os.WriteFile(hermesPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake hermes: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	agent := NewHermesAgent("", "", "")
	session, err := agent.StartSession(context.Background(), "")
	if err != nil {
		t.Fatalf("StartSession error: %v", err)
	}
	defer session.Close()

	if got := session.CurrentSessionID(); got != "hermes-acp-session" {
		t.Fatalf("unexpected session id: %q", got)
	}
	if err := session.Send("hello"); err != nil {
		t.Fatalf("Send error: %v", err)
	}

	var gotMessage, gotDone bool
	deadline := time.After(2 * time.Second)
	for !gotMessage || !gotDone {
		select {
		case event := <-session.Events():
			switch event.Type {
			case EventTypeDelta, EventTypeMessage:
				gotMessage = event.Content == "hello from acp"
			case EventTypeDone:
				gotDone = true
			}
		case <-deadline:
			t.Fatalf("timed out waiting for ACP events; message=%v done=%v", gotMessage, gotDone)
		}
	}
}

func TestHermesAgent_ResumeIgnoresACPHistoryReplay(t *testing.T) {
	binDir := t.TempDir()
	hermesPath := filepath.Join(binDir, "hermes")
	script := `#!/bin/sh
while IFS= read -r line; do
  case "$line" in
    *\"method\":\"initialize\"*)
      printf '%s\n' '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1}}'
      ;;
    *\"method\":\"session/resume\"*)
      printf '%s\n' '{"jsonrpc":"2.0","id":2,"result":{}}'
      printf '%s\n' '{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"hermes-existing-session","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"previous answer replay"}}}}'
      ;;
    *\"method\":\"session/prompt\"*)
      printf '%s\n' '{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"hermes-existing-session","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"current answer"}}}}'
      printf '%s\n' '{"jsonrpc":"2.0","id":3,"result":{"stopReason":"end_turn"}}'
      ;;
  esac
done
`
	if err := os.WriteFile(hermesPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake hermes: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	agent := NewHermesAgent("", "", "")
	session, err := agent.StartSession(context.Background(), "hermes-existing-session")
	if err != nil {
		t.Fatalf("StartSession error: %v", err)
	}
	defer session.Close()

	if err := session.Send("next prompt"); err != nil {
		t.Fatalf("Send error: %v", err)
	}

	var gotCurrent, gotDone bool
	deadline := time.After(2 * time.Second)
	for !gotCurrent || !gotDone {
		select {
		case event := <-session.Events():
			switch event.Type {
			case EventTypeDelta, EventTypeMessage:
				if event.Content == "previous answer replay" {
					t.Fatalf("history replay leaked into current turn: %#v", event)
				}
				gotCurrent = event.Content == "current answer"
			case EventTypeDone:
				gotDone = true
			}
		case <-deadline:
			t.Fatalf("timed out waiting for ACP events; current=%v done=%v", gotCurrent, gotDone)
		}
	}
}

func TestHermesACPInitializeParamsMatchHermesSchema(t *testing.T) {
	raw, err := json.Marshal(hermesACPInitializeParams())
	if err != nil {
		t.Fatalf("marshal initialize params: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal initialize params: %v", err)
	}

	caps, ok := got["clientCapabilities"].(map[string]any)
	if !ok {
		t.Fatalf("missing clientCapabilities: %#v", got)
	}
	if terminal, ok := caps["terminal"].(bool); !ok || terminal {
		t.Fatalf("terminal capability must be boolean false for Hermes ACP schema, got %#v", caps["terminal"])
	}
	fs, ok := caps["fs"].(map[string]any)
	if !ok {
		t.Fatalf("missing fs capability: %#v", caps)
	}
	if _, ok := fs["readTextFile"].(bool); !ok {
		t.Fatalf("readTextFile must be a boolean, got %#v", fs["readTextFile"])
	}
	if _, ok := fs["writeTextFile"].(bool); !ok {
		t.Fatalf("writeTextFile must be a boolean, got %#v", fs["writeTextFile"])
	}
}

func TestHermesACPPromptTextUsesSteerDuringActivePrompt(t *testing.T) {
	got := hermesACPPromptText("  add this context  ", true)
	if got != "/steer add this context" {
		t.Fatalf("expected active prompt to become raw /steer command, got %q", got)
	}
}

func TestHermesACPPromptTextWrapsNormalPrompt(t *testing.T) {
	got := hermesACPPromptText("hello", false)
	want := wrapUserPrompt("hello")
	if got != want {
		t.Fatalf("expected normal prompt to use wrapper\nwant: %q\n got: %q", want, got)
	}
}

func TestParseHermesOutputExtractsSessionAndContent(t *testing.T) {
	session := &hermesSession{}
	output := "\n╭─ ⚕ Hermes ─╮\nOK\n\nsession_id: 20260509_165837_579738\n"

	events := parseHermesOutput(session, output)

	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %#v", events)
	}
	if events[0].Type != EventTypeSession || events[0].SessionID != "20260509_165837_579738" {
		t.Fatalf("unexpected session event: %#v", events[0])
	}
	if events[1].Type != EventTypeMessage || strings.TrimSpace(events[1].Content) != "OK" {
		t.Fatalf("unexpected message event: %#v", events[1])
	}
	if events[2].Type != EventTypeDone {
		t.Fatalf("unexpected done event: %#v", events[2])
	}
}

func TestParseHermesOutputSupportsSessionIDLabel(t *testing.T) {
	session := &hermesSession{}
	output := "hello\nSession ID: session-123\n"

	events := parseHermesOutput(session, output)

	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %#v", events)
	}
	if events[0].SessionID != "session-123" {
		t.Fatalf("unexpected session id: %#v", events[0])
	}
	if strings.TrimSpace(events[1].Content) != "hello" {
		t.Fatalf("unexpected content: %#v", events[1])
	}
}
