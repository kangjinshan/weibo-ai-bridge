package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	codexAppServerReadyTimeout = 10 * time.Second
	codexAppServerReadTimeout  = 5 * time.Minute
)

type codexAppServerTransport string

const (
	codexAppServerTransportStdio     codexAppServerTransport = "stdio"
	codexAppServerTransportWebSocket codexAppServerTransport = "websocket"
)

type codexAppServerConn interface {
	WriteJSON(map[string]any) error
	ReadJSON(*map[string]any) error
	SetReadDeadline(time.Time) error
	Close() error
}

type codexAppServerStdioConn struct {
	stdin  io.WriteCloser
	stdout io.ReadCloser

	encoder *json.Encoder
	decoder *json.Decoder

	writeMu sync.Mutex
}

type codexAppServerWebSocketConn struct {
	conn *websocket.Conn
}

type codexAppServerClient struct {
	conn   codexAppServerConn
	cmd    *exec.Cmd
	cancel context.CancelFunc

	stderr bytes.Buffer

	messages chan map[string]any
	readErr  chan error
}

func (a *CodeXAgent) executeViaAppServer(ctx context.Context, session *codexSession, input string) (<-chan Event, error) {
	events, err := a.executeViaAppServerTransport(ctx, session, input, codexAppServerTransportStdio)
	if err == nil {
		return events, nil
	}

	wsEvents, wsErr := a.executeViaAppServerTransport(ctx, session, input, codexAppServerTransportWebSocket)
	if wsErr == nil {
		return wsEvents, nil
	}

	return nil, fmt.Errorf("codex app-server stdio failed: %v; websocket fallback failed: %w", err, wsErr)
}

func (a *CodeXAgent) executeViaAppServerTransport(ctx context.Context, session *codexSession, input string, transport codexAppServerTransport) (<-chan Event, error) {
	childCtx, cancel := context.WithCancel(ctx)
	client := &codexAppServerClient{
		cancel:   cancel,
		messages: make(chan map[string]any, 256),
		readErr:  make(chan error, 1),
	}

	err := client.start(childCtx, WorkDirFromContext(ctx), transport)
	if err != nil {
		cancel()
		return nil, err
	}

	go client.readLoop()

	var pending []map[string]any

	if err := client.initialize(&pending); err != nil {
		client.shutdown()
		return nil, appendCodexAppServerStderr(err, client.stderr.String())
	}

	threadID, err := client.startOrResumeThread(session, &pending, a.model)
	if err != nil {
		client.shutdown()
		return nil, appendCodexAppServerStderr(err, client.stderr.String())
	}

	if err := client.startTurn(threadID, wrapUserPrompt(input), &pending, a.model); err != nil {
		client.shutdown()
		return nil, appendCodexAppServerStderr(err, client.stderr.String())
	}

	events := make(chan Event, 256)
	emitOrCancel(ctx, events, Event{Type: EventTypeSession, SessionID: threadID})

	go client.streamEvents(ctx, session, pending, events)

	return events, nil
}

func (c *codexAppServerClient) start(ctx context.Context, workDir string, transport codexAppServerTransport) error {
	switch transport {
	case codexAppServerTransportStdio:
		return c.startStdio(ctx, workDir)
	case codexAppServerTransportWebSocket:
		return c.startWebSocket(ctx, workDir)
	default:
		return fmt.Errorf("unsupported codex app-server transport: %s", transport)
	}
}

func (c *codexAppServerClient) startStdio(ctx context.Context, workDir string) error {
	c.cmd = exec.CommandContext(ctx, "codex", "app-server", "--listen", "stdio://")
	if workDir != "" {
		c.cmd.Dir = workDir
	}
	c.cmd.Stderr = &c.stderr

	conn, err := newCodexAppServerStdioConn(c.cmd)
	if err != nil {
		return fmt.Errorf("failed to prepare codex app-server stdio: %w", err)
	}
	c.conn = conn

	if err := c.cmd.Start(); err != nil {
		c.shutdown()
		return fmt.Errorf("failed to start codex app-server stdio: %w", err)
	}

	return nil
}

func (c *codexAppServerClient) startWebSocket(ctx context.Context, workDir string) error {
	wsURL, httpURL, err := reserveCodexAppServerURL()
	if err != nil {
		return err
	}

	c.cmd = exec.CommandContext(ctx, "codex", "app-server", "--listen", wsURL)
	if workDir != "" {
		c.cmd.Dir = workDir
	}
	c.cmd.Stderr = &c.stderr

	if err := c.cmd.Start(); err != nil {
		c.shutdown()
		return fmt.Errorf("failed to start codex app-server websocket: %w", err)
	}

	if err := waitForCodexAppServerReady(ctx, httpURL+"/readyz"); err != nil {
		c.shutdown()
		return appendCodexAppServerStderr(fmt.Errorf("codex app-server websocket not ready: %w", err), c.stderr.String())
	}

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		c.shutdown()
		return fmt.Errorf("failed to connect codex app-server websocket: %w", err)
	}
	conn.SetReadLimit(10 << 20)
	c.conn = &codexAppServerWebSocketConn{conn: conn}

	return nil
}

func newCodexAppServerStdioConn(cmd *exec.Cmd) (*codexAppServerStdioConn, error) {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}

	return &codexAppServerStdioConn{
		stdin:   stdin,
		stdout:  stdout,
		encoder: json.NewEncoder(stdin),
		decoder: json.NewDecoder(stdout),
	}, nil
}

func reserveCodexAppServerURL() (string, string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", "", err
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		return "", "", err
	}

	return "ws://" + addr, "http://" + addr, nil
}

func waitForCodexAppServerReady(ctx context.Context, readyURL string) error {
	client := &http.Client{Timeout: 500 * time.Millisecond}
	deadline := time.Now().Add(codexAppServerReadyTimeout)

	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, readyURL, nil)
		if err != nil {
			return err
		}

		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}

		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(deadline) {
			if err != nil {
				return err
			}
			return fmt.Errorf("ready check timed out")
		}

		time.Sleep(100 * time.Millisecond)
	}
}

func (c *codexAppServerStdioConn) WriteJSON(payload map[string]any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.encoder.Encode(payload)
}

func (c *codexAppServerStdioConn) ReadJSON(target *map[string]any) error {
	return c.decoder.Decode(target)
}

func (c *codexAppServerStdioConn) SetReadDeadline(deadline time.Time) error {
	deadlineWriter, ok := c.stdout.(interface {
		SetReadDeadline(time.Time) error
	})
	if !ok {
		return nil
	}
	return deadlineWriter.SetReadDeadline(deadline)
}

func (c *codexAppServerStdioConn) Close() error {
	var err error
	if c.stdin != nil {
		err = c.stdin.Close()
	}
	if c.stdout != nil {
		if closeErr := c.stdout.Close(); err == nil {
			err = closeErr
		}
	}
	return err
}

func (c *codexAppServerWebSocketConn) WriteJSON(payload map[string]any) error {
	return c.conn.WriteJSON(payload)
}

func (c *codexAppServerWebSocketConn) ReadJSON(target *map[string]any) error {
	return c.conn.ReadJSON(target)
}

func (c *codexAppServerWebSocketConn) SetReadDeadline(deadline time.Time) error {
	return c.conn.SetReadDeadline(deadline)
}

func (c *codexAppServerWebSocketConn) Close() error {
	return c.conn.Close()
}

func appendCodexAppServerStderr(err error, stderr string) error {
	if err == nil {
		return nil
	}
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		return err
	}
	return fmt.Errorf("%w; stderr: %s", err, stderr)
}

func (c *codexAppServerClient) initialize(pending *[]map[string]any) error {
	if err := c.sendJSON(map[string]any{
		"id":     "init",
		"method": "initialize",
		"params": map[string]any{
			"clientInfo": map[string]any{
				"name":    "weibo-ai-bridge",
				"title":   "weibo-ai-bridge",
				"version": "1.0.0",
			},
			"capabilities": map[string]any{
				"experimentalApi":           true,
				"optOutNotificationMethods": []string{},
			},
		},
	}); err != nil {
		return err
	}

	if _, err := c.waitForResponse("init", pending); err != nil {
		return err
	}

	return c.sendJSON(map[string]any{"method": "initialized"})
}

func (c *codexAppServerClient) startOrResumeThread(session *codexSession, pending *[]map[string]any, model string) (string, error) {
	reqID := "thread"
	method := "thread/start"
	params := buildCodexThreadStartParams("never", "danger-full-access", model, false)

	if threadID := session.CurrentSessionID(); threadID != "" {
		method = "thread/resume"
		params = buildCodexThreadResumeParams(threadID)
	}

	if err := c.sendJSON(map[string]any{
		"id":     reqID,
		"method": method,
		"params": params,
	}); err != nil {
		return "", err
	}

	msg, err := c.waitForResponse(reqID, pending)
	if err != nil {
		return "", err
	}

	result, _ := msg["result"].(map[string]any)
	thread, _ := result["thread"].(map[string]any)
	threadID, _ := thread["id"].(string)
	if !session.SetCurrentSessionID(threadID) && session.CurrentSessionID() == "" {
		return "", fmt.Errorf("codex app-server returned empty thread id")
	}

	return session.CurrentSessionID(), nil
}

func (c *codexAppServerClient) startTurn(threadID, prompt string, pending *[]map[string]any, model string) error {
	params := map[string]any{
		"threadId":       threadID,
		"approvalPolicy": "never",
		"input": []map[string]any{
			{
				"type":          "text",
				"text":          prompt,
				"text_elements": []string{},
			},
		},
	}
	if model = strings.TrimSpace(model); model != "" {
		params["model"] = model
	}

	if err := c.sendJSON(map[string]any{
		"id":     "turn",
		"method": "turn/start",
		"params": params,
	}); err != nil {
		return err
	}

	_, err := c.waitForResponse("turn", pending)
	return err
}

func (c *codexAppServerClient) sendJSON(payload map[string]any) error {
	return c.conn.WriteJSON(payload)
}

func (c *codexAppServerClient) waitForResponse(id string, pending *[]map[string]any) (map[string]any, error) {
	for {
		select {
		case msg, ok := <-c.messages:
			if !ok {
				return nil, io.EOF
			}
			if respID, _ := msg["id"].(string); respID == id {
				if errObj, ok := msg["error"].(map[string]any); ok {
					return nil, fmt.Errorf("codex app-server rpc error: %s", extractRPCError(errObj))
				}
				return msg, nil
			}
			*pending = append(*pending, msg)
		case err := <-c.readErr:
			return nil, err
		}
	}
}

func (c *codexAppServerClient) readLoop() {
	defer close(c.messages)

	for {
		_ = c.conn.SetReadDeadline(time.Now().Add(codexAppServerReadTimeout))
		var msg map[string]any
		if err := c.conn.ReadJSON(&msg); err != nil {
			select {
			case c.readErr <- err:
			default:
			}
			return
		}

		c.messages <- msg
	}
}

func (c *codexAppServerClient) streamEvents(ctx context.Context, session *codexSession, pending []map[string]any, events chan<- Event) {
	defer close(events)
	defer c.shutdown()

	deltaSeen := make(map[string]bool)

	emitMessage := func(msg map[string]any) bool {
		done := false
		for _, event := range parseCodexAppServerMessage(session, msg, deltaSeen) {
			if !emitOrCancel(ctx, events, event) {
				return true
			}
			if event.Type == EventTypeDone {
				done = true
			}
		}
		return done
	}

	for _, msg := range pending {
		if emitMessage(msg) {
			return
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-c.messages:
			if !ok {
				return
			}
			if emitMessage(msg) {
				return
			}
		case err := <-c.readErr:
			if ctx.Err() != nil {
				return
			}
			emitOrCancel(ctx, events, Event{Type: EventTypeError, Error: fmt.Sprintf("codex app-server stream error: %v", err)})
			return
		}
	}
}

type codexThreadSession interface {
	CurrentSessionID() string
	SetCurrentSessionID(threadID string) bool
}

func parseCodexAppServerMessage(session codexThreadSession, msg map[string]any, deltaSeen map[string]bool) []Event {
	method, _ := msg["method"].(string)
	params, _ := msg["params"].(map[string]any)
	events := make([]Event, 0, 2)

	if session != nil {
		if threadID, _ := params["threadId"].(string); strings.TrimSpace(threadID) != "" && session.SetCurrentSessionID(threadID) {
			events = append(events, Event{Type: EventTypeSession, SessionID: strings.TrimSpace(threadID)})
		}
	}

	switch method {
	case "item/agentMessage/delta":
		itemID, _ := params["itemId"].(string)
		delta, _ := params["delta"].(string)
		if itemID != "" {
			deltaSeen[itemID] = true
		}
		if delta == "" {
			return events
		}
		events = append(events, Event{Type: EventTypeDelta, Content: delta})
		return events

	case "item/completed":
		item, _ := params["item"].(map[string]any)
		itemType, _ := item["type"].(string)
		if itemType != "agentMessage" {
			return events
		}
		itemID, _ := item["id"].(string)
		text, _ := item["text"].(string)
		if itemID != "" && deltaSeen[itemID] {
			return events
		}
		if text == "" {
			return events
		}
		events = append(events, Event{Type: EventTypeMessage, Content: text})
		return events

	case "turn/completed":
		if errorText := extractCodexAppServerEventError(params); errorText != "" {
			events = append(events, Event{Type: EventTypeError, Error: errorText})
		}
		events = append(events, Event{Type: EventTypeDone})
		return events

	case "error":
		if errorText := extractCodexAppServerEventError(params); errorText != "" {
			events = append(events, Event{Type: EventTypeError, Error: errorText})
			return events
		}
	}

	return events
}

func extractCodexAppServerEventError(params map[string]any) string {
	if params == nil {
		return ""
	}

	if errObj, _ := params["error"].(map[string]any); errObj != nil {
		if message := normalizeCodexAppServerErrorMessage(errObj["message"]); message != "" {
			return message
		}
		if details := normalizeCodexAppServerErrorMessage(errObj["additionalDetails"]); details != "" {
			return details
		}
	}

	if turn, _ := params["turn"].(map[string]any); turn != nil {
		if errObj, _ := turn["error"].(map[string]any); errObj != nil {
			if message := normalizeCodexAppServerErrorMessage(errObj["message"]); message != "" {
				return message
			}
			if details := normalizeCodexAppServerErrorMessage(errObj["additionalDetails"]); details != "" {
				return details
			}
		}
	}

	return ""
}

func normalizeCodexAppServerErrorMessage(value any) string {
	message, _ := value.(string)
	message = strings.TrimSpace(message)
	if message == "" {
		return ""
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(message), &payload); err == nil {
		if detail, _ := payload["detail"].(string); strings.TrimSpace(detail) != "" {
			return strings.TrimSpace(detail)
		}
		if nestedMessage, _ := payload["message"].(string); strings.TrimSpace(nestedMessage) != "" {
			return strings.TrimSpace(nestedMessage)
		}
	}

	return message
}

func extractRPCError(errObj map[string]any) string {
	if message, _ := errObj["message"].(string); strings.TrimSpace(message) != "" {
		return strings.TrimSpace(message)
	}
	return "unknown rpc error"
}

func (c *codexAppServerClient) shutdown() {
	if c.cancel != nil {
		c.cancel()
	}
	if c.conn != nil {
		_ = c.conn.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
		_, _ = c.cmd.Process.Wait()
	}
}
