# 上下文记忆功能设计文档

## 概述

为 weibo-ai-bridge 项目添加上下文记忆能力，通过将 Session ID 传递给底层 AI CLI 工具（Claude Code 和 Codex），实现永久保留对话上下文。

## 问题分析

### 当前问题

1. **Agent 接口缺陷**：`Execute(input string)` 只接受单个输入，无法传递会话上下文
2. **Session ID 未使用**：虽然 Session 有 ID，但从未传递给 Agent
3. **每次对话独立**：每次调用 `claude --print "消息"` 都是新会话，无历史记录

### 根本原因

Claude Code 和 Codex CLI 都支持通过 Session ID 恢复对话，但当前实现没有利用这个能力。

## 解决方案

### 核心思路

利用 Claude Code 和 Codex 的原生 Session 管理能力：
- **Claude Code**: `claude --session-id <uuid> --print "消息"`
- **Codex**: `codex exec resume <uuid> "消息"`

将 weibo-ai-bridge 的 Session ID（UUID 格式）传递给底层 CLI，让 CLI 自己管理完整的对话历史和上下文。

## 架构设计

### 1. Session ID 管理

**Session 创建规则：**
- Session ID 使用 UUID 格式（而不是直接用 UserID）
- 首次用户发送消息时，创建新 Session，ID 自动生成为 UUID
- 同一用户后续消息复用同一个 Session（通过 `activeByUser` 映射）
- 用户可以通过命令 `/new` 创建新 Session
- 用户可以通过命令 `/resume <session-id>` 恢复历史 Session

**Session ID 生成：**
```go
import "github.com/google/uuid"

func (m *Manager) Create(id, userID, agentType string) *Session {
    if id == "" {
        id = uuid.New().String()
    }
    // ... 其余逻辑
}
```

### 2. Agent 接口修改

**接口定义：**
```go
type Agent interface {
    Name() string
    Execute(input string, sessionID string) (string, error)  // 新增 sessionID 参数
    IsAvailable() bool
}
```

**ClaudeCodeAgent 实现：**
```go
func (a *ClaudeCodeAgent) Execute(input string, sessionID string) (string, error) {
    if !a.IsAvailable() {
        return "", fmt.Errorf("claude CLI is not available")
    }

    args := []string{"--print"}
    if sessionID != "" {
        args = append(args, "--session-id", sessionID)
    }
    args = append(args, input)

    cmd := exec.Command("claude", args...)

    var stdout, stderr bytes.Buffer
    cmd.Stdout = &stdout
    cmd.Stderr = &stderr

    err := cmd.Run()
    if err != nil {
        return "", fmt.Errorf("failed to execute claude CLI: %w, stderr: %s", err, stderr.String())
    }

    result := strings.TrimSpace(stdout.String())
    if result == "" {
        return "", fmt.Errorf("empty response from claude CLI")
    }

    return result, nil
}
```

**CodeXAgent 实现：**
```go
func (a *CodeXAgent) Execute(input string, sessionID string) (string, error) {
    if !a.IsAvailable() {
        return "", fmt.Errorf("codex CLI is not available")
    }

    args := []string{"exec", "resume"}
    if sessionID != "" {
        args = append(args, sessionID)
    }
    args = append(args, input)

    cmd := exec.Command("codex", args...)

    var stdout, stderr bytes.Buffer
    cmd.Stdout = &stdout
    cmd.Stderr = &stderr

    err := cmd.Run()
    if err != nil {
        return "", fmt.Errorf("failed to execute codex CLI: %w, stderr: %s", err, stderr.String())
    }

    result := strings.TrimSpace(stdout.String())
    if result == "" {
        return "", fmt.Errorf("empty response from codex CLI")
    }

    return result, nil
}
```

### 3. Router 层修改

**Router.handleAIMessage 方法：**
```go
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

### 4. 命令扩展

**新增 `/new` 命令：**
```go
func (h *CommandHandler) handleNew(msg *router.Message) (*router.Response, error) {
    // 创建新 Session（ID 会自动生成为 UUID）
    newSession := h.sessionMgr.Create("", msg.UserID, "claude")
    if newSession == nil {
        return &router.Response{
            Success: false,
            Content: "Failed to create new session",
        }, nil
    }

    return &router.Response{
        Success: true,
        Content: fmt.Sprintf("已创建新会话，Session ID: %s", newSession.ID),
    }, nil
}
```

**新增 `/resume` 命令：**
```go
func (h *CommandHandler) handleResume(msg *router.Message, sessionID string) (*router.Response, error) {
    if sessionID == "" {
        // 列出用户的所有 Session
        sessions := h.sessionMgr.GetUserSessions(msg.UserID)
        if len(sessions) == 0 {
            return &router.Response{
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
        return &router.Response{
            Success: true,
            Content: list.String(),
        }, nil
    }

    // 恢复指定 Session
    if h.sessionMgr.SetActiveSession(msg.UserID, sessionID) {
        return &router.Response{
            Success: true,
            Content: fmt.Sprintf("已切换到会话: %s", sessionID),
        }, nil
    }

    return &router.Response{
        Success: false,
        Content: "会话不存在或不属于当前用户",
    }, nil
}
```

### 5. Session Manager 扩展

**新增方法：**
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

**修改 Create 方法：**
```go
func (m *Manager) Create(id, userID, agentType string) *Session {
    m.mu.Lock()
    defer m.mu.Unlock()

    if m.config.MaxSize > 0 && len(m.sessions) >= m.config.MaxSize {
        m.cleanExpiredLocked()
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

    if m.storagePath != "" {
        m.saveSessionLocked(session)
    }

    return session
}
```

### 6. 数据持久化

**Session 持久化：**
```go
// saveSessionLocked 保存会话到存储
func (m *Manager) saveSessionLocked(session *Session) {
    if m.storagePath == "" {
        return
    }

    data, err := session.ToJSON()
    if err != nil {
        return
    }

    filename := fmt.Sprintf("%s/%s.json", m.storagePath, session.ID)

    if err := os.WriteFile(filename, data, 0644); err != nil {
        return
    }
}
```

**启动时加载会话：**
```go
// loadSessions 从存储加载会话
func (m *Manager) loadSessions() {
    if m.storagePath == "" {
        return
    }

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
        if session.State == StateActive {
            m.activeByUser[session.UserID] = session.ID
        }
    }
}
```

## 数据流

### 用户发送消息流程

1. 用户发送消息 → 微博平台接收
2. Router.HandleMessage → 转换消息格式
3. Session Manager → 查找用户的活跃 Session（通过 `activeByUser` 映射）
4. 如果不存在，创建新 Session（UUID 作为 ID）
5. Agent.Execute → 调用时传入 `session.ID`
6. Claude CLI / Codex CLI → 使用 Session ID 恢复上下文
   - Claude: `claude --session-id <uuid> --print "消息"`
   - Codex: `codex exec resume <uuid> "消息"`
7. AI 响应 → 返回给 Router
8. Router.sendReply → 分块发送回复
9. Session 更新 → 更新 `UpdatedAt` 时间并持久化

### 用户切换会话流程

1. 用户发送 `/resume <session-id>`
2. 验证 Session 存在且属于该用户
3. 更新 `activeByUser[userID] = sessionID`
4. 后续消息使用新的 Session ID

## 依赖项

### 新增依赖

```go
import "github.com/google/uuid"
```

需要添加到 `go.mod`:
```
require github.com/google/uuid v1.6.0
```

## 配置项

### Session 存储配置

在 `config.toml` 中配置：
```toml
[session]
storage_path = "/home/ubuntu/.cc-connect/sessions"
timeout = 86400  # 24小时
max_size = 1000  # 最多1000个会话
```

## 测试计划

### 单元测试

1. **Session Manager 测试**
   - 测试 Session 创建（自动生成 UUID）
   - 测试 Session 恢复
   - 测试 `GetUserSessions` 方法
   - 测试持久化和加载

2. **Agent 测试**
   - 测试 ClaudeCodeAgent 的 `--session-id` 参数传递
   - 测试 CodeXAgent 的 `exec resume` 命令构建

3. **Router 测试**
   - 测试消息处理时 Session ID 传递
   - 测试 `/new` 和 `/resume` 命令

### 集成测试

1. 发送多条消息，验证上下文保留
2. 使用 `/new` 创建新会话，验证独立性
3. 使用 `/resume` 切换会话，验证历史恢复
4. 重启服务，验证 Session 持久化

## 风险和限制

### 风险

1. **Codex 新会话处理**：Codex 的 `exec resume` 可能要求 Session 必须已存在
   - 缓解措施：首次调用时不传 sessionID，让 Codex 创建新会话，然后记录返回的 Session ID

2. **Session ID 冲突**：UUID 碰撞概率极低，但理论上存在
   - 缓解措施：使用标准 UUID 库，碰撞概率可忽略

3. **存储空间**：永久保留会话可能占用大量存储
   - 缓解措施：可配置 `max_size` 和清理策略

### 限制

1. **上下文窗口**：虽然永久保留，但受 AI 模型的上下文窗口限制
2. **跨 Agent 切换**：不同 Agent（Claude vs Codex）的 Session 不共享

## 实现顺序

1. 添加 `github.com/google/uuid` 依赖
2. 修改 Session Manager（UUID 生成、GetUserSessions、持久化）
3. 修改 Agent 接口和实现（添加 sessionID 参数）
4. 修改 Router（传递 Session ID）
5. 添加命令处理（`/new`、`/resume`）
6. 编写测试
7. 更新文档

## 后续优化

1. 添加 `/delete <session-id>` 命令删除会话
2. 添加会话标签功能，方便用户识别
3. 添加会话搜索功能
4. 实现会话自动清理策略（基于时间和数量）
