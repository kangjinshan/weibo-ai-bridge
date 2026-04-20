package weibo

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMessageTypeConstants(t *testing.T) {
	tests := []struct {
		name     string
		msgType  MessageType
		expected string
	}{
		{"Text message", MessageTypeText, "text"},
		{"Image message", MessageTypeImage, "image"},
		{"Link message", MessageTypeLink, "link"},
		{"At message", MessageTypeAt, "at"},
		{"Reply message", MessageTypeReply, "reply"},
		{"Unknown message", MessageType("unknown"), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, string(tt.msgType))
		})
	}
}

func TestMessage(t *testing.T) {
	t.Run("valid message", func(t *testing.T) {
		msg := &Message{
			ID:        "msg123",
			Type:      MessageTypeText,
			Content:   "测试消息",
			UserID:    "user456",
			UserName:  "测试用户",
			Timestamp: 1713556800,
			ReplyContext: &ReplyContext{
				OriginalMessageID: "orig789",
				OriginalUserID:    "origUser",
				OriginalUserName:  "原用户",
			},
		}

		assert.Equal(t, "msg123", msg.ID)
		assert.Equal(t, MessageTypeText, msg.Type)
		assert.Equal(t, "测试消息", msg.Content)
		assert.Equal(t, "user456", msg.UserID)
		assert.Equal(t, "测试用户", msg.UserName)
		assert.Equal(t, int64(1713556800), msg.Timestamp)
		assert.NotNil(t, msg.ReplyContext)
		assert.Equal(t, "orig789", msg.ReplyContext.OriginalMessageID)
	})

	t.Run("message without reply context", func(t *testing.T) {
		msg := &Message{
			ID:        "msg123",
			Type:      MessageTypeText,
			Content:   "测试消息",
			UserID:    "user456",
			UserName:  "测试用户",
			Timestamp: 1713556800,
		}

		assert.Nil(t, msg.ReplyContext)
	})
}

func TestReplyContext(t *testing.T) {
	t.Run("valid reply context", func(t *testing.T) {
		ctx := &ReplyContext{
			OriginalMessageID: "orig789",
			OriginalUserID:    "origUser",
			OriginalUserName:  "原用户",
		}

		assert.Equal(t, "orig789", ctx.OriginalMessageID)
		assert.Equal(t, "origUser", ctx.OriginalUserID)
		assert.Equal(t, "原用户", ctx.OriginalUserName)
	})
}

func TestParseMessage(t *testing.T) {
	t.Run("parse text message", func(t *testing.T) {
		raw := map[string]interface{}{
			"type": "message",
			"payload": map[string]interface{}{
				"messageId":   "msg123",
				"fromUserId":  "user456",
				"text":        "测试消息",
				"timestamp":   float64(1713556800),
			},
		}

		msg, err := ParseMessage(raw)
		assert.NoError(t, err)
		assert.NotNil(t, msg)
		assert.Equal(t, "msg123", msg.ID)
		assert.Equal(t, MessageType("message"), msg.Type)
		assert.Equal(t, "测试消息", msg.Content)
		assert.Equal(t, "user456", msg.UserID)
		assert.Equal(t, "user456", msg.UserName)
		assert.Equal(t, int64(1713556800), msg.Timestamp)
		assert.Nil(t, msg.ReplyContext)
	})

	t.Run("parse image message", func(t *testing.T) {
		raw := map[string]interface{}{
			"type": "message",
			"payload": map[string]interface{}{
				"messageId":  "msg456",
				"fromUserId": "user789",
				"text":       "https://example.com/image.jpg",
				"timestamp":  float64(1713556900),
			},
		}

		msg, err := ParseMessage(raw)
		assert.NoError(t, err)
		assert.NotNil(t, msg)
		assert.Equal(t, "msg456", msg.ID)
		assert.Equal(t, MessageType("message"), msg.Type)
		assert.Equal(t, "https://example.com/image.jpg", msg.Content)
		assert.Equal(t, "user789", msg.UserID)
		assert.Equal(t, "user789", msg.UserName)
	})

	t.Run("parse link message", func(t *testing.T) {
		raw := map[string]interface{}{
			"type": "message",
			"payload": map[string]interface{}{
				"messageId":  "msg789",
				"fromUserId": "user000",
				"text":       "https://example.com",
				"timestamp":  float64(1713557000),
			},
		}

		msg, err := ParseMessage(raw)
		assert.NoError(t, err)
		assert.NotNil(t, msg)
		assert.Equal(t, "msg789", msg.ID)
		assert.Equal(t, MessageType("message"), msg.Type)
	})

	t.Run("parse at message", func(t *testing.T) {
		raw := map[string]interface{}{
			"type": "message",
			"payload": map[string]interface{}{
				"messageId":  "msg000",
				"fromUserId": "user111",
				"text":       "@测试用户 你好",
				"timestamp":  float64(1713557100),
			},
		}

		msg, err := ParseMessage(raw)
		assert.NoError(t, err)
		assert.NotNil(t, msg)
		assert.Equal(t, "msg000", msg.ID)
		assert.Equal(t, MessageType("message"), msg.Type)
		assert.Equal(t, "@测试用户 你好", msg.Content)
	})

	t.Run("parse reply message", func(t *testing.T) {
		raw := map[string]interface{}{
			"type": "message",
			"payload": map[string]interface{}{
				"messageId":  "msg111",
				"fromUserId": "user222",
				"text":       "回复内容",
				"timestamp":  float64(1713557200),
			},
		}

		msg, err := ParseMessage(raw)
		assert.NoError(t, err)
		assert.NotNil(t, msg)
		assert.Equal(t, "msg111", msg.ID)
		assert.Equal(t, MessageType("message"), msg.Type)
		assert.Equal(t, "回复内容", msg.Content)
	})

	t.Run("missing required fields", func(t *testing.T) {
		raw := map[string]interface{}{
			"type": "message",
			"payload": map[string]interface{}{
				"messageId": "msg123",
			},
		}

		msg, err := ParseMessage(raw)
		assert.Error(t, err)
		assert.Nil(t, msg)
	})

	t.Run("invalid timestamp", func(t *testing.T) {
		raw := map[string]interface{}{
			"type": "message",
			"payload": map[string]interface{}{
				"messageId":  "msg123",
				"fromUserId": "user456",
				"text":       "测试",
				"timestamp":  "invalid",
			},
		}

		msg, err := ParseMessage(raw)
		assert.Error(t, err)
		assert.Nil(t, msg)
	})
}

func TestMessageToJSON(t *testing.T) {
	t.Run("convert to JSON", func(t *testing.T) {
		msg := &Message{
			ID:        "msg123",
			Type:      MessageTypeText,
			Content:   "测试消息",
			UserID:    "user456",
			UserName:  "测试用户",
			Timestamp: 1713556800,
		}

		jsonData, err := msg.ToJSON()
		assert.NoError(t, err)
		assert.NotNil(t, jsonData)

		// 验证 JSON 可以被解析
		var parsed map[string]interface{}
		err = json.Unmarshal(jsonData, &parsed)
		assert.NoError(t, err)
		assert.Equal(t, "msg123", parsed["id"])
		assert.Equal(t, "text", parsed["type"])
		assert.Equal(t, "测试消息", parsed["content"])
	})

	t.Run("convert with reply context", func(t *testing.T) {
		msg := &Message{
			ID:        "msg123",
			Type:      MessageTypeReply,
			Content:   "回复内容",
			UserID:    "user456",
			UserName:  "测试用户",
			Timestamp: 1713556800,
			ReplyContext: &ReplyContext{
				OriginalMessageID: "orig789",
				OriginalUserID:    "origUser",
				OriginalUserName:  "原用户",
			},
		}

		jsonData, err := msg.ToJSON()
		assert.NoError(t, err)
		assert.NotNil(t, jsonData)

		var parsed map[string]interface{}
		err = json.Unmarshal(jsonData, &parsed)
		assert.NoError(t, err)
		assert.NotNil(t, parsed["reply_context"])
	})
}