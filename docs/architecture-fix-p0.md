# P0 架构修复报告

## 变更总览

| 编号 | 问题 | 状态 | 改动文件数 |
|------|------|------|-----------|
| P0-1 | 敏感凭证提交到仓库 | 已确认安全 | 0 |
| P0-2 | HTTP 端点无认证 | 已提供可选认证，配置后受保护 | 7 |
| P0-3 | 全局可变 logger | 已修复 | 2 |

---

## P0-1: 敏感凭证提交到仓库

### 问题

`config/config.toml` 包含微博 `app_id` 和 `app_secret` 明文凭证，存在泄露风险。

### 确认结果

经 `git ls-files` 和 `git log` 验证：`config/config.toml` 已在 `.gitignore:44` 中排除，且从未被提交到 git 历史。无需额外操作。

### 相关文件（未修改）

- `.gitignore:44` — `config/config.toml` 排除规则
- `config/config.example.toml` — 安全模板，凭证字段为占位符

---

## P0-2: HTTP 端点无认证

### 问题

`/stats` 和 `/chat/stream` 端点接受任意请求，无身份验证。攻击者可伪造 `user_id` 与 AI Agent 交互。

### 修复方案

添加 Bearer Token 认证中间件，通过配置项 `http.api_key` 控制。设置后，受保护端点需要 `Authorization: Bearer <api_key>` 请求头；未设置则保持原行为（无认证），保持向后兼容。服务启动时若未配置 API Key，会输出日志提醒 `/stats` 和 `/chat/stream` 当前未启用认证。

### 改动点

#### 1. `config/config.go` — 新增 HTTPConfig 结构体和加载逻辑

```go
// 新增结构体
type HTTPConfig struct {
    Port   string `toml:"port"`
    APIKey string `toml:"api_key"`
}

// Config 新增字段
HTTP HTTPConfig

// defaultConfig() 新增默认值
HTTP: HTTPConfig{Port: "5533"},

// Load() 新增环境变量读取
if val := os.Getenv("SERVER_PORT"); val != "" {
    cfg.HTTP.Port = val
}
if val := os.Getenv("HTTP_API_KEY"); val != "" {
    cfg.HTTP.APIKey = val
}
```

#### 2. `cmd/server/main.go:575-595` — 新增 withAPIKey 中间件

```go
func withAPIKey(apiKey string, next http.HandlerFunc) http.HandlerFunc {
    apiKey = strings.TrimSpace(apiKey)
    if apiKey == "" {
        return next  // 未配置 api_key 时跳过认证
    }
    return func(w http.ResponseWriter, r *http.Request) {
        authHeader := r.Header.Get("Authorization")
        if authHeader == "" {
            http.Error(w, "missing Authorization header", http.StatusUnauthorized)
            return
        }
        token, ok := strings.CutPrefix(authHeader, "Bearer ")
        token = strings.TrimSpace(token)
        if !ok || subtle.ConstantTimeCompare([]byte(token), []byte(apiKey)) != 1 {
            http.Error(w, "invalid API key", http.StatusForbidden)
            return
        }
        next(w, r)
    }
}
```

- `apiKey` 会先 `TrimSpace`；空字符串或纯空白时直接返回原 handler，零配置无影响
- 同时校验 header 缺失（401）和 token 不匹配（403）
- token 比较使用 `crypto/subtle.ConstantTimeCompare`

#### 3. `cmd/server/main.go:242-248` — 路由注册改为使用中间件 + 配置端口

```diff
- mux.HandleFunc("/stats", statsHandler(sessionMgr, agentMgr))
- mux.HandleFunc("/chat/stream", chatStreamHandler(msgRouter))
- port := os.Getenv("SERVER_PORT")
- if port == "" {
-     port = "5533"
- }
+ mux.HandleFunc("/stats", withAPIKey(cfg.HTTP.APIKey, statsHandler(sessionMgr, agentMgr, logger)))
+ mux.HandleFunc("/chat/stream", withAPIKey(cfg.HTTP.APIKey, chatStreamHandler(msgRouter, logger)))
+ port := cfg.HTTP.Port
```

- `/health` 保持无认证（健康检查端点）
- 端口从硬编码 `os.Getenv` 迁移到 `cfg.HTTP.Port`

#### 4. `config/config.example.toml` — 新增 `[http]` 配置段

```toml
[http]
port = "5533"
api_key = ""  # 留空不启用认证
```

#### 5. `cmd/server/main_test.go` / `config/*_test.go` — 覆盖认证和配置加载

- 覆盖无 API Key、纯空白 API Key、缺失 Authorization、错误 token、正确 token 和带空白配置 key 的认证行为
- 覆盖 `[http]` TOML 配置、`SERVER_PORT` 和 `HTTP_API_KEY` 环境变量覆盖

#### 6. `README.md` — 同步运行配置和调试示例

- 新增 `HTTP_API_KEY` 环境变量说明
- 新增 `/stats` 和 `/chat/stream` 的 Bearer Token 认证说明
- `/chat/stream` 示例改为推荐 POST，GET 仅作为本地调试兼容方式保留

### 使用方式

```bash
# 环境变量方式
export HTTP_API_KEY="your-secret-key"

# 或在 config.toml 中
[http]
api_key = "your-secret-key"

# 请求时携带
curl -N \
  -H "Authorization: Bearer your-secret-key" \
  -H "Content-Type: application/json" \
  -d '{"user_id":"123456","content":"hello"}' \
  http://127.0.0.1:5533/chat/stream
```

推荐使用 POST 调试 `/chat/stream`，避免把消息正文写入 URL、shell history 或代理访问日志；GET 仍保留用于本地兼容调试。

---

## P0-3: 全局可变 logger

### 问题

`var logger *log.Logger` 是包级变量，`initLogger()` 直接修改全局状态。所有函数通过全局变量访问日志，导致：
- 无法并发测试（测试间共享全局 logger 状态）
- 隐式依赖，代码可读性差
- 无法在同进程中使用不同日志配置

### 修复方案

将 `initLogger()` 改为 `newLogger()` 返回 `*log.Logger`，`main()` 使用局部变量，所有需要 logger 的函数通过参数接收。

### 改动点

#### 1. `cmd/server/main.go:25-27` — 移除包级 logger 变量

```diff
 var (
-    logger *log.Logger
-
     version   = "dev"
     gitCommit = "unknown"
     buildTime = "unknown"
 )
```

#### 2. `cmd/server/main.go:121-128` — main() 使用局部变量

```diff
 func main() {
     cfg := config.Load()
-    initLogger(cfg.Log)
+    logger := newLogger(cfg.Log)
     logger.Printf(...)
```

#### 3. `cmd/server/main.go:289-311` — initLogger → newLogger

```diff
-func initLogger(logCfg config.LogConfig) {
+func newLogger(logCfg config.LogConfig) *log.Logger {
     // ... 相同逻辑 ...
-    logger = log.New(output, "[weibo-ai-bridge] ", log.LstdFlags|log.Lshortfile)
+    return log.New(output, "[weibo-ai-bridge] ", log.LstdFlags|log.Lshortfile)
 }
```

#### 4. `cmd/server/main.go:325` — processMessages 接收 logger 参数

```diff
-func processMessages(ctx context.Context, platform messagePlatform, msgRouter messageHandler) {
-    processor := newMessageProcessor(platform, msgRouter, logger)
+func processMessages(ctx context.Context, platform messagePlatform, msgRouter messageHandler, logger *log.Logger) {
+    processor := newMessageProcessor(platform, msgRouter, logger)
```

#### 5. `cmd/server/main.go` — HTTP handler 工厂函数接收 logger

```diff
-func statsHandler(sessionMgr *session.Manager, agentMgr *agent.Manager) http.HandlerFunc {
+func statsHandler(sessionMgr *session.Manager, agentMgr *agent.Manager, logger *log.Logger) http.HandlerFunc {

-func chatStreamHandler(msgRouter *router.Router) http.HandlerFunc {
+func chatStreamHandler(msgRouter *router.Router, logger *log.Logger) http.HandlerFunc {
```

#### 6. `cmd/server/main_test.go` — 测试调用适配新签名

```diff
- statsHandler(sessionMgr, agentMgr)(w, req)
+ statsHandler(sessionMgr, agentMgr, log.Default())(w, req)

- statsHandler(session.NewManager(session.ManagerConfig{}), agent.NewManager())(w, req)
+ statsHandler(session.NewManager(session.ManagerConfig{}), agent.NewManager(), log.Default())(w, req)

- handler := chatStreamHandler(msgRouter)
+ handler := chatStreamHandler(msgRouter, log.Default())
```

测试中使用 `log.Default()` 作为 logger 参数，满足签名要求且无副作用。

---

## 验证

```bash
# 全量编译
go build ./cmd/server/    # PASS

# 定向测试
go test ./cmd/server -run 'TestWithAPIKey|TestStatsHandler|TestChatStreamHandler'  # PASS
go test ./config -run 'TestLoad|TestDefaultValues'                                 # PASS

# 全量测试
go test ./...             # 7 packages, all PASS

# 具体结果
ok  github.com/kangjinshan/weibo-ai-bridge/agent
ok  github.com/kangjinshan/weibo-ai-bridge/cmd/server
ok  github.com/kangjinshan/weibo-ai-bridge/cmd/test-report
ok  github.com/kangjinshan/weibo-ai-bridge/config
ok  github.com/kangjinshan/weibo-ai-bridge/platform/weibo
ok  github.com/kangjinshan/weibo-ai-bridge/router
ok  github.com/kangjinshan/weibo-ai-bridge/session
```

---

## 未修改的文件清单

以下文件涉及全局 logger 引用但**无需修改**：

| 文件 | 原因 |
|------|------|
| `router/*.go` | 不引用 cmd/server 的全局 logger |
| `agent/*.go` | 不引用 cmd/server 的全局 logger |
| `session/*.go` | 不引用 cmd/server 的全局 logger |
| `platform/weibo/*.go` | 不引用 cmd/server 的全局 logger |

logger 重构的影响范围仅限于 `cmd/server/` 包内，因为其他包原本就不依赖该全局变量。
