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
	"time"

	"github.com/gorilla/websocket"
)

const (
	codexAppServerReadyTimeout = 10 * time.Second
	codexAppServerReadTimeout  = 5 * time.Minute
)

type codexAppServerClient struct {
	conn   *websocket.Conn
	cmd    *exec.Cmd
	cancel context.CancelFunc

	stdout bytes.Buffer
	stderr bytes.Buffer

	messages chan map[string]any
	readErr  chan error
}

func (a *CodeXAgent) executeViaAppServer(ctx context.Context, session *codexSession, input string) (<-chan Event, error) {
	wsURL, httpURL, err := reserveCodexAppServerURL()
	if err != nil {
		return nil, err
	}

	childCtx, cancel := context.WithCancel(ctx)
	client := &codexAppServerClient{
		cancel:   cancel,
		messages: make(chan map[string]any, 256),
		readErr:  make(chan error, 1),
	}

	args := []string{"app-server", "--listen", wsURL}
	client.cmd = exec.CommandContext(childCtx, "codex", args...)
	client.cmd.Stdout = &client.stdout
	client.cmd.Stderr = &client.stderr

	if err := client.cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to start codex app-server: %w", err)
	}

	if err := waitForCodexAppServerReady(childCtx, httpURL+"/readyz"); err != nil {
		client.shutdown()
		return nil, fmt.Errorf("codex app-server not ready: %w; stderr: %s", err, strings.TrimSpace(client.stderr.String()))
	}

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		client.shutdown()
		return nil, fmt.Errorf("failed to connect codex app-server websocket: %w", err)
	}
	client.conn = conn
	client.conn.SetReadLimit(10 << 20)

	go client.readLoop()

	var pending []map[string]any

	if err := client.initialize(&pending); err != nil {
		client.shutdown()
		return nil, err
	}

	threadID, err := client.startOrResumeThread(session, &pending, a.model)
	if err != nil {
		client.shutdown()
		return nil, err
	}

	if err := client.startTurn(threadID, wrapUserPrompt(input), &pending, a.model); err != nil {
		client.shutdown()
		return nil, err
	}

	events := make(chan Event, 256)
	sendEvent(events, Event{Type: EventTypeSession, SessionID: threadID})

	go client.streamEvents(ctx, session, pending, events)

	return events, nil
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
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			c.readErr <- err
			return
		}

		var msg map[string]any
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
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
			sendEvent(events, event)
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
			sendEvent(events, Event{Type: EventTypeError, Error: fmt.Sprintf("codex app-server stream error: %v", err)})
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
		events = append(events, Event{Type: EventTypeDone})
		return events
	}

	return events
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
