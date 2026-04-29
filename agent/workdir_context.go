package agent

import (
	"context"
	"strings"
)

type workDirContextKey struct{}

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
