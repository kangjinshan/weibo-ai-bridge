package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestMockAgent 验证 MockAgent 的实现是否正确
func TestMockAgent(t *testing.T) {
	agent := &MockAgent{
		name:      "test-agent",
		available: true,
	}

	assert.Equal(t, "test-agent", agent.Name())
	assert.True(t, agent.IsAvailable())

	response, err := agent.Execute(context.Background(), "", "test input")
	assert.NoError(t, err)
	assert.Equal(t, "response: test input", response)
}

// TestInterface 验证 MockAgent 实现了 Agent 接口
func TestInterface(t *testing.T) {
	var _ Agent = &MockAgent{name: "test", available: true}
}

func TestEmitOrCancelReturnsFalseWhenContextCanceledAndChannelFull(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	events := make(chan Event, 1)
	events <- Event{Type: EventTypeMessage, Content: "queued"}
	cancel()

	done := make(chan bool, 1)
	go func() {
		done <- emitOrCancel(ctx, events, Event{Type: EventTypeMessage, Content: "late"})
	}()

	select {
	case ok := <-done:
		assert.False(t, ok)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("emitOrCancel blocked after context cancellation")
	}
}
