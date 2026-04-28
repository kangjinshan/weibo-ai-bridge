package router

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/kangjinshan/weibo-ai-bridge/agent"
	"github.com/kangjinshan/weibo-ai-bridge/session"
)

type interactiveSessionState struct {
	agentType        string
	session          agent.InteractiveSession
	awaitingApproval bool
	allowAll         bool
}

const interactiveDoneGracePeriod = 200 * time.Millisecond

func (r *Router) getInteractiveSession(sessionID string) (*interactiveSessionState, bool) {
	r.liveMu.Lock()
	defer r.liveMu.Unlock()

	state, ok := r.liveSessions[sessionID]
	return state, ok
}

func (r *Router) removeInteractiveSession(sessionID string) {
	r.liveMu.Lock()
	defer r.liveMu.Unlock()

	state, ok := r.liveSessions[sessionID]
	if !ok {
		return
	}

	_ = state.session.Close()
	delete(r.liveSessions, sessionID)
}

func (r *Router) getOrCreateInteractiveSession(ctx context.Context, sess *session.Session, sessionKey, agentSessionID string, interactiveAgent agent.InteractiveAgent) (*interactiveSessionState, bool, error) {
	r.liveMu.Lock()
	defer r.liveMu.Unlock()

	if existing, ok := r.liveSessions[sess.ID]; ok {
		if existing.agentType == sess.AgentType {
			return existing, false, nil
		}
		_ = existing.session.Close()
		delete(r.liveSessions, sess.ID)
	}

	liveSession, err := interactiveAgent.StartSession(context.WithoutCancel(ctx), agentSessionID)
	if err != nil {
		return nil, false, err
	}

	state := &interactiveSessionState{
		agentType: sess.AgentType,
		session:   liveSession,
	}
	r.liveSessions[sess.ID] = state

	if sessionKey != "" {
		if sid := strings.TrimSpace(liveSession.CurrentSessionID()); sid != "" {
			if sess.Context == nil {
				sess.Context = make(map[string]interface{})
			}
			sess.Context[sessionKey] = sid
			r.sessionMgr.UpdateSession(sess.ID, sessionKey, sid)
		}
	}

	return state, true, nil
}

func (r *Router) streamInteractiveAIMessage(ctx context.Context, msg *Message, sess *session.Session, sessionKey, agentSessionID string, interactiveAgent agent.InteractiveAgent, events chan<- agent.Event) error {
	liveState, created, err := r.getOrCreateInteractiveSession(ctx, sess, sessionKey, agentSessionID, interactiveAgent)
	if err != nil {
		return err
	}

	if liveState.awaitingApproval {
		action, ok := parseApprovalAction(msg.Content)
		if !ok {
			events <- agent.Event{
				Type:    agent.EventTypeApproval,
				Content: approvalHintMessage(),
			}
			return nil
		}

		allowAllRequested := action == agent.ApprovalActionAllowAll
		if allowAllRequested {
			liveState.allowAll = true
		}
		liveState.awaitingApproval = false

		liveState, err = r.respondInteractiveApproval(ctx, sess, sessionKey, agentSessionID, action, interactiveAgent, liveState)
		if err != nil {
			return err
		}

		if allowAllRequested {
			events <- agent.Event{
				Type:    agent.EventTypeApproval,
				Content: "授权成功，这对话内将不再需要再次授权。",
			}
		}

		return r.drainInteractiveSession(ctx, sess, sessionKey, liveState, events)
	}

	if !created {
		r.waitInteractiveEventsQuiesced(sess, sessionKey, liveState, interactiveDoneGracePeriod)
	}
	r.discardBufferedInteractiveEvents(sess, sessionKey, liveState)

	liveState, err = r.sendInteractiveInput(ctx, sess, sessionKey, agentSessionID, msg.Content, interactiveAgent, liveState)
	if err != nil {
		return err
	}

	return r.drainInteractiveSession(ctx, sess, sessionKey, liveState, events)
}

func (r *Router) respondInteractiveApproval(ctx context.Context, sess *session.Session, sessionKey, agentSessionID string, action agent.ApprovalAction, interactiveAgent agent.InteractiveAgent, liveState *interactiveSessionState) (*interactiveSessionState, error) {
	if err := liveState.session.RespondApproval(action); err != nil {
		if !isSessionNotRunningError(err) {
			return nil, err
		}

		r.removeInteractiveSession(sess.ID)
		restartedState, _, restartErr := r.getOrCreateInteractiveSession(ctx, sess, sessionKey, agentSessionID, interactiveAgent)
		if restartErr != nil {
			return nil, restartErr
		}
		restartedState.allowAll = liveState.allowAll
		restartedState.awaitingApproval = false
		if err := restartedState.session.RespondApproval(action); err != nil {
			r.removeInteractiveSession(sess.ID)
			return nil, err
		}
		return restartedState, nil
	}

	return liveState, nil
}

func (r *Router) sendInteractiveInput(ctx context.Context, sess *session.Session, sessionKey, agentSessionID, input string, interactiveAgent agent.InteractiveAgent, liveState *interactiveSessionState) (*interactiveSessionState, error) {
	if err := liveState.session.Send(input); err != nil {
		r.removeInteractiveSession(sess.ID)
		if !isSessionNotRunningError(err) {
			return nil, err
		}

		restartedState, _, restartErr := r.getOrCreateInteractiveSession(ctx, sess, sessionKey, agentSessionID, interactiveAgent)
		if restartErr != nil {
			return nil, restartErr
		}
		if err := restartedState.session.Send(input); err != nil {
			r.removeInteractiveSession(sess.ID)
			return nil, err
		}
		return restartedState, nil
	}

	return liveState, nil
}

func (r *Router) discardBufferedInteractiveEvents(sess *session.Session, sessionKey string, liveState *interactiveSessionState) {
	for {
		select {
		case event, ok := <-liveState.session.Events():
			if !ok {
				r.removeInteractiveSession(sess.ID)
				return
			}

			if event.Type == agent.EventTypeSession && sessionKey != "" && strings.TrimSpace(event.SessionID) != "" {
				if sess.Context == nil {
					sess.Context = make(map[string]interface{})
				}
				sess.Context[sessionKey] = event.SessionID
				r.sessionMgr.UpdateSession(sess.ID, sessionKey, event.SessionID)
			}
		default:
			return
		}
	}
}

func (r *Router) waitInteractiveEventsQuiesced(sess *session.Session, sessionKey string, liveState *interactiveSessionState, quietPeriod time.Duration) {
	timer := time.NewTimer(quietPeriod)
	defer timer.Stop()

	for {
		select {
		case <-timer.C:
			return
		case event, ok := <-liveState.session.Events():
			if !ok {
				r.removeInteractiveSession(sess.ID)
				return
			}

			if event.Type == agent.EventTypeSession && sessionKey != "" && strings.TrimSpace(event.SessionID) != "" {
				if sess.Context == nil {
					sess.Context = make(map[string]interface{})
				}
				sess.Context[sessionKey] = event.SessionID
				r.sessionMgr.UpdateSession(sess.ID, sessionKey, event.SessionID)
			}

			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(quietPeriod)
		}
	}
}

func (r *Router) drainInteractiveSession(ctx context.Context, sess *session.Session, sessionKey string, liveState *interactiveSessionState, events chan<- agent.Event) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-liveState.session.Events():
			if !ok {
				r.removeInteractiveSession(sess.ID)
				return errors.New("agent session closed unexpectedly")
			}

			if event.Type == agent.EventTypeSession && sessionKey != "" && strings.TrimSpace(event.SessionID) != "" {
				if sess.Context == nil {
					sess.Context = make(map[string]interface{})
				}
				sess.Context[sessionKey] = event.SessionID
				r.sessionMgr.UpdateSession(sess.ID, sessionKey, event.SessionID)
			}

			switch event.Type {
			case agent.EventTypeApproval:
				if liveState.allowAll {
					if err := liveState.session.RespondApproval(agent.ApprovalActionAllow); err != nil {
						r.removeInteractiveSession(sess.ID)
						return err
					}
					continue
				}

				liveState.awaitingApproval = true
				events <- agent.Event{
					Type:    agent.EventTypeApproval,
					Content: formatApprovalPrompt(event.ToolName, event.ToolInput),
				}
				return nil
			case agent.EventTypeDone:
				return r.drainInteractiveTailAfterDone(ctx, sess, sessionKey, liveState, events)
			case agent.EventTypeError:
				events <- event
				r.removeInteractiveSession(sess.ID)
				return nil
			default:
				events <- event
			}
		}
	}
}

func (r *Router) drainInteractiveTailAfterDone(ctx context.Context, sess *session.Session, sessionKey string, liveState *interactiveSessionState, events chan<- agent.Event) error {
	timer := time.NewTimer(interactiveDoneGracePeriod)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		case event, ok := <-liveState.session.Events():
			if !ok {
				r.removeInteractiveSession(sess.ID)
				return nil
			}

			if event.Type == agent.EventTypeSession && sessionKey != "" && strings.TrimSpace(event.SessionID) != "" {
				if sess.Context == nil {
					sess.Context = make(map[string]interface{})
				}
				sess.Context[sessionKey] = event.SessionID
				r.sessionMgr.UpdateSession(sess.ID, sessionKey, event.SessionID)
			}

			switch event.Type {
			case agent.EventTypeDone:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(interactiveDoneGracePeriod)
			case agent.EventTypeError:
				events <- event
				r.removeInteractiveSession(sess.ID)
				return nil
			default:
				events <- event
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(interactiveDoneGracePeriod)
			}
		}
	}
}

func isSessionNotRunningError(err error) bool {
	if err == nil {
		return false
	}

	return strings.Contains(strings.ToLower(strings.TrimSpace(err.Error())), "session is not running")
}
