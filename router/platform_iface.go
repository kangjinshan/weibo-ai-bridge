package router

import (
	"context"

	"github.com/kangjinshan/weibo-ai-bridge/platform/weibo"
)

// Platform 是 Router 对上游平台（微博、msghub 等）的统一依赖接口。
//
// 它把回复（Reply）与流式回复（OpenReplyStream）合并到一个接口，
// 新增上游适配器（例如 platform/local）时做编译期校验。
//
// 注意：OpenReplyStream 返回的 ChunkSender 仍然复用 platform/weibo 包里
// 已经导出的接口类型。
type Platform interface {
	Reply(ctx context.Context, userID string, content string) error
	OpenReplyStream(ctx context.Context, userID string) (weibo.ChunkSender, error)
}

// 编译期校验：微博平台必须满足统一 Platform 接口。
var _ Platform = (*weibo.Platform)(nil)
