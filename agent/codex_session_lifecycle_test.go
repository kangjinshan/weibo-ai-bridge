package agent

import (
	"context"
	"testing"
	"time"
)

func TestCodexInteractiveSessionPendingRespCleanedUpOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s := &codexInteractiveSession{
		ctx:         ctx,
		cancel:      cancel,
		pendingResp: make(map[string]chan map[string]any),
		readDone:    make(chan struct{}),
	}
	s.alive.Store(true)

	respCh := make(chan map[string]any, 1)
	s.pendingResp["req-test"] = respCh

	cancel()
	s.cleanupPendingRespOnCancel("req-test")

	s.pendingRespMu.Lock()
	_, stillThere := s.pendingResp["req-test"]
	s.pendingRespMu.Unlock()
	if stillThere {
		t.Fatalf("expected pendingResp[req-test] to be removed after ctx cancel")
	}
}

func TestCodexInteractiveSessionCloseHasTimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s := &codexInteractiveSession{
		ctx:         ctx,
		cancel:      cancel,
		pendingResp: make(map[string]chan map[string]any),
		readDone:    make(chan struct{}),
	}
	s.alive.Store(true)

	done := make(chan error, 1)
	go func() { done <- s.Close() }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Close returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("Close did not return within 3s; it should bound its wait on readDone")
	}
}
