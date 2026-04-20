package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yourusername/weibo-ai-bridge/agent"
	"github.com/yourusername/weibo-ai-bridge/config"
	"github.com/yourusername/weibo-ai-bridge/platform/weibo"
	"github.com/yourusername/weibo-ai-bridge/router"
	"github.com/yourusername/weibo-ai-bridge/session"
)

var (
	logger *log.Logger
)

func main() {
	// 初始化日志
	initLogger()

	// 加载配置
	cfg := config.Load()
	logger.Printf("Configuration loaded: log_level=%s, log_format=%s", cfg.Log.Level, cfg.Log.Format)

	// 验证配置
	if err := cfg.Validate(); err != nil {
		logger.Fatalf("Configuration validation failed: %v", err)
	}

	// 创建上下文和取消函数
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 创建会话管理器
	sessionMgr := session.NewManager(session.ManagerConfig{
		Timeout: cfg.Session.Timeout,
		MaxSize: cfg.Session.MaxSize,
	})
	logger.Printf("Session manager initialized: timeout=%ds, max_size=%d", cfg.Session.Timeout, cfg.Session.MaxSize)

	// 创建 Agent 管理器并注册 Agent
	agentMgr := agent.NewManager()

	// 注册 Claude Agent
	if cfg.Agent.Claude.Enabled {
		claudeAgent := agent.NewClaudeCodeAgent()
		agentMgr.Register(claudeAgent)
		agentMgr.SetDefault("claude-code")
		logger.Printf("Claude agent registered: model=%s", cfg.Agent.Claude.Model)
	}

	// 注册 Codex Agent（如果启用）
	if cfg.Agent.Codex.Enabled {
		codexAgent := agent.NewCodeXAgent()
		agentMgr.Register(codexAgent)
		if !cfg.Agent.Claude.Enabled {
			agentMgr.SetDefault("codex")
		}
		logger.Printf("Codex agent registered: model=%s", cfg.Agent.Codex.Model)
	}

	logger.Printf("Agent manager initialized: count=%d, default=%s", agentMgr.Count(), "claude")

	// 创建微博平台适配器
	platform, err := weibo.NewPlatform(cfg.Platform.Weibo.AppID, cfg.Platform.Weibo.AppSecret)
	if err != nil {
		logger.Fatalf("Failed to create platform: %v", err)
	}
	logger.Printf("Platform created: app_id=%s", cfg.Platform.Weibo.AppID)

	// 创建消息路由器
	msgRouter := router.NewRouter(platform, sessionMgr, agentMgr)
	logger.Printf("Message router created")

	// 启动平台
	if err := platform.Start(ctx); err != nil {
		logger.Fatalf("Failed to start platform: %v", err)
	}
	logger.Printf("Platform started successfully")

	// 启动消息处理循环
	go processMessages(ctx, platform, msgRouter)

	// 设置 HTTP 路由
	mux := http.NewServeMux()
	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("/stats", statsHandler(sessionMgr, agentMgr))

	// 获取服务端口
	port := os.Getenv("SERVER_PORT")
	if port == "" {
		port = "5533"  // 默认端口
	}

	// 创建 HTTP 服务器
	server := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

// 启动 HTTP 服务器（在 goroutine 中）
	go func() {
		logger.Printf("HTTP server starting on %s", server.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatalf("HTTP server error: %v", err)
		}
	}()

	// 等待中断信号
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Println("Shutdown signal received, shutting down gracefully...")

	// 创建超时上下文用于优雅关闭
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	// 停止平台
	platform.Stop()
	logger.Println("Platform stopped")

	// 关闭 HTTP 服务器
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Printf("HTTP server shutdown error: %v", err)
	} else {
		logger.Println("HTTP server shutdown completed")
	}

	// 取消主上下文
	cancel()

	logger.Println("Server shutdown completed")
}

// initLogger 初始化日志
func initLogger() {
	logger = log.New(os.Stdout, "[weibo-ai-bridge] ", log.LstdFlags|log.Lshortfile)
}

// processMessages 处理消息循环
func processMessages(ctx context.Context, platform *weibo.Platform, msgRouter *router.Router) {
	for {
		select {
		case <-ctx.Done():
			logger.Println("Message processing stopped")
			return
		case msg, ok := <-platform.Messages():
			if !ok {
				logger.Println("Message channel closed")
				return
			}

			logger.Printf("Processing message: id=%s, type=%s, user=%s", msg.ID, msg.Type, msg.UserID)

			// 处理消息
			if err := msgRouter.HandleMessage(ctx, msg); err != nil {
				logger.Printf("Failed to handle message: id=%s, error=%v", msg.ID, err)
			} else {
				logger.Printf("Message processed successfully: id=%s", msg.ID)
			}
		}
	}
}

// healthHandler 健康检查处理器
func healthHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"status":"ok","service":"weibo-ai-bridge"}`)
}

// statsHandler 统计信息处理器
func statsHandler(sessionMgr *session.Manager, agentMgr *agent.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		stats := map[string]interface{}{
			"sessions": map[string]interface{}{
				"count": sessionMgr.Count(),
			},
			"agents": map[string]interface{}{
				"count": agentMgr.Count(),
				"list":  getAgentNames(agentMgr),
			},
			"timestamp": time.Now().Unix(),
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(stats); err != nil {
			logger.Printf("Failed to encode stats: %v", err)
		}
	}
}

// getAgentNames 获取所有 Agent 名称
func getAgentNames(agentMgr *agent.Manager) []string {
	agents := agentMgr.ListAgents()
	names := make([]string, 0, len(agents))
	for _, a := range agents {
		names = append(names, a.Name())
	}
	return names
}