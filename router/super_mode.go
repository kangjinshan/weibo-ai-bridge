package router

import (
	"strconv"
	"strings"

	"github.com/kangjinshan/weibo-ai-bridge/session"
)

const (
	superModeContextKey        = "super_mode"
	superAutoApproveContextKey = "super_auto_approve"

	superFeedbackForClaudeKey = "super_feedback_for_claude"
	superFeedbackForCodexKey  = "super_feedback_for_codex"

	superFeedbackReadyForClaudeKey = "super_feedback_ready_for_claude"
	superFeedbackReadyForCodexKey  = "super_feedback_ready_for_codex"
)

func isSuperModeEnabled(sess *session.Session) bool {
	if sess == nil {
		return false
	}
	return sessionContextBool(sess, superModeContextKey)
}

func isSuperAutoApproveEnabled(sess *session.Session) bool {
	if sess == nil {
		return false
	}
	// /super on 等价于 allow all，开启 super 就默认开启自动审批。
	if sessionContextBool(sess, superModeContextKey) {
		return true
	}
	return sessionContextBool(sess, superAutoApproveContextKey)
}

func superFeedbackKeyForAgent(agentType string) string {
	switch strings.ToLower(strings.TrimSpace(agentType)) {
	case "claude":
		return superFeedbackForClaudeKey
	case "codex":
		return superFeedbackForCodexKey
	default:
		return ""
	}
}

func superFeedbackReadyKeyForAgent(agentType string) string {
	switch strings.ToLower(strings.TrimSpace(agentType)) {
	case "claude":
		return superFeedbackReadyForClaudeKey
	case "codex":
		return superFeedbackReadyForCodexKey
	default:
		return ""
	}
}

func superFeedbackReadyForAgent(sess *session.Session, agentType string) (string, bool) {
	if sess == nil {
		return "", false
	}
	feedbackKey := superFeedbackKeyForAgent(agentType)
	readyKey := superFeedbackReadyKeyForAgent(agentType)
	if feedbackKey == "" || readyKey == "" {
		return "", false
	}

	feedback := sessionContextString(sess, feedbackKey)
	ready := sessionContextBool(sess, readyKey)
	if !ready || strings.TrimSpace(feedback) == "" {
		return "", false
	}

	return feedback, true
}

func superFeedbackForAgent(sess *session.Session, agentType string) string {
	if sess == nil {
		return ""
	}
	feedbackKey := superFeedbackKeyForAgent(agentType)
	readyKey := superFeedbackReadyKeyForAgent(agentType)
	if feedbackKey == "" || readyKey == "" {
		return ""
	}
	if !sessionContextBool(sess, readyKey) {
		return ""
	}
	return sessionContextString(sess, feedbackKey)
}

func setSuperMode(sessionMgr *session.Manager, sessionID string, enabled bool) {
	if sessionMgr == nil || strings.TrimSpace(sessionID) == "" {
		return
	}

	_ = sessionMgr.UpdateSessionContextAtomically(sessionID, func(ctx map[string]interface{}) bool {
		changed := false
		if contextBoolMap(ctx, superModeContextKey) != enabled {
			ctx[superModeContextKey] = enabled
			changed = true
		}
		if contextBoolMap(ctx, superAutoApproveContextKey) != enabled {
			ctx[superAutoApproveContextKey] = enabled
			changed = true
		}
		if enabled {
			return changed
		}

		if strings.TrimSpace(contextStringMap(ctx, superFeedbackForClaudeKey)) != "" {
			ctx[superFeedbackForClaudeKey] = ""
			changed = true
		}
		if strings.TrimSpace(contextStringMap(ctx, superFeedbackForCodexKey)) != "" {
			ctx[superFeedbackForCodexKey] = ""
			changed = true
		}
		if contextBoolMap(ctx, superFeedbackReadyForClaudeKey) {
			ctx[superFeedbackReadyForClaudeKey] = false
			changed = true
		}
		if contextBoolMap(ctx, superFeedbackReadyForCodexKey) {
			ctx[superFeedbackReadyForCodexKey] = false
			changed = true
		}
		return changed
	})
}

func clearAllSuperFeedback(sessionMgr *session.Manager, sessionID string) {
	if sessionMgr == nil || strings.TrimSpace(sessionID) == "" {
		return
	}

	_ = sessionMgr.UpdateSessionContextAtomically(sessionID, func(ctx map[string]interface{}) bool {
		changed := false
		if strings.TrimSpace(contextStringMap(ctx, superFeedbackForClaudeKey)) != "" {
			ctx[superFeedbackForClaudeKey] = ""
			changed = true
		}
		if strings.TrimSpace(contextStringMap(ctx, superFeedbackForCodexKey)) != "" {
			ctx[superFeedbackForCodexKey] = ""
			changed = true
		}
		if contextBoolMap(ctx, superFeedbackReadyForClaudeKey) {
			ctx[superFeedbackReadyForClaudeKey] = false
			changed = true
		}
		if contextBoolMap(ctx, superFeedbackReadyForCodexKey) {
			ctx[superFeedbackReadyForCodexKey] = false
			changed = true
		}
		return changed
	})
}

func setSuperFeedbackForAgent(sessionMgr *session.Manager, sessionID, agentType, feedback string) bool {
	if sessionMgr == nil || strings.TrimSpace(sessionID) == "" {
		return false
	}
	feedbackKey := superFeedbackKeyForAgent(agentType)
	readyKey := superFeedbackReadyKeyForAgent(agentType)
	if feedbackKey == "" || readyKey == "" {
		return false
	}

	trimmed := strings.TrimSpace(feedback)
	return sessionMgr.UpdateSessionContextAtomically(sessionID, func(ctx map[string]interface{}) bool {
		// /super off 后，不允许后台旧任务再写回反馈。
		if !contextBoolMap(ctx, superModeContextKey) {
			return false
		}
		changed := false
		if strings.TrimSpace(contextStringMap(ctx, feedbackKey)) != trimmed {
			ctx[feedbackKey] = trimmed
			changed = true
		}
		ready := trimmed != ""
		if contextBoolMap(ctx, readyKey) != ready {
			ctx[readyKey] = ready
			changed = true
		}
		return changed
	})
}

func consumeSuperFeedbackForAgent(sessionMgr *session.Manager, sess *session.Session, agentType string) string {
	if sessionMgr == nil || sess == nil {
		return ""
	}

	feedback, ready := superFeedbackReadyForAgent(sess, agentType)
	if !ready {
		return ""
	}

	feedbackKey := superFeedbackKeyForAgent(agentType)
	readyKey := superFeedbackReadyKeyForAgent(agentType)
	if feedbackKey == "" || readyKey == "" {
		return ""
	}

	sessionMgr.UpdateSession(sess.ID, feedbackKey, "")
	sessionMgr.UpdateSession(sess.ID, readyKey, false)
	return strings.TrimSpace(feedback)
}

func clearSuperFeedbackForAgentIfMatch(sessionMgr *session.Manager, sessionID, agentType, expected string) bool {
	if sessionMgr == nil || strings.TrimSpace(sessionID) == "" {
		return false
	}
	expected = strings.TrimSpace(expected)
	if expected == "" {
		return false
	}

	sess, ok := sessionMgr.Get(strings.TrimSpace(sessionID))
	if !ok || sess == nil {
		return false
	}

	current := superFeedbackForAgent(sess, agentType)
	if strings.TrimSpace(current) != expected {
		return false
	}

	feedbackKey := superFeedbackKeyForAgent(agentType)
	readyKey := superFeedbackReadyKeyForAgent(agentType)
	if feedbackKey == "" || readyKey == "" {
		return false
	}

	sessionMgr.UpdateSession(sessionID, feedbackKey, "")
	sessionMgr.UpdateSession(sessionID, readyKey, false)
	return true
}

func contextBool(ctx map[string]interface{}, key string) bool {
	if ctx == nil {
		return false
	}
	raw, ok := ctx[key]
	if !ok || raw == nil {
		return false
	}

	switch v := raw.(type) {
	case bool:
		return v
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(v))
		return err == nil && parsed
	default:
		return false
	}
}

func contextString(ctx map[string]interface{}, key string) string {
	if ctx == nil {
		return ""
	}
	val, ok := ctx[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(val)
}

func sessionContextBool(sess *session.Session, key string) bool {
	if sess == nil {
		return false
	}
	val, ok := sess.ContextBool(key)
	return ok && val
}

func sessionContextString(sess *session.Session, key string) string {
	if sess == nil {
		return ""
	}
	val, ok := sess.ContextString(key)
	if !ok {
		return ""
	}
	return strings.TrimSpace(val)
}

func contextBoolMap(ctx map[string]interface{}, key string) bool {
	return contextBool(ctx, key)
}

func contextStringMap(ctx map[string]interface{}, key string) string {
	return contextString(ctx, key)
}
