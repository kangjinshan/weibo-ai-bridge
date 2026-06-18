package router

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/kangjinshan/weibo-ai-bridge/agent"
	"github.com/kangjinshan/weibo-ai-bridge/platform/weibo"
)

const bufferedDeltaFlushPollInterval = 100 * time.Millisecond

// StreamMessage 处理消息并返回结构化事件流。
func (r *Router) StreamMessage(ctx context.Context, msg *weibo.Message) (<-chan agent.Event, error) {
	if msg == nil {
		return nil, errors.New("message cannot be nil")
	}

	return r.Stream(ctx, r.toRouterMessage(msg))
}

// Stream 处理路由层消息并返回结构化事件流。
func (r *Router) Stream(ctx context.Context, msg *Message) (<-chan agent.Event, error) {
	if msg == nil {
		return nil, errors.New("message cannot be nil")
	}

	return r.streamRouterMessage(ctx, msg)
}

// HandleMessage 处理消息（主入口）
func (r *Router) HandleMessage(ctx context.Context, msg *weibo.Message) error {
	if msg == nil {
		return errors.New("message cannot be nil")
	}

	runCtx, cancel := r.lifecycleContext(ctx)
	defer cancel()

	routerMsg := r.toRouterMessage(msg)
	content := strings.TrimSpace(routerMsg.Content)
	if strings.HasPrefix(content, "/") && !isSpecialRouterCommand(content) && r.commandHandler != nil {
		return r.handleSlashCommandDirect(runCtx, routerMsg)
	}

	stream, err := r.streamRouterMessage(runCtx, routerMsg)
	if err != nil {
		return err
	}

	if !strings.HasPrefix(content, "/") && r.simpleModeForMessage(routerMsg) {
		return r.forwardSimpleStreamToPlatform(runCtx, msg.UserID, stream)
	}

	return r.forwardStreamToPlatform(runCtx, msg.UserID, stream)
}

func (r *Router) handleSlashCommandDirect(ctx context.Context, msg *Message) error {
	resp, err := r.commandHandler.Handle(msg)
	if err != nil {
		return err
	}
	if resp == nil {
		return errors.New("response is nil")
	}

	if strings.TrimSpace(resp.Content) != "" {
		if sendErr := r.sendReply(ctx, msg.UserID, resp.Content); sendErr != nil {
			return sendErr
		}
	}

	if resp.Success && isDirSetCommand(msg.Content) {
		sessionID := strings.TrimSpace(msg.SessionID)
		if sessionID == "" && r.sessionMgr != nil {
			sessionID = strings.TrimSpace(r.sessionMgr.GetActiveSessionID(msg.UserID))
		}
		if sessionID != "" {
			r.removeInteractiveSession(sessionID)
		}
	}

	if !resp.Success && resp.Error != nil {
		return resp.Error
	}

	return nil
}

func (r *Router) streamRouterMessage(ctx context.Context, msg *Message) (<-chan agent.Event, error) {
	if msg == nil {
		return nil, errors.New("message cannot be nil")
	}

	runCtx, cancel := r.lifecycleContext(ctx)
	events := make(chan agent.Event, 32)

	go func() {
		defer cancel()
		defer close(events)

		content := strings.TrimSpace(msg.Content)
		if strings.HasPrefix(content, "/") && r.commandHandler != nil {
			if handled, err := r.emitSpecialCommandEvents(runCtx, events, msg); handled {
				if err != nil && !IsBenignCancellation(err) {
					emitRouterEvent(runCtx, events, agent.Event{Type: agent.EventTypeError, Error: err.Error()})
				}
				emitRouterEvent(runCtx, events, agent.Event{Type: agent.EventTypeDone})
				return
			}
			r.emitCommandEvents(runCtx, events, msg)
			return
		}

		if err := r.streamAIMessage(runCtx, msg, events); err != nil && !IsBenignCancellation(err) {
			emitRouterEvent(runCtx, events, agent.Event{Type: agent.EventTypeError, Error: err.Error()})
		}

		emitRouterEvent(runCtx, events, agent.Event{Type: agent.EventTypeDone})
	}()

	return events, nil
}

func emitRouterEvent(ctx context.Context, events chan<- agent.Event, event agent.Event) bool {
	if event.Type == "" {
		return true
	}
	if ctx == nil {
		ctx = context.Background()
	}

	select {
	case events <- event:
		return true
	case <-ctx.Done():
		return false
	}
}

func (r *Router) forwardStreamToPlatform(ctx context.Context, userID string, stream <-chan agent.Event) error {
	if r.platform == nil {
		return errors.New("platform is not set")
	}

	writer, err := r.openStreamWriter(ctx, userID)
	if err != nil {
		return err
	}
	sender := newStreamReplySender(writer)
	ticker := time.NewTicker(bufferedDeltaFlushPollInterval)
	defer ticker.Stop()

	var streamErr error

	for {
		select {
		case <-ctx.Done():
			if err := sender.Settle(context.WithoutCancel(ctx)); err != nil {
				return err
			}
			return ctx.Err()
		case event, ok := <-stream:
			if !ok {
				if err := sender.Settle(ctx); err != nil {
					return err
				}
				return streamErr
			}

			switch event.Type {
			case agent.EventTypeDelta:
				if err := sender.PushDelta(ctx, event.Content); err != nil {
					return err
				}
			case agent.EventTypeApproval:
				if strings.TrimSpace(event.Content) != "" {
					if err := sender.PushInformationalText(ctx, event.Content); err != nil {
						return err
					}
				}
			case agent.EventTypeMessage:
				if err := sender.PushDeliverText(ctx, event.Content, true); err != nil {
					return err
				}
			case agent.EventTypeError:
				if strings.TrimSpace(event.Error) != "" && !IsBenignCancellation(errors.New(event.Error)) {
					if err := sender.PushDeliverText(ctx, "AI execution failed: "+event.Error, true); err != nil {
						return err
					}
					streamErr = errors.New(event.Error)
				}
			case agent.EventTypeDone:
				if err := sender.Settle(ctx); err != nil {
					return err
				}
			}
		case <-ticker.C:
			if err := sender.FlushPendingIfDelayed(ctx); err != nil {
				return err
			}
		}
	}
}

func (r *Router) forwardSimpleStreamToPlatform(ctx context.Context, userID string, stream <-chan agent.Event) error {
	var deltaText strings.Builder
	var finalMessage string
	var streamErr error
	var sender *streamReplySender

	ensureSender := func() (*streamReplySender, error) {
		if sender != nil {
			return sender, nil
		}
		if r.platform == nil {
			return nil, errors.New("platform is not set")
		}
		writer, err := r.openStreamWriter(ctx, userID)
		if err != nil {
			return nil, err
		}
		sender = newStreamReplySender(writer)
		return sender, nil
	}

	sendInformational := func(text string) error {
		text = strings.TrimSpace(text)
		if text == "" {
			return nil
		}
		s, err := ensureSender()
		if err != nil {
			return err
		}
		return s.PushInformationalText(ctx, text)
	}

	sendFinal := func(text string) error {
		text = strings.TrimSpace(text)
		if text == "" {
			return nil
		}
		s, err := ensureSender()
		if err != nil {
			return err
		}
		return s.PushDeliverText(ctx, text, true)
	}

	settle := func() error {
		if sender == nil {
			return nil
		}
		return sender.Settle(ctx)
	}
	finalText := func() string {
		if streamErr != nil {
			return "AI execution failed: " + streamErr.Error()
		}
		if strings.TrimSpace(finalMessage) != "" {
			return finalMessage
		}
		return deltaText.String()
	}

	for {
		select {
		case <-ctx.Done():
			if sender != nil {
				if err := sender.Settle(context.WithoutCancel(ctx)); err != nil {
					return err
				}
			}
			return ctx.Err()
		case event, ok := <-stream:
			if !ok {
				if err := sendFinal(finalText()); err != nil {
					return err
				}
				if err := settle(); err != nil {
					return err
				}
				return streamErr
			}

			switch event.Type {
			case agent.EventTypeDelta:
				deltaText.WriteString(event.Content)
			case agent.EventTypeApproval:
				if err := sendInformational(event.Content); err != nil {
					return err
				}
			case agent.EventTypeMessage:
				if strings.TrimSpace(event.Content) != "" && strings.TrimSpace(finalMessage) == "" {
					finalMessage = event.Content
				}
			case agent.EventTypeError:
				if strings.TrimSpace(event.Error) != "" && !IsBenignCancellation(errors.New(event.Error)) {
					streamErr = errors.New(event.Error)
				}
			case agent.EventTypeDone:
				if err := sendFinal(finalText()); err != nil {
					return err
				}
				if err := settle(); err != nil {
					return err
				}
			}
		}
	}
}

func IsBenignCancellation(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	text := strings.TrimSpace(err.Error())
	return text == context.Canceled.Error() || text == context.DeadlineExceeded.Error()
}

func (r *Router) openStreamWriter(ctx context.Context, userID string) (streamReplyWriter, error) {
	return r.platform.OpenReplyStream(ctx, userID)
}

// sendReply 发送回复（支持分块）
func (r *Router) sendReply(ctx context.Context, userID string, content string) error {
	if r.platform == nil {
		return errors.New("platform is not set")
	}

	return r.platform.Reply(ctx, userID, content)
}

func (r *Router) emitCommandEvents(ctx context.Context, events chan<- agent.Event, msg *Message) {
	resp, err := r.commandHandler.Handle(msg)
	if err != nil {
		emitRouterEvent(ctx, events, agent.Event{Type: agent.EventTypeError, Error: err.Error()})
		emitRouterEvent(ctx, events, agent.Event{Type: agent.EventTypeDone})
		return
	}

	if resp == nil {
		emitRouterEvent(ctx, events, agent.Event{Type: agent.EventTypeError, Error: "response is nil"})
		emitRouterEvent(ctx, events, agent.Event{Type: agent.EventTypeDone})
		return
	}

	if resp.Content != "" {
		emitRouterEvent(ctx, events, agent.Event{Type: agent.EventTypeMessage, Content: resp.Content})
	}
	if resp.Success && isDirSetCommand(msg.Content) {
		sessionID := strings.TrimSpace(msg.SessionID)
		if sessionID == "" && r.sessionMgr != nil {
			sessionID = strings.TrimSpace(r.sessionMgr.GetActiveSessionID(msg.UserID))
		}
		if sessionID != "" {
			r.removeInteractiveSession(sessionID)
		}
	}
	if !resp.Success && resp.Error != nil {
		emitRouterEvent(ctx, events, agent.Event{Type: agent.EventTypeError, Error: resp.Error.Error()})
	}
	emitRouterEvent(ctx, events, agent.Event{Type: agent.EventTypeDone})
}

func isDirSetCommand(content string) bool {
	fields := strings.Fields(strings.TrimSpace(content))
	return len(fields) > 1 && strings.EqualFold(fields[0], "/dir")
}
