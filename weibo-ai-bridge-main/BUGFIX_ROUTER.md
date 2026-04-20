# Bug 修复：命令处理器误处理普通文本消息

## 问题描述

用户反馈：发送给安装好的服务任何消息，都返回 "Unknown command. Use /help to see available commands."

## 问题原因

在 `router/router.go` 的 `NewRouter` 函数中，命令处理器被错误地设置为了默认处理器：

```go
// 将命令处理器注册为默认处理器
router.SetDefault(router.commandHandler)
```

这导致所有消息（包括普通文本）都被命令处理器处理。而命令处理器在 `command.go` 中会检查消息是否以 `/` 开头：

```go
// 检查是否是命令
if !strings.HasPrefix(content, "/") {
    return &Response{
        Success: false,
        Content: "Unknown command. Use /help to see available commands.",
    }, nil
}
```

## 修复方案

### 1. 修改路由逻辑

将命令处理器注册为 `TypeText` 类型的处理器，而不是默认处理器：

```go
// 注册 AI 消息处理器（处理普通文本消息）
router.Register(TypeText, router)
// 命令处理器不设为默认，只有在消息以 / 开头时才处理
```

### 2. 实现 Handle 方法

让 `Router` 实现 `Handler` 接口，在 `Handle` 方法中添加命令检测逻辑：

```go
// Handle 处理消息（实现 Handler 接口）
func (r *Router) Handle(msg *Message) (*Response, error) {
    content := strings.TrimSpace(msg.Content)

    // 如果消息以 / 开头，使用命令处理器
    if strings.HasPrefix(content, "/") && r.commandHandler != nil {
        return r.commandHandler.Handle(msg)
    }

    // 否则使用 AI 处理器
    return r.handleAIResponse(context.Background(), msg)
}
```

## 修复后的行为

- ✅ 命令消息（以 `/` 开头）：由命令处理器处理
  - `/help` - 显示帮助信息
  - `/new` - 创建新会话
  - `/switch` - 切换 Agent
  - `/model` - 显示模型
  - `/dir` - 显示目录
  - `/status` - 显示状态

- ✅ 普通文本消息：由 AI 处理器处理
  - 发送任何非命令文本都会得到 AI 回复

## 测试建议

1. **测试命令**：
   ```
   /help
   /new claude
   /status
   ```

2. **测试普通文本**：
   ```
   你好
   今天天气怎么样？
   帮我写一个函数
   ```

## 相关文件

- `router/router.go` - 路由器主逻辑
- `router/command.go` - 命令处理器

## 提交信息

```
fix: 修复路由逻辑，命令处理器不再误处理普通文本消息

问题：命令处理器被设为默认处理器，导致普通文本消息也被当成命令解析

修复：
- 将命令处理器注册为 TypeText 类型的处理器
- 在 Handle 方法中添加命令检测逻辑
- 只有以 / 开头的消息才使用命令处理器
- 普通文本消息直接使用 AI 处理器

影响：
- /help, /new, /switch 等命令正常工作
- 普通文本消息不再被误判为命令
- AI 回复功能不受影响
```

