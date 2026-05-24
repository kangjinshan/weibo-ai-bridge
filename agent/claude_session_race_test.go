package agent

import (
	"sync"
	"testing"
)

func TestClaudeSessionCurrentSessionIDIsRaceSafe(t *testing.T) {
	s := &claudeInteractiveSession{
		state: &claudeStreamState{messageSnapshot: make(map[string]string)},
	}

	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(2)
		go func(v string) {
			defer wg.Done()
			s.setSessionID(v)
		}("sess-" + string(rune('a'+(i%26))))
		go func() {
			defer wg.Done()
			_ = s.CurrentSessionID()
		}()
	}
	wg.Wait()
}
