package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConfigStruct(t *testing.T) {
	cfg := &Config{
			Platform: PlatformConfig{
				Weibo: WeiboConfig{
					AppID:          "test-app-id",
					AppSecret: "test-Secret",
					TokenURL:       "http://example.com/token",
					WSURL:          "ws://example.com/ws",
					Timeout:        30,
				},
			},
			Agent: AgentConfig{
				Claude: ClaudeConfig{
					Enabled: true,
				},
				Codex: CodexConfig{
					APIKey:  "codex-key",
					Model:   "gpt-4",
					Enabled: false,
				},
			},
			Session: SessionConfig{
				Timeout: 3600,
				MaxSize: 1000,
			},
			Log: LogConfig{
				Level:  "info",
				Format: "json",
			},
		}

		assert.NotNil(t, cfg)
		assert.Equal(t, "test-app-id", cfg.Platform.Weibo.AppID)
		assert.Equal(t, "test-Secret", cfg.Platform.Weibo.AppSecret)
		assert.Equal(t, 3600, cfg.Session.Timeout)
		assert.Equal(t, "info", cfg.Log.Level)
}

func TestPlatformConfigStruct(t *testing.T) {
	platform := PlatformConfig{
			Weibo: WeiboConfig{
				AppID:          "test-app-id",
				AppSecret: "test-Secret",
				TokenURL:       "http://example.com/token",
				WSURL:          "ws://example.com/ws",
				Timeout:        30,
			},
		}

		assert.Equal(t, "test-app-id", platform.Weibo.AppID)
		assert.Equal(t, "test-Secret", platform.Weibo.AppSecret)
		assert.Equal(t, 30, platform.Weibo.Timeout)
}

func TestAgentConfigStruct(t *testing.T) {
	agent := AgentConfig{
			Claude: ClaudeConfig{
				Enabled: true,
			},
			Codex: CodexConfig{
				APIKey:  "codex-key",
				Model:   "gpt-4",
				Enabled: false,
			},
		}

		assert.True(t, agent.Claude.Enabled)
		assert.False(t, agent.Codex.Enabled)
}

func TestSessionConfigStruct(t *testing.T) {
	session := SessionConfig{
		Timeout: 3600,
		MaxSize: 1000,
	}

	assert.Equal(t, 3600, session.Timeout)
	assert.Equal(t, 1000, session.MaxSize)
}

func TestLogConfigStruct(t *testing.T) {
	log := LogConfig{
		Level:  "debug",
		Format: "text",
		Output: "/var/log/app.log",
	}

	assert.Equal(t, "debug", log.Level)
	assert.Equal(t, "text", log.Format)
	assert.Equal(t, "/var/log/app.log", log.Output)
}