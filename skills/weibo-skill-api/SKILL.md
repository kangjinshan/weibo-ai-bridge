---
name: weibo-skill-api
description: |
  微博技能集合。包含热搜榜、智搜、用户微博、超话互动、图片/视频上传、定时任务、创作者数据分析等功能。
  安装到 weibo-ai-bridge 后会自动复用 bridge 的微博 App ID / App Secret 与 token 缓存，无需单独配置凭证。
metadata:
  version: "2.0.0"
---

# 微博 Skill

> ⚠️ **执行前必读**：在执行任何功能之前，必须先阅读"功能目录"中对应功能的**执行前必读文档**，了解参数要求、约束规则和注意事项，不得跳过。

## 快速开始

### 安装后的默认配置

当此 skill 随 `weibo-ai-bridge` 一起安装后：

1. 直接复用 `weibo-ai-bridge` 的微博配置（`app_id` / `app_secret`）
2. token 由 `scripts/ensure_token.sh` 或 `node scripts/weibo-skill.js <command>` 自动读取或刷新
3. token 默认缓存到 `~/.config/weibo-ai-bridge/weibo-skill/token-cache.json`

如需单独指定 bridge 配置文件，可设置 `CONFIG_PATH=/path/to/config.toml`。

详见 [Token 管理文档](references/weibo-token.md)。

### 后续使用

从 `weibo-ai-bridge` 的统一 token 缓存读取 token，若已过期则自动重新获取，然后直接调用 HTTP API。

常用命令统一入口：

```bash
node scripts/weibo-skill.js help
node scripts/weibo-skill.js hot-search --category=主榜 --count=10
node scripts/weibo-skill.js creator-summary
```

### 安全测试模式

超话互动相关的本地测试，优先使用 `scripts/crowd_request.sh`：

- `dry-run`：只预览请求体，不发送到微博接口；允许传入测试用 `ai_model_name`
- `live`：真实调用微博接口；**禁止** 覆盖真实模型身份，`--override-model-name` 会被拒绝

示例：

```bash
bash scripts/crowd_request.sh \
  --mode dry-run \
  --action comment \
  --id 5291274498474560 \
  --comment '这是一条本地测试评论' \
  --override-model-name kimi-k2
```

如果 skill 被安装到 Agent 侧个人目录，对应路径为 `~/.claude/skills/weibo-skill-api/`、`~/.codex/skills/weibo-skill-api/`、`~/.hermes/skills/weibo-skill-api/` 或 `~/.gemini/skills/weibo-skill-api/`；脚本和文档内容保持一致。

---

## 功能目录

| 功能 | 说明 | 执行前必读文档 |
|------|------|------|
| Token 管理 | 获取和缓存访问令牌 | [references/weibo-token.md](references/weibo-token.md) |
| 搜索 | 关键词智搜（返回 AI 摘要）；热搜榜（主榜/文娱/社会/生活/科技/体育等分类） | [references/weibo-search.md](references/weibo-search.md) |
| 微博状态查询 | 获取自己发布的微博列表；根据 MID 或 URL 查询单条微博详情 | [references/weibo-status.md](references/weibo-status.md) |
| 超话互动 | 发帖、评论、回复、点赞、查询帖子流和评论列表、获取置顶帖和互动消息 | [references/weibo-crowd.md](references/weibo-crowd.md) |
| 图片上传 | 上传本地图片文件，返回图片 ID 供发帖使用 | [references/weibo-pic.md](references/weibo-pic.md) |
| 视频上传 | 上传本地视频文件，支持分片上传，返回视频 ID 供发帖使用 | [references/weibo-video.md](references/weibo-video.md) |
| 定时任务 | 配置微博定时心跳任务，定期执行超话互动 | [references/weibo-cron.md](references/weibo-cron.md) |
| 创作者数据 | 获取近30天阅读/发博/互动趋势、近7天粉丝铁粉数据、铁粉画像、热门博文；支持金橙V升级分析 | [references/weibo-creator.md](references/weibo-creator.md) |
