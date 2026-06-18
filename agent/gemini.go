package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/joho/godotenv"
)

// GeminiAgent Gemini CLI Agent 实现。
type GeminiAgent struct {
	name  string
	model string
}

type geminiSession struct {
	sessionID atomic.Value
}

// NewGeminiAgent 创建新的 Gemini Agent。
func NewGeminiAgent(model string) *GeminiAgent {
	return &GeminiAgent{
		name:  "gemini",
		model: model,
	}
}

func (a *GeminiAgent) Name() string {
	return a.name
}

// ExecuteStream 执行 Gemini CLI，并把 stream-json 输出转成统一事件。
func (a *GeminiAgent) ExecuteStream(ctx context.Context, sessionID string, input string) (<-chan Event, error) {
	if !a.IsAvailable() {
		return nil, fmt.Errorf("gemini CLI is not available")
	}

	session := &geminiSession{}
	if strings.TrimSpace(sessionID) != "" {
		session.sessionID.Store(strings.TrimSpace(sessionID))
	}
	if sid := session.CurrentSessionID(); sid != "" {
		if _, err := sanitizeGeminiResumeSessionHistory(sid); err != nil {
			return nil, fmt.Errorf("failed to prepare gemini resume session: %w", err)
		}
	}

	cmd := a.buildCommand(ctx, session, input)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to get stdout pipe: %w", err)
	}

	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start gemini CLI: %w", err)
	}

	events := make(chan Event, 32)

	go func() {
		defer close(events)

		errorParts, readErr := a.streamGeminiOutput(ctx, session, stdout, events)
		if readErr != nil {
			if ctx.Err() != nil {
				return
			}
			emitOrCancel(ctx, events, Event{Type: EventTypeError, Error: readErr.Error()})
			return
		}

		if err := cmd.Wait(); err != nil {
			if ctx.Err() != nil {
				return
			}
			if len(errorParts) > 0 {
				return
			}

			details := joinNonEmpty(errorParts, cleanGeminiStderr(stderrBuf.String()))
			if details == "" {
				details = err.Error()
			}
			emitOrCancel(ctx, events, Event{
				Type:  EventTypeError,
				Error: fmt.Sprintf("gemini CLI failed: %s", details),
			})
			return
		}
	}()

	return events, nil
}

func (s *geminiSession) CurrentSessionID() string {
	v, _ := s.sessionID.Load().(string)
	return strings.TrimSpace(v)
}

func (s *geminiSession) SetCurrentSessionID(sessionID string) bool {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return false
	}
	if s.CurrentSessionID() == sessionID {
		return false
	}
	s.sessionID.Store(sessionID)
	return true
}

func (a *GeminiAgent) buildCommand(ctx context.Context, session *geminiSession, input string) *exec.Cmd {
	args := []string{
		"--skip-trust",
		"--output-format", "stream-json",
		"--include-directories", string(filepath.Separator),
	}
	if AllowAllFromContext(ctx) {
		args = append(args, "-y")
	}
	if strings.TrimSpace(a.model) != "" {
		args = append(args, "--model", strings.TrimSpace(a.model))
	}
	if sid := session.CurrentSessionID(); sid != "" {
		args = append(args, "--resume", sid)
	}
	args = append(args, "--prompt", wrapUserPrompt(input))

	cmd := exec.CommandContext(ctx, "gemini", args...)
	if workDir := WorkDirFromContext(ctx); workDir != "" {
		cmd.Dir = workDir
	}
	cmd.Env = geminiCommandEnv(cmd.Environ())
	return cmd
}

func geminiCommandEnv(base []string) []string {
	env := append([]string(nil), base...)

	ensure := func(key, value string) {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" || envHasNonEmptyValue(env, key) {
			return
		}
		env = append(env, key+"="+value)
	}

	home := envValue(env, "HOME")
	if strings.TrimSpace(home) == "" {
		if userHome, err := os.UserHomeDir(); err == nil {
			home = strings.TrimSpace(userHome)
		}
	}
	ensure("HOME", home)

	userName := envValue(env, "USER")
	if strings.TrimSpace(userName) == "" && strings.TrimSpace(home) != "" {
		userName = filepath.Base(strings.TrimRight(home, string(filepath.Separator)))
	}
	ensure("USER", userName)
	ensure("LOGNAME", firstNonEmpty(envValue(env, "LOGNAME"), userName))
	ensure("SHELL", firstNonEmpty(envValue(env, "SHELL"), "/bin/zsh"))

	if strings.TrimSpace(home) != "" {
		env = appendDotEnvIfMissing(env, filepath.Join(home, ".gemini", ".env"))
	}
	env = appendGeminiPayloadPatchEnv(env)

	return env
}

func appendGeminiPayloadPatchEnv(env []string) []string {
	patchPath, err := ensureGeminiPayloadPatch()
	if err != nil || patchPath == "" {
		return env
	}
	nodeOptions := strings.TrimSpace(envValue(env, "NODE_OPTIONS"))
	importArg := "--import=" + (&url.URL{Scheme: "file", Path: patchPath}).String()
	if strings.Contains(nodeOptions, importArg) {
		return env
	}
	if nodeOptions == "" {
		env = append(env, "NODE_OPTIONS="+importArg)
		return env
	}
	return setEnvValue(env, "NODE_OPTIONS", nodeOptions+" "+importArg)
}

func ensureGeminiPayloadPatch() (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cacheDir, "weibo-ai-bridge")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "gemini-payload-sanitizer.cjs")
	content := []byte(geminiPayloadSanitizerScript)
	if current, err := os.ReadFile(path); err == nil && bytes.Equal(current, content) {
		return path, nil
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func appendDotEnvIfMissing(env []string, path string) []string {
	values, err := godotenv.Read(path)
	if err != nil {
		return env
	}
	for key, value := range values {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" || envHasNonEmptyValue(env, key) {
			continue
		}
		env = append(env, key+"="+value)
	}
	return env
}

func envValue(env []string, key string) string {
	prefix := key + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix)
		}
	}
	return ""
}

func setEnvValue(env []string, key, value string) []string {
	prefix := key + "="
	for i, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			next := append([]string(nil), env...)
			next[i] = key + "=" + value
			return next
		}
	}
	return append(env, key+"="+value)
}

func envHasNonEmptyValue(env []string, key string) bool {
	return strings.TrimSpace(envValue(env, key)) != ""
}

const geminiPayloadSanitizerScript = `(() => {
  const sanitizeToolIDs = (value) => {
    let changed = false;
    const visit = (node) => {
      if (!node || typeof node !== "object") return;
      if (Array.isArray(node)) {
        for (const item of node) visit(item);
        return;
      }
      for (const key of ["functionCall", "function_call", "functionResponse", "function_response"]) {
        const part = node[key];
        if (part && typeof part === "object" && !Array.isArray(part) && Object.prototype.hasOwnProperty.call(part, "id")) {
          delete part.id;
          changed = true;
        }
      }
      for (const child of Object.values(node)) visit(child);
    };
    visit(value);
    return changed;
  };

  const sanitizeBody = (body) => {
    if (typeof body !== "string" || !body.includes("function")) return body;
    try {
      const parsed = JSON.parse(body);
      return sanitizeToolIDs(parsed) ? JSON.stringify(parsed) : body;
    } catch {
      return body;
    }
  };

  if (typeof globalThis.Request === "function") {
    const OriginalRequest = globalThis.Request;
    const PatchedRequest = function(input, init) {
      if (init && typeof init === "object" && "body" in init) {
        init = { ...init, body: sanitizeBody(init.body) };
      }
      return new OriginalRequest(input, init);
    };
    Object.setPrototypeOf(PatchedRequest, OriginalRequest);
    PatchedRequest.prototype = OriginalRequest.prototype;
    globalThis.Request = PatchedRequest;
  }

  if (typeof globalThis.fetch === "function") {
    const originalFetch = globalThis.fetch;
    globalThis.fetch = function(input, init) {
      if (init && typeof init === "object" && "body" in init) {
        init = { ...init, body: sanitizeBody(init.body) };
      }
      return originalFetch.call(this, input, init);
    };
  }
})();`

func sanitizeGeminiResumeSessionHistory(sessionID string) (bool, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return false, nil
	}

	paths, err := findGeminiSessionHistoryFiles(sessionID)
	if err != nil {
		return false, err
	}

	changed := false
	for _, path := range paths {
		fileChanged, err := sanitizeGeminiSessionHistoryFile(path)
		if err != nil {
			return changed, err
		}
		changed = changed || fileChanged
	}
	return changed, nil
}

func findGeminiSessionHistoryFiles(sessionID string) ([]string, error) {
	home, err := geminiHomeDir()
	if err != nil {
		return nil, err
	}

	var matches []string
	for _, root := range []string{filepath.Join(home, "tmp"), filepath.Join(home, "history")} {
		if _, err := os.Stat(root); err != nil {
			continue
		}
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || filepath.Ext(d.Name()) != ".jsonl" {
				return nil
			}
			if !strings.HasPrefix(d.Name(), "session-") {
				return nil
			}
			if geminiSessionFileMatches(path, sessionID) {
				matches = append(matches, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return matches, nil
}

func geminiHomeDir() (string, error) {
	if home := strings.TrimSpace(os.Getenv("GEMINI_HOME")); home != "" {
		return home, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".gemini"), nil
}

func geminiSessionFileMatches(path, sessionID string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var meta map[string]any
		if err := json.Unmarshal(line, &meta); err != nil {
			return false
		}
		for _, key := range []string{"sessionId", "session_id"} {
			if sid, _ := meta[key].(string); strings.TrimSpace(sid) == sessionID {
				return true
			}
		}
		return false
	}
	return false
}

func sanitizeGeminiSessionHistoryFile(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}

	changed := false
	lines := bytes.Split(data, []byte{'\n'})
	for i, line := range lines {
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 {
			continue
		}
		var value any
		if err := json.Unmarshal(trimmed, &value); err != nil {
			continue
		}
		if removeGeminiUnsupportedToolIDs(value) {
			encoded, err := json.Marshal(value)
			if err != nil {
				return changed, err
			}
			lines[i] = encoded
			changed = true
		}
	}
	if !changed {
		return false, nil
	}

	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	return true, os.WriteFile(path, bytes.Join(lines, []byte{'\n'}), mode)
}

func removeGeminiUnsupportedToolIDs(value any) bool {
	changed := false
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if isGeminiToolPartKey(key) {
				if part, ok := child.(map[string]any); ok {
					if _, ok := part["id"]; ok {
						delete(part, "id")
						changed = true
					}
				}
			}
			if key == "toolCalls" {
				if calls, ok := child.([]any); ok {
					for _, call := range calls {
						if callObj, ok := call.(map[string]any); ok {
							if _, ok := callObj["id"]; ok {
								delete(callObj, "id")
								changed = true
							}
						}
					}
				}
			}
			if removeGeminiUnsupportedToolIDs(child) {
				changed = true
			}
		}
	case []any:
		for _, child := range typed {
			if removeGeminiUnsupportedToolIDs(child) {
				changed = true
			}
		}
	}
	return changed
}

func isGeminiToolPartKey(key string) bool {
	switch key {
	case "functionCall", "function_call", "functionResponse", "function_response":
		return true
	default:
		return false
	}
}

func (a *GeminiAgent) streamGeminiOutput(ctx context.Context, session *geminiSession, stdout io.ReadCloser, events chan<- Event) ([]string, error) {
	reader := bufio.NewReader(stdout)
	var errorParts []string

	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return errorParts, fmt.Errorf("failed to read gemini output: %w", err)
		}

		line = bytes.TrimRight(line, "\r\n")
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}

		var raw map[string]any
		if err := json.Unmarshal(line, &raw); err != nil {
			continue
		}

		for _, event := range parseGeminiEvent(session, raw) {
			if event.Type == EventTypeError && strings.TrimSpace(event.Error) != "" {
				errorParts = append(errorParts, event.Error)
			}
			if !emitOrCancel(ctx, events, event) {
				return uniqueNonEmpty(errorParts), ctx.Err()
			}
		}
	}

	return uniqueNonEmpty(errorParts), nil
}

func parseGeminiEvent(session *geminiSession, raw map[string]any) []Event {
	eventType, _ := raw["type"].(string)

	switch eventType {
	case "init":
		if sid, _ := raw["session_id"].(string); session.SetCurrentSessionID(sid) {
			return []Event{{Type: EventTypeSession, SessionID: strings.TrimSpace(sid)}}
		}
	case "message":
		role, _ := raw["role"].(string)
		if role != "assistant" {
			return nil
		}
		content, _ := raw["content"].(string)
		if content == "" {
			return nil
		}
		if delta, _ := raw["delta"].(bool); delta {
			return []Event{{Type: EventTypeDelta, Content: content}}
		}
		return []Event{{Type: EventTypeMessage, Content: content}}
	case "tool_use":
		toolName, _ := raw["tool_name"].(string)
		toolID, _ := raw["tool_id"].(string)
		parameters := raw["parameters"]
		return []Event{{
			Type:      EventTypeToolStart,
			ToolName:  toolName,
			ToolInput: geminiToolInputString(parameters),
			Metadata: map[string]any{
				"tool_id":    toolID,
				"parameters": parameters,
				"status":     "in_progress",
			},
		}}
	case "tool_result":
		toolID, _ := raw["tool_id"].(string)
		status, _ := raw["status"].(string)
		output, _ := raw["output"].(string)
		metadata := map[string]any{
			"tool_id": toolID,
			"status":  status,
			"output":  output,
		}
		if errObj, ok := raw["error"].(map[string]any); ok {
			metadata["error"] = errObj
		}
		return []Event{{Type: EventTypeToolEnd, Metadata: metadata}}
	case "error":
		if message, _ := raw["message"].(string); strings.TrimSpace(message) != "" {
			return []Event{{Type: EventTypeError, Error: strings.TrimSpace(message)}}
		}
	case "result":
		status, _ := raw["status"].(string)
		if status == "error" {
			return []Event{{Type: EventTypeError, Error: geminiResultError(raw)}, {Type: EventTypeDone}}
		}
		return []Event{{Type: EventTypeDone}}
	}

	return nil
}

func geminiToolInputString(value any) string {
	if value == nil {
		return ""
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(data)
}

func geminiResultError(raw map[string]any) string {
	errObj, _ := raw["error"].(map[string]any)
	if message, _ := errObj["message"].(string); strings.TrimSpace(message) != "" {
		return strings.TrimSpace(message)
	}
	if errType, _ := errObj["type"].(string); strings.TrimSpace(errType) != "" {
		return strings.TrimSpace(errType)
	}
	return "gemini CLI returned error result"
}

func cleanGeminiStderr(stderr string) string {
	var lines []string
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// IsAvailable 检查 gemini CLI 是否可用。
func (a *GeminiAgent) IsAvailable() bool {
	_, err := exec.LookPath("gemini")
	return err == nil
}
