package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// ClaudeCodeAgent Claude Code CLI Agent 实现
type ClaudeCodeAgent struct {
	name string
}

type claudePrintResult struct {
	Result    string `json:"result"`
	SessionID string `json:"session_id"`
	IsError   bool   `json:"is_error"`
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

// Execute 执行 AI 任务
func (a *ClaudeCodeAgent) Execute(ctx context.Context, sessionID string, input string) (string, error) {
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

// ExecuteStream 执行 AI 任务并返回事件流。
func (a *ClaudeCodeAgent) ExecuteStream(ctx context.Context, sessionID string, input string) (<-chan Event, error) {
	command, err := resolveClaudeCommand()
	if err != nil {
		return nil, fmt.Errorf("claude CLI is not available")
	}

	cmd := exec.CommandContext(ctx, command, a.buildArgs(sessionID, input)...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	events := make(chan Event, 8)

	go func() {
		defer close(events)

		runErr := cmd.Run()
		result, parseErr := parseClaudePrintOutput(stdout.String())
		if parseErr != nil {
			if runErr != nil {
				sendEvent(events, Event{
					Type:  EventTypeError,
					Error: fmt.Sprintf("failed to execute claude CLI: %v, stderr: %s", runErr, strings.TrimSpace(stderr.String())),
				})
				return
			}

			text := strings.TrimSpace(stdout.String())
			if text == "" {
				sendEvent(events, Event{Type: EventTypeError, Error: "empty response from claude CLI"})
				return
			}

			sendEvent(events, Event{Type: EventTypeMessage, Content: text})
			sendEvent(events, Event{Type: EventTypeDone})
			return
		}

		if runErr != nil || result.IsError {
			details := strings.TrimSpace(result.Result)
			if details == "" {
				details = strings.TrimSpace(stderr.String())
			}
			if details == "" {
				details = "unknown claude CLI error"
			}
			sendEvent(events, Event{Type: EventTypeError, Error: details})
			return
		}

		response := strings.TrimSpace(result.Result)
		if response == "" {
			sendEvent(events, Event{Type: EventTypeError, Error: "empty response from claude CLI"})
			return
		}

		if sid := strings.TrimSpace(result.SessionID); sid != "" {
			sendEvent(events, Event{Type: EventTypeSession, SessionID: sid})
		}
		sendEvent(events, Event{Type: EventTypeMessage, Content: response})
		sendEvent(events, Event{Type: EventTypeDone})
	}()

	return events, nil
}

func (a *ClaudeCodeAgent) buildArgs(sessionID string, input string) []string {
	prompt := wrapUserPrompt(input)

	args := []string{"--print", "--output-format", "json"}
	if strings.TrimSpace(sessionID) != "" {
		args = append(args, "--resume", strings.TrimSpace(sessionID))
	}
	args = append(args, prompt)
	return args
}

func parseClaudePrintOutput(output string) (*claudePrintResult, error) {
	var result claudePrintResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &result); err != nil {
		return nil, err
	}
	return &result, nil
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
