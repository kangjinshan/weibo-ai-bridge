# 代码审查修复清单 — 2026-05-26

本文件由 2026-05-26 全项目审查产出，按严重度排序。每个条目都说明：

- **位置**：file:line
- **问题描述**：当前代码做了什么
- **不修复会怎样**：保留这段代码会引发的具体后果
- **修复后效果**：改完之后系统行为变成什么样
- **修复步骤**：建议的最小改动
- **验证方式**：怎么证明改对了

执行原则：

1. 每修一条提交一个独立 commit，commit message 引用本文条目编号（如 `fix(weibo): P0-1 sendChunk 锁外 sleep`）。
2. 改完跑 `go test -race ./...`，并对相关包补/改测试。
3. 涉及 AGENTS.md 描述的约束（流式、锁顺序、ctx 透传）改完同步更新 AGENTS.md。
4. 任何一条评估后认为不应修，必须在 PR 描述里说明理由，不要静默跳过。

---

## 当前状态

更新时间：2026-05-26

| 条目 | 状态 | 备注 |
|---|---|---|
| P0-1 | 已修复 | `fa889f6`：`sendChunk` 发送后释放 `connMutex`，节流 sleep 移到锁外，并补回归测试。 |
| P0-2 | 已修复 | `fa889f6`：Agent 流式事件统一走 `emitOrCancel(ctx, ...)`，覆盖 Claude/Codex/Hermes/Gemini 及 app-server/interactive 路径。 |
| P0-3 | 已修复 | `fa889f6`：Hermes ACP 进程等待改为 `sync.Once`，stderr/readLoop 纳入 `WaitGroup`。 |
| P0-4 | 已修复 | `48d585e`：Router 增加 lifecycle ctx/`Close`，legacy `Handle` 与流式路径随 Router close 取消。 |
| P0-5 | 未修复 | `/listen` 与 `/super` 后台任务仍未统一挂到 Router 生命周期。 |
| P0-6 | 已修复 | `fa889f6`：`/new` 使用一次 session 锁原子切换 AgentType 并清空 native session id。 |
| P0-7 | 未修复 | 重连仍是固定 5s 等待，无退避/熔断。 |
| P0-8 | 已修复 | `48d585e`：主服务先 cancel+close Router，再 HTTP shutdown，最后停微博平台；关停后新 HTTP 请求返回 503。 |
| P0-9 | 已修复 | `5f1d1b4`：startup notification 改为主 ctx 派生，取消后不再发送。 |
| P1-1 | 已修复 | `5f1d1b4`：`GetOrCreateSession` 改为单个 `Manager.mu` 临界区内完成 get/create/active 更新。 |
| P1-2 ~ P1-3 | 未修复 | 保留待后续处理。 |
| P1-4 | 已修复 | `5f1d1b4`：`waitInteractiveEventsQuiesced` 增加 ctx 取消路径。 |
| P1-5 | 未修复 | 保留待后续处理。 |
| P1-6 | 已修复 | `5f1d1b4`：Codex state DB 的 `sqlite3` 调用增加 3s timeout。 |
| P1-7 | 未修复 | 保留待后续处理。 |
| P1-8 | 已修复 | `fa889f6`：token 日志只记录 redacted 长度，不再打印明文片段。 |
| P1-9 ~ P1-12 | 未修复 | 保留待后续处理。 |
| P2-1 ~ P2-8 | 未修复/按需 | 偏维护性或体验项。 |
| P2-9 | 已修复 | `fa889f6`：`readErr` 写入改为非阻塞 select，避免退出后阻塞。 |

已验证：

```bash
git diff --check
make test
go vet ./...
make build
go test -race ./agent ./platform/weibo ./router ./session -count=2
```

## P0 严重问题（数据竞争 / 资源泄漏 / 关停不安全）

### P0-1. `platform/weibo/client.go:380-394` `sendChunk` 持锁 sleep

**状态**：已修复（`fa889f6`）

**问题描述**
`sendChunk` 在持有 `connMutex` 的情况下执行 `websocket.Message.Send`，然后调用 `time.Sleep(sendChunkDelay)`（100ms）。`connMutex` 同时被心跳、读循环、重连、关停争抢。

**不修复会怎样**
- 长回复每个分片阻塞写锁 100ms，期间心跳无法发送，微博侧可能判定连接超时主动断开。
- `messageLoop` 的 `Receive` 不直接持锁，但 `closeConnection` 想拿到锁来关 socket 时会被阻塞；优雅关停最坏要等 `分片数 × 100ms` 才能开始。
- 在长输出 + 高并发场景下表现为 "心跳掉线 → 自动重连 → 上一轮还在写 → 数据错乱"。

**修复后效果**
心跳和关停不再被分片节流拖延；分片之间仍然有 100ms 间隔以保护微博限流。

**修复步骤**
```go
// sendChunk 末尾改为
p.connMutex.Lock()
if p.conn == nil {
    p.connMutex.Unlock()
    return fmt.Errorf("connection not established")
}
err := websocket.Message.Send(p.conn, string(data))
p.connMutex.Unlock()
if err != nil {
    return err
}
time.Sleep(sendChunkDelay)
return nil
```
不要再用 `defer Unlock`，确保 sleep 落在锁外。

**验证方式**
- `go test -race ./platform/weibo/...`
- 新增/调整测试：模拟两个 goroutine 并发调 `sendChunk` 与 `heartbeat`，断言心跳延迟 < 50ms。

---

### P0-2. Agent 流式发送 `events <- event` 无 ctx 兜底（goroutine 泄漏）

**状态**：已修复（`fa889f6`）

**问题描述**
`agent/codex.go:438` 的 `sendEvent`、以及 `claude.go` / `hermes.go` / `gemini.go` 中所有 `events <- ev` 都是无超时的阻塞写。下游 router 一旦慢消费且 ctx 已取消，生产者会永远卡在发送，`defer close(events)` 也不会执行。

**不修复会怎样**
- 用户在微博侧断开或 platform `Stop()` 触发 ctx cancel 后，agent 子进程的读循环 + 转发 goroutine 都不会退出。
- 长时间运行的服务会持续累积僵尸 goroutine 和子进程 stdout pipe，最终 OOM 或文件描述符耗尽。
- 关停时 `cmd/server/main.go` 的 `wg.Wait()` 永远等不到这些 goroutine，进程必须靠 SIGKILL 才能停。

**修复后效果**
任何一处 emit 都能在 ctx 取消时立刻退出，子进程被关、pipe 被关、`close(events)` 正常执行，关停秒级完成。

**修复步骤**
在 `agent/agent.go` 加公共 helper：
```go
// emitOrCancel 在 events 满且 ctx 未取消时阻塞；ctx 取消立刻返回 false。
func emitOrCancel(ctx context.Context, events chan<- Event, ev Event) bool {
    if ev.Type == "" {
        return true
    }
    select {
    case events <- ev:
        return true
    case <-ctx.Done():
        return false
    }
}
```
然后把 4 个 agent 文件里所有 `events <- ev` / `sendEvent(events, ev)` 改为 `if !emitOrCancel(ctx, events, ev) { return }`。注意 `sendEvent` 当前签名没有 ctx，需要把 ctx 传进去（`streamEvents`、`executeViaJSONCLI`、`executeViaAppServerTransport` 都有 ctx）。

**验证方式**
- `go test -race ./agent/...`
- 新增测试：构造一个不读 events 的 consumer + 立即 cancel ctx，断言生产 goroutine 在 100ms 内退出（`runtime.NumGoroutine` 对比）。

---

### P0-3. `agent/hermes.go` 双重 `cmd.Process.Wait()` + 裸 stderr goroutine

**状态**：已修复（`fa889f6`）

**问题描述**
- `Close()` 在 453 行调 `s.cmd.Process.Wait()`；`readLoop` 的 defer 在 521-525 行也调一次。
- `go io.Copy(io.Discard, stderr)` 没注册到 wg，生命周期靠 hermes 进程自然退出。

**不修复会怎样**
- Go 标准库对同一 Process 第二次 Wait 返回 `"Wait was already called"`，目前两边都忽略错误；行为依赖竞态，未来如果 Wait 改成阻塞收集 rusage 会出现死锁。
- stderr goroutine 在 hermes 进程僵死时永不退出，泄漏 goroutine + pipe。
- `Close()` 没等 stderr goroutine，可能在 pipe 被关闭前提前返回，触发 "use of closed file" warning。

**修复后效果**
Wait 只执行一次，stderr 由 readLoop 收尾或被 wg 等待；Close 返回时所有派生 goroutine 都已退出。

**修复步骤**
1. 给 `hermesSession` 加 `waitOnce sync.Once` 和 `waitErr error`，包装：
```go
func (s *hermesSession) waitProcess() error {
    s.waitOnce.Do(func() { s.waitErr = s.cmd.Wait() })
    return s.waitErr
}
```
两个 Wait 调用点都改用 `s.waitProcess()`。
2. 把 stderr goroutine 加进 `s.wg`：
```go
s.wg.Add(1)
go func() {
    defer s.wg.Done()
    io.Copy(io.Discard, stderr)
}()
```
在 `Close()` 末尾 `s.wg.Wait()`（注意 readLoop 也要在 wg 里）。

**验证方式**
- `go test -race ./agent/...`，重点跑 `hermes_test.go`。
- 新增测试：模拟 Hermes 进程在 readLoop 期间被外部 Kill，断言 Close 不报错且不重复 Wait。

---

### P0-4. Router 流式路径无 ctx 透传 + `Handle` 强制 `context.Background()`

**状态**：已修复（`48d585e`）

**问题描述**
- `router/router_core.go:143` `Handle` 接口创建 `ctx, _ := context.WithTimeout(context.Background(), 5*time.Minute)`，丢掉了上层 ctx。
- `router/router_stream.go:90` `forwardStreamToPlatform` 串行调 `SendChunk`，上游 events 缓冲只有 32，生产者一旦填满会反压。

**不修复会怎样**
- 客户端断开、platform `Stop()`、SIGTERM 都无法快速取消正在跑的流；最坏要等到 5min 超时。
- 配合 P0-2 一起，会让大量"已经没人要的回复"继续跑完整轮 agent + 微博分片。
- 测试里如果想验证 cancel 行为基本写不出来。

**修复后效果**
Router 持有 lifecycle context；platform 关停时所有正在跑的流式回复在 1 秒内被取消、agent 子进程被关。

**修复步骤**
1. 给 `Router` 增加字段 `rootCtx context.Context` + `rootCancel context.CancelFunc`，在 `NewRouter` 里初始化，`Close()` 里 cancel。
2. `Handle` 用 `context.WithTimeout(r.rootCtx, 5*time.Minute)` 而不是 `Background()`。
3. `forwardStreamToPlatform` 在 `for ev := range events` 循环里改成 `select { case ev, ok := <-events: ...; case <-ctx.Done(): return }`，并把 ctx cancel 信号往 events 生产端传（P0-2 已经处理生产端）。

**验证方式**
- `go test -race ./router/...`
- 新增测试：起一个 mock platform 让 `SendChunk` 永远阻塞，cancel ctx 后断言 `forwardStreamToPlatform` 在 100ms 内返回。

---

### P0-5. 后台监听 goroutine（`/listen`、`/super` peer review）用 `context.Background()`

**状态**：未修复

**问题描述**
- `router/listen.go:48,93` 启动 `listenRuns` 用独立 cancel；`super_mode.go` 的 peer review 同样。
- Router 没有任何全局 shutdown 路径来取消它们；`followJSONLSessionFile` 也不处理日志 rotate / 删除。

**不修复会怎样**
- 进程退出时这些 goroutine 留着 open 的文件句柄、TCP 连接、子进程引用，要么靠 OS 回收，要么变成 leak。
- 日志被 rotate 后 `tail` 一直 EOF 空转，用户 `/listen` 永远收不到新事件，必须 `/unlisten` + 再 `/listen` 才能恢复。

**修复后效果**
Router Close 时所有 listen/super 后台任务一起取消；rotate 后能自动 reopen 新文件。

**修复步骤**
1. 完成 P0-4 后，把 `listenRuns` / `superReviews` 的 ctx 都派生自 `r.rootCtx`：
```go
runCtx, cancel := context.WithCancel(r.rootCtx)
```
2. `followJSONLSessionFile` 在 EOF 时调用 `os.Stat(path)`，发现 inode 变化或 size 缩小，关掉旧 fd、重新 `os.Open(path)`。
3. `Router.Close()` 里 cancel rootCtx 后 `r.listenWG.Wait()`。

**验证方式**
- `go test -race ./router/...`
- 手动场景：跑 `/listen` 监听一个文件 → `mv file file.old && touch file && echo x >> file` → 应该能看到 `x`。

---

### P0-6. `router/command.go:213-224` `/new` 多次 `UpdateSession` 非原子

**状态**：已修复（`fa889f6`）

**问题描述**
`handleNew` 依次调用 `UpdateSession(agentType=...)`, `UpdateSession(claude_session_id="")`, `codex_session_id=""`, `hermes_session_id=""`, `gemini_session_id=""` 共 5 次。

**不修复会怎样**
其它 goroutine 在这 5 次写入之间读到的会话 state 处于不一致中间态，例如 agent 已切到 codex 但 claude_session_id 还在，可能导致下一条消息错误地尝试 resume 旧 native session。

**修复后效果**
`/new` 的状态切换对其它 reader 表现为原子可见。

**修复步骤**
session 包已有 `UpdateSessionContextAtomically`（见 `session/session.go`）。把 5 次写改成一次：
```go
err := h.sessionManager.UpdateSessionContextAtomically(userID, sessID, func(ctx map[string]string) {
    ctx["agentType"] = agentType
    delete(ctx, "claude_session_id")
    delete(ctx, "codex_session_id")
    delete(ctx, "hermes_session_id")
    delete(ctx, "gemini_session_id")
})
```

**验证方式**
- 更新 `router/command_test.go`，新增并发 reader 测试断言中间态不可见。
- `go test -race ./router/...`

---

### P0-7. `platform/weibo/client.go:497-506` 重连无退避无熔断

**状态**：未修复

**问题描述**
重连失败时固定 sleep 5s，无指数退避、无最大失败计数；`refreshToken` 失败也走同一分支。

**不修复会怎样**
- 微博服务 5xx 或鉴权拒绝时，bridge 会以 5s/次的频率持续刷 token 和重连，单日上万次请求，可能触发风控被永久封禁。
- 配置错误的 token 启动后无任何告警，仅日志刷屏。

**修复后效果**
错误指数退避 1s→2s→4s→...→30s 封顶；连续 N 次（建议 10 次）鉴权类错误后停止重连并告警。

**修复步骤**
```go
backoff := time.Second
authFailures := 0
for {
    if err := p.connect(); err != nil {
        if isAuthError(err) {
            authFailures++
            if authFailures >= 10 {
                p.logger.Printf("❌ 鉴权连续失败 %d 次，停止重连", authFailures)
                return
            }
        }
        select {
        case <-time.After(backoff):
        case <-ctx.Done():
            return
        }
        if backoff < 30*time.Second {
            backoff *= 2
        }
        continue
    }
    backoff = time.Second
    authFailures = 0
    // ... 进入正常消息循环
}
```

**验证方式**
- 新增 `client_reconnect_test.go` 测试用例：mock 鉴权失败，断言第 10 次后退出。
- 断言连续失败时间间隔符合指数序列。

---

### P0-8. `cmd/server/main.go:275-290` 关停顺序错误

**状态**：已修复（`48d585e`）

**问题描述**
当前顺序：`platform.Stop()` → `httpServer.Shutdown` → `cancel()`。HTTP Shutdown 期间仍可能受理 `/chat/stream` 请求，调用已经 stop 的 router/platform。

**不修复会怎样**
关停瞬间到来的请求会触发 nil 指针或 "use of closed network connection" panic；日志吵杂，systemd 会把这次关停记成 `failed`。

**修复后效果**
关停顺序：`cancel()`（让 router/agent 收尾） → `httpServer.Shutdown`（拒绝新请求、等老请求完成） → `platform.Stop()`（关 WebSocket）。无 panic，systemd 显示 clean exit。

**修复步骤**
重排 `processMessages` 关闭、Shutdown、Stop 调用顺序；并在 HTTP handler 入口判断 `select { case <-ctx.Done(): return 503 ; default: }`。

**验证方式**
- 新增 `cmd/server/main_test.go` 测试：起服务 → 同时发送 `/chat/stream` 请求和 SIGTERM → 断言无 panic、HTTP 返回 503 而不是连接重置。

---

### P0-9. `cmd/server/main.go:222-239` startup notification 使用 `context.Background()`

**状态**：已修复（`5f1d1b4`）

**问题描述**
启动后 2s 异步发送 "bridge 启动成功" 私信，goroutine ctx 是 `context.Background()`。

**不修复会怎样**
2s 内收到 SIGTERM 时，主流程已经在跑 `Stop()`、`closeConnection` 想拿 `connMutex`，而 notification goroutine 也想拿同一把锁去 send，可能死锁或写到已关 socket。

**修复后效果**
关停立刻取消 notification；不会与 closeConnection 抢锁。

**修复步骤**
把 notification goroutine 的 ctx 改为主 ctx（或派生自它），并在 send 之前 `select { case <-ctx.Done(): return ; default: }`。

**验证方式**
- 新增测试：启动后立即 cancel，断言 notification goroutine 在 100ms 内退出。

---

## P1 中等问题

### P1-1. `session/session.go:174-183` `GetOrCreateSession` TOCTOU

**状态**：已修复（`5f1d1b4`）

**问题描述**
内部依次 `getInternal` → `SetActiveSession` → `Create`，中间释放过锁。

**不修复会怎样**
并发同一 userID + sessionID 调用可能创建两次 session 或 active session 短暂为空。

**修复后效果**
原子的"读不到就建，建好就 active"，并发安全。

**修复步骤**
把三步合并到单一 `Manager.mu` 持锁段里完成；或用 `sync.Map.LoadOrStore` 思路。

---

### P1-2. `session/session.go:237-251` `saveSessionLocked` 持 Manager.mu 做磁盘 I/O

**状态**：未修复

**问题描述**
JSON 序列化 + 文件写入在 hold Manager.mu 期间执行。

**不修复会怎样**
高并发会话写时所有会话操作（包括读）都被串行化，P99 延迟显著上涨。

**修复后效果**
序列化在锁外完成，落盘可异步；锁内只做内存状态变更。

**修复步骤**
1. 在持锁段构造 snapshot（`map[string]any` 或 `[]byte`）。
2. 释放锁后再 `os.WriteFile`，或丢进单 goroutine 的 write queue。

---

### P1-3. `agent/manager.go:62-79` `ResolveAgent` 在 RLock 下做 IO

**状态**：未修复

**问题描述**
`IsAvailable()` 内部 `exec.LookPath`，磁盘 I/O。

**不修复会怎样**
极端情况下 PATH 上 NFS 挂载慢会拖住所有 reader。

**修复后效果**
锁内只 snapshot agent 列表，IO 在锁外。

**修复步骤**
```go
m.mu.RLock()
agents := make([]Agent, 0, len(m.agents))
for _, a := range m.agents { agents = append(agents, a) }
m.mu.RUnlock()
for _, a := range agents { if a.IsAvailable() { ... } }
```

---

### P1-4. `router/router_interactive.go:340-367` `waitInteractiveEventsQuiesced` 不监听 ctx

**状态**：已修复（`5f1d1b4`）

**问题描述**
按 `quietPeriod` 200ms 轮转，无 ctx select。

**不修复会怎样**
尾事件密集时可能持续 Reset timer 拖住关停。

**修复后效果**
ctx 取消立刻返回。

**修复步骤**
在 `select` 里增加 `case <-ctx.Done(): return ctx.Err()`。

---

### P1-5. `router/router_interactive.go:218,377` `remove + getOrCreate` TOCTOU

**状态**：未修复

**问题描述**
先 `removeInteractiveSession`，紧接着 `getOrCreateInteractiveSession`，两段之间锁被释放过。

**不修复会怎样**
并发请求可能让重建的 state 被顶替；用户偶发"上一轮 Allow All 状态丢失"。

**修复后效果**
remove 和 create 在同一锁段完成，或新增 `replaceInteractiveSession` 原子方法。

**修复步骤**
新增 `replaceInteractiveSession(sessionKey, builder func() *interactiveSessionState)`，在内部锁里 delete + create。

---

### P1-6. `router/native_sessions.go:1015` `exec sqlite3` 无 ctx 超时

**状态**：已修复（`5f1d1b4`）

**问题描述**
`exec.Command("sqlite3", ...)` 没有 ctx。

**不修复会怎样**
sqlite3 hang（如文件损坏）会让 `/list` 命令永久阻塞。

**修复后效果**
3s 超时后返回错误，`/list` 退回到 jsonl 数据源。

**修复步骤**
改用 `exec.CommandContext(ctx, ...)` 并 `context.WithTimeout(ctx, 3*time.Second)`。

---

### P1-7. `router/command.go:543-572` `collectSwitchCandidates` 每次 walkDir

**状态**：未修复

**问题描述**
`/list` `/switch` `/listen` 都会 walk 4 个 home 目录。

**不修复会怎样**
用户频繁使用这几个命令时磁盘 IO 抖动；macOS 上还会触发 fseventsd。

**修复后效果**
1-2s TTL 缓存，热路径命中。

**修复步骤**
Router 加 `switchCandidatesCache` 字段（带 mtime/expireAt），过期再 walk。

---

### P1-8. Token 日志泄漏（`platform/weibo/client.go:178`）

**状态**：已修复（`fa889f6`）

**问题描述**
日志 `Printf("token=%s...", token[:20])`，明文写到日志文件。

**不修复会怎样**
日志被收集到 ELK / 云端日志服务后 token 泄漏；攻击者可冒充 bridge 发消息。

**修复后效果**
只记录 token 长度和前 4 后 4 字符的 hash 或 mask。

**修复步骤**
```go
p.logger.Printf("token acquired len=%d hint=%s***%s", len(token), token[:4], token[len(token)-4:])
```
或用 `sha256(token)[:8]`。

---

### P1-9. `agent/codex_interactive_session.go:366` ctx 取消时静默丢消息

**状态**：未修复

**问题描述**
`requestWithID` ctx 取消后 cleanup 仅 `delete(pending, id)`，readLoop 后到的响应会被静默丢弃。

**不修复会怎样**
调试时无法判断是 ctx 超时还是 codex 没回包，问题排查困难。

**修复后效果**
丢弃消息时打 debug 日志，包含 id 和 type。

**修复步骤**
readLoop 投递前若 `pending` 中无 id，记录 `logger.Printf("[codex] drop late response id=%s type=%s", id, msg["type"])`。

---

### P1-10. `agent/gemini.go:121-138` Wait 错误被静默吞

**状态**：未修复

**问题描述**
若 `errorParts` 已有内容，`cmd.Wait()` 返回的错误被丢弃。

**不修复会怎样**
真实 exit 原因（如 segfault、OOM Killed）不会被记录。

**修复后效果**
即使有 errorParts，也至少 `logger.Printf` 一行 warn。

**修复步骤**
`if waitErr != nil { logger.Printf("[gemini] cmd.Wait warn: %v (errorParts=%d)", waitErr, len(errorParts)) }`。

---

### P1-11. `router/router_bytheway.go:160` `slices.Compact` 用法错误

**状态**：未修复（低优先级；当前只有两个候选 ID，重复时相邻，暂未造成实际故障）

**问题描述**
`slices.Compact` 只去除相邻重复，不能整体去重。

**不修复会怎样**
当前两个 ID 通常不同，问题不暴露；但任何上游改动只要让重复 ID 不相邻，去重就失效。

**修复后效果**
真正的集合去重。

**修复步骤**
```go
seen := make(map[string]struct{}, len(ids))
out := ids[:0]
for _, id := range ids {
    if _, ok := seen[id]; ok { continue }
    seen[id] = struct{}{}
    out = append(out, id)
}
ids = out
```

---

### P1-12. `platform/weibo/client.go:222` `Start` 不可重启

**状态**：未修复

**问题描述**
`Start` 不重置 `wg`、不重新初始化 `messageChan`、不重置 `cancel`。

**不修复会怎样**
任何尝试 Stop→Start 的代码路径（测试、热重载）都会出现 wg 计数错乱或 send on closed channel。

**修复后效果**
Start 入口幂等：若已经 running 返回错误；否则重新初始化所有字段。

**修复步骤**
```go
func (p *Platform) Start(ctx context.Context) error {
    p.mu.Lock()
    if p.running {
        p.mu.Unlock()
        return errors.New("platform already running")
    }
    p.running = true
    p.messageChan = make(chan Message, messageChanBuffer)
    p.ctx, p.cancel = context.WithCancel(ctx)
    p.wg = sync.WaitGroup{}
    p.mu.Unlock()
    // ... 之后照常 wg.Add(3) go ...
}
```

---

## P2 提示（建议但非必需）

### P2-1. 4 个 Agent 的 `Execute` / 流式 readLoop 抽公共

**状态**：未修复（按需）

`claude.go:53-90` / `codex.go:40-77` / `hermes.go:96-133` / `gemini.go:42-79` 几乎逐字复制。建议抽 `func collectStreamEvents(ctx, stream) (string, error)` 放到 `agent/agent.go`。

**不改的代价**：未来给 8 种 EventType 加字段或行为时要改 4 处，漏掉一处就行为分叉。
**改后**：单点维护，新增事件类型只改一处。

---

### P2-2. `router/router_core.go:48` `handlersMu` + `Register/Route` 死代码

**状态**：未修复（按需）

外部基本不再 Register；4 个内部 handler 完全可以静态调度。

**不改的代价**：新人误以为这是扩展点。
**改后**：移除 Register 接口，代码量减少 ~50 行。

---

### P2-3. `router/router_interactive.go:601` 用字符串包含判断 session not running

**状态**：未修复（按需）

**不改的代价**：agent 包改文案后 router 静默失效。
**改后**：agent 包导出 `var ErrSessionNotRunning = errors.New(...)`，router 用 `errors.Is`。

---

### P2-4. `router/stream_sender.go:298` 切分标点不全

**状态**：未修复（按需）

缺中文 `）`、`」`、`"`、`』` 等。

**不改的代价**：包含这些字符的长回复在不自然位置截断。
**改后**：分片落在中文标点边界，阅读体验更好。

---

### P2-5. `platform/weibo/client.go:188` `connect` 持锁做同步 Dial

**状态**：未修复（按需）

**不改的代价**：网络慢时所有 send 被阻塞数秒。
**改后**：Dial 在锁外完成，连上后再持锁赋值 `p.conn`。

---

### P2-6. `session/session.go:1112` 反射深拷贝是性能热点

**状态**：未修复（需要 profiling/benchmark 证据）

**不改的代价**：context 较大时每次 `Snapshot`/`Get` 都付反射开销。
**改后**：对 string/bool/int/[]string 走类型 switch 快路径，剩余类型再 fallback 到反射。

---

### P2-7. `router/command.go:80` 命令大小写不一致

**状态**：未修复（按需）

`/help` 等走 `strings.ToLower`，但 `/btw`、`/listen` 在 `isSpecialRouterCommand` 是大小写敏感的。

**不改的代价**：`/BTW`、`/Listen` 被当成 unknown，行为不一致。
**改后**：`isSpecialRouterCommand` 也 ToLower，统一行为。

---

### P2-8. `platform/weibo/client.go` 600 行单文件承担过多职责

**状态**：未修复（按需）

**不改的代价**：每次修改连接逻辑都要 diff 整个文件，code review 难度高。
**改后**：拆分为 `conn.go` / `token.go` / `dedup.go` / `chunk.go`，单文件职责清晰。

---

### P2-9. `agent/codex_appserver.go:418` `readErr` 残留未 drain

**状态**：已修复（`fa889f6`）

当前 buffer=1 单写一次，理论不泄漏；但 `streamEvents` 已退出后 `readErr` 留着一个未读 error，等 GC。

**不改的代价**：极小，但代码读起来像 bug。
**改后**：写入端用 `select { case c.readErr <- err: default: }`，语义自洽。

---

## 验证总览

完成所有 P0 后必须通过：

```bash
go test -race -coverprofile=coverage.out ./...
make test-report   # 生成 reports/test-report.md，对比 coverage 不下降
```

并手动验证：

1. 长回复（>10 分片）期间 SIGTERM，bridge 必须在 2 秒内退出，systemd 显示 clean exit。
2. 客户端断开后 5 秒内，对应 agent 子进程消失（`ps aux | grep claude` / `codex` / `hermes` / `gemini`）。
3. `/listen` 监听一个文件 → rotate → 仍能收到新内容。
4. 微博 token 错误启动 → 10 次重试后停止并日志告警，不会无限刷。

---

## 修复顺序建议

1. **已完成**：P0-1, P0-2, P0-3, P0-4, P0-6, P0-8, P0-9, P1-1, P1-4, P1-6, P1-8, P2-9。
2. **剩余 P0**：P0-5, P0-7 — 后台 goroutine 生命周期与 WebSocket 重连退避。
3. **剩余 P1**：P1-2, P1-3, P1-5, P1-7, P1-9, P1-10, P1-11, P1-12 — 可拆成 3-4 个小 PR。
4. **P2**：提示性改动，按需。
