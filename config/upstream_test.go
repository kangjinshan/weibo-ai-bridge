package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newAgentEnabledConfig() *Config {
	return &Config{
		Platform: PlatformConfig{
			Weibo: WeiboConfig{
				AppID:     "test-app-id",
				Appsecret: "test-secret",
				Timeout:   30,
			},
		},
		Agent: AgentConfig{
			Claude: ClaudeConfig{Enabled: true},
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
}

func TestValidate_UpstreamDefaultsToWeibo(t *testing.T) {
	cfg := newAgentEnabledConfig()
	cfg.Upstream.Kind = ""
	require.NoError(t, cfg.Validate())
	assert.Equal(t, string(UpstreamKindWeibo), cfg.Upstream.Kind, "empty kind should be normalized to weibo")
}

func TestValidate_LocalUpstreamRequiresHubURL(t *testing.T) {
	cfg := newAgentEnabledConfig()
	cfg.Upstream = UpstreamConfig{
		Kind: string(UpstreamKindLocal),
		Local: LocalUpstreamConfig{
			DeviceToken: "tok",
		},
	}
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hub_url")
}

func TestValidate_LocalUpstreamRejectsHTTPScheme(t *testing.T) {
	cfg := newAgentEnabledConfig()
	cfg.Upstream = UpstreamConfig{
		Kind: string(UpstreamKindLocal),
		Local: LocalUpstreamConfig{
			HubURL:      "http://127.0.0.1:8181/ws",
			DeviceToken: "tok",
		},
	}
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ws://")
}

func TestValidate_LocalUpstreamRequiresToken(t *testing.T) {
	cfg := newAgentEnabledConfig()
	cfg.Upstream = UpstreamConfig{
		Kind: string(UpstreamKindLocal),
		Local: LocalUpstreamConfig{
			HubURL: "ws://127.0.0.1:8181/ws",
		},
	}
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "device_token")
}

func TestValidate_LocalUpstreamHappyPathSkipsWeiboFields(t *testing.T) {
	cfg := newAgentEnabledConfig()
	// 故意清掉微博字段：local 模式下不应再被要求
	cfg.Platform.Weibo.AppID = ""
	cfg.Platform.Weibo.Appsecret = ""
	cfg.Platform.Weibo.Timeout = 0
	cfg.Upstream = UpstreamConfig{
		Kind: string(UpstreamKindLocal),
		Local: LocalUpstreamConfig{
			HubURL:      "wss://hub.example.com/ws",
			DeviceToken: "tok",
			BridgeName:  "bridge-mac",
		},
	}
	require.NoError(t, cfg.Validate())
}

func TestValidate_UnknownUpstreamKindFails(t *testing.T) {
	cfg := newAgentEnabledConfig()
	cfg.Upstream.Kind = "telegram"
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid upstream.kind")
}
