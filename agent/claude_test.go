package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
)

type testWriteCloser struct {
	bytes.Buffer
}

func (w *testWriteCloser) Close() error {
	return nil
}

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
	_, err := agent.Execute(context.Background(), "", "test input")
	if err != nil {
		t.Logf("Execute failed (expected if claude CLI is not logged in or not installed): %v", err)
	}
}

func TestClaudeCodeAgent_buildStreamArgs_NewSession(t *testing.T) {
	agent := NewClaudeCodeAgent()
	got := agent.buildStreamArgs("", "hello")
	want := []string{"--print", "--verbose", "--output-format", "stream-json", "--include-partial-messages", wrapUserPrompt("hello")}
	assertSliceEqual(t, got, want)
}

func TestClaudeCodeAgent_buildStreamArgs_ResumeSession(t *testing.T) {
	agent := NewClaudeCodeAgent()
	got := agent.buildStreamArgs("11111111-1111-1111-1111-111111111111", "hello again")
	want := []string{"--print", "--verbose", "--output-format", "stream-json", "--include-partial-messages", "--resume", "11111111-1111-1111-1111-111111111111", wrapUserPrompt("hello again")}
	assertSliceEqual(t, got, want)
}

func TestParseClaudeStreamEvent_AssistantPartial(t *testing.T) {
	state := &claudeStreamState{messageSnapshot: make(map[string]string)}
	events := parseClaudeStreamEvent(state, map[string]any{
		"type":       "assistant",
		"session_id": "session-1",
		"message": map[string]any{
			"id": "msg-1",
			"content": []any{
				map[string]any{"type": "text", "text": "你好"},
			},
		},
	})

	if len(events) != 1 || events[0].Type != EventTypeDelta || events[0].Content != "你好" {
		t.Fatalf("unexpected events: %+v", events)
	}
	if state.sessionID != "session-1" {
		t.Fatalf("unexpected session id: %q", state.sessionID)
	}
}

func TestParseClaudeStreamEvent_AssistantGrowingSnapshot(t *testing.T) {
	state := &claudeStreamState{
		sessionID:       "session-1",
		messageSnapshot: map[string]string{"msg-1": "你好"},
		lastMessageID:   "msg-1",
	}
	events := parseClaudeStreamEvent(state, map[string]any{
		"type":       "assistant",
		"session_id": "session-1",
		"message": map[string]any{
			"id": "msg-1",
			"content": []any{
				map[string]any{"type": "text", "text": "你好呀"},
			},
		},
	})

	if len(events) != 1 || events[0].Type != EventTypeDelta || events[0].Content != "呀" {
		t.Fatalf("unexpected events: %+v", events)
	}
}

func TestParseClaudeStreamEvent_StreamEventTextDelta(t *testing.T) {
	state := &claudeStreamState{messageSnapshot: make(map[string]string)}

	startEvents := parseClaudeStreamEvent(state, map[string]any{
		"type":       "stream_event",
		"session_id": "session-1",
		"event": map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id": "msg-1",
			},
		},
	})

	if len(startEvents) != 1 || startEvents[0].Type != EventTypeSession || startEvents[0].SessionID != "session-1" {
		t.Fatalf("unexpected start events: %+v", startEvents)
	}

	events := parseClaudeStreamEvent(state, map[string]any{
		"type":       "stream_event",
		"session_id": "session-1",
		"event": map[string]any{
			"type": "content_block_delta",
			"delta": map[string]any{
				"type": "text_delta",
				"text": "春天的早晨，",
			},
		},
	})

	if len(events) != 1 || events[0].Type != EventTypeDelta || events[0].Content != "春天的早晨，" {
		t.Fatalf("unexpected delta events: %+v", events)
	}
	if state.lastMessageID != "msg-1" {
		t.Fatalf("unexpected last message id: %q", state.lastMessageID)
	}
	if got := state.messageSnapshot["msg-1"]; got != "春天的早晨，" {
		t.Fatalf("unexpected snapshot: %q", got)
	}
}

func TestParseClaudeStreamEvent_StreamEventThinkingDeltaIgnored(t *testing.T) {
	state := &claudeStreamState{
		sessionID:       "session-1",
		messageSnapshot: map[string]string{"msg-1": ""},
		lastMessageID:   "msg-1",
	}

	events := parseClaudeStreamEvent(state, map[string]any{
		"type":       "stream_event",
		"session_id": "session-1",
		"event": map[string]any{
			"type": "content_block_delta",
			"delta": map[string]any{
				"type":     "thinking_delta",
				"thinking": "internal",
			},
		},
	})

	if len(events) != 0 {
		t.Fatalf("expected no events, got %+v", events)
	}
	if got := state.messageSnapshot["msg-1"]; got != "" {
		t.Fatalf("unexpected snapshot: %q", got)
	}
}

func TestParseClaudeStreamEvent_StreamEventDeltaAvoidsDuplicateAssistantSnapshot(t *testing.T) {
	state := &claudeStreamState{messageSnapshot: make(map[string]string)}

	parseClaudeStreamEvent(state, map[string]any{
		"type":       "stream_event",
		"session_id": "session-1",
		"event": map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id": "msg-1",
			},
		},
	})
	parseClaudeStreamEvent(state, map[string]any{
		"type":       "stream_event",
		"session_id": "session-1",
		"event": map[string]any{
			"type": "content_block_delta",
			"delta": map[string]any{
				"type": "text_delta",
				"text": "你好",
			},
		},
	})

	events := parseClaudeStreamEvent(state, map[string]any{
		"type":       "assistant",
		"session_id": "session-1",
		"message": map[string]any{
			"id": "msg-1",
			"content": []any{
				map[string]any{"type": "text", "text": "你好呀"},
			},
		},
	})

	if len(events) != 1 || events[0].Type != EventTypeDelta || events[0].Content != "呀" {
		t.Fatalf("unexpected events: %+v", events)
	}
}

func TestParseClaudeStreamEvent_ResultAvoidsDuplicateFinal(t *testing.T) {
	state := &claudeStreamState{
		sessionID:       "session-1",
		messageSnapshot: map[string]string{"msg-1": "你好呀"},
		lastMessageID:   "msg-1",
	}
	events := parseClaudeStreamEvent(state, map[string]any{
		"type":       "result",
		"session_id": "session-1",
		"is_error":   false,
		"result":     "你好呀",
	})

	if len(events) != 1 || events[0].Type != EventTypeDone {
		t.Fatalf("unexpected events: %+v", events)
	}
}

func TestClaudeInteractiveSession_RespondApproval(t *testing.T) {
	tests := []struct {
		name             string
		action           ApprovalAction
		expectedBehavior string
		expectInput      bool
	}{
		{
			name:             "allow",
			action:           ApprovalActionAllow,
			expectedBehavior: "allow",
			expectInput:      true,
		},
		{
			name:             "allow all",
			action:           ApprovalActionAllowAll,
			expectedBehavior: "allow",
			expectInput:      true,
		},
		{
			name:             "cancel",
			action:           ApprovalActionCancel,
			expectedBehavior: "deny",
			expectInput:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdin := &testWriteCloser{}
			session := &claudeInteractiveSession{
				stdin: stdin,
				pending: &claudePendingApproval{
					requestID: "req-1",
					input: map[string]any{
						"command": "ls -la",
					},
				},
			}
			session.alive.Store(true)

			err := session.RespondApproval(tt.action)
			if err != nil {
				t.Fatalf("RespondApproval returned error: %v", err)
			}

			var payload map[string]any
			if err := json.Unmarshal(bytes.TrimSpace(stdin.Bytes()), &payload); err != nil {
				t.Fatalf("failed to parse payload: %v", err)
			}

			if payload["type"] != "control_response" {
				t.Fatalf("unexpected type: %v", payload["type"])
			}

			response, _ := payload["response"].(map[string]any)
			if response["request_id"] != "req-1" {
				t.Fatalf("unexpected request id: %v", response["request_id"])
			}

			inner, _ := response["response"].(map[string]any)
			if inner["behavior"] != tt.expectedBehavior {
				t.Fatalf("unexpected behavior: %v", inner["behavior"])
			}

			_, hasUpdatedInput := inner["updatedInput"]
			if hasUpdatedInput != tt.expectInput {
				t.Fatalf("updatedInput presence = %v, want %v", hasUpdatedInput, tt.expectInput)
			}
		})
	}
}

func TestParseClaudeStreamEvent_ResultEmitsRemainingSuffix(t *testing.T) {
	state := &claudeStreamState{
		sessionID:       "session-1",
		messageSnapshot: map[string]string{"msg-1": "你好"},
		lastMessageID:   "msg-1",
	}
	events := parseClaudeStreamEvent(state, map[string]any{
		"type":       "result",
		"session_id": "session-1",
		"is_error":   false,
		"result":     "你好呀",
	})

	if len(events) != 2 {
		t.Fatalf("unexpected events: %+v", events)
	}
	if events[0].Type != EventTypeMessage || events[0].Content != "呀" {
		t.Fatalf("unexpected message event: %+v", events[0])
	}
	if events[1].Type != EventTypeDone {
		t.Fatalf("unexpected done event: %+v", events[1])
	}
}

func TestParseClaudeStreamEvent_ResultError(t *testing.T) {
	state := &claudeStreamState{messageSnapshot: make(map[string]string)}
	events := parseClaudeStreamEvent(state, map[string]any{
		"type":       "result",
		"session_id": "session-1",
		"is_error":   true,
		"result":     "Not logged in · Please run /login",
	})

	if len(events) != 3 {
		t.Fatalf("unexpected events: %+v", events)
	}
	if events[0].Type != EventTypeSession || events[0].SessionID != "session-1" {
		t.Fatalf("unexpected first event: %+v", events[0])
	}
	if events[1].Type != EventTypeError {
		t.Fatalf("unexpected second event: %+v", events[1])
	}
	if events[2].Type != EventTypeDone {
		t.Fatalf("unexpected third event: %+v", events[2])
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
