package local

import (
	"context"
	"errors"
	"strings"
	"sync"
)

// RegisterApprovalRouter attaches an approval handler for a conversation. The
// router invokes this when it asks msghub to request approval, then unregisters
// when the approval flow completes. This lets handleUserApproval dispatch the
// inbound action to the right callback without any global state inside Router.
//
// De-duplication note: when both a button approval frame and a text approval
// arrive for the same approval id, only the first one to call the router
// callback wins; subsequent calls are dropped. The de-dupe key combines
// approval id + conv id so distinct conversations stay isolated.
func (p *Platform) RegisterApprovalRouter(convID, approvalID string, fn ApprovalRouter) {
	if convID == "" || approvalID == "" || fn == nil {
		return
	}
	key := approvalKey(convID, approvalID)
	wrapped := dedupApprovalRouter(fn)
	p.approvalMu.Lock()
	p.approvalRouters[key] = wrapped
	p.approvalMu.Unlock()
}

// UnregisterApprovalRouter removes a previously installed approval handler.
func (p *Platform) UnregisterApprovalRouter(convID, approvalID string) {
	if convID == "" || approvalID == "" {
		return
	}
	key := approvalKey(convID, approvalID)
	p.approvalMu.Lock()
	delete(p.approvalRouters, key)
	p.approvalMu.Unlock()
}

// RequestApproval ships an outbound request_approval frame to msghub. The
// caller normally pairs this with RegisterApprovalRouter so the matching
// user_approval frame can be delivered back to it.
func (p *Platform) RequestApproval(ctx context.Context, convID, approvalID, tool string, payload map[string]interface{}) error {
	if convID == "" || approvalID == "" {
		return errors.New("local: conv_id and approval_id are required")
	}
	return p.writeFrame(FrameRequestApproval, "", RequestApprovalReq{
		ConvID:     convID,
		ApprovalID: approvalID,
		Tool:       tool,
		Payload:    payload,
	})
}

func (p *Platform) handleUserApproval(ctx context.Context, evt *UserApprovalEvt) {
	if evt == nil {
		return
	}
	action := strings.TrimSpace(evt.Action)
	if action == "" {
		p.logger.Printf("local: dropping empty user_approval action for %s/%s", evt.ConvID, evt.ApprovalID)
		return
	}
	device := ""
	if evt.Device != nil {
		device = *evt.Device
	}
	key := approvalKey(evt.ConvID, evt.ApprovalID)
	p.approvalMu.RLock()
	fn := p.approvalRouters[key]
	p.approvalMu.RUnlock()
	if fn == nil {
		p.logger.Printf("local: no approval handler for %s/%s (action=%q); ignoring", evt.ConvID, evt.ApprovalID, action)
		return
	}
	if err := fn(ctx, action, device); err != nil {
		p.logger.Printf("local: approval handler returned error for %s/%s: %v", evt.ConvID, evt.ApprovalID, err)
	}
}

func approvalKey(convID, approvalID string) string {
	return convID + "|" + approvalID
}

// dedupApprovalRouter ensures the underlying handler is fired at most once.
// Text-channel approvals (e.g. user typing "允许" into chat) still flow
// through user_message and Router.parseApprovalAction, so this is a safety
// net against double-resolution when both channels race.
func dedupApprovalRouter(fn ApprovalRouter) ApprovalRouter {
	var once sync.Once
	var firstErr error
	return func(ctx context.Context, action, device string) error {
		once.Do(func() {
			firstErr = fn(ctx, action, device)
		})
		return firstErr
	}
}

// approvalKnown is a helper used by tests.
func (p *Platform) approvalKnown(convID, approvalID string) bool {
	key := approvalKey(convID, approvalID)
	p.approvalMu.RLock()
	defer p.approvalMu.RUnlock()
	_, ok := p.approvalRouters[key]
	return ok
}

// jsonStringOrEmpty exposes jsonString to other files in the package without
// re-declaring the helper (silences "declared and not used" for unused payload
// debugging utilities).
func jsonStringOrEmpty(v interface{}) string { return jsonString(v) }
