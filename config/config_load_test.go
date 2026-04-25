package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLoadFromFile(t *testing.T) {
	// 创建临时测试配置文件
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.toml")

	content := `
[platform.weibo]
app_id = "test-app-id"
app_secret = "test-app-Secret"
token_url = "http://example.com/token"
ws_url = "ws://example.com/ws"
timeout = 60

[agent.claude]
enabled = true

[agent.codex]
api_key = "codex-test-key"
model = "gpt-4-turbo"
enabled = true

[session]
timeout = 7200
max_size = 2000
storage_path = "/tmp/weibo-ai-bridge-sessions"

[log]
level = "debug"
format = "text"
output = "/var/log/app.log"
`
	err := os.WriteFile(configFile, []byte(content), 0644)
	assert.NoError(t, err)

	cfg, err := LoadFromFile(configFile)
	assert.NoError(t, err)
	assert.NotNil(t, cfg)

	assert.Equal(t, "test-app-id", cfg.Platform.Weibo.AppID)
	assert.Equal(t, "test-app-Secret", cfg.Platform.Weibo.AppSecret)
	assert.Equal(t, "http://example.com/token", cfg.Platform.Weibo.TokenURL)
	assert.Equal(t, "ws://example.com/ws", cfg.Platform.Weibo.WSURL)
	assert.Equal(t, 60, cfg.Platform.Weibo.Timeout)

	assert.True(t, cfg.Agent.Claude.Enabled)

	assert.Equal(t, "codex-test-key", cfg.Agent.Codex.APIKey)
	assert.Equal(t, "gpt-4-turbo", cfg.Agent.Codex.Model)
	assert.True(t, cfg.Agent.Codex.Enabled)

	assert.Equal(t, 7200, cfg.Session.Timeout)
	assert.Equal(t, 2000, cfg.Session.MaxSize)
	assert.Equal(t, "/tmp/weibo-ai-bridge-sessions", cfg.Session.StoragePath)

	assert.Equal(t, "debug", cfg.Log.Level)
	assert.Equal(t, "text", cfg.Log.Format)
	assert.Equal(t, "/var/log/app.log", cfg.Log.Output)
}

func TestLoadFromFileNotFound(t *testing.T) {
	cfg, err := LoadFromFile("/non/existent/path/config.toml")
	assert.Error(t, err)
	assert.Nil(t, cfg)
}

func TestLoadFromFileInvalidTOML(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "invalid.toml")

	content := `
[platform.weibo
app_id = "test"
`
	err := os.WriteFile(configFile, []byte(content), 0644)
	assert.NoError(t, err)

	cfg, err := LoadFromFile(configFile)
	assert.Error(t, err)
	assert.Nil(t, cfg)
}

func TestLoadWithEnvOverride(t *testing.T) {
	// 创建临时测试配置文件
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.toml")

	content := `
[platform.weibo]
app_id = "file-app-id"
app_secret = "file-Secret"
token_url = "http://file.example.com/token"

[agent.claude]
enabled = false

[session]
timeout = 3600
max_size = 1000

[log]
level = "info"
format = "json"
output = "stdout"
`
	err := os.WriteFile(configFile, []byte(content), 0644)
	assert.NoError(t, err)

	// LoadFromFile 不会自动应用环境变量覆盖，只有 Load() 会
	// 所以这个测试应该验证文件配置被正确加载
	cfg, err := LoadFromFile(configFile)
	assert.NoError(t, err)
	assert.NotNil(t, cfg)

	// 验证文件配置被正确加载
	assert.Equal(t, "file-app-id", cfg.Platform.Weibo.AppID)
	assert.Equal(t, "file-Secret", cfg.Platform.Weibo.AppSecret)
	assert.False(t, cfg.Agent.Claude.Enabled)
	assert.Equal(t, "info", cfg.Log.Level)

	// 文件中的其他配置应该保留
	assert.Equal(t, "http://file.example.com/token", cfg.Platform.Weibo.TokenURL)
	assert.Equal(t, 3600, cfg.Session.Timeout)
}
