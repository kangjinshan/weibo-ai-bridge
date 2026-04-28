package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateSuccess(t *testing.T) {
	cfg := &Config{
		Platform: PlatformConfig{
			Weibo: WeiboConfig{
				AppID:          "test-app-id",
				Appsecret: "test-Secret",
				Timeout:        30,
			},
		},
		Agent: AgentConfig{
			Claude: ClaudeConfig{
				Enabled: true,
			},
			Codex: CodexConfig{
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
			Output: "stdout",
		},
	}

	err := cfg.Validate()
	assert.NoError(t, err)
}

func TestValidateEmptyAppID(t *testing.T) {
	cfg := &Config{
		Platform: PlatformConfig{
			Weibo: WeiboConfig{
				AppID:          "",
				Appsecret: "test-Secret",
				Timeout:        30,
			},
		},
		Agent: AgentConfig{
			Claude: ClaudeConfig{
				Enabled: true,
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

	err := cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "app_id")
}

func TestValidateEmptyAppsecret(t *testing.T) {
	cfg := &Config{
		Platform: PlatformConfig{
			Weibo: WeiboConfig{
				AppID:          "test-app-id",
				Appsecret: "",
				Timeout:        30,
			},
		},
		Agent: AgentConfig{
			Claude: ClaudeConfig{
				Enabled: true,
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

	err := cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "app_secret")
}

func TestValidateNoEnabledAgent(t *testing.T) {
	cfg := &Config{
		Platform: PlatformConfig{
			Weibo: WeiboConfig{
				AppID:          "test-app-id",
				Appsecret: "test-Secret",
				Timeout:        30,
			},
		},
		Agent: AgentConfig{
			Claude: ClaudeConfig{
				Enabled: false,
			},
			Codex: CodexConfig{
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

	err := cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "agent")
}

func TestValidateCodexEnabledWithoutAPIKey(t *testing.T) {
	cfg := &Config{
		Platform: PlatformConfig{
			Weibo: WeiboConfig{
				AppID:          "test-app-id",
				Appsecret: "test-Secret",
				Timeout:        30,
			},
		},
		Agent: AgentConfig{
			Claude: ClaudeConfig{
				Enabled: false,
			},
			Codex: CodexConfig{
				APIKey:  "",
				Model:   "gpt-4",
				Enabled: true,
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

	err := cfg.Validate()
	assert.NoError(t, err)
}

func TestValidateInvalidLogLevel(t *testing.T) {
	cfg := &Config{
		Platform: PlatformConfig{
			Weibo: WeiboConfig{
				AppID:          "test-app-id",
				Appsecret: "test-Secret",
				Timeout:        30,
			},
		},
		Agent: AgentConfig{
			Claude: ClaudeConfig{
				Enabled: true,
			},
		},
		Session: SessionConfig{
			Timeout: 3600,
			MaxSize: 1000,
		},
		Log: LogConfig{
			Level:  "invalid",
			Format: "json",
		},
	}

	err := cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "log level")
}

func TestValidateInvalidLogFormat(t *testing.T) {
	cfg := &Config{
		Platform: PlatformConfig{
			Weibo: WeiboConfig{
				AppID:          "test-app-id",
				Appsecret: "test-Secret",
				Timeout:        30,
			},
		},
		Agent: AgentConfig{
			Claude: ClaudeConfig{
				Enabled: true,
			},
		},
		Session: SessionConfig{
			Timeout: 3600,
			MaxSize: 1000,
		},
		Log: LogConfig{
			Level:  "info",
			Format: "invalid",
		},
	}

	err := cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "log format")
}

func TestValidateInvalidTimeout(t *testing.T) {
	cfg := &Config{
		Platform: PlatformConfig{
			Weibo: WeiboConfig{
				AppID:          "test-app-id",
				Appsecret: "test-Secret",
				Timeout:        -1,
			},
		},
		Agent: AgentConfig{
			Claude: ClaudeConfig{
				Enabled: true,
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

	err := cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "timeout")
}

func TestValidateInvalidSessionTimeout(t *testing.T) {
	cfg := &Config{
		Platform: PlatformConfig{
			Weibo: WeiboConfig{
				AppID:          "test-app-id",
				Appsecret: "test-Secret",
				Timeout:        30,
			},
		},
		Agent: AgentConfig{
			Claude: ClaudeConfig{
				Enabled: true,
			},
		},
		Session: SessionConfig{
			Timeout: 0,
			MaxSize: 1000,
		},
		Log: LogConfig{
			Level:  "info",
			Format: "json",
		},
	}

	err := cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "session.timeout")
}

func TestValidateInvalidMaxSize(t *testing.T) {
	cfg := &Config{
		Platform: PlatformConfig{
			Weibo: WeiboConfig{
				AppID:          "test-app-id",
				Appsecret: "test-Secret",
				Timeout:        30,
			},
		},
		Agent: AgentConfig{
			Claude: ClaudeConfig{
				Enabled: true,
			},
		},
		Session: SessionConfig{
			Timeout: 3600,
			MaxSize: 0,
		},
		Log: LogConfig{
			Level:  "info",
			Format: "json",
		},
	}

	err := cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "session.max_size")
}
