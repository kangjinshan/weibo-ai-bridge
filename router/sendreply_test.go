package router

import (
	"context"
	"strings"
	"testing"

	"github.com/kangjinshan/weibo-ai-bridge/agent"
	"github.com/kangjinshan/weibo-ai-bridge/session"
	"github.com/stretchr/testify/assert"
)

func TestSendReply_Simple(t *testing.T) {
	platform := &MockPlatform{}
	sessionMgr := session.NewManager(session.ManagerConfig{
		Timeout: 300,
		MaxSize: 10,
	})
	agentMgr := agent.NewManager()

	router := NewRouter(platform, sessionMgr, agentMgr)

	err := router.sendReply(context.Background(), "msg-1", "Hello")
	assert.NoError(t, err)
	assert.Len(t, platform.replies, 1)
}

func TestSendReply_LongMessage(t *testing.T) {
	platform := &MockPlatform{}
	sessionMgr := session.NewManager(session.ManagerConfig{
		Timeout: 300,
		MaxSize: 10,
	})
	agentMgr := agent.NewManager()

	router := NewRouter(platform, sessionMgr, agentMgr)

	content := strings.Repeat("这是一条测试消息。\n", 100)
	err := router.sendReply(context.Background(), "msg-1", content)
	assert.NoError(t, err)
	assert.Len(t, platform.replies, 1)
}

func TestSendReply_PlatformNotSet(t *testing.T) {
	sessionMgr := session.NewManager(session.ManagerConfig{
		Timeout: 300,
		MaxSize: 10,
	})
	agentMgr := agent.NewManager()

	router := NewRouter(nil, sessionMgr, agentMgr)

	err := router.sendReply(context.Background(), "msg-1", "Test")
	assert.Error(t, err)
}
