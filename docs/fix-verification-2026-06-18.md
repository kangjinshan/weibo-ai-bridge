# weibo-ai-bridge 修复核验文档

> 本文档列出本轮代码审查发现并修复的全部 19 个问题，供独立核验使用。
> 基线 commit：`9f939a9`（修复均为工作区未提交改动）
> 改动规模：25 个文件，+265 / -502 行
> 验证状态：`go build ./...` 通过、`go vet ./...` 无告警、`go test -race ./...` 全绿、`gofmt` 已格式化改动文件、`scripts/check-root-executables.sh` 通过

---

## 如何核验

```bash
cd /Users/kanayama/Desktop/AI/weibo-ai-bridge

# 1. 构建
go build ./...

# 2. 静态检查
go vet ./...

# 3. 全量测试（含竞态检测）
go test -race -count=1 ./...

# 4. 查看完整改动
git diff --stat
git diff
```

每个问题下方都标注了**文件:函数**与**核验要点**，可逐项 `git diff <file>` 对照。

---

## 问题分类总览

| 编号 | 类别 | 严重度 | 标题 |
|------|------|--------|------|
| B1 | Bug | 高 | Session.Get() 快照副本的 AgentType 修改未回写 Manager |
| B2 | Bug | 中 | messageProcessor.handle() 无 panic 恢复，panic 后用户永久卡死 |
| B3 | Bug | 低 | jsonLogWriter.Write 返回字节数不准确 |
| B4 | Bug | 低 | weibo.Platform.Start 硬编码 wg.Add(3) |
| D1 | 设计 | 中 | handleAIMessage 死代码（仅测试使用） |
| D2 | 设计 | 中 | Agent.Execute 非流式方法生产无用途 |
| D3 | 设计 | 中 | oppositeAgentType 只支持 claude↔codex，hermes/gemini 静默跳过 peer review |
| D4 | 设计 | 中 | /simple 模式错误处理顺序不一致 |
| D5 | 设计 | 高 | 微博平台使用过时的 x/net/websocket 库 |
| D6 | 设计 | 中 | PlatformInterface / streamingPlatformInterface 接口分裂 |
| D7 | 设计 | 中 | queuedMessages 队列无容量上限 |
| D8 | 设计 | 低 | 启动通知 goroutine 未在关停前检查 ctx |
| D9 | 设计 | 低 | listenRuns / superReviews 后台任务未与 Router.Close 协调 |
| Q1 | 质量 | 低 | 根目录散落构建产物未进 .gitignore |
| Q2 | 质量 | 低 | ClaudeConfig.APIKey / Model 死字段 |
| Q3 | 质量 | 低 | resolveTextDelta / resolveDeltaFromSnapshot 重复实现 |

> 说明：原审查列出 19 项，部分质量项合并表述。下方按修复批次（Batch 1–6）展开，每项含**问题描述 / 修复方案 / 核验要点**。

---

## Batch 1：一次性小修复

### B3 — jsonLogWriter.Write 返回字节数不准确

- **文件**：`cmd/server/main.go` → `jsonLogWriter.Write`
- **问题**：JSON 序列化成功时返回 `len(p)`（原始输入长度），但实际写入的是 JSON 编码后的 `data`（长度不同）。`io.Writer` 约定要求返回实际写入字节数，返回值错误可能误导调用方。
- **修复**：成功路径返回实际写入的 `n`。
- **核验要点**：
  - `Write` 成功分支应为 `return n, nil`，而非 `return len(p), nil`。
  - 对应测试 `cmd/server/main_test.go: TestJSONLogWriterWrapsLogLine` 已从 `assert.Equal(len("hello\n"), n)` 改为 `assert.Greater(n, 0)`（因为返回值现在是 JSON 编码后的长度，必然大于原文）。

### B4 — weibo.Platform.Start 硬编码 wg.Add(3)

- **文件**：`platform/weibo/client.go` → `Start`
- **问题**：启动 3 个 goroutine 前用 `p.wg.Add(3)` 硬编码计数。后续增减 goroutine 时易遗漏更新，导致 `wg.Wait()` 永久阻塞或提前返回。
- **修复**：改为每个 goroutine 各自 `p.wg.Add(1)`。
- **核验要点**：`Start` 中应有 3 处独立的 `p.wg.Add(1)`，分别紧邻 heartbeatLoop / messageLoop / closeConnection 三个 goroutine。

### Q1 — 根目录产物未进 .gitignore

- **文件**：`.gitignore`
- **问题**：`app-session-*.js` 等运行产物可能被误提交。
- **修复**：新增 `app-session-*.js` 忽略规则（`*.log`、`*.out`、`build/` 等已有规则覆盖其余产物）。
- **核验要点**：`.gitignore` 末尾「Temporary files」段含 `app-session-*.js`。

---

## Batch 2：Session 变异 Bug + Panic 恢复

### B1 — Session.Get() 快照副本的 AgentType 修改未回写（高优先级）

- **文件**：`router/router_agent.go` → `resolveAgentExecution`
- **问题**：`session.Manager.Get()` 经 `detachedSessionFromSnapshot` 返回的是**独立副本**，不是内部存储指针。`resolveAgentExecution` 中当会话来自 `GetActiveSession()`（也走 Get → 副本）时，`currentSession.AgentType = agentType` 只改了副本，Manager 内部的原始会话 AgentType 仍为空。下次解析会重复落空，AgentType 始终无法持久化。
- **修复**：先调用 `r.sessionMgr.SetSessionAgentType(currentSession.ID, agentType)` 把变更写回 Manager（已存在的方法，内部加锁+持久化），再同步本地副本 `currentSession.AgentType = agentType` 以便本函数后续读取一致。
- **核验要点**：
  - `resolveAgentExecution` 中 `if strings.TrimSpace(currentSession.AgentType) == ""` 块内应同时有 `SetSessionAgentType(...)` 调用和本地赋值。
  - 可核验 `session.Manager.SetSessionAgentType`（`session/session.go`）确实对内部 `m.sessions[id]` 加锁修改并 `saveSessionLocked`。
  - 注意：其它 `Get()` 后修改的点（`bindAgentNativeSessionID`、`clearAgentNativeSessionID`）原本已通过 `UpdateSession` 回写，无此问题。

### B2 — messageProcessor.handle() 无 panic 恢复

- **文件**：`cmd/server/main.go` → `messageProcessor.handle`
- **问题**：若 `router.HandleMessage` panic，goroutine 直接死亡，不会执行 `cancel()`/`endRun`/`clearUser`。`inFlightUsers[userID]` 和 `activeRuns[userID]` 永久残留，该用户后续消息全部排队但无 goroutine 消费 → 永久卡死。
- **修复**：在 `handle` 顶部加 `defer recover`，捕获 panic 后记录日志并调用 `clearUser(userID)` 清理该用户全部状态。userID 在循环前取出，避免 defer 捕获到被循环重新赋值的 `current`。
- **核验要点**：
  - `handle` 函数体开头有 `userID := msg.UserID` 和 `defer func(){ if r := recover(); r != nil { ...; p.clearUser(userID) } }()`。
  - `clearUser` 会 `delete` inFlightUsers / activeRuns / queuedMessages / lastQueueNotice 四个 map。

---

## Batch 3：生命周期协调与队列安全

### D9 — Router.Close 未取消后台任务

- **文件**：`router/router_core.go` → `Close`
- **问题**：`Close()` 原本只关闭 `liveSessions`，`listenRuns`（/listen 监听）和 `superReviews`（/super peer review）两类后台 goroutine 仅依赖 `rootCtx` cancel 链路。若关停顺序导致连接先关、cancel 后到，后台任务可能仍向已关闭的平台发送消息。
- **修复**：`Close()` 中在 `rootCancel()` **之前**，先遍历 `listenRuns` 和 `superReviews` 调用各自 `cancel()` 并清空 map。
- **核验要点**：`Close` 的 `closeOnce.Do` 内，按「先 listenRuns/superReviews 取消 → 再 rootCancel → 再关 liveSessions」顺序执行；两个 map 遍历各自持有对应的 `listenMu` / `superReviewMu`。

### D8 — 启动通知未在发送前检查 ctx

- **文件**：`cmd/server/main.go` → `sendStartupNotificationAfterDelay`
- **问题**：启动 2s 后发送的「服务启动成功」通知，若此时 SIGTERM 已到达、平台已关停，仍可能尝试 `platform.Reply`。
- **修复**：在 `platform.Reply(...)` 调用前增加 `if ctx.Err() != nil { return }` 兜底检查。
- **核验要点**：`msg := fmt.Sprintf("✅ 服务启动成功...")` 之后、`platform.Reply` 之前有独立的 `if ctx.Err() != nil { return }`（注意它在 `if err := platform.Reply` 之外，不是嵌在里面）。

### D7 — queuedMessages 无容量上限

- **文件**：`cmd/server/main.go` → 常量区 + `enqueue`
- **问题**：`queuedMessages` 是 `map[string][]*weibo.Message`，无上限。某用户狂发消息而 AI 处理慢时队列无限增长，存在内存风险。
- **修复**：新增常量 `maxQueuedPerUser = 10`；`enqueue` 中 append 后若队列长度超过上限，丢弃最旧的一条（`queue[1:]`）。
- **核验要点**：
  - 常量区有 `maxQueuedPerUser = 10`。
  - `enqueue` 的「已在处理中」分支：append 后判断 `len > maxQueuedPerUser` 则 `[1:]` 丢首元素。
  - 行为权衡：保留最新 10 条、丢最旧（FIFO 截断）。

---

## Batch 4：消息传输行为修正

### B/D（双重分片）— 移除 sendReply 的 1000-rune 预分片

- **文件**：`router/router_stream.go` → `sendReply`
- **问题**：`sendReply` 先 `splitMessage(content, 1000)` 分片逐片发送，而每片再经 `weibo.Platform.Reply` 内部的 `splitContent(..., 4000)` 二次分片。1000 这层是冗余的——流式发送器 `streamReplySender` 已做边界感知 flush，平台层也有 4000 切分。两层阈值（1000 vs 4000）不一致且令人困惑。
- **修复**：`sendReply` 直接 `r.platform.Reply(ctx, userID, content)`，由平台层统一按 4000-rune（rune 安全）切分。`splitMessage` 函数本身保留（仍被 `TestSplitMessage` 使用）。
- **核验要点**：
  - `sendReply` 仅剩 platform == nil 检查 + 一行 `return r.platform.Reply(...)`，不再调用 `splitMessage`。
  - 测试相应调整：`router/router_test.go` 的 `TestSendReply` 重写为 `TestSendReply_DirectPlatformCall`（断言一次 Reply）；新增独立文件 `router/sendreply_test.go` 覆盖短消息/长消息/平台未设置三种情况，断言 `platform.replies` 长度为 1。
  - rune 安全切分逻辑仍在 `platform/weibo/client.go: splitContent`（核验其按 `[]rune` 切分，且优先在换行处断开）。

### D4 — /simple 模式错误处理顺序不一致

- **文件**：`router/router_stream.go` → `forwardSimpleStreamToPlatform`
- **问题**：simple 模式下，delta 被静默缓冲到最后统一发送，但 EventTypeError 却**立即** `sendFinal`。导致用户可能先看到错误、再看到最终回复，时序混乱。
- **修复**：EventTypeError 不再立即发送，仅记录 `streamErr`；`finalText()` 在有 `streamErr` 时返回 `"AI execution failed: " + streamErr.Error()`；统一在 EventTypeDone / 流关闭时通过 `sendFinal(finalText())` 发送（移除原先 `streamErr == nil` 的判断分支，保证错误也走最终交付点）。
- **核验要点**：
  - EventTypeError 分支内只剩 `streamErr = errors.New(event.Error)`，无 `sendFinal` 调用。
  - `finalText()` 顶部优先判断 `if streamErr != nil { return "AI execution failed: " + ... }`。
  - EventTypeDone 与「流关闭(`!ok`)」分支都无条件调用 `sendFinal(finalText())` 后 `settle()`。

### D3 — oppositeAgentType 只支持 claude↔codex

- **文件**：`router/router_agent.go` → `oppositeAgentType` / `launchSuperPeerReview`；`router/super_mode.go`
- **问题**：`oppositeAgentType` 只映射 claude↔codex，hermes/gemini 返回空串。当主 Agent 是 hermes/gemini 且开启 super 模式时，peer review 被**静默跳过**，用户无任何提示。且 super_mode 的反馈键只为 claude/codex 定义，hermes/gemini 无法存取反馈。
- **修复**：
  1. `oppositeAgentType` 增加 `hermes→gemini`、`gemini→hermes` 配对。
  2. `super_mode.go` 增加 `superFeedbackForHermesKey/ForGeminiKey` 及对应 ready 键；新增 `superFeedbackAgentKeys` 切片统一枚举四个 agent 的键；`superFeedbackKeyForAgent` / `superFeedbackReadyKeyForAgent` / `setSuperMode`（关闭时清理）/ `clearAllSuperFeedback` 全部覆盖四个 agent。
  3. `launchSuperPeerReview`：当 `oppositeAgentType(currentAgentType) == ""` 时，发送通知「Super：当前代理没有对侧代理，已跳过复盘。」并提前返回（避免空转 goroutine）。
- **核验要点**：
  - `oppositeAgentType` 四分支齐全。
  - `super_mode.go` 有 4 组 feedback/ready 常量 + `superFeedbackAgentKeys` 切片；`setSuperMode`/`clearAllSuperFeedback` 用切片遍历清理（不再逐 agent 硬编码）。
  - `launchSuperPeerReview` 在 `beginSuperPeerReview` 之前有空对侧的提示+return。

---

## Batch 5：死代码清理与接口整合

### D1 — 移除 handleAIMessage 死代码

- **文件**：`router/router_agent.go`（删函数）、`router/router_test.go`（重写测试）
- **问题**：`handleAIMessage`（非流式路径，调用 `Agent.Execute`）生产代码零调用，仅 7 个测试用例使用。生产主流程只走 `streamAIMessage`。保留废弃路径增加维护负担。
- **修复**：删除 `handleAIMessage`（约 58 行）。测试改造：在 `router_test.go` 新增测试辅助函数 `collectAIMessage(r, ctx, msg)`，内部跑 `streamAIMessage` 并把事件流收集成与旧 `Response` 等价的结构；原 7 个 `TestHandleAIMessage*` 用例改为调用该辅助函数，断言逻辑（会话类型、session ID 持久化、标题仅取首条等）保持不变。
- **核验要点**：
  - `router_agent.go` 中已无 `handleAIMessage` 定义。
  - `router_test.go` 有 `collectAIMessage` 辅助函数，7 处调用点从 `router.handleAIMessage(...)` 改为 `collectAIMessage(router, ...)`。
  - 这些测试仍验证同样的行为契约（grep `TestHandleAIMessage` 仍能看到用例名，断言未削弱）。

### D2 — 移除 Agent.Execute 非流式方法

- **文件**：`agent/agent.go`（接口）、`agent/claude.go`、`agent/codex.go`、`agent/hermes.go`、`agent/gemini.go`（4 个实现）、各测试文件、`router/router_test.go`、`cmd/server/main_test.go`
- **问题**：`Agent.Execute` 唯一生产调用方是 `handleAIMessage`（已删）。各实现本质是消费 `ExecuteStream` 再拼回字符串，属冗余间接层。
- **修复**：
  1. 从 `agent.Agent` 接口删除 `Execute` 方法。
  2. 删除 4 个 Agent 实现的 `Execute`（claude/codex/hermes/gemini 各约 40 行）。
  3. 删除 mock 实现的 `Execute`：`router/router_test.go` 的 `MockAgent`（同时删除已无用的 `executeFn` 字段）和 `MockInteractiveAgent`、`agent/manager_test.go` 的 `MockAgent`、`cmd/server/main_test.go` 的 `sseTestAgent`。
  4. 直接调用 `Execute` 的测试改用 `ExecuteStream` + 遍历事件：`agent/agent_test.go`、`agent/claude_test.go`（`TestClaudeCodeAgent_Execute`→`_ExecuteStream`）、`agent/codex_test.go`（同改名）。
- **核验要点**：
  - `agent/agent.go` 的 `Agent` 接口只剩 `Name()` / `ExecuteStream()` / `IsAvailable()`。
  - `grep -rn "func.*Execute(ctx" agent/ router/ cmd/` 不应再有 `(string, error)` 签名的 Execute（仅 `ExecuteStream`）。
  - `agent/codex.go` 的辅助函数 `uniqueNonEmpty` / `joinNonEmpty` 仍被 `ExecuteStream` 路径使用，保留是正确的。

### Q2 — ClaudeConfig 死字段

- **文件**：`config/config.go`、`cmd/server/main.go`、`config/*_test.go`
- **问题**：`ClaudeConfig.APIKey` 代码从不读取；`ClaudeConfig.Model` 默认值 `claude-3-5-sonnet-20241022` 已过时且 Claude Agent 不使用它（模型由 Claude CLI 自管）。属配置幻觉，误导开发者。
- **修复**：从 `ClaudeConfig` 删除 `APIKey` 和 `Model` 字段（仅留 `Enabled`）；`defaultConfig()` 删除 Model 默认值；`cmd/server/main.go` 注册日志从 `model=%s` 改为不带 model。
- **核验要点**：
  - `ClaudeConfig` 结构体只剩 `Enabled bool`。
  - `defaultConfig` 的 Claude 段只设 `Enabled: true`。
  - 无 `CLAUDE_API_KEY`/`CLAUDE_MODEL` 环境变量读取（本就没有，确认未引入）。
  - config 包测试中 `ClaudeConfig{...}` 字面量只设 Enabled，编译通过。

### Q3 — resolveTextDelta 重复实现

- **文件**：`agent/claude.go`（导出）、`router/stream_sender.go`（改为调用）、两处 `resolve_delta_test.go`
- **问题**：`agent/claude.go: resolveTextDelta` 与 `router/stream_sender.go: resolveDeltaFromSnapshot` 逻辑几乎完全相同（按 UTF-8 rune 比较增量），名字不同。修一处易漏另一处。
- **修复**：
  1. `agent/claude.go` 新增导出函数 `ResolveTextDelta`，原 `resolveTextDelta` 改为薄封装调用它。
  2. `router/stream_sender.go` 的 `resolveDeltaFromSnapshot` 改为薄封装 `return agent.ResolveTextDelta(...)`；实际调用点（`PushPartialSnapshot`）直接用 `agent.ResolveTextDelta`。无包循环（agent 不依赖 router）。
- **核验要点**：
  - `agent/claude.go` 有导出的 `ResolveTextDelta`（rune 安全：用 `utf8.DecodeRuneInString` 逐 rune 比较）。
  - `router/stream_sender.go` 不再有独立的 rune 比较实现；`resolveDeltaFromSnapshot` 仅一行转发（为兼容现有测试保留）。
  - `agent/resolve_delta_test.go` 和 `router/resolve_delta_test.go` 仍各自存在且通过（满足 AGENTS.md「两包各一份测试」约束）。

### D6 — Platform 接口分裂

- **文件**：`router/platform_iface.go`、`router/router_core.go`、`router/router_stream.go`、`router/stream_sender.go`、`router/router_test.go`、`router/listen_test.go`
- **问题**：Router 把 `platform` 存为 `PlatformInterface`（仅 `Reply`），运行时又类型断言到 `streamingPlatformInterface`（`OpenReplyStream`）。实际唯一实现 `weibo.Platform` 同时满足两者，断言永远成功，分裂带来散落的类型断言和无意义的 `legacyStreamReplyWriter` 兜底。
- **修复**：
  1. `platform_iface.go`：统一 `Platform` 接口为 `Reply` + `OpenReplyStream` 两方法的合集（带编译期校验 `var _ Platform = (*weibo.Platform)(nil)`）。
  2. `router_core.go`：删除 `PlatformInterface` 和 `streamingPlatformInterface`；`Router.platform` 字段类型改为 `Platform`；`NewRouter` 参数类型改为 `Platform`。
  3. `router_stream.go`：`openStreamWriter` 简化为直接 `return r.platform.OpenReplyStream(...)`，移除类型断言与 legacy 兜底。
  4. `stream_sender.go`：删除 `legacyStreamReplyWriter` 类型及其方法。
  5. 测试适配：`listen_test.go` 的 `listenTestPlatform` 增加 `OpenReplyStream`（返回记录 chunk 的 `listenTestStream`）以满足新接口；删除 `TestLegacyStreamReplyWriterSkipsEmptyDoneChunk`。
- **核验要点**：
  - `grep -rn "PlatformInterface\|streamingPlatformInterface\|legacyStreamReplyWriter"` 在非测试代码中应无残留。
  - `Platform` 接口含 `Reply` 与 `OpenReplyStream`；`weibo.Platform` 编译期校验存在。
  - `openStreamWriter` 仅一行。
  - 所有 `NewRouter(...)` 调用方（生产 `cmd/server/main.go`、各测试 mock）均满足新接口（mock 同时实现两方法）。

---

## Batch 6：WebSocket 库迁移

### D5 — 微博平台迁移到 gorilla/websocket（高风险）

- **文件**：`platform/weibo/client.go`、`platform/weibo/weibo_test.go`、`go.mod`、`go.sum`
- **问题**：`platform/weibo` 用 `golang.org/x/net/websocket`（Go 旧实现，不支持读写分离、`WriteControl` 等），而 `platform/local` 已用功能更强的 `github.com/gorilla/websocket`。两库并存增加维护负担。
- **修复**：
  - import 由 `golang.org/x/net/websocket` 换为 `github.com/gorilla/websocket`。
  - `connect()`：用 `websocket.DefaultDialer.Dial(url, header)` 替换 `NewConfig`+`DialConfig`，Origin 经 header 传入，并 `resp.Body.Close()`。
  - `sendChunk()` / `heartbeatLoop()` / `sendJSONPong()`：`websocket.Message.Send(conn, string(data))` → `conn.WriteMessage(websocket.TextMessage, data)`。
  - `messageLoop()`：`websocket.Message.Receive(conn, &data)` → `messageType, payload, err := conn.ReadMessage()`；新增 `if messageType != websocket.TextMessage { continue }` 只处理文本帧；`data := string(payload)`，pong 检测改为 `data == "pong"`。
  - 写操作仍在 `connMutex` 下串行（gorilla Conn 写非并发安全，现有锁已满足）。
  - 测试 `weibo_test.go`：三个用 ws 的用例改用 `httptest` + gorilla `Upgrader`（新增 `testUpgrader` 变量，CheckOrigin 放行）；客户端拨号改 `websocket.DefaultDialer.Dial`；服务端读改 `conn.ReadMessage()`。
  - `go mod tidy`：`golang.org/x/net` 已从直接依赖移除（go.mod / go.sum 相应更新）。
- **核验要点**：
  - `platform/weibo/client.go` 仅 import `github.com/gorilla/websocket`，无 `golang.org/x/net/websocket`。
  - 所有发送走 `WriteMessage(websocket.TextMessage, ...)`，接收走 `ReadMessage()` 且过滤非 TextMessage。
  - `go.mod` 的 require 块不再直接列 `golang.org/x/net`（`grep "golang.org/x/net" go.mod` 为空）。
  - `go test -race ./platform/weibo/` 通过（含 reconnect、Reply 流式帧、sendChunk 锁释放等用例）。
  - 全局 `grep -rn "golang.org/x/net" --include=*.go .` 无任何引用。

---

## 跨项依赖关系（核验顺序参考）

- B1 是优先级最高的正确性 bug（会话状态不一致）。
- D1（删 handleAIMessage）是 D2（删 Execute）的前置。
- 双重分片修复（Batch 4）是 D6（统一接口）的前置——两者都触及 Reply 路径。
- D5（WebSocket 迁移）独立于其它项但风险最高，最后执行。

## 已知未触碰项（非本次范围）

- `config/config.go`、`platform/local/frame.go` 存在**预先就有**的 gofmt 字段对齐差异（与本次改动无关），未格式化以保持改动聚焦。如需全库 `make fmt` 可另行处理。
- `agent/codex.go` 的 `uniqueNonEmpty`/`joinNonEmpty` 仍被 `ExecuteStream` 使用，保留。
- `sendChunk` 中 `time.Sleep(sendChunkDelay)`（100ms 节流）行为保持原样，本次未改。
