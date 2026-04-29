package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLoad(t *testing.T) {
	// 设置测试环境变量
	t.Setenv("CONFIG_PATH", filepath.Join(t.TempDir(), "missing-config.toml"))
	t.Setenv("WEIBO_APP_ID", "test-app-id")
	t.Setenv("WEIBO_APP_SECRET", "test-app-secret")
	t.Setenv("CLAUDE_ENABLED", "true")
	t.Setenv("CODEX_ENABLED", "false")

	cfg := Load()

	assert.NotNil(t, cfg)
	assert.Equal(t, "test-app-id", cfg.Platform.Weibo.AppID)
	assert.Equal(t, "test-app-secret", cfg.Platform.Weibo.Appsecret)
	assert.True(t, cfg.Agent.Claude.Enabled)
	assert.False(t, cfg.Agent.Codex.Enabled)
}

func TestLoad_LegacyWeiboAppsecretEnvStillWorks(t *testing.T) {
	t.Setenv("CONFIG_PATH", filepath.Join(t.TempDir(), "missing-config.toml"))
	os.Unsetenv("WEIBO_APP_SECRET")
	t.Setenv("WEIBO_APP_Secret", "legacy-secret")

	cfg := Load()

	assert.Equal(t, "legacy-secret", cfg.Platform.Weibo.Appsecret)
}

func TestLoad_UsesConfigPathWhenProvided(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.toml")
	content := `
[platform.weibo]
app_id = "external-app-id"
app_secret = "external-app-secret"

[agent.claude]
enabled = false

[agent.codex]
enabled = true

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

	os.Setenv("CONFIG_PATH", configFile)
	os.Unsetenv("WEIBO_APP_ID")
	os.Unsetenv("WEIBO_APP_SECRET")
	os.Unsetenv("WEIBO_APP_Secret")
	t.Cleanup(func() {
		os.Unsetenv("CONFIG_PATH")
	})

	cfg := Load()

	assert.Equal(t, "external-app-id", cfg.Platform.Weibo.AppID)
	assert.Equal(t, "external-app-secret", cfg.Platform.Weibo.Appsecret)
	assert.False(t, cfg.Agent.Claude.Enabled)
	assert.True(t, cfg.Agent.Codex.Enabled)
}

func TestDefaultValues(t *testing.T) {
	// 清理所有相关环境变量
	os.Unsetenv("WEIBO_APP_ID")
	os.Unsetenv("WEIBO_APP_SECRET")
	os.Unsetenv("WEIBO_APP_Secret")
	os.Unsetenv("CLAUDE_ENABLED")
	os.Unsetenv("CODEX_ENABLED")
	os.Unsetenv("LOG_LEVEL")
	os.Unsetenv("LOG_FORMAT")
	os.Unsetenv("LOG_OUTPUT")
	os.Unsetenv("CONFIG_PATH")

	cfg := Load()

	assert.Equal(t, 30, cfg.Platform.Weibo.Timeout)
	assert.Equal(t, 3600, cfg.Session.Timeout)
	assert.Equal(t, 1000, cfg.Session.MaxSize)
	assert.Equal(t, defaultSessionStoragePath(), cfg.Session.StoragePath)
	assert.Equal(t, "info", cfg.Log.Level)
	assert.Equal(t, "json", cfg.Log.Format)
	assert.Equal(t, "stdout", cfg.Log.Output)
	assert.True(t, cfg.Agent.Claude.Enabled)
	assert.False(t, cfg.Agent.Codex.Enabled)
}

func TestLoad_SessionStoragePathEnvOverride(t *testing.T) {
	t.Setenv("CONFIG_PATH", filepath.Join(t.TempDir(), "missing-config.toml"))
	t.Setenv("SESSION_STORAGE_PATH", "/tmp/custom-weibo-ai-bridge-sessions")

	cfg := Load()

	assert.Equal(t, "/tmp/custom-weibo-ai-bridge-sessions", cfg.Session.StoragePath)
}

func TestLoad_ReadsDotEnvWhenConfigMissing(t *testing.T) {
	tmpDir := t.TempDir()
	envFile := filepath.Join(tmpDir, ".env")
	envContent := "WEIBO_APP_ID=dot-env-app-id\nWEIBO_APP_SECRET=dot-env-app-secret\nCLAUDE_ENABLED=true\nCODEX_ENABLED=false\n"
	err := os.WriteFile(envFile, []byte(envContent), 0644)
	assert.NoError(t, err)

	withWorkingDir(t, tmpDir)
	withUnsetEnv(t, "WEIBO_APP_ID", "WEIBO_APP_SECRET", "WEIBO_APP_Secret", "CLAUDE_ENABLED", "CODEX_ENABLED")
	t.Setenv("CONFIG_PATH", filepath.Join(tmpDir, "missing-config.toml"))

	cfg := Load()

	assert.Equal(t, "dot-env-app-id", cfg.Platform.Weibo.AppID)
	assert.Equal(t, "dot-env-app-secret", cfg.Platform.Weibo.Appsecret)
	assert.NoError(t, cfg.Validate())
}

func TestLoad_DotEnvDoesNotOverrideSystemEnv(t *testing.T) {
	tmpDir := t.TempDir()
	envFile := filepath.Join(tmpDir, ".env")
	envContent := "WEIBO_APP_ID=dot-env-app-id\nWEIBO_APP_SECRET=dot-env-app-secret\n"
	err := os.WriteFile(envFile, []byte(envContent), 0644)
	assert.NoError(t, err)

	withWorkingDir(t, tmpDir)
	t.Setenv("CONFIG_PATH", filepath.Join(tmpDir, "missing-config.toml"))
	t.Setenv("WEIBO_APP_ID", "system-env-app-id")
	withUnsetEnv(t, "WEIBO_APP_SECRET", "WEIBO_APP_Secret")

	cfg := Load()

	assert.Equal(t, "system-env-app-id", cfg.Platform.Weibo.AppID)
	assert.Equal(t, "dot-env-app-secret", cfg.Platform.Weibo.Appsecret)
}

func withWorkingDir(t *testing.T, dir string) {
	t.Helper()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd failed: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir failed: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(cwd)
	})
}

func withUnsetEnv(t *testing.T, keys ...string) {
	t.Helper()

	type envValue struct {
		value string
		ok    bool
	}
	oldValues := make(map[string]envValue, len(keys))

	for _, key := range keys {
		value, ok := os.LookupEnv(key)
		oldValues[key] = envValue{value: value, ok: ok}
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("unsetenv %s failed: %v", key, err)
		}
	}

	t.Cleanup(func() {
		for _, key := range keys {
			old := oldValues[key]
			if old.ok {
				_ = os.Setenv(key, old.value)
				continue
			}
			_ = os.Unsetenv(key)
		}
	})
}
