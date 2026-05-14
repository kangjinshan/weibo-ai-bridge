# Weibo AI Bridge

微博私信与本地 AI Agent CLI 的桥接服务。它通过微博开放平台 WebSocket API 接收私信，把消息路由到 Claude Code、Codex CLI、Hermes CLI 或 Gemini CLI，再把 Agent 输出流式回传到微博。

它不只是把微博当成聊天入口，也把微博能力接到 Agent 工作流里：Agent 可以复用 bridge 的微博配置与 token 缓存，使用内置 skill 查询热搜和智搜、检查微博状态、参与超话互动、上传图片/视频，并生成创作者数据摘要。

适合用来把一台本地机器上的编码 Agent、安全审批、项目会话、微博私信入口和微博内容能力连在一起。

## 核心特性

- **微博私信桥接** — 通过 WebSocket API 实时收发微博私信，长回复按自然边界分片回传。
- **微博能力接入** — 内置 `weibo-skill-api` 让 Agent 可使用热搜/智搜、微博状态查询、超话发帖评论点赞、图片/视频上传和创作者数据摘要等能力。
- **多 Agent 支持** — 支持 Claude Code、Codex CLI、Hermes CLI 和 Gemini CLI，并可在会话中切换。
- **原生会话续接** — 优先使用各 Agent 自己的 session/thread ID，bridge 只维护索引、活动会话和少量上下文。
- **流式交互与审批** — Agent 请求授权时可直接回复 `允许`、`允许所有` 或 `取消`；`/btw` 可向正在运行的交互式 turn 注入补充说明。
- **命令即时响应** — `/help`、`/status`、`/super` 等 slash 命令会旁路普通消息队列，避免被长任务堵住。
- **Super 协作模式** — `/super on` 会开启当前会话的 `Allow All`，并在主 Agent 回复后异步调用对侧 Agent 复盘，把结论注入下一轮。
- **安全自升级** — `/upgrade` 会先比较本地和目标 Git commit；确实有新版本时才构建替换，并在当前回复发出后延迟重启。
- **统一微博凭证** — 内置微博 Skills 复用 bridge 的微博配置与 token 缓存，不需要为每个 Agent 单独维护一套微博凭证。
- **本地调试接口** — `/health`、`/stats` 和 `/chat/stream` 可用于健康检查、状态观察和 SSE 调试。
- **启动自检通知** — 服务启动成功后会给 bot 自己发送一条私信，包含版本、commit 和编译时间。

## 快速开始

### 前置要求

- Go 1.22+
- 微博开放平台 App ID / App secret
- 至少安装并配置一个 Agent CLI：`claude`、`codex`、`hermes` 或 `gemini`

### 安装运行

```bash
git clone https://github.com/kangjinshan/weibo-ai-bridge.git
cd weibo-ai-bridge

cp .env.example .env
```

编辑 `.env`，至少填入微博凭证，并确保有一个 Agent 已启用：

```dotenv
WEIBO_APP_ID=your-app-id
WEIBO_APP_SECRET=your-app-secret

# 默认启用 Claude；如果使用其它 Agent，启用对应开关
CLAUDE_ENABLED=true
CODEX_ENABLED=false
HERMES_ENABLED=false
GEMINI_ENABLED=false
```

构建并运行：

```bash
make build && ./build/weibo-ai-bridge
```

开发时也可以使用：

```bash
make dev
```

服务默认监听 `127.0.0.1:5533`。启动成功后，bridge 会连接微博 WebSocket，并给 bot 自己发送一条启动通知。

微博凭证获取方式见 [微博凭证获取](#微博凭证获取)。

如果要安装为系统服务并同步内置微博 Skills，使用仓库脚本：

```bash
bash scripts/install.sh
```

### 常用命令

| 命令 | 说明 |
|------|------|
| `make build` | 构建到 `build/weibo-ai-bridge` |
| `make build-linux` | 交叉编译 Linux AMD64 |
| `make test` | 运行测试（含覆盖率） |
| `make test-report` | 生成 Markdown/文本测试报告到 `reports/` |
| `make test-coverage` | 生成 HTML 覆盖率报告 |
| `make fmt` | 格式化代码 |
| `make lint` | 代码检查（需 golangci-lint） |
| `make dev` | 构建并运行 |

产物规范：
- `build/`：本地构建产物
- `dist/`：发布包产物
- 仓库根目录不放可执行文件；统一从 `build/weibo-ai-bridge` 运行

## 用户命令

| 命令 | 说明 |
|------|------|
| `/help` | 显示帮助信息 |
| `/new [claude\|codex\|hermes\|gemini]` | 准备新的原生会话（不传参数时沿用当前 Agent） |
| `/list` | 查看可切换的原生会话列表（编号、标题、目录、时间） |
| `/switch <编号>` | 按 `/list` 中的编号切换活跃会话 |
| `/switch <agent类型>` | 切换当前会话的 Agent 类型 |
| `/claude` | 等价于 `/switch claude`（大小写不敏感） |
| `/codex` | 等价于 `/switch codex`（大小写不敏感） |
| `/hermes` | 等价于 `/switch hermes`（大小写不敏感） |
| `/gemini` | 等价于 `/switch gemini`（大小写不敏感） |
| `/btw <内容>` | 向当前交互式会话注入补充信息（若当前在审批等待态，需先回复 `允许` / `取消` / `允许所有`） |
| `/model` | 显示当前使用的模型 |
| `/dir [path]` | 显示当前工作目录；传 `path` 时设置当前会话工作目录 |
| `/status` | 显示当前会话状态（`session_id` 缺失时自动回退到当前活跃会话） |
| `/super [on\|off\|status]` | 管理 Super 模式；`on` 等价于对当前会话开启 `Allow All` |
| `/upgrade [--ref branch\|tag]` | 先比对本地与 GitHub 目标版本；不一致时下载、编译安装，并在当前回复发出后延迟重启服务 |

### 授权回复

当 Agent 请求授权时，直接回复以下任意词汇：

| 类别 | 支持的回复 |
|------|-----------|
| 允许 | 允许 / 同意 / 可以 / 好 / 好的 / 是 / 确认 / approve / allow / yes / y / ok |
| 取消 | 取消 / 拒绝 / 不允许 / 不行 / 不 / 否 / deny / no / n / reject / cancel |
| 允许所有 | 允许所有 / 允许全部 / 全部允许 / 所有允许 / 都允许 / 全部同意 / allow all / allowall / approve all / yes all |

`允许所有` 仅对当前会话生效，后续授权自动通过。
审批等待态下，`/btw` 会被拒绝并提示先完成审批回复，避免把补充消息注入到未授权的工具执行上下文。

### Super 模式说明

- `/super on`：
  - 当前会话开启 `Allow All`（审批自动通过）
  - Gemini 会话在 `Allow All` 后会以 YOLO 模式启动，自动批准工具调用
  - 主 Agent 每轮输出完成后，会自动调用对侧 Agent 做复盘（超时 180 秒）
  - 对侧复盘结论写入会话，并在下一轮自动注入给主 Agent 作为优化基础
- `/super off`：
  - 关闭 Super 模式
  - 清空待注入的对侧复盘结论

## 配置

优先级：环境变量 > TOML 配置文件 > 默认值

启动时会自动尝试读取 `.env`（当前工作目录，以及 `CONFIG_PATH` 所在目录），并且不会覆盖已导出的系统环境变量。

### 关键环境变量

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `WEIBO_APP_ID` | 微博应用 ID | 必填 |
| `WEIBO_APP_SECRET` | 微博应用密钥（兼容旧名 `WEIBO_APP_Secret`） | 必填 |
| `SERVER_PORT` | HTTP 端口 | 5533 |
| `HTTP_API_KEY` | `/stats`、`/chat/stream` 的 Bearer Token；留空不启用认证 | 空 |
| `CONFIG_PATH` | TOML 配置路径 | `config/config.toml` |
| `CLAUDE_ENABLED` | 启用 Claude | true |
| `CODEX_ENABLED` | 启用 Codex | false |
| `CODEX_MODEL` | Codex 模型覆盖（留空沿用本机 CLI 默认） | 空 |
| `HERMES_ENABLED` | 启用 Hermes | false |
| `HERMES_MODEL` | Hermes 模型覆盖（留空沿用本机 CLI 默认） | 空 |
| `HERMES_PROFILE` | Hermes profile 覆盖（CLI fallback 兼容项；ACP 主链路沿用当前 Hermes profile） | 空 |
| `HERMES_PROVIDER` | Hermes provider 覆盖（留空沿用本机默认） | 空 |
| `GEMINI_ENABLED` | 启用 Gemini | false |
| `GEMINI_MODEL` | Gemini 模型覆盖（留空沿用本机 CLI 默认） | 空 |
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

[agent.hermes]
enabled = false
model = ""     # 留空沿用本机 hermes CLI 默认配置
profile = ""   # CLI fallback 兼容项；ACP 主链路沿用当前 Hermes profile
provider = ""  # 留空沿用本机 hermes CLI 默认 provider

[agent.gemini]
enabled = false
model = ""  # 留空沿用本机 gemini CLI 默认配置

[session]
timeout = 3600
max_size = 1000
storage_path = "~/.config/weibo-ai-bridge/sessions"

[http]
port = "5533"
api_key = ""  # 留空不启用 /stats 和 /chat/stream 认证

[log]
level = "info"
format = "json"
output = "stdout"
```

**注意**：
- Claude API Key 和模型配置由 Claude Code CLI 管理，不在此配置文件中
- `agent.codex.model` 建议留空，让 Bridge 沿用本机 CLI 的默认配置；手动指定时需确认目标 provider 上存在对应 deployment
- `agent.hermes.model/provider` 建议留空，让 Bridge 沿用本机 Hermes CLI 的默认配置；`profile` 目前仅作为 CLI fallback 兼容项，ACP 主链路沿用当前 Hermes profile
- `agent.gemini.model` 建议留空，让 Bridge 沿用本机 Gemini CLI 的默认配置；Gemini 认证由本机 `gemini` CLI / `GEMINI_API_KEY` 等配置负责
- 如果报 `404 deployment does not exist`，通常不是启动方式问题，而是模型名和 provider 配置不匹配

## 会话管理

Bridge 采用 native-first 会话模型：真正的对话历史保存在 Claude/Codex/Hermes/Gemini 自己的原生会话里，bridge 只负责记录当前用户正在使用哪个会话、工作目录、Agent 类型和必要的上下文标记。

### 使用方式

- 第一次发普通消息时，如果没有活跃会话，bridge 会先准备一个 pending 锚点；等 Agent 返回原生 `session_id` 或 `thread_id` 后，再绑定为 native 会话。
- `/new [agent]` 用于准备下一条消息的新原生会话，不会提前创建 bridge 自增会话。
- `/list` 会扫描本机 Agent 的原生会话，并显示可切换编号、标题、目录和时间。
- `/switch <编号>` 切换到 `/list` 中的原生会话；`/switch <agent>` 或 `/claude`、`/codex`、`/hermes`、`/gemini` 切换当前会话的 Agent 类型。
- 会话索引默认持久化到 `~/.config/weibo-ai-bridge/sessions/`，服务重启后可恢复。
- 新版本首次启动时会自动导入旧版 `data/sessions/` 数据。

### Agent 会话实现

- **Claude** — 使用 `--output-format stream-json` 流式路径，首轮提取 `session_id`，后续用 `--resume` 继续对话。
- **Codex** — 优先通过 `codex app-server` 获取流式增量；不可用时回退到 `codex exec --json`。bridge 会把 `thread_id` 持久化到 `codex_session_id`。
- **Hermes** — 主链路使用 `hermes acp` 交互式形态，并把 ACP `sessionId` 持久化到 `hermes_session_id`。当前 turn 仍在运行时，`/btw` 会转成 Hermes ACP `/steer` 注入。
- **Gemini** — 使用 `gemini --output-format stream-json --prompt` 流式路径，首轮从 `init.session_id` 提取 native session ID，后续用 `--resume` 继续对话。Gemini 默认追加 `--include-directories /`，允许读取当前项目外目录；当前会话开启 `Allow All` 后会额外追加 `-y` 自动批准工具调用。

Hermes 的 ACP 接入方式与 `cc-connect` 的通用 ACP agent 配置一致，通过 stdio 启动 `hermes acp`：

```toml
[projects.agent]
type = "acp"
command = "hermes"
args = ["acp"]
```

### 使用示例

```
用户: 帮我看看这个 Go 项目怎么拆模块
Bot: 已收到消息，正在处理中，请稍候。
Bot: <随后流式返回真实回复>
```

```
用户: /list
Bot:
| 编号 | 标题 | 目录 | 时间 |
| --- | --- | --- | --- |
| 1 | 帮我看看这个 Go 项目怎么拆模块（当前） | weibo-ai-bridge | 2026-05-14 10:20 |
| 2 | 未命名会话 | other-project | 2026-05-13 22:41 |

用户: /switch 1
Bot: Switched to native session: 帮我看看这个 Go 项目怎么拆模块 (codex, native=abc123...)
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

认证说明：
- `/health` 始终不需要认证，便于服务健康检查。
- `http.api_key` 或 `HTTP_API_KEY` 设置后，`/stats` 和 `/chat/stream` 需要携带 `Authorization: Bearer <api_key>`。
- 未设置 API Key 时保持兼容行为，不启用 HTTP 认证；服务默认只监听 `127.0.0.1`。

`/health` 返回示例：
```json
{
  "status": "ok",
  "service": "weibo-ai-bridge",
  "build": {
    "version": "dev",
    "git_commit": "abc1234",
    "build_time": "2026-04-29T10:11:12Z"
  }
}
```

说明：
- `build_time` 为二进制编译时间（UTC，RFC3339），可用于确认当前进程是否为最新构建
- `make build` / `make build-linux` 会自动注入 `version`、`git_commit`、`build_time`

### `/chat/stream`

推荐使用 POST，避免把 `content` 写入 URL、shell history 或代理访问日志：

```bash
curl -N \
  -H "Authorization: Bearer your-secret-key" \
  -H "Content-Type: application/json" \
  -d '{"user_id":"123456","content":"请用中文写三段文字","session_id":"optional-session-id"}' \
  http://127.0.0.1:5533/chat/stream
```

GET 仍保留用于本地调试：`/chat/stream?user_id=<user>&content=<urlencoded-content>&session_id=<optional>`

POST 请求：
```json
{
  "user_id": "123456",
  "content": "请用中文写三段文字",
  "session_id": "optional-session-id"
}
```

补充说明：
- `session_id` 为可选；当请求内容是 slash 命令（例如 `/status`、`/super status`）且未传 `session_id` 时，路由层会回退到该 `user_id` 的当前活跃会话。
- 传入 `session_id` 时会校验会话归属：该会话必须属于同一个 `user_id`，否则返回错误事件，不会复用到其他用户会话。

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

### 统一服务管理（Linux + macOS）

```bash
# 安装服务定义
scripts/service.sh install

# 启动/重启/停止
scripts/service.sh start
scripts/service.sh restart
scripts/service.sh stop

# 状态与日志
scripts/service.sh status
scripts/service.sh logs
```

Linux 说明：
```bash
# root 默认安装为 system service（/etc/systemd/system）
sudo scripts/service.sh install --scope system
sudo scripts/service.sh start --scope system
sudo scripts/service.sh status --scope system
sudo scripts/service.sh logs --scope system

# 非 root 可用 user service（~/.config/systemd/user）
scripts/service.sh install --scope user
scripts/service.sh start --scope user
```

macOS 说明：
```bash
# 用户级 launchd（~/Library/LaunchAgents/com.weibo-ai-bridge.plist）
scripts/service.sh install
scripts/service.sh start
scripts/service.sh status
scripts/service.sh logs
```

可选环境变量（覆盖自动探测）：
- `WEIBO_AI_BRIDGE_BIN`
- `WEIBO_AI_BRIDGE_CONFIG_PATH`
- `WEIBO_AI_BRIDGE_ENV_FILE`
- `WEIBO_AI_BRIDGE_SCOPE`（Linux）
- `WEIBO_AI_BRIDGE_SERVICE_USER`（Linux system scope）

说明：
- 统一入口脚本会根据系统自动选择 Linux `systemd` 或 macOS `launchd`
- 服务进程通过 `CONFIG_PATH` 读取 TOML，并按现有逻辑自动尝试加载 `.env`
- 模板文件位于 `deploy/weibo-ai-bridge.service.tmpl` 与 `deploy/com.weibo-ai-bridge.plist.tmpl`

### 安全自升级

在微博私信里发送：

```text
/upgrade
```

服务会先读取本地二进制的 commit，再查询 GitHub 目标 ref 的 commit；两边一致时直接回复“已经是最新版本”，不下载、不编译、不重启。只有版本不一致时，才会下载 GitHub 最新代码，编译并原子替换当前二进制；成功回复用户之后，再由后台任务延迟重启服务。

不要在 Agent 普通对话里直接执行 `scripts/service.sh restart`、`systemctl restart` 或 `launchctl bootout`，这些命令会先杀掉承载当前回复的 bridge 进程，导致升级流程和对话中断。

可选用法：

```text
/upgrade --ref v1.2.3
/upgrade --ref main
```

也可以在 shell 中手动运行：

```bash
scripts/self-update.sh
scripts/self-update.sh --ref main
```

### 安装/修复内置微博 Skills

内置 `weibo-skill-api` 是 bridge 联通微博能力的主要入口。安装后，Claude/Codex/Hermes/Gemini 可以在对话中调用同一套微博能力，包括：

- 热搜榜与微博智搜
- 微博状态查询
- 超话发帖、评论、点赞等互动
- 图片/视频上传
- 定时任务
- 创作者数据摘要

```bash
bash scripts/install-skills.sh
```

`scripts/install.sh` 在安装 `weibo-ai-bridge` 时会自动安装内置 skills 到：
- `~/.codex/skills/weibo-skill-api`
- `~/.claude/skills/weibo-skill-api`
- `~/.hermes/skills/weibo-skill-api`
- `~/.gemini/skills/weibo-skill-api`

如果只是本地 `make build`，可以用上面的 `scripts/install-skills.sh` 手动安装或修复。安装后的 skill 会自动复用 bridge 的微博配置与 token 缓存，不需要在各 Agent 侧重新配置微博 App ID、App secret 或单独的 token 文件。

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
| Hermes 不可用 | 确认 `hermes --version` 和 `hermes acp` 可用；若新 Hermes ACP 会话仍返回 404，优先检查 Hermes 当前 provider/model/deployment 配置 |
| Gemini 不可用 | 确认 `gemini --version` 可用；若提示缺少凭证，检查本机 Gemini CLI 登录状态或 `GEMINI_API_KEY` |
| WebSocket 断连 | 检查网络、Token 是否过期、心跳配置 |
| 会话丢失 | 检查 `SESSION_TIMEOUT` 和 `SESSION_STORAGE_PATH` |
| 消息处理超时 | 增加超时时间，检查 Agent 服务可用性 |

详细日志：`export LOG_LEVEL="debug"`

## 项目结构

```
weibo-ai-bridge/
├── cmd/server/               # 服务入口
│   └── main.go               # HTTP 服务、消息排队、平台生命周期
├── cmd/test-report/          # 可读测试报告生成工具
├── router/                   # 消息路由
│   ├── router_core.go        # Router 类型、Handle 主入口
│   ├── router_stream.go      # 统一流式路径、forwardStreamToPlatform
│   ├── router_agent.go       # Agent 选择与调用
│   ├── router_interactive.go # 交互式会话管理、liveSessions
│   ├── router_approval.go    # 审批提示与同义词解析
│   ├── router_bytheway.go    # /btw 插话
│   ├── stream_sender.go      # 流式分片发送器、边界感知 flush
│   ├── agent_repair.go       # Agent 可用性自动修复
│   ├── native_sessions.go    # 原生会话扫描（.jsonl、sessions-index、history.jsonl）
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
│   ├── hermes.go             # Hermes ACP 交互式会话与 CLI fallback
│   ├── gemini.go             # Gemini stream-json 流式执行
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
├── deploy/                   # systemd/launchd 模板
├── scripts/                  # 安装与服务管理脚本
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
