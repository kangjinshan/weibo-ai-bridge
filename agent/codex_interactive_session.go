package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

type codexInteractiveSession struct {
	conn   *websocket.Conn
	cmd    *exec.Cmd
	cancel context.CancelFunc
	ctx    context.Context

	events chan Event

	writeMu sync.Mutex

	reqCounter atomic.Int64

	pendingRespMu sync.Mutex
	pendingResp   map[string]chan map[string]any

	threadID atomic.Value
	turnID   atomic.Value
	alive    atomic.Bool

	readDone chan struct{}

	deltaSeenMu sync.Mutex
	deltaSeen   map[string]bool

	pendingApprovalMu sync.Mutex
	pendingApproval   *codexPendingApproval
}

type codexPendingApproval struct {
	id     any
	method string
	params map[string]any
}

func (a *CodeXAgent) StartSession(ctx context.Context, sessionID string) (InteractiveSession, error) {
	wsURL, httpURL, err := reserveCodexAppServerURL()
	if err != nil {
		return nil, err
	}

	childCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(childCtx, "codex", "app-server", "--listen", wsURL)
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to start codex app-server: %w", err)
	}

	if err := waitForCodexAppServerReady(childCtx, httpURL+"/readyz"); err != nil {
		cancel()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
		return nil, fmt.Errorf("codex app-server not ready: %w", err)
	}

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, http.Header{})
	if err != nil {
		cancel()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
		return nil, fmt.Errorf("failed to connect codex app-server websocket: %w", err)
	}

	session := &codexInteractiveSession{
		conn:        conn,
		cmd:         cmd,
		cancel:      cancel,
		ctx:         childCtx,
		events:      make(chan Event, 128),
		pendingResp: make(map[string]chan map[string]any),
		readDone:    make(chan struct{}),
		deltaSeen:   make(map[string]bool),
	}
	session.alive.Store(true)
	if strings.TrimSpace(sessionID) != "" {
		session.threadID.Store(strings.TrimSpace(sessionID))
	}

	go session.readLoop()

	if err := session.initialize(); err != nil {
		_ = session.Close()
		return nil, err
	}
	if err := session.ensureThread(a.model); err != nil {
		_ = session.Close()
		return nil, err
	}

	return session, nil
}

func (s *codexInteractiveSession) Send(input string) error {
	params := map[string]any{
		"threadId":       s.CurrentSessionID(),
		"approvalPolicy": "on-request",
		"input": []map[string]any{
			{
				"type":          "text",
				"text":          wrapUserPrompt(input),
				"text_elements": []string{},
			},
		},
	}

	s.deltaSeenMu.Lock()
	s.deltaSeen = make(map[string]bool)
	s.deltaSeenMu.Unlock()

	resp, err := s.request("turn/start", params)
	if err != nil {
		return err
	}

	if result, _ := resp["result"].(map[string]any); result != nil {
		if turn, _ := result["turn"].(map[string]any); turn != nil {
			if turnID, _ := turn["id"].(string); strings.TrimSpace(turnID) != "" {
				s.turnID.Store(strings.TrimSpace(turnID))
			}
		}
	}

	return nil
}

func (s *codexInteractiveSession) RespondApproval(action ApprovalAction) error {
	s.pendingApprovalMu.Lock()
	pending := s.pendingApproval
	if pending != nil {
		s.pendingApproval = nil
	}
	s.pendingApprovalMu.Unlock()

	if pending == nil {
		return fmt.Errorf("no pending codex approval")
	}

	result, err := codexApprovalResult(pending, action)
	if err != nil {
		return err
	}

	return s.writeJSON(map[string]any{
		"id":     pending.id,
		"result": result,
	})
}

func (s *codexInteractiveSession) Interrupt() error {
	threadID := strings.TrimSpace(s.CurrentSessionID())
	turnID, _ := s.turnID.Load().(string)
	turnID = strings.TrimSpace(turnID)
	if threadID == "" || turnID == "" {
		return nil
	}

	_, err := s.request("turn/interrupt", map[string]any{
		"threadId": threadID,
		"turnId":   turnID,
	})
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "not found") ||
			strings.Contains(strings.ToLower(err.Error()), "no active") ||
			strings.Contains(strings.ToLower(err.Error()), "already completed") {
			return nil
		}
		return err
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		currentTurnID, _ := s.turnID.Load().(string)
		if strings.TrimSpace(currentTurnID) == "" || strings.TrimSpace(currentTurnID) != turnID {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}

	return nil
}

func (s *codexInteractiveSession) Events() <-chan Event {
	return s.events
}

func (s *codexInteractiveSession) CurrentSessionID() string {
	v, _ := s.threadID.Load().(string)
	return v
}

func (s *codexInteractiveSession) Close() error {
	if !s.alive.Swap(false) {
		return nil
	}

	if s.cancel != nil {
		s.cancel()
	}
	if s.conn != nil {
		_ = s.conn.Close()
	}
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
		_, _ = s.cmd.Process.Wait()
	}
	<-s.readDone
	return nil
}

func (s *codexInteractiveSession) initialize() error {
	if _, err := s.requestWithID("init", "initialize", map[string]any{
		"clientInfo": map[string]any{
			"name":    "weibo-ai-bridge",
			"title":   "weibo-ai-bridge",
			"version": "1.0.0",
		},
		"capabilities": map[string]any{
			"experimentalApi":           true,
			"optOutNotificationMethods": []string{},
		},
	}); err != nil {
		return fmt.Errorf("failed to initialize codex app-server: %w", err)
	}

	return s.writeJSON(map[string]any{"method": "initialized"})
}

func (s *codexInteractiveSession) ensureThread(model string) error {
	method := "thread/start"
	params := map[string]any{
		"approvalPolicy":         "on-request",
		"sandbox":                "danger-full-access",
		"persistExtendedHistory": false,
		"experimentalRawEvents":  false,
	}

	if strings.TrimSpace(model) != "" {
		params["model"] = strings.TrimSpace(model)
	}

	if threadID := s.CurrentSessionID(); threadID != "" {
		method = "thread/resume"
		params = map[string]any{
			"threadId":       threadID,
			"approvalPolicy": "on-request",
			"sandbox":        "danger-full-access",
		}
		if strings.TrimSpace(model) != "" {
			params["model"] = strings.TrimSpace(model)
		}
	}

	resp, err := s.requestWithID("thread", method, params)
	if err != nil {
		return err
	}

	result, _ := resp["result"].(map[string]any)
	thread, _ := result["thread"].(map[string]any)
	threadID, _ := thread["id"].(string)
	if strings.TrimSpace(threadID) == "" {
		return fmt.Errorf("codex app-server returned empty thread id")
	}

	s.threadID.Store(strings.TrimSpace(threadID))
	return nil
}

func (s *codexInteractiveSession) request(method string, params map[string]any) (map[string]any, error) {
	id := fmt.Sprintf("req-%d", s.reqCounter.Add(1))
	return s.requestWithID(id, method, params)
}

func (s *codexInteractiveSession) requestWithID(id, method string, params map[string]any) (map[string]any, error) {
	respCh := make(chan map[string]any, 1)

	s.pendingRespMu.Lock()
	s.pendingResp[id] = respCh
	s.pendingRespMu.Unlock()

	if err := s.writeJSON(map[string]any{
		"id":     id,
		"method": method,
		"params": params,
	}); err != nil {
		s.pendingRespMu.Lock()
		delete(s.pendingResp, id)
		s.pendingRespMu.Unlock()
		return nil, err
	}

	select {
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	case resp, ok := <-respCh:
		if !ok {
			return nil, fmt.Errorf("codex app-server closed while waiting for %s", method)
		}
		if errObj, ok := resp["error"].(map[string]any); ok {
			return nil, fmt.Errorf("codex app-server rpc error: %s", extractRPCError(errObj))
		}
		return resp, nil
	}
}

func (s *codexInteractiveSession) writeJSON(payload map[string]any) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.conn.WriteJSON(payload)
}

func (s *codexInteractiveSession) readLoop() {
	defer close(s.events)
	defer close(s.readDone)

	for {
		var msg map[string]any
		if err := s.conn.ReadJSON(&msg); err != nil {
			if s.ctx.Err() == nil {
				currentTurnID, _ := s.turnID.Load().(string)
				if !shouldIgnoreCodexAppServerReadError(err, strings.TrimSpace(currentTurnID) != "") {
					sendEvent(s.events, Event{
						Type:  EventTypeError,
						Error: fmt.Sprintf("codex app-server stream error: %v", err),
					})
				}
			}
			return
		}

		if id, ok := msg["id"].(string); ok {
			if _, hasMethod := msg["method"]; !hasMethod {
				s.pendingRespMu.Lock()
				respCh := s.pendingResp[id]
				delete(s.pendingResp, id)
				s.pendingRespMu.Unlock()
				if respCh != nil {
					respCh <- msg
					close(respCh)
				}
				continue
			}
		}

		if method, _ := msg["method"].(string); strings.Contains(method, "requestApproval") {
			if event, ok := s.parseApprovalRequest(msg); ok {
				sendEvent(s.events, event)
				continue
			}
		}

		if method, _ := msg["method"].(string); method == "turn/started" {
			if params, _ := msg["params"].(map[string]any); params != nil {
				if turn, _ := params["turn"].(map[string]any); turn != nil {
					if turnID, _ := turn["id"].(string); strings.TrimSpace(turnID) != "" {
						s.turnID.Store(strings.TrimSpace(turnID))
					}
				}
			}
		}
		if method, _ := msg["method"].(string); method == "turn/completed" {
			s.turnID.Store("")
		}

		s.deltaSeenMu.Lock()
		events := parseCodexAppServerMessage(nil, msg, s.deltaSeen)
		s.deltaSeenMu.Unlock()

		for _, event := range events {
			sendEvent(s.events, event)
		}
	}
}

func shouldIgnoreCodexAppServerReadError(err error, hasActiveTurn bool) bool {
	if err == nil || hasActiveTurn {
		return false
	}

	if errors.Is(err, io.EOF) {
		return true
	}

	var closeErr *websocket.CloseError
	if errors.As(err, &closeErr) {
		switch closeErr.Code {
		case websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseNoStatusReceived, websocket.CloseAbnormalClosure:
			return true
		}
	}

	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(msg, "unexpected eof") ||
		strings.Contains(msg, "use of closed network connection") ||
		strings.Contains(msg, "broken pipe")
}

func (s *codexInteractiveSession) parseApprovalRequest(msg map[string]any) (Event, bool) {
	method, _ := msg["method"].(string)
	params, _ := msg["params"].(map[string]any)
	if params == nil {
		return Event{}, false
	}

	s.pendingApprovalMu.Lock()
	s.pendingApproval = &codexPendingApproval{
		id:     msg["id"],
		method: method,
		params: params,
	}
	s.pendingApprovalMu.Unlock()

	return Event{
		Type:     EventTypeApproval,
		ToolName: codexApprovalToolName(method),
		ToolInput: summarizeCodexApprovalInput(
			method,
			params,
		),
	}, true
}

func codexApprovalToolName(method string) string {
	switch method {
	case "item/commandExecution/requestApproval":
		return "command_execution"
	case "item/fileChange/requestApproval":
		return "file_change"
	case "item/permissions/requestApproval":
		return "permissions"
	default:
		return method
	}
}

func summarizeCodexApprovalInput(method string, params map[string]any) string {
	switch method {
	case "item/commandExecution/requestApproval":
		if command, _ := params["command"].(string); strings.TrimSpace(command) != "" {
			return strings.TrimSpace(command)
		}
		if reason, _ := params["reason"].(string); strings.TrimSpace(reason) != "" {
			return strings.TrimSpace(reason)
		}
	case "item/fileChange/requestApproval":
		if preview, _ := params["preview"].(string); strings.TrimSpace(preview) != "" {
			return strings.TrimSpace(preview)
		}
	case "item/permissions/requestApproval":
		if reason, _ := params["reason"].(string); strings.TrimSpace(reason) != "" {
			return strings.TrimSpace(reason)
		}
	}

	data, err := json.MarshalIndent(params, "", "  ")
	if err != nil {
		return method
	}
	return string(data)
}

func codexApprovalResult(pending *codexPendingApproval, action ApprovalAction) (map[string]any, error) {
	switch pending.method {
	case "item/commandExecution/requestApproval":
		switch action {
		case ApprovalActionAllow:
			return map[string]any{"decision": "accept"}, nil
		case ApprovalActionAllowAll:
			return map[string]any{"decision": "acceptForSession"}, nil
		case ApprovalActionCancel:
			return map[string]any{"decision": "cancel"}, nil
		}
	case "item/fileChange/requestApproval":
		switch action {
		case ApprovalActionAllow:
			return map[string]any{"decision": "accept"}, nil
		case ApprovalActionAllowAll:
			return map[string]any{"decision": "acceptForSession"}, nil
		case ApprovalActionCancel:
			return map[string]any{"decision": "cancel"}, nil
		}
	case "item/permissions/requestApproval":
		permissions, _ := pending.params["permissions"].(map[string]any)
		scope := "turn"
		if action == ApprovalActionAllowAll {
			scope = "session"
		}
		if action == ApprovalActionCancel {
			permissions = map[string]any{}
		}
		return map[string]any{
			"permissions": permissions,
			"scope":       scope,
		}, nil
	}

	return nil, fmt.Errorf("unsupported codex approval method: %s", pending.method)
}
