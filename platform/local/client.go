package local

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/kangjinshan/weibo-ai-bridge/platform/weibo"
)

// Defaults mirror the values discussed in DM/docs/superpowers/specs/2026-05-27-msghub-design.md §4.
const (
	defaultInitialReconnect = 500 * time.Millisecond
	defaultMaxReconnect     = 30 * time.Second
	defaultDialTimeout      = 10 * time.Second
	defaultWriteTimeout     = 10 * time.Second
	defaultReadIdle         = 90 * time.Second
	defaultPingInterval     = 30 * time.Second
	defaultMessageBuffer    = 100
	defaultAckTimeout       = 15 * time.Second
)

// AgentLister is implemented by agent.Manager and any test double.
type AgentLister interface {
	ListAgents() []AgentDescriptor
}

// AgentDescriptor is the minimal projection of agent.Agent we need to publish
// during register_agents. Defined here so test doubles don't have to import
// the agent package.
type AgentDescriptor struct {
	ID          string
	DisplayName string
}

// CommandLister enumerates the slash commands the bridge supports.
type CommandLister interface {
	ListCommands() []CommandDescriptor
}

// CommandDescriptor is a stable, package-local projection of the command metadata.
type CommandDescriptor struct {
	Name        string
	Description string
	Args        []string
}

// PlatformDeps wires the upstream platform to bridge-internal singletons
// without taking a hard dependency on concrete types.
type PlatformDeps struct {
	Agents   AgentLister
	Commands CommandLister
	Sessions SessionBinder
	Logger   *log.Logger
	// Now is injected for tests; defaults to time.Now.
	Now func() time.Time
}

// SessionBinder lets the local platform tell the bridge's session manager
// which agent each inbound conv should be routed to. msghub is the source of
// truth for "conv X belongs to agent Y" (the user picked the agent in the
// web/android client). Without this hook the Router falls back to its default
// agent and every conv ends up answered by the same one. Bind is invoked
// before the user_message reaches the Router, so the Router's GetActiveSession
// path sees the right AgentType on the very first turn.
type SessionBinder interface {
	BindActiveSessionAgent(userID, agentType string)
}

// Platform is the msghub upstream adapter. It satisfies router.Platform via
// Reply/OpenReplyStream and matches the surface area cmd/server expects from
// the weibo platform (Messages / Start / Stop / UID).
type Platform struct {
	hubURL      string
	deviceToken string
	bridgeName  string

	deps   PlatformDeps
	logger *log.Logger
	now    func() time.Time

	dialer *websocket.Dialer

	ctx    context.Context
	cancel context.CancelFunc

	connMu sync.Mutex
	conn   *websocket.Conn

	writeMu sync.Mutex

	messageChan chan *weibo.Message

	running atomic.Bool

	// pending request-id → reply channel (ack/error)
	pendingMu sync.Mutex
	pending   map[string]chan ackResult

	// approval bridging: convID → callback for incoming user_approval
	approvalMu      sync.RWMutex
	approvalRouters map[string]ApprovalRouter

	// cancel bridging: msg_id → cancel func for the currently-streaming assistant turn
	cancelMu      sync.Mutex
	cancelByMsgID map[string]context.CancelFunc

	wg sync.WaitGroup
}

// ApprovalRouter is invoked when msghub delivers a user_approval frame.
// The bridge router supplies the actual handler when it asks for approval.
type ApprovalRouter func(ctx context.Context, action string, device string) error

type ackResult struct {
	ack *AckEvt
	err error
}

// NewPlatform constructs a local platform adapter. The returned platform is
// not connected; call Start(ctx) to dial msghub and begin pumping frames.
func NewPlatform(hubURL, deviceToken, bridgeName string, deps PlatformDeps) (*Platform, error) {
	if strings.TrimSpace(hubURL) == "" {
		return nil, errors.New("local: hub_url is required")
	}
	if strings.TrimSpace(deviceToken) == "" {
		return nil, errors.New("local: device_token is required")
	}
	parsed, err := url.Parse(hubURL)
	if err != nil {
		return nil, fmt.Errorf("local: parse hub_url: %w", err)
	}
	if parsed.Scheme != "ws" && parsed.Scheme != "wss" {
		return nil, fmt.Errorf("local: hub_url must use ws/wss scheme, got %q", parsed.Scheme)
	}

	logger := deps.Logger
	if logger == nil {
		logger = log.Default()
	}
	now := deps.Now
	if now == nil {
		now = time.Now
	}

	return &Platform{
		hubURL:      hubURL,
		deviceToken: deviceToken,
		bridgeName:  strings.TrimSpace(bridgeName),
		deps:        deps,
		logger:      logger,
		now:         now,
		dialer: &websocket.Dialer{
			HandshakeTimeout: defaultDialTimeout,
		},
		messageChan:     make(chan *weibo.Message, defaultMessageBuffer),
		pending:         make(map[string]chan ackResult),
		approvalRouters: make(map[string]ApprovalRouter),
		cancelByMsgID:   make(map[string]context.CancelFunc),
	}, nil
}

// UID is required by cmd/server for the startup notification path. Local
// upstream has no equivalent of a "bot uid", so we return 0; the main loop
// already treats 0 as "skip notification".
func (p *Platform) UID() int64 { return 0 }

// Messages exposes the inbound message stream, identical in shape to the
// weibo platform so the existing messageProcessor can be reused.
func (p *Platform) Messages() <-chan *weibo.Message { return p.messageChan }

// IsRunning reports whether Start has been called and Stop has not.
func (p *Platform) IsRunning() bool { return p.running.Load() }

// Start opens the WS connection and spawns the read/ping loops. It returns
// once the first connection attempt either succeeds or is cancelled. Later
// disconnects are handled internally with exponential backoff.
func (p *Platform) Start(ctx context.Context) error {
	if !p.running.CompareAndSwap(false, true) {
		return errors.New("local: platform already started")
	}

	runCtx, cancel := context.WithCancel(context.Background())
	if ctx != nil {
		go func() {
			select {
			case <-ctx.Done():
				cancel()
			case <-runCtx.Done():
			}
		}()
	}
	p.ctx = runCtx
	p.cancel = cancel

	if err := p.dial(runCtx); err != nil {
		// Don't fail the bridge if msghub is temporarily down — log and let
		// the reconnect loop keep retrying.
		p.logger.Printf("local: initial dial failed, will keep retrying: %v", err)
	}

	p.wg.Add(1)
	go p.supervisor(runCtx)
	return nil
}

// Stop tears down the connection and goroutines. Safe to call once.
func (p *Platform) Stop() error {
	if !p.running.CompareAndSwap(true, false) {
		return nil
	}
	if p.cancel != nil {
		p.cancel()
	}
	p.closeConn()
	p.wg.Wait()
	close(p.messageChan)
	return nil
}

func (p *Platform) closeConn() {
	p.connMu.Lock()
	defer p.connMu.Unlock()
	if p.conn != nil {
		_ = p.conn.Close()
		p.conn = nil
	}
}

func (p *Platform) currentConn() *websocket.Conn {
	p.connMu.Lock()
	defer p.connMu.Unlock()
	return p.conn
}

func (p *Platform) dial(ctx context.Context) error {
	u, err := url.Parse(p.hubURL)
	if err != nil {
		return fmt.Errorf("local: parse hub url: %w", err)
	}
	q := u.Query()
	q.Set("token", p.deviceToken)
	if p.bridgeName != "" {
		q.Set("name", p.bridgeName)
	}
	q.Set("role", "bridge")
	u.RawQuery = q.Encode()

	dialCtx, dialCancel := context.WithTimeout(ctx, defaultDialTimeout)
	defer dialCancel()

	headers := http.Header{}
	conn, resp, err := p.dialer.DialContext(dialCtx, u.String(), headers)
	if resp != nil {
		// Drain response body so the connection can be reused.
		_ = resp.Body.Close()
	}
	if err != nil {
		return fmt.Errorf("local: dial msghub: %w", err)
	}

	conn.SetReadDeadline(time.Time{})
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Time{})
		return nil
	})

	p.connMu.Lock()
	p.conn = conn
	p.connMu.Unlock()

	p.logger.Printf("local: connected to msghub at %s", p.hubURL)
	return nil
}

// supervisor owns the long-running read loop plus reconnect / ping goroutines.
func (p *Platform) supervisor(ctx context.Context) {
	defer p.wg.Done()

	delay := defaultInitialReconnect
	for {
		if ctx.Err() != nil {
			return
		}
		conn := p.currentConn()
		if conn == nil {
			// Reconnect with backoff.
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			if err := p.dial(ctx); err != nil {
				p.logger.Printf("local: reconnect failed, backing off: %v", err)
				delay = nextBackoff(delay)
				continue
			}
			delay = defaultInitialReconnect
			conn = p.currentConn()
		}

		pingStop := make(chan struct{})
		p.wg.Add(1)
		go p.pingLoop(ctx, conn, pingStop)

		// afterConnected runs register_agents which expects an ack. readLoop
		// must be live to deliver that ack, so run afterConnected in a
		// separate goroutine and let this one enter readLoop immediately.
		go p.afterConnected(ctx)

		p.readLoop(ctx, conn)
		close(pingStop)

		// readLoop returned: connection is gone.
		p.closeConn()
		p.failAllPending(errors.New("local: connection closed"))
	}
}

func (p *Platform) pingLoop(ctx context.Context, conn *websocket.Conn, stop <-chan struct{}) {
	defer p.wg.Done()
	ticker := time.NewTicker(defaultPingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-ticker.C:
			// Use protocol-level ping frame (envelope with type=ping) plus a
			// websocket-level control ping for keepalive at both layers.
			if err := p.writeFrame(FramePing, "", struct{}{}); err != nil {
				p.logger.Printf("local: ping write failed: %v", err)
				_ = conn.Close()
				return
			}
			p.writeMu.Lock()
			err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(defaultWriteTimeout))
			p.writeMu.Unlock()
			if err != nil {
				p.logger.Printf("local: control ping failed: %v", err)
				_ = conn.Close()
				return
			}
		}
	}
}

func (p *Platform) readLoop(ctx context.Context, conn *websocket.Conn) {
	for {
		if ctx.Err() != nil {
			return
		}
		_, data, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() == nil {
				p.logger.Printf("local: read error: %v", err)
			}
			return
		}
		p.handleIncoming(ctx, data)
	}
}

func (p *Platform) handleIncoming(ctx context.Context, raw []byte) {
	env, err := DecodeEnvelope(raw)
	if err != nil {
		p.logger.Printf("local: decode envelope: %v (raw=%s)", err, truncateForLog(raw))
		return
	}
	switch env.Type {
	case FrameAck:
		var ack AckEvt
		if err := DecodePayload(env, &ack); err != nil {
			p.logger.Printf("local: decode ack: %v", err)
			return
		}
		p.resolvePending(ack.RequestID, &ack, nil)

	case FrameError:
		var errEvt ErrorEvt
		_ = DecodePayload(env, &errEvt)
		// If the error frame is correlated by id (ack-style) resolve the
		// matching request; otherwise just log it.
		if env.ID != "" {
			p.resolvePending(env.ID, nil, fmt.Errorf("local: msghub error %s: %s", errEvt.Code, errEvt.Message))
			return
		}
		p.logger.Printf("local: msghub error frame code=%s message=%s", errEvt.Code, errEvt.Message)

	case FramePong:
		// keepalive, nothing to do

	case FrameAgentStatus:
		// Broadcast to all msghub clients (including bridge itself). Bridge
		// is the source of agent status, so nothing to do here.

	case "message_appended", "message_delta", "message_finalized":
		// Broadcasts from msghub to all clients (web/android). Bridge is the
		// origin of these messages, so it has no use for the echo.

	case FrameUserMessage:
		var evt UserMessageEvt
		if err := DecodePayload(env, &evt); err != nil {
			p.logger.Printf("local: decode user_message: %v", err)
			return
		}
		p.handleUserMessage(ctx, &evt)

	case FrameUserApproval:
		var evt UserApprovalEvt
		if err := DecodePayload(env, &evt); err != nil {
			p.logger.Printf("local: decode user_approval: %v", err)
			return
		}
		p.handleUserApproval(ctx, &evt)

	case FrameUserCommand:
		var evt UserCommandEvt
		if err := DecodePayload(env, &evt); err != nil {
			p.logger.Printf("local: decode user_command: %v", err)
			return
		}
		p.handleUserCommand(ctx, &evt)

	case FrameCancelRequest:
		var evt CancelRequestEvt
		if err := DecodePayload(env, &evt); err != nil {
			p.logger.Printf("local: decode cancel_request: %v", err)
			return
		}
		p.handleCancelRequest(evt.MsgID)

	default:
		p.logger.Printf("local: ignoring unknown frame type %q", env.Type)
	}
}

// writeFrame serializes and sends a frame. The id is optional; pass empty for
// fire-and-forget events.
func (p *Platform) writeFrame(frameType FrameType, id string, payload interface{}) error {
	raw, err := EncodeFrame(frameType, id, payload)
	if err != nil {
		return err
	}
	conn := p.currentConn()
	if conn == nil {
		return errors.New("local: connection not established")
	}
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	if err := conn.SetWriteDeadline(time.Now().Add(defaultWriteTimeout)); err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, raw)
}

// request sends a frame that expects an ack/error correlated by request id.
func (p *Platform) request(ctx context.Context, frameType FrameType, payload interface{}) (*AckEvt, error) {
	id := newRequestID()
	ch := make(chan ackResult, 1)
	p.pendingMu.Lock()
	p.pending[id] = ch
	p.pendingMu.Unlock()

	defer func() {
		p.pendingMu.Lock()
		delete(p.pending, id)
		p.pendingMu.Unlock()
	}()

	if err := p.writeFrame(frameType, id, payload); err != nil {
		return nil, err
	}
	timeout := defaultAckTimeout
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, fmt.Errorf("local: ack timeout for frame %s", frameType)
	case res := <-ch:
		if res.err != nil {
			return nil, res.err
		}
		if res.ack != nil && !res.ack.OK {
			msg := ""
			if res.ack.Error != nil {
				msg = *res.ack.Error
			}
			return res.ack, fmt.Errorf("local: msghub rejected %s: %s", frameType, msg)
		}
		return res.ack, nil
	}
}

func (p *Platform) resolvePending(requestID string, ack *AckEvt, err error) {
	if requestID == "" {
		return
	}
	p.pendingMu.Lock()
	ch, ok := p.pending[requestID]
	p.pendingMu.Unlock()
	if !ok {
		return
	}
	select {
	case ch <- ackResult{ack: ack, err: err}:
	default:
	}
}

func (p *Platform) failAllPending(err error) {
	p.pendingMu.Lock()
	pending := p.pending
	p.pending = make(map[string]chan ackResult)
	p.pendingMu.Unlock()
	for _, ch := range pending {
		select {
		case ch <- ackResult{err: err}:
		default:
		}
	}
}

func (p *Platform) afterConnected(ctx context.Context) {
	if err := p.publishRegistration(ctx); err != nil {
		p.logger.Printf("local: register_agents failed: %v", err)
	}
}

func newRequestID() string {
	var buf [12]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("req-%d", time.Now().UnixNano())
	}
	return "req-" + hex.EncodeToString(buf[:])
}

func nextBackoff(d time.Duration) time.Duration {
	next := d * 2
	if next > defaultMaxReconnect {
		return defaultMaxReconnect
	}
	return next
}

func truncateForLog(raw []byte) string {
	const max = 256
	if len(raw) <= max {
		return string(raw)
	}
	return string(raw[:max]) + "...(truncated)"
}

// jsonString is a tiny helper used by approval payload encoding.
func jsonString(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}
