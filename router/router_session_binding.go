package router

import (
	"strings"

	"github.com/kangjinshan/weibo-ai-bridge/session"
)

const pendingNativeSessionPrefix = "pending-native:"

func pendingNativeSessionID(userID string) string {
	return pendingNativeSessionPrefix + strings.TrimSpace(userID)
}

func isPendingNativeSessionID(userID, sessionID string) bool {
	return strings.TrimSpace(sessionID) == pendingNativeSessionID(userID)
}

func shouldAdoptNativeSessionID(sess *session.Session, nativeID string) bool {
	if sess == nil {
		return false
	}

	nativeID = strings.TrimSpace(nativeID)
	if nativeID == "" {
		return false
	}

	currentID := strings.TrimSpace(sess.ID)
	if currentID == "" || currentID == nativeID {
		return false
	}

	if isPendingNativeSessionID(sess.UserID, currentID) {
		return true
	}

	userPrefix := strings.TrimSpace(sess.UserID) + "-"
	return strings.HasPrefix(currentID, userPrefix)
}

func (r *Router) bindAgentNativeSessionID(sess *session.Session, sessionKey, nativeID string) *session.Session {
	if sess == nil {
		return nil
	}

	nativeID = strings.TrimSpace(nativeID)
	if sessionKey == "" || nativeID == "" {
		return sess
	}

	if sess.Context == nil {
		sess.Context = make(map[string]interface{})
	}
	sess.Context[sessionKey] = nativeID

	if r.sessionMgr == nil {
		return sess
	}

	oldID := strings.TrimSpace(sess.ID)
	r.sessionMgr.UpdateSession(oldID, sessionKey, nativeID)

	if !shouldAdoptNativeSessionID(sess, nativeID) {
		return sess
	}

	adopted, ok := r.sessionMgr.AdoptSessionID(oldID, nativeID)
	if !ok || adopted == nil {
		return sess
	}
	r.rekeyInteractiveSession(oldID, adopted.ID)
	return adopted
}

func (r *Router) rekeyInteractiveSession(oldID, newID string) {
	oldID = strings.TrimSpace(oldID)
	newID = strings.TrimSpace(newID)
	if oldID == "" || newID == "" || oldID == newID {
		return
	}

	r.liveMu.Lock()
	defer r.liveMu.Unlock()

	state, ok := r.liveSessions[oldID]
	if !ok {
		return
	}
	if _, exists := r.liveSessions[newID]; !exists {
		r.liveSessions[newID] = state
	}
	delete(r.liveSessions, oldID)
}
