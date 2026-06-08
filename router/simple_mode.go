package router

import (
	"strings"

	"github.com/kangjinshan/weibo-ai-bridge/session"
)

const simpleModeContextKey = "simple_mode"

func isSimpleModeEnabled(sess *session.Session) bool {
	return sessionContextBool(sess, simpleModeContextKey)
}

func setSimpleMode(manager *session.Manager, sessionID string, enabled bool) {
	if manager == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	manager.UpdateSession(sessionID, simpleModeContextKey, enabled)
}

func (r *Router) simpleModeForMessage(msg *Message) bool {
	if r == nil || r.sessionMgr == nil || msg == nil {
		return false
	}

	sessionIDs := []string{}
	if sessionID := strings.TrimSpace(msg.SessionID); sessionID != "" {
		sessionIDs = append(sessionIDs, sessionID)
	}
	if activeID := strings.TrimSpace(r.sessionMgr.GetActiveSessionID(msg.UserID)); activeID != "" {
		sessionIDs = append(sessionIDs, activeID)
	}

	for _, sessionID := range sessionIDs {
		sess, ok := r.sessionMgr.Get(sessionID)
		if ok && sess.UserID == msg.UserID {
			return isSimpleModeEnabled(sess)
		}
	}

	return false
}
