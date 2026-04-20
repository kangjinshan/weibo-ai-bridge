# Weibo AI Bridge - Agents 配置和使用

## 概述

Weibo AI Bridge 支持多种 AI Agent 的自动发现和使用。系统会自动检测本地安装的 Agent CLI 工具，无需手动配置路径。

**默认端口**: 5533（可通过环境变量 `SERVER_PORT` 修改）

## 支持的 Agent

### 1. Claude Code Agent

**CLI 工具识别**：
- 主要：`claude`（位于 `/usr/local/bin/claude`）
- 备用：`cc`（位于 `/usr/bin/cc`）

**当前版本**：
- `claude`: 2.1.110 (Claude Code)
- `cc`: 13.3.0 (Ubuntu 版本)

**安装方式**：
```bash
# 方式1：通过 npm 安装
npm install -g @anthropic-ai/claude-code

# 方式2：通过编译安装
git clone https://github.com/anthropics/claude-code.git
cd claude-code
npm install
npm run build
sudo npm install -g .
```

**配置示例**：
```toml
[agent.claude]
api_key = "sk-ant-xxxxx"  # Claude API 密钥
model = "claude-3-5-sonnet-20241022"  # 模型名称
enabled = true  # 启用此 Agent
```

**环境变量**：
```bash
# Claude API Key 和模型由 Claude Code CLI 管理
# 配置方式：
export ANTHROPIC_API_KEY="sk-ant-xxxxx"
# 或编辑 ~/.config/claude/config.json

# 项目只需要控制是否启用
export CLAUDE_ENABLED="true"
```

### 2. CodeX Agent

**CLI 工具识别**：
- `codex` CLI 工具

**当前状态**：未安装 ❌

**安装方式**：
```bash
# 通过 npm 安装
npm install -g codex-cli

# 或通过源码安装
git clone https://github.com/codex-cli/codex.git
cd codex
npm install
npm run build
sudo npm install -g .
```

**配置示例**：
```toml
[agent.codex]
api_key = "your-codex-api-key"  # CodeX API 密钥
model = "gpt-4"  # 模型名称
enabled = false  # 启用此 Agent（需先安装）
```

**环境变量**：
```bash
export CODEX_API_KEY="your-codex-api-key"
export CODEX_MODEL="gpt-4"
export CODEX_ENABLED="true"
```

## Agent 自动发现机制

### 工作原理

系统使用 Go 的 `exec.LookPath()` 方法自动检测系统中可用的 CLI 工具：

```go
// 检测 Claude Code
claudePath, err := exec.LookPath("claude")
if err == nil {
    fmt.Printf("发现 Claude Code: %s\n", claudePath)
}

// 检测备用命令
ccPath, err := exec.LookPath("cc")
if err == nil {
    fmt.Printf("发现 Claude Code (cc): %s\n", ccPath)
}

// 检测 CodeX
codexPath, err := exec.LookPath("codex")
if err == nil {
    fmt.Printf("发现 CodeX: %s\n", codexPath)
} else {
    fmt.Println("CodeX 未安装")
}
```

### 优先级

1. **Claude Code**: 优先使用 `claude` 命令，如果不存在则尝试 `cc`
2. **CodeX**: 使用 `codex` 命令

### 可用性检查

每个 Agent 在执行前会检查对应的 CLI 工具是否可用：

```go
func (a *ClaudeCodeAgent) IsAvailable() bool {
    _, err := exec.LookPath("claude")
    return err == nil
}

func (a *CodeXAgent) IsAvailable() bool {
    _, err := exec.LookPath("codex")
    return err == nil
}
```

如果工具不可用，系统会返回明确的错误信息：
```
❌ claude CLI is not available
❌ codex CLI is not available
```

## 使用方式

### 切换 Agent

用户可以在微博私信中发送命令切换 AI Agent：

```
/agent claude-code  # 切换到 Claude Code
/agent codex         # 切换到 CodeX
```

### 自动降级

如果当前 Agent 不可用，系统会自动尝试使用其他可用的 Agent：

1. 检查当前 Agent 的 CLI 是否安装
2. 如果不可用，尝试使用默认 Agent
3. 如果所有 Agent 都不可用，返回错误信息

### 命令格式

所有 Agent 使用统一的命令格式：

```bash
# --print：打印模式（适合程序化调用）
claude --print "你的问题"

# --interactive：交互模式（适合直接使用）
claude --interactive
```

## 配置管理

### 配置优先级

系统按以下优先级加载配置：

1. **环境变量**（最高优先级）
2. **TOML 配置文件**
3. **默认配置**（最低优先级）

### 配置文件位置

```bash
# 主配置文件
config/config.toml

# 示例配置文件
config/config.example.toml
```

### 配置验证

系统启动时会验证所有配置：

- ✅ 至少启用一个 Agent
- ✅ 启用的 Agent 必须配置 API Key
- ✅ 平台凭证必须正确
- ✅ 会话配置必须有效

如果配置错误，系统会拒绝启动并显示详细的错误信息。

## 故障排除

### Agent 不可用

**问题**：
```
❌ claude CLI is not available
```

**解决方法**：
1. 检查 Agent 是否已安装：
   ```bash
   which claude
   which cc
   which codex
   ```

2. 如果未安装，按照上面的安装方式安装

3. 验证安装：
   ```bash
   claude --version
   codex --version
   ```

### Agent 执行失败

**问题**：
```
❌ failed to execute claude CLI: exit status 1
```

**解决方法**：
1. 检查 API Key 是否正确
2. 检查网络连接
3. 检查 Agent CLI 是否有权限问题
4. 查看详细日志：
   ```bash
   export LOG_LEVEL="debug"
   ./bin/server
   ```

### 配置不生效

**问题**：修改配置后没有生效

**解决方法**：
1. 确认配置文件格式正确（TOML 语法）
2. 重启服务以加载新配置
3. 检查环境变量是否覆盖了配置文件
4. 使用健康检查接口验证配置：
   ```bash
   curl http://localhost:5533/stats
   ```

## 扩展新的 Agent

### 添加新的 Agent

1. 创建新的 Agent 文件：
   ```go
   package agent

   import (
       "fmt"
       "os/exec"
       "strings"
   )

   type NewAgent struct {
       name string
   }

   func NewNewAgent() *NewAgent {
       return &NewAgent{
           name: "new-agent",
       }
   }

   func (a *NewAgent) Name() string {
       return a.name
   }

   func (a *NewAgent) Execute(input string) (string, error) {
       if !a.IsAvailable() {
           return "", fmt.Errorf("new-agent CLI is not available")
       }

       cmd := exec.Command("new-agent-cli", "--print", input)
       // ... 执行逻辑
       return result, nil
   }

   func (a *NewAgent) IsAvailable() bool {
       _, err := exec.LookPath("new-agent-cli")
       return err == nil
       }
   ```

2. 在 Agent Manager 中注册：
   ```go
   // 在 agent/manager.go 中
   if cfg.Agent.NewAgent.Enabled {
       newAgent := agent.NewNewAgent()
       agentMgr.Register(newAgent)
   }
   ```

3. 添加配置项：
   ```toml
   [agent.new_agent]
   api_key = "your-api-key"
   model = "model-name"
   enabled = true
   ```

## 性能优化

### Agent 执行优化

1. **并发控制**：限制同时执行的 Agent 请求数量
2. **超时控制**：为 Agent 执行设置合理的超时时间
3. **缓存机制**：对相同的问题使用缓存结果
4. **错误重试**：对临时性错误自动重试

### 资源管理

1. **连接池**：复用 Agent CLI 的连接
2. **内存优化**：限制会话历史大小
3. **异步处理**：使用 goroutine 处理 Agent 响应

## 最佳实践

### 配置建议

1. **使用环境变量管理敏感信息**
2. **定期轮换 API Keys**
3. **为不同环境使用不同配置**
4. **监控 Agent 使用情况**

### 使用建议

1. **选择合适的 Agent**：根据任务类型选择最合适的 AI Agent
2. **合理设置超时**：避免长时间等待
3. **监控资源使用**：关注 CPU 和内存使用情况
4. **定期清理会话**：避免内存泄漏

## 安全建议

1. **不要在配置文件中硬编码敏感信息**
2. **使用环境变量或密钥管理服务**
3. **限制 API Key 权限**
4. **定期审计访问日志**
5. **更新 Agent CLI 到最新版本**

## 相关文档

- [主 README](README.md) - 项目总体说明
- [配置文档](config/config.example.toml) - 详细配置示例
- [平台文档](platform/weibo/) - 微博平台集成说明

## 测试验证

### 当前测试结果

**系统状态**：
- ✅ 服务运行在端口 5533
- ✅ Claude Code Agent 自动发现成功
- ✅ CodeX Agent 未安装（已正确识别）
- ✅ WebSocket 连接稳定
- ✅ 心跳机制正常

**测试接口**：
```bash
# 健康检查
curl http://localhost:5533/health

# 统计信息
curl http://localhost:5533/stats
```

**测试输出示例**：
```json
{
  "agents": {
    "count": 1,
    "list": ["claude-code"]
  },
  "sessions": {
    "count": 0
  },
  "timestamp": 1776655764
}
```
