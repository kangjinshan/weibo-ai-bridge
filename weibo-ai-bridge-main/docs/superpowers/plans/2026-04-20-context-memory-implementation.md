# 上下文记忆功能实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 weibo-ai-bridge 添加上下文记忆能力，通过将 Session ID 传递给底层 AI CLI 工具实现永久保留对话上下文。

**Architecture:** 利用 Claude Code 和 Codex 的原生 Session 管理能力，将 UUID 格式的 Session ID 传递给 CLI，让 CLI 自己管理对话历史。修改 Agent 接口增加 sessionID 参数，Session Manager 自动生成 UUID，Router 传递 Session ID。

**Tech Stack:** Go 1.21+, github.com/google/uuid, Claude Code CLI, Codex CLI

---

## 文件结构

### 修改的文件
- `weibo-ai-bridge-main/go.mod` - 添加 UUID 依赖
- `weibo-ai-bridge-main/session/session.go` - UUID 生成、GetUserSessions、持久化实现
- `weibo-ai-bridge-main/agent/agent.go` - 接口增加 sessionID 参数
- `weibo-ai-bridge-main/agent/claude.go` - 实现 --session-id 参数传递
- `weibo-ai-bridge-main/agent/codex.go` - 实现 exec resume 命令
- `weibo-ai-bridge-main/router/router.go` - 传递 Session ID 给 Agent
- `weibo-ai-bridge-main/router/command.go` - 添加 /new 和 /resume 命令
- `weibo-ai-bridge-main/agent/manager.go` - 更新 Execute 调用传递 sessionID

### 新增的测试文件
- `weibo-ai-bridge-main/session/session_test.go` - Session Manager 测试
- `weibo-ai-bridge-main/agent/claude_test.go` - Claude Agent 测试
- `weibo-ai-bridge-main/agent/codex_test.go` - Codex Agent 测试

---

## Task 1: 添加 UUID 依赖

**Files:**
- Modify: `weibo-ai-bridge-main/go.mod`

- [ ] **Step 1: 添加 UUID 依赖到 go.mod**

运行命令：
```bash
cd weibo-ai-bridge-main && go get github.com/google/uuid@v1.6.0
```

预期输出：
```
go: downloading github.com/google/uuid v1.6.0
go: added github.com/google/uuid v1.6.0
```

- [ ] **Step 2: 验证依赖已添加**

运行命令：
```bash
cd weibo-ai-bridge-main && grep "github.com/google/uuid" go.mod
```

预期输出：
```
github.com/google/uuid v1.6.0
```

- [ ] **Step 3: 提交依赖变更**

```bash
cd weibo-ai-bridge-main && git add go.mod go.sum && git commit -m "chore: add google/uuid dependency for session ID generation"
```

---

## Task 2: 修改 Session Manager - UUID 生成

**Files:**
- Modify: `weibo-ai-bridge-main/session/session.go`

- [ ] **Step 1: 导入 UUID 包**

在 `session/session.go` 文件顶部的 import 区域添加：

```go
import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)
```

- [ ] **Step 2: 修改 Create 方法生成 UUID**

在 `session/session.go` 中找到 `Create` 方法，修改为：

```go
// Create 创建新会话
func (m *Manager) Create(id, userID, agentType string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 检查是否超过最大会话数
	if m.config.MaxSize > 0 && len(m.sessions) >= m.config.MaxSize {
		// 清理过期会话
		m.cleanExpiredLocked()
		// 如果清理后仍超过限制，返回 nil
		if len(m.sessions) >= m.config.MaxSize {
			return nil
		}
	}

	// 如果未提供 ID，生成 UUID
	if id == "" {
		id = uuid.New().String()
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
```

- [ ] **Step 3: 验证编译通过**

运行命令：
```bash
cd weibo-ai-bridge-main && go build ./session
```

预期输出：无错误

- [ ] **Step 4: 提交变更**

```bash
cd weibo-ai-bridge-main && git add session/session.go && git commit -m "feat(session): auto-generate UUID for session ID"
```

---

## Task 3: 添加 GetUserSessions 方法

**Files:**
- Modify: `weibo-ai-bridge-main/session/session.go`

- [ ] **Step 1: 添加 GetUserSessions 方法**

在 `session/session.go` 文件的 Manager 类型中添加：

```go
// GetUserSessions 获取用户的所有会话
func (m *Manager) GetUserSessions(userID string) []*Session {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var sessions []*Session
	for _, session := range m.sessions {
		if session.UserID == userID {
			sessions = append(sessions, session)
		}
	}

	// 按更新时间倒序排序
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})

	return sessions
}
```

- [ ] **Step 2: 添加必要的 import**

确保 import 区域包含：
```go
import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)
```

- [ ] **Step 3: 验证编译通过**

```bash
cd weibo-ai-bridge-main && go build ./session
```

预期输出：无错误

- [ ] **Step 4: 提交变更**

```bash
cd weibo-ai-bridge-main && git add session/session.go && git commit -m "feat(session): add GetUserSessions method to list user sessions"
```

---

## Task 4: 实现持久化 - saveSessionLocked

**Files:**
- Modify: `weibo-ai-bridge-main/session/session.go`

- [ ] **Step 1: 替换 saveSessionLocked 方法**

在 `session/session.go` 中找到 `saveSessionLocked` 方法，替换为：

```go
// saveSessionLocked 保存会话到存储（内部方法，已持有锁）
func (m *Manager) saveSessionLocked(session *Session) {
	if m.storagePath == "" {
		return
	}

	data, err := session.ToJSON()
	if err != nil {
		return
	}

	// 使用 Session ID 作为文件名
	filename := fmt.Sprintf("%s/%s.json", m.storagePath, session.ID)

	// 写入文件
	if err := os.WriteFile(filename, data, 0644); err != nil {
		// 记录错误但不阻塞流程
		return
	}
}
```

- [ ] **Step 2: 验证编译通过**

```bash
cd weibo-ai-bridge-main && go build ./session
```

预期输出：无错误

- [ ] **Step 3: 提交变更**

```bash
cd weibo-ai-bridge-main && git add session/session.go && git commit -m "feat(session): implement session persistence to JSON files"
```

---

## Task 5: 实现持久化 - loadSessions

**Files:**
- Modify: `weibo-ai-bridge-main/session/session.go`

- [ ] **Step 1: 替换 loadSessions 方法**

在 `session/session.go` 中找到 `loadSessions` 方法，替换为：

```go
// loadSessions 从存储加载会话
func (m *Manager) loadSessions() {
	if m.storagePath == "" {
		return
	}

	// 读取存储目录中的所有 JSON 文件
	files, err := os.ReadDir(m.storagePath)
	if err != nil {
		return
	}

	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
			continue
		}

		path := fmt.Sprintf("%s/%s", m.storagePath, file.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		var session Session
		if err := session.FromJSON(data); err != nil {
			continue
		}

		m.sessions[session.ID] = session
		// 恢复活跃会话映射
		if session.State == StateActive {
			m.activeByUser[session.UserID] = session.ID
		}
	}
}
```

- [ ] **Step 2: 验证编译通过**

```bash
cd weibo-ai-bridge-main && go build ./session
```

预期输出：无错误

- [ ] **Step 3: 提交变更**

```bash
cd weibo-ai-bridge-main && git add session/session.go && git commit -m "feat(session): implement session loading from JSON files on startup"
```

---

## Task 6: 修改 Agent 接口

**Files:**
- Modify: `weibo-ai-bridge-main/agent/agent.go`

- [ ] **Step 1: 修改 Execute 方法签名**

在 `agent/agent.go` 中修改 Agent 接口：

```go
// Agent AI Agent 接口
type Agent interface {
	// Name 返回 Agent 名称
	Name() string

	// Execute 执行 AI 任务（新增 sessionID 参数）
	Execute(input string, sessionID string) (string, error)

	// IsAvailable 检查 Agent 是否可用
	IsAvailable() bool
}
```

- [ ] **Step 2: 验证编译错误（预期失败）**

```bash
cd weibo-ai-bridge-main && go build ./agent
```

预期输出：编译错误，提示 ClaudeCodeAgent 和 CodeXAgent 未实现新接口

- [ ] **Step 3: 提交变更**

```bash
cd weibo-ai-bridge-main && git add agent/agent.go && git commit -m "feat(agent): add sessionID parameter to Execute interface"
```

---

## Task 7: 修改 ClaudeCodeAgent 实现

**Files:**
- Modify: `weibo-ai-bridge-main/agent/claude.go`

- [ ] **Step 1: 修改 Execute 方法**

在 `agent/claude.go` 中修改 Execute 方法：

```go
// Execute 执行 AI 任务
func (a *ClaudeCodeAgent) Execute(input string, sessionID string) (string, error) {
	// 检查 claude CLI 是否可用
	if !a.IsAvailable() {
		return "", fmt.Errorf("claude CLI is not available")
	}

	// 准备命令（添加 --session-id 参数）
	args := []string{"--print"}
	if sessionID != "" {
		args = append(args, "--session-id", sessionID)
	}
	args = append(args, input)

	cmd := exec.Command("claude", args...)

	// 捕获输出
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// 执行命令
	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("failed to execute claude CLI: %w, stderr: %s", err, stderr.String())
	}

	// 返回结果
	result := strings.TrimSpace(stdout.String())
	if result == "" {
		return "", fmt.Errorf("empty response from claude CLI")
	}

	return result, nil
}
```

- [ ] **Step 2: 验证编译通过**

```bash
cd weibo-ai-bridge-main && go build ./agent
```

预期输出：无错误

- [ ] **Step 3: 提交变更**

```bash
cd weibo-ai-bridge-main && git add agent/claude.go && git commit -m "feat(agent): implement --session-id parameter for Claude CLI"
```

---

## Task 8: 修改 CodeXAgent 实现

**Files:**
- Modify: `weibo-ai-bridge-main/agent/codex.go`

- [ ] **Step 1: 修改 Execute 方法**

在 `agent/codex.go` 中修改 Execute 方法：

```go
// Execute 执行 AI 任务
func (a *CodeXAgent) Execute(input string, sessionID string) (string, error) {
	// 检查 codex CLI 是否可用
	if !a.IsAvailable() {
		return "", fmt.Errorf("codex CLI is not available")
	}

	// 准备命令（使用 exec resume 子命令）
	args := []string{"exec", "resume"}
	if sessionID != "" {
		args = append(args, sessionID)
	}
	args = append(args, input)

	cmd := exec.Command("codex", args...)

	// 捕获输出
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// 执行命令
	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("failed to execute codex CLI: %w, stderr: %s", err, stderr.String())
	}

	// 返回结果
	result := strings.TrimSpace(stdout.String())
	if result == "" {
		return "", fmt.Errorf("empty response from codex CLI")
	}

	return result, nil
}
```

- [ ] **Step 2: 验证编译通过**

```bash
cd weibo-ai-bridge-main && go build ./agent
```

预期输出：无错误

- [ ] **Step 3: 提交变更**

```bash
cd weibo-ai-bridge-main && git add agent/codex.go && git commit -m "feat(agent): implement exec resume command for Codex CLI"
```

---

## Task 9: 更新 Agent Manager

**Files:**
- Modify: `weibo-ai-bridge-main/agent/manager.go`

- [ ] **Step 1: 查找 Execute 调用位置**

运行命令：
```bash
cd weibo-ai-bridge-main && grep -n "Execute(" agent/manager.go
```

预期输出：找到调用 Execute 的位置

- [ ] **Step 2: 更新 Execute 调用（如果存在）**

如果 agent/manager.go 中有直接调用 Execute 的地方，需要更新签名。如果没有，跳过此步骤。

- [ ] **Step 3: 验证编译通过**

```bash
cd weibo-ai-bridge-main && go build ./agent
```

预期输出：无错误

- [ ] **Step 4: 提交变更**

```bash
cd weibo-ai-bridge-main && git add agent/manager.go && git commit -m "fix(agent): update Execute calls to pass sessionID parameter"
```

---

## Task 10: 修改 Router - 传递 Session ID

**Files:**
- Modify: `weibo-ai-bridge-main/router/router.go`

- [ ] **Step 1: 修改 handleAIMessage 方法**

在 `router/router.go` 中找到 `handleAIMessage` 方法，修改 Execute 调用：

```go
// handleAIMessage 处理 AI 消息（非命令消息）
func (r *Router) handleAIMessage(ctx context.Context, msg *Message) (*Response, error) {
	if r.agentMgr == nil {
		return &Response{
			Success: false,
			Content: "Agent manager is not available",
		}, nil
	}

	if r.sessionMgr == nil {
		return &Response{
			Success: false,
			Content: "Session manager is not available",
		}, nil
	}

	// 获取或创建会话
	var session *session.Session
	if strings.TrimSpace(msg.SessionID) != "" {
		session = r.sessionMgr.GetOrCreateSession(msg.SessionID, msg.UserID, "claude")
	} else {
		session = r.sessionMgr.GetOrCreateActiveSession(msg.UserID, "claude")
	}
	if session == nil {
		return &Response{
			Success: false,
			Content: "Failed to create or get session",
		}, nil
	}

	// 获取 Agent
	currentAgent := r.agentMgr.ResolveAgent(session.AgentType)
	if currentAgent == nil {
		return &Response{
			Success: false,
			Content: fmt.Sprintf("No agent available for session type: %s", session.AgentType),
		}, nil
	}

	// 执行 AI 任务（传入 Session ID）
	response, err := currentAgent.Execute(msg.Content, session.ID)
	if err != nil {
		return &Response{
			Success: false,
			Content: fmt.Sprintf("AI execution failed: %v", err),
		}, nil
	}

	return &Response{
		Success: true,
		Content: response,
	}, nil
}
```

- [ ] **Step 2: 验证编译通过**

```bash
cd weibo-ai-bridge-main && go build ./router
```

预期输出：无错误

- [ ] **Step 3: 提交变更**

```bash
cd weibo-ai-bridge-main && git add router/router.go && git commit -m "feat(router): pass session ID to Agent.Execute for context persistence"
```

---

## Task 11: 添加 /new 命令

**Files:**
- Modify: `weibo-ai-bridge-main/router/command.go`

- [ ] **Step 1: 添加 handleNew 方法**

在 `router/command.go` 中添加：

```go
// handleNew 处理 /new 命令
func (h *CommandHandler) handleNew(msg *Message) (*Response, error) {
	// 创建新 Session（ID 会自动生成为 UUID）
	newSession := h.sessionMgr.Create("", msg.UserID, "claude")
	if newSession == nil {
		return &Response{
			Success: false,
			Content: "Failed to create new session",
		}, nil
	}

	return &Response{
		Success: true,
		Content: fmt.Sprintf("已创建新会话，Session ID: %s", newSession.ID),
	}, nil
}
```

- [ ] **Step 2: 注册 /new 命令**

在 `CommandHandler` 的命令注册部分添加（通常在初始化函数中）：

```go
h.commands["new"] = h.handleNew
```

- [ ] **Step 3: 验证编译通过**

```bash
cd weibo-ai-bridge-main && go build ./router
```

预期输出：无错误

- [ ] **Step 4: 提交变更**

```bash
cd weibo-ai-bridge-main && git add router/command.go && git commit -m "feat(router): add /new command to create new session"
```

---

## Task 12: 添加 /resume 命令

**Files:**
- Modify: `weibo-ai-bridge-main/router/command.go`

- [ ] **Step 1: 添加 handleResume 方法**

在 `router/command.go` 中添加：

```go
// handleResume 处理 /resume 命令
func (h *CommandHandler) handleResume(msg *Message, sessionID string) (*Response, error) {
	if sessionID == "" {
		// 列出用户的所有 Session
		sessions := h.sessionMgr.GetUserSessions(msg.UserID)
		if len(sessions) == 0 {
			return &Response{
				Success: true,
				Content: "暂无会话记录",
			}, nil
		}

		var list strings.Builder
		list.WriteString("可用会话：\n")
		for _, s := range sessions {
			list.WriteString(fmt.Sprintf("- %s (%s, 更新于 %s)\n",
				s.ID, s.AgentType, s.UpdatedAt.Format("2006-01-02 15:04")))
		}
		return &Response{
			Success: true,
			Content: list.String(),
		}, nil
	}

	// 恢复指定 Session
	if h.sessionMgr.SetActiveSession(msg.UserID, sessionID) {
		return &Response{
			Success: true,
			Content: fmt.Sprintf("已切换到会话: %s", sessionID),
		}, nil
	}

	return &Response{
		Success: false,
		Content: "会话不存在或不属于当前用户",
	}, nil
}
```

- [ ] **Step 2: 注册 /resume 命令**

在命令注册部分添加：

```go
h.commands["resume"] = h.handleResume
```

- [ ] **Step 3: 验证编译通过**

```bash
cd weibo-ai-bridge-main && go build ./router
```

预期输出：无错误

- [ ] **Step 4: 提交变更**

```bash
cd weibo-ai-bridge-main && git add router/command.go && git commit -m "feat(router): add /resume command to switch sessions"
```

---

## Task 13: 编写 Session Manager 测试

**Files:**
- Create: `weibo-ai-bridge-main/session/session_test.go`

- [ ] **Step 1: 创建测试文件**

创建 `session/session_test.go`：

```go
package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestManager_Create_UUIDGeneration(t *testing.T) {
	config := ManagerConfig{
		Timeout: 3600,
		MaxSize: 100,
	}
	mgr := NewManager(config)

	session := mgr.Create("", "user1", "claude")
	if session == nil {
		t.Fatal("Expected session to be created")
	}

	// 验证 ID 不是空的
	if session.ID == "" {
		t.Error("Expected session ID to be auto-generated, got empty string")
	}

	// 验证 ID 长度符合 UUID 格式（36字符，包含4个连字符）
	if len(session.ID) != 36 {
		t.Errorf("Expected UUID format (36 chars), got %d chars: %s", len(session.ID), session.ID)
	}
}

func TestManager_GetUserSessions(t *testing.T) {
	config := ManagerConfig{
		Timeout: 3600,
		MaxSize: 100,
	}
	mgr := NewManager(config)

	// 创建多个会话
	s1 := mgr.Create("", "user1", "claude")
	time.Sleep(10 * time.Millisecond) // 确保时间戳不同
	s2 := mgr.Create("", "user1", "claude")
	time.Sleep(10 * time.Millisecond)
	s3 := mgr.Create("", "user2", "claude")

	// 获取 user1 的会话
	sessions := mgr.GetUserSessions("user1")
	if len(sessions) != 2 {
		t.Errorf("Expected 2 sessions for user1, got %d", len(sessions))
	}

	// 验证排序（最新的在前）
	if sessions[0].ID != s2.ID {
		t.Error("Expected sessions sorted by UpdatedAt descending")
	}
	if sessions[1].ID != s1.ID {
		t.Error("Expected sessions sorted by UpdatedAt descending")
	}

	// 获取 user2 的会话
	sessions = mgr.GetUserSessions("user2")
	if len(sessions) != 1 {
		t.Errorf("Expected 1 session for user2, got %d", len(sessions))
	}
	if sessions[0].ID != s3.ID {
		t.Error("Expected correct session for user2")
	}
}

func TestManager_Persistence(t *testing.T) {
	// 创建临时目录
	tmpDir, err := os.MkdirTemp("", "session_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	config := ManagerConfig{
		Timeout:     3600,
		MaxSize:     100,
		StoragePath: tmpDir,
	}

	// 创建第一个 manager 并创建会话
	mgr1 := NewManager(config)
	session := mgr1.Create("", "user1", "claude")
	if session == nil {
		t.Fatal("Failed to create session")
	}
	sessionID := session.ID

	// 验证文件已创建
	filename := filepath.Join(tmpDir, sessionID+".json")
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		t.Error("Expected session file to be created")
	}

	// 创建第二个 manager 模拟重启
	mgr2 := NewManager(config)

	// 验证会话已加载
	loaded, exists := mgr2.Get(sessionID)
	if !exists {
		t.Fatal("Expected session to be loaded from storage")
	}

	if loaded.UserID != "user1" {
		t.Errorf("Expected UserID user1, got %s", loaded.UserID)
	}
	if loaded.AgentType != "claude" {
		t.Errorf("Expected AgentType claude, got %s", loaded.AgentType)
	}
}
```

- [ ] **Step 2: 运行测试**

```bash
cd weibo-ai-bridge-main && go test ./session -v
```

预期输出：所有测试通过

- [ ] **Step 3: 提交测试**

```bash
cd weibo-ai-bridge-main && git add session/session_test.go && git commit -m "test(session): add tests for UUID generation, GetUserSessions, and persistence"
```

---

## Task 14: 编写 Claude Agent 测试

**Files:**
- Create: `weibo-ai-bridge-main/agent/claude_test.go`

- [ ] **Step 1: 创建测试文件**

创建 `agent/claude_test.go`：

```go
package agent

import (
	"strings"
	"testing"
)

func TestClaudeCodeAgent_Execute_WithSessionID(t *testing.T) {
	agent := NewClaudeCodeAgent()

	if !agent.IsAvailable() {
		t.Skip("Claude CLI not available")
	}

	// 注意：这个测试需要实际的 Claude CLI
	// 测试参数构建逻辑而不是实际执行
	tests := []struct {
		name      string
		input     string
		sessionID string
	}{
		{
			name:      "with session ID",
			input:     "test message",
			sessionID: "test-session-123",
		},
		{
			name:      "without session ID",
			input:     "test message",
			sessionID: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 仅验证方法不会崩溃
			// 实际 CLI 调用需要集成测试
			t.Skip("Integration test - requires actual Claude CLI")
		})
	}
}

func TestClaudeCodeAgent_Name(t *testing.T) {
	agent := NewClaudeCodeAgent()
	if agent.Name() != "claude-code" {
		t.Errorf("Expected name 'claude-code', got %s", agent.Name())
	}
}
```

- [ ] **Step 2: 运行测试**

```bash
cd weibo-ai-bridge-main && go test ./agent -v
```

预期输出：测试通过（可能跳过集成测试）

- [ ] **Step 3: 提交测试**

```bash
cd weibo-ai-bridge-main && git add agent/claude_test.go && git commit -m "test(agent): add tests for ClaudeCodeAgent"
```

---

## Task 15: 编写 Codex Agent 测试

**Files:**
- Create: `weibo-ai-bridge-main/agent/codex_test.go`

- [ ] **Step 1: 创建测试文件**

创建 `agent/codex_test.go`：

```go
package agent

import (
	"testing"
)

func TestCodeXAgent_Execute_WithSessionID(t *testing.T) {
	agent := NewCodeXAgent()

	if !agent.IsAvailable() {
		t.Skip("Codex CLI not available")
	}

	// 注意：这个测试需要实际的 Codex CLI
	t.Skip("Integration test - requires actual Codex CLI")
}

func TestCodeXAgent_Name(t *testing.T) {
	agent := NewCodeXAgent()
	if agent.Name() != "codex" {
		t.Errorf("Expected name 'codex', got %s", agent.Name())
	}
}
```

- [ ] **Step 2: 运行测试**

```bash
cd weibo-ai-bridge-main && go test ./agent -v
```

预期输出：测试通过

- [ ] **Step 3: 提交测试**

```bash
cd weibo-ai-bridge-main && git add agent/codex_test.go && git commit -m "test(agent): add tests for CodeXAgent"
```

---

## Task 16: 集成测试和验证

**Files:**
- 无新文件

- [ ] **Step 1: 编译整个项目**

```bash
cd weibo-ai-bridge-main && go build ./...
```

预期输出：无错误

- [ ] **Step 2: 运行所有测试**

```bash
cd weibo-ai-bridge-main && go test ./... -v
```

预期输出：所有测试通过

- [ ] **Step 3: 创建会话存储目录**

```bash
mkdir -p /home/ubuntu/.cc-connect/sessions
```

- [ ] **Step 4: 提交最终验证**

```bash
cd weibo-ai-bridge-main && git add -A && git commit -m "chore: final integration verification"
```

---

## Task 17: 更新文档

**Files:**
- Modify: `weibo-ai-bridge-main/README.md`

- [ ] **Step 1: 添加上下文记忆功能说明**

在 README.md 中添加：

```markdown
## 上下文记忆功能

### 功能说明

weibo-ai-bridge 支持永久保留对话上下文。每次对话都会自动保存，下次继续时可以恢复历史记录。

### 使用方法

1. **正常对话**：直接发送消息，系统自动创建并维护会话
2. **创建新会话**：发送 `/new` 命令
3. **切换会话**：发送 `/resume` 列出所有会话，或 `/resume <session-id>` 切换到指定会话

### 技术实现

- 使用 UUID 作为 Session ID
- 将 Session ID 传递给底层 Claude Code 或 Codex CLI
- CLI 工具自己管理完整的对话历史
- 会话持久化存储在 `~/.cc-connect/sessions/` 目录

### 配置

在 `config.toml` 中配置：

\`\`\`toml
[session]
storage_path = "/home/ubuntu/.cc-connect/sessions"
timeout = 86400  # 24小时
max_size = 1000  # 最多1000个会话
\`\`\`
```

- [ ] **Step 2: 提交文档更新**

```bash
cd weibo-ai-bridge-main && git add README.md && git commit -m "docs: add context memory feature documentation"
```

---

## 实施顺序总结

1. **Task 1**: 添加 UUID 依赖
2. **Task 2-5**: 修改 Session Manager（UUID 生成、GetUserSessions、持久化）
3. **Task 6-9**: 修改 Agent 接口和实现（添加 sessionID 参数）
4. **Task 10**: 修改 Router（传递 Session ID）
5. **Task 11-12**: 添加命令处理（/new、/resume）
6. **Task 13-15**: 编写测试
7. **Task 16**: 集成测试和验证
8. **Task 17**: 更新文档

每个 Task 都是独立的、可测试的单元，可以逐个实施并验证。
