package router

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kangjinshan/weibo-ai-bridge/agent"
	"github.com/kangjinshan/weibo-ai-bridge/session"
)

var superPeerReviewTimeout = 3 * time.Minute

const (
	superAutoApprovalNotice           = "Super：对侧请求已自动审批通过。"
	superPeerReviewTimeoutNotice      = "Super：对侧分析超时（180秒），已跳过，不影响当前回复。"
	superPeerReviewFailedNotice       = "Super：对侧分析失败，已跳过，不影响当前回复。"
	superPeerReviewUnsupportedApprove = "Super：对侧出现审批请求，当前路径无法自动审批，已跳过本次复盘。"
)

type superTurnCollector struct {
	output   strings.Builder
	hasError bool
}

func (c *superTurnCollector) Add(event agent.Event) {
	switch event.Type {
	case agent.EventTypeDelta, agent.EventTypeMessage:
		if strings.TrimSpace(event.Content) != "" {
			c.output.WriteString(event.Content)
		}
	case agent.EventTypeError:
		if strings.TrimSpace(event.Error) != "" {
			c.hasError = true
		}
	}
}

func (c *superTurnCollector) Output() string {
	return strings.TrimSpace(c.output.String())
}

func (c *superTurnCollector) HasError() bool {
	return c.hasError
}

// handleAIMessage 处理 AI 消息（非命令消息）
func (r *Router) handleAIMessage(ctx context.Context, msg *Message) (*Response, error) {
	if r.agentMgr == nil {
		return &Response{
			Success: false,
			Content: "Agent manager is not available",
		}, nil
	}

	if r.sessionMgr == nil {
		return &Response{
			Success: false,
			Content: "Session manager is not available",
		}, nil
	}

	sess, sessionKey, agentSessionID, currentAgent, err := r.resolveAgentExecution(msg)
	if err != nil {
		return &Response{
			Success: false,
			Content: err.Error(),
		}, nil
	}

	injectedFeedback := ""
	execInput := msg.Content
	if isSuperModeEnabled(sess) {
		if feedback, ready := superFeedbackReadyForAgent(sess, sess.AgentType); ready {
			injectedFeedback = strings.TrimSpace(feedback)
			execInput = buildSuperInjectedInput(msg.Content, injectedFeedback)
		}
	}

	execCtx := agent.WithWorkDir(ctx, sessionWorkDir(sess))
	execCtx = agent.WithAllowAll(execCtx, isSuperAutoApproveEnabled(sess))
	response, err := currentAgent.Execute(execCtx, agentSessionID, execInput)
	if err != nil {
		return &Response{
			Success: false,
			Content: fmt.Sprintf("AI execution failed: %v", err),
		}, nil
	}

	if strings.TrimSpace(injectedFeedback) != "" {
		clearSuperFeedbackForAgentIfMatch(r.sessionMgr, sess.ID, sess.AgentType, injectedFeedback)
	}

	if sessionKey != "" {
		if newSessionID := extractSessionID(response); newSessionID != "" {
			sess = r.bindAgentNativeSessionID(sess, sessionKey, newSessionID)
			response = removeSessionIDMarker(response)
		}
	}

	return &Response{
		Success: true,
		Content: response,
	}, nil
}

func (r *Router) streamAIMessage(ctx context.Context, msg *Message, events chan<- agent.Event) error {
	if r.agentMgr == nil {
		return errors.New("Agent manager is not available")
	}
	if r.sessionMgr == nil {
		return errors.New("Session manager is not available")
	}

	sess, sessionKey, agentSessionID, currentAgent, err := r.resolveAgentExecution(msg)
	if err != nil {
		return err
	}

	currentAgentType := strings.TrimSpace(sess.AgentType)
	if currentAgentType == "" {
		currentAgentType = mapAgentName(currentAgent.Name())
	}

	injectedFeedback := ""
	execInput := msg.Content
	allowAllForTurn := isSuperAutoApproveEnabled(sess)
	superEnabled := isSuperModeEnabled(sess)
	if superEnabled {
		if feedback, ready := superFeedbackReadyForAgent(sess, currentAgentType); ready {
			injectedFeedback = strings.TrimSpace(feedback)
			execInput = buildSuperInjectedInput(msg.Content, injectedFeedback)
		}
	}

	collector := &superTurnCollector{}
	emit := func(event agent.Event) {
		collector.Add(event)
		emitRouterEvent(ctx, events, event)
	}

	execCtx := agent.WithWorkDir(ctx, sessionWorkDir(sess))
	execCtx = agent.WithAllowAll(execCtx, allowAllForTurn)
	if interactiveAgent, ok := currentAgent.(agent.InteractiveAgent); ok {
		execMsg := *msg
		execMsg.Content = execInput
		if err := r.streamInteractiveAIMessage(execCtx, &execMsg, sess, sessionKey, agentSessionID, interactiveAgent, emit); err != nil {
			return err
		}
	} else {
		stream, err := currentAgent.ExecuteStream(execCtx, agentSessionID, execInput)
		if err != nil {
			return err
		}

		for event := range stream {
			if event.Type == agent.EventTypeDone {
				continue
			}

			if event.Type == agent.EventTypeSession && sessionKey != "" && strings.TrimSpace(event.SessionID) != "" {
				sess = r.bindAgentNativeSessionID(sess, sessionKey, event.SessionID)
			}

			emit(event)
		}
	}

	if !collector.HasError() && strings.TrimSpace(injectedFeedback) != "" {
		clearSuperFeedbackForAgentIfMatch(r.sessionMgr, sess.ID, currentAgentType, injectedFeedback)
	}

	if superEnabled && !collector.HasError() {
		r.launchSuperPeerReview(
			sess.ID,
			msg.UserID,
			currentAgentType,
			msg.Content,
			collector.Output(),
			sessionWorkDir(sess),
		)
	}

	return nil
}

func (r *Router) launchSuperPeerReview(sessionID, userID, currentAgentType, userInput, mainOutput, workDir string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	if strings.TrimSpace(mainOutput) == "" {
		return
	}

	reviewCtx, reviewID, cancel := r.beginSuperPeerReview(sessionID)
	go func() {
		defer cancel()
		defer r.endSuperPeerReview(sessionID, reviewID)

		if workDir != "" {
			reviewCtx = agent.WithWorkDir(reviewCtx, workDir)
		}
		r.runSuperPeerReview(reviewCtx, reviewID, sessionID, userID, currentAgentType, userInput, mainOutput)
	}()
}

func (r *Router) beginSuperPeerReview(sessionID string) (context.Context, int64, context.CancelFunc) {
	r.superReviewMu.Lock()
	defer r.superReviewMu.Unlock()

	if existing, ok := r.superReviews[sessionID]; ok && existing.cancel != nil {
		existing.cancel()
	}

	r.nextReviewID++
	reviewID := r.nextReviewID
	ctx, cancel := context.WithTimeout(context.Background(), superPeerReviewTimeout)
	r.superReviews[sessionID] = superReviewRun{
		id:     reviewID,
		cancel: cancel,
	}

	return ctx, reviewID, cancel
}

func (r *Router) endSuperPeerReview(sessionID string, reviewID int64) {
	r.superReviewMu.Lock()
	defer r.superReviewMu.Unlock()

	current, ok := r.superReviews[sessionID]
	if !ok || current.id != reviewID {
		return
	}
	delete(r.superReviews, sessionID)
}

func (r *Router) runSuperPeerReview(
	ctx context.Context,
	reviewID int64,
	sessionID string,
	userID string,
	currentAgentType string,
	userInput string,
	mainOutput string,
) {
	if r.agentMgr == nil || r.sessionMgr == nil {
		return
	}

	sess, ok := r.sessionMgr.Get(sessionID)
	if !ok || sess == nil || !isSuperModeEnabled(sess) {
		return
	}

	peerAgentType := oppositeAgentType(currentAgentType)
	if peerAgentType == "" {
		return
	}

	peerAgent := r.agentMgr.ResolveAgent(peerAgentType)
	if peerAgent == nil {
		return
	}

	reviewPrompt := buildSuperPeerReviewPrompt(currentAgentType, userInput, mainOutput)
	var (
		feedback string
		err      error
	)
	if interactivePeer, ok := peerAgent.(agent.InteractiveAgent); ok {
		feedback, err = r.runSuperPeerReviewInteractive(ctx, sessionID, userID, peerAgentType, interactivePeer, reviewPrompt)
	} else {
		feedback, err = r.runSuperPeerReviewStream(ctx, sessionID, peerAgentType, peerAgent, reviewPrompt)
	}

	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) || !r.isCurrentSuperPeerReview(sessionID, reviewID) {
			return
		}
		if errors.Is(err, context.DeadlineExceeded) {
			r.sendSuperNotice(userID, superPeerReviewTimeoutNotice)
			return
		}
		if strings.TrimSpace(err.Error()) == superPeerReviewUnsupportedApprove {
			r.sendSuperNotice(userID, superPeerReviewUnsupportedApprove)
			return
		}
		r.sendSuperNotice(userID, superPeerReviewFailedNotice)
		return
	}

	latest, ok := r.sessionMgr.Get(sessionID)
	if !ok || latest == nil || !isSuperModeEnabled(latest) {
		return
	}
	if !r.isCurrentSuperPeerReview(sessionID, reviewID) {
		return
	}
	setSuperFeedbackForAgent(r.sessionMgr, sessionID, currentAgentType, feedback)
}

func (r *Router) isCurrentSuperPeerReview(sessionID string, reviewID int64) bool {
	r.superReviewMu.Lock()
	defer r.superReviewMu.Unlock()

	current, ok := r.superReviews[sessionID]
	return ok && current.id == reviewID
}

func (r *Router) runSuperPeerReviewInteractive(
	ctx context.Context,
	sessionID string,
	userID string,
	peerAgentType string,
	interactivePeer agent.InteractiveAgent,
	input string,
) (string, error) {
	sess, ok := r.sessionMgr.Get(sessionID)
	if !ok || sess == nil {
		return "", errors.New("session not found")
	}

	peerSessionID := ""
	peerSessionKey := agentSessionContextKey(peerAgentType)
	if peerSessionKey != "" {
		peerSessionID = sessionContextString(sess, peerSessionKey)
	}

	liveSession, err := interactivePeer.StartSession(ctx, peerSessionID)
	if err != nil {
		return "", err
	}
	defer func() { _ = liveSession.Close() }()

	if sid := strings.TrimSpace(liveSession.CurrentSessionID()); sid != "" && peerSessionKey != "" {
		r.sessionMgr.UpdateSession(sessionID, peerSessionKey, sid)
	}

	if err := liveSession.Send(input); err != nil {
		return "", err
	}

	var feedback strings.Builder
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case event, ok := <-liveSession.Events():
			if !ok {
				return strings.TrimSpace(feedback.String()), nil
			}

			switch event.Type {
			case agent.EventTypeSession:
				if peerSessionKey != "" && strings.TrimSpace(event.SessionID) != "" {
					r.sessionMgr.UpdateSession(sessionID, peerSessionKey, strings.TrimSpace(event.SessionID))
				}
			case agent.EventTypeApproval:
				latest, exists := r.sessionMgr.Get(sessionID)
				if !exists || latest == nil || !isSuperAutoApproveEnabled(latest) {
					return "", errors.New("super auto approval disabled")
				}
				if err := liveSession.RespondApproval(agent.ApprovalActionAllow); err != nil {
					return "", err
				}
				r.sendSuperNotice(userID, superAutoApprovalNotice)
			case agent.EventTypeDelta, agent.EventTypeMessage:
				if strings.TrimSpace(event.Content) != "" {
					feedback.WriteString(event.Content)
				}
			case agent.EventTypeError:
				if strings.TrimSpace(event.Error) == "" {
					return "", errors.New("peer review failed")
				}
				return "", errors.New(event.Error)
			case agent.EventTypeDone:
				return strings.TrimSpace(feedback.String()), nil
			}
		}
	}
}

func (r *Router) runSuperPeerReviewStream(
	ctx context.Context,
	sessionID string,
	peerAgentType string,
	peerAgent agent.Agent,
	input string,
) (string, error) {
	sess, ok := r.sessionMgr.Get(sessionID)
	if !ok || sess == nil {
		return "", errors.New("session not found")
	}

	peerSessionID := ""
	peerSessionKey := agentSessionContextKey(peerAgentType)
	if peerSessionKey != "" {
		peerSessionID = sessionContextString(sess, peerSessionKey)
	}

	stream, err := peerAgent.ExecuteStream(ctx, peerSessionID, input)
	if err != nil {
		return "", err
	}

	var (
		feedback strings.Builder
		errParts []string
	)
	for event := range stream {
		switch event.Type {
		case agent.EventTypeSession:
			if peerSessionKey != "" && strings.TrimSpace(event.SessionID) != "" {
				r.sessionMgr.UpdateSession(sessionID, peerSessionKey, strings.TrimSpace(event.SessionID))
			}
		case agent.EventTypeApproval:
			return "", errors.New(superPeerReviewUnsupportedApprove)
		case agent.EventTypeDelta, agent.EventTypeMessage:
			if strings.TrimSpace(event.Content) != "" {
				feedback.WriteString(event.Content)
			}
		case agent.EventTypeError:
			if strings.TrimSpace(event.Error) != "" {
				errParts = append(errParts, strings.TrimSpace(event.Error))
			}
		}
	}

	if len(errParts) > 0 {
		return "", errors.New(strings.Join(errParts, "\n"))
	}

	return strings.TrimSpace(feedback.String()), nil
}

func (r *Router) sendSuperNotice(userID, content string) {
	if strings.TrimSpace(userID) == "" || strings.TrimSpace(content) == "" {
		return
	}
	if r.platform == nil {
		return
	}

	noticeCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = r.sendReply(noticeCtx, userID, content)
}

func buildSuperInjectedInput(userInput, feedback string) string {
	userInput = strings.TrimSpace(userInput)
	feedback = strings.TrimSpace(feedback)
	if feedback == "" {
		return userInput
	}

	return fmt.Sprintf(
		"%s\n\n[Super 对侧已审批结论，请据此优化当前回答]\n%s",
		userInput,
		feedback,
	)
}

func buildSuperPeerReviewPrompt(currentAgentType, userInput, mainOutput string) string {
	currentAgentType = strings.TrimSpace(currentAgentType)
	userInput = strings.TrimSpace(userInput)
	mainOutput = strings.TrimSpace(mainOutput)
	if mainOutput == "" {
		mainOutput = "(empty)"
	}

	return fmt.Sprintf(
		"你是 %s 的对侧复盘代理。请基于以下信息给出“下一轮可直接注入给主代理”的优化结论。\n"+
			"要求：\n"+
			"1. 只输出结论，不要寒暄。\n"+
			"2. 优先指出可执行的修正点和遗漏点。\n"+
			"3. 如主回复已足够好，明确写“维持当前方案”并给最多 2 条微调建议。\n\n"+
			"[用户原始消息]\n%s\n\n"+
			"[主代理本轮输出]\n%s",
		currentAgentType,
		userInput,
		mainOutput,
	)
}

func oppositeAgentType(currentAgentType string) string {
	switch strings.ToLower(strings.TrimSpace(currentAgentType)) {
	case "claude":
		return "codex"
	case "codex":
		return "claude"
	default:
		return ""
	}
}

func (r *Router) resolveAgentExecution(msg *Message) (*session.Session, string, string, agent.Agent, error) {
	var currentSession *session.Session
	var agentType string

	defaultAgent := r.agentMgr.GetDefaultAgent()
	if defaultAgent == nil {
		return nil, "", "", nil, errors.New("No default agent configured")
	}
	agentType = mapAgentName(defaultAgent.Name())

	if strings.TrimSpace(msg.SessionID) != "" {
		currentSession = r.sessionMgr.GetOrCreateSession(msg.SessionID, msg.UserID, agentType)
	} else if active, ok := r.sessionMgr.GetActiveSession(msg.UserID); ok {
		currentSession = active
	} else {
		currentSession = r.sessionMgr.GetOrCreateSession(pendingNativeSessionID(msg.UserID), msg.UserID, agentType)
	}
	if currentSession == nil {
		return nil, "", "", nil, errors.New("Failed to create or get session")
	}
	if strings.TrimSpace(currentSession.AgentType) == "" {
		currentSession.AgentType = agentType
	}

	if r.sessionMgr != nil {
		r.sessionMgr.SetSessionTitleIfEmpty(currentSession.ID, msg.Content)
	} else {
		currentSession.SetTitleIfEmpty(msg.Content)
	}

	currentAgent := r.agentMgr.ResolveAgent(currentSession.AgentType)
	if currentAgent == nil {
		return nil, "", "", nil, fmt.Errorf("No agent available for session type: %s", currentSession.AgentType)
	}

	sessionKey := agentSessionContextKey(currentSession.AgentType)
	agentSessionID := ""
	if sessionKey != "" {
		if sid, ok := currentSession.ContextString(sessionKey); ok {
			agentSessionID = sid
		}
	}

	return currentSession, sessionKey, agentSessionID, currentAgent, nil
}

// mapAgentName 将 Agent 名称映射到会话类型
func mapAgentName(agentName string) string {
	switch agentName {
	case "claude-code":
		return "claude"
	case "codex":
		return "codex"
	case "hermes":
		return "hermes"
	case "gemini":
		return "gemini"
	default:
		return agentName
	}
}

func sessionWorkDir(sess *session.Session) string {
	if sess == nil {
		return ""
	}
	return sessionContextString(sess, "work_dir")
}

func agentSessionContextKey(agentType string) string {
	switch strings.ToLower(strings.TrimSpace(agentType)) {
	case "claude":
		return "claude_session_id"
	case "codex":
		return "codex_session_id"
	case "hermes":
		return "hermes_session_id"
	case "gemini":
		return "gemini_session_id"
	default:
		return ""
	}
}

// extractSessionID 从响应中提取 session ID
func extractSessionID(response string) string {
	prefix := "\n\n__SESSION_ID__: "
	idx := strings.LastIndex(response, prefix)
	if idx == -1 {
		return ""
	}
	return strings.TrimSpace(response[idx+len(prefix):])
}

// removeSessionIDMarker 从响应中移除 session ID 标记
func removeSessionIDMarker(response string) string {
	prefix := "\n\n__SESSION_ID__: "
	idx := strings.LastIndex(response, prefix)
	if idx == -1 {
		return response
	}
	return response[:idx]
}
