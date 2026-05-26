package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// HermesAgent Hermes CLI Agent 实现。
type HermesAgent struct {
	name     string
	model    string
	profile  string
	provider string
}

// NewHermesAgent 创建新的 Hermes Agent。
func NewHermesAgent(model, profile, provider string) *HermesAgent {
	return &HermesAgent{
		name:     "hermes",
		model:    strings.TrimSpace(model),
		profile:  strings.TrimSpace(profile),
		provider: strings.TrimSpace(provider),
	}
}

// Name 返回 Agent 名称。
func (a *HermesAgent) Name() string {
	return a.name
}

type hermesSession struct {
	sessionID atomic.Value
}

type hermesInteractiveSession struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	events chan Event

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	alive  atomic.Bool

	writeMu sync.Mutex
	reqID   atomic.Int64

	pendingMu sync.Mutex
	pending   map[int64]chan hermesACPResponse

	promptMu  sync.Mutex
	promptIDs map[int64]struct{}

	sessionID atomic.Value

	suppressReplay atomic.Bool
	replayDropped  chan struct{}

	approvalMu sync.Mutex
	approval   *hermesPendingApproval

	wg       sync.WaitGroup
	waitOnce sync.Once
	waitErr  error
}

type hermesACPResponse struct {
	Result map[string]any
	Error  string
}

type hermesPendingApproval struct {
	id      int64
	options []hermesPermissionOption
	tool    map[string]any
}

type hermesPermissionOption struct {
	OptionID string
	Kind     string
	Name     string
}

const (
	hermesReplayQuietPeriod = 250 * time.Millisecond
	hermesReplayMaxDrain    = 2 * time.Second
)

// Execute 执行 AI 任务并等待完整结果。
func (a *HermesAgent) Execute(ctx context.Context, sessionID string, input string) (string, error) {
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
func (a *HermesAgent) ExecuteStream(ctx context.Context, sessionID string, input string) (<-chan Event, error) {
	if !a.IsAvailable() {
		return nil, fmt.Errorf("hermes CLI is not available")
	}

	session := &hermesSession{}
	if strings.TrimSpace(sessionID) != "" {
		session.SetCurrentSessionID(sessionID)
	}

	cmd := a.buildCommand(session, input)
	cmd = exec.CommandContext(ctx, cmd.Path, cmd.Args[1:]...)
	if workDir := WorkDirFromContext(ctx); workDir != "" {
		cmd.Dir = workDir
	}

	events := make(chan Event, 8)
	go func() {
		defer close(events)

		output, err := cmd.CombinedOutput()
		if err != nil {
			details := strings.TrimSpace(string(output))
			if details == "" {
				details = err.Error()
			}
			emitOrCancel(ctx, events, Event{Type: EventTypeError, Error: fmt.Sprintf("hermes CLI failed: %s", details)})
			return
		}

		for _, event := range parseHermesOutput(session, string(output)) {
			if !emitOrCancel(ctx, events, event) {
				return
			}
		}
	}()

	return events, nil
}

func (a *HermesAgent) StartSession(ctx context.Context, sessionID string) (InteractiveSession, error) {
	if !a.IsAvailable() {
		return nil, fmt.Errorf("hermes CLI is not available")
	}

	childCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(childCtx, "hermes", "acp")
	if workDir := WorkDirFromContext(ctx); workDir != "" {
		cmd.Dir = workDir
	}
	cmd.Env = hermesACPEnv(cmd.Environ(), a.model, a.provider)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to get hermes stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to get hermes stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to get hermes stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to start hermes ACP: %w", err)
	}

	session := &hermesInteractiveSession{
		cmd:           cmd,
		stdin:         stdin,
		events:        make(chan Event, 128),
		ctx:           childCtx,
		cancel:        cancel,
		done:          make(chan struct{}),
		pending:       make(map[int64]chan hermesACPResponse),
		promptIDs:     make(map[int64]struct{}),
		replayDropped: make(chan struct{}, 1),
	}
	session.alive.Store(true)
	if strings.TrimSpace(sessionID) != "" {
		session.sessionID.Store(strings.TrimSpace(sessionID))
		session.suppressReplay.Store(true)
	}

	session.wg.Add(1)
	go func() {
		defer session.wg.Done()
		session.readLoop(stdout)
	}()
	session.wg.Add(1)
	go func() {
		defer session.wg.Done()
		_, _ = io.Copy(io.Discard, stderr)
	}()

	if _, err := session.request(ctx, "initialize", hermesACPInitializeParams()); err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("failed to initialize hermes ACP: %w", err)
	}

	workDir := WorkDirFromContext(ctx)
	if strings.TrimSpace(workDir) == "" {
		if cwd, err := os.Getwd(); err == nil {
			workDir = cwd
		}
	}

	method := "session/new"
	params := map[string]any{
		"cwd":        workDir,
		"mcpServers": []any{},
	}
	if strings.TrimSpace(sessionID) != "" {
		method = "session/resume"
		params["sessionId"] = strings.TrimSpace(sessionID)
	}
	resp, err := session.request(ctx, method, params)
	if err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("failed to create hermes ACP session: %w", err)
	}
	if method == "session/resume" {
		if err := session.waitForReplayDrain(ctx); err != nil {
			_ = session.Close()
			return nil, err
		}
	}
	if sid, _ := resp.Result["sessionId"].(string); strings.TrimSpace(sid) != "" {
		session.SetCurrentSessionID(sid)
	}
	if session.CurrentSessionID() == "" {
		_ = session.Close()
		return nil, fmt.Errorf("hermes ACP returned empty session id")
	}

	return session, nil
}

func (a *HermesAgent) buildCommand(session *hermesSession, input string) *exec.Cmd {
	args := make([]string, 0, 16)
	if strings.TrimSpace(a.profile) != "" {
		args = append(args, "--profile", strings.TrimSpace(a.profile))
	}

	args = append(args, "chat", "--quiet", "--source", "tool")

	if strings.TrimSpace(a.model) != "" {
		args = append(args, "--model", strings.TrimSpace(a.model))
	}
	if strings.TrimSpace(a.provider) != "" {
		args = append(args, "--provider", strings.TrimSpace(a.provider))
	}
	if session != nil {
		if sessionID := session.CurrentSessionID(); sessionID != "" {
			args = append(args, "--resume", sessionID)
		}
	}

	args = append(args, "--query", wrapUserPrompt(input))
	return exec.Command("hermes", args...)
}

func hermesACPEnv(base []string, model, provider string) []string {
	env := append([]string{}, base...)
	if strings.TrimSpace(model) != "" {
		env = append(env, "HERMES_INFERENCE_MODEL="+strings.TrimSpace(model))
	}
	if strings.TrimSpace(provider) != "" {
		env = append(env, "HERMES_INFERENCE_PROVIDER="+strings.TrimSpace(provider))
	}
	return env
}

func hermesACPInitializeParams() map[string]any {
	return map[string]any{
		"protocolVersion": 1,
		"clientCapabilities": map[string]any{
			"fs": map[string]any{
				"readTextFile":  false,
				"writeTextFile": false,
			},
			"terminal": false,
		},
		"clientInfo": map[string]any{
			"name":    "weibo-ai-bridge",
			"title":   "Weibo AI Bridge",
			"version": "1.0.0",
		},
	}
}

func (s *hermesSession) CurrentSessionID() string {
	if s == nil {
		return ""
	}
	v, _ := s.sessionID.Load().(string)
	return strings.TrimSpace(v)
}

func (s *hermesSession) SetCurrentSessionID(sessionID string) bool {
	if s == nil {
		return false
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || s.CurrentSessionID() == sessionID {
		return false
	}
	s.sessionID.Store(sessionID)
	return true
}

func (s *hermesInteractiveSession) Send(input string) error {
	if !s.alive.Load() {
		return fmt.Errorf("hermes session is not running")
	}
	sessionID := strings.TrimSpace(s.CurrentSessionID())
	if sessionID == "" {
		return fmt.Errorf("hermes ACP session id is empty")
	}
	promptText := hermesACPPromptText(input, s.hasActivePrompt())

	id := s.nextRequestID()
	s.trackPromptRequest(id)
	if err := s.writeJSON(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "session/prompt",
		"params": map[string]any{
			"sessionId": sessionID,
			"prompt": []map[string]any{
				{
					"type": "text",
					"text": promptText,
				},
			},
		},
	}); err != nil {
		s.untrackPromptRequest(id)
		return err
	}
	return nil
}

func (s *hermesInteractiveSession) RespondApproval(action ApprovalAction) error {
	if !s.alive.Load() {
		return fmt.Errorf("hermes session is not running")
	}

	s.approvalMu.Lock()
	pending := s.approval
	if pending != nil {
		s.approval = nil
	}
	s.approvalMu.Unlock()

	if pending == nil {
		return fmt.Errorf("no pending hermes approval")
	}

	outcome := map[string]any{
		"outcome": "cancelled",
	}
	if action == ApprovalActionAllow || action == ApprovalActionAllowAll {
		optionID := hermesApprovalOptionID(pending.options, action)
		if optionID == "" {
			optionID = "allow_once"
		}
		outcome = map[string]any{
			"outcome":  "selected",
			"optionId": optionID,
		}
	}

	return s.writeJSON(map[string]any{
		"jsonrpc": "2.0",
		"id":      pending.id,
		"result": map[string]any{
			"outcome": outcome,
		},
	})
}

func (s *hermesInteractiveSession) Events() <-chan Event {
	return s.events
}

func (s *hermesInteractiveSession) CurrentSessionID() string {
	if s == nil {
		return ""
	}
	v, _ := s.sessionID.Load().(string)
	return strings.TrimSpace(v)
}

func (s *hermesInteractiveSession) SetCurrentSessionID(sessionID string) bool {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || s.CurrentSessionID() == sessionID {
		return false
	}
	s.sessionID.Store(sessionID)
	return true
}

func (s *hermesInteractiveSession) Close() error {
	if !s.alive.Swap(false) {
		return nil
	}
	if s.stdin != nil {
		_ = s.stdin.Close()
	}
	if s.cancel != nil {
		s.cancel()
	}
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}

	select {
	case <-s.done:
	case <-time.After(3 * time.Second):
		if s.cmd != nil && s.cmd.Process != nil {
			_ = s.waitProcess()
		}
	}
	s.wg.Wait()
	return nil
}

func (s *hermesInteractiveSession) waitProcess() error {
	if s == nil || s.cmd == nil {
		return nil
	}

	s.waitOnce.Do(func() {
		s.waitErr = s.cmd.Wait()
	})
	return s.waitErr
}

func (s *hermesInteractiveSession) request(ctx context.Context, method string, params map[string]any) (hermesACPResponse, error) {
	if !s.alive.Load() {
		return hermesACPResponse{}, fmt.Errorf("hermes session is not running")
	}

	id := s.nextRequestID()
	respCh := make(chan hermesACPResponse, 1)
	s.pendingMu.Lock()
	s.pending[id] = respCh
	s.pendingMu.Unlock()

	if err := s.writeJSON(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}); err != nil {
		s.removePending(id)
		return hermesACPResponse{}, err
	}

	select {
	case <-ctx.Done():
		s.removePending(id)
		return hermesACPResponse{}, ctx.Err()
	case <-s.done:
		s.removePending(id)
		return hermesACPResponse{}, fmt.Errorf("hermes ACP exited")
	case resp := <-respCh:
		if resp.Error != "" {
			return resp, errors.New(resp.Error)
		}
		return resp, nil
	case <-time.After(30 * time.Second):
		s.removePending(id)
		return hermesACPResponse{}, fmt.Errorf("timed out waiting for hermes ACP %s", method)
	}
}

func (s *hermesInteractiveSession) nextRequestID() int64 {
	return s.reqID.Add(1)
}

func (s *hermesInteractiveSession) writeJSON(payload map[string]any) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal hermes ACP payload: %w", err)
	}
	data = append(data, '\n')
	if _, err := s.stdin.Write(data); err != nil {
		return fmt.Errorf("failed to write hermes ACP payload: %w", err)
	}
	return nil
}

func (s *hermesInteractiveSession) readLoop(stdout io.Reader) {
	defer close(s.done)
	defer close(s.events)
	defer s.alive.Store(false)
	defer func() {
		_ = s.waitProcess()
	}()

	reader := bufio.NewReader(stdout)
	for {
		line, err := reader.ReadString('\n')
		if strings.TrimSpace(line) != "" {
			s.handleACPLine(line)
		}
		if err != nil {
			if err != io.EOF && s.alive.Load() {
				emitOrCancel(s.ctx, s.events, Event{Type: EventTypeError, Error: fmt.Sprintf("hermes ACP read failed: %v", err)})
			}
			return
		}
	}
}

func (s *hermesInteractiveSession) handleACPLine(line string) {
	var msg map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &msg); err != nil {
		return
	}

	if id, ok := jsonRPCID(msg["id"]); ok && msg["method"] == nil {
		resp := hermesACPResponse{Result: map[string]any{}}
		if result, _ := msg["result"].(map[string]any); result != nil {
			resp.Result = result
		}
		if errorObj, _ := msg["error"].(map[string]any); errorObj != nil {
			resp.Error, _ = errorObj["message"].(string)
			if strings.TrimSpace(resp.Error) == "" {
				resp.Error = fmt.Sprintf("%v", errorObj)
			}
		}
		if ch := s.removePending(id); ch != nil {
			ch <- resp
			return
		}
		if s.untrackPromptRequest(id) {
			if resp.Error != "" {
				emitOrCancel(s.ctx, s.events, Event{Type: EventTypeError, Error: resp.Error})
			}
			emitOrCancel(s.ctx, s.events, Event{Type: EventTypeDone})
		}
		return
	}

	method, _ := msg["method"].(string)
	switch method {
	case "session/update":
		if params, _ := msg["params"].(map[string]any); params != nil {
			s.handleSessionUpdate(params)
		}
	case "session/request_permission":
		if id, ok := jsonRPCID(msg["id"]); ok {
			params, _ := msg["params"].(map[string]any)
			s.handlePermissionRequest(id, params)
		}
	default:
		if id, ok := jsonRPCID(msg["id"]); ok {
			_ = s.writeJSON(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"error": map[string]any{
					"code":    -32601,
					"message": "method not found",
				},
			})
		}
	}
}

func (s *hermesInteractiveSession) handleSessionUpdate(params map[string]any) {
	if sid, _ := params["sessionId"].(string); strings.TrimSpace(sid) != "" {
		if s.SetCurrentSessionID(sid) {
			emitOrCancel(s.ctx, s.events, Event{Type: EventTypeSession, SessionID: strings.TrimSpace(sid)})
		}
	}

	update, _ := params["update"].(map[string]any)
	if update == nil {
		return
	}
	updateType, _ := update["sessionUpdate"].(string)
	if s.shouldSuppressReplayUpdate(updateType) {
		return
	}
	switch updateType {
	case "agent_message_chunk":
		if text := acpContentText(update["content"]); text != "" {
			if isHermesACPProviderFailure(text) {
				emitOrCancel(s.ctx, s.events, Event{Type: EventTypeError, Error: text})
				return
			}
			emitOrCancel(s.ctx, s.events, Event{Type: EventTypeDelta, Content: text})
		}
	case "agent_thought_chunk":
		return
	case "tool_call":
		toolID, _ := update["toolCallId"].(string)
		title, _ := update["title"].(string)
		emitOrCancel(s.ctx, s.events, Event{Type: EventTypeToolStart, ToolName: firstNonEmpty(title, toolID), ToolInput: acpRawText(update)})
	case "tool_call_update":
		toolID, _ := update["toolCallId"].(string)
		title, _ := update["title"].(string)
		emitOrCancel(s.ctx, s.events, Event{Type: EventTypeToolEnd, ToolName: firstNonEmpty(title, toolID), ToolInput: acpRawText(update)})
	}
}

func isHermesACPProviderFailure(text string) bool {
	normalized := strings.ToLower(strings.TrimSpace(text))
	return strings.Contains(normalized, "api call failed after 3 retries") &&
		strings.Contains(normalized, "http 404") &&
		strings.Contains(normalized, "resource not found")
}

func (s *hermesInteractiveSession) shouldSuppressReplayUpdate(updateType string) bool {
	if !s.suppressReplay.Load() {
		return false
	}
	switch updateType {
	case "user_message_chunk", "agent_message_chunk", "tool_call", "tool_call_update":
		select {
		case s.replayDropped <- struct{}{}:
		default:
		}
		return true
	default:
		return false
	}
}

func (s *hermesInteractiveSession) waitForReplayDrain(ctx context.Context) error {
	if !s.suppressReplay.Load() {
		return nil
	}
	defer s.suppressReplay.Store(false)

	quiet := time.NewTimer(hermesReplayQuietPeriod)
	defer quiet.Stop()
	maxWait := time.NewTimer(hermesReplayMaxDrain)
	defer maxWait.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.done:
			return fmt.Errorf("hermes ACP exited")
		case <-s.replayDropped:
			resetTimer(quiet, hermesReplayQuietPeriod)
		case <-quiet.C:
			return nil
		case <-maxWait.C:
			return nil
		}
	}
}

func resetTimer(timer *time.Timer, duration time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(duration)
}

func (s *hermesInteractiveSession) handlePermissionRequest(id int64, params map[string]any) {
	pending := &hermesPendingApproval{
		id:   id,
		tool: map[string]any{},
	}
	if params != nil {
		if options, _ := params["options"].([]any); len(options) > 0 {
			pending.options = make([]hermesPermissionOption, 0, len(options))
			for _, raw := range options {
				opt, _ := raw.(map[string]any)
				if opt == nil {
					continue
				}
				optionID, _ := opt["optionId"].(string)
				kind, _ := opt["kind"].(string)
				name, _ := opt["name"].(string)
				pending.options = append(pending.options, hermesPermissionOption{
					OptionID: optionID,
					Kind:     kind,
					Name:     name,
				})
			}
		}
		if tool, _ := params["toolCall"].(map[string]any); tool != nil {
			pending.tool = tool
		}
	}

	s.approvalMu.Lock()
	s.approval = pending
	s.approvalMu.Unlock()

	title, _ := pending.tool["title"].(string)
	emitOrCancel(s.ctx, s.events, Event{
		Type:      EventTypeApproval,
		ToolName:  firstNonEmpty(title, "hermes"),
		ToolInput: acpRawText(pending.tool),
	})
}

func (s *hermesInteractiveSession) removePending(id int64) chan hermesACPResponse {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	ch := s.pending[id]
	delete(s.pending, id)
	return ch
}

func (s *hermesInteractiveSession) trackPromptRequest(id int64) {
	s.promptMu.Lock()
	defer s.promptMu.Unlock()
	s.promptIDs[id] = struct{}{}
}

func (s *hermesInteractiveSession) untrackPromptRequest(id int64) bool {
	s.promptMu.Lock()
	defer s.promptMu.Unlock()
	if _, ok := s.promptIDs[id]; !ok {
		return false
	}
	delete(s.promptIDs, id)
	return true
}

func (s *hermesInteractiveSession) hasActivePrompt() bool {
	s.promptMu.Lock()
	defer s.promptMu.Unlock()
	return len(s.promptIDs) > 0
}

func hermesACPPromptText(input string, activePrompt bool) string {
	if activePrompt {
		return "/steer " + strings.TrimSpace(input)
	}
	return wrapUserPrompt(input)
}

func jsonRPCID(raw any) (int64, bool) {
	switch v := raw.(type) {
	case float64:
		return int64(v), true
	case int64:
		return v, true
	case int:
		return int64(v), true
	case string:
		var id int64
		if _, err := fmt.Sscanf(v, "%d", &id); err == nil {
			return id, true
		}
	}
	return 0, false
}

func acpContentText(raw any) string {
	content, _ := raw.(map[string]any)
	if content == nil {
		return ""
	}
	text, _ := content["text"].(string)
	return text
}

func acpRawText(raw any) string {
	if raw == nil {
		return ""
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return fmt.Sprintf("%v", raw)
	}
	return string(data)
}

func hermesApprovalOptionID(options []hermesPermissionOption, action ApprovalAction) string {
	wantAlways := action == ApprovalActionAllowAll
	for _, option := range options {
		kind := strings.ToLower(strings.TrimSpace(option.Kind))
		if wantAlways && kind == "allow_always" {
			return option.OptionID
		}
		if !wantAlways && kind == "allow_once" {
			return option.OptionID
		}
	}
	for _, option := range options {
		kind := strings.ToLower(strings.TrimSpace(option.Kind))
		if strings.HasPrefix(kind, "allow") {
			return option.OptionID
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func parseHermesOutput(session *hermesSession, output string) []Event {
	var contentLines []string
	sessionID := ""

	for _, line := range strings.Split(output, "\n") {
		line = stripANSI(strings.TrimRight(line, "\r"))
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if len(contentLines) > 0 {
				contentLines = append(contentLines, "")
			}
			continue
		}

		if isHermesChromeLine(trimmed) {
			continue
		}

		if sid, ok := parseHermesSessionLine(trimmed); ok {
			sessionID = sid
			continue
		}

		contentLines = append(contentLines, line)
	}

	content := strings.TrimSpace(strings.Join(contentLines, "\n"))
	events := make([]Event, 0, 3)
	if sessionID != "" && (session == nil || session.SetCurrentSessionID(sessionID)) {
		events = append(events, Event{Type: EventTypeSession, SessionID: sessionID})
	}
	if content != "" {
		events = append(events, Event{Type: EventTypeMessage, Content: content})
	}
	events = append(events, Event{Type: EventTypeDone})
	return events
}

func parseHermesSessionLine(line string) (string, bool) {
	lower := strings.ToLower(strings.TrimSpace(line))
	for _, prefix := range []string{"session_id:", "session id:", "session:"} {
		if strings.HasPrefix(lower, prefix) {
			sessionID := strings.TrimSpace(line[len(prefix):])
			return sessionID, sessionID != ""
		}
	}
	return "", false
}

func isHermesChromeLine(line string) bool {
	return strings.HasPrefix(line, "╭") ||
		strings.HasPrefix(line, "╰") ||
		strings.HasPrefix(line, "├") ||
		strings.HasPrefix(line, "╞") ||
		strings.HasPrefix(line, "─")
}

func stripANSI(s string) string {
	var b strings.Builder
	inEscape := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if inEscape {
			if ch >= '@' && ch <= '~' {
				inEscape = false
			}
			continue
		}
		if ch == 0x1b {
			inEscape = true
			continue
		}
		b.WriteByte(ch)
	}
	return b.String()
}

// IsAvailable 检查 hermes CLI 是否可用。
func (a *HermesAgent) IsAvailable() bool {
	_, err := exec.LookPath("hermes")
	return err == nil
}
