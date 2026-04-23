package agent

import "context"

// EventType 定义 Agent 流式事件类型。
type EventType string

const (
	EventTypeSession   EventType = "session"
	EventTypeDelta     EventType = "delta"
	EventTypeMessage   EventType = "message"
	EventTypeApproval  EventType = "approval"
	EventTypeToolStart EventType = "tool_start"
	EventTypeToolEnd   EventType = "tool_end"
	EventTypeError     EventType = "error"
	EventTypeDone      EventType = "done"
)

type ApprovalAction string

const (
	ApprovalActionAllow    ApprovalAction = "allow"
	ApprovalActionAllowAll ApprovalAction = "allow_all"
	ApprovalActionCancel   ApprovalAction = "cancel"
)

// Event 描述 Agent 运行过程中产生的结构化事件。
type Event struct {
	Type      EventType      `json:"type"`
	Content   string         `json:"content,omitempty"`
	SessionID string         `json:"session_id,omitempty"`
	Error     string         `json:"error,omitempty"`
	ToolName  string         `json:"tool_name,omitempty"`
	ToolInput string         `json:"tool_input,omitempty"`
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

// InteractiveAgent 支持创建可持续交互并能处理中途审批的 Agent。
type InteractiveAgent interface {
	Agent

	// StartSession 创建或恢复一个可继续的会话。
	StartSession(ctx context.Context, sessionID string) (InteractiveSession, error)
}

// InteractiveSession 表示一个长期存活的 Agent 会话。
type InteractiveSession interface {
	// Send 向当前会话发送一条新的用户消息。
	Send(input string) error

	// RespondApproval 对当前待审批请求进行响应。
	RespondApproval(action ApprovalAction) error

	// Events 返回会话产生的事件流。该 channel 会跨多轮复用。
	Events() <-chan Event

	// CurrentSessionID 返回当前 Agent 侧会话 ID。
	CurrentSessionID() string

	// Close 关闭底层会话。
	Close() error
}
