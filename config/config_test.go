package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLoad(t *testing.T) {
	// 设置测试环境变量
	os.Setenv("WEIBO_APP_ID", "test-app-id")
	os.Setenv("WEIBO_APP_Secret", "test-app-Secret")
	os.Setenv("CLAUDE_ENABLED", "true")

	cfg := Load()

	assert.NotNil(t, cfg)
	assert.Equal(t, "test-app-id", cfg.Platform.Weibo.AppID)
	assert.Equal(t, "test-app-Secret", cfg.Platform.Weibo.AppSecret)
	assert.True(t, cfg.Agent.Claude.Enabled)
	assert.False(t, cfg.Agent.Codex.Enabled)

	// 清理环境变量
	os.Unsetenv("WEIBO_APP_ID")
	os.Unsetenv("WEIBO_APP_Secret")
	os.Unsetenv("CLAUDE_ENABLED")
}

func TestDefaultValues(t *testing.T) {
	// 清理所有相关环境变量
	os.Unsetenv("WEIBO_APP_ID")
	os.Unsetenv("WEIBO_APP_Secret")
	os.Unsetenv("CLAUDE_ENABLED")
	os.Unsetenv("CODEX_ENABLED")
	os.Unsetenv("LOG_LEVEL")
	os.Unsetenv("LOG_FORMAT")
	os.Unsetenv("LOG_OUTPUT")

	cfg := Load()

	assert.Equal(t, 30, cfg.Platform.Weibo.Timeout)
	assert.Equal(t, 3600, cfg.Session.Timeout)
	assert.Equal(t, 1000, cfg.Session.MaxSize)
	assert.Equal(t, "info", cfg.Log.Level)
	assert.Equal(t, "json", cfg.Log.Format)
	assert.Equal(t, "stdout", cfg.Log.Output)
	assert.True(t, cfg.Agent.Claude.Enabled)
	assert.False(t, cfg.Agent.Codex.Enabled)
}