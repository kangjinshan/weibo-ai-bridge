package local

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// userMessageFixture mirrors DM/docs/protocol/examples/user_message.json.
const userMessageFixture = `{
  "type": "user_message",
  "data": {
    "conv_id": "conv_claude",
    "agent_id": "claude",
    "message": {
      "id": "msg_01HXYZ123",
      "conversation_id": "conv_claude",
      "seq": 42,
      "role": "user",
      "kind": "text",
      "content": "请帮我写一段排序代码",
      "status": "done",
      "origin_device": "dev_web_abc",
      "client_msg_id": "cli-01HXYZ12345",
      "created_at": 1748400000000,
      "updated_at": 1748400000000
    }
  }
}`

const userApprovalFixture = `{
  "type": "user_approval",
  "id": "req-9c1d",
  "data": {
    "conv_id": "conv_claude",
    "approval_id": "appr_01HXYZ200",
    "action": "allow_once",
    "device": "dev_web_abc"
  }
}`

const userCommandFixture = `{
  "type": "user_command",
  "data": {
    "conv_id": "conv_claude",
    "command": "/new",
    "args": ["claude"],
    "device": "dev_web_abc"
  }
}`

const ackFixture = `{
  "type": "ack",
  "data": { "request_id": "req-7f3a", "ok": true, "msg_id": "msg_99" }
}`

const cancelRequestFixture = `{
  "type": "cancel_request",
  "data": { "msg_id": "msg_42" }
}`

func TestDecodeEnvelope_UserMessage(t *testing.T) {
	env, err := DecodeEnvelope([]byte(userMessageFixture))
	require.NoError(t, err)
	require.Equal(t, FrameUserMessage, env.Type)

	var payload UserMessageEvt
	require.NoError(t, DecodePayload(env, &payload))

	assert.Equal(t, "conv_claude", payload.ConvID)
	assert.Equal(t, "claude", payload.AgentID)
	assert.Equal(t, "msg_01HXYZ123", payload.Message.ID)
	assert.Equal(t, "请帮我写一段排序代码", payload.Message.Content)
	assert.Equal(t, int64(42), payload.Message.Seq)
	assert.Equal(t, RoleUser, payload.Message.Role)
	assert.Equal(t, KindText, payload.Message.Kind)
	require.NotNil(t, payload.Message.OriginDevice)
	assert.Equal(t, "dev_web_abc", *payload.Message.OriginDevice)
	require.NotNil(t, payload.Message.ClientMsgID)
	assert.Equal(t, "cli-01HXYZ12345", *payload.Message.ClientMsgID)
}

func TestDecodeEnvelope_UserApproval(t *testing.T) {
	env, err := DecodeEnvelope([]byte(userApprovalFixture))
	require.NoError(t, err)
	require.Equal(t, FrameUserApproval, env.Type)
	assert.Equal(t, "req-9c1d", env.ID)

	var payload UserApprovalEvt
	require.NoError(t, DecodePayload(env, &payload))
	assert.Equal(t, "appr_01HXYZ200", payload.ApprovalID)
	assert.Equal(t, "allow_once", payload.Action)
	require.NotNil(t, payload.Device)
	assert.Equal(t, "dev_web_abc", *payload.Device)
}

func TestDecodeEnvelope_UserCommand(t *testing.T) {
	env, err := DecodeEnvelope([]byte(userCommandFixture))
	require.NoError(t, err)
	require.Equal(t, FrameUserCommand, env.Type)

	var payload UserCommandEvt
	require.NoError(t, DecodePayload(env, &payload))
	assert.Equal(t, "/new", payload.Command)
	assert.Equal(t, []string{"claude"}, payload.Args)
}

func TestDecodeEnvelope_Ack_WithMsgIDExtension(t *testing.T) {
	env, err := DecodeEnvelope([]byte(ackFixture))
	require.NoError(t, err)
	require.Equal(t, FrameAck, env.Type)

	var payload AckEvt
	require.NoError(t, DecodePayload(env, &payload))
	assert.Equal(t, "req-7f3a", payload.RequestID)
	assert.True(t, payload.OK)
	assert.Equal(t, "msg_99", payload.MsgID, "ack should expose optional msg_id used by start_assistant_message")
}

func TestDecodeEnvelope_CancelRequest(t *testing.T) {
	env, err := DecodeEnvelope([]byte(cancelRequestFixture))
	require.NoError(t, err)
	require.Equal(t, FrameCancelRequest, env.Type)

	var payload CancelRequestEvt
	require.NoError(t, DecodePayload(env, &payload))
	assert.Equal(t, "msg_42", payload.MsgID)
}

func TestEncodeFrame_RegisterAgents(t *testing.T) {
	payload := RegisterAgentsReq{
		Agents: []Agent{
			{ID: "claude", DisplayName: "Claude"},
			{ID: "codex", DisplayName: "Codex"},
		},
		Commands: []CommandMeta{
			{Name: "/new", Description: "新建会话"},
			{Name: "/switch", Description: "切换会话", Args: []string{"index"}},
		},
	}
	raw, err := EncodeFrame(FrameRegisterAgents, "req-init-1", payload)
	require.NoError(t, err)

	var env Envelope
	require.NoError(t, json.Unmarshal(raw, &env))
	assert.Equal(t, FrameRegisterAgents, env.Type)
	assert.Equal(t, "req-init-1", env.ID)

	// 字段名 / 顺序应与 schema 对齐
	var roundtrip RegisterAgentsReq
	require.NoError(t, json.Unmarshal(env.Data, &roundtrip))
	assert.Equal(t, payload, roundtrip)
}

func TestEncodeFrame_StartAssistantMessageWithClientID(t *testing.T) {
	raw, err := EncodeFrame(FrameStartAssistantMessage, "req-start-1", StartAssistantMessageReq{
		ConvID:      "conv_claude",
		ClientMsgID: "cli-001",
	})
	require.NoError(t, err)
	var env Envelope
	require.NoError(t, json.Unmarshal(raw, &env))

	var payload StartAssistantMessageReq
	require.NoError(t, json.Unmarshal(env.Data, &payload))
	assert.Equal(t, "conv_claude", payload.ConvID)
	assert.Equal(t, "cli-001", payload.ClientMsgID)
}

func TestEncodeFrame_AppendDeltaRoundTrip(t *testing.T) {
	raw, err := EncodeFrame(FrameAppendDelta, "", AppendDeltaReq{
		MsgID:     "msg_99",
		DeltaText: "好的, 这是一段",
	})
	require.NoError(t, err)
	env, err := DecodeEnvelope(raw)
	require.NoError(t, err)
	assert.Equal(t, FrameAppendDelta, env.Type)
	assert.Empty(t, env.ID, "events without request semantics omit id")

	var payload AppendDeltaReq
	require.NoError(t, DecodePayload(env, &payload))
	assert.Equal(t, "msg_99", payload.MsgID)
	assert.Equal(t, "好的, 这是一段", payload.DeltaText)
}

func TestEncodeFrame_FinishMessageOptionalFinalContent(t *testing.T) {
	final := "final body"
	raw, err := EncodeFrame(FrameFinishMessage, "", FinishMessageReq{
		MsgID:        "msg_99",
		Status:       StatusDone,
		FinalContent: &final,
	})
	require.NoError(t, err)
	env, err := DecodeEnvelope(raw)
	require.NoError(t, err)

	var payload FinishMessageReq
	require.NoError(t, DecodePayload(env, &payload))
	assert.Equal(t, StatusDone, payload.Status)
	require.NotNil(t, payload.FinalContent)
	assert.Equal(t, final, *payload.FinalContent)
}

func TestEncodeFrame_RejectsEmptyType(t *testing.T) {
	_, err := EncodeFrame("", "", nil)
	require.Error(t, err)
}

func TestDecodeEnvelope_RejectsEmpty(t *testing.T) {
	_, err := DecodeEnvelope(nil)
	require.Error(t, err)
}

func TestDecodeEnvelope_RejectsMissingType(t *testing.T) {
	_, err := DecodeEnvelope([]byte(`{"data":{}}`))
	require.Error(t, err)
}
