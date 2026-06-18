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
	"sync"
	"unicode/utf8"
)

// ClaudeCodeAgent Claude Code CLI Agent 实现
type ClaudeCodeAgent struct {
	name string
}

type claudeStreamState struct {
	sessionIDMu     sync.RWMutex
	sessionID       string
	messageSnapshot map[string]string
	lastMessageID   string
}

func (s *claudeStreamState) storeSessionID(v string) {
	s.sessionIDMu.Lock()
	s.sessionID = v
	s.sessionIDMu.Unlock()
}

func (s *claudeStreamState) loadSessionID() string {
	s.sessionIDMu.RLock()
	defer s.sessionIDMu.RUnlock()
	return s.sessionID
}

// NewClaudeCodeAgent 创建新的 Claude Code Agent
func NewClaudeCodeAgent() *ClaudeCodeAgent {
	return &ClaudeCodeAgent{
		name: "claude-code",
	}
}

// Name 返回 Agent 名称
func (a *ClaudeCodeAgent) Name() string {
	return a.name
}

// ExecuteStream 执行 AI 任务并返回事件流。
func (a *ClaudeCodeAgent) ExecuteStream(ctx context.Context, sessionID string, input string) (<-chan Event, error) {
	command, err := resolveClaudeCommand()
	if err != nil {
		return nil, fmt.Errorf("claude CLI is not available")
	}

	cmd := exec.CommandContext(ctx, command, a.buildStreamArgs(sessionID, input)...)
	if workDir := WorkDirFromContext(ctx); workDir != "" {
		cmd.Dir = workDir
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to get stdout pipe: %w", err)
	}

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	events := make(chan Event, 8)

	go func() {
		defer close(events)

		if err := cmd.Start(); err != nil {
			emitOrCancel(ctx, events, Event{
				Type:  EventTypeError,
				Error: fmt.Sprintf("failed to start claude CLI: %v", err),
			})
			return
		}

		state := &claudeStreamState{
			messageSnapshot: make(map[string]string),
		}

		parseErr := a.streamClaudeOutput(ctx, stdout, state, events)

		runErr := cmd.Wait()
		if parseErr != nil {
			if ctx.Err() != nil {
				return
			}
			emitOrCancel(ctx, events, Event{
				Type:  EventTypeError,
				Error: parseErr.Error(),
			})
			return
		}

		if runErr != nil {
			details := strings.TrimSpace(stderr.String())
			if details == "" {
				details = runErr.Error()
			}
			emitOrCancel(ctx, events, Event{Type: EventTypeError, Error: fmt.Sprintf("failed to execute claude CLI: %s", details)})
			return
		}
	}()

	return events, nil
}

func (a *ClaudeCodeAgent) buildStreamArgs(sessionID string, input string) []string {
	prompt := wrapUserPrompt(input)

	args := []string{
		"--print",
		"--verbose",
		"--output-format", "stream-json",
		"--include-partial-messages",
	}
	if strings.TrimSpace(sessionID) != "" {
		args = append(args, "--resume", strings.TrimSpace(sessionID))
	}
	args = append(args, prompt)
	return args
}

func (a *ClaudeCodeAgent) streamClaudeOutput(ctx context.Context, stdout io.ReadCloser, state *claudeStreamState, events chan<- Event) error {
	reader := bufio.NewReader(stdout)

	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("failed to read claude output: %w", err)
		}

		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}

		var raw map[string]any
		if err := json.Unmarshal(line, &raw); err != nil {
			continue
		}

		for _, event := range parseClaudeStreamEvent(state, raw) {
			if !emitOrCancel(ctx, events, event) {
				return ctx.Err()
			}
		}
	}

	return nil
}

func parseClaudeStreamEvent(state *claudeStreamState, raw map[string]any) []Event {
	eventType, _ := raw["type"].(string)

	switch eventType {
	case "stream_event":
		return parseClaudeStructuredStreamEvent(state, raw)

	case "system":
		if sid, _ := raw["session_id"].(string); sid != "" && state.loadSessionID() != sid {
			state.storeSessionID(sid)
			return []Event{{Type: EventTypeSession, SessionID: sid}}
		}
		return nil

	case "assistant":
		if sid, _ := raw["session_id"].(string); sid != "" && state.loadSessionID() != sid {
			state.storeSessionID(sid)
		}
		message, _ := raw["message"].(map[string]any)
		messageID, _ := message["id"].(string)
		text := extractClaudeMessageText(message)
		if text == "" {
			return nil
		}

		if messageID != "" {
			state.lastMessageID = messageID
		}
		previous := state.messageSnapshot[messageID]
		delta, next := resolveTextDelta(previous, text)
		state.messageSnapshot[messageID] = next
		if delta == "" {
			return nil
		}
		return []Event{{Type: EventTypeDelta, Content: delta}}

	case "result":
		result, _ := raw["result"].(string)
		sid, _ := raw["session_id"].(string)
		isError, _ := raw["is_error"].(bool)

		var events []Event
		if sid != "" && state.loadSessionID() != sid {
			state.storeSessionID(sid)
			events = append(events, Event{Type: EventTypeSession, SessionID: sid})
		}
		if isError {
			if strings.TrimSpace(result) != "" {
				events = append(events, Event{Type: EventTypeError, Error: strings.TrimSpace(result)})
			}
			events = append(events, Event{Type: EventTypeDone})
			return events
		}

		finalDelta := result
		if state.lastMessageID != "" {
			lastSnapshot := state.messageSnapshot[state.lastMessageID]
			if delta, _ := resolveTextDelta(lastSnapshot, result); delta != "" {
				finalDelta = delta
			} else {
				finalDelta = ""
			}
		}

		if strings.TrimSpace(finalDelta) != "" {
			events = append(events, Event{Type: EventTypeMessage, Content: finalDelta})
		}
		events = append(events, Event{Type: EventTypeDone})
		return events
	}

	return nil
}

func parseClaudeStructuredStreamEvent(state *claudeStreamState, raw map[string]any) []Event {
	var events []Event

	if sid, _ := raw["session_id"].(string); sid != "" && state.loadSessionID() != sid {
		state.storeSessionID(sid)
		events = append(events, Event{Type: EventTypeSession, SessionID: sid})
	}

	event, _ := raw["event"].(map[string]any)
	if event == nil {
		return events
	}

	switch event["type"] {
	case "message_start":
		message, _ := event["message"].(map[string]any)
		if messageID, _ := message["id"].(string); messageID != "" {
			state.lastMessageID = messageID
			if _, ok := state.messageSnapshot[messageID]; !ok {
				state.messageSnapshot[messageID] = ""
			}
		}

	case "content_block_start":
		contentBlock, _ := event["content_block"].(map[string]any)
		text, _ := contentBlock["text"].(string)
		if text == "" || state.lastMessageID == "" {
			return events
		}
		state.messageSnapshot[state.lastMessageID] += text
		events = append(events, Event{Type: EventTypeDelta, Content: text})

	case "content_block_delta":
		delta, _ := event["delta"].(map[string]any)
		deltaType, _ := delta["type"].(string)
		if deltaType != "text_delta" {
			return events
		}

		text, _ := delta["text"].(string)
		if text == "" {
			return events
		}
		if state.lastMessageID != "" {
			state.messageSnapshot[state.lastMessageID] += text
		}
		events = append(events, Event{Type: EventTypeDelta, Content: text})
	}

	return events
}

func extractClaudeMessageText(message map[string]any) string {
	if message == nil {
		return ""
	}

	content, ok := message["content"].([]any)
	if !ok {
		return ""
	}

	var parts []string
	for _, item := range content {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		itemType, _ := m["type"].(string)
		if itemType != "text" {
			continue
		}
		if text, _ := m["text"].(string); text != "" {
			parts = append(parts, text)
		}
	}

	return strings.Join(parts, "\n")
}

func resolveTextDelta(previous, next string) (string, string) {
	return ResolveTextDelta(previous, next)
}

// ResolveTextDelta 按 UTF-8 rune 比较，返回 next 相对 previous 的新增部分和新的快照。
func ResolveTextDelta(previous, next string) (string, string) {
	if next == "" || next == previous {
		return "", next
	}
	if strings.HasPrefix(next, previous) {
		return next[len(previous):], next
	}
	if strings.HasPrefix(previous, next) {
		return "", next
	}

	commonBytes := 0
	prevIdx, nextIdx := 0, 0
	for prevIdx < len(previous) && nextIdx < len(next) {
		prevRune, prevSize := utf8.DecodeRuneInString(previous[prevIdx:])
		nextRune, nextSize := utf8.DecodeRuneInString(next[nextIdx:])
		if prevRune != nextRune {
			break
		}
		commonBytes = nextIdx + nextSize
		prevIdx += prevSize
		nextIdx += nextSize
	}

	return next[commonBytes:], next
}

func resolveClaudeCommand() (string, error) {
	for _, candidate := range []string{"claude", "cc"} {
		if _, err := exec.LookPath(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", exec.ErrNotFound
}

// IsAvailable 检查 Agent 是否可用
func (a *ClaudeCodeAgent) IsAvailable() bool {
	_, err := resolveClaudeCommand()
	return err == nil
}
