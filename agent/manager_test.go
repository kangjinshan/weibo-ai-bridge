package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// MockAgent 用于测试的模拟 Agent
type MockAgent struct {
	name      string
	available bool
}

func (m *MockAgent) Name() string {
	return m.name
}

func (m *MockAgent) Execute(input string) (string, error) {
	return "response: " + input, nil
}

func (m *MockAgent) IsAvailable() bool {
	return m.available
}

func TestManager_NewManager(t *testing.T) {
	mgr := NewManager()
	assert.NotNil(t, mgr)
	assert.NotNil(t, mgr.agents)
}

func TestManager_Register(t *testing.T) {
	mgr := NewManager()
	agent := &MockAgent{name: "test-agent", available: true}

	// 注册 Agent
	mgr.Register(agent)

	// 验证注册成功
	assert.Equal(t, 1, mgr.Count())
}

func TestManager_GetAgent(t *testing.T) {
	mgr := NewManager()
	agent := &MockAgent{name: "test-agent", available: true}
	mgr.Register(agent)

	// 测试获取已注册的 Agent
	found, exists := mgr.GetAgent("test-agent")
	assert.True(t, exists)
	assert.NotNil(t, found)
	assert.Equal(t, "test-agent", found.Name())

	// 测试获取不存在的 Agent
	notFound, exists := mgr.GetAgent("non-existent")
	assert.False(t, exists)
	assert.Nil(t, notFound)
}

func TestManager_GetDefaultAgent(t *testing.T) {
	t.Run("with default agent set", func(t *testing.T) {
		mgr := NewManager()
		agent1 := &MockAgent{name: "agent1", available: true}
		agent2 := &MockAgent{name: "agent2", available: true}

		mgr.Register(agent1)
		mgr.Register(agent2)
		mgr.SetDefault("agent1")

		defaultAgent := mgr.GetDefaultAgent()
		assert.NotNil(t, defaultAgent)
		assert.Equal(t, "agent1", defaultAgent.Name())
	})

	t.Run("without default agent set", func(t *testing.T) {
		mgr := NewManager()
		agent := &MockAgent{name: "only-agent", available: true}
		mgr.Register(agent)

		// 当没有设置默认时，应返回第一个可用的 Agent
		defaultAgent := mgr.GetDefaultAgent()
		assert.NotNil(t, defaultAgent)
		assert.Equal(t, "only-agent", defaultAgent.Name())
	})

	t.Run("with no agents", func(t *testing.T) {
		mgr := NewManager()

		// 当没有任何 Agent 时，应返回 nil
		defaultAgent := mgr.GetDefaultAgent()
		assert.Nil(t, defaultAgent)
	})
}

func TestManager_ResolveAgent(t *testing.T) {
	mgr := NewManager()
	claudeAgent := &MockAgent{name: "claude-code", available: true}
	codexAgent := &MockAgent{name: "codex", available: true}
	mgr.Register(claudeAgent)
	mgr.Register(codexAgent)
	mgr.SetDefault("claude-code")

	assert.Equal(t, "claude-code", mgr.ResolveAgent("claude").Name())
	assert.Equal(t, "codex", mgr.ResolveAgent("codex").Name())
	assert.Equal(t, "claude-code", mgr.ResolveAgent("").Name())
	assert.Nil(t, mgr.ResolveAgent("missing"))
}

func TestManager_ListAgents(t *testing.T) {
	mgr := NewManager()
	agent1 := &MockAgent{name: "agent1", available: true}
	agent2 := &MockAgent{name: "agent2", available: false}
	agent3 := &MockAgent{name: "agent3", available: true}

	mgr.Register(agent1)
	mgr.Register(agent2)
	mgr.Register(agent3)

	// 测试列出所有 Agent
	agents := mgr.ListAgents()
	assert.Len(t, agents, 3)

	// 验证所有 Agent 都在列表中
	names := make(map[string]bool)
	for _, a := range agents {
		names[a.Name()] = true
	}
	assert.True(t, names["agent1"])
	assert.True(t, names["agent2"])
	assert.True(t, names["agent3"])
}

func TestManager_Count(t *testing.T) {
	mgr := NewManager()

	// 初始应为 0
	assert.Equal(t, 0, mgr.Count())

	// 添加后应为 1
	agent1 := &MockAgent{name: "agent1", available: true}
	mgr.Register(agent1)
	assert.Equal(t, 1, mgr.Count())

	// 再添加后应为 2
	agent2 := &MockAgent{name: "agent2", available: true}
	mgr.Register(agent2)
	assert.Equal(t, 2, mgr.Count())
}

func TestManager_Unregister(t *testing.T) {
	mgr := NewManager()
	agent := &MockAgent{name: "test-agent", available: true}
	mgr.Register(agent)

	assert.Equal(t, 1, mgr.Count())

	// 注销 Agent
	mgr.Unregister("test-agent")
	assert.Equal(t, 0, mgr.Count())

	// 再次获取应不存在
	found, exists := mgr.GetAgent("test-agent")
	assert.False(t, exists)
	assert.Nil(t, found)
}

func TestManager_ConcurrentAccess(t *testing.T) {
	mgr := NewManager()

	// 并发注册
	done := make(chan bool)
	for i := 0; i < 100; i++ {
		go func(id int) {
			agent := &MockAgent{
				name:      "agent-" + string(rune(id)),
				available: true,
			}
			mgr.Register(agent)
			done <- true
		}(i)
	}

	// 等待所有 goroutine 完成
	for i := 0; i < 100; i++ {
		<-done
	}

	// 验证并发注册后的数量
	assert.Equal(t, 100, mgr.Count())
}
