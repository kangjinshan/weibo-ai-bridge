package router

import (
	"context"

	"github.com/kangjinshan/weibo-ai-bridge/platform/weibo"
)

// Platform 是 Router 对上游平台（微博、msghub 等）的最小依赖集合。
//
// 这个接口把原本散落在 router 包内部的隐式约束显式化，方便新增上游适配器
// (例如 platform/local) 时做编译期校验。Router 自身仍然继续以
// PlatformInterface / streamingPlatformInterface 这两个细分接口来调用，
// 这里只是它们的并集。
//
// 注意：OpenReplyStream 返回的 ChunkSender 仍然复用 platform/weibo 包里
// 已经导出的接口类型，避免现有 Router/cmd/server 代码改动。
type Platform interface {
	PlatformInterface
	OpenReplyStream(ctx context.Context, userID string) (weibo.ChunkSender, error)
}

// 编译期校验：微博平台必须满足新的统一 Platform 接口。
// 若 weibo.Platform 的方法签名漂移导致这里不通过，Router 调用方会同步出错。
var _ Platform = (*weibo.Platform)(nil)
