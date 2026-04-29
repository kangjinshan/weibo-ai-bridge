# Weibo AI Bridge

微博私信与 AI Agent 的桥接服务。通过微博开放平台 WebSocket API 连接微博和多个 AI Agent（Claude Code、Codex CLI），实现智能对话功能。

## 核心特性

- **微博私信桥接** — 通过 WebSocket API 实时收发微博私信
- **多 Agent 支持** — Claude Code 和 Codex CLI，可灵活切换
- **流式回复** — 先发"正在处理中"提示，再逐片流式发送真实回复
- **会话管理** — 多会话创建/切换/持久化，自动记录会话标题
- **自动建会话** — 无活跃会话时发首条消息自动创建
- **审批回复** — Agent 请求授权时回复 `允许` / `允许所有` / `取消`
- **交互式插话** — `/btw` 向正在执行的 Agent turn 注入补充信息
- **命令旁路** — `/help`、`/status` 等命令立即执行，不排队
- **内置微博 Skills** — 仓库自带 `weibo-skill-api`，安装时同步到 Agent skills 目录
- **SSE 调试出口** — `/chat/stream` 接口可观察内部事件流

## 快速开始

### 前置要求

- Go 1.22+
- 至少安装一个 Agent CLI（`claude` 或 `codex`）

### 安装运行

```bash
# 克隆
git clone https://github.com/kangjinshan/weibo-ai-bridge.git
cd weibo-ai-bridge

# 配置
cp .env.example .env
# 编辑 .env 填入微博 App ID / App secret

# 使用预编译二进制（Linux x86_64）
chmod +x ./server && ./server

# 或自行构建
make build && ./build/weibo-ai-bridge

# 开发模式
make dev
```

### 常用命令

| 命令 | 说明 |
|------|------|
| `make build` | 构建到 `build/weibo-ai-bridge` |
| `make build-linux` | 交叉编译 Linux AMD64 |
| `make test` | 运行测试（含覆盖率） |
| `make test-coverage` | 生成 HTML 覆盖率报告 |
| `make fmt` | 格式化代码 |
| `make lint` | 代码检查（需 golangci-lint） |
| `make dev` | 构建并运行 |

## 用户命令

| 命令 | 说明 |
|------|------|
| `/help` | 显示帮助信息 |
| `/new [claude\|codex]` | 创建新会话（不传参数时沿用当前 Agent） |
| `/list` | 查看所有会话（带编号） |
| `/switch <编号>` | 按 `/list` 中的编号切换活跃会话 |
| `/switch <agent类型>` | 切换当前会话的 Agent 类型 |
| `/btw <内容>` | 向当前交互式会话注入补充信息 |
| `/model` | 显示当前使用的模型 |
| `/dir` | 显示当前工作目录 |
| `/status` | 显示当前会话状态 |

### 授权回复

当 Agent 请求授权时，直接回复以下任意词汇：

| 类别 | 支持的回复 |
|------|-----------|
| 允许 | 允许 / 同意 / 可以 / 好 / 好的 / 是 / 确认 / approve / allow / yes / y / ok |
| 取消 | 取消 / 拒绝 / 不允许 / 不行 / 不 / 否 / deny / no / n / reject / cancel |
| 允许所有 | 允许所有 / 允许全部 / 全部允许 / 所有允许 / 都允许 / 全部同意 / allow all / allowall / approve all / yes all |

`允许所有` 仅对当前会话生效，后续授权自动通过。

## 配置

优先级：环境变量 > TOML 配置文件 > 默认值

### 关键环境变量

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `WEIBO_APP_ID` | 微博应用 ID | 必填 |
| `WEIBO_APP_SECRET` | 微博应用密钥（兼容旧名 `WEIBO_APP_Secret`） | 必填 |
| `SERVER_PORT` | HTTP 端口 | 5533 |
| `CONFIG_PATH` | TOML 配置路径 | `config/config.toml` |
| `CLAUDE_ENABLED` | 启用 Claude | true |
| `CODEX_ENABLED` | 启用 Codex | false |
| `CODEX_MODEL` | Codex 模型覆盖（留空沿用本机 CLI 默认） | 空 |
| `SESSION_TIMEOUT` | 会话超时（秒） | 3600 |
| `SESSION_MAX_SIZE` | 最大会话数 | 1000 |
| `SESSION_STORAGE_PATH` | 会话存储路径 | `~/.config/weibo-ai-bridge/sessions` |
| `LOG_LEVEL` | 日志级别（debug/info/warn/error） | info |
| `LOG_FORMAT` | 日志格式（json/text） | json |
| `LOG_OUTPUT` | 日志输出（stdout/stderr/文件路径） | stdout |

### TOML 配置文件

默认路径 `config/config.toml`，可通过 `CONFIG_PATH` 指定仓库外配置文件。示例见 `config/config.example.toml`。

```toml
[platform.weibo]
app_id = "your-app-id"
app_secret = "your-app-secret"

[agent.claude]
enabled = true

[agent.codex]
enabled = false
model = ""  # 留空沿用本机 codex CLI 默认配置

[session]
timeout = 3600
max_size = 1000
storage_path = "~/.config/weibo-ai-bridge/sessions"

[log]
level = "info"
format = "json"
output = "stdout"
```

**注意**：
- Claude API Key 和模型配置由 Claude Code CLI 管理，不在此配置文件中
- `agent.codex.model` 建议留空，让 Bridge 沿用本机 CLI 的默认配置；手动指定时需确认目标 provider 上存在对应 deployment
- 如果报 `404 deployment does not exist`，通常不是启动方式问题，而是模型名和 provider 配置不匹配

## 会话管理

### 核心特性

- **持久化存储** — 会话数据默认存储在 `~/.config/weibo-ai-bridge/sessions/`，服务重启后自动恢复
- **多会话支持** — 每个用户可创建多个独立会话，按编号切换
- **自动建会话** — 无活跃会话时发送第一条消息自动创建
- **会话标题** — 自动记录首条真实问题作为标题（最长 50 字符）
- **旧路径迁移** — 新版本首次启动时会自动导入旧版 `data/sessions/` 的数据

### Agent Session ID

- **Claude** — 使用 `--output-format stream-json` 流式路径，首轮提取 `session_id`，后续用 `--resume` 继续对话
- **Codex** — 优先通过 `codex app-server` 获取 `item/agentMessage/delta` 流式增量；不可用时回退到 `codex exec --json`。Bridge 把 `thread_id` 持久化到 `codex_session_id`。续接已存在线程时使用最小 `thread/resume` 参数（不覆盖原线程策略），并在运行中同步 `threadId` 变化，确保持续续写同一线程

### 使用示例

```
用户: 帮我看看这个 Go 项目怎么拆模块
Bot: 已收到消息，正在处理中，请稍候。
Bot: <随后流式返回真实回复>
```

```
用户: /list
Bot: Sessions:
     【1】帮我看看这个 Go 项目怎么拆模块 (id=1639733600-1, agent=codex, active)
     【2】未命名会话 (id=1639733600-2, agent=codex)

用户: /switch 1
Bot: Switched to session 1: 帮我看看这个 Go 项目怎么拆模块
```

```
用户: /btw 顺便检查一下 router 和 session 层的边界
Bot: <注入当前进行中的 Agent turn，继续处理>
```

```
用户: 允许所有
Bot: 授权成功，这对话内将不再需要再次授权。
```

## HTTP 接口

| 接口 | 方法 | 说明 |
|------|------|------|
| `/health` | GET | 健康检查 |
| `/stats` | GET | 统计信息 |
| `/chat/stream` | GET/POST | SSE 调试流 |

### `/chat/stream`

GET 请求：`/chat/stream?user_id=<user>&content=<urlencoded-content>&session_id=<optional>`

POST 请求：
```json
{
  "user_id": "123456",
  "content": "请用中文写三段文字",
  "session_id": "optional-session-id"
}
```

返回 `text/event-stream`，事件类型：

| 事件 | 说明 |
|------|------|
| `session` | Agent 会话 ID |
| `delta` | 流式正文增量 |
| `message` | 完整消息 |
| `approval` | 审批请求 |
| `tool_start` | 工具调用开始 |
| `tool_end` | 工具调用结束 |
| `error` | 执行错误 |
| `done` | 本轮结束 |

## 部署

### systemd

```bash
# 修改 deploy/weibo-ai-bridge.service 中的 User/WorkingDirectory/ExecStart/PATH
sudo cp deploy/weibo-ai-bridge.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now weibo-ai-bridge.service
```

常用命令：
```bash
sudo systemctl status weibo-ai-bridge.service
sudo systemctl restart weibo-ai-bridge.service
journalctl -u weibo-ai-bridge.service -f
```

说明：
- 预编译二进制可直接指向仓库根目录的 `./server`
- service 模板从 `CONFIG_PATH` 读取 TOML 配置，额外读取 `.env`
- `Restart=always` + `RestartSec=5` 会在异常退出后自动重启

### 安装内置微博 Skills

```bash
bash scripts/install-skills.sh
```

安装到 `~/.codex/skills/weibo-skill-api` 和 `~/.claude/skills/weibo-skill-api`，自动复用 bridge 的微博配置与 token 缓存。

## 微博凭证获取

1. 在微博私信中找到"微博龙虾助手"，发送"连接龙虾"
2. 获取 App ID 和 App secret
3. 填入 `.env` 或 `config/config.toml`

安全建议：不要将 App secret 提交到代码仓库；定期更换凭证；使用环境变量管理敏感信息。

## 故障排除

| 问题 | 解决方法 |
|------|---------|
| 配置验证失败 | 检查 `WEIBO_APP_ID` 和 `WEIBO_APP_SECRET`（兼容 `WEIBO_APP_Secret`） |
| Claude 不可用 | 确认 `claude --version` 可用，API Key 已在 CLI 中配置 |
| Codex 404 deployment | `CODEX_MODEL` 留空，让 Bridge 沿用本机 CLI 默认配置 |
| WebSocket 断连 | 检查网络、Token 是否过期、心跳配置 |
| 会话丢失 | 检查 `SESSION_TIMEOUT` 和 `SESSION_STORAGE_PATH` |
| 消息处理超时 | 增加超时时间，检查 Agent 服务可用性 |

详细日志：`export LOG_LEVEL="debug"`

## 项目结构

```
weibo-ai-bridge/
├── cmd/server/               # 服务入口
│   └── main.go               # HTTP 服务、消息排队、平台生命周期
├── router/                   # 消息路由
│   ├── router_core.go        # Router 类型、Handle 主入口
│   ├── router_stream.go      # 统一流式路径、forwardStreamToPlatform
│   ├── router_agent.go       # Agent 选择与调用
│   ├── router_interactive.go # 交互式会话管理、liveSessions
│   ├── router_approval.go    # 审批提示与同义词解析
│   ├── router_bytheway.go    # /btw 插话
│   ├── stream_sender.go      # 流式分片发送器、边界感知 flush
│   ├── agent_repair.go       # Agent 可用性自动修复
│   ├── command.go            # 斜杠命令处理
│   └── router_utils.go       # rune 安全切分等辅助函数
├── agent/                    # Agent 抽象层
│   ├── agent.go              # Agent 接口、EventType 定义
│   ├── manager.go            # Agent 注册与解析
│   ├── claude.go             # Claude 流式执行
│   ├── claude_session.go     # Claude 交互式会话 + 审批
│   ├── codex.go              # Codex 流式执行（app-server 优先）
│   ├── codex_interactive_session.go  # Codex 交互式会话 + 审批
│   ├── codex_appserver.go    # Codex app-server 客户端
│   └── prompt.go             # 用户提示包装
├── session/                  # 会话管理与持久化
│   └── session.go            # Session Manager、JSON 持久化
├── config/                   # 配置管理
│   ├── config.go             # TOML + 环境变量加载与校验
│   ├── config.toml           # 默认配置文件
│   └── config.example.toml   # 示例配置文件
├── platform/weibo/           # 微博平台适配
│   ├── client.go             # WebSocket 连接、心跳、分片发送
│   └── message.go            # 消息类型定义与解析
├── skills/weibo-skill-api/   # 内置微博 Skill
├── deploy/                   # systemd service 模板
├── scripts/                  # 安装脚本
├── docs/                     # 设计文档
├── build/                    # 构建产物
├── Makefile                  # 构建脚本
├── go.mod / go.sum           # Go 模块定义
├── .env.example              # 环境变量示例
├── README.md                 # 本文件
└── AGENTS.md                 # 开发协作手册
```

## 许可证

MIT License
