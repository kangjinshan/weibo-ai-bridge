# 微博私信多 AI 平台桥接插件设计文档

## 项目概述

创建一个独立的微博私信插件，能够连接多个 AI 平台（Claude Code 和 CodeX），通过微博私信管理和控制本地的 AI 助手。

**创建时间**: 2026-04-20
**项目路径**: `/home/ubuntu/workspace/weibo-ai-bridge`

## 核心功能

### 1. 多平台连接
- 支持微博私信（基于 WebSocket）
- 支持连接多个 AI Agent（Claude Code、CodeX）
- 统一的消息入口和出口

### 2. 会话管理
- 每个微博用户独立的会话
- 会话持久化存储
- 服务重启后自动恢复会话
- 支持 Agent 切换

### 3. 消息路由
- 智能消息分发
- 支持命令行操作（/switch、/model 等）
- 流式响应支持

### 4. 健壮性
- 自动重连机制
- 错误处理和恢复
- 日志记录

## 系统架构

### 整体架构

```
┌─────────────────────────────────────────────────────┐
│                  weibo-ai-bridge                     │
│                                                      │
│  ┌──────────────┐         ┌──────────────────────┐ │
│  │   Platform   │         │    Agent Manager     │ │
│  │              │         │                      │ │
│  │  Weibo DM    │◄────────┤  ┌────────────────┐  │ │
│  │  (WebSocket) │         │  │  Claude Code   │  │ │
│  │              │         │  │    Agent       │  │ │
│  └──────────────┘         │  └────────────────┘  │ │
│         │                 │  ┌────────────────┐  │ │
│         │                 │  │    CodeX       │  │ │
│         ▼                 │  │    Agent       │  │ │
│  ┌──────────────┐         │  └────────────────┘  │ │
│  │   Session    │         └──────────────────────┘ │
│  │   Manager    │                                   │
│  │              │         ┌──────────────────────┐ │
│  │  - 会话存储   │         │   Message Router    │ │
│  │  - 状态维护   │         │                      │ │
│  └──────────────┘         │  - 消息分发          │ │
│                           │  - Agent 选择        │ │
│                           └──────────────────────┘ │
└─────────────────────────────────────────────────────┘
```

### 核心组件

**1. Platform（平台适配器）**
- 复用 cc-connect 的微博平台实现
- 通过 WebSocket 连接微博私信
- 接收和发送消息

**2. Agent Manager（Agent 管理器）**
- 管理多个 AI Agent 实例（Claude Code、CodeX）
- Agent 生命周期管理
- Agent 选择策略

**3. Session Manager（会话管理器）**
- 维护每个用户的会话状态
- Session ID 与微博用户 ID 映射
- 会话持久化和恢复

**4. Message Router（消息路由器）**
- 接收用户消息
- 选择合适的 Agent 处理
- 将 Agent 响应发回用户

## 详细组件设计

### 1. Platform（平台适配器）

**职责**：
- 管理 WebSocket 连接到微博私信
- 消息收发和解析
- 用户身份验证

**核心结构**：
```go
type WeiboPlatform struct {
    appID          string
    appSecret      string
    wsURL          string

    conn          *websocket.Conn
    messageHandler MessageHandler

    // Token 管理
    token        string
    tokenExpire  time.Time

    // 会话映射
    sessions     map[string]string // userID -> sessionID
}
```

**关键方法**：
- `Start(handler)`: 启动 WebSocket 连接
- `Reply(ctx, replyCtx, content)`: 回复消息
- `Stop()`: 停止连接

**消息格式**：
```json
{
  "type": "message",
  "from_user_id": "1234567890",
  "content": "你好",
  "timestamp": 1234567890
}
```

### 2. Agent Manager（Agent 管理器）

**职责**：
- 管理 Claude Code 和 CodeX 的 Agent 实例
- Agent 健康检查
- Agent 选择策略

**核心结构**：
```go
type AgentManager struct {
    agents      map[string]Agent // name -> Agent
    defaultAgent string

    // Agent 配置
    workDirs    map[string]string // agent -> work_dir
    models      map[string]string // agent -> model
}

type Agent interface {
    Name() string
    Execute(ctx context.Context, input string, sessionID string) (string, error)
    SetWorkDir(dir string)
    SetModel(model string)
}
```

**支持的 Agent**：
1. **Claude Code Agent**
   - 调用 `claude` CLI
   - 支持会话恢复（通过 session 文件）
   - 支持模型切换

2. **CodeX Agent**
   - 调用 `codex` CLI
   - 支持不同模式
   - 支持推理强度调整

**Agent 选择策略**：
- 默认使用配置的 default agent
- 用户可通过命令切换（如 `/use claude`、`/use codex`）
- 支持自动选择（未来扩展）

### 3. Session Manager（会话管理器）

**职责**：
- 会话生命周期管理
- 会话持久化
- 会话恢复

**核心结构**：
```go
type SessionManager struct {
    sessions    map[string]*Session // sessionID -> Session
    userMapping map[string]string   // userID -> sessionID

    dataDir     string // 会话数据存储目录
    mu          sync.RWMutex
}

type Session struct {
    ID           string
    UserID       string
    AgentName    string
    WorkDir      string
    CreatedAt    time.Time
    LastActiveAt time.Time

    // Agent 特定数据
    AgentData    map[string]interface{}
}
```

**会话持久化**：
- 存储路径：`~/.weibo-ai-bridge/sessions/<session_id>.json`
- 格式：JSON
- 内容：用户ID、当前Agent、工作目录、创建时间等

**会话恢复流程**：
```
服务启动
  ↓
加载所有会话文件
  ↓
重建 userMapping
  ↓
用户发送消息
  ↓
查找现有会话
  ├─ 找到 → 恢复会话
  └─ 未找到 → 创建新会话
```

### 4. Message Router（消息路由器）

**职责**：
- 消息解析和预处理
- 命令处理（/开头的消息）
- 消息分发到 Agent

**核心结构**：
```go
type MessageRouter struct {
    agentManager   *AgentManager
    sessionManager *SessionManager
    platform       *WeiboPlatform

    commandHandler *CommandHandler
}
```

**支持的命令**：
```
/new [name]       - 创建新会话
/switch <agent>   - 切换 Agent (claude/codex)
/model <name>     - 切换模型
/dir <path>       - 切换工作目录
/status           - 查看当前会话状态
/help             - 显示帮助
```

**消息处理流程**：
```
接收消息
  ↓
解析消息类型
  ├─ 命令消息 (/开头)
  │    ↓
  │  CommandHandler 处理
  │    ↓
  │  返回结果
  │
  └─ 普通消息
       ↓
     SessionManager 获取/创建会话
       ↓
     AgentManager 选择 Agent
       ↓
     Agent.Execute()
       ↓
     流式响应或完整响应
       ↓
     Platform.Reply()
```

## 数据流设计

### 场景一：用户发送普通消息

```
用户 (微博ID: 123456)
  │
  │ "帮我写一个 Python 脚本"
  ▼
微博服务器
  │ WebSocket 推送
  ▼
WeiboPlatform.messageHandler
  │ 解析消息
  │ {from_user_id: "123456", content: "帮我写..."}
  ▼
MessageRouter.HandleMessage()
  │ 判断：非命令消息
  ▼
SessionManager.GetOrCreateSession("123456")
  │ 查找会话
  │ ├─ 找到：Session{ID: "sess_abc", AgentName: "claude"}
  │ └─ 未找到：创建新会话，默认 agent: "claude"
  ▼
AgentManager.GetAgent("claude")
  │ 获取 ClaudeCodeAgent 实例
  ▼
ClaudeCodeAgent.Execute(ctx, "帮我写一个 Python 脚本", "sess_abc")
  │ 执行：claude --resume sess_abc "帮我写一个 Python 脚本"
  │ 流式输出
  ▼
WeiboPlatform.Reply(ctx, replyCtx, response)
  │ 分块发送（微博 4000 字符限制）
  ▼
用户收到回复
```

### 场景二：用户切换 Agent

```
用户 (微博ID: 123456)
  │
  │ "/switch codex"
  ▼
MessageRouter.HandleMessage()
  │ 判断：命令消息
  ▼
CommandHandler.HandleSwitch("codex")
  │ 验证 Agent 存在
  ▼
SessionManager.UpdateSession("123456", agentName="codex")
  │ 更新会话配置
  ▼
WeiboPlatform.Reply(ctx, replyCtx, "已切换到 CodeX")
  ▼
用户下次消息将使用 CodeX 处理
```

### 场景三：服务重启后会话恢复

```
服务启动
  │
  ▼
SessionManager.LoadSessions()
  │ 读取 ~/.weibo-ai-bridge/sessions/*.json
  │ ├─ sess_abc.json -> {userID: "123456", agent: "claude"}
  │ └─ sess_def.json -> {userID: "789012", agent: "codex"}
  ▼
重建 userMapping
  │ map["123456"] = "sess_abc"
  │ map["789012"] = "sess_def"
  ▼
服务就绪
  │
  │ 用户 123456 发送消息
  ▼
SessionManager.GetSession("123456")
  │ 直接返回：Session{ID: "sess_abc", AgentName: "claude"}
  ▼
继续之前的会话
```

## 错误处理

### 错误分类与处理策略

| 错误类型 | 处理策略 | 用户提示 |
|---------|---------|---------|
| WebSocket 连接失败 | 自动重连（指数退避） | "连接断开，正在重连..." |
| Token 过期 | 自动刷新 | 无感知 |
| Agent 不存在 | 返回错误信息 | "Agent 'xxx' 不存在，可用：claude, codex" |
| Agent 执行失败 | 记录日志，返回错误 | "执行失败：[错误信息]" |
| 会话数据损坏 | 删除损坏数据，创建新会话 | "会话恢复失败，已创建新会话" |
| 微博 API 限流 | 等待后重试 | "请求过于频繁，稍后重试" |
| CLI 工具未安装 | 返回配置错误 | "Claude Code CLI 未安装，请先安装" |

### 重连机制

```go
func (p *WeiboPlatform) reconnect() {
    backoff := 1 * time.Second
    maxBackoff := 60 * time.Second

    for {
        err := p.connect()
        if err == nil {
            return
        }

        slog.Error("reconnect failed", "error", err, "backoff", backoff)
        time.Sleep(backoff)

        backoff *= 2
        if backoff > maxBackoff {
            backoff = maxBackoff
        }
    }
}
```

### 错误响应格式

```
❌ 错误

执行失败：Claude API 调用超时

请稍后重试或使用 /help 查看帮助
```

## 配置设计

### 配置文件

**配置文件**：`~/.weibo-ai-bridge/config.toml`

```toml
# 微博平台配置
[platform]
app_id = "your_app_id"
app_secret = "your_app_secret"
ws_url = "ws://open-im.api.weibo.com/ws/stream"  # 可选，默认值

# Agent 配置
[[agents]]
name = "claude"
type = "claude-code"
enabled = true
work_dir = "/home/ubuntu/workspace"
model = "claude-sonnet-4-6"  # 可选

[[agents]]
name = "codex"
type = "codex"
enabled = true
work_dir = "/home/ubuntu/workspace"
model = "gpt-4"  # 可选
mode = "suggest"  # suggest/auto-edit/full-auto/yolo

# 会话配置
[session]
default_agent = "claude"  # 默认使用的 Agent
max_idle_time = 3600  # 会话最大空闲时间（秒），0 表示永不过期
data_dir = "~/.weibo-ai-bridge/sessions"  # 会话数据存储目录

# 日志配置
[log]
level = "info"  # debug/info/warn/error
file = "~/.weibo-ai-bridge/bridge.log"  # 日志文件路径
```

### 环境变量

```bash
# 也可以通过环境变量配置
export WEIBO_APP_ID="your_app_id"
export WEIBO_APP_Secret="your_app_Secret"
export DEFAULT_AGENT="claude"
```

### 配置优先级

1. 环境变量
2. 配置文件
3. 默认值

## 项目目录结构

```
weibo-ai-bridge/
├── cmd/
│   └── main.go              # 入口文件
├── platform/
│   └── weibo/
│       ├── weibo.go         # 微博平台适配器
│       └── message.go       # 消息解析
├── agent/
│   ├── agent.go             # Agent 接口定义
│   ├── manager.go           # Agent 管理器
│   ├── claude/
│   │   └── claude.go        # Claude Code Agent
│   └── codex/
│       └── codex.go         # CodeX Agent
├── session/
│   ├── manager.go           # 会话管理器
│   └── session.go           # 会话数据结构
├── router/
│   ├── router.go            # 消息路由器
│   └── command.go           # 命令处理器
├── config/
│   ├── config.go            # 配置加载
│   └── config.example.toml  # 配置示例
├── scripts/
│   ├── install.sh           # 安装脚本
│   └── start.sh             # 启动脚本
├── go.mod
├── go.sum
├── Makefile
└── README.md
```

## 安装与配置流程

### 首次安装引导

**目标**：为用户提供友好的安装体验，引导用户完成微博应用配置。

**安装步骤**：

1. **安装检查**
   ```bash
   # 用户下载并运行安装脚本
   curl -fsSL https://raw.githubusercontent.com/your-repo/weibo-ai-bridge/main/scripts/install.sh | bash
   
   # 或从源码编译
   git clone https://github.com/your-repo/weibo-ai-bridge.git
   cd weibo-ai-bridge
   make install
   ```

2. **配置引导**
   ```bash
   # 首次运行时检测配置不存在，进入交互式配置模式
   $ weibo-ai-bridge
   
   🔧 检测到首次运行，开始配置向导...
   
   ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
   步骤 1/4: 微博应用配置
   ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
   
   📱 要使用微博私信功能，需要获取微博应用凭证。
   
   请按以下步骤操作：
   
   1. 打开微博 APP
   2. 搜索并关注 "微博龙虾助手" 官方账号
   3. 向 "微博龙虾助手" 发送消息: "申请开发者凭证"
   4. 等待回复，获取以下信息：
      - APP ID (应用ID)
      - APP Secret (应用密钥)
   
   💡 提示：通常在 1-3 个工作日内审核通过
   
   ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
   
   已获取到凭证？(y/n): y
   
   请输入 APP ID: your_app_id_here
   请输入 APP Secret: your_app_Secret_here
   
   ✅ 微博应用配置完成
   ```

3. **Agent 配置检查**
   ```
   ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
   步骤 2/4: AI Agent 检查
   ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
   
   正在检查 Claude Code CLI...
   ✅ Claude Code 已安装 (版本: 1.2.3)
   
   正在检查 CodeX CLI...
   ❌ CodeX 未安装
   
   是否安装 CodeX? (y/n): y
   正在安装 CodeX...
   ✅ CodeX 安装完成
   
   ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
   ```

4. **工作目录配置**
   ```
   ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
   步骤 3/4: 工作目录配置
   ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
   
   请输入 AI Agent 的工作目录 (默认: ~/workspace): 
   使用默认目录: /home/ubuntu/workspace
   
   ✅ 工作目录配置完成
   ```

5. **配置确认与保存**
   ```
   ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
   步骤 4/4: 配置确认
   ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
   
   配置摘要：
   - 微博 APP ID: your_app_id_here
   - 工作目录: /home/ubuntu/workspace
   - 默认 Agent: claude
   - 日志级别: info
   - 会话存储: ~/.weibo-ai-bridge/sessions
   
   确认保存配置? (y/n): y
   
   ✅ 配置已保存到: ~/.weibo-ai-bridge/config.toml
   ✅ 会话目录已创建: ~/.weibo-ai-bridge/sessions
   ✅ 日志目录已创建: ~/.weibo-ai-bridge/logs
   
   ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
   
   🎉 安装完成！
   
   启动服务：
     weibo-ai-bridge start
   
   查看帮助：
     weibo-ai-bridge --help
   
   微博私信使用：
     1. 打开微博 APP
     2. 给你的机器人账号发送消息
     3. 发送 /help 查看可用命令
   ```

### 配置文件自动生成

安装向导会生成以下配置文件：

**~/.weibo-ai-bridge/config.toml**
```toml
# 微博平台配置
[platform]
app_id = "your_app_id_here"
app_secret = "your_app_Secret_here"
ws_url = "ws://open-im.api.weibo.com/ws/stream"

# Agent 配置
[[agents]]
name = "claude"
type = "claude-code"
enabled = true
work_dir = "/home/ubuntu/workspace"
model = "claude-sonnet-4-6"

[[agents]]
name = "codex"
type = "codex"
enabled = true
work_dir = "/home/ubuntu/workspace"
model = "gpt-4"
mode = "suggest"

# 会话配置
[session]
default_agent = "claude"
max_idle_time = 3600
data_dir = "~/.weibo-ai-bridge/sessions"

# 日志配置
[log]
level = "info"
file = "~/.weibo-ai-bridge/bridge.log"
```

### 安装脚本实现

**scripts/install.sh**
```bash
#!/bin/bash

set -e

echo "🚀 开始安装 weibo-ai-bridge..."

# 检查依赖
check_dependencies() {
    echo "检查系统依赖..."
    
    # 检查 Go
    if ! command -v go &> /dev/null; then
        echo "❌ Go 未安装，请先安装 Go 1.22+"
        exit 1
    fi
    
    echo "✅ Go 已安装: $(go version)"
}

# 编译项目
build_project() {
    echo "编译项目..."
    go build -o weibo-ai-bridge ./cmd/main.go
    echo "✅ 编译完成"
}

# 安装二进制
install_binary() {
    echo "安装二进制文件..."
    sudo mv weibo-ai-bridge /usr/local/bin/
    echo "✅ 已安装到 /usr/local/bin/weibo-ai-bridge"
}

# 创建配置目录
setup_directories() {
    echo "创建配置目录..."
    mkdir -p ~/.weibo-ai-bridge/sessions
    mkdir -p ~/.weibo-ai-bridge/logs
    echo "✅ 目录创建完成"
}

# 主流程
main() {
    check_dependencies
    build_project
    install_binary
    setup_directories
    
    echo ""
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo "✅ 安装完成！"
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo ""
    echo "首次运行配置："
    echo "  weibo-ai-bridge"
    echo ""
    echo "查看帮助："
    echo "  weibo-ai-bridge --help"
    echo ""
}

main "$@"
```

### 安装文档提示

**README.md 中添加**

```markdown
## 安装指南

### 前置要求

1. **Go 1.22+** - 用于编译项目
2. **Claude Code CLI** - AI 编程助手
   ```bash
   npm install -g @anthropic-ai/claude-code
   ```
3. **CodeX CLI** (可选) - OpenAI Codex
   ```bash
   npm install -g @openai/codex
   ```

### 安装步骤

**方式一：使用安装脚本**

```bash
curl -fsSL https://raw.githubusercontent.com/your-repo/weibo-ai-bridge/main/scripts/install.sh | bash
```

**方式二：从源码编译**

```bash
git clone https://github.com/your-repo/weibo-ai-bridge.git
cd weibo-ai-bridge
make install
```

### 首次配置

安装后首次运行会进入配置向导，引导你完成：

1. **微博应用配置** - 需要获取 APP ID 和 APP Secret
   - 打开微博 APP
   - 搜索并关注 **"微博龙虾助手"**
   - 发送消息：`申请开发者凭证`
   - 等待审核通过（1-3 个工作日）

2. **AI Agent 检查** - 自动检测并安装 Claude Code / CodeX

3. **工作目录配置** - 设置 AI Agent 的工作目录

4. **配置确认** - 确认并保存配置

### 启动服务

```bash
# 前台运行（调试模式）
weibo-ai-bridge

# 后台运行
weibo-ai-bridge start

# 使用 systemd 服务
sudo systemctl start weibo-ai-bridge
sudo systemctl enable weibo-ai-bridge  # 开机自启
```

### 验证安装

```bash
# 检查版本
weibo-ai-bridge --version

# 检查配置
weibo-ai-bridge config show

# 测试连接
weibo-ai-bridge test
```
```

## 技术实现

### 依赖项

**必需**：
- Go 1.22+
- gorilla/websocket (WebSocket 连接)
- Claude Code CLI
- CodeX CLI

**可选**：
- systemd 服务文件（用于后台运行）

### 关键技术点

1. **WebSocket 连接管理**
   - 心跳保活
   - 自动重连
   - 消息去重

2. **会话持久化**
   - JSON 格式存储
   - 原子写入
   - 定期清理过期会话

3. **CLI 调用**
   - 流式输出处理
   - 上下文传递
   - 错误捕获

4. **消息分块**
   - 微博私信 4000 字符限制
   - 智能分片（避免截断代码）
   - 分块标记

## 实现步骤

1. **创建项目结构** - 建立目录和基础文件
2. **实现配置模块** - 配置加载和环境变量支持
3. **实现会话管理器** - 会话数据结构和持久化
4. **实现 Agent 接口** - 统一的 Agent 接口定义
5. **实现 Claude Code Agent** - 调用 claude CLI
6. **实现 CodeX Agent** - 调用 codex CLI
7. **实现 Agent 管理器** - Agent 生命周期和选择
8. **实现微博平台适配器** - WebSocket 连接和消息处理
9. **实现消息路由器** - 消息分发和命令处理
10. **实现命令处理器** - 支持的命令逻辑
11. **编写启动脚本** - 安装和启动脚本
12. **测试完整流程** - 端到端测试

## 预期效果

- 用户通过微博私信使用 Claude Code 和 CodeX
- 每个用户独立的会话，互不干扰
- 服务重启后自动恢复会话
- 支持在多个 AI 平台之间切换
- 稳定的 WebSocket 连接和自动重连
- 完善的错误处理和日志记录

## 后续优化

- 支持更多 AI 平台（Gemini、Cursor Agent 等）
- 支持文件传输（图片、代码文件）
- 支持语音消息（STT/TTS）
- 支持定时任务（类似 cc-connect 的 cron）
- 支持多 Agent 协作（bot-to-bot relay）
- 性能优化和缓存机制