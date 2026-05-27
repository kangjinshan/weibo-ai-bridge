package config

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/joho/godotenv"
)

// Config 应用配置
type Config struct {
	Platform PlatformConfig
	Upstream UpstreamConfig
	Agent    AgentConfig
	Session  SessionConfig
	Log      LogConfig
	HTTP     HTTPConfig
}

// HTTPConfig HTTP 服务器配置
type HTTPConfig struct {
	Port   string `toml:"port"`
	APIKey string `toml:"api_key"`
}

// PlatformConfig 平台配置
type PlatformConfig struct {
	Weibo WeiboConfig
}

// UpstreamKind 描述当前 bridge 连接的上游消息源。
type UpstreamKind string

const (
	// UpstreamKindWeibo 表示连接微博开放平台（默认，向后兼容）。
	UpstreamKindWeibo UpstreamKind = "weibo"
	// UpstreamKindLocal 表示连接本地 msghub WS。
	UpstreamKindLocal UpstreamKind = "local"
)

// UpstreamConfig 选择 bridge 的上游消息源。
type UpstreamConfig struct {
	Kind  string            `toml:"kind"`
	Local LocalUpstreamConfig `toml:"local"`
}

// LocalUpstreamConfig 用于 upstream.kind = "local" 时的 msghub 连接信息。
type LocalUpstreamConfig struct {
	HubURL      string `toml:"hub_url"`
	DeviceToken string `toml:"device_token"`
	BridgeName  string `toml:"bridge_name"`
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
	Hermes HermesConfig
	Gemini GeminiConfig
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

// HermesConfig Hermes Agent 配置
type HermesConfig struct {
	Model    string `toml:"model"`
	Profile  string `toml:"profile"`
	Provider string `toml:"provider"`
	Enabled  bool   `toml:"enabled"`
}

// GeminiConfig Gemini Agent 配置
type GeminiConfig struct {
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

	configPath := preloadEnvFiles()

	// 从配置文件加载
	if _, err := toml.DecodeFile(configPath, cfg); err != nil && !os.IsNotExist(err) {
		log.Printf("config: failed to decode %s: %v", configPath, err)
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
		cfg.Agent.Claude.Enabled = parseBoolEnv(val)
	}
	if val := os.Getenv("CODEX_API_KEY"); val != "" {
		cfg.Agent.Codex.APIKey = val
	}
	if val := os.Getenv("CODEX_MODEL"); val != "" {
		cfg.Agent.Codex.Model = val
	}
	if val := os.Getenv("CODEX_ENABLED"); val != "" {
		cfg.Agent.Codex.Enabled = parseBoolEnv(val)
	}
	if val := os.Getenv("HERMES_MODEL"); val != "" {
		cfg.Agent.Hermes.Model = val
	}
	if val := os.Getenv("HERMES_PROFILE"); val != "" {
		cfg.Agent.Hermes.Profile = val
	}
	if val := os.Getenv("HERMES_PROVIDER"); val != "" {
		cfg.Agent.Hermes.Provider = val
	}
	if val := os.Getenv("HERMES_ENABLED"); val != "" {
		cfg.Agent.Hermes.Enabled = parseBoolEnv(val)
	}
	if val := os.Getenv("GEMINI_MODEL"); val != "" {
		cfg.Agent.Gemini.Model = val
	}
	if val := os.Getenv("GEMINI_ENABLED"); val != "" {
		cfg.Agent.Gemini.Enabled = parseBoolEnv(val)
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
		if timeout, err := strconv.Atoi(val); err != nil {
			log.Printf("config: SESSION_TIMEOUT is not an integer (%q), keeping default", val)
		} else {
			cfg.Session.Timeout = timeout
		}
	}
	if val := os.Getenv("SESSION_MAX_SIZE"); val != "" {
		if maxSize, err := strconv.Atoi(val); err != nil {
			log.Printf("config: SESSION_MAX_SIZE is not an integer (%q), keeping default", val)
		} else {
			cfg.Session.MaxSize = maxSize
		}
	}
	if val := os.Getenv("SESSION_STORAGE_PATH"); val != "" {
		cfg.Session.StoragePath = val
	}
	if val := os.Getenv("SERVER_PORT"); val != "" {
		cfg.HTTP.Port = val
	}
	if val := os.Getenv("HTTP_API_KEY"); val != "" {
		cfg.HTTP.APIKey = val
	}

	// 上游选择与本地 msghub 配置
	if val := strings.TrimSpace(os.Getenv("BRIDGE_UPSTREAM_KIND")); val != "" {
		cfg.Upstream.Kind = val
	}
	if val := os.Getenv("MSGHUB_URL"); val != "" {
		cfg.Upstream.Local.HubURL = val
	}
	if val := os.Getenv("MSGHUB_DEVICE_TOKEN"); val != "" {
		cfg.Upstream.Local.DeviceToken = val
	}
	if val := os.Getenv("MSGHUB_BRIDGE_NAME"); val != "" {
		cfg.Upstream.Local.BridgeName = val
	}

	return cfg
}

func preloadEnvFiles() string {
	loadEnvFile(".env")

	configPath := resolveConfigPath()
	configDirEnvFile := filepath.Join(filepath.Dir(configPath), ".env")
	loadEnvFile(configDirEnvFile)

	return configPath
}

func loadEnvFile(path string) {
	if path == "" {
		return
	}

	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return
	}

	_ = godotenv.Load(path)
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

func parseBoolEnv(value string) bool {
	parsed, err := strconv.ParseBool(strings.TrimSpace(value))
	return err == nil && parsed
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
			Hermes: HermesConfig{
				Model:    "",
				Profile:  "",
				Provider: "",
				Enabled:  false,
			},
			Gemini: GeminiConfig{
				Model:   "",
				Enabled: false,
			},
		},
		Session: SessionConfig{
			Timeout:     3600,
			MaxSize:     1000,
			StoragePath: defaultSessionStoragePath(),
		},
		HTTP: HTTPConfig{
			Port: "5533",
		},
		Upstream: UpstreamConfig{
			Kind: string(UpstreamKindWeibo),
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
	kind := strings.TrimSpace(strings.ToLower(c.Upstream.Kind))
	if kind == "" {
		kind = string(UpstreamKindWeibo)
		c.Upstream.Kind = kind
	}

	switch UpstreamKind(kind) {
	case UpstreamKindWeibo:
		// 验证微博配置
		if strings.TrimSpace(c.Platform.Weibo.AppID) == "" {
			return errors.New("platform.weibo.app_id is required")
		}
		if strings.TrimSpace(c.Platform.Weibo.Appsecret) == "" {
			return errors.New("platform.weibo.app_secret is required")
		}
		// 验证超时配置
		if c.Platform.Weibo.Timeout <= 0 {
			return errors.New("platform.weibo.timeout must be positive")
		}
	case UpstreamKindLocal:
		hubURL := strings.TrimSpace(c.Upstream.Local.HubURL)
		if hubURL == "" {
			return errors.New("upstream.local.hub_url is required when upstream.kind = \"local\"")
		}
		lower := strings.ToLower(hubURL)
		if !strings.HasPrefix(lower, "ws://") && !strings.HasPrefix(lower, "wss://") {
			return fmt.Errorf("upstream.local.hub_url must use ws:// or wss:// scheme, got %q", hubURL)
		}
		if strings.TrimSpace(c.Upstream.Local.DeviceToken) == "" {
			return errors.New("upstream.local.device_token is required when upstream.kind = \"local\"")
		}
	default:
		return fmt.Errorf("invalid upstream.kind: %q (expected \"weibo\" or \"local\")", c.Upstream.Kind)
	}

	// 验证 Agent 配置
	// 各 Agent 的认证都可以由本地 CLI 自行管理，因此这里不强制要求 API Key。

	// 至少启用一个 Agent
	if !c.Agent.Claude.Enabled && !c.Agent.Codex.Enabled && !c.Agent.Hermes.Enabled && !c.Agent.Gemini.Enabled {
		return errors.New("at least one agent must be enabled")
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
