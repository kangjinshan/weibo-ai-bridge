package agent

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

type nopWriteCloser struct{ *bytes.Buffer }

func (nopWriteCloser) Close() error { return nil }

func TestSummarizeApprovalInput(t *testing.T) {
	t.Run("empty input returns empty", func(t *testing.T) {
		if got := summarizeApprovalInput("Bash", nil); got != "" {
			t.Errorf("got %q", got)
		}
		if got := summarizeApprovalInput("Bash", map[string]any{}); got != "" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("command field wins", func(t *testing.T) {
		got := summarizeApprovalInput("Bash", map[string]any{
			"command":   "  ls  ",
			"file_path": "/etc",
		})
		if got != "ls" {
			t.Errorf("got %q, want ls", got)
		}
	})

	t.Run("file_path used when no command", func(t *testing.T) {
		got := summarizeApprovalInput("Edit", map[string]any{
			"file_path": " /tmp/x ",
			"prompt":    "ignored",
		})
		if got != "/tmp/x" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("prompt used when no command/file_path", func(t *testing.T) {
		got := summarizeApprovalInput("Task", map[string]any{
			"prompt": "  do thing  ",
		})
		if got != "do thing" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("falls back to JSON dump", func(t *testing.T) {
		got := summarizeApprovalInput("Unknown", map[string]any{
			"arbitrary": "value",
		})
		if !strings.Contains(got, `"arbitrary": "value"`) {
			t.Errorf("expected JSON dump, got %q", got)
		}
	})

	t.Run("blank string fields trigger JSON fallback", func(t *testing.T) {
		got := summarizeApprovalInput("Bash", map[string]any{
			"command":   "   ",
			"file_path": "",
			"prompt":    "",
			"other":     "x",
		})
		if !strings.Contains(got, `"other"`) {
			t.Errorf("expected JSON fallback, got %q", got)
		}
	})
}

func TestClaudeParseControlRequest(t *testing.T) {
	t.Run("valid can_use_tool populates pending and event", func(t *testing.T) {
		s := &claudeInteractiveSession{}
		raw := map[string]any{
			"type":       "control_request",
			"request_id": "req-1",
			"request": map[string]any{
				"subtype":   "can_use_tool",
				"tool_name": "Bash",
				"input": map[string]any{
					"command": "echo hi",
				},
			},
		}
		ev, ok := s.parseControlRequest(raw)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if ev.Type != EventTypeApproval {
			t.Errorf("type = %v", ev.Type)
		}
		if ev.ToolName != "Bash" {
			t.Errorf("tool = %q", ev.ToolName)
		}
		if ev.ToolInput != "echo hi" {
			t.Errorf("input = %q", ev.ToolInput)
		}
		if s.pending == nil {
			t.Fatal("pending not set")
		}
		if s.pending.requestID != "req-1" {
			t.Errorf("pending requestID = %q", s.pending.requestID)
		}
	})

	t.Run("wrong type returns false", func(t *testing.T) {
		s := &claudeInteractiveSession{}
		_, ok := s.parseControlRequest(map[string]any{"type": "something_else"})
		if ok {
			t.Error("expected ok=false")
		}
		if s.pending != nil {
			t.Error("pending should remain nil")
		}
	})

	t.Run("missing request returns false", func(t *testing.T) {
		s := &claudeInteractiveSession{}
		_, ok := s.parseControlRequest(map[string]any{
			"type":       "control_request",
			"request_id": "x",
		})
		if ok {
			t.Error("expected ok=false")
		}
	})

	t.Run("subtype not can_use_tool returns false", func(t *testing.T) {
		s := &claudeInteractiveSession{}
		_, ok := s.parseControlRequest(map[string]any{
			"type": "control_request",
			"request": map[string]any{
				"subtype": "interrupt",
			},
		})
		if ok {
			t.Error("expected ok=false")
		}
		if s.pending != nil {
			t.Error("pending should remain nil")
		}
	})

	t.Run("empty input still produces approval event", func(t *testing.T) {
		s := &claudeInteractiveSession{}
		ev, ok := s.parseControlRequest(map[string]any{
			"type":       "control_request",
			"request_id": "r",
			"request": map[string]any{
				"subtype":   "can_use_tool",
				"tool_name": "Read",
			},
		})
		if !ok {
			t.Fatal("expected ok=true")
		}
		if ev.ToolName != "Read" {
			t.Errorf("tool = %q", ev.ToolName)
		}
		if ev.ToolInput != "" {
			t.Errorf("input should be empty, got %q", ev.ToolInput)
		}
	})
}

func TestParseUserQuestions(t *testing.T) {
	t.Run("parses questions with options", func(t *testing.T) {
		input := map[string]any{
			"questions": []any{
				map[string]any{
					"question":    "选哪个框架?",
					"header":      "框架",
					"multiSelect": true,
					"options": []any{
						map[string]any{"label": "React", "description": "前端库"},
						map[string]any{"label": "Vue"},
					},
				},
			},
		}
		got := parseUserQuestions(input)
		if len(got) != 1 {
			t.Fatalf("len = %d", len(got))
		}
		q := got[0]
		if q.Question != "选哪个框架?" || q.Header != "框架" || !q.MultiSelect {
			t.Errorf("unexpected question: %+v", q)
		}
		if len(q.Options) != 2 || q.Options[0].Label != "React" || q.Options[0].Description != "前端库" || q.Options[1].Label != "Vue" {
			t.Errorf("unexpected options: %+v", q.Options)
		}
	})

	t.Run("skips questions without text", func(t *testing.T) {
		input := map[string]any{
			"questions": []any{
				map[string]any{"question": ""},
				map[string]any{"question": "real"},
			},
		}
		got := parseUserQuestions(input)
		if len(got) != 1 || got[0].Question != "real" {
			t.Errorf("unexpected: %+v", got)
		}
	})

	t.Run("missing questions returns nil", func(t *testing.T) {
		if got := parseUserQuestions(map[string]any{}); got != nil {
			t.Errorf("expected nil, got %+v", got)
		}
	})
}

func TestParseControlRequestAskUserQuestion(t *testing.T) {
	s := &claudeInteractiveSession{}
	raw := map[string]any{
		"type":       "control_request",
		"request_id": "req-ask",
		"request": map[string]any{
			"subtype":   "can_use_tool",
			"tool_name": "AskUserQuestion",
			"input": map[string]any{
				"questions": []any{
					map[string]any{
						"question": "继续吗?",
						"options": []any{
							map[string]any{"label": "是"},
							map[string]any{"label": "否"},
						},
					},
				},
			},
		},
	}
	ev, ok := s.parseControlRequest(raw)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if ev.Type != EventTypeApproval {
		t.Errorf("type = %v", ev.Type)
	}
	if len(ev.Questions) != 1 || ev.Questions[0].Question != "继续吗?" {
		t.Errorf("questions = %+v", ev.Questions)
	}
	if s.pending == nil || s.pending.requestID != "req-ask" {
		t.Errorf("pending not set correctly: %+v", s.pending)
	}
}

func TestRespondQuestionAnswers(t *testing.T) {
	buf := &bytes.Buffer{}
	s := &claudeInteractiveSession{
		stdin: nopWriteCloser{buf},
		pending: &claudePendingApproval{
			requestID: "req-ask",
			input: map[string]any{
				"questions": []any{map[string]any{"question": "q"}},
			},
		},
	}
	s.alive.Store(true)

	if err := s.RespondQuestionAnswers(map[int]string{0: "选项A", 1: "选项B"}); err != nil {
		t.Fatalf("RespondQuestionAnswers: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	response, _ := payload["response"].(map[string]any)
	if response["request_id"] != "req-ask" {
		t.Errorf("request_id = %v", response["request_id"])
	}
	inner, _ := response["response"].(map[string]any)
	if inner["behavior"] != "allow" {
		t.Errorf("behavior = %v", inner["behavior"])
	}
	updated, _ := inner["updatedInput"].(map[string]any)
	answers, _ := updated["answers"].(map[string]any)
	if answers["0"] != "选项A" || answers["1"] != "选项B" {
		t.Errorf("answers = %+v", answers)
	}
	if _, ok := updated["questions"]; !ok {
		t.Errorf("updatedInput should preserve original input fields: %+v", updated)
	}

	if s.pending != nil {
		t.Errorf("pending should be cleared")
	}
}

func TestRespondQuestionAnswersNoPending(t *testing.T) {
	s := &claudeInteractiveSession{stdin: nopWriteCloser{&bytes.Buffer{}}}
	s.alive.Store(true)
	if err := s.RespondQuestionAnswers(map[int]string{0: "x"}); err == nil {
		t.Error("expected error when no pending approval")
	}
}
