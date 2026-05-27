package local

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/kangjinshan/weibo-ai-bridge/platform/weibo"
)

// Reply sends a single complete assistant message to the conversation
// identified by userID. Reply mirrors the weibo platform interface so the
// existing Router code that uses Platform.Reply keeps working.
//
// Mapping note: in the local upstream the "userID" the bridge passes around
// is actually a conv_id (Router pulls it from incoming UserID we populate in
// handleUserMessage). Reply opens a stream and finishes it in one shot.
func (p *Platform) Reply(ctx context.Context, userID, content string) error {
	stream, err := p.OpenReplyStream(ctx, userID)
	if err != nil {
		return err
	}
	return stream.SendChunk(ctx, content, true)
}

// OpenReplyStream opens a streaming assistant message for the given conv_id.
// Returned ChunkSender satisfies weibo.ChunkSender so router.openStreamWriter
// can route deltas through us without any code change.
func (p *Platform) OpenReplyStream(ctx context.Context, userID string) (weibo.ChunkSender, error) {
	convID := strings.TrimSpace(userID)
	if convID == "" {
		return nil, errors.New("local: conv_id (userID) is required to open reply stream")
	}
	if p.currentConn() == nil {
		return nil, errors.New("local: cannot open reply stream while disconnected")
	}
	return &replyStream{
		platform:    p,
		convID:      convID,
		clientMsgID: newClientMsgID(),
	}, nil
}

// replyStream is the local equivalent of weibo.ReplyStream. It maps a single
// Router-driven turn onto a (start_assistant_message → append_delta…→ finish_message)
// frame triplet.
type replyStream struct {
	platform    *Platform
	convID      string
	clientMsgID string

	mu     sync.Mutex
	msgID  string // assigned by msghub on first chunk
	closed bool
}

// SendChunk forwards an assistant streaming chunk to msghub. The contract is
// the same as weibo.ChunkSender.SendChunk: the final chunk carries done=true
// and may legally have empty content.
//
// Failure handling:
//   - if the very first frame (start_assistant_message) fails, the stream is
//     never opened on the msghub side, so we propagate the error and let the
//     caller decide whether to retry;
//   - if append_delta fails mid-stream, we mark the stream closed and let the
//     Router observe the error;
//   - finish_message failures are surfaced to the caller; the platform side
//     will eventually time out and garbage-collect the streaming row.
func (s *replyStream) SendChunk(ctx context.Context, content string, done bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return errors.New("local: reply stream already closed")
	}
	if !done && content == "" {
		return errors.New("local: non-final chunk content cannot be empty")
	}

	if s.msgID == "" {
		// First chunk: start the assistant message and grab the server-assigned msg_id.
		ack, err := s.platform.request(ctx, FrameStartAssistantMessage, StartAssistantMessageReq{
			ConvID:      s.convID,
			ClientMsgID: s.clientMsgID,
		})
		if err != nil {
			return fmt.Errorf("local: start_assistant_message: %w", err)
		}
		s.msgID = pickMsgID(ack, s.clientMsgID)
		s.platform.bindStreamForCancel(ctx, s.msgID)
	}

	if content != "" && !done {
		if err := s.platform.writeFrame(FrameAppendDelta, "", AppendDeltaReq{
			MsgID:     s.msgID,
			DeltaText: content,
		}); err != nil {
			s.closed = true
			s.platform.releaseStreamCancel(s.msgID)
			return fmt.Errorf("local: append_delta: %w", err)
		}
	}

	if done {
		final := buildFinalContent(content)
		err := s.platform.writeFrame(FrameFinishMessage, "", FinishMessageReq{
			MsgID:        s.msgID,
			Status:       StatusDone,
			FinalContent: final,
		})
		s.closed = true
		s.platform.releaseStreamCancel(s.msgID)
		if err != nil {
			return fmt.Errorf("local: finish_message: %w", err)
		}
	}
	return nil
}

// Abort marks the assistant message as cancelled/errored. Currently nothing
// in router calls this directly — context cancellation is observed via
// handleCancelRequest — but the helper is exposed for future use.
func (s *replyStream) Abort(ctx context.Context, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.msgID == "" {
		return nil
	}
	s.closed = true
	s.platform.releaseStreamCancel(s.msgID)
	if status != StatusError && status != StatusCancelled {
		status = StatusCancelled
	}
	return s.platform.writeFrame(FrameFinishMessage, "", FinishMessageReq{
		MsgID:  s.msgID,
		Status: status,
	})
}

func buildFinalContent(last string) *string {
	if last == "" {
		return nil
	}
	v := last
	return &v
}

func pickMsgID(ack *AckEvt, fallback string) string {
	if ack != nil && strings.TrimSpace(ack.MsgID) != "" {
		return ack.MsgID
	}
	return fallback
}

func newClientMsgID() string {
	return fmt.Sprintf("cli-%d-%s", time.Now().UnixNano(), shortRandom(6))
}

func shortRandom(n int) string {
	id := newRequestID() // "req-<hex>"
	id = strings.TrimPrefix(id, "req-")
	if len(id) > n {
		return id[:n]
	}
	return id
}

// ─── inbound: msghub → bridge ───

// supportedBridgeAgents mirrors router.isSupportedAgentType. We duplicate the
// list here so platform/local stays free of a router import. If the bridge
// gains a new agent kind, add it in both places.
var supportedBridgeAgents = map[string]struct{}{
	"claude": {},
	"codex":  {},
	"hermes": {},
	"gemini": {},
}

func (p *Platform) handleUserMessage(ctx context.Context, evt *UserMessageEvt) {
	if evt == nil {
		return
	}
	if len(evt.Message.Attachments) > 0 {
		p.logger.Printf("local: user_message %s carries %d attachment(s); MVP ignores attachments", evt.Message.ID, len(evt.Message.Attachments))
	}

	// Tell the bridge session manager which agent this conv belongs to BEFORE
	// the Router pulls the message off messageChan. Router will then resolve
	// the active session for UserID=convID and find the correct AgentType.
	// Skip the bind for unknown agent ids so the Router emits its usual
	// "no agent" error instead of registering a bogus type.
	if p.deps.Sessions != nil {
		agentType := strings.ToLower(strings.TrimSpace(evt.AgentID))
		if _, ok := supportedBridgeAgents[agentType]; ok {
			p.deps.Sessions.BindActiveSessionAgent(evt.ConvID, agentType)
		} else if agentType != "" {
			p.logger.Printf("local: user_message %s has unsupported agent_id=%q; not binding session", evt.Message.ID, evt.AgentID)
		}
	}

	wmsg := &weibo.Message{
		ID:        evt.Message.ID,
		Type:      weibo.MessageTypeText,
		Content:   evt.Message.Content,
		UserID:    evt.ConvID, // routes assistant chunks back via Reply(convID, …)
		UserName:  evt.AgentID,
		Timestamp: deriveTimestamp(evt.Message.CreatedAt, p.now),
	}
	select {
	case <-ctx.Done():
		return
	case p.messageChan <- wmsg:
	}
}

func deriveTimestamp(createdAt int64, nowFn func() time.Time) int64 {
	if createdAt > 0 {
		return createdAt
	}
	if nowFn != nil {
		return nowFn().UnixMilli()
	}
	return time.Now().UnixMilli()
}

// bindStreamForCancel records the cancel hook for the current Router turn so
// that an inbound cancel_request can stop it.
//
// Today the Router does not expose a per-message cancel callback to platforms,
// so this is a no-op storage that will be wired up when Router gains the hook.
// We still populate it to make handleCancelRequest observable in tests.
func (p *Platform) bindStreamForCancel(ctx context.Context, msgID string) {
	if msgID == "" {
		return
	}
	_, cancel := context.WithCancel(ctx)
	p.cancelMu.Lock()
	p.cancelByMsgID[msgID] = cancel
	p.cancelMu.Unlock()
}

func (p *Platform) releaseStreamCancel(msgID string) {
	if msgID == "" {
		return
	}
	p.cancelMu.Lock()
	cancel, ok := p.cancelByMsgID[msgID]
	delete(p.cancelByMsgID, msgID)
	p.cancelMu.Unlock()
	if ok && cancel != nil {
		cancel()
	}
}

func (p *Platform) handleCancelRequest(msgID string) {
	p.cancelMu.Lock()
	cancel, ok := p.cancelByMsgID[msgID]
	delete(p.cancelByMsgID, msgID)
	p.cancelMu.Unlock()
	if ok && cancel != nil {
		cancel()
		p.logger.Printf("local: cancelled stream for msg_id=%s on user cancel_request", msgID)
	} else {
		p.logger.Printf("local: cancel_request for unknown msg_id=%s (ignored)", msgID)
	}
}
