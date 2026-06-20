# Weibo AI Bridge

微博私信与本地 AI Agent CLI 的桥接服务。它通过微博开放平台 WebSocket API 接收私信，把消息路由到 Claude Code、Codex CLI、Hermes CLI 或 Gemini CLI，并把 Agent 输出流式回传到微博。

这个项目适合把一台本地机器上的编码 Agent、审批确认、项目会话和微博私信入口连在一起。内置的 `weibo-skill-api` 还可以让 Agent 复用同一套微博凭证，查询热搜/智搜、检查微博状态、参与超话互动、上传媒体、管理定时任务，并生成创作者数据摘要。

## 核心特性

- **微博私信桥接**：实时收发微博私信，长回复会按自然边界分片回传。
- **多 Agent 支持**：支持 Claude Code、Codex CLI、Hermes CLI 和 Gemini CLI，可在会话中切换。
- **原生会话续接**：优先使用各 Agent 自己的 session/thread ID，Bridge 只维护索引、活动会话和必要上下文。
- **交互式审批**：Agent 请求授权时，可直接回复 `允许`、`取消` 或 `允许所有`。
- **实时补充与监听**：`/btw` 可向正在运行的 turn 注入补充说明；`/listen` 可旁听本机已有原生会话日志。
- **简洁模式与 Super 模式**：`/simple` 控制是否只发送最终回复；`/super` 开启当前会话的自动审批与对侧 Agent 复盘。
- **安全自升级**：`/upgrade` 会先比较本地和目标 Git commit，只有确实有新版本时才构建替换并延迟重启。
- **本地调试接口**：提供 `/health`、`/stats` 和 `/chat/stream`。

## 快速开始

### 前置要求

- Go 1.22+
- 微博开放平台 App ID / App Secret
- 至少安装并配置一个 Agent CLI：`claude`、`codex`、`hermes` 或 `gemini`

### 本地运行

```bash
git clone https://github.com/kangjinshan/weibo-ai-bridge.git
cd weibo-ai-bridge

cp .env.example .env
```

编辑 `.env`，至少填入微博凭证，并启用一个 Agent：

```dotenv
WEIBO_APP_ID=your-app-id
WEIBO_APP_SECRET=your-app-secret

CLAUDE_ENABLED=true
CODEX_ENABLED=false
HERMES_ENABLED=false
GEMINI_ENABLED=false
```

构建并运行：

```bash
make build
./build/weibo-ai-bridge
```

开发时也可以使用：

```bash
make dev
```

服务默认监听 `127.0.0.1:5533`。启动成功后，Bridge 会连接微博 WebSocket，并给 bot 自己发送一条启动通知。

### 安装为服务

Linux 和 macOS 使用统一脚本：

```bash
bash scripts/install.sh
scripts/service.sh start
scripts/service.sh status
scripts/service.sh logs
```

Windows 11 原生运行：

```powershell
go build -o build\weibo-ai-bridge.exe .\cmd\server
.\build\weibo-ai-bridge.exe
```

安装为 Windows Service：

```powershell
.\scripts\service.ps1 install
.\scripts\service.ps1 start
.\scripts\service.ps1 logs
```

如果 Agent CLI 的登录态只存在于当前桌面用户环境中，优先前台运行；需要后台服务时，请确保服务账号也完成 Claude/Codex/Hermes/Gemini CLI 配置。

## 用户命令

| 命令 | 说明 |
|------|------|
| `/help` | 显示帮助 |
| `/new [claude\|codex\|hermes\|gemini]` | 准备下一条消息使用的新原生会话 |
| `/list` | 查看可切换的原生会话列表 |
| `/switch <编号>` | 切换到 `/list` 中的会话 |
| `/<编号>` | 等价于 `/switch <编号>` |
| `/switch <agent>` | 切换当前会话的 Agent 类型 |
| `/claude`、`/codex`、`/hermes`、`/gemini` | 快速切换 Agent |
| `/dir [path]` | 查看或设置当前会话工作目录 |
| `/model` | 显示当前模型 |
| `/status` | 显示当前会话状态 |
| `/btw <内容>` | 向当前运行中的交互式 turn 注入补充说明 |
| `/listen [编号]` | 监听当前或指定原生会话日志，不向 Agent 发送输入 |
| `/unlisten` | 停止监听 |
| `/simple [on\|off\|status]` | 管理简洁模式 |
| `/super [on\|off\|status]` | 管理 Super 模式和当前会话的 `Allow All` |
| `/upgrade [--ref branch\|tag]` | 比对、构建并延迟重启到指定版本 |

授权提示出现时，可以直接回复：

| 类别 | 支持的回复 |
|------|-----------|
| 允许 | `允许` / `同意` / `可以` / `好` / `yes` / `y` / `ok` / `approve` / `allow` |
| 取消 | `取消` / `拒绝` / `不允许` / `no` / `n` / `reject` / `cancel` |
| 允许所有 | `允许所有` / `允许全部` / `全部允许` / `allow all` / `approve all` / `yes all` |

`允许所有` 只对当前会话生效。Claude 发起结构化选择题时，Bridge 会把选项渲染成编号文本；回复编号即可选择，多选题可用逗号分隔。

## 微博能力

内置 `skills/weibo-skill-api/` 是 Agent 使用微博开放能力的入口。安装后，Claude/Codex/Hermes/Gemini 会拿到同一套 skill 文档和脚本，并复用 Bridge 的微博 App ID / App Secret 与 token 缓存，不需要在各 Agent 侧单独登录。

主要能力包括：

- 热搜榜、分频道热搜和微博智搜摘要
- 微博列表与单条微博状态查询
- 超话发帖、评论、回复、点赞、评论查询和置顶帖查询
- 图片/视频上传
- 微博定时任务
- 创作者数据摘要、金橙 V 升级分析、V 榜分析、粉丝群分析和内容效率分析

安装或修复内置 skills：

```bash
bash scripts/install-skills.sh
```

`scripts/install.sh` 会在安装 Bridge 时自动同步这些 skills 到 `~/.codex/skills/`、`~/.claude/skills/`、`~/.hermes/skills/` 和 `~/.gemini/skills/`。

## 配置

配置优先级：

```text
环境变量 > TOML 配置文件 > 默认值
```

启动时会自动尝试读取 `.env`，也可以用 `CONFIG_PATH` 指定 TOML 配置文件。完整示例见 `.env.example` 和 `config/config.example.toml`。

### 常用环境变量

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `WEIBO_APP_ID` | 微博应用 ID | 必填 |
| `WEIBO_APP_SECRET` | 微博应用密钥，兼容旧名 `WEIBO_APP_Secret` | 必填 |
| `SERVER_PORT` | HTTP 端口 | `5533` |
| `HTTP_API_KEY` | `/stats`、`/chat/stream` 的 Bearer Token，留空不启用认证 | 空 |
| `CONFIG_PATH` | TOML 配置路径 | `config/config.toml` |
| `CLAUDE_ENABLED` | 启用 Claude | `true` |
| `CODEX_ENABLED` | 启用 Codex | `false` |
| `HERMES_ENABLED` | 启用 Hermes | `false` |
| `GEMINI_ENABLED` | 启用 Gemini | `false` |
| `SESSION_STORAGE_PATH` | 会话索引持久化目录 | `~/.config/weibo-ai-bridge/sessions` |
| `LOG_LEVEL` | 日志级别 | `info` |
| `LOG_FORMAT` | 日志格式 | `json` |
| `LOG_OUTPUT` | 日志输出位置 | `stdout` |

Agent 的 API Key、模型和 provider 通常由各自 CLI 管理。除非明确需要覆盖，`CODEX_MODEL`、`HERMES_MODEL`、`HERMES_PROVIDER`、`GEMINI_MODEL` 建议留空，沿用本机 CLI 默认配置。

### TOML 示例

```toml
[platform.weibo]
app_id = "your-app-id"
app_secret = "your-app-secret"

[agent.claude]
enabled = true

[agent.codex]
enabled = false
model = ""

[agent.hermes]
enabled = false
model = ""
profile = ""
provider = ""

[agent.gemini]
enabled = false
model = ""

[session]
timeout = 3600
max_size = 1000
storage_path = "~/.config/weibo-ai-bridge/sessions"

[http]
port = "5533"
api_key = ""

[log]
level = "info"
format = "json"
output = "stdout"
```

## HTTP 调试接口

| 接口 | 方法 | 说明 |
|------|------|------|
| `/health` | GET | 健康检查 |
| `/stats` | GET | 统计信息 |
| `/chat/stream` | GET/POST | SSE 调试流 |

设置 `HTTP_API_KEY` 后，`/stats` 和 `/chat/stream` 需要携带 `Authorization: Bearer <api_key>`；`/health` 始终不需要认证。

推荐使用 POST 调试 `/chat/stream`：

```bash
curl -N \
  -H "Authorization: Bearer your-secret-key" \
  -H "Content-Type: application/json" \
  -d '{"user_id":"123456","content":"请用中文写三段文字","session_id":"optional-session-id"}' \
  http://127.0.0.1:5533/chat/stream
```

SSE 事件类型包括 `session`、`delta`、`message`、`approval`、`tool_start`、`tool_end`、`error` 和 `done`。

## 部署与升级

常用服务命令：

```bash
scripts/service.sh install
scripts/service.sh start
scripts/service.sh restart
scripts/service.sh stop
scripts/service.sh status
scripts/service.sh logs
```

Linux 可通过 `--scope system` 或 `--scope user` 选择 systemd 作用域；macOS 使用用户级 launchd；Windows 使用 `scripts/service.ps1` 安装到 Windows Service Control Manager。

在微博私信里发送 `/upgrade` 可以触发安全自升级：

```text
/upgrade
/upgrade --ref v1.2.3
/upgrade --ref main
```

Bridge 会先比较本地二进制 commit 与 GitHub 目标 ref commit；一致时直接回复已是最新，不会下载、编译或重启。版本不一致时才会编译替换，并在当前回复发出后安排延迟重启。

也可以在 shell 中手动运行：

```bash
scripts/self-update.sh
scripts/self-update.sh --ref main
```

Windows 11 原生运行暂不使用 `/upgrade` 的 shell 自升级链路；请重新构建 `build\weibo-ai-bridge.exe` 后用 `.\scripts\service.ps1 restart` 重启服务。

## 微博凭证获取

1. 在微博私信中找到“微博龙虾助手”，发送“连接龙虾”
2. 获取 App ID 和 App Secret
3. 填入 `.env` 或 `config/config.toml`

不要把 App Secret 提交到代码仓库；建议使用环境变量或仓库外配置文件管理敏感信息。

## 开发

常用命令：

| 命令 | 说明 |
|------|------|
| `make build` | 构建到 `build/weibo-ai-bridge` |
| `make build-linux` | 交叉编译 Linux AMD64 |
| `make build-windows` | 交叉编译 Windows AMD64 |
| `make test` | 运行测试，含 race 和覆盖率 |
| `make test-report` | 生成测试报告到 `reports/` |
| `make fmt` | 格式化代码 |
| `make lint` | 运行 golangci-lint |
| `make dev` | 构建并运行 |

面向编码代理和维护者的仓库结构、测试要求、实现约束和文档同步规则见 `AGENTS.md`。运行时微博 skill 的接入说明见 `skills/weibo-skill-api/`。

构建产物约定：

- `build/` 放本地构建产物
- `dist/` 放发布包
- 仓库根目录不放可执行文件

## 故障排除

| 问题 | 解决方法 |
|------|---------|
| 配置验证失败 | 检查 `WEIBO_APP_ID` 和 `WEIBO_APP_SECRET` |
| Claude 不可用 | 确认 `claude --version` 可用，且 Claude Code CLI 已完成认证 |
| Codex 不可用或模型不匹配 | 确认 `codex` 可用；优先让 `CODEX_MODEL` 留空，沿用 CLI 默认配置。Windows 11 下若 Store/MSIX 安装路径解析到 `Program Files\WindowsApps\...\app\resources\codex.exe` 并报 `Access is denied`，bridge 会优先避开包内 exe，改用可执行 shim 或 `cmd.exe /c codex` 入口 |
| Hermes 不可用 | 确认 `hermes --version` 和 `hermes acp` 可用 |
| Gemini 不可用 | 确认 `gemini --version` 可用，并检查 Gemini CLI 登录状态或 `GEMINI_API_KEY` |
| WebSocket 断连 | 检查网络、微博凭证和 token 状态 |
| 会话丢失 | 检查 `SESSION_STORAGE_PATH` 和服务运行账号 |

需要更详细日志时：

```bash
export LOG_LEVEL=debug
```

## 许可证

MIT License
