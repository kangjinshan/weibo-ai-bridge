package agent

import (
	"sync"
)

// Manager Agent 管理器
type Manager struct {
	agents       map[string]Agent
	defaultAgent string
	mu           sync.RWMutex
}

// NewManager 创建新的 Agent 管理器
func NewManager() *Manager {
	return &Manager{
		agents: make(map[string]Agent),
	}
}

// Register 注册 Agent
func (m *Manager) Register(agent Agent) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.agents[agent.Name()] = agent
}

// Unregister 注销 Agent
func (m *Manager) Unregister(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.agents, name)

	// 如果注销的是默认 Agent，清除默认设置
	if m.defaultAgent == name {
		m.defaultAgent = ""
	}
}

// GetAgent 根据名称获取 Agent
func (m *Manager) GetAgent(name string) (Agent, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	agent, exists := m.agents[name]
	return agent, exists
}

// GetDefaultAgent 获取默认 Agent
func (m *Manager) GetDefaultAgent() Agent {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// 如果设置了默认 Agent，返回它
	if m.defaultAgent != "" {
		if agent, exists := m.agents[m.defaultAgent]; exists {
			return agent
		}
	}

	// 否则返回第一个可用的 Agent
	for _, agent := range m.agents {
		if agent.IsAvailable() {
			return agent
		}
	}

	// 如果没有可用 Agent，返回 nil
	return nil
}

// SetDefault 设置默认 Agent
func (m *Manager) SetDefault(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.agents[name]; exists {
		m.defaultAgent = name
	}
}

// ListAgents 列出所有 Agent
func (m *Manager) ListAgents() []Agent {
	m.mu.RLock()
	defer m.mu.RUnlock()

	agents := make([]Agent, 0, len(m.agents))
	for _, agent := range m.agents {
		agents = append(agents, agent)
	}

	return agents
}

// Count 获取 Agent 数量
func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return len(m.agents)
}
