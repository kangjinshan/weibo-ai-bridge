package router

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/kangjinshan/weibo-ai-bridge/agent"
	"github.com/kangjinshan/weibo-ai-bridge/platform/weibo"
	"github.com/kangjinshan/weibo-ai-bridge/session"
)

func isByTheWayCommand(content string) bool {
	parts := strings.Fields(strings.TrimSpace(content))
	return len(parts) > 0 && parts[0] == "/btw"
}

func (r *Router) handleByTheWaySync(ctx context.Context, msg *Message) (*Response, error) {
	stream, err := r.streamRouterMessage(ctx, msg)
	if err != nil {
		return nil, err
	}

	var contentParts []string
	var responseErr error
	success := true

	for event := range stream {
		switch event.Type {
		case agent.EventTypeMessage, agent.EventTypeApproval:
			if text := strings.TrimSpace(event.Content); text != "" {
				contentParts = append(contentParts, text)
			}
		case agent.EventTypeError:
			success = false
			if strings.TrimSpace(event.Error) != "" {
				responseErr = errors.New(event.Error)
				if len(contentParts) == 0 {
					contentParts = append(contentParts, event.Error)
				}
			}
		}
	}

	return &Response{
		Success: success,
		Content: strings.Join(contentParts, "\n"),
		Error:   responseErr,
	}, nil
}

func (r *Router) emitSpecialCommandEvents(ctx context.Context, events chan<- agent.Event, msg *Message) (bool, error) {
	if !isByTheWayCommand(msg.Content) {
		return false, nil
	}

	return true, r.handleByTheWayCommand(ctx, msg, events)
}

func (r *Router) handleByTheWayCommand(ctx context.Context, msg *Message, events chan<- agent.Event) error {
	sess, liveState, content, err := r.resolveByTheWayTarget(msg)
	if err != nil {
		events <- agent.Event{Type: agent.EventTypeMessage, Content: err.Error()}
		return nil
	}

	if err := liveState.session.Send(content); err != nil {
		r.removeInteractiveSession(sess.ID)
		return err
	}

	return r.drainInteractiveSession(ctx, sess, agentSessionContextKey(sess.AgentType), liveState, events)
}

func (r *Router) injectByTheWay(msg *Message) (string, error) {
	_, liveState, content, err := r.resolveByTheWayTarget(msg)
	if err != nil {
		return "", err
	}

	if err := liveState.session.Send(content); err != nil {
		return "", err
	}

	return "已注入补充说明，当前回复会继续输出。", nil
}

// InjectByTheWay 公开方法，注入 /btw 消息
func (r *Router) InjectByTheWay(ctx context.Context, msg *weibo.Message) (bool, error) {
	if msg == nil {
		return false, errors.New("message cannot be nil")
	}

	routerMsg := r.toRouterMessage(msg)
	if !isByTheWayCommand(routerMsg.Content) {
		return false, nil
	}

	_, err := r.injectByTheWay(routerMsg)
	return true, err
}

func (r *Router) resolveByTheWayTarget(msg *Message) (*session.Session, *interactiveSessionState, string, error) {
	if r.sessionMgr == nil {
		return nil, nil, "", errors.New("Session manager is not available.")
	}

	parts := strings.Fields(strings.TrimSpace(msg.Content))
	if len(parts) < 2 {
		return nil, nil, "", errors.New("Please provide content to insert: /btw <message>")
	}

	content := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(msg.Content), parts[0]))
	if content == "" {
		return nil, nil, "", errors.New("Please provide content to insert: /btw <message>")
	}

	sess, ok := r.resolveByTheWaySession(msg)
	if !ok {
		return nil, nil, "", errors.New("No active session found. Use /new to create or activate a session first.")
	}

	liveState, ok := r.getInteractiveSession(sess.ID)
	if !ok {
		return nil, nil, "", errors.New("No live interactive session is running for the current session yet.")
	}

	if liveState.awaitingApproval {
		return nil, nil, "", errors.New("Current session is waiting for approval. Reply with 允许 / 取消 / 允许所有 first.")
	}

	return sess, liveState, content, nil
}

func (r *Router) resolveByTheWaySession(msg *Message) (*session.Session, bool) {
	sessionIDs := []string{}
	if trimmed := strings.TrimSpace(msg.SessionID); trimmed != "" {
		sessionIDs = append(sessionIDs, trimmed)
	}
	if r.sessionMgr != nil {
		if activeSessionID := strings.TrimSpace(r.sessionMgr.GetActiveSessionID(msg.UserID)); activeSessionID != "" {
			sessionIDs = append(sessionIDs, activeSessionID)
		}
	}

	for _, sessionID := range slices.Compact(sessionIDs) {
		sess, exists := r.sessionMgr.Get(sessionID)
		if exists && sess.UserID == msg.UserID {
			return sess, true
		}
	}

	return nil, false
}
