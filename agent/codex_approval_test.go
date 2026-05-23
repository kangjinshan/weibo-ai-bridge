package agent

import (
	"strings"
	"testing"
)

func TestCodexApprovalToolName(t *testing.T) {
	cases := map[string]string{
		"item/commandExecution/requestApproval": "command_execution",
		"item/fileChange/requestApproval":       "file_change",
		"item/permissions/requestApproval":      "permissions",
		"item/unknown/method":                   "item/unknown/method",
		"":                                      "",
	}
	for method, want := range cases {
		if got := codexApprovalToolName(method); got != want {
			t.Errorf("codexApprovalToolName(%q) = %q, want %q", method, got, want)
		}
	}
}

func TestSummarizeCodexApprovalInput(t *testing.T) {
	t.Run("command prefers command field", func(t *testing.T) {
		got := summarizeCodexApprovalInput("item/commandExecution/requestApproval", map[string]any{
			"command": "  ls -la  ",
			"reason":  "list files",
		})
		if got != "ls -la" {
			t.Errorf("got %q, want trimmed command", got)
		}
	})

	t.Run("command falls back to reason", func(t *testing.T) {
		got := summarizeCodexApprovalInput("item/commandExecution/requestApproval", map[string]any{
			"reason": "  needs sudo  ",
		})
		if got != "needs sudo" {
			t.Errorf("got %q, want trimmed reason", got)
		}
	})

	t.Run("file change uses preview", func(t *testing.T) {
		got := summarizeCodexApprovalInput("item/fileChange/requestApproval", map[string]any{
			"preview": "--- diff ---",
		})
		if got != "--- diff ---" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("permissions uses reason", func(t *testing.T) {
		got := summarizeCodexApprovalInput("item/permissions/requestApproval", map[string]any{
			"reason": "read /etc",
		})
		if got != "read /etc" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("unknown method falls back to JSON", func(t *testing.T) {
		got := summarizeCodexApprovalInput("item/x/y", map[string]any{"k": "v"})
		if !strings.Contains(got, `"k": "v"`) {
			t.Errorf("expected JSON dump, got %q", got)
		}
	})

	t.Run("command empty after trim falls back to JSON", func(t *testing.T) {
		got := summarizeCodexApprovalInput("item/commandExecution/requestApproval", map[string]any{
			"command": "   ",
			"other":   "x",
		})
		if !strings.Contains(got, `"other"`) {
			t.Errorf("expected JSON fallback, got %q", got)
		}
	})
}

func TestCodexParseApprovalRequest(t *testing.T) {
	t.Run("valid command approval populates pending and event", func(t *testing.T) {
		s := &codexInteractiveSession{}
		msg := map[string]any{
			"id":     float64(42),
			"method": "item/commandExecution/requestApproval",
			"params": map[string]any{
				"command": "rm -rf /tmp/x",
			},
		}
		ev, ok := s.parseApprovalRequest(msg)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if ev.Type != EventTypeApproval {
			t.Errorf("type = %v, want approval", ev.Type)
		}
		if ev.ToolName != "command_execution" {
			t.Errorf("tool = %q", ev.ToolName)
		}
		if ev.ToolInput != "rm -rf /tmp/x" {
			t.Errorf("input = %q", ev.ToolInput)
		}
		if s.pendingApproval == nil {
			t.Fatal("pendingApproval not set")
		}
		if s.pendingApproval.method != "item/commandExecution/requestApproval" {
			t.Errorf("pending method = %q", s.pendingApproval.method)
		}
		if id, _ := s.pendingApproval.id.(float64); id != 42 {
			t.Errorf("pending id = %v", s.pendingApproval.id)
		}
	})

	t.Run("nil params returns false and does not set pending", func(t *testing.T) {
		s := &codexInteractiveSession{}
		_, ok := s.parseApprovalRequest(map[string]any{
			"id":     "x",
			"method": "item/commandExecution/requestApproval",
		})
		if ok {
			t.Error("expected ok=false when params missing")
		}
		if s.pendingApproval != nil {
			t.Error("pendingApproval should remain nil")
		}
	})

	t.Run("non-map params returns false", func(t *testing.T) {
		s := &codexInteractiveSession{}
		_, ok := s.parseApprovalRequest(map[string]any{
			"method": "item/commandExecution/requestApproval",
			"params": "nope",
		})
		if ok {
			t.Error("expected ok=false when params wrong type")
		}
	})
}
