package session

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

type storageMetadata struct {
	ActiveByUser map[string]string `json:"active_by_user"`
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
		m.saveMetadataLocked()
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
	m.saveMetadataLocked()
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
		session.UpdatedAt = time.Now()
		m.saveSessionLocked(session)
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
	if m.storagePath != "" {
		m.deleteSessionStorage(id)
		m.saveMetadataLocked()
	}
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

	if expired > 0 {
		m.saveMetadataLocked()
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

	if err := os.MkdirAll(m.storageSessionsDir(), 0o755); err != nil {
		return
	}

	_ = writeFileAtomically(m.sessionStoragePath(session.ID), data, 0o644)
}

// deleteSessionStorage 从存储中删除会话
func (m *Manager) deleteSessionStorage(id string) {
	if m.storagePath == "" {
		return
	}

	_ = os.Remove(m.sessionStoragePath(id))
}

// loadSessions 从存储加载会话
func (m *Manager) loadSessions() {
	if m.storagePath == "" {
		return
	}

	baseDir := normalizeStoragePath(m.storagePath)
	if baseDir == "" {
		return
	}
	m.storagePath = baseDir

	if err := os.MkdirAll(m.storageSessionsDir(), 0o755); err != nil {
		return
	}

	m.loadStoragePath(baseDir, false)

	imported := false
	for _, legacyPath := range legacyStoragePaths(baseDir) {
		if m.loadStoragePath(legacyPath, true) {
			imported = true
		}
	}

	m.restoreMissingActiveSessions()

	if imported {
		for _, sess := range m.sessions {
			m.saveSessionLocked(sess)
		}
		m.saveMetadataLocked()
	}
}

func (m *Manager) loadStoragePath(storagePath string, preserveExisting bool) bool {
	baseDir := normalizeStoragePath(storagePath)
	if baseDir == "" {
		return false
	}

	imported := false
	entries, err := os.ReadDir(filepath.Join(baseDir, "sessions"))
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
				continue
			}

			data, readErr := os.ReadFile(filepath.Join(baseDir, "sessions", entry.Name()))
			if readErr != nil {
				continue
			}

			sess := &Session{}
			if unmarshalErr := sess.FromJSON(data); unmarshalErr != nil {
				continue
			}
			if strings.TrimSpace(sess.ID) == "" || strings.TrimSpace(sess.UserID) == "" {
				continue
			}
			if sess.Context == nil {
				sess.Context = make(map[string]interface{})
			}

			if existing, ok := m.sessions[sess.ID]; ok && preserveExisting {
				if existing != nil {
					continue
				}
			}

			if existing, ok := m.sessions[sess.ID]; !ok || !preserveExisting || existing == nil {
				m.sessions[sess.ID] = sess
				imported = true
			}
		}
	}

	metaData, err := os.ReadFile(filepath.Join(baseDir, "metadata.json"))
	if err == nil {
		var meta storageMetadata
		if unmarshalErr := json.Unmarshal(metaData, &meta); unmarshalErr == nil && meta.ActiveByUser != nil {
			for userID, sessionID := range meta.ActiveByUser {
				sess, ok := m.sessions[sessionID]
				if !ok || sess.UserID != userID {
					continue
				}
				if preserveExisting {
					if _, exists := m.activeByUser[userID]; exists {
						continue
					}
				}
				m.activeByUser[userID] = sessionID
				imported = true
			}
		}
	}

	return imported
}

func (m *Manager) restoreMissingActiveSessions() {
	for _, sess := range m.sessions {
		activeID, ok := m.activeByUser[sess.UserID]
		if !ok {
			m.activeByUser[sess.UserID] = sess.ID
			continue
		}

		current, exists := m.sessions[activeID]
		if !exists || current == nil || current.UserID != sess.UserID {
			m.activeByUser[sess.UserID] = sess.ID
		}
	}
}

func (m *Manager) PersistSession(id string) {
	if m.storagePath == "" {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	session, exists := m.sessions[id]
	if !exists {
		return
	}
	m.saveSessionLocked(session)
}

func (m *Manager) SetSessionAgentType(id, agentType string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, exists := m.sessions[id]
	if !exists {
		return false
	}

	session.SetAgentType(agentType)
	m.saveSessionLocked(session)
	return true
}

func (m *Manager) SetSessionTitleIfEmpty(id, title string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, exists := m.sessions[id]
	if !exists {
		return false
	}

	updated := session.SetTitleIfEmpty(title)
	if updated {
		m.saveSessionLocked(session)
	}
	return updated
}

// AdoptSessionID 将会话 ID 迁移为原生会话 ID。
// 当目标 ID 已存在且属于同一用户时，会合并两条会话并保持目标 ID。
func (m *Manager) AdoptSessionID(oldID, newID string) (*Session, bool) {
	oldID = strings.TrimSpace(oldID)
	newID = strings.TrimSpace(newID)
	if oldID == "" || newID == "" {
		return nil, false
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	sess, exists := m.sessions[oldID]
	if !exists || sess == nil {
		return nil, false
	}

	if oldID == newID {
		return sess, true
	}

	if existing, ok := m.sessions[newID]; ok {
		if existing == nil || existing.UserID != sess.UserID {
			return sess, false
		}

		mergeSessionLocked(existing, sess)
		delete(m.sessions, oldID)
		if activeID, ok := m.activeByUser[sess.UserID]; ok && activeID == oldID {
			m.activeByUser[sess.UserID] = newID
		}

		if m.storagePath != "" {
			m.saveSessionLocked(existing)
			m.deleteSessionStorage(oldID)
			m.saveMetadataLocked()
		}

		return existing, true
	}

	delete(m.sessions, oldID)
	sess.mu.Lock()
	sess.ID = newID
	sess.UpdatedAt = time.Now()
	sess.mu.Unlock()
	m.sessions[newID] = sess
	if activeID, ok := m.activeByUser[sess.UserID]; ok && activeID == oldID {
		m.activeByUser[sess.UserID] = newID
	}

	if m.storagePath != "" {
		m.saveSessionLocked(sess)
		m.deleteSessionStorage(oldID)
		m.saveMetadataLocked()
	}

	return sess, true
}

func mergeSessionLocked(dst, src *Session) {
	if dst == nil || src == nil {
		return
	}

	dst.mu.Lock()
	defer dst.mu.Unlock()

	src.mu.RLock()
	defer src.mu.RUnlock()

	if strings.TrimSpace(dst.Title) == "" && strings.TrimSpace(src.Title) != "" {
		dst.Title = src.Title
	}

	if dst.Context == nil {
		dst.Context = make(map[string]interface{})
	}
	for key, value := range src.Context {
		if _, exists := dst.Context[key]; !exists {
			dst.Context[key] = value
		}
	}

	if dst.CreatedAt.IsZero() || (!src.CreatedAt.IsZero() && src.CreatedAt.Before(dst.CreatedAt)) {
		dst.CreatedAt = src.CreatedAt
	}
	if src.UpdatedAt.After(dst.UpdatedAt) {
		dst.UpdatedAt = src.UpdatedAt
	}

	if strings.TrimSpace(dst.AgentType) == "" && strings.TrimSpace(src.AgentType) != "" {
		dst.AgentType = src.AgentType
	}
	if dst.State == "" && src.State != "" {
		dst.State = src.State
	}
}

func (m *Manager) saveMetadataLocked() {
	if m.storagePath == "" {
		return
	}

	if err := os.MkdirAll(m.storagePath, 0o755); err != nil {
		return
	}

	data, err := json.Marshal(storageMetadata{ActiveByUser: m.activeByUser})
	if err != nil {
		return
	}

	_ = writeFileAtomically(m.metadataStoragePath(), data, 0o644)
}

func (m *Manager) storageSessionsDir() string {
	return filepath.Join(normalizeStoragePath(m.storagePath), "sessions")
}

func (m *Manager) metadataStoragePath() string {
	return filepath.Join(normalizeStoragePath(m.storagePath), "metadata.json")
}

func (m *Manager) sessionStoragePath(id string) string {
	encoded := base64.RawURLEncoding.EncodeToString([]byte(id))
	return filepath.Join(m.storageSessionsDir(), encoded+".json")
}

func normalizeStoragePath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	if trimmed == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	if strings.HasPrefix(trimmed, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(trimmed, "~/"))
		}
	}
	return filepath.Clean(trimmed)
}

func legacyStoragePaths(currentStoragePath string) []string {
	current := normalizeStoragePath(currentStoragePath)
	seen := map[string]struct{}{}
	paths := make([]string, 0, 2)

	add := func(path string) {
		normalized := normalizeStoragePath(path)
		if normalized == "" || normalized == current {
			return
		}
		if _, exists := seen[normalized]; exists {
			return
		}
		seen[normalized] = struct{}{}
		paths = append(paths, normalized)
	}

	if cwd, err := os.Getwd(); err == nil {
		add(filepath.Join(cwd, "data", "sessions"))
	}

	if exePath, err := os.Executable(); err == nil {
		add(filepath.Join(filepath.Dir(exePath), "data", "sessions"))
	}

	return paths
}

func writeFileAtomically(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}

	tmpName := tmp.Name()
	cleanup := func() {
		_ = os.Remove(tmpName)
	}

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}
	return nil
}
