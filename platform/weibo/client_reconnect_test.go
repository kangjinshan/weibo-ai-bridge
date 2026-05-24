package weibo

import (
	"context"
	"sync"
	"testing"
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
