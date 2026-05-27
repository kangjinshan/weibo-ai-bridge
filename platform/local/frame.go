// Package local implements the msghub upstream adapter for weibo-ai-bridge.
//
// This package mirrors the on-the-wire frames defined in the msghub project
// (see DM/docs/protocol/ws-frames.schema.json) as a private projection. We
// deliberately do not import that project here: bridge stays an independent
// Go module, so any contract drift has to be caught by the schema/example
// fixtures duplicated under platform/local/testdata or by the integration
// run described in the Phase 2 plan.
package local

import (
	"encoding/json"
	"errors"
	"fmt"
)

// FrameType is the discriminator string carried on the wire envelope.
type FrameType string

const (
	// bridge → msghub
	FrameRegisterAgents        FrameType = "register_agents"
	FrameStartAssistantMessage FrameType = "start_assistant_message"
	FrameAppendDelta           FrameType = "append_delta"
	FrameFinishMessage         FrameType = "finish_message"
	FrameRequestApproval       FrameType = "request_approval"
	FrameAgentStatus           FrameType = "agent_status"
	FramePing                  FrameType = "ping"

	// msghub → bridge
	FrameAck            FrameType = "ack"
	FramePong           FrameType = "pong"
	FrameError          FrameType = "error"
	FrameUserMessage    FrameType = "user_message"
	FrameUserApproval   FrameType = "user_approval"
	FrameUserCommand    FrameType = "user_command"
	FrameCancelRequest  FrameType = "cancel_request"
)

const (
	RoleUser      = "user"
	RoleAssistant = "assistant"

	KindText            = "text"
	KindApprovalRequest = "approval_request"
	KindApprovalResult  = "approval_result"
	KindCommandEcho     = "command_echo"

	StatusStreaming = "streaming"
	StatusDone      = "done"
	StatusError     = "error"
	StatusCancelled = "cancelled"
)

// Envelope is the wrapper for every WS frame.
type Envelope struct {
	Type FrameType       `json:"type"`
	ID   string          `json:"id,omitempty"`
	Data json.RawMessage `json:"data"`
}

// AttachmentRef mirrors the MVP-reserved attachment payload.
type AttachmentRef struct {
	AttachmentID string `json:"attachment_id"`
	Kind         string `json:"kind"`
	MIME         string `json:"mime"`
	Filename     string `json:"filename,omitempty"`
}

// Message mirrors the protocol Message object.
//
// We mark every field as omitempty-where-safe to avoid serializing zero
// values that are not required on the wire, but we keep required fields
// (id/conversation_id/seq/role/kind/content/status/created_at/updated_at)
// always present.
type Message struct {
	ID             string                 `json:"id"`
	ConversationID string                 `json:"conversation_id"`
	Seq            int64                  `json:"seq"`
	Role           string                 `json:"role"`
	Kind           string                 `json:"kind"`
	Content        string                 `json:"content"`
	Status         string                 `json:"status"`
	OriginDevice   *string                `json:"origin_device,omitempty"`
	Attachments    []AttachmentRef        `json:"attachments,omitempty"`
	Meta           map[string]interface{} `json:"meta,omitempty"`
	ClientMsgID    *string                `json:"client_msg_id,omitempty"`
	CreatedAt      int64                  `json:"created_at"`
	UpdatedAt      int64                  `json:"updated_at"`
}

// Agent mirrors the protocol Agent object used in register_agents/agent_status.
type Agent struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Status      string `json:"status,omitempty"`
}

// CommandMeta mirrors the slash-command descriptor advertised at register time.
type CommandMeta struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Args        []string `json:"args,omitempty"`
}

// ─── bridge → msghub payloads ───

type RegisterAgentsReq struct {
	Agents   []Agent       `json:"agents"`
	Commands []CommandMeta `json:"commands,omitempty"`
}

type StartAssistantMessageReq struct {
	ConvID      string `json:"conv_id"`
	ClientMsgID string `json:"client_msg_id"`
}

type AppendDeltaReq struct {
	MsgID     string `json:"msg_id"`
	DeltaText string `json:"delta_text"`
}

type FinishMessageReq struct {
	MsgID        string  `json:"msg_id"`
	Status       string  `json:"status"`
	FinalContent *string `json:"final_content,omitempty"`
}

type RequestApprovalReq struct {
	ConvID     string                 `json:"conv_id"`
	ApprovalID string                 `json:"approval_id"`
	Tool       string                 `json:"tool"`
	Payload    map[string]interface{} `json:"payload,omitempty"`
}

type AgentStatusReq struct {
	AgentID string `json:"agent_id"`
	Status  string `json:"status"`
}

// ─── msghub → bridge payloads ───

// AckEvt is the request/response correlation frame. Phase 1 server may
// extend it with a "msg_id" field (e.g. as reply to start_assistant_message).
// Unknown fields are tolerated via the embedded RawMessage Extra.
type AckEvt struct {
	RequestID string  `json:"request_id"`
	OK        bool    `json:"ok"`
	Error     *string `json:"error,omitempty"`
	// MsgID is an optional extension: when bridge sends start_assistant_message
	// the server is expected to echo the freshly-allocated assistant msg_id
	// here so subsequent append_delta/finish_message frames can address it.
	// If the field is missing, the bridge falls back to using ClientMsgID.
	MsgID string `json:"msg_id,omitempty"`
}

type ErrorEvt struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type UserMessageEvt struct {
	ConvID  string  `json:"conv_id"`
	AgentID string  `json:"agent_id"`
	Message Message `json:"message"`
}

type UserApprovalEvt struct {
	ConvID     string  `json:"conv_id"`
	ApprovalID string  `json:"approval_id"`
	Action     string  `json:"action"`
	Device     *string `json:"device,omitempty"`
}

type UserCommandEvt struct {
	ConvID  string   `json:"conv_id"`
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
	Device  *string  `json:"device,omitempty"`
}

type CancelRequestEvt struct {
	MsgID string `json:"msg_id"`
}

// EncodeFrame builds a wire envelope for an outbound frame.
//
// id may be empty for events without request/response semantics
// (append_delta, finish_message, agent_status). For request-style frames the
// caller is responsible for generating a unique id used to match the ack.
func EncodeFrame(frameType FrameType, id string, payload interface{}) ([]byte, error) {
	if frameType == "" {
		return nil, errors.New("local: frame type is required")
	}
	var raw json.RawMessage
	if payload == nil {
		raw = json.RawMessage(`{}`)
	} else {
		var err error
		raw, err = json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("local: marshal payload for %s: %w", frameType, err)
		}
	}

	env := Envelope{
		Type: frameType,
		ID:   id,
		Data: raw,
	}
	return json.Marshal(env)
}

// DecodeEnvelope parses the outer envelope without inspecting the data payload.
func DecodeEnvelope(data []byte) (*Envelope, error) {
	if len(data) == 0 {
		return nil, errors.New("local: empty frame")
	}
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("local: decode envelope: %w", err)
	}
	if env.Type == "" {
		return nil, errors.New("local: frame is missing type")
	}
	return &env, nil
}

// DecodePayload unmarshals envelope.Data into the supplied destination.
func DecodePayload(env *Envelope, dst interface{}) error {
	if env == nil {
		return errors.New("local: envelope is nil")
	}
	if dst == nil {
		return errors.New("local: payload destination is nil")
	}
	if len(env.Data) == 0 {
		// Treat as empty object so callers don't have to special-case ping/pong.
		return json.Unmarshal([]byte(`{}`), dst)
	}
	return json.Unmarshal(env.Data, dst)
}
