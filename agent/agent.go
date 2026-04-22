package agent

import "context"

// EventType 定义 Agent 流式事件类型。
type EventType string

const (
	EventTypeSession   EventType = "session"
	EventTypeDelta     EventType = "delta"
	EventTypeMessage   EventType = "message"
	EventTypeToolStart EventType = "tool_start"
	EventTypeToolEnd   EventType = "tool_end"
	EventTypeError     EventType = "error"
	EventTypeDone      EventType = "done"
)

// Event 描述 Agent 运行过程中产生的结构化事件。
type Event struct {
	Type      EventType      `json:"type"`
	Content   string         `json:"content,omitempty"`
	SessionID string         `json:"session_id,omitempty"`
	Error     string         `json:"error,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// Agent AI Agent 接口
type Agent interface {
	// Name 返回 Agent 名称
	Name() string

	// Execute 执行 AI 任务并等待完整结果
	Execute(ctx context.Context, sessionID string, input string) (string, error)

	// ExecuteStream 执行 AI 任务并返回事件流
	ExecuteStream(ctx context.Context, sessionID string, input string) (<-chan Event, error)

	// IsAvailable 检查 Agent 是否可用
	IsAvailable() bool
}
