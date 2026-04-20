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
			"id":        "msg123",
			"type":      "text",
			"text":      "测试消息",
			"user_id":   "user456",
			"user_name": "测试用户",
			"timestamp": float64(1713556800),
		}

		msg, err := ParseMessage(raw)
		assert.NoError(t, err)
		assert.NotNil(t, msg)
		assert.Equal(t, "msg123", msg.ID)
		assert.Equal(t, MessageTypeText, msg.Type)
		assert.Equal(t, "测试消息", msg.Content)
		assert.Equal(t, "user456", msg.UserID)
		assert.Equal(t, "测试用户", msg.UserName)
		assert.Equal(t, int64(1713556800), msg.Timestamp)
		assert.Nil(t, msg.ReplyContext)
	})

	t.Run("parse image message", func(t *testing.T) {
		raw := map[string]interface{}{
			"id":        "msg456",
			"type":      "image",
			"image_url": "https://example.com/image.jpg",
			"user_id":   "user789",
			"user_name": "图片用户",
			"timestamp": float64(1713556900),
		}

		msg, err := ParseMessage(raw)
		assert.NoError(t, err)
		assert.NotNil(t, msg)
		assert.Equal(t, "msg456", msg.ID)
		assert.Equal(t, MessageTypeImage, msg.Type)
		assert.Equal(t, "https://example.com/image.jpg", msg.Content)
		assert.Equal(t, "user789", msg.UserID)
	})

	t.Run("parse link message", func(t *testing.T) {
		raw := map[string]interface{}{
			"id":        "msg789",
			"type":      "link",
			"url":       "https://example.com",
			"title":     "链接标题",
			"user_id":   "user000",
			"user_name": "链接用户",
			"timestamp": float64(1713557000),
		}

		msg, err := ParseMessage(raw)
		assert.NoError(t, err)
		assert.NotNil(t, msg)
		assert.Equal(t, "msg789", msg.ID)
		assert.Equal(t, MessageTypeLink, msg.Type)
	})

	t.Run("parse at message", func(t *testing.T) {
		raw := map[string]interface{}{
			"id":        "msg000",
			"type":      "at",
			"text":      "@测试用户 你好",
			"user_id":   "user111",
			"user_name": "发送者",
			"timestamp": float64(1713557100),
		}

		msg, err := ParseMessage(raw)
		assert.NoError(t, err)
		assert.NotNil(t, msg)
		assert.Equal(t, "msg000", msg.ID)
		assert.Equal(t, MessageTypeAt, msg.Type)
		assert.Equal(t, "@测试用户 你好", msg.Content)
	})

	t.Run("parse reply message", func(t *testing.T) {
		raw := map[string]interface{}{
			"id":        "msg111",
			"type":      "reply",
			"text":      "回复内容",
			"user_id":   "user222",
			"user_name": "回复用户",
			"timestamp": float64(1713557200),
			"reply_context": map[string]interface{}{
				"original_message_id": "orig123",
				"original_user_id":    "origUser",
				"original_user_name":  "原用户",
			},
		}

		msg, err := ParseMessage(raw)
		assert.NoError(t, err)
		assert.NotNil(t, msg)
		assert.Equal(t, "msg111", msg.ID)
		assert.Equal(t, MessageTypeReply, msg.Type)
		assert.NotNil(t, msg.ReplyContext)
		assert.Equal(t, "orig123", msg.ReplyContext.OriginalMessageID)
		assert.Equal(t, "origUser", msg.ReplyContext.OriginalUserID)
		assert.Equal(t, "原用户", msg.ReplyContext.OriginalUserName)
	})

	t.Run("missing required fields", func(t *testing.T) {
		raw := map[string]interface{}{
			"id": "msg123",
		}

		msg, err := ParseMessage(raw)
		assert.Error(t, err)
		assert.Nil(t, msg)
	})

	t.Run("invalid timestamp", func(t *testing.T) {
		raw := map[string]interface{}{
			"id":        "msg123",
			"type":      "text",
			"text":      "测试",
			"user_id":   "user456",
			"user_name": "用户",
			"timestamp": "invalid",
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