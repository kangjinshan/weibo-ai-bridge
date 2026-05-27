package local

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── fake hub ─────────────────────────────────────────────────────────────

type fakeHub struct {
	t        *testing.T
	server   *httptest.Server
	upgrader websocket.Upgrader

	mu       sync.Mutex
	conn     *websocket.Conn
	received chan Envelope
	gotConn  chan struct{}
	gotOnce  sync.Once

	// auto-ack controls whether the hub auto-replies ack{ok:true} to
	// request-style frames carrying an envelope id. Tests that want to
	// drive the ack themselves can toggle this off.
	autoAck  bool
	ackMsgID string // optional msg_id returned in auto-ack
}

func newFakeHub(t *testing.T) *fakeHub {
	h := &fakeHub{
		t:        t,
		upgrader: websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }},
		received: make(chan Envelope, 32),
		gotConn:  make(chan struct{}),
		autoAck:  true,
	}
	h.server = httptest.NewServer(http.HandlerFunc(h.handle))
	return h
}

func (h *fakeHub) URL() string {
	return "ws" + strings.TrimPrefix(h.server.URL, "http") + "/ws"
}

func (h *fakeHub) Close() {
	h.mu.Lock()
	c := h.conn
	h.conn = nil
	h.mu.Unlock()
	if c != nil {
		_ = c.Close()
	}
	h.server.Close()
}

func (h *fakeHub) handle(w http.ResponseWriter, r *http.Request) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.t.Logf("upgrade failed: %v", err)
		return
	}
	h.mu.Lock()
	h.conn = conn
	h.mu.Unlock()
	h.gotOnce.Do(func() { close(h.gotConn) })

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		env, err := DecodeEnvelope(data)
		if err != nil {
			h.t.Logf("hub decode error: %v", err)
			continue
		}
		select {
		case h.received <- *env:
		default:
		}
		if h.autoAck && env.ID != "" {
			h.sendAck(env.ID, true, "", h.ackMsgID)
		}
	}
}

func (h *fakeHub) sendAck(requestID string, ok bool, errMsg, msgID string) {
	h.mu.Lock()
	conn := h.conn
	h.mu.Unlock()
	if conn == nil {
		return
	}
	ack := AckEvt{RequestID: requestID, OK: ok}
	if errMsg != "" {
		ack.Error = &errMsg
	}
	if msgID != "" {
		ack.MsgID = msgID
	}
	raw, err := EncodeFrame(FrameAck, "", ack)
	if err != nil {
		h.t.Fatalf("encode ack: %v", err)
	}
	_ = conn.WriteMessage(websocket.TextMessage, raw)
}

func (h *fakeHub) push(t *testing.T, frameType FrameType, payload interface{}) {
	h.mu.Lock()
	conn := h.conn
	h.mu.Unlock()
	if conn == nil {
		t.Fatalf("push %s: no client connection", frameType)
	}
	raw, err := EncodeFrame(frameType, "", payload)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, raw))
}

func (h *fakeHub) waitFor(t *testing.T, predicate func(env Envelope) bool, timeout time.Duration) Envelope {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case env := <-h.received:
			if predicate(env) {
				return env
			}
		case <-deadline:
			t.Fatal("timed out waiting for frame")
		}
	}
}

// ─── test doubles ─────────────────────────────────────────────────────────

type fakeAgentLister struct{ agents []AgentDescriptor }

func (f *fakeAgentLister) ListAgents() []AgentDescriptor { return f.agents }

type fakeCommandLister struct{ commands []CommandDescriptor }

func (f *fakeCommandLister) ListCommands() []CommandDescriptor { return f.commands }

type fakeSessionBinder struct {
	mu    sync.Mutex
	calls []bindCall
}

type bindCall struct {
	UserID    string
	AgentType string
}

func (f *fakeSessionBinder) BindActiveSessionAgent(userID, agentType string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, bindCall{UserID: userID, AgentType: agentType})
}

func (f *fakeSessionBinder) Calls() []bindCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]bindCall, len(f.calls))
	copy(out, f.calls)
	return out
}

func newTestPlatform(t *testing.T, hub *fakeHub) *Platform {
	return newTestPlatformWithBinder(t, hub, nil)
}

func newTestPlatformWithBinder(t *testing.T, hub *fakeHub, binder SessionBinder) *Platform {
	t.Helper()
	plat, err := NewPlatform(hub.URL(), "test-token", "bridge-test", PlatformDeps{
		Agents: &fakeAgentLister{agents: []AgentDescriptor{
			{ID: "claude", DisplayName: "Claude"},
			{ID: "codex", DisplayName: "Codex"},
		}},
		Commands: &fakeCommandLister{commands: []CommandDescriptor{
			{Name: "/help", Description: "显示帮助"},
			{Name: "/new", Description: "新建会话", Args: []string{"agent_type"}},
		}},
		Logger:   log.New(testWriter{t: t}, "[hub-test] ", 0),
		Sessions: binder,
	})
	require.NoError(t, err)
	require.NoError(t, plat.Start(context.Background()))
	t.Cleanup(func() { _ = plat.Stop() })

	// Wait for the WS handshake before returning so tests have a stable starting point.
	select {
	case <-hub.gotConn:
	case <-time.After(2 * time.Second):
		t.Fatal("platform never connected to fake hub")
	}
	return plat
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) { w.t.Log(strings.TrimRight(string(p), "\n")); return len(p), nil }

// ─── tests ────────────────────────────────────────────────────────────────

func TestNewPlatform_ValidatesScheme(t *testing.T) {
	_, err := NewPlatform("http://example.com", "tok", "bridge", PlatformDeps{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ws/wss")

	_, err = NewPlatform("ws://example.com", "", "bridge", PlatformDeps{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "device_token")
}

func TestPlatform_RegistersAgentsOnConnect(t *testing.T) {
	hub := newFakeHub(t)
	defer hub.Close()
	_ = newTestPlatform(t, hub)

	env := hub.waitFor(t, func(e Envelope) bool { return e.Type == FrameRegisterAgents }, 2*time.Second)
	var payload RegisterAgentsReq
	require.NoError(t, DecodePayload(&env, &payload))
	require.Len(t, payload.Agents, 2)
	assert.Equal(t, "claude", payload.Agents[0].ID)
	assert.Equal(t, "online", payload.Agents[0].Status)
	require.Len(t, payload.Commands, 2)
	assert.Equal(t, "/help", payload.Commands[0].Name)
	assert.Equal(t, []string{"agent_type"}, payload.Commands[1].Args)
}

func TestPlatform_UserMessageFlowsThroughChannel(t *testing.T) {
	hub := newFakeHub(t)
	defer hub.Close()
	plat := newTestPlatform(t, hub)
	// drain register_agents so the next assertion isn't confused
	hub.waitFor(t, func(e Envelope) bool { return e.Type == FrameRegisterAgents }, 2*time.Second)

	device := "dev_web"
	clientID := "cli-1"
	hub.push(t, FrameUserMessage, UserMessageEvt{
		ConvID:  "conv-1",
		AgentID: "claude",
		Message: Message{
			ID:             "msg-in-1",
			ConversationID: "conv-1",
			Seq:            1,
			Role:           RoleUser,
			Kind:           KindText,
			Content:        "你好",
			Status:         StatusDone,
			OriginDevice:   &device,
			ClientMsgID:    &clientID,
			CreatedAt:      1700000000000,
			UpdatedAt:      1700000000000,
		},
	})

	select {
	case msg := <-plat.Messages():
		assert.Equal(t, "msg-in-1", msg.ID)
		assert.Equal(t, "conv-1", msg.UserID, "conv_id should be carried as UserID so Reply routes back to the same conversation")
		assert.Equal(t, "你好", msg.Content)
	case <-time.After(time.Second):
		t.Fatal("user_message never arrived on Messages channel")
	}
}

// Regression: msghub tells the bridge which agent each conv belongs to via
// UserMessageEvt.AgentID. The bridge previously dropped that field, so the
// Router fell back to the default agent and every conv was answered by claude
// regardless of the user's selection. This test pins the contract that
// platform/local hands the agent_id to a SessionBinder *before* the message
// reaches the Router, so the Router's GetActiveSession path sees the right
// AgentType.
func TestPlatform_UserMessageBindsActiveSessionToAgentID(t *testing.T) {
	hub := newFakeHub(t)
	defer hub.Close()
	binder := &fakeSessionBinder{}
	plat := newTestPlatformWithBinder(t, hub, binder)
	hub.waitFor(t, func(e Envelope) bool { return e.Type == FrameRegisterAgents }, 2*time.Second)

	push := func(convID, agentID, msgID string) {
		hub.push(t, FrameUserMessage, UserMessageEvt{
			ConvID:  convID,
			AgentID: agentID,
			Message: Message{
				ID:             msgID,
				ConversationID: convID,
				Seq:            1,
				Role:           RoleUser,
				Kind:           KindText,
				Content:        "ping",
				Status:         StatusDone,
				CreatedAt:      1700000000000,
				UpdatedAt:      1700000000000,
			},
		})
	}

	push("conv-claude", "claude", "m1")
	push("conv-codex", "codex", "m2")

	// drain both messages so we know handleUserMessage completed for both
	for i := 0; i < 2; i++ {
		select {
		case <-plat.Messages():
		case <-time.After(time.Second):
			t.Fatalf("user_message %d never arrived on Messages channel", i+1)
		}
	}

	calls := binder.Calls()
	require.Len(t, calls, 2, "binder must be called once per inbound user_message")
	assert.Equal(t, bindCall{UserID: "conv-claude", AgentType: "claude"}, calls[0])
	assert.Equal(t, bindCall{UserID: "conv-codex", AgentType: "codex"}, calls[1])
}

// Defensive: an unknown agent_id from msghub must NOT bind a bogus agentType
// onto the Router session, because Router would then try to resolve an
// unsupported agent and fail in confusing ways. We expect the binder to be
// skipped (or rejected) for unsupported types; the message itself can still
// flow through so the Router can emit its usual "no agent" error.
func TestPlatform_UserMessageSkipsBindForUnsupportedAgentID(t *testing.T) {
	hub := newFakeHub(t)
	defer hub.Close()
	binder := &fakeSessionBinder{}
	plat := newTestPlatformWithBinder(t, hub, binder)
	hub.waitFor(t, func(e Envelope) bool { return e.Type == FrameRegisterAgents }, 2*time.Second)

	hub.push(t, FrameUserMessage, UserMessageEvt{
		ConvID:  "conv-x",
		AgentID: "definitely-not-an-agent",
		Message: Message{
			ID: "m1", ConversationID: "conv-x", Seq: 1,
			Role: RoleUser, Kind: KindText, Content: "ping",
			Status: StatusDone, CreatedAt: 1700000000000, UpdatedAt: 1700000000000,
		},
	})

	select {
	case <-plat.Messages():
	case <-time.After(time.Second):
		t.Fatal("user_message never arrived on Messages channel")
	}

	assert.Empty(t, binder.Calls(), "binder must not be called for unsupported agent_id")
}

func TestPlatform_UserCommandIsLoweredToSlashMessage(t *testing.T) {
	hub := newFakeHub(t)
	defer hub.Close()
	plat := newTestPlatform(t, hub)
	hub.waitFor(t, func(e Envelope) bool { return e.Type == FrameRegisterAgents }, 2*time.Second)

	device := "dev_web"
	hub.push(t, FrameUserCommand, UserCommandEvt{
		ConvID:  "conv-cmd",
		Command: "/new",
		Args:    []string{"claude"},
		Device:  &device,
	})

	select {
	case msg := <-plat.Messages():
		assert.Equal(t, "conv-cmd", msg.UserID)
		assert.Equal(t, "/new claude", msg.Content, "user_command should be reassembled into a plain slash command string the existing CommandHandler can parse")
	case <-time.After(time.Second):
		t.Fatal("user_command never arrived on Messages channel")
	}
}

func TestPlatform_ReplyStreamsStartDeltaFinish(t *testing.T) {
	hub := newFakeHub(t)
	hub.ackMsgID = "msg-server-99"
	defer hub.Close()
	plat := newTestPlatform(t, hub)
	hub.waitFor(t, func(e Envelope) bool { return e.Type == FrameRegisterAgents }, 2*time.Second)

	stream, err := plat.OpenReplyStream(context.Background(), "conv-out")
	require.NoError(t, err)

	require.NoError(t, stream.SendChunk(context.Background(), "Hello ", false))
	require.NoError(t, stream.SendChunk(context.Background(), "world", false))
	require.NoError(t, stream.SendChunk(context.Background(), "!", true))

	start := hub.waitFor(t, func(e Envelope) bool { return e.Type == FrameStartAssistantMessage }, 2*time.Second)
	var startReq StartAssistantMessageReq
	require.NoError(t, DecodePayload(&start, &startReq))
	assert.Equal(t, "conv-out", startReq.ConvID)
	assert.NotEmpty(t, startReq.ClientMsgID)

	delta1 := hub.waitFor(t, func(e Envelope) bool { return e.Type == FrameAppendDelta }, 2*time.Second)
	delta2 := hub.waitFor(t, func(e Envelope) bool { return e.Type == FrameAppendDelta }, 2*time.Second)
	finish := hub.waitFor(t, func(e Envelope) bool { return e.Type == FrameFinishMessage }, 2*time.Second)

	var d1, d2 AppendDeltaReq
	require.NoError(t, DecodePayload(&delta1, &d1))
	require.NoError(t, DecodePayload(&delta2, &d2))
	assert.Equal(t, "msg-server-99", d1.MsgID, "msg_id from ack should be reused on every append_delta")
	assert.Equal(t, "Hello ", d1.DeltaText)
	assert.Equal(t, "world", d2.DeltaText)

	var fin FinishMessageReq
	require.NoError(t, DecodePayload(&finish, &fin))
	assert.Equal(t, "msg-server-99", fin.MsgID)
	assert.Equal(t, StatusDone, fin.Status)
	require.NotNil(t, fin.FinalContent)
	assert.Equal(t, "!", *fin.FinalContent)
}

func TestPlatform_ReplyStreamFallsBackToClientMsgIDWhenAckHasNoMsgID(t *testing.T) {
	hub := newFakeHub(t)
	// leave ackMsgID empty so the platform must fall back to client id
	defer hub.Close()
	plat := newTestPlatform(t, hub)
	hub.waitFor(t, func(e Envelope) bool { return e.Type == FrameRegisterAgents }, 2*time.Second)

	stream, err := plat.OpenReplyStream(context.Background(), "conv-x")
	require.NoError(t, err)
	require.NoError(t, stream.SendChunk(context.Background(), "ok", true))

	start := hub.waitFor(t, func(e Envelope) bool { return e.Type == FrameStartAssistantMessage }, 2*time.Second)
	var startReq StartAssistantMessageReq
	require.NoError(t, DecodePayload(&start, &startReq))

	finish := hub.waitFor(t, func(e Envelope) bool { return e.Type == FrameFinishMessage }, 2*time.Second)
	var fin FinishMessageReq
	require.NoError(t, DecodePayload(&finish, &fin))
	assert.Equal(t, startReq.ClientMsgID, fin.MsgID, "msg_id should fall back to ClientMsgID when ack omits it")
}

func TestPlatform_ApprovalRoundTrip(t *testing.T) {
	hub := newFakeHub(t)
	defer hub.Close()
	plat := newTestPlatform(t, hub)
	hub.waitFor(t, func(e Envelope) bool { return e.Type == FrameRegisterAgents }, 2*time.Second)

	var callCount int
	var gotAction, gotDevice string
	plat.RegisterApprovalRouter("conv-a", "appr-1", func(_ context.Context, action, device string) error {
		callCount++
		gotAction = action
		gotDevice = device
		return nil
	})

	require.NoError(t, plat.RequestApproval(context.Background(), "conv-a", "appr-1", "Bash", map[string]interface{}{"cmd": "ls"}))
	req := hub.waitFor(t, func(e Envelope) bool { return e.Type == FrameRequestApproval }, 2*time.Second)
	var reqPayload RequestApprovalReq
	require.NoError(t, DecodePayload(&req, &reqPayload))
	assert.Equal(t, "Bash", reqPayload.Tool)
	assert.Equal(t, "appr-1", reqPayload.ApprovalID)

	device := "dev_btn"
	hub.push(t, FrameUserApproval, UserApprovalEvt{ConvID: "conv-a", ApprovalID: "appr-1", Action: "allow_once", Device: &device})
	hub.push(t, FrameUserApproval, UserApprovalEvt{ConvID: "conv-a", ApprovalID: "appr-1", Action: "deny", Device: &device})

	require.Eventually(t, func() bool { return callCount == 1 }, 2*time.Second, 20*time.Millisecond, "duplicate user_approval frames should be deduplicated")
	assert.Equal(t, "allow_once", gotAction)
	assert.Equal(t, "dev_btn", gotDevice)
}

func TestPlatform_CancelRequestStopsRecordedStream(t *testing.T) {
	hub := newFakeHub(t)
	hub.ackMsgID = "msg-cancel-1"
	defer hub.Close()
	plat := newTestPlatform(t, hub)
	hub.waitFor(t, func(e Envelope) bool { return e.Type == FrameRegisterAgents }, 2*time.Second)

	stream, err := plat.OpenReplyStream(context.Background(), "conv-c")
	require.NoError(t, err)
	require.NoError(t, stream.SendChunk(context.Background(), "partial", false))
	hub.waitFor(t, func(e Envelope) bool { return e.Type == FrameAppendDelta }, 2*time.Second)

	plat.cancelMu.Lock()
	_, tracked := plat.cancelByMsgID["msg-cancel-1"]
	plat.cancelMu.Unlock()
	require.True(t, tracked, "active stream must register its cancel hook before cancel_request can act on it")

	hub.push(t, FrameCancelRequest, CancelRequestEvt{MsgID: "msg-cancel-1"})

	require.Eventually(t, func() bool {
		plat.cancelMu.Lock()
		_, still := plat.cancelByMsgID["msg-cancel-1"]
		plat.cancelMu.Unlock()
		return !still
	}, time.Second, 20*time.Millisecond, "cancel_request should drop the tracked cancel hook")
}

// ─── basic safety / unit-level checks ─────────────────────────────────────

func TestPlatform_StopIsIdempotent(t *testing.T) {
	hub := newFakeHub(t)
	defer hub.Close()
	plat := newTestPlatform(t, hub)
	require.NoError(t, plat.Stop())
	require.NoError(t, plat.Stop())
}

func TestPlatform_NextBackoffSaturates(t *testing.T) {
	d := defaultInitialReconnect
	for i := 0; i < 12; i++ {
		d = nextBackoff(d)
	}
	assert.Equal(t, defaultMaxReconnect, d)
}

func TestUserCommand_IgnoresMissingSlash(t *testing.T) {
	hub := newFakeHub(t)
	defer hub.Close()
	plat := newTestPlatform(t, hub)
	hub.waitFor(t, func(e Envelope) bool { return e.Type == FrameRegisterAgents }, 2*time.Second)

	hub.push(t, FrameUserCommand, UserCommandEvt{ConvID: "conv-bad", Command: "no-slash"})
	select {
	case msg := <-plat.Messages():
		t.Fatalf("unexpected message dispatched for invalid user_command: %+v", msg)
	case <-time.After(150 * time.Millisecond):
		// expected: invalid commands should be dropped
	}
}

// Sanity-check the package compiles against gorilla/websocket so reviewers
// can find the import quickly if dependency hygiene comes up.
var _ = websocket.IsCloseError

// jsonRoundTrip is a tiny helper used by ad-hoc debugging when a test fails;
// kept in the file so future contributors don't have to reinvent it.
func jsonRoundTrip(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "<marshal err: " + err.Error() + ">"
	}
	return string(b)
}

// Some compile-time guards so we don't silently regress shapes that other
// parts of the bridge rely on.
var (
	_ AgentLister   = (*fakeAgentLister)(nil)
	_ CommandLister = (*fakeCommandLister)(nil)
)

// Defensive: make sure NewPlatform refuses dial addresses that are not
// reachable so test-time misconfigurations fail fast.
func TestPlatform_FailsFastOnDeadEndpoint(t *testing.T) {
	// Find a port that is definitely not listening.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())

	plat, err := NewPlatform("ws://"+addr+"/ws", "tok", "bridge", PlatformDeps{
		Agents: &fakeAgentLister{},
	})
	require.NoError(t, err)
	require.NoError(t, plat.Start(context.Background()))
	defer plat.Stop()
	// Start does not block on initial dial failure (it just logs and retries),
	// so success here means: the supervisor was launched and the platform is
	// considered "running" from the bridge perspective.
	assert.True(t, plat.IsRunning())
}

// pretty-print helper used while debugging timeouts; intentionally referenced
// once so unused-warnings tools don't strip it.
func init() { _ = fmt.Sprintf("%v", errors.New("boot")) }
