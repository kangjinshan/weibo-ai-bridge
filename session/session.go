package session

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// State 会话状态
type State string

const (
	StateActive   State = "active"
	StateInactive State = "inactive"
	StateClosed   State = "closed"
)

// Session 用户会话
type Session struct {
	ID        string
	UserID    string
	Title     string
	AgentType string // "claude" or "codex"
	State     State
	Context   map[string]interface{}
	CreatedAt time.Time
	UpdatedAt time.Time
	mu        sync.RWMutex
}

// Manager 会话管理器
type Manager struct {
	sessions     map[string]*Session
	activeByUser map[string]string
	mu           sync.RWMutex
	config       ManagerConfig
	storagePath  string // 存储路径
}

// ManagerConfig 会话管理器配置
type ManagerConfig struct {
	Timeout     int
	MaxSize     int
	StoragePath string // 可选：会话持久化存储路径
}

// NewManager 创建会话管理器
func NewManager(config ManagerConfig) *Manager {
	mgr := &Manager{
		sessions:     make(map[string]*Session),
		activeByUser: make(map[string]string),
		config:       config,
		storagePath:  config.StoragePath,
	}

	// 如果配置了存储路径，尝试加载已有会话
	if config.StoragePath != "" {
		mgr.loadSessions()
	}

	return mgr
}

// Create 创建新会话
func (m *Manager) Create(id, userID, agentType string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.createLocked(id, userID, agentType)
}

// CreateNext 为指定用户创建下一个编号会话。
func (m *Manager) CreateNext(userID, agentType string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.createLocked(m.nextSessionIDLocked(userID), userID, agentType)
}

func (m *Manager) createLocked(id, userID, agentType string) *Session {
	if strings.TrimSpace(id) == "" || strings.TrimSpace(userID) == "" {
		return nil
	}

	// 检查是否超过最大会话数
	if m.config.MaxSize > 0 && len(m.sessions) >= m.config.MaxSize {
		// 清理过期会话
		m.cleanExpiredLocked()
		// 如果清理后仍超过限制，返回 nil
		if len(m.sessions) >= m.config.MaxSize {
			return nil
		}
	}

	now := time.Now()
	session := &Session{
		ID:        id,
		UserID:    userID,
		AgentType: agentType,
		State:     StateActive,
		Context:   make(map[string]interface{}),
		CreatedAt: now,
		UpdatedAt: now,
	}

	m.sessions[id] = session
	m.activeByUser[userID] = id

	// 持久化会话
	if m.storagePath != "" {
		m.saveSessionLocked(session)
	}

	return session
}

func (m *Manager) nextSessionIDLocked(userID string) string {
	prefix := userID + "-"
	maxIndex := 0

	for id, sess := range m.sessions {
		if sess.UserID != userID {
			continue
		}
		if !strings.HasPrefix(id, prefix) {
			continue
		}

		suffix := strings.TrimPrefix(id, prefix)
		index, err := strconv.Atoi(suffix)
		if err != nil || index <= 0 {
			continue
		}
		if index > maxIndex {
			maxIndex = index
		}
	}

	return fmt.Sprintf("%s-%d", userID, maxIndex+1)
}

// GetOrCreateSession 获取或创建会话
func (m *Manager) GetOrCreateSession(id, userID, agentType string) *Session {
	// 先尝试获取
	if session, exists := m.Get(id); exists {
		m.SetActiveSession(userID, id)
		return session
	}

	// 不存在则创建
	return m.Create(id, userID, agentType)
}

// GetActiveSessionID 获取用户当前活跃会话 ID
func (m *Manager) GetActiveSessionID(userID string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	sessionID, exists := m.activeByUser[userID]
	if !exists {
		return ""
	}

	if _, ok := m.sessions[sessionID]; !ok {
		return ""
	}

	return sessionID
}

// GetActiveSession 获取用户当前活跃会话
func (m *Manager) GetActiveSession(userID string) (*Session, bool) {
	sessionID := m.GetActiveSessionID(userID)
	if sessionID == "" {
		return nil, false
	}

	return m.Get(sessionID)
}

// SetActiveSession 设置用户当前活跃会话
func (m *Manager) SetActiveSession(userID, sessionID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, exists := m.sessions[sessionID]
	if !exists || session.UserID != userID {
		return false
	}

	m.activeByUser[userID] = sessionID
	return true
}

// GetOrCreateActiveSession 获取或创建用户当前活跃会话
func (m *Manager) GetOrCreateActiveSession(userID, agentType string) *Session {
	if session, exists := m.GetActiveSession(userID); exists {
		return session
	}

	return m.CreateNext(userID, agentType)
}

// UpdateSession 更新会话
func (m *Manager) UpdateSession(id string, key string, value interface{}) {
	session, exists := m.Get(id)
	if !exists {
		return
	}

	session.Update(key, value)

	// 持久化更新
	if m.storagePath != "" {
		m.mu.Lock()
		m.saveSessionLocked(session)
		m.mu.Unlock()
	}
}

// Get 获取会话
func (m *Manager) Get(id string) (*Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	session, exists := m.sessions[id]
	return session, exists
}

// Update 更新会话
func (s *Session) Update(key string, value interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Context[key] = value
	s.UpdatedAt = time.Now()
}

// SetAgentType 更新会话 Agent 类型
func (s *Session) SetAgentType(agentType string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.AgentType = agentType
	s.UpdatedAt = time.Now()
}

// SetTitleIfEmpty 在标题为空时设置会话标题。
func (s *Session) SetTitleIfEmpty(title string) bool {
	normalized := normalizeSessionTitle(title)
	if normalized == "" {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if strings.TrimSpace(s.Title) != "" {
		return false
	}

	s.Title = normalized
	s.UpdatedAt = time.Now()
	return true
}

func normalizeSessionTitle(title string) string {
	title = strings.Join(strings.Fields(strings.TrimSpace(title)), " ")
	if title == "" {
		return ""
	}

	runes := []rune(title)
	if len(runes) > 50 {
		return string(runes[:50])
	}

	return title
}

// ToJSON 序列化会话为 JSON
func (s *Session) ToJSON() ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// 创建一个可序列化的副本
	data := map[string]interface{}{
		"id":         s.ID,
		"user_id":    s.UserID,
		"title":      s.Title,
		"agent_type": s.AgentType,
		"state":      s.State,
		"context":    s.Context,
		"created_at": s.CreatedAt,
		"updated_at": s.UpdatedAt,
	}

	return json.Marshal(data)
}

// FromJSON 从 JSON 反序列化会话
func (s *Session) FromJSON(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var temp struct {
		ID        string                 `json:"id"`
		UserID    string                 `json:"user_id"`
		Title     string                 `json:"title"`
		AgentType string                 `json:"agent_type"`
		State     State                  `json:"state"`
		Context   map[string]interface{} `json:"context"`
		CreatedAt time.Time              `json:"created_at"`
		UpdatedAt time.Time              `json:"updated_at"`
	}

	if err := json.Unmarshal(data, &temp); err != nil {
		return err
	}

	s.ID = temp.ID
	s.UserID = temp.UserID
	s.Title = temp.Title
	s.AgentType = temp.AgentType
	s.State = temp.State
	s.Context = temp.Context
	s.CreatedAt = temp.CreatedAt
	s.UpdatedAt = temp.UpdatedAt

	return nil
}

// Close 关闭会话
func (m *Manager) Close(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if session, exists := m.sessions[id]; exists {
		session.State = StateClosed
	}
}

// Delete 删除会话
func (m *Manager) Delete(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if session, exists := m.sessions[id]; exists {
		if activeID, ok := m.activeByUser[session.UserID]; ok && activeID == id {
			delete(m.activeByUser, session.UserID)
		}
	}

	delete(m.sessions, id)
}

// Count 获取会话数量
func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return len(m.sessions)
}

// ListByUser 按创建时间列出指定用户的全部会话。
func (m *Manager) ListByUser(userID string) []*Session {
	m.mu.RLock()
	defer m.mu.RUnlock()

	sessions := make([]*Session, 0)
	for _, sess := range m.sessions {
		if sess.UserID == userID {
			sessions = append(sessions, sess)
		}
	}

	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].CreatedAt.Equal(sessions[j].CreatedAt) {
			return sessions[i].ID < sessions[j].ID
		}
		return sessions[i].CreatedAt.Before(sessions[j].CreatedAt)
	})

	return sessions
}

// CleanExpired 清理过期会话
func (m *Manager) CleanExpired() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cleanExpiredLocked()
}

// cleanExpiredLocked 清理过期会话（内部方法，已持有锁）
func (m *Manager) cleanExpiredLocked() int {
	now := time.Now()
	timeout := time.Duration(m.config.Timeout) * time.Second
	expired := 0

	for id, session := range m.sessions {
		if now.Sub(session.UpdatedAt) > timeout {
			if activeID, ok := m.activeByUser[session.UserID]; ok && activeID == id {
				delete(m.activeByUser, session.UserID)
			}
			delete(m.sessions, id)
			expired++

			// 从存储中删除
			if m.storagePath != "" {
				m.deleteSessionStorage(id)
			}
		}
	}

	return expired
}

// saveSessionLocked 保存会话到存储（内部方法，已持有锁）
func (m *Manager) saveSessionLocked(session *Session) {
	if m.storagePath == "" {
		return
	}

	data, err := session.ToJSON()
	if err != nil {
		return
	}

	// 这里简化实现，实际应该使用文件系统或数据库
	// 由于没有文件系统权限，这里只是占位实现
	_ = data
}

// deleteSessionStorage 从存储中删除会话
func (m *Manager) deleteSessionStorage(id string) {
	if m.storagePath == "" {
		return
	}

	// 这里简化实现，实际应该使用文件系统或数据库
	_ = id
}

// loadSessions 从存储加载会话
func (m *Manager) loadSessions() {
	if m.storagePath == "" {
		return
	}

	// 这里简化实现，实际应该从文件系统或数据库加载
	// 由于没有文件系统权限，这里只是占位实现
}
