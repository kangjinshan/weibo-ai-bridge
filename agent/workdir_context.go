package agent

import (
	"context"
	"strings"
)

type workDirContextKey struct{}
type allowAllContextKey struct{}

// WithWorkDir 将会话工作目录写入执行上下文。
func WithWorkDir(ctx context.Context, workDir string) context.Context {
	workDir = strings.TrimSpace(workDir)
	if ctx == nil || workDir == "" {
		return ctx
	}
	return context.WithValue(ctx, workDirContextKey{}, workDir)
}

// WorkDirFromContext 从执行上下文中读取会话工作目录。
func WorkDirFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	workDir, _ := ctx.Value(workDirContextKey{}).(string)
	return strings.TrimSpace(workDir)
}

// WithAllowAll 将“允许所有”授权状态写入执行上下文。
func WithAllowAll(ctx context.Context, allowAll bool) context.Context {
	if ctx == nil || !allowAll {
		return ctx
	}
	return context.WithValue(ctx, allowAllContextKey{}, true)
}

// AllowAllFromContext 从执行上下文中读取“允许所有”授权状态。
func AllowAllFromContext(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	allowAll, _ := ctx.Value(allowAllContextKey{}).(bool)
	return allowAll
}
