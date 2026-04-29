package router

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/kangjinshan/weibo-ai-bridge/agent"
	"github.com/kangjinshan/weibo-ai-bridge/session"
)

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

	execCtx := agent.WithWorkDir(ctx, sessionWorkDir(sess))
	response, err := currentAgent.Execute(execCtx, agentSessionID, msg.Content)
	if err != nil {
		return &Response{
			Success: false,
			Content: fmt.Sprintf("AI execution failed: %v", err),
		}, nil
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
	execCtx := agent.WithWorkDir(ctx, sessionWorkDir(sess))

	if interactiveAgent, ok := currentAgent.(agent.InteractiveAgent); ok {
		return r.streamInteractiveAIMessage(execCtx, msg, sess, sessionKey, agentSessionID, interactiveAgent, events)
	}

	stream, err := currentAgent.ExecuteStream(execCtx, agentSessionID, msg.Content)
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

		events <- event
	}

	return nil
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
		if sid, ok := currentSession.Context[sessionKey].(string); ok {
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
	default:
		return agentName
	}
}

func sessionWorkDir(sess *session.Session) string {
	if sess == nil {
		return ""
	}
	if workDir, ok := sess.Context["work_dir"].(string); ok {
		return strings.TrimSpace(workDir)
	}
	return ""
}

func agentSessionContextKey(agentType string) string {
	switch strings.ToLower(strings.TrimSpace(agentType)) {
	case "claude":
		return "claude_session_id"
	case "codex":
		return "codex_session_id"
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
