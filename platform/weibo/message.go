package weibo

import (
	"encoding/json"
	"errors"
	"fmt"
)

// MessageType 定义消息类型常量
type MessageType string

const (
	// MessageTypeText 文本消息
	MessageTypeText MessageType = "text"
	// MessageTypeImage 图片消息
	MessageTypeImage MessageType = "image"
	// MessageTypeLink 链接消息
	MessageTypeLink MessageType = "link"
	// MessageTypeAt @消息
	MessageTypeAt MessageType = "at"
	// MessageTypeReply 回复消息
	MessageTypeReply MessageType = "reply"
)

// Message 定义微博消息结构
type Message struct {
	ID           string        `json:"id"`
	Type         MessageType   `json:"type"`
	Content      string        `json:"content"`
	UserID       string        `json:"user_id"`
	UserName     string        `json:"user_name"`
	Timestamp    int64         `json:"timestamp"`
	ReplyContext *ReplyContext `json:"reply_context,omitempty"`
}

// ReplyContext 定义回复上下文结构
type ReplyContext struct {
	OriginalMessageID string `json:"original_message_id"`
	OriginalUserID    string `json:"original_user_id"`
	OriginalUserName  string `json:"original_user_name"`
}

// ParseMessage 从原始数据解析消息
func ParseMessage(raw map[string]interface{}) (*Message, error) {
	// 检查消息类型
	msgType, ok := raw["type"].(string)
	if !ok {
		return nil, errors.New("missing or invalid message type")
	}

	// 微博 WebSocket 推送格式
	if msgType == "message" {
		return parseWSMessage(raw)
	}

	// 兼容测试中使用的直接消息格式
	return parseDirectMessage(raw, MessageType(msgType))
}

func parseWSMessage(raw map[string]interface{}) (*Message, error) {
	// 提取 payload
	payload, ok := raw["payload"].(map[string]interface{})
	if !ok {
		return nil, errors.New("missing or invalid payload")
	}

	// 提取消息 ID
	messageID, ok := payload["messageId"].(string)
	if !ok {
		return nil, errors.New("missing or invalid message id")
	}

	// 提取发送用户 ID
	fromUserID, ok := payload["fromUserId"].(string)
	if !ok {
		return nil, errors.New("missing or invalid from_user_id")
	}

	// 提取消息内容
	content, _ := payload["text"].(string)

	// 提取时间戳
	timestampFloat, ok := payload["timestamp"].(float64)
	if !ok {
		return nil, errors.New("missing or invalid timestamp")
	}

	msg := &Message{
		ID:        messageID,
		Type:      MessageTypeText,
		UserID:    fromUserID,
		UserName:  shortWeiboUser(fromUserID),
		Content:   content,
		Timestamp: int64(timestampFloat),
	}

	return msg, nil
}

func parseDirectMessage(raw map[string]interface{}, msgType MessageType) (*Message, error) {
	messageID, ok := raw["id"].(string)
	if !ok {
		return nil, errors.New("missing or invalid message id")
	}

	userID, ok := raw["user_id"].(string)
	if !ok {
		return nil, errors.New("missing or invalid user_id")
	}

	timestampFloat, ok := raw["timestamp"].(float64)
	if !ok {
		return nil, errors.New("missing or invalid timestamp")
	}

	userName, _ := raw["user_name"].(string)
	msg := &Message{
		ID:        messageID,
		Type:      msgType,
		UserID:    userID,
		UserName:  userName,
		Timestamp: int64(timestampFloat),
	}

	switch msgType {
	case MessageTypeText, MessageTypeAt, MessageTypeReply:
		msg.Content, _ = raw["text"].(string)
	case MessageTypeImage:
		msg.Content, _ = raw["image_url"].(string)
	case MessageTypeLink:
		msg.Content, _ = raw["url"].(string)
	default:
		return nil, fmt.Errorf("unsupported message type: %s", msgType)
	}

	if msgType == MessageTypeReply {
		replyContext, err := parseReplyContext(raw["reply_context"])
		if err != nil {
			return nil, err
		}
		msg.ReplyContext = replyContext
	}

	return msg, nil
}

func parseReplyContext(raw interface{}) (*ReplyContext, error) {
	if raw == nil {
		return nil, nil
	}

	replyContext, ok := raw.(map[string]interface{})
	if !ok {
		return nil, errors.New("invalid reply_context")
	}

	originalMessageID, _ := replyContext["original_message_id"].(string)
	originalUserID, _ := replyContext["original_user_id"].(string)
	originalUserName, _ := replyContext["original_user_name"].(string)

	return &ReplyContext{
		OriginalMessageID: originalMessageID,
		OriginalUserID:    originalUserID,
		OriginalUserName:  originalUserName,
	}, nil
}

// shortWeiboUser 生成微博用户短名称
func shortWeiboUser(id string) string {
	if len(id) > 32 {
		return id[:32] + "…"
	}
	return id
}

// ToJSON 将消息转换为 JSON
func (m *Message) ToJSON() ([]byte, error) {
	return json.Marshal(m)
}
