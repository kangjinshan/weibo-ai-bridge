package agent

// Agent AI Agent 接口
type Agent interface {
	// Name 返回 Agent 名称
	Name() string

	// Execute 执行 AI 任务
	// input: 用户输入
	// sessionID: 会话ID，用于持久化会话上下文，空字符串表示新会话
	Execute(input string, sessionID string) (string, error)

	// IsAvailable 检查 Agent 是否可用
	IsAvailable() bool
}
