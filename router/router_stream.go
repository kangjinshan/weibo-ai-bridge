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

	stream, err := r.StreamMessage(ctx, msg)
	if err != nil {
		return err
	}

	return r.forwardStreamToPlatform(ctx, msg.UserID, stream)
}

func (r *Router) streamRouterMessage(ctx context.Context, msg *Message) (<-chan agent.Event, error) {
	if msg == nil {
		return nil, errors.New("message cannot be nil")
	}

	events := make(chan agent.Event, 32)

	go func() {
		defer close(events)

		content := strings.TrimSpace(msg.Content)
		if strings.HasPrefix(content, "/") && r.commandHandler != nil {
			if handled, err := r.emitSpecialCommandEvents(ctx, events, msg); handled {
				if err != nil && !IsBenignCancellation(err) {
					events <- agent.Event{Type: agent.EventTypeError, Error: err.Error()}
				}
				events <- agent.Event{Type: agent.EventTypeDone}
				return
			}
			r.emitCommandEvents(events, msg)
			return
		}

		if err := r.streamAIMessage(ctx, msg, events); err != nil && !IsBenignCancellation(err) {
			events <- agent.Event{Type: agent.EventTypeError, Error: err.Error()}
		}

		events <- agent.Event{Type: agent.EventTypeDone}
	}()

	return events, nil
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

func IsBenignCancellation(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	text := strings.TrimSpace(err.Error())
	return text == context.Canceled.Error() || text == context.DeadlineExceeded.Error()
}

func (r *Router) openStreamWriter(ctx context.Context, userID string) (streamReplyWriter, error) {
	if streamer, ok := r.platform.(streamingPlatformInterface); ok {
		return streamer.OpenReplyStream(ctx, userID)
	}

	return &legacyStreamReplyWriter{
		send: func(content string) error {
			return r.sendReply(ctx, userID, content)
		},
	}, nil
}

// sendReply 发送回复（支持分块）
func (r *Router) sendReply(ctx context.Context, userID string, content string) error {
	if r.platform == nil {
		return errors.New("platform is not set")
	}

	chunks := r.splitMessage(content, 1000)

	for _, chunk := range chunks {
		if err := r.platform.Reply(ctx, userID, chunk); err != nil {
			return errors.New("send reply chunk failed")
		}
	}

	return nil
}

func (r *Router) emitCommandEvents(events chan<- agent.Event, msg *Message) {
	resp, err := r.commandHandler.Handle(msg)
	if err != nil {
		events <- agent.Event{Type: agent.EventTypeError, Error: err.Error()}
		events <- agent.Event{Type: agent.EventTypeDone}
		return
	}

	if resp == nil {
		events <- agent.Event{Type: agent.EventTypeError, Error: "response is nil"}
		events <- agent.Event{Type: agent.EventTypeDone}
		return
	}

	if resp.Content != "" {
		events <- agent.Event{Type: agent.EventTypeMessage, Content: resp.Content}
	}
	if !resp.Success && resp.Error != nil {
		events <- agent.Event{Type: agent.EventTypeError, Error: resp.Error.Error()}
	}
	events <- agent.Event{Type: agent.EventTypeDone}
}
