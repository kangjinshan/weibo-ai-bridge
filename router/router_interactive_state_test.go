package router

import (
	"sync"
	"testing"
)

// TestInteractiveSessionStateConcurrentFlags asserts that concurrent reads and
// writes of awaitingApproval / allowAll go through the state's own lock and
// therefore are race-free under `go test -race`.
func TestInteractiveSessionStateConcurrentFlags(t *testing.T) {
	st := &interactiveSessionState{}

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			st.SetAwaitingApproval(true)
			st.SetAllowAll(true)
		}()
		go func() {
			defer wg.Done()
			_ = st.AwaitingApproval()
			_ = st.AllowAll()
		}()
	}
	wg.Wait()

	if !st.AwaitingApproval() {
		t.Fatalf("expected awaitingApproval to be true after writers")
	}
	if !st.AllowAll() {
		t.Fatalf("expected allowAll to be true after writers")
	}
}
