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
	"sync/atomic"
	"syscall"
	"time"
)

type claudeInteractiveSession struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	events chan Event

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	alive  atomic.Bool

	stdinMu sync.Mutex

	state *claudeStreamState

	pendingMu sync.Mutex
	pending   *claudePendingApproval
}

type claudePendingApproval struct {
	requestID string
	input     map[string]any
}

func (a *ClaudeCodeAgent) StartSession(ctx context.Context, sessionID string) (InteractiveSession, error) {
	command, err := resolveClaudeCommand()
	if err != nil {
		return nil, fmt.Errorf("claude CLI is not available")
	}

	childCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(childCtx, command, a.buildInteractiveArgs(sessionID)...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to get stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to get stdout pipe: %w", err)
	}

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to start claude CLI: %w", err)
	}

	session := &claudeInteractiveSession{
		cmd:    cmd,
		stdin:  stdin,
		events: make(chan Event, 64),
		ctx:    childCtx,
		cancel: cancel,
		done:   make(chan struct{}),
		state: &claudeStreamState{
			messageSnapshot: make(map[string]string),
		},
	}
	session.alive.Store(true)

	go session.readLoop(stdout, &stderr)

	return session, nil
}

func (a *ClaudeCodeAgent) buildInteractiveArgs(sessionID string) []string {
	args := []string{
		"--verbose",
		"--output-format", "stream-json",
		"--input-format", "stream-json",
		"--include-partial-messages",
		"--permission-prompt-tool", "stdio",
	}
	if strings.TrimSpace(sessionID) != "" {
		args = append(args, "--resume", strings.TrimSpace(sessionID))
	}
	return args
}

func (s *claudeInteractiveSession) Send(input string) error {
	if !s.alive.Load() {
		return fmt.Errorf("claude session is not running")
	}

	return s.writeJSON(map[string]any{
		"type": "user",
		"message": map[string]any{
			"role":    "user",
			"content": wrapUserPrompt(input),
		},
	})
}

func (s *claudeInteractiveSession) RespondApproval(action ApprovalAction) error {
	if !s.alive.Load() {
		return fmt.Errorf("claude session is not running")
	}

	s.pendingMu.Lock()
	pending := s.pending
	if pending != nil {
		s.pending = nil
	}
	s.pendingMu.Unlock()

	if pending == nil {
		return fmt.Errorf("no pending claude approval")
	}

	response := map[string]any{
		"behavior": "deny",
		"message":  "The user cancelled this tool use.",
	}

	switch action {
	case ApprovalActionAllow, ApprovalActionAllowAll:
		updatedInput := pending.input
		if updatedInput == nil {
			updatedInput = make(map[string]any)
		}
		response = map[string]any{
			"behavior":     "allow",
			"updatedInput": updatedInput,
		}
	case ApprovalActionCancel:
	default:
		return fmt.Errorf("unsupported approval action: %s", action)
	}

	return s.writeJSON(map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype":    "success",
			"request_id": pending.requestID,
			"response":   response,
		},
	})
}

func (s *claudeInteractiveSession) Events() <-chan Event {
	return s.events
}

func (s *claudeInteractiveSession) CurrentSessionID() string {
	return s.state.sessionID
}

func (s *claudeInteractiveSession) Close() error {
	if !s.alive.Load() {
		return nil
	}

	s.alive.Store(false)

	s.stdinMu.Lock()
	_ = s.stdin.Close()
	s.stdinMu.Unlock()

	select {
	case <-s.done:
		return nil
	case <-time.After(3 * time.Second):
	}

	s.cancel()
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Signal(syscall.SIGTERM)
	}

	select {
	case <-s.done:
		return nil
	case <-time.After(2 * time.Second):
	}

	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}

	<-s.done
	return nil
}

func (s *claudeInteractiveSession) writeJSON(payload map[string]any) error {
	s.stdinMu.Lock()
	defer s.stdinMu.Unlock()

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal claude payload: %w", err)
	}

	if _, err := s.stdin.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("failed to write claude payload: %w", err)
	}

	return nil
}

func (s *claudeInteractiveSession) readLoop(stdout io.ReadCloser, stderr *bytes.Buffer) {
	defer close(s.events)
	defer close(s.done)
	defer s.alive.Store(false)

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var raw map[string]any
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}

		if approvalEvent, ok := s.parseControlRequest(raw); ok {
			sendEvent(s.events, approvalEvent)
			continue
		}

		for _, event := range parseClaudeStreamEvent(s.state, raw) {
			sendEvent(s.events, event)
		}
	}

	if err := scanner.Err(); err != nil && s.ctx.Err() == nil {
		sendEvent(s.events, Event{
			Type:  EventTypeError,
			Error: fmt.Sprintf("failed to read claude session output: %v", err),
		})
	}

	if err := s.cmd.Wait(); err != nil && s.ctx.Err() == nil {
		details := strings.TrimSpace(stderr.String())
		if details == "" {
			details = err.Error()
		}
		sendEvent(s.events, Event{
			Type:  EventTypeError,
			Error: fmt.Sprintf("failed to execute claude CLI: %s", details),
		})
	}
}

func (s *claudeInteractiveSession) parseControlRequest(raw map[string]any) (Event, bool) {
	if rawType, _ := raw["type"].(string); rawType != "control_request" {
		return Event{}, false
	}

	requestID, _ := raw["request_id"].(string)
	request, _ := raw["request"].(map[string]any)
	if request == nil {
		return Event{}, false
	}

	subtype, _ := request["subtype"].(string)
	if subtype != "can_use_tool" {
		return Event{}, false
	}

	toolName, _ := request["tool_name"].(string)
	input, _ := request["input"].(map[string]any)

	s.pendingMu.Lock()
	s.pending = &claudePendingApproval{
		requestID: requestID,
		input:     input,
	}
	s.pendingMu.Unlock()

	return Event{
		Type:     EventTypeApproval,
		ToolName: toolName,
		ToolInput: summarizeApprovalInput(
			toolName,
			input,
		),
	}, true
}

func summarizeApprovalInput(toolName string, input map[string]any) string {
	if len(input) == 0 {
		return ""
	}

	if command, _ := input["command"].(string); strings.TrimSpace(command) != "" {
		return strings.TrimSpace(command)
	}
	if filePath, _ := input["file_path"].(string); strings.TrimSpace(filePath) != "" {
		return strings.TrimSpace(filePath)
	}
	if prompt, _ := input["prompt"].(string); strings.TrimSpace(prompt) != "" {
		return strings.TrimSpace(prompt)
	}

	data, err := json.MarshalIndent(input, "", "  ")
	if err != nil {
		return toolName
	}

	return string(data)
}
