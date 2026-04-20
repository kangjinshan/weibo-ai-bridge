package session

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewManager(t *testing.T) {
	config := ManagerConfig{
		Timeout:     3600,
		MaxSize:     100,
		StoragePath: "",
	}

	mgr := NewManager(config)

	assert.NotNil(t, mgr)
	assert.Equal(t, 0, mgr.Count())
}

func TestNewManager_WithStorage(t *testing.T) {
	config := ManagerConfig{
		Timeout:     3600,
		MaxSize:     100,
		StoragePath: "/tmp/sessions",
	}

	mgr := NewManager(config)

	assert.NotNil(t, mgr)
	assert.Equal(t, 0, mgr.Count())
}

func TestManager_Create(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 3600})

	session := mgr.Create("test-id", "user-123", "claude")

	assert.NotNil(t, session)
	assert.Equal(t, "test-id", session.ID)
	assert.Equal(t, "user-123", session.UserID)
	assert.Equal(t, "claude", session.AgentType)
	assert.Equal(t, StateActive, session.State)
	assert.NotNil(t, session.Context)
	assert.Equal(t, 1, mgr.Count())
}

func TestManager_Get(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 3600})
	mgr.Create("test-id", "user-123", "claude")

	// 测试存在的会话
	session, exists := mgr.Get("test-id")
	assert.True(t, exists)
	assert.NotNil(t, session)
	assert.Equal(t, "test-id", session.ID)

	// 测试不存在的会话
	session, exists = mgr.Get("non-existent")
	assert.False(t, exists)
	assert.Nil(t, session)
}

func TestSession_Update(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 3600})
	session := mgr.Create("test-id", "user-123", "claude")

	originalTime := session.UpdatedAt
	time.Sleep(10 * time.Millisecond) // 确保时间差异

	session.Update("key1", "value1")

	assert.Equal(t, "value1", session.Context["key1"])
	assert.True(t, session.UpdatedAt.After(originalTime))
}

func TestManager_Close(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 3600})
	mgr.Create("test-id", "user-123", "claude")

	mgr.Close("test-id")

	session, exists := mgr.Get("test-id")
	assert.True(t, exists)
	assert.Equal(t, StateClosed, session.State)
}

func TestManager_Delete(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 3600})
	mgr.Create("test-id", "user-123", "claude")

	assert.Equal(t, 1, mgr.Count())

	mgr.Delete("test-id")

	assert.Equal(t, 0, mgr.Count())
	_, exists := mgr.Get("test-id")
	assert.False(t, exists)
}

func TestManager_Count(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 3600})

	assert.Equal(t, 0, mgr.Count())

	mgr.Create("id1", "user1", "claude")
	assert.Equal(t, 1, mgr.Count())

	mgr.Create("id2", "user2", "codex")
	assert.Equal(t, 2, mgr.Count())
}

func TestManager_CleanExpired(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 1}) // 1秒超时

	// 创建会话
	session1 := mgr.Create("id1", "user1", "claude")
	mgr.Create("id2", "user2", "codex")

	// 手动设置一个会话为过期
	session1.UpdatedAt = time.Now().Add(-2 * time.Second)

	time.Sleep(100 * time.Millisecond) // 确保时间差异

	// 清理过期会话
	expired := mgr.CleanExpired()

	assert.Equal(t, 1, expired) // 应该清理掉1个过期会话
	assert.Equal(t, 1, mgr.Count())

	// 验证剩余的是未过期的会话
	_, exists := mgr.Get("id2")
	assert.True(t, exists)
}

func TestSession_ConcurrentAccess(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 3600})
	session := mgr.Create("test-id", "user-123", "claude")

	// 并发读写测试
	done := make(chan bool)

	for i := 0; i < 10; i++ {
		go func(n int) {
			session.Update("counter", n)
			// 使用 Get 方法安全地读取值
			session.mu.RLock()
			_ = session.Context["counter"]
			session.mu.RUnlock()
			done <- true
		}(i)
	}

	// 等待所有协程完成
	for i := 0; i < 10; i++ {
		<-done
	}

	// 如果没有竞态条件，测试通过
	session.mu.RLock()
	assert.NotNil(t, session.Context["counter"])
	session.mu.RUnlock()
}

func TestSession_ToJSON(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 3600})
	session := mgr.Create("test-id", "user-123", "claude")
	session.Update("key1", "value1")
	session.Update("key2", 123)

	jsonData, err := session.ToJSON()

	assert.NoError(t, err)
	assert.NotNil(t, jsonData)
	assert.Contains(t, string(jsonData), "test-id")
	assert.Contains(t, string(jsonData), "user-123")
	assert.Contains(t, string(jsonData), "claude")
	assert.Contains(t, string(jsonData), "active")
}

func TestSession_FromJSON(t *testing.T) {
	jsonData := []byte(`{
		"id": "test-id",
		"user_id": "user-123",
		"agent_type": "claude",
		"state": "active",
		"context": {
			"key1": "value1",
			"key2": 123
		},
		"created_at": "2024-01-01T00:00:00Z",
		"updated_at": "2024-01-01T00:00:00Z"
	}`)

	session := &Session{}
	err := session.FromJSON(jsonData)

	assert.NoError(t, err)
	assert.Equal(t, "test-id", session.ID)
	assert.Equal(t, "user-123", session.UserID)
	assert.Equal(t, "claude", session.AgentType)
	assert.Equal(t, StateActive, session.State)
	assert.NotNil(t, session.Context)
}

func TestSession_ToJSON_FromJSON_RoundTrip(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 3600})
	original := mgr.Create("test-id", "user-123", "claude")
	original.Update("key1", "value1")
	original.Update("key2", 123)

	// 序列化
	jsonData, err := original.ToJSON()
	assert.NoError(t, err)

	// 反序列化
	restored := &Session{}
	err = restored.FromJSON(jsonData)
	assert.NoError(t, err)

	// 验证
	assert.Equal(t, original.ID, restored.ID)
	assert.Equal(t, original.UserID, restored.UserID)
	assert.Equal(t, original.AgentType, restored.AgentType)
	assert.Equal(t, original.State, restored.State)
}

func TestManager_GetOrCreateSession(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 3600})

	// 第一次调用应该创建
	session1 := mgr.GetOrCreateSession("test-id", "user-123", "claude")
	assert.NotNil(t, session1)
	assert.Equal(t, "test-id", session1.ID)
	assert.Equal(t, 1, mgr.Count())

	// 第二次调用应该返回已存在的会话
	session2 := mgr.GetOrCreateSession("test-id", "user-456", "codex")
	assert.NotNil(t, session2)
	assert.Equal(t, session1, session2)
	assert.Equal(t, "user-123", session2.UserID) // 应该保持原来的 user
	assert.Equal(t, 1, mgr.Count())
}

func TestManager_UpdateSession(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 3600})
	mgr.Create("test-id", "user-123", "claude")

	mgr.UpdateSession("test-id", "key1", "value1")

	session, exists := mgr.Get("test-id")
	assert.True(t, exists)
	assert.Equal(t, "value1", session.Context["key1"])
}

func TestManager_UpdateSession_NonExistent(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 3600})

	// 更新不存在的会话不应该 panic
	mgr.UpdateSession("non-existent", "key1", "value1")

	assert.Equal(t, 0, mgr.Count())
}

func TestManager_Create_MaxSize(t *testing.T) {
	mgr := NewManager(ManagerConfig{
		Timeout: 3600,
		MaxSize: 2,
	})

	// 创建两个会话
	session1 := mgr.Create("id1", "user1", "claude")
	session2 := mgr.Create("id2", "user2", "claude")

	assert.NotNil(t, session1)
	assert.NotNil(t, session2)
	assert.Equal(t, 2, mgr.Count())

	// 尝试创建第三个会话，应该返回 nil（因为超过最大限制）
	session3 := mgr.Create("id3", "user3", "claude")
	assert.Nil(t, session3)
	assert.Equal(t, 2, mgr.Count())
}
