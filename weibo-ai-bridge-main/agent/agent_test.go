package agent

import (
	"testing"

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

	response, err := agent.Execute("test input", "")
	assert.NoError(t, err)
	assert.Equal(t, "response: test input", response)
}

// TestInterface 验证 MockAgent 实现了 Agent 接口
func TestInterface(t *testing.T) {
	var _ Agent = &MockAgent{name: "test", available: true}
}
