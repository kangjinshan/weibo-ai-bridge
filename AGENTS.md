# AGENTS.md

## 作用

这个文件是 `weibo-ai-bridge` 仓库的协作与编码代理说明。

- 这里描述仓库结构、开发流程、测试方式和修改约束。
- 运行时 AI Agent 的接入、安装和配置说明仍然放在 `agents.md`。

## 项目概览

`weibo-ai-bridge` 是一个 Go 服务，用来把微博私信桥接到本地 AI Agent CLI。

当前支持的 Agent 后端：

- Claude Code：内部注册名是 `claude-code`，会话层暴露为 `claude`
- Codex CLI：会话层暴露为 `codex`

服务运行时的主流程：

1. 连接微博开放平台 WebSocket API。
2. 为用户创建或恢复会话。
3. 路由命令消息和普通对话消息。
4. 将 Agent 输出按分片流式回传给微博。
5. 暴露健康检查、统计和 SSE 调试接口。

## 仓库结构

- `cmd/server/main.go`
  服务入口，负责 HTTP 服务、平台启动与关闭、顶层消息排队处理。
- `router/`
  消息路由、斜杠命令、交互式会话、流式转发、审批流和 `/btw` 插话。
- `agent/`
  Agent 抽象层，以及 Claude/Codex 的具体实现，包含交互式会话和 Codex app-server 流式协议。
- `skills/`
  项目内置的 skill 能力包。当前包含 `weibo-skill-api`，安装 bridge 时会同步安装到 Codex 和 Claude 的 personal skills 目录。
- `platform/weibo/`
  微博平台适配层、消息收发、分片回复发送器和平台侧类型定义。
- `session/`
  内存会话管理，以及可选的持久化入口。
- `config/`
  TOML 与环境变量配置加载、配置校验。
- `deploy/`
  部署相关资源，例如 `systemd` service 文件。
- `docs/`
  设计文档、规格说明和计划记录。
- `README.md`
  面向使用者和运维的项目说明。
- `agents.md`
  面向运行时 Agent 接入的说明，不是仓库协作手册。

## 关键运行约束

这些行为是当前系统的重要约束，除非任务明确要求，否则不要随意改动。

- 默认 HTTP 端口是 `5533`，除非设置了 `SERVER_PORT`
- 默认配置文件路径是 `config/config.toml`，可由 `CONFIG_PATH` 覆盖
- 至少要启用一个 Agent，否则服务会在启动时失败
- 会话层的 Agent 类型统一使用 `claude` 或 `codex`
- Agent Manager 内部把 Claude 注册为 `claude-code`，并把 `claude` 解析到 `claude-code`
- 新建会话按用户递增编号，格式是 `<userID>-<n>`
- 非命令消息会进入当前活跃会话路径；命令消息由 `router/command.go` 处理
- `/btw` 是特殊命令，它会把补充内容注入当前活跃的交互式会话，而不是走普通命令逻辑
- 当用户已有普通消息在处理中时，其它 slash 指令应旁路消息队列并立即执行；不要把 `/help`、`/status` 之类命令排到当前回复之后
- Codex 优先走 `codex app-server` 流式路径，失败时才回退到 JSON CLI 路径
- 长回复需要保持中文安全切分，并尽量在自然边界 flush
- 如果流式正文连续 5 秒没有实际输出，下一次恢复输出前应补一个换行，避免微博侧长段回复缺少视觉分隔
- 对 Codex interactive session，`turn/completed` 之后紧跟着出现的 EOF 或 `websocket close 1006` 应按正常收尾处理，不应回给用户 `AI execution failed`
- `skills/weibo-skill-api` 默认应复用 `weibo-ai-bridge` 的微博配置与 token 缓存，不要重新引入单独的 `~/.weibo-skill/config.json`

## 命令与接口

当前由 `router/command.go` 处理的用户命令：

- `/help`
- `/new [claude|codex]`
- `/list`
- `/switch [index|claude|codex]`
- `/model`
- `/dir`
- `/status`
- `/btw <content>`

交互式授权回复：

- 当路由层进入 `EventTypeApproval` 等待态时，用户回复 `允许`、`允许所有` 或 `取消` 会被解析为审批动作
- `允许所有` 仅对当前会话生效；router 会把后续同会话审批自动转成 allow
- `/btw` 与授权回复都依赖交互式会话；当前测试已覆盖 `claude-code` 和 `codex`

当前由 `cmd/server/main.go` 暴露的 HTTP 接口：

- `GET /health`
- `GET /stats`
- `GET /chat/stream`
- `POST /chat/stream`

如果修改命令语义或接口返回，优先补测试，并同步更新 `README.md`。

## 开发流程

常用命令：

```bash
make build
make test
make fmt
make lint
make dev
```

构建产物：

- `build/weibo-ai-bridge`

补充说明：

- `make test` 实际执行 `go test -v -race -coverprofile=coverage.out ./...`
- `make fmt` 会执行 `gofmt -w -s .`
- `make lint` 依赖 `golangci-lint`
- 仓库根目录可能已有预编译的 `server` 二进制，但改动源码后应以重新构建结果为准

## 测试要求

优先运行能证明改动正确性的最小测试范围；如果改动跨层，再逐步扩大。

示例：

```bash
go test ./router ./agent
go test ./cmd/server
go test ./...
```

如果改动影响消息链路，优先关注：

- `router/*_test.go`
- `agent/*_test.go`
- `cmd/server/main_test.go`
- `platform/weibo/*_test.go`
- `session/*_test.go`

具体对应关系：

- 改命令解析时，更新 `router/command_test.go`
- 改流式事件或事件翻译时，更新 `agent/*_test.go` 和 `router/router_test.go`
- 改 HTTP handler 时，更新 `cmd/server/main_test.go`
- 改配置逻辑时，更新 `config/*_test.go`
- 改会话生命周期时，更新 `session/session_test.go`

## 修改边界

- 用户可见的中文提示文案应尽量与现有风格保持一致，除非任务明确要求改文案
- 不要静默重命名 `claude-code` 或 `codex` 这些标识；改之前先检查解析逻辑和测试
- 保持流式事件顺序稳定；router 和 HTTP 流式出口都依赖这个顺序
- 谨慎修改会话上下文键，例如 `claude_session_id`、`codex_session_id`，它们直接影响续接逻辑
- 不要引入按字节切分中文消息的逻辑，必须保持 rune 安全
- 普通命令处理和普通聊天处理要分开，`/btw` 是唯一会进入实时交互注入路径的特殊命令
- `/btw` 的语义是“注入当前活跃 turn 的补充说明”，不是“打断当前 turn 并单独开新回复”
- 保持 `cmd/server/main.go` 里的优雅关闭语义
- 不要把 Codex 当成只有 JSON CLI 一种路径；这个仓库把 app-server 当作一等能力

## 配置说明

配置先从 TOML 读取，再由环境变量覆盖。

常见环境变量：

- `CONFIG_PATH`
- `SERVER_PORT`
- `WEIBO_APP_ID`
- `WEIBO_APP_SECRET`
- `WEIBO_TOKEN_URL`
- `WEIBO_WS_URL`
- `CLAUDE_ENABLED`
- `CODEX_API_KEY`
- `CODEX_MODEL`
- `CODEX_ENABLED`
- `SESSION_TIMEOUT`
- `SESSION_MAX_SIZE`
- `SESSION_STORAGE_PATH`
- `LOG_LEVEL`
- `LOG_FORMAT`
- `LOG_OUTPUT`

Claude 的认证主要由本地 CLI 环境负责。Codex 也可能依赖本地 CLI 或 provider 的现有配置。

## 建议的改动方式

实现改动时，建议按下面顺序处理：

1. 先判断改动归属哪一层：`config`、`session`、`agent`、`router`、`platform` 或 `cmd/server`
2. 对行为型逻辑，先读对应测试
3. 只改最适合承载这段逻辑的那一层
4. 在同包补充或更新测试
5. 先跑定向测试，再跑更大范围测试

## 仓库里的已知情况

- 当前 `go.mod` 里的 module path 仍是 `github.com/kangjinshan/weibo-ai-bridge`，除非任务明确要求，否则不要顺手改
- 仓库中可能存在预编译或构建产物，例如根目录下的 `server` 和 `build/`
- 已经存在一个小写的 `agents.md`，除非任务明确要求整合或删除，否则保留

## 文档维护

如果行为发生变化，请同步更新相应文档：

- `README.md`：面向使用者和运维的行为、命令、接口、部署说明
- `agents.md`：运行时 Agent 接入、可用性、配置说明
- `AGENTS.md`：仓库结构、开发流程、协作约束
- `skills/weibo-skill-api/*`：内置微博 skill 的脚本、说明与配置约束

提交 GitHub 前，必须显式检查一次文档是否需要同步：

1. 行为、命令、接口、部署方式变了，先更新 `README.md`
2. 协作约束、开发流程、提交要求变了，先更新 `AGENTS.md`
3. Agent 接入或运行时配置变了，再更新 `agents.md`
4. 文档未同步时，不要直接提交或推送
