package agent

import (
	"strings"
	"testing"
)

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
