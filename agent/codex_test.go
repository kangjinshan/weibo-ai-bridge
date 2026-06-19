package agent

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestBuildCodexThreadResumeParams_UsesMinimalResumePayload(t *testing.T) {
	params := buildCodexThreadResumeParams("thread-123")

	expected := map[string]any{
		"threadId":               "thread-123",
		"persistExtendedHistory": true,
	}

	if !reflect.DeepEqual(params, expected) {
		t.Fatalf("unexpected params: got %#v want %#v", params, expected)
	}
}

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

func TestResolveCodexCommandSpec_WindowsPrefersCmdShimOverPackagedWindowsAppsExe(t *testing.T) {
	packagedExe := `C:\Program Files\WindowsApps\OpenAI.Codex_26.616.3309.0_x64__2p2nqsd0c76g0\app\resources\codex.exe`
	cmdShim := `C:\Users\alice\AppData\Roaming\npm\codex.cmd`

	spec, err := resolveCodexCommandSpecFor("windows", func(file string) (string, error) {
		switch file {
		case "codex":
			return packagedExe, nil
		case "codex.cmd":
			return cmdShim, nil
		default:
			return "", exec.ErrNotFound
		}
	})
	if err != nil {
		t.Fatalf("resolveCodexCommandSpecFor returned error: %v", err)
	}
	if spec.command != "cmd.exe" {
		t.Fatalf("unexpected command: got %q want cmd.exe", spec.command)
	}
	wantPrefix := []string{"/d", "/s", "/c", cmdShim}
	if !reflect.DeepEqual(spec.argsPrefix, wantPrefix) {
		t.Fatalf("unexpected args prefix: got %v want %v", spec.argsPrefix, wantPrefix)
	}
}

func TestResolveCodexCommandSpec_WindowsRunsBatchCommandViaCmdExe(t *testing.T) {
	cmdShim := `C:\Users\alice\AppData\Roaming\npm\codex.cmd`

	spec, err := resolveCodexCommandSpecFor("windows", func(file string) (string, error) {
		if file == "codex" {
			return cmdShim, nil
		}
		return "", exec.ErrNotFound
	})
	if err != nil {
		t.Fatalf("resolveCodexCommandSpecFor returned error: %v", err)
	}
	if spec.command != "cmd.exe" {
		t.Fatalf("unexpected command: got %q want cmd.exe", spec.command)
	}
	wantPrefix := []string{"/d", "/s", "/c", cmdShim}
	if !reflect.DeepEqual(spec.argsPrefix, wantPrefix) {
		t.Fatalf("unexpected args prefix: got %v want %v", spec.argsPrefix, wantPrefix)
	}
}

func TestResolveCodexCommandSpec_WindowsUsesShellForPackagedWindowsAppsExeWithoutShim(t *testing.T) {
	t.Setenv("LOCALAPPDATA", filepath.Join(t.TempDir(), "missing"))
	packagedExe := `C:\Program Files\WindowsApps\OpenAI.Codex_26.616.3309.0_x64__2p2nqsd0c76g0\app\resources\codex.exe`

	spec, err := resolveCodexCommandSpecFor("windows", func(file string) (string, error) {
		if file == "codex" {
			return packagedExe, nil
		}
		return "", exec.ErrNotFound
	})
	if err != nil {
		t.Fatalf("resolveCodexCommandSpecFor returned error: %v", err)
	}
	if spec.command != "cmd.exe" {
		t.Fatalf("unexpected command: got %q want cmd.exe", spec.command)
	}
	wantPrefix := []string{"/d", "/s", "/c", "codex"}
	if !reflect.DeepEqual(spec.argsPrefix, wantPrefix) {
		t.Fatalf("unexpected args prefix: got %v want %v", spec.argsPrefix, wantPrefix)
	}
}

func TestResolveCodexCommandSpec_WindowsPrefersDesktopBundleOverPackagedWindowsAppsExe(t *testing.T) {
	localAppData := t.TempDir()
	t.Setenv("LOCALAPPDATA", localAppData)
	desktopExe := filepath.Join(localAppData, "OpenAI", "Codex", "bin", "hash", "codex.exe")
	if err := os.MkdirAll(filepath.Dir(desktopExe), 0o755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	if err := os.WriteFile(desktopExe, []byte("fake"), 0o755); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	packagedExe := `C:\Program Files\WindowsApps\OpenAI.Codex_26.616.3309.0_x64__2p2nqsd0c76g0\app\resources\codex.exe`
	spec, err := resolveCodexCommandSpecFor("windows", func(file string) (string, error) {
		if file == "codex" {
			return packagedExe, nil
		}
		return "", exec.ErrNotFound
	})
	if err != nil {
		t.Fatalf("resolveCodexCommandSpecFor returned error: %v", err)
	}
	if spec.command != desktopExe {
		t.Fatalf("unexpected command: got %q want %q", spec.command, desktopExe)
	}
	if len(spec.argsPrefix) != 0 {
		t.Fatalf("unexpected args prefix: got %v want empty", spec.argsPrefix)
	}
}

func TestResolveCodexCommandSpec_ReturnsNotFoundWhenCodexMissing(t *testing.T) {
	_, err := resolveCodexCommandSpecFor("linux", func(file string) (string, error) {
		return "", exec.ErrNotFound
	})
	if !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCodeXAgent_ExecuteStream(t *testing.T) {
	agent := NewCodeXAgent("gpt-4.5")
	events, err := agent.ExecuteStream(context.Background(), "", "test input")
	if err != nil {
		t.Logf("ExecuteStream failed (expected if codex CLI is not configured): %v", err)
		return
	}
	for range events {
	}
}

func TestCodeXAgent_buildCommand_NewSession(t *testing.T) {
	t.Setenv("LOCALAPPDATA", filepath.Join(t.TempDir(), "missing"))
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
	t.Setenv("LOCALAPPDATA", filepath.Join(t.TempDir(), "missing"))
	agent := NewCodeXAgent("")

	cmd := agent.buildCommand(context.Background(), &codexSession{}, "hello")

	want := []string{"codex", "-a", "never", "exec", "--skip-git-repo-check", "--json", "-"}
	if strings.Join(cmd.Args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("unexpected args: got %v want %v", cmd.Args, want)
	}
}

func TestCodeXAgent_buildCommand_ResumeSession(t *testing.T) {
	t.Setenv("LOCALAPPDATA", filepath.Join(t.TempDir(), "missing"))
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

func TestCodeXAgent_buildCommand_UsesWorkDirFromContext(t *testing.T) {
	agent := NewCodeXAgent("")
	workDir := t.TempDir()
	ctx := WithWorkDir(context.Background(), workDir)

	cmd := agent.buildCommand(ctx, &codexSession{}, "hello")

	if cmd.Dir != workDir {
		t.Fatalf("unexpected cmd dir: got %q want %q", cmd.Dir, workDir)
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
	errorParts, err := agent.streamCodexOutput(context.Background(), session, stdout, events)
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
	_, err := agent.streamCodexOutput(context.Background(), session, stdout, events)
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
	_, err := agent.streamCodexOutput(context.Background(), session, stdout, events)
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
	errorParts, err := agent.streamCodexOutput(context.Background(), session, stdout, events)
	close(events)
	if err != nil {
		t.Fatalf("streamCodexOutput returned error: %v", err)
	}
	if len(errorParts) != 1 || errorParts[0] != "upstream failed" {
		t.Fatalf("unexpected errors: %v", errorParts)
	}
}

func TestParseCodexEvent_ItemStartedEmitsToolStart(t *testing.T) {
	events := parseCodexEvent(&codexSession{}, map[string]any{
		"type": "item.started",
		"item": map[string]any{
			"type":    "command_execution",
			"command": "go test ./...",
		},
	})

	if len(events) != 1 || events[0].Type != EventTypeToolStart {
		t.Fatalf("unexpected events: %+v", events)
	}
	if events[0].Metadata["command"] != "go test ./..." {
		t.Fatalf("unexpected metadata: %+v", events[0].Metadata)
	}
}

func TestParseCodexItemCompleted_CommandExecutionEmitsToolEnd(t *testing.T) {
	events := parseCodexItemCompleted(map[string]any{
		"type":              "command_execution",
		"command":           "go test ./router",
		"aggregated_output": "ok",
		"exit_code":         float64(0),
	})

	if len(events) != 1 || events[0].Type != EventTypeToolEnd {
		t.Fatalf("unexpected events: %+v", events)
	}
	metadata := events[0].Metadata
	if metadata["command"] != "go test ./router" || metadata["aggregated_output"] != "ok" || metadata["exit_code"] != 0 {
		t.Fatalf("unexpected metadata: %+v", metadata)
	}
}

func TestExtractMessageTextSupportsPartsAndMessageFallbacks(t *testing.T) {
	got := extractMessageText(map[string]any{
		"type": "message",
		"parts": []any{
			map[string]any{"type": "text", "text": "第一段"},
			map[string]any{"type": "image", "text": "忽略"},
			map[string]any{"type": "text", "text": "第二段"},
		},
	})
	if got != "第一段\n第二段" {
		t.Fatalf("unexpected parts text: %q", got)
	}

	got = extractMessageText(map[string]any{
		"type":    "agent_message",
		"message": "fallback message",
	})
	if got != "fallback message" {
		t.Fatalf("unexpected fallback message: %q", got)
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
	session.threadID.Store("thread-1")
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

func TestParseCodexAppServerMessage_UpdatesThreadIDFromNotifications(t *testing.T) {
	session := &codexSession{}
	session.threadID.Store("thread-old")
	deltaSeen := make(map[string]bool)

	events := parseCodexAppServerMessage(session, map[string]any{
		"method": "turn/started",
		"params": map[string]any{
			"threadId": "thread-new",
			"turnId":   "turn-1",
		},
	}, deltaSeen)

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d (%+v)", len(events), events)
	}
	if events[0].Type != EventTypeSession || events[0].SessionID != "thread-new" {
		t.Fatalf("unexpected session event: %+v", events[0])
	}
	if got := session.CurrentSessionID(); got != "thread-new" {
		t.Fatalf("unexpected session id: got %q want %q", got, "thread-new")
	}
}

func TestParseCodexAppServerMessage_DoesNotEmitSessionEventWhenThreadUnchanged(t *testing.T) {
	session := &codexSession{}
	session.threadID.Store("thread-same")
	deltaSeen := make(map[string]bool)

	events := parseCodexAppServerMessage(session, map[string]any{
		"method": "turn/started",
		"params": map[string]any{
			"threadId": "thread-same",
			"turnId":   "turn-1",
		},
	}, deltaSeen)

	if len(events) != 0 {
		t.Fatalf("expected no events, got %+v", events)
	}
	if got := session.CurrentSessionID(); got != "thread-same" {
		t.Fatalf("unexpected session id: got %q want %q", got, "thread-same")
	}
}

func TestParseCodexAppServerMessage_FinalMessageSkippedAfterDelta(t *testing.T) {
	session := &codexSession{}
	session.threadID.Store("thread-1")
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
	session.threadID.Store("thread-1")
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

func TestParseCodexAppServerMessage_ErrorNotification(t *testing.T) {
	session := &codexSession{}
	session.threadID.Store("thread-1")
	deltaSeen := make(map[string]bool)

	events := parseCodexAppServerMessage(session, map[string]any{
		"method": "error",
		"params": map[string]any{
			"threadId": "thread-1",
			"turnId":   "turn-1",
			"error": map[string]any{
				"message": "{\"detail\":\"The 'gpt-4' model is not supported when using Codex with a ChatGPT account.\"}",
			},
			"willRetry": false,
		},
	}, deltaSeen)

	if len(events) != 1 || events[0].Type != EventTypeError {
		t.Fatalf("unexpected events: %+v", events)
	}
	if events[0].Error != "The 'gpt-4' model is not supported when using Codex with a ChatGPT account." {
		t.Fatalf("unexpected error: %q", events[0].Error)
	}
}

func TestParseCodexAppServerMessage_FailedTurnCompletedEmitsErrorBeforeDone(t *testing.T) {
	session := &codexSession{}
	session.threadID.Store("thread-1")
	deltaSeen := make(map[string]bool)

	events := parseCodexAppServerMessage(session, map[string]any{
		"method": "turn/completed",
		"params": map[string]any{
			"threadId": "thread-1",
			"turn": map[string]any{
				"id":     "turn-1",
				"status": "failed",
				"error": map[string]any{
					"message": "plain failure",
				},
			},
		},
	}, deltaSeen)

	if len(events) != 2 {
		t.Fatalf("expected error and done, got %+v", events)
	}
	if events[0].Type != EventTypeError || events[0].Error != "plain failure" {
		t.Fatalf("unexpected error event: %+v", events[0])
	}
	if events[1].Type != EventTypeDone {
		t.Fatalf("unexpected done event: %+v", events[1])
	}
}

func TestShouldIgnoreCodexAppServerReadError(t *testing.T) {
	err := &websocket.CloseError{Code: websocket.CloseAbnormalClosure, Text: "unexpected EOF"}

	if !shouldIgnoreCodexAppServerReadError(err, false) {
		t.Fatal("expected idle abnormal closure to be ignored")
	}
	if shouldIgnoreCodexAppServerReadError(err, true) {
		t.Fatal("expected active-turn abnormal closure to surface as error")
	}
}

func TestCodexAppServerReadTimeout(t *testing.T) {
	if codexAppServerReadTimeout != 5*time.Minute {
		t.Fatalf("unexpected codex app-server read timeout: %v", codexAppServerReadTimeout)
	}
}

func TestCodexApprovalResult(t *testing.T) {
	tests := []struct {
		name     string
		pending  *codexPendingApproval
		action   ApprovalAction
		expected map[string]any
	}{
		{
			name: "command allow",
			pending: &codexPendingApproval{
				method: "item/commandExecution/requestApproval",
			},
			action:   ApprovalActionAllow,
			expected: map[string]any{"decision": "accept"},
		},
		{
			name: "command allow all",
			pending: &codexPendingApproval{
				method: "item/commandExecution/requestApproval",
			},
			action:   ApprovalActionAllowAll,
			expected: map[string]any{"decision": "acceptForSession"},
		},
		{
			name: "file change cancel",
			pending: &codexPendingApproval{
				method: "item/fileChange/requestApproval",
			},
			action:   ApprovalActionCancel,
			expected: map[string]any{"decision": "cancel"},
		},
		{
			name: "permissions allow all",
			pending: &codexPendingApproval{
				method: "item/permissions/requestApproval",
				params: map[string]any{
					"permissions": map[string]any{"disk": "write"},
				},
			},
			action: ApprovalActionAllowAll,
			expected: map[string]any{
				"permissions": map[string]any{"disk": "write"},
				"scope":       "session",
			},
		},
		{
			name: "permissions cancel",
			pending: &codexPendingApproval{
				method: "item/permissions/requestApproval",
				params: map[string]any{
					"permissions": map[string]any{"disk": "write"},
				},
			},
			action: ApprovalActionCancel,
			expected: map[string]any{
				"permissions": map[string]any{},
				"scope":       "turn",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := codexApprovalResult(tt.pending, tt.action)
			if err != nil {
				t.Fatalf("codexApprovalResult returned error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.expected) {
				t.Fatalf("unexpected result: got %#v want %#v", got, tt.expected)
			}
		})
	}
}
