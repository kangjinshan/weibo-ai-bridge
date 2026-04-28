package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config 应用配置
type Config struct {
	Platform PlatformConfig
	Agent    AgentConfig
	Session  SessionConfig
	Log      LogConfig
}

// PlatformConfig 平台配置
type PlatformConfig struct {
	Weibo WeiboConfig
}

// WeiboConfig 微博配置
type WeiboConfig struct {
	AppID     string `toml:"app_id"`
	Appsecret string `toml:"app_secret"`
	TokenURL  string `toml:"token_url"`
	WSURL     string `toml:"ws_url"`
	Timeout   int    `toml:"timeout"`
}

// AgentConfig AI Agent 配置
type AgentConfig struct {
	Claude ClaudeConfig
	Codex  CodexConfig
}

// ClaudeConfig Claude Agent 配置
type ClaudeConfig struct {
	APIKey  string `toml:"api_key"`
	Model   string `toml:"model"`
	Enabled bool   `toml:"enabled"`
}

// CodexConfig Codex Agent 配置
type CodexConfig struct {
	APIKey  string `toml:"api_key"`
	Model   string `toml:"model"`
	Enabled bool   `toml:"enabled"`
}

// SessionConfig 会话配置
type SessionConfig struct {
	Timeout     int    `toml:"timeout"`
	MaxSize     int    `toml:"max_size"`
	StoragePath string `toml:"storage_path"`
}

// LogConfig 日志配置
type LogConfig struct {
	Level  string `toml:"level"`  // debug, info, warn, error
	Format string `toml:"format"` // json, text
	Output string `toml:"output"`
}

func defaultSessionStoragePath() string {
	if dir, err := os.UserConfigDir(); err == nil && strings.TrimSpace(dir) != "" {
		return filepath.Join(dir, "weibo-ai-bridge", "sessions")
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".weibo-ai-bridge", "sessions")
	}
	return filepath.Join("data", "sessions")
}

// Load 加载配置
func Load() *Config {
	cfg := defaultConfig()

	// 从配置文件加载
	if _, err := toml.DecodeFile(resolveConfigPath(), cfg); err == nil {
		// 配置文件加载成功，继续使用
	}

	// 环境变量覆盖
	if val := os.Getenv("WEIBO_APP_ID"); val != "" {
		cfg.Platform.Weibo.AppID = val
	}
	if val := firstEnv("WEIBO_APP_SECRET", "WEIBO_APP_Secret"); val != "" {
		cfg.Platform.Weibo.Appsecret = val
	}
	if val := os.Getenv("WEIBO_TOKEN_URL"); val != "" {
		cfg.Platform.Weibo.TokenURL = val
	}
	if val := os.Getenv("WEIBO_WS_URL"); val != "" {
		cfg.Platform.Weibo.WSURL = val
	}
	// Claude 配置：API Key 和模型由 Claude Code CLI 管理
	// 只需要控制是否启用 Claude Agent
	if val := os.Getenv("CLAUDE_ENABLED"); val != "" {
		cfg.Agent.Claude.Enabled = val == "true"
	}
	if val := os.Getenv("CODEX_API_KEY"); val != "" {
		cfg.Agent.Codex.APIKey = val
	}
	if val := os.Getenv("CODEX_MODEL"); val != "" {
		cfg.Agent.Codex.Model = val
	}
	if val := os.Getenv("CODEX_ENABLED"); val != "" {
		cfg.Agent.Codex.Enabled = val == "true"
	}
	if val := os.Getenv("LOG_LEVEL"); val != "" {
		cfg.Log.Level = val
	}
	if val := os.Getenv("LOG_FORMAT"); val != "" {
		cfg.Log.Format = val
	}
	if val := os.Getenv("LOG_OUTPUT"); val != "" {
		cfg.Log.Output = val
	}
	if val := os.Getenv("SESSION_TIMEOUT"); val != "" {
		timeout, _ := strconv.Atoi(val)
		cfg.Session.Timeout = timeout
	}
	if val := os.Getenv("SESSION_MAX_SIZE"); val != "" {
		maxSize, _ := strconv.Atoi(val)
		cfg.Session.MaxSize = maxSize
	}
	if val := os.Getenv("SESSION_STORAGE_PATH"); val != "" {
		cfg.Session.StoragePath = val
	}

	return cfg
}

func resolveConfigPath() string {
	if val := strings.TrimSpace(os.Getenv("CONFIG_PATH")); val != "" {
		return val
	}

	return filepath.Join("config", "config.toml")
}

func firstEnv(keys ...string) string {
	for _, key := range keys {
		if val := os.Getenv(key); val != "" {
			return val
		}
	}

	return ""
}

// LoadFromFile 从文件加载配置
func LoadFromFile(path string) (*Config, error) {
	var cfg Config
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// defaultConfig 默认配置
func defaultConfig() *Config {
	return &Config{
		Platform: PlatformConfig{
			Weibo: WeiboConfig{
				TokenURL: "http://open-im.api.weibo.com/open/auth/ws_token",
				WSURL:    "ws://open-im.api.weibo.com/ws/stream",
				Timeout:  30,
			},
		},
		Agent: AgentConfig{
			Claude: ClaudeConfig{
				Model:   "claude-3-5-sonnet-20241022",
				Enabled: true,
			},
			Codex: CodexConfig{
				Model:   "",
				Enabled: false,
			},
		},
		Session: SessionConfig{
			Timeout:     3600,
			MaxSize:     1000,
			StoragePath: defaultSessionStoragePath(),
		},
		Log: LogConfig{
			Level:  "info",
			Format: "json",
			Output: "stdout",
		},
	}
}

// Validate 验证配置
func (c *Config) Validate() error {
	// 验证微博配置
	if strings.TrimSpace(c.Platform.Weibo.AppID) == "" {
		return errors.New("platform.weibo.app_id is required")
	}
	if strings.TrimSpace(c.Platform.Weibo.Appsecret) == "" {
		return errors.New("platform.weibo.app_secret is required")
	}

	// 验证 Agent 配置
	// Claude 和 Codex 的认证都可以由本地 CLI 自行管理，因此这里不强制要求 API Key。

	// 至少启用一个 Agent
	if !c.Agent.Claude.Enabled && !c.Agent.Codex.Enabled {
		return errors.New("at least one agent must be enabled")
	}

	// 验证超时配置
	if c.Platform.Weibo.Timeout <= 0 {
		return errors.New("platform.weibo.timeout must be positive")
	}
	if c.Session.Timeout <= 0 {
		return errors.New("session.timeout must be positive")
	}
	if c.Session.MaxSize <= 0 {
		return errors.New("session.max_size must be positive")
	}

	// 验证日志级别
	validLogLevels := map[string]bool{
		"debug": true,
		"info":  true,
		"warn":  true,
		"error": true,
	}
	if !validLogLevels[c.Log.Level] {
		return fmt.Errorf("invalid log level: %s", c.Log.Level)
	}

	// 验证日志格式
	validFormats := map[string]bool{
		"json": true,
		"text": true,
	}
	if !validFormats[c.Log.Format] {
		return fmt.Errorf("invalid log format: %s", c.Log.Format)
	}

	return nil
}
