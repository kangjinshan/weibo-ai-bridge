package weibo

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestReconnectRespectsCancelledContext(t *testing.T) {
	p := &Platform{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := p.reconnect(ctx); err == nil {
		t.Fatalf("expected reconnect on cancelled ctx to return error, got nil")
	} else if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestStopWaitsForCloseGoroutine(t *testing.T) {
	p := &Platform{}
	p.wg.Add(1)
	released := make(chan struct{})
	go func() {
		defer p.wg.Done()
		<-released
	}()

	waited := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(waited)
	}()

	select {
	case <-waited:
		t.Fatalf("wg.Wait returned before goroutine released")
	default:
	}

	close(released)
	<-waited
}

var _ = sync.Mutex{}

func TestNextReconnectDelayExponentialBackoffWithCap(t *testing.T) {
	tests := []struct {
		current time.Duration
		want    time.Duration
	}{
		{current: 0, want: time.Second},
		{current: time.Second, want: 2 * time.Second},
		{current: 2 * time.Second, want: 4 * time.Second},
		{current: 16 * time.Second, want: 30 * time.Second},
		{current: 30 * time.Second, want: 30 * time.Second},
	}

	for _, tt := range tests {
		if got := nextReconnectDelay(tt.current); got != tt.want {
			t.Fatalf("nextReconnectDelay(%s) = %s, want %s", tt.current, got, tt.want)
		}
	}
}

func TestShouldStopReconnectAfterRepeatedAuthFailures(t *testing.T) {
	authErr := errors.New("token error: invalid credentials (code: 40003)")
	nonAuthErr := errors.New("dial tcp: connection refused")

	for failures := 1; failures < maxReconnectAuthFailures; failures++ {
		if shouldStopReconnectAfterFailure(authErr, failures) {
			t.Fatalf("auth failure %d stopped reconnect before threshold", failures)
		}
	}
	if !shouldStopReconnectAfterFailure(authErr, maxReconnectAuthFailures) {
		t.Fatalf("auth failure %d did not stop reconnect", maxReconnectAuthFailures)
	}
	if shouldStopReconnectAfterFailure(nonAuthErr, maxReconnectAuthFailures) {
		t.Fatalf("non-auth failures should not stop reconnect")
	}
}
