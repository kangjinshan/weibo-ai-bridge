package router

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kangjinshan/weibo-ai-bridge/agent"
	"github.com/kangjinshan/weibo-ai-bridge/session"
)

const nativeListenPollInterval = 100 * time.Millisecond

func isListenCommand(content string) bool {
	parts := strings.Fields(strings.TrimSpace(content))
	return len(parts) > 0 && strings.EqualFold(parts[0], "/listen")
}

func isUnlistenCommand(content string) bool {
	parts := strings.Fields(strings.TrimSpace(content))
	return len(parts) > 0 && strings.EqualFold(parts[0], "/unlisten")
}

func (r *Router) handleListenCommand(ctx context.Context, msg *Message, events chan<- agent.Event) error {
	target, err := r.resolveListenTarget(msg)
	if err != nil {
		events <- agent.Event{Type: agent.EventTypeMessage, Content: err.Error()}
		return nil
	}

	path, err := nativeSessionLogPath(target)
	if err != nil {
		events <- agent.Event{Type: agent.EventTypeMessage, Content: err.Error()}
		return nil
	}

	r.stopListen(msg.UserID)

	parent := r.rootCtx
	if parent == nil {
		parent = context.Background()
	}
	runCtx, cancel := context.WithCancel(parent)
	r.listenMu.Lock()
	r.nextListenID++
	runID := r.nextListenID
	r.listenRuns[msg.UserID] = listenRun{id: runID, cancel: cancel, target: target}
	r.listenMu.Unlock()

	go r.runNativeListen(runCtx, msg.UserID, runID, target, path)

	events <- agent.Event{
		Type: agent.EventTypeMessage,
		Content: fmt.Sprintf(
			"开始监听 %s 会话：%s\n发送 /unlisten 停止监听。",
			target.AgentType,
			truncateListTitle(target.Title),
		),
	}

	return nil
}

func (r *Router) handleUnlistenCommand(msg *Message, events chan<- agent.Event) error {
	if r.stopListen(msg.UserID) {
		events <- agent.Event{Type: agent.EventTypeMessage, Content: "已停止监听。"}
		return nil
	}

	events <- agent.Event{Type: agent.EventTypeMessage, Content: "当前没有正在进行的监听。"}
	return nil
}

func (r *Router) stopListen(userID string) bool {
	r.listenMu.Lock()
	run, ok := r.listenRuns[userID]
	if ok {
		delete(r.listenRuns, userID)
	}
	r.listenMu.Unlock()

	if ok && run.cancel != nil {
		run.cancel()
	}
	return ok
}

func (r *Router) runNativeListen(ctx context.Context, userID string, runID int64, target NativeSession, path string) {
	defer func() {
		r.listenMu.Lock()
		if run, ok := r.listenRuns[userID]; ok && run.id == runID {
			delete(r.listenRuns, userID)
		}
		r.listenMu.Unlock()
	}()

	var err error
	if strings.EqualFold(target.AgentType, "hermes") {
		err = r.followHermesSessionFile(ctx, userID, path)
	} else {
		err = r.followJSONLSessionFile(ctx, userID, target.AgentType, path)
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		_ = r.sendReply(context.WithoutCancel(ctx), userID, "监听已停止: "+err.Error())
	}
}

func (r *Router) followJSONLSessionFile(ctx context.Context, userID, agentType, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	fileInfo, err := f.Stat()
	if err != nil {
		return err
	}

	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		return err
	}
	reader := bufio.NewReader(f)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line, err := reader.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				if text := strings.TrimSpace(line); text != "" {
					r.sendNativeListenTexts(ctx, userID, nativeLogLineTexts(agentType, text))
				}
				reopened, nextFile, nextInfo, err := reopenJSONLSessionFileIfRotated(f, path, fileInfo)
				if err != nil {
					return err
				}
				if reopened {
					f = nextFile
					fileInfo = nextInfo
					reader = bufio.NewReader(f)
					continue
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(nativeListenPollInterval):
					continue
				}
			}
			return err
		}
		r.sendNativeListenTexts(ctx, userID, nativeLogLineTexts(agentType, line))
	}
}

func reopenJSONLSessionFileIfRotated(current *os.File, path string, currentInfo os.FileInfo) (bool, *os.File, os.FileInfo, error) {
	pathInfo, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil, nil, nil
		}
		return false, nil, nil, err
	}

	offset, err := current.Seek(0, io.SeekCurrent)
	if err != nil {
		return false, nil, nil, err
	}

	changed := currentInfo == nil || !os.SameFile(currentInfo, pathInfo) || pathInfo.Size() < offset
	if !changed {
		return false, nil, nil, nil
	}

	nextFile, err := os.Open(path)
	if err != nil {
		return false, nil, nil, err
	}
	if err := current.Close(); err != nil {
		_ = nextFile.Close()
		return false, nil, nil, err
	}
	return true, nextFile, pathInfo, nil
}

func (r *Router) followHermesSessionFile(ctx context.Context, userID, path string) error {
	seen := len(readHermesListenMessages(path))

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(nativeListenPollInterval):
		}

		messages := readHermesListenMessages(path)
		if len(messages) <= seen {
			continue
		}
		for _, text := range messages[seen:] {
			if err := r.sendReply(ctx, userID, text); err != nil {
				return err
			}
		}
		seen = len(messages)
	}
}

func (r *Router) sendNativeListenTexts(ctx context.Context, userID string, texts []string) {
	for _, text := range texts {
		if strings.TrimSpace(text) == "" {
			continue
		}
		if err := r.sendReply(ctx, userID, text); err != nil {
			return
		}
	}
}

func (r *Router) resolveListenTarget(msg *Message) (NativeSession, error) {
	if msg == nil {
		return NativeSession{}, errors.New("message cannot be nil")
	}
	if r.sessionMgr == nil || r.commandHandler == nil {
		return NativeSession{}, errors.New("Session manager is not available.")
	}

	parts := strings.Fields(strings.TrimSpace(msg.Content))
	if len(parts) > 1 {
		index, err := parseListenIndex(parts[1])
		if err != nil {
			return NativeSession{}, err
		}
		return r.resolveListenTargetByListNumber(msg.UserID, index)
	}

	sess, ok := r.sessionMgr.GetActiveSession(msg.UserID)
	if !ok || sess == nil {
		return NativeSession{}, errors.New("No active session found. Use /list and /listen <number> first.")
	}

	target := nativeSessionFromManagedSession(sess)
	if strings.TrimSpace(target.ID) == "" {
		return NativeSession{}, errors.New("Current session has no native session ID yet. Use /listen <number> from /list.")
	}
	return target, nil
}

func (r *Router) resolveListenTargetByListNumber(userID string, index int) (NativeSession, error) {
	managedNativeSessions, allNative, _, _ := r.commandHandler.collectSwitchCandidates(userID)
	if len(allNative) > 0 {
		if index < 1 || index > len(allNative) {
			return NativeSession{}, fmt.Errorf("Invalid session number. Use /list to see valid sessions (1-%d).", len(allNative))
		}
		return allNative[index-1], nil
	}

	if len(managedNativeSessions) == 0 {
		return NativeSession{}, errors.New("No sessions found. Use /new to create a new session.")
	}
	if index < 1 || index > len(managedNativeSessions) {
		return NativeSession{}, fmt.Errorf("Invalid session number. Use /list to see valid sessions (1-%d).", len(managedNativeSessions))
	}
	return nativeSessionFromManagedSession(managedNativeSessions[index-1]), nil
}

func parseListenIndex(raw string) (int, error) {
	target := strings.TrimSpace(raw)
	if strings.HasPrefix(strings.ToUpper(target), "N") && len(target) > 1 {
		target = target[1:]
	}
	index, err := strconv.Atoi(target)
	if err != nil || index < 1 {
		return 0, errors.New("Invalid listen command. Use /listen or /listen <number>.")
	}
	return index, nil
}

func nativeSessionFromManagedSession(sess *session.Session) NativeSession {
	if sess == nil {
		return NativeSession{}
	}

	nativeID := nativeSessionIDFromSession(sess.AgentType, sess)
	if strings.TrimSpace(nativeID) == "" && !strings.HasPrefix(strings.TrimSpace(sess.ID), pendingNativeSessionPrefix) {
		nativeID = strings.TrimSpace(sess.ID)
	}
	project := ""
	if workDir, ok := sess.ContextString("work_dir"); ok {
		project = strings.TrimSpace(workDir)
	}

	return NativeSession{
		ID:        nativeID,
		AgentType: strings.TrimSpace(sess.AgentType),
		Project:   project,
		Title:     displaySessionTitle(sess),
		StartedAt: sess.UpdatedAt,
		InBridge:  true,
	}
}

func nativeSessionLogPath(target NativeSession) (string, error) {
	switch strings.ToLower(strings.TrimSpace(target.AgentType)) {
	case "claude":
		return findClaudeSessionLog(target.ID)
	case "codex":
		return findCodexSessionLog(target.ID)
	case "hermes":
		return findHermesSessionLog(target.ID)
	case "gemini":
		return findGeminiSessionLog(target.ID)
	default:
		return "", fmt.Errorf("Unsupported agent type for listen: %s", target.AgentType)
	}
}

func findClaudeSessionLog(sessionID string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	target := strings.TrimSpace(sessionID) + ".jsonl"
	root := filepath.Join(home, ".claude", "projects")
	return findFileByName(root, target)
}

func findCodexSessionLog(sessionID string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if codexHome == "" {
		codexHome = filepath.Join(home, ".codex")
	}

	root := filepath.Join(codexHome, "sessions")
	sessionID = strings.TrimSpace(sessionID)
	var found string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(d.Name()) != ".jsonl" || found != "" {
			return nil
		}
		if codexSessionFileID(path) == sessionID {
			found = path
			return fs.SkipAll
		}
		return nil
	})
	if found == "" {
		return "", fmt.Errorf("No codex session log found for %s", sessionID)
	}
	return found, nil
}

func findHermesSessionLog(sessionID string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	hermesHome := strings.TrimSpace(os.Getenv("HERMES_HOME"))
	if hermesHome == "" {
		hermesHome = filepath.Join(home, ".hermes")
	}
	for _, candidate := range []string{
		filepath.Join(hermesHome, "sessions", "session_"+strings.TrimSpace(sessionID)+".json"),
		filepath.Join(hermesHome, "sessions", strings.TrimSpace(sessionID)+".json"),
	} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("No hermes session log found for %s", sessionID)
}

func findGeminiSessionLog(sessionID string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	geminiHome := strings.TrimSpace(os.Getenv("GEMINI_HOME"))
	if geminiHome == "" {
		geminiHome = filepath.Join(home, ".gemini")
	}

	sessionID = strings.TrimSpace(sessionID)
	var found string
	for _, root := range []string{filepath.Join(geminiHome, "tmp"), filepath.Join(geminiHome, "history")} {
		if found != "" {
			break
		}
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || filepath.Ext(d.Name()) != ".jsonl" || found != "" {
				return nil
			}
			if geminiSessionFileID(path) == sessionID {
				found = path
				return fs.SkipAll
			}
			return nil
		})
	}
	if found == "" {
		return "", fmt.Errorf("No gemini session log found for %s", sessionID)
	}
	return found, nil
}

func findFileByName(root, name string) (string, error) {
	var found string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != name || found != "" {
			return nil
		}
		found = path
		return fs.SkipAll
	})
	if found == "" {
		return "", fmt.Errorf("No session log found for %s", strings.TrimSuffix(name, filepath.Ext(name)))
	}
	return found, nil
}

func codexSessionFileID(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	if !scanner.Scan() {
		return ""
	}

	var meta codexSessionMeta
	if err := json.Unmarshal([]byte(scanner.Text()), &meta); err != nil {
		return ""
	}
	return strings.TrimSpace(meta.Payload.ID)
}

func geminiSessionFileID(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	if !scanner.Scan() {
		return ""
	}

	var meta geminiSessionMeta
	if err := json.Unmarshal([]byte(scanner.Text()), &meta); err != nil {
		return ""
	}
	return strings.TrimSpace(meta.SessionID)
}

func nativeLogLineTexts(agentType, line string) []string {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}

	switch strings.ToLower(strings.TrimSpace(agentType)) {
	case "codex":
		return codexListenLineTexts(line)
	case "claude":
		return claudeListenLineTexts(line)
	case "gemini":
		return geminiListenLineTexts(line)
	default:
		return nil
	}
}

func codexListenLineTexts(line string) []string {
	var entry struct {
		Type    string         `json:"type"`
		Payload map[string]any `json:"payload"`
	}
	if json.Unmarshal([]byte(line), &entry) != nil {
		return nil
	}

	if entry.Type != "response_item" {
		return nil
	}

	itemType, _ := entry.Payload["type"].(string)
	switch itemType {
	case "message":
		role, _ := entry.Payload["role"].(string)
		if role != "user" && role != "assistant" {
			return nil
		}
		text := strings.Join(contentTexts(entry.Payload["content"]), "\n")
		if strings.TrimSpace(text) == "" {
			return nil
		}
		return []string{formatNativeListenMessage(role, text)}
	default:
		return nil
	}
}

func claudeListenLineTexts(line string) []string {
	var entry struct {
		Type    string         `json:"type"`
		Subtype string         `json:"subtype"`
		Content any            `json:"content"`
		Message map[string]any `json:"message"`
		Error   any            `json:"error"`
	}
	if json.Unmarshal([]byte(line), &entry) != nil {
		return nil
	}

	switch entry.Type {
	case "user", "assistant":
		role := entry.Type
		if msgRole, _ := entry.Message["role"].(string); msgRole != "" {
			role = msgRole
		}
		content := entry.Content
		if entry.Message != nil {
			if msgContent, exists := entry.Message["content"]; exists {
				content = msgContent
			}
		}
		text := strings.Join(contentTexts(content), "\n")
		if strings.TrimSpace(text) == "" {
			return nil
		}
		return []string{formatNativeListenMessage(role, text)}
	case "system":
		if entry.Subtype == "api_error" {
			return []string{"错误: " + truncateRunes(line, 300)}
		}
		return nil
	default:
		return nil
	}
}

func geminiListenLineTexts(line string) []string {
	var entry struct {
		Type    string `json:"type"`
		Content any    `json:"content"`
	}
	if json.Unmarshal([]byte(line), &entry) != nil {
		return nil
	}

	switch strings.ToLower(strings.TrimSpace(entry.Type)) {
	case "user", "gemini", "assistant":
		text := strings.Join(contentTexts(entry.Content), "\n")
		if strings.TrimSpace(text) == "" {
			return nil
		}
		role := entry.Type
		if role == "gemini" {
			role = "assistant"
		}
		return []string{formatNativeListenMessage(role, text)}
	default:
		return nil
	}
}

func readHermesListenMessages(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var file hermesSessionFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil
	}

	messages := make([]string, 0, len(file.Messages))
	for _, msg := range file.Messages {
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		if role != "user" && role != "assistant" {
			continue
		}
		text := hermesMessageContentString(msg.Content)
		if strings.TrimSpace(text) == "" {
			continue
		}
		messages = append(messages, formatNativeListenMessage(role, text))
	}
	return messages
}

func contentTexts(content any) []string {
	switch v := content.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return nil
		}
		return []string{v}
	case []any:
		texts := make([]string, 0, len(v))
		for _, item := range v {
			texts = append(texts, contentTexts(item)...)
		}
		return texts
	case map[string]any:
		if text, _ := v["text"].(string); strings.TrimSpace(text) != "" {
			return []string{text}
		}
		if output, _ := v["output"].(string); strings.TrimSpace(output) != "" {
			return []string{output}
		}
		return nil
	default:
		return nil
	}
}

func formatNativeListenMessage(role, text string) string {
	role = strings.ToLower(strings.TrimSpace(role))
	text = strings.TrimSpace(text)
	switch role {
	case "user":
		return "用户: " + text
	case "assistant", "gemini":
		return "AI: " + text
	default:
		if role == "" {
			return text
		}
		return role + ": " + text
	}
}

func truncateRunes(text string, limit int) string {
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "..."
}
