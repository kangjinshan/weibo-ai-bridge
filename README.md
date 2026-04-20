# Weibo AI Bridge

微博私信与 AI Agent 的桥接服务，通过微博开放平台 WebSocket API 连接微博和多个 AI Agent（Claude、Codex），实现智能对话功能。

## 项目简介

Weibo AI Bridge 是一个基于 Go 语言开发的中间件服务，旨在连接微博私信和 AI 智能助手。通过微博开放平台的 WebSocket API，本项目能够实时接收微博私信消息，并将其转发给配置的 AI Agent 进行处理，最终将 AI 的回复返回给微博用户。

本项目采用模块化设计，支持多种 AI Agent 接入，具有良好的扩展性和可维护性。主要特点包括：

- 完整的消息收发流程
- 多 Agent 支持（Claude、Codex）
- 会话管理与会话上下文保持
- 异步消息处理
- 完善的测试覆盖
- 灵活的配置系统

## 特性说明

### 核心功能

- **微博私信桥接**: 通过微博开放平台 WebSocket API 实时接收和发送微博私信
- **多 Agent 支持**: 支持 Claude 和 Codex 两种 AI Agent，可灵活切换
- **会话管理**: 自动管理用户会话，保持对话上下文
- **消息路由**: 智能路由消息到对应的 AI Agent
- **命令处理**: 支持切换 Agent、清除会话等命令
- **健康检查**: 提供 HTTP 接口用于健康检查和统计信息

### 技术特性

- **Go 语言实现**: 使用 Go 1.22+ 开发，性能优异
- **模块化架构**: 清晰的模块划分，易于扩展
- **完整测试**: 采用 TDD 开发模式，测试覆盖率高
- **配置灵活**: 支持 TOML 配置文件和环境变量
- **优雅关闭**: 支持优雅关闭，确保消息不丢失
- **自动发现**: 自动检测本地安装的 Agent CLI 工具

## 安装指南

### 前置要求

- Go 1.22 或更高版本
- Git

### 下载源码

```bash
git clone https://github.com/yourusername/weibo-ai-bridge.git
cd weibo-ai-bridge
```

### 安装依赖

```bash
make deps
```

### 构建项目

```bash
make build
```

构建产物位于 `bin/weibo-ai-bridge`

### 运行测试

```bash
# 运行所有测试
make test

# 生成覆盖率报告
make test-coverage
```

### 代码质量检查

```bash
# 格式化代码
make fmt

# 代码检查
make lint
```

## 使用说明

### 快速开始

1. 配置环境变量或配置文件（详见配置说明）
2. 构建项目：`make build`
3. 运行服务：`./bin/weibo-ai-bridge`

### 运行模式

#### 开发模式

```bash
make dev
```

此命令会自动构建并运行服务。

#### 生产模式

```bash
# 构建 Linux 版本
make build-linux

# 运行服务
./bin/weibo-ai-bridge
```

### HTTP 接口

服务启动后，会监听 5533 端口（可通过环境变量 `SERVER_PORT` 修改），提供以下接口：

#### 健康检查

```bash
GET /health
```

返回示例：
```json
{
  "status": "ok",
  "service": "weibo-ai-bridge"
}
```

#### 统计信息

```bash
GET /stats
```

返回示例：
```json
{
  "sessions": {
    "count": 5
  },
  "agents": {
    "count": 2,
    "list": ["claude-code", "codex"]
  },
  "timestamp": 1713597123
}
```

### 用户命令

用户可以在微博私信中发送以下命令：

- `/agent <name>` - 切换到指定的 AI Agent（如：`/agent claude-code`）
- `/clear` - 清除当前会话上下文

## 配置说明

### 配置方式

项目支持三种配置方式，优先级从高到低：

1. 环境变量
2. TOML 配置文件
3. 默认配置

### 环境变量配置

创建 `.env` 文件或直接设置环境变量：

```bash
# 微博平台配置
export WEIBO_APP_ID="your-app-id"
export WEIBO_APP_Secret="your-app-secret"
export WEIBO_TOKEN_URL="http://open-im.api.weibo.com/open/auth/ws_token"
export WEIBO_WS_URL="ws://open-im.api.weibo.com/ws/stream"
export SERVER_PORT="5533"

# Claude Agent 配置
# Claude API Key 和模型配置请在 Claude Code CLI 中设置
# 配置方式：export ANTHROPIC_API_KEY="sk-ant-xxxxx"
# 或编辑 ~/.config/claude/config.json
export CLAUDE_ENABLED="true"

# Codex Agent 配置（可选）
export CODEX_API_KEY="your-codex-api-key"
export CODEX_MODEL="gpt-4"
export CODEX_ENABLED="false"

# 日志配置
export LOG_LEVEL="info"
export LOG_FORMAT="json"
export LOG_OUTPUT="stdout"

# 会话配置
export SESSION_TIMEOUT="3600"
export SESSION_MAX_SIZE="1000"
```

### TOML 配置文件

创建 `config.toml` 文件：

```toml
[platform.weibo]
app_id = "your-app-id"
app_Secret = "your-app-secret"
token_url = "http://open-im.api.weibo.com/open/auth/ws_token"
ws_url = "ws://open-im.api.weibo.com/ws/stream"
server_port = "5533"
timeout = 30

[agent.claude]
# Claude API Key 和模型配置由 Claude Code CLI 管理
# 配置文件位置：~/.config/claude/config.json
enabled = true

[agent.codex]
api_key = "your-codex-api-key"
model = "gpt-4"
enabled = false

[session]
timeout = 3600
max_size = 1000

[log]
level = "info"
format = "json"
output = "stdout"
```

### 配置项说明

#### Platform 配置

| 字段 | 类型 | 说明 | 必填 | 默认值 |
|------|------|------|------|--------|
| `platform.weibo.app_id` | string | 微博应用 ID | 是 | - |
| `platform.weibo.app_Secret` | string | 微博应用密钥 | 是 | - |
| `platform.weibo.token_url` | string | Token 获取 URL | 否 | `http://open-im.api.weibo.com/open/auth/ws_token` |
| `platform.weibo.ws_url` | string | WebSocket 连接 URL | 否 | `ws://open-im.api.weibo.com/ws/stream` |
| `platform.weibo.timeout` | int | HTTP 请求超时时间（秒） | 否 | 30 |
| `server_port` | int | 服务器监听端口 | 否 | 5533 |
| `server_port` | int | 服务器监听端口 | 否 | 5533 |

#### Agent 配置

| 字段 | 类型 | 说明 | 必填 | 默认值 |
|------|------|------|------|--------|
| `agent.claude.enabled` | bool | 是否启用 Claude | 否 | true |

**注意**：Claude API Key 和模型配置由 Claude Code CLI 管理，不在此配置文件中。

| 字段 | 类型 | 说明 | 必填 | 默认值 |
|------|------|------|------|--------|
| `agent.codex.api_key` | string | Codex API Key | 是（当启用时） | - |
| `agent.codex.model` | string | Codex 模型名称 | 否 | `gpt-4` |
| `agent.codex.enabled` | bool | 是否启用 Codex | 否 | false |

#### Session 配置

| 字段 | 类型 | 说明 | 必填 | 默认值 |
|------|------|------|------|--------|
| `session.timeout` | int | 会话超时时间（秒） | 否 | 3600 |
| `session.max_size` | int | 会话最大消息数 | 否 | 1000 |

#### Log 配置

| 字段 | 类型 | 说明 | 必填 | 默认值 |
|------|------|------|------|--------|
| `log.level` | string | 日志级别（debug/info/warn/error） | 否 | info |
| `log.format` | string | 日志格式（json/text） | 否 | json |
| `log.output` | string | 日志输出位置 | 否 | stdout |

## 微博开放平台凭证说明

### 获取凭证步骤

#### 1. 注册微博开发者账号

访问微博开放平台（https://open.weibo.com）注册开发者账号。

#### 2. 创建应用

在微博开放平台创建应用，获取 App ID 和 App Secret。

#### 3. 配置应用

在应用配置中启用 WebSocket 私信功能，配置回调地址。

### 凭证格式说明

- **App ID**: 应用唯一标识，例如：`your-weibo-app-id`
- **App Secret**: 应用密钥，32 位十六进制字符串
- **Token URL**: `http://open-im.api.weibo.com/open/auth/ws_token`
- **WebSocket URL**: `ws://open-im.api.weibo.com/ws/stream`

### WebSocket 连接格式

**Token 获取请求**：
```json
POST http://open-im.api.weibo.com/open/auth/ws_token
Content-Type: application/json

{
  "app_id": "your-weibo-app-id",
  "app_secret": "your-app-secret"
}
```

**Token 获取响应**：
```json
{
  "code": 0,
  "data": {
    "uid": 1639733600,
    "token": "64字符token",
    "expire_in": 7199
  },
  "message": "success"
}
```

**WebSocket 连接 URL**：
```
ws://open-im.api.weibo.com/ws/stream?app_id=your-weibo-app-id&token=your-64-char-token
```

### 安全建议

1. 不要将 App Secret 提交到代码仓库
2. 定期更换凭证
3. 使用环境变量管理敏感信息
4. 在生产环境中启用 HTTPS
5. 监控异常访问

## 故障排除

### 常见问题

#### 1. 服务启动失败：配置验证错误

**错误信息**:
```
Configuration validation failed: platform.weibo.app_id is required
```

**解决方法**:
检查环境变量或配置文件中是否正确设置了 `WEIBO_APP_ID` 和 `WEIBO_APP_Secret`。

#### 2. Agent 初始化失败

**错误信息**:
```
claude.api_key is required when claude agent is enabled
```

**解决方法**:
1. 确保在 Claude Code CLI 中配置了 API Key（环境变量或配置文件）
2. 检查 `claude` 命令是否可用：`claude --version`
3. 查看 Claude Code 配置文档

#### 3. 微博平台连接失败

**错误信息**:
```
Failed to create platform: invalid token URL
```

**解决方法**:
检查 Token URL 格式是否正确，确保以 `https://` 或 `http://` 开头。

#### 4. 消息处理超时

**错误信息**:
```
Failed to handle message: context deadline exceeded
```

**解决方法**:
- 检查网络连接是否正常
- 增加超时时间配置：`platform.weibo.timeout`
- 检查 AI Agent 服务是否可用

#### 5. 会话丢失

**症状**:
AI 无法记住之前的对话内容

**解决方法**:
检查会话配置：
- `session.timeout`: 会话超时时间，默认 3600 秒
- `session.max_size`: 会话最大消息数，默认 1000 条

#### 6. WebSocket 连接断开

**症状**:
频繁出现 WebSocket 连接断开和重连

**解决方法**:
- 检查网络稳定性
- 检查 Token 是否过期
- 增加心跳间隔
- 检查微博 API 限制

### 日志调试

启用调试日志以获取更详细的信息：

```bash
export LOG_LEVEL="debug"
./bin/weibo-ai-bridge
```

### 健康检查

使用健康检查接口验证服务状态：

```bash
curl http://localhost:5533/health
```

### 查看统计信息

使用统计接口查看服务运行状态：

```bash
curl http://localhost:5533/stats
```

### 性能问题

如果遇到性能问题：

1. 检查会话数量：`curl http://localhost:5533/stats`
2. 清理过期会话：发送 `/clear` 命令
3. 调整会话配置：减少 `session.max_size`
4. 增加服务器资源

### 依赖问题

如果遇到依赖问题：

```bash
# 清理并重新下载依赖
make clean
make deps
make tidy
```

### 测试失败

如果测试失败：

```bash
# 查看详细测试输出
go test -v ./...

# 检查特定包的测试
go test -v ./platform/weibo
```

### 获取帮助

如果以上方法都无法解决问题：

1. 查看项目 Issues: https://github.com/yourusername/weibo-ai-bridge/issues
2. 创建新 Issue 并附带：
   - 错误日志
   - 配置信息（隐藏敏感信息）
   - 复现步骤
   - 环境信息（操作系统、Go 版本等）

## 项目结构

```
weibo-ai-bridge/
├── cmd/                  # 应用入口
│   └── server/          # 主服务入口
│       └── main.go      # 服务主程序
├── platform/            # 平台适配器
│   └── weibo/          # 微博平台集成
│       ├── client.go     # WebSocket 连接实现
│       └── message.go    # 消息定义和解析
├── agent/               # AI Agent 集成
│   ├── agent.go         # Agent 接口
│   ├── manager.go       # Agent 管理器
│   ├── claude.go        # Claude Agent
│   └── codex.go         # Codex Agent
├── session/             # 会话管理
│   └── manager.go       # 会话管理实现
├── router/              # 消息路由
│   ├── router.go        # 路由实现
│   └── command.go       # 命令处理
├── config/              # 配置管理
│   └── config.go        # 配置实现
├── scripts/             # 部署和运维脚本
├── bin/                # 编译产物
├── config.toml         # 配置文件
├── config.example.toml  # 示例配置文件
├── go.mod              # Go 模块定义
├── Makefile            # 构建脚本
├── README.md           # 项目文档
├── agents.md           # Agent 配置文档
└── LICENSE             # 许可证文件
```

## 架构设计

### 模块职责

1. **Platform Layer**: 负责与微博 API 交互，接收和发送消息
2. **Agent Layer**: 封装不同 AI Agent 的调用接口，提供统一的 Agent 抽象
3. **Session Layer**: 管理用户会话状态，保持对话上下文
4. **Router Layer**: 消息分发与路由逻辑，处理命令和消息转发
5. **Config Layer**: 配置文件管理与加载，支持多种配置方式

### 数据流

```
微博私信 → WebSocket → Platform → Router → Session Manager
                                         ↓
                                    Agent Manager → AI Agent (Claude/Codex)
                                         ↓
                                    Router → Platform → WebSocket → 微博用户
```

## 开发指南

### 代码格式化

```bash
make fmt
```

### 代码检查

```bash
make lint
```

### 清理构建产物

```bash
make clean
```

### 添加新的 AI Agent

1. 在 `agent/` 目录创建新文件，实现 `Agent` 接口
2. 在 `agent/manager.go` 中注册新 Agent
3. 在配置中添加新 Agent 的配置项
4. 编写测试用例

## 贡献指南

我们欢迎所有形式的贡献！

### 贡献流程

1. Fork 项目
2. 创建功能分支 (`git checkout -b feature/amazing-feature`)
3. 编写代码和测试
4. 确保所有测试通过 (`make test`)
5. 提交更改 (`git commit -m 'Add some amazing feature'`)
6. 推送到分支 (`git push origin feature/amazing-feature`)
7. 创建 Pull Request

### 代码规范

- 遵循 Go 语言官方代码规范
- 为所有公开函数编写文档注释
- 为新功能编写单元测试
- 保持测试覆盖率在 80% 以上

## 许可证

本项目采用 MIT 许可证。详见 [LICENSE](LICENSE) 文件。

## 联系方式

项目维护者：Your Name

问题反馈：https://github.com/yourusername/weibo-ai-bridge/issues

## 致谢

感谢以下项目和服务的支持：

- [Anthropic Claude](https://www.anthropic.com/)
- [微博开放平台](https://open.weibo.com/)
- 所有贡献者