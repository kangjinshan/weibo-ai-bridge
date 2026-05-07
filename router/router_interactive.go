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
const interactiveLeadingDoneWait = 12 * time.Second

var errInteractiveTurnNoSignal = errors.New("interactive turn completed without any turn signal")

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
	if existing, ok := r.liveSessions[sess.ID]; ok {
		if existing.agentType == sess.AgentType {
			r.liveMu.Unlock()
			return existing, false, nil
		}
		_ = existing.session.Close()
		delete(r.liveSessions, sess.ID)
	}
	r.liveMu.Unlock()

	// StartSession 是耗时操作（启动子进程/网络），在锁外执行避免阻塞其他会话。
	liveSession, err := interactiveAgent.StartSession(context.WithoutCancel(ctx), agentSessionID)
	if err != nil {
		return nil, false, err
	}

	state := &interactiveSessionState{
		agentType: sess.AgentType,
		session:   liveSession,
	}

	r.liveMu.Lock()
	if existing, ok := r.liveSessions[sess.ID]; ok {
		// 并发场景下另一个 goroutine 已创建了会话，放弃刚启动的副本。
		r.liveMu.Unlock()
		_ = liveSession.Close()
		return existing, false, nil
	}
	r.liveSessions[sess.ID] = state
	r.liveMu.Unlock()

	if sessionKey != "" {
		if sid := strings.TrimSpace(liveSession.CurrentSessionID()); sid != "" {
			sess = r.bindAgentNativeSessionID(sess, sessionKey, sid)
		}
	}

	return state, true, nil
}

func (r *Router) streamInteractiveAIMessage(ctx context.Context, msg *Message, sess *session.Session, sessionKey, agentSessionID string, interactiveAgent agent.InteractiveAgent, emit func(agent.Event)) error {
	liveState, created, err := r.getOrCreateInteractiveSession(ctx, sess, sessionKey, agentSessionID, interactiveAgent)
	if err != nil {
		return err
	}
	if isSuperAutoApproveEnabled(sess) {
		liveState.allowAll = true
	}

	if liveState.awaitingApproval {
		if isSuperAutoApproveEnabled(sess) {
			liveState.awaitingApproval = false
			liveState, err = r.respondInteractiveApproval(ctx, sess, sessionKey, agentSessionID, agent.ApprovalActionAllow, interactiveAgent, liveState)
			if err != nil {
				return err
			}
			// 先收尾之前被审批阻塞的 turn。
			if err := r.drainInteractiveSession(ctx, sess, sessionKey, liveState, emit); err != nil && !errors.Is(err, errInteractiveTurnNoSignal) {
				return err
			}
			// 兼容“先 pending 审批，再 /super on，再发新消息”的场景：
			// 自动审批完成后继续处理这条新输入，避免消息被吞掉。
			if strings.TrimSpace(msg.Content) == "" {
				return nil
			}
			if _, isApprovalReply := parseApprovalAction(msg.Content); isApprovalReply {
				return nil
			}

			// 自动审批收尾后，交互 channel 里可能仍有旧 turn 的延迟 done 尾事件。
			// 若直接发送新输入，旧 done 可能被误当作新 turn 结束信号，造成“无回复”。
			r.waitInteractiveEventsQuiesced(sess, sessionKey, liveState, interactiveDoneGracePeriod)
			r.discardBufferedInteractiveEvents(sess, sessionKey, liveState)

			return r.sendAndDrainInteractiveTurn(ctx, sess, sessionKey, agentSessionID, msg.Content, interactiveAgent, liveState, emit)
		}

		action, ok := parseApprovalAction(msg.Content)
		if !ok {
			emit(agent.Event{
				Type:    agent.EventTypeApproval,
				Content: approvalHintMessage(),
			})
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
			emit(agent.Event{
				Type:    agent.EventTypeApproval,
				Content: "授权成功，这对话内将不再需要再次授权。",
			})
		}

		if err := r.drainInteractiveSession(ctx, sess, sessionKey, liveState, emit); err != nil && !errors.Is(err, errInteractiveTurnNoSignal) {
			return err
		}
		return nil
	}

	if !created {
		r.waitInteractiveEventsQuiesced(sess, sessionKey, liveState, interactiveDoneGracePeriod)
	}
	r.discardBufferedInteractiveEvents(sess, sessionKey, liveState)

	return r.sendAndDrainInteractiveTurn(ctx, sess, sessionKey, agentSessionID, msg.Content, interactiveAgent, liveState, emit)
}

func (r *Router) sendAndDrainInteractiveTurn(
	ctx context.Context,
	sess *session.Session,
	sessionKey string,
	agentSessionID string,
	input string,
	interactiveAgent agent.InteractiveAgent,
	liveState *interactiveSessionState,
	emit func(agent.Event),
) error {
	state, err := r.sendInteractiveInput(ctx, sess, sessionKey, agentSessionID, input, interactiveAgent, liveState)
	if err != nil {
		return err
	}

	err = r.drainInteractiveSession(ctx, sess, sessionKey, state, emit)
	if !errors.Is(err, errInteractiveTurnNoSignal) {
		return err
	}

	// 兼容 stale 会话：首个事件是旧 done 且后续无信号，自动重建会话并重试一次。
	r.removeInteractiveSession(sess.ID)

	restartedState, _, restartErr := r.getOrCreateInteractiveSession(ctx, sess, sessionKey, agentSessionID, interactiveAgent)
	if restartErr != nil {
		return restartErr
	}
	restartedState.allowAll = state.allowAll
	restartedState.awaitingApproval = false
	r.discardBufferedInteractiveEvents(sess, sessionKey, restartedState)

	restartedState, restartErr = r.sendInteractiveInput(ctx, sess, sessionKey, agentSessionID, input, interactiveAgent, restartedState)
	if restartErr != nil {
		return restartErr
	}

	return r.drainInteractiveSession(ctx, sess, sessionKey, restartedState, emit)
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
				sess = r.bindAgentNativeSessionID(sess, sessionKey, event.SessionID)
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
				sess = r.bindAgentNativeSessionID(sess, sessionKey, event.SessionID)
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

func (r *Router) drainInteractiveSession(ctx context.Context, sess *session.Session, sessionKey string, liveState *interactiveSessionState, emit func(agent.Event)) error {
	sawTurnSignal := false

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
				sess = r.bindAgentNativeSessionID(sess, sessionKey, event.SessionID)
			}

			switch event.Type {
			case agent.EventTypeApproval:
				sawTurnSignal = true
				if isSuperAutoApproveEnabled(sess) {
					if err := liveState.session.RespondApproval(agent.ApprovalActionAllow); err != nil {
						r.removeInteractiveSession(sess.ID)
						return err
					}
					continue
				}
				if liveState.allowAll {
					if err := liveState.session.RespondApproval(agent.ApprovalActionAllow); err != nil {
						r.removeInteractiveSession(sess.ID)
						return err
					}
					continue
				}

				liveState.awaitingApproval = true
				emit(agent.Event{
					Type:    agent.EventTypeApproval,
					Content: formatApprovalPrompt(event.ToolName, event.ToolInput),
				})
				return nil
			case agent.EventTypeDone:
				if !sawTurnSignal {
					// 新输入后首个事件是 done 时，优先按”上一轮延迟尾事件”处理，
					// 短时间继续等待非 done 事件，避免把当前 turn 提前结束成空回复。
					next, hasNext, err := r.waitForInteractiveEventAfterLeadingDone(ctx, sess, sessionKey, liveState)
					if err != nil {
						return err
					}
					if !hasNext {
						// 首个 done 后无任何信号，视为 stale 会话导致的空结束。
						return errInteractiveTurnNoSignal
					}

					event = next
					if event.Type == agent.EventTypeSession && sessionKey != "" && strings.TrimSpace(event.SessionID) != "" {
						sess = r.bindAgentNativeSessionID(sess, sessionKey, event.SessionID)
					}

					switch event.Type {
					case agent.EventTypeApproval:
						sawTurnSignal = true
						if isSuperAutoApproveEnabled(sess) {
							if err := liveState.session.RespondApproval(agent.ApprovalActionAllow); err != nil {
								r.removeInteractiveSession(sess.ID)
								return err
							}
							continue
						}
						if liveState.allowAll {
							if err := liveState.session.RespondApproval(agent.ApprovalActionAllow); err != nil {
								r.removeInteractiveSession(sess.ID)
								return err
							}
							continue
						}
						liveState.awaitingApproval = true
						emit(agent.Event{
							Type:    agent.EventTypeApproval,
							Content: formatApprovalPrompt(event.ToolName, event.ToolInput),
						})
						return nil
					case agent.EventTypeDone:
						continue
					case agent.EventTypeError:
						sawTurnSignal = true
						emit(event)
						r.removeInteractiveSession(sess.ID)
						return nil
					default:
						if event.Type != agent.EventTypeSession {
							sawTurnSignal = true
						}
						emit(event)
						continue
					}
				}
				return r.drainInteractiveTailAfterDone(ctx, sess, sessionKey, liveState, emit)
			case agent.EventTypeError:
				sawTurnSignal = true
				emit(event)
				r.removeInteractiveSession(sess.ID)
				return nil
			default:
				if event.Type != agent.EventTypeSession {
					sawTurnSignal = true
				}
				emit(event)
			}
		}
	}
}

func (r *Router) waitForInteractiveEventAfterLeadingDone(
	ctx context.Context,
	sess *session.Session,
	sessionKey string,
	liveState *interactiveSessionState,
) (agent.Event, bool, error) {
	timer := time.NewTimer(interactiveLeadingDoneWait)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return agent.Event{}, false, ctx.Err()
		case <-timer.C:
			return agent.Event{}, false, nil
		case event, ok := <-liveState.session.Events():
			if !ok {
				r.removeInteractiveSession(sess.ID)
				return agent.Event{}, false, nil
			}

			if event.Type == agent.EventTypeSession && sessionKey != "" && strings.TrimSpace(event.SessionID) != "" {
				sess = r.bindAgentNativeSessionID(sess, sessionKey, event.SessionID)
				continue
			}
			if event.Type == agent.EventTypeDone {
				continue
			}
			return event, true, nil
		}
	}
}

func (r *Router) drainInteractiveTailAfterDone(ctx context.Context, sess *session.Session, sessionKey string, liveState *interactiveSessionState, emit func(agent.Event)) error {
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
				sess = r.bindAgentNativeSessionID(sess, sessionKey, event.SessionID)
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
				emit(event)
				r.removeInteractiveSession(sess.ID)
				return nil
			default:
				emit(event)
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

	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(msg, "session is not running") ||
		strings.Contains(msg, "use of closed network connection") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "connection reset by peer") ||
		strings.Contains(msg, "websocket: close sent")
}
