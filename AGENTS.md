# AGENTS.md

## 作用

这个文件是 `weibo-ai-bridge` 仓库的协作与编码代理说明。

- 这里描述仓库结构、开发流程、测试方式和修改约束。
- 运行时 AI Agent 的接入、安装和配置说明见 `skills/weibo-skill-api/` 目录。

## 项目概览

`weibo-ai-bridge` 是一个 Go 服务，用来把微博私信桥接到本地 AI Agent CLI。

当前支持的 Agent 后端：

- Claude Code：内部注册名是 `claude-code`，会话层暴露为 `claude`
- Codex CLI：会话层暴露为 `codex`
- Hermes CLI：会话层暴露为 `hermes`
- Gemini CLI：会话层暴露为 `gemini`

服务运行时的主流程：

1. 连接微博开放平台 WebSocket API。
2. 为用户创建或恢复会话。
3. 路由命令消息和普通对话消息。
4. 将 Agent 输出按分片流式回传给微博。
5. 暴露健康检查、统计和 SSE 调试接口。

## 仓库结构

### `cmd/server/`

- `main.go` — 服务入口。HTTP 服务（`/health`、`/stats`、`/chat/stream`）、平台启动与关闭、顶层消息排队（`messageProcessor`）、`/btw` 注入分发、优雅关闭。日志初始化在配置加载之后，`initLogger` 接受 `LogConfig` 参数以支持文件输出和 JSON 格式。

### `cmd/test-report/`

- `main.go` — 测试报告生成工具。运行 `go test -json -coverprofile`，解析包覆盖率、低覆盖函数和失败测试，输出 `reports/test-report.md`、`reports/test-report.txt`、`reports/go-test.jsonl` 和 `reports/coverage.out`。

### `router/`

- `router_core.go` — Router 类型定义、Handle/Route 主入口、toRouterMessage 转换。非命令消息统一走 `streamRouterMessage`。
- `router_stream.go` — 统一流式路径 `streamRouterMessage`、`forwardStreamToPlatform`（delta/message/approval/error→分片回传）、`IsBenignCancellation`。
- `router_agent.go` — `resolveAgentExecution`（会话获取/Agent 解析）、`streamAIMessage`（交互式优先→流式回退）、`handleAIMessage`（私有方法，主入口不再调用）、`agentSessionContextKey`（`claude_session_id` / `codex_session_id` / `hermes_session_id` / `gemini_session_id`）。执行上下文会携带当前会话 `work_dir` 和 `Allow All` 状态，供 Agent 选择 CLI 参数。
- `router_interactive.go` — `liveSessions` 生命周期管理、`getOrCreateInteractiveSession`、`drainInteractiveSession`、审批等待态、`allowAll` 标记、交互式会话尾部静默保护（`interactiveDoneGracePeriod` 200ms）、leading done 防误判等待（`interactiveLeadingDoneWait` 12s）与 stale 会话“空 done”自动重建重试。
- `router_approval.go` — `formatApprovalPrompt`（审批提示格式化）、`parseApprovalAction`（28 个同义词解析，分为允许类/取消类/允许所有类）。
- `router_bytheway.go` — `/btw` 命令注入逻辑，区分流式/交互式两种注入路径。
- `listen.go` — `/listen` / `/unlisten` 原生会话日志监听。按 `/list` 编号或当前活跃会话解析 Claude/Codex/Hermes/Gemini 原生日志文件，tail 新增内容并回传微博；不会向 Agent 发送输入。
- `stream_sender.go` — 流式分片发送器 `streamReplySender`，delta 缓冲与边界感知 flush，`idleLineBreakAfter`（5s 静默补换行）、`maxBufferDelay`。
- `agent_repair.go` — Agent 可用性自动修复：`configBackedAgentAvailabilityRepairer` 会写入 TOML 配置文件并重新注册 Agent。
- `command.go` — 斜杠命令处理（`/help`、`/new`、`/list`、`/switch`、`/claude`、`/codex`、`/hermes`、`/gemini`、`/model`、`/dir`、`/status`、`/super`、`/upgrade`）。`/list` 展示所有项目的 native 会话，带项目名前缀区分来源。`/listen`、`/unlisten` 是 Router 层特殊命令，不在 command.go 里执行监听。
- `self_update.go` — `/upgrade` 的 shell 自更新适配器，调用 `scripts/self-update.sh`，收集输出并识别“已是最新”与延迟重启标记。
- `native_sessions.go` — 原生会话扫描与元数据提取。Claude 数据源：① `sessions-index.json` ② `.jsonl` 文件解析 ③ `~/.claude/history.jsonl` 补充；Codex 数据源：`state_5.sqlite` / `session_index.jsonl` / `.jsonl`；Hermes 数据源：`~/.hermes/sessions/session_*.json`；Gemini 数据源：`~/.gemini/tmp|history/*/chats/session-*.jsonl`。`decodeProjectPath` 把 Claude 项目目录中的 `-` 还原成 `/`，无法保留原路径中本身包含 `-` 的目录名，这是与 Claude Code 同步的已知不可逆限制。
- `router_session_binding.go` — `bindAgentNativeSessionID` 把 Agent 返回的 native session ID 收敛到 bridge 会话；adopt 后可能返回新的 `*session.Session`，调用方需要使用返回值而不是旧指针。
- `stream_sender.go` — 流式分片发送器 `streamReplySender`，delta 缓冲与边界感知 flush，`idleLineBreakAfter`（5s 静默补换行）、`maxBufferDelay`。
- `super_mode.go` — `/super` 模式状态管理与 Super Peer Review 后台流程（向其它 Agent 请求 review 并把结果作为 feedback 写回当前 turn）。
- `router_utils.go` — rune 安全切分等辅助函数。

### `agent/`

- `agent.go` — `Agent`/`InteractiveAgent`/`InteractiveSession`/`InterruptibleSession` 接口、8 种 EventType（session/delta/message/approval/tool_start/tool_end/error/done）、ApprovalAction。
- `manager.go` — Agent 注册/解析/默认。`agentCandidates`（`claude`→`claude-code`）。`getDefaultAgentLocked` 和 `ListAgents` 按名称排序。
- `claude.go` — Claude 流式执行（`--output-format stream-json`）、`resolveTextDelta`（rune 安全增量对比）、`parseClaudeStreamEvent`/`parseClaudeStructuredStreamEvent`。
- `claude_session.go` — Claude 交互式会话（stdin/stdout stream-json 协议）、审批（`control_request`/`control_response`）、`claudePendingApproval`。
- `codex.go` — Codex 流式执行。`ExecuteStream` 优先走 `executeViaAppServer`，失败时回退到 `executeViaJSONCLI`。`parseCodexEvent`/`parseCodexItemCompleted`。
- `codex_interactive_session.go` — Codex app-server 交互式会话，优先 stdio，兼容回退 WebSocket。审批（`requestApproval` 系列）、`Interrupt`（turn/interrupt）、`shouldIgnoreCodexAppServerReadError`（EOF/closed pipe/close 1006 容错）。
- `codex_appserver.go` — Codex app-server 客户端。优先 `--listen stdio://`，失败时回退旧版 `ws://127.0.0.1:port` + `/readyz`；通过 JSON-RPC 执行 initialize/ensureThread/startTurn，保留 5 分钟读超时，`parseCodexAppServerMessage`（delta/completed）。
- `hermes.go` — Hermes CLI 执行与 ACP 交互式会话。流式 fallback 仍可走 `hermes chat --quiet --source tool --query`，主链路优先启动 `hermes acp`，通过 newline-delimited JSON-RPC 的 `initialize` / `session/new|resume` / `session/prompt` 接收 `session/update` 增量、审批请求和 `done`。
- `gemini.go` — Gemini CLI 流式执行（`--output-format stream-json --prompt`）、默认追加 `--include-directories /`、`Allow All` 时追加 `-y`、`init.session_id` 会话 ID 提取、`message/tool_use/tool_result/error/result` 事件翻译，以及 Gemini preview API 不支持 tool `id` 时的请求 payload / resume history 清理。
- `prompt.go` — `wrapUserPrompt`。

### `session/`

- `session.go` — 会话 CRUD、持久化存储（默认启用 `storagePath`，JSON 文件存储）、`CreateNext`（历史兼容）、`AdoptSessionID`（pending/session ID 收敛到 native ID）、`SetTitleIfEmpty`、过期清理、旧路径迁移、原子文件写入。

### `config/`

- `config.go` — TOML 与环境变量配置加载、`firstEnv`（`WEIBO_APP_SECRET` 优先、`WEIBO_APP_Secret` 兼容）、`Validate`。
- `config.toml` — 默认配置文件。
- `config.example.toml` — 示例配置文件。

### `platform/weibo/`

- `client.go` — WebSocket 连接与消息收发、心跳（30s）、Token 刷新、分片发送（`ReplyStream`/`ChunkSender`）、消息去重、自动重连、rune 安全切分（`maxWeiboChunk` 4000）。
- `message.go` — 消息类型定义（text/image/link/at/reply）、WebSocket 消息解析。

### `skills/`

- `weibo-skill-api/` — 内置微博 Skill 能力包，包含热搜/智搜、微博状态查询、超话互动、媒体上传、定时任务和创作者数据摘要；安装 bridge 时同步安装到 `~/.codex/skills/`、`~/.claude/skills/`、`~/.hermes/skills/` 和 `~/.gemini/skills/`。复用 bridge 的微博配置与 token 缓存。

### `deploy/`

- `weibo-ai-bridge.service` — systemd 示例配置。
- `weibo-ai-bridge.service.tmpl` — 供 `scripts/service.sh` 渲染的 systemd 模板。
- `com.weibo-ai-bridge.plist.tmpl` — 供 `scripts/service.sh` 渲染的 macOS launchd 模板。

### `scripts/`

- `install.sh` — 完整安装（含 skills）。
- `install-skills.sh` — 仅安装 skills。
- `self-update.sh` — 安全自升级脚本：先比对本地二进制与 GitHub 指定 ref 的 commit；不一致时下载、编译、原子替换二进制，并通过延迟任务重启服务。Linux 下优先用 `systemd-run` 创建 transient timer/service，避免重启任务随旧 bridge service cgroup 一起被清理。
- `setup.sh` — 初始设置。
- `service.sh` — 跨平台服务管理入口（Linux systemd / macOS launchd）。

### `docs/`

- 设计文档、规格说明和计划记录。

### 根目录文件

- `README.md` — 面向使用者和运维的项目说明。
- `skills/weibo-skill-api/` — 运行时 Agent 接入说明与配置约束。
- `AGENTS.md` — 本文件，仓库协作手册。
- `Makefile` — 构建脚本。
- `go.mod` / `go.sum` — Go 模块定义。
- `.env.example` — 环境变量示例。

## 关键运行约束

这些行为是当前系统的重要约束，除非任务明确要求，否则不要随意改动。

- 默认 HTTP 端口是 `5533`，除非设置了 `SERVER_PORT`
- 默认配置文件路径是 `config/config.toml`，可由 `CONFIG_PATH` 覆盖
- 至少要启用一个 Agent，否则服务会在启动时失败
- 会话层的 Agent 类型统一使用 `claude`、`codex`、`hermes` 或 `gemini`
- Agent Manager 内部把 Claude 注册为 `claude-code`，并把 `claude` 解析到 `claude-code`
- 会话管理采用 native-first：`/new` 只准备 pending 会话锚点，收到 Agent `session/thread` 事件后会把会话 ID 收敛为 native ID
- 非命令消息会进入当前活跃会话路径；命令消息由 `router/command.go` 处理
- `/btw` 是特殊命令，它会把补充内容注入当前活跃的交互式会话，而不是走普通命令逻辑
- `/listen` 和 `/unlisten` 是 Router 层特殊命令。监听只读取本地原生会话日志并回传新增内容，不应调用 Agent resume/send，也不应打断当前对话。
- 当用户已有普通消息在处理中时，其它 slash 指令应旁路消息队列并立即执行；不要把 `/help`、`/status` 之类命令排到当前回复之后
- 从微博对话里升级 bridge 时优先使用 `/upgrade` 或 `scripts/self-update.sh`；不要在普通 Agent turn 中直接同步执行 `scripts/service.sh restart`、`systemctl restart` 或 `launchctl bootout`，否则会先终止承载当前回复的 bridge 进程
- `/upgrade` 必须先比较本地二进制 commit 与 GitHub 目标 ref commit；一致时直接回复已是最新，不应下载、编译或重启
- Linux systemd unit 必须持久化 `WEIBO_AI_BRIDGE_SCOPE=system|user`，避免 system service 中的非 root 进程把 `/upgrade` 延迟重启误判为 `systemctl --user`；system scope 的非 root 延迟重启只能使用非交互式 `sudo -n systemd-run`，无权限时不要回报已安排重启
- Codex 优先走 `codex app-server` 流式路径，失败时才回退到 JSON CLI 路径
- Hermes 主链路走 `hermes acp` 交互式形态，按 ACP `sessionId` 持久化到 `hermes_session_id`；`/btw` 在 Hermes turn 运行中会转成 ACP `/steer` 注入当前 turn；一次性 `hermes chat --quiet --source tool --query` 仅作为流式 fallback 保留
- Hermes 的 ACP 接入方式与 `cc-connect` 的通用 ACP agent 一致：`type = "acp"`、`command = "hermes"`、`args = ["acp"]`，协议为 stdin/stdout 上的 newline-delimited JSON-RPC
- Hermes 续接旧 ACP session 后若返回 `API call failed after 3 retries: HTTP 404: Resource not found`，router 会清空旧 `hermes_session_id`、新建 Hermes ACP session，并对当前消息自动重试一次
- Gemini 走 `gemini --output-format stream-json --prompt` 流式路径，按 `init.session_id` 持久化到 `gemini_session_id`，续接时使用 `--resume`
- Gemini 默认追加 `--include-directories /`，跨目录读取是默认能力；不要要求用户先确认或先开 `/super on` 才能读取当前项目外目录
- `/super on` / `Allow All` 对 Gemini 仅用于追加 `-y` 自动批准工具调用，不负责放开目录读取
- Gemini preview API 当前不接受 `functionCall.id` / `functionResponse.id`，保留请求 payload sanitizer 和 resume history sanitizer；不要通过禁用工具调用来规避这个 400
- 长回复需要保持中文安全切分，并尽量在自然边界 flush
- 流式增量对比（delta resolution）必须按 UTF-8 rune 比较而不是按字节比较，避免在多字节中文字符中间截断
- 连接需要设置合理的读超时：微博 WebSocket 平台 60 秒，Codex app-server stdio/WebSocket 5 分钟
- 如果流式正文连续 5 秒没有实际输出，下一次恢复输出前应补一个换行，避免微博侧长段回复缺少视觉分隔
- 对 Codex interactive session，`turn/completed` 之后紧跟着出现的 EOF、closed pipe（以及旧 WebSocket 路径的 close 1006）应按正常收尾处理，不应回给用户 `AI execution failed`
- 对交互式会话，若新 turn 首个事件是 `done` 且在 `interactiveLeadingDoneWait` 窗口内没有任何有效信号（delta/message/approval/error），应视为 stale 会话空结束并自动重建会话后重试一次，避免用户看到“发了消息但无回复”
- Codex `thread/resume` 续接已存在本地线程时，应避免覆盖原线程策略参数（如 approval/sandbox/model）；优先使用最小续接参数并同步事件里的 `threadId` 变化，避免“看似续接但实际分叉新线程”
- `skills/weibo-skill-api` 默认应复用 `weibo-ai-bridge` 的微博配置与 token 缓存，不要重新引入单独的 `~/.weibo-skill/config.json`
- Router 的 `Handle` 主入口（`Handler` 接口）和生产入口 `HandleMessage` 都走流式路径（`streamRouterMessage`）。`handleAIMessage` 作为私有方法仍保留，仅供单元测试调用；生产流程不再经过它。Agent 接口仍保留 `Execute`（非流式）方法，但主流程只走 `ExecuteStream` 和 `InteractiveSession`
- `cmd/server/main.go` 启动后会通过微博平台发出一条 startup notification（约启动 2s 后），用的是 `context.Background()`；若与即时 SIGTERM 关停叠加，可能在 `Stop` 期间仍然尝试发送，需要注意这条 goroutine 不受主 ctx 管控
- `/listen` / `/unlisten` 的后台 goroutine（`listenRuns`）和 `/super` 的 peer review 后台 goroutine（`superReviews`）目前都使用独立 ctx，进程退出时依赖 cancel 链路收尾；后续如果加入 Router-level shutdown，需要把这两类后台任务一起取消

## 命令与接口

当前用户命令：

- `/help`
- `/new [claude|codex|hermes|gemini]`
- `/list`（展示所有项目的 native 会话列表，带项目名前缀）
- `/switch [index|claude|codex|hermes|gemini]`
- `/claude`（等价于 `/switch claude`）
- `/codex`（等价于 `/switch codex`）
- `/hermes`（等价于 `/switch hermes`）
- `/gemini`（等价于 `/switch gemini`）
- `/model`
- `/dir [path]`（不传参数显示当前目录；传 `path` 时设置当前会话目录）
- `/status`
- `/super [on|off|status]`（开启/关闭/查看 Super 模式，`on` 等价于当前会话 `Allow All`）
- `/upgrade [--ref branch|tag]`（先比对本地与 GitHub 目标版本；不一致时下载、编译安装，并在当前回复发出后延迟重启服务）
- `/btw <content>`（实际在 `router_core.go` 和 `router_bytheway.go` 中处理，不走 command.go）
- `/listen [index]`（实际在 `router/listen.go` 中处理，不走 command.go；不传 index 时监听当前活跃原生会话，传 index 时监听 `/list` 对应编号的原生会话）
- `/unlisten`（停止当前用户正在进行的 `/listen`）

命令语义备注：
- `/new` 不直接创建 bridge 自增会话，而是准备下一条消息要使用的新 native 会话
- `/list` 展示所有项目的 native 会话（不再按当前项目过滤），标题前带项目名前缀（如 `weibo-ai-bridge/会话标题`）
- `/claude`、`/codex`、`/hermes` 与 `/gemini` 是 `/switch` 的快捷别名（大小写不敏感）
- `/status` 在 `session_id` 缺失时，会回退到该用户当前 active session
- `/upgrade` 由 bridge 命令层直接执行，不进入 Agent；本地与 GitHub 目标 commit 一致时不执行升级；成功构建并安装后只安排后台延迟重启，确保本轮回复先发给用户
- `/listen` 复用 `/list` 的 native 会话候选顺序。再次 `/listen` 会替换同一用户旧监听；`/unlisten` 通过 cancel 结束后台监听 goroutine。
- native 会话标题优先级与 Claude Code resume 一致：customTitle > aiTitle > summary > lastPrompt > content
- native 会话扫描有三个数据源：sessions-index.json、.jsonl 文件解析、history.jsonl 补充

交互式授权回复：

- 当路由层进入 `EventTypeApproval` 等待态时，用户回复会被 `parseApprovalAction`（`router/router_approval.go`）解析
- 允许类：允许/同意/可以/好/好的/是/确认/approve/allow/yes/y/ok
- 取消类：取消/拒绝/不允许/不行/不/否/deny/no/n/reject/cancel
- 允许所有类：允许所有/允许全部/全部允许/所有允许/都允许/全部同意/allow all/allowall/approve all/yes all
- `允许所有` 仅对当前会话生效；router 会把后续同会话审批自动转成 allow
- Gemini 是一次性 stream-json 进程，不走交互式审批等待；router 会把当前会话 `Allow All` 状态下传，Gemini Agent 只用它决定是否追加 `-y`
- `/btw` 与授权回复都依赖交互式会话；当前测试已覆盖 `claude-code`、`codex` 和 `hermes`

当前由 `cmd/server/main.go` 暴露的 HTTP 接口：

- `GET /health`
- `GET /stats`
- `GET /chat/stream`
- `POST /chat/stream`

SSE 事件类型（8 种，定义在 `agent/agent.go`）：

- `session` — Agent 会话 ID
- `delta` — 流式正文增量
- `message` — 完整消息
- `approval` — 审批请求
- `tool_start` — 工具调用开始
- `tool_end` — 工具调用结束
- `error` — 执行错误
- `done` — 本轮结束

如果修改命令语义或接口返回，优先补测试，并同步更新 `README.md`。

## 开发流程

常用命令：

```bash
make build
make test
make test-report
make fmt
make lint
make dev
```

构建产物：

- `build/weibo-ai-bridge`

补充说明：

- `make test` 实际执行 `go test -v -race -coverprofile=coverage.out ./...`
- `make test-report` 执行 `go run ./cmd/test-report`，输出 Markdown/文本报告、测试 JSON 日志和 coverage profile 到 `reports/`
- `make fmt` 会执行 `gofmt -w -s .`
- `make lint` 依赖 `golangci-lint`
- 产物目录约定：`build/` 放本地构建产物，`dist/` 放发布包；仓库根目录不应出现可执行文件

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
- 改 Gemini CLI 参数、preview payload sanitizer 或 resume history sanitizer 时，更新 `agent/gemini_test.go`，并尽量用一次真实工具调用验证跨目录读取与工具能力仍可用
- 改 delta resolution 时，`agent/resolve_delta_test.go` 和 `router/resolve_delta_test.go` 各有一份测试
- 改 HTTP handler 时，更新 `cmd/server/main_test.go`
- 改配置逻辑时，更新 `config/*_test.go`
- 改会话生命周期时，更新 `session/session_test.go`

## 修改边界

- 用户可见的中文提示文案应尽量与现有风格保持一致，除非任务明确要求改文案
- 不要静默重命名 `claude-code` 或 `codex` 这些标识；改之前先检查解析逻辑和测试
- 保持流式事件顺序稳定；router 和 HTTP 流式出口都依赖这个顺序
- 谨慎修改会话上下文键，例如 `claude_session_id`、`codex_session_id`、`hermes_session_id`、`gemini_session_id`，它们直接影响续接逻辑
- `session.Session.Context` 视为私有并发敏感字段：禁止在 `session` 包外直接 `sess.Context[...]` 读写；统一使用 `sess.GetContext/SetContext`、`sess.ContextString/ContextBool` 或 `Snapshot`
- 不要引入按字节切分中文消息的逻辑，必须保持 rune 安全
- delta resolution（`resolveTextDelta` 和 `resolveDeltaFromSnapshot`）同样必须按 UTF-8 rune 比较，不能按字节比较
- 普通命令处理和普通聊天处理要分开；`/btw`、`/listen`、`/unlisten` 是 Router 层特殊命令。`/btw` 进入实时交互注入路径，`/listen` 只进入原生日志监听路径
- `/btw` 的语义是"注入当前活跃 turn 的补充说明"，不是"打断当前 turn 并单独开新回复"
- 保持 `cmd/server/main.go` 里的优雅关闭语义
- 不要把 Codex 当成只有 JSON CLI 一种路径；这个仓库把 app-server 当作一等能力
- 不要把 Gemini 跨目录读取能力绑定到 `/super on`；默认应允许读取当前项目外目录，`Allow All` 只影响是否自动批准工具调用
- 不要通过禁用 Gemini 工具调用来绕过 preview API 的 tool `id` 400；应继续保留工具能力，并在出站请求和旧会话历史中清理不被 API 接受的字段
- Claude Agent 只走流式路径（`--output-format stream-json`），`--print --output-format json` 路径已移除
- `IsBenignCancellation` 是 router 包的导出函数，`cmd/server` 和 `router/stream` 共用，不要在各自包内再定义私有版本
- `liveSessions` 生命周期由 `router_interactive.go` 管理，修改时注意锁边界和并发安全
- `stream_sender.go` 的 `idleLineBreakAfter` 和 `maxBufferDelay` 是可调节常量，修改需评估对用户体验的影响
- `agent_repair.go` 会写入 TOML 配置文件来修复 Agent 可用性，修改需注意与 config 包的一致性

## 配置说明

配置先从 TOML 读取，再由环境变量覆盖。

常见环境变量：

- `CONFIG_PATH` — TOML 配置文件路径
- `SERVER_PORT` — HTTP 监听端口
- `WEIBO_APP_ID` — 微博应用 ID
- `WEIBO_APP_SECRET` — 微博应用密钥（主环境变量名）；`WEIBO_APP_Secret` 是兼容别名
- `WEIBO_TOKEN_URL` — Token 获取 URL
- `WEIBO_WS_URL` — WebSocket 连接 URL
- `CLAUDE_ENABLED` — 是否启用 Claude
- `CODEX_API_KEY` — Codex API Key
- `CODEX_MODEL` — Codex 模型覆盖
- `CODEX_ENABLED` — 是否启用 Codex
- `HERMES_MODEL` — Hermes 模型覆盖
- `HERMES_PROFILE` — Hermes profile 覆盖（当前 ACP 主链路沿用 Hermes 当前 profile；该字段保留给 CLI fallback 兼容）
- `HERMES_PROVIDER` — Hermes provider 覆盖
- `HERMES_ENABLED` — 是否启用 Hermes
- `GEMINI_MODEL` — Gemini 模型覆盖
- `GEMINI_ENABLED` — 是否启用 Gemini
- `SESSION_TIMEOUT` — 会话超时（秒）
- `SESSION_MAX_SIZE` — 最大会话数
- `SESSION_STORAGE_PATH` — 会话存储路径
- `LOG_LEVEL` — 日志级别
- `LOG_FORMAT` — 日志格式
- `LOG_OUTPUT` — 日志输出位置
- `HTTP_API_KEY` — `/chat/stream` 等 HTTP 接口的访问密钥（在 `config.go:190` 读取）

Claude 的认证主要由本地 CLI 环境负责。Codex、Hermes 和 Gemini 也可能依赖本地 CLI 或 provider 的现有配置；Gemini 会从 `~/.gemini/.env` 补齐当前进程缺失的 Gemini API 环境变量。

## 建议的改动方式

实现改动时，建议按下面顺序处理：

1. 先判断改动归属哪一层：`config`、`session`、`agent`、`router`、`platform` 或 `cmd/server`
2. 对行为型逻辑，先读对应测试
3. 只改最适合承载这段逻辑的那一层
4. 在同包补充或更新测试
5. 先跑定向测试，再跑更大范围测试

## 仓库里的已知情况

- 当前 `go.mod` 里的 module path 仍是 `github.com/kangjinshan/weibo-ai-bridge`，除非任务明确要求，否则不要顺手改
- 当前规范是产物集中在 `build/` 或 `dist/`，根目录若出现可执行文件应视为异常并清理

## 文档维护

如果行为发生变化，请同步更新相应文档：

- `README.md`：面向使用者和运维的行为、命令、接口、部署说明
- `AGENTS.md`：仓库结构、开发流程、协作约束
- `skills/weibo-skill-api/*`：内置微博 skill 的脚本、说明与配置约束

提交 GitHub 前，必须显式检查一次文档是否需要同步：

1. 行为、命令、接口、部署方式变了，先更新 `README.md`
2. 协作约束、开发流程、提交要求变了，先更新 `AGENTS.md`
3. Agent 接入或运行时配置变了，更新 `skills/weibo-skill-api/` 下的文档
4. 文档未同步时，不要直接提交或推送
