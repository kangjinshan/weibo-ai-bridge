package agent

// Agent AI Agent 接口
type Agent interface {
	// Name 返回 Agent 名称
	Name() string

	// Execute 执行 AI 任务
	Execute(input string) (string, error)

	// IsAvailable 检查 Agent 是否可用
	IsAvailable() bool
}
