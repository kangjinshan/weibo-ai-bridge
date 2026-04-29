package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync/atomic"
)

// CodeXAgent CodeX CLI Agent 实现
type CodeXAgent struct {
	name  string
	model string
}

// NewCodeXAgent 创建新的 CodeX Agent
func NewCodeXAgent(model string) *CodeXAgent {
	return &CodeXAgent{
		name:  "codex",
		model: model,
	}
}

// Name 返回 Agent 名称
func (a *CodeXAgent) Name() string {
	return a.name
}

// codexSession 用于管理 Codex 会话状态
type codexSession struct {
	threadID atomic.Value
}

// Execute 执行 AI 任务（带会话 ID 支持）
func (a *CodeXAgent) Execute(ctx context.Context, sessionID string, input string) (string, error) {
	events, err := a.ExecuteStream(ctx, sessionID, input)
	if err != nil {
		return "", err
	}

	var responseParts []string
	var errorParts []string
	var latestSessionID string

	for event := range events {
		switch event.Type {
		case EventTypeSession:
			if strings.TrimSpace(event.SessionID) != "" {
				latestSessionID = strings.TrimSpace(event.SessionID)
			}
		case EventTypeDelta, EventTypeMessage:
			if strings.TrimSpace(event.Content) != "" {
				responseParts = append(responseParts, event.Content)
			}
		case EventTypeError:
			if strings.TrimSpace(event.Error) != "" {
				errorParts = append(errorParts, event.Error)
			}
		}
	}

	if len(errorParts) > 0 {
		return "", fmt.Errorf("%s", strings.Join(uniqueNonEmpty(errorParts), "\n"))
	}

	response := strings.Join(responseParts, "\n")
	if latestSessionID != "" {
		response += "\n\n__SESSION_ID__: " + latestSessionID
	}

	return response, nil
}

// ExecuteStream 执行 AI 任务并返回结构化事件流。
func (a *CodeXAgent) ExecuteStream(ctx context.Context, sessionID string, input string) (<-chan Event, error) {
	if !a.IsAvailable() {
		return nil, fmt.Errorf("codex CLI is not available")
	}

	session := &codexSession{}
	if sessionID != "" {
		session.threadID.Store(sessionID)
	}

	if events, err := a.executeViaAppServer(ctx, session, input); err == nil {
		return events, nil
	}

	return a.executeViaJSONCLI(ctx, session, input)
}

func (a *CodeXAgent) executeViaJSONCLI(ctx context.Context, session *codexSession, input string) (<-chan Event, error) {

	cmd := a.buildCommand(ctx, session, input)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to get stdout pipe: %w", err)
	}

	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start codex CLI: %w", err)
	}

	events := make(chan Event, 32)

	go func() {
		defer close(events)

		errorParts, readErr := a.streamCodexOutput(session, stdout, events)
		if readErr != nil {
			sendEvent(events, Event{Type: EventTypeError, Error: readErr.Error()})
			return
		}

		if err := cmd.Wait(); err != nil {
			if ctx.Err() != nil {
				return
			}

			details := joinNonEmpty(errorParts, cleanCodexStderr(stderrBuf.String()))
			if details == "" {
				details = err.Error()
			}
			sendEvent(events, Event{
				Type:  EventTypeError,
				Error: fmt.Sprintf("codex CLI failed: %s", details),
			})
			return
		}

		sendEvent(events, Event{Type: EventTypeDone})
	}()

	return events, nil
}

// CurrentSessionID 获取当前会话 ID
func (cs *codexSession) CurrentSessionID() string {
	v, _ := cs.threadID.Load().(string)
	return v
}

func (cs *codexSession) SetCurrentSessionID(threadID string) bool {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return false
	}

	if cs.CurrentSessionID() == threadID {
		return false
	}

	cs.threadID.Store(threadID)
	return true
}

func (a *CodeXAgent) buildCommand(ctx context.Context, session *codexSession, input string) *exec.Cmd {
	threadID := session.CurrentSessionID()
	isResume := threadID != ""
	prompt := wrapUserPrompt(input)

	args := []string{"-a", "never"}
	if strings.TrimSpace(a.model) != "" {
		args = append(args, "-m", strings.TrimSpace(a.model))
	}

	if isResume {
		args = append(args,
			"exec", "resume",
			"--skip-git-repo-check",
			"--json",
			threadID,
			"-",
		)
	} else {
		args = append(args,
			"exec",
			"--skip-git-repo-check",
			"--json",
			"-",
		)
	}

	cmd := exec.CommandContext(ctx, "codex", args...)
	if workDir := WorkDirFromContext(ctx); workDir != "" {
		cmd.Dir = workDir
	}
	cmd.Stdin = strings.NewReader(prompt)

	return cmd
}

func (a *CodeXAgent) streamCodexOutput(session *codexSession, stdout io.ReadCloser, events chan<- Event) ([]string, error) {
	reader := bufio.NewReader(stdout)
	var errorParts []string

	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return errorParts, fmt.Errorf("failed to read output: %w", err)
		}

		line = bytes.TrimRight(line, "\r\n")
		if len(line) == 0 {
			continue
		}

		var raw map[string]any
		if err := json.Unmarshal(line, &raw); err != nil {
			continue
		}

		for _, event := range parseCodexEvent(session, raw) {
			if event.Type == EventTypeError && strings.TrimSpace(event.Error) != "" {
				errorParts = append(errorParts, event.Error)
			}
			sendEvent(events, event)
		}
	}

	return uniqueNonEmpty(errorParts), nil
}

func parseCodexEvent(session *codexSession, raw map[string]any) []Event {
	eventType, _ := raw["type"].(string)

	switch eventType {
	case "thread.started":
		if tid, ok := raw["thread_id"].(string); ok && session.SetCurrentSessionID(tid) {
			return []Event{{Type: EventTypeSession, SessionID: tid}}
		}
		return nil

	case "session_meta":
		if payload, ok := raw["payload"].(map[string]any); ok {
			if tid, ok := payload["id"].(string); ok && session.SetCurrentSessionID(tid) {
				return []Event{{Type: EventTypeSession, SessionID: tid}}
			}
		}
		return nil

	case "item.started":
		if item, ok := raw["item"].(map[string]any); ok {
			return parseCodexItemStarted(item)
		}
		return nil

	case "item.completed":
		if item, ok := raw["item"].(map[string]any); ok {
			return parseCodexItemCompleted(item)
		}
		return nil

	case "event_msg":
		if payload, ok := raw["payload"].(map[string]any); ok {
			msgType, _ := payload["type"].(string)
			if msgType == "agent_message" {
				if text, ok := payload["message"].(string); ok && text != "" {
					return []Event{{Type: EventTypeMessage, Content: text}}
				}
			}
		}
		return nil

	case "error":
		if message, ok := raw["message"].(string); ok && message != "" {
			return []Event{{Type: EventTypeError, Error: message}}
		}
		return nil

	case "turn.failed":
		if turnErr, ok := raw["error"].(map[string]any); ok {
			if message, ok := turnErr["message"].(string); ok && message != "" {
				return []Event{{Type: EventTypeError, Error: message}}
			}
		}
		return nil
	}

	return nil
}

func parseCodexItemStarted(item map[string]any) []Event {
	itemType, _ := item["type"].(string)
	if itemType != "command_execution" {
		return nil
	}

	command, _ := item["command"].(string)
	return []Event{{
		Type: EventTypeToolStart,
		Metadata: map[string]any{
			"command": command,
			"status":  "in_progress",
		},
	}}
}

func parseCodexItemCompleted(item map[string]any) []Event {
	itemType, _ := item["type"].(string)

	switch itemType {
	case "agent_message", "assistant_message", "message":
		if text := extractMessageText(item); text != "" {
			return []Event{{Type: EventTypeMessage, Content: text}}
		}
	case "command_execution":
		command, _ := item["command"].(string)
		output, _ := item["aggregated_output"].(string)
		exitCode, _ := item["exit_code"].(float64)
		return []Event{{
			Type: EventTypeToolEnd,
			Metadata: map[string]any{
				"command":           command,
				"aggregated_output": output,
				"exit_code":         int(exitCode),
				"status":            "completed",
			},
		}}
	}

	return nil
}

func extractMessageText(item map[string]any) string {
	itemType, _ := item["type"].(string)
	switch itemType {
	case "agent_message", "assistant_message", "message":
	default:
		return ""
	}

	if text, ok := item["text"].(string); ok && text != "" {
		return text
	}

	if text := extractItemText(item, "content", "output_text"); text != "" {
		return text
	}

	if text := extractItemText(item, "content", "text"); text != "" {
		return text
	}

	if text := extractItemText(item, "parts", "text"); text != "" {
		return text
	}

	if text, ok := item["message"].(string); ok && text != "" {
		return text
	}

	return ""
}

// extractItemText 从 Codex 输出中提取文本内容
func extractItemText(raw map[string]any, arrayField, elementType string) string {
	arr, ok := raw[arrayField].([]any)
	if !ok {
		return ""
	}

	var parts []string
	for _, elem := range arr {
		m, ok := elem.(map[string]any)
		if !ok {
			continue
		}
		if elementType != "" {
			if t, _ := m["type"].(string); t != elementType {
				continue
			}
		}
		if t, _ := m["text"].(string); t != "" {
			parts = append(parts, t)
		}
	}

	return strings.Join(parts, "\n")
}

func cleanCodexStderr(stderr string) string {
	var lines []string
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "Reading additional input from stdin..." {
			continue
		}
		lines = append(lines, line)
	}

	return strings.Join(lines, "\n")
}

func joinNonEmpty(parts []string, extra string) string {
	combined := append([]string{}, parts...)
	if extra != "" {
		combined = append(combined, extra)
	}

	return strings.Join(uniqueNonEmpty(combined), "\n")
}

func uniqueNonEmpty(parts []string) []string {
	seen := make(map[string]struct{}, len(parts))
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		result = append(result, part)
	}

	return result
}

func sendEvent(events chan<- Event, event Event) {
	if event.Type == "" {
		return
	}

	events <- event
}

// IsAvailable 检查 codex CLI 是否可用
func (a *CodeXAgent) IsAvailable() bool {
	_, err := exec.LookPath("codex")
	return err == nil
}
