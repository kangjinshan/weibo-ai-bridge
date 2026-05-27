package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/kangjinshan/weibo-ai-bridge/agent"
	"github.com/kangjinshan/weibo-ai-bridge/config"
	"github.com/kangjinshan/weibo-ai-bridge/platform/local"
	"github.com/kangjinshan/weibo-ai-bridge/platform/weibo"
	"github.com/kangjinshan/weibo-ai-bridge/router"
	"github.com/kangjinshan/weibo-ai-bridge/session"
)

// upstreamPlatform unifies the surface area cmd/server expects from any
// upstream adapter (microblog or msghub). Both weibo.Platform and
// local.Platform satisfy it without modification.
type upstreamPlatform interface {
	router.Platform // Reply + OpenReplyStream — used by Router itself
	Messages() <-chan *weibo.Message
	Start(ctx context.Context) error
	Stop() error
	UID() int64
}

func platformKind(cfg *config.Config) string {
	kind := strings.TrimSpace(strings.ToLower(cfg.Upstream.Kind))
	if kind == "" {
		return string(config.UpstreamKindWeibo)
	}
	return kind
}

// buildPlatform picks the upstream platform based on cfg.Upstream.Kind.
func buildPlatform(cfg *config.Config, agentMgr *agent.Manager, sessionMgr *session.Manager, logger *log.Logger) (upstreamPlatform, error) {
	switch platformKind(cfg) {
	case string(config.UpstreamKindLocal):
		deps := local.PlatformDeps{
			Agents:   agentListerFromManager(agentMgr),
			Commands: commandListerFromRouter(sessionMgr, agentMgr),
			Sessions: sessionBinderFromManager(sessionMgr),
			Logger:   logger,
		}
		return local.NewPlatform(
			cfg.Upstream.Local.HubURL,
			cfg.Upstream.Local.DeviceToken,
			cfg.Upstream.Local.BridgeName,
			deps,
		)

	case string(config.UpstreamKindWeibo), "":
		plat, err := weibo.NewPlatform(cfg.Platform.Weibo.AppID, cfg.Platform.Weibo.Appsecret)
		if err != nil {
			return nil, err
		}
		plat.Configure(
			cfg.Platform.Weibo.TokenURL,
			cfg.Platform.Weibo.WSURL,
			time.Duration(cfg.Platform.Weibo.Timeout)*time.Second,
		)
		return plat, nil

	default:
		return nil, fmt.Errorf("unknown upstream.kind %q", cfg.Upstream.Kind)
	}
}

// agentManagerLister adapts agent.Manager.ListAgents() into local.AgentLister.
type agentManagerLister struct{ mgr *agent.Manager }

func agentListerFromManager(mgr *agent.Manager) local.AgentLister {
	return &agentManagerLister{mgr: mgr}
}

func (a *agentManagerLister) ListAgents() []local.AgentDescriptor {
	if a == nil || a.mgr == nil {
		return nil
	}
	registered := a.mgr.ListAgents()
	out := make([]local.AgentDescriptor, 0, len(registered))
	for _, agt := range registered {
		name := agt.Name()
		if name == "" {
			continue
		}
		out = append(out, local.AgentDescriptor{
			ID:          publicAgentID(name),
			DisplayName: humanAgentName(name),
		})
	}
	return out
}

// publicAgentID maps the bridge-internal registry name to the externally
// advertised agent id. The session layer already uses "claude" as the public
// id for the "claude-code" internal name, so we keep the same mapping.
func publicAgentID(internal string) string {
	if internal == "claude-code" {
		return "claude"
	}
	return internal
}

func humanAgentName(internal string) string {
	switch internal {
	case "claude-code":
		return "Claude"
	case "codex":
		return "Codex"
	case "hermes":
		return "Hermes"
	case "gemini":
		return "Gemini"
	}
	return internal
}

// routerCommandLister bridges router.CommandHandler.ListCommands() into
// local.CommandLister without exposing the whole CommandHandler.
type routerCommandLister struct{ handler *router.CommandHandler }

func commandListerFromRouter(sessionMgr *session.Manager, agentMgr *agent.Manager) local.CommandLister {
	return &routerCommandLister{handler: router.NewCommandHandler(sessionMgr, agentMgr)}
}

func (r *routerCommandLister) ListCommands() []local.CommandDescriptor {
	if r == nil || r.handler == nil {
		return nil
	}
	src := r.handler.ListCommands()
	out := make([]local.CommandDescriptor, 0, len(src))
	for _, c := range src {
		out = append(out, local.CommandDescriptor{
			Name:        c.Name,
			Description: c.Description,
			Args:        c.Args,
		})
	}
	return out
}

// sessionBinderAdapter routes local.SessionBinder calls into the existing
// session.Manager API. The semantic contract is "after Bind returns, the
// active session for userID has AgentType=agentType": create the session if
// needed, otherwise rewrite its AgentType if msghub told us the conv now
// belongs to a different agent.
type sessionBinderAdapter struct{ mgr *session.Manager }

func sessionBinderFromManager(mgr *session.Manager) local.SessionBinder {
	return &sessionBinderAdapter{mgr: mgr}
}

func (s *sessionBinderAdapter) BindActiveSessionAgent(userID, agentType string) {
	if s == nil || s.mgr == nil || userID == "" || agentType == "" {
		return
	}
	sess := s.mgr.GetOrCreateActiveSession(userID, agentType)
	if sess == nil {
		return
	}
	if sess.AgentType != agentType {
		s.mgr.SetSessionAgentType(sess.ID, agentType)
	}
}
