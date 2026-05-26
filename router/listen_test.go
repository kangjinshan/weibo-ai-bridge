package router

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kangjinshan/weibo-ai-bridge/agent"
	"github.com/kangjinshan/weibo-ai-bridge/platform/weibo"
	"github.com/kangjinshan/weibo-ai-bridge/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNativeLogLineTextsCodexAssistantMessage(t *testing.T) {
	line := `{"timestamp":"2026-05-26T00:00:00Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"第一段"},{"type":"output_text","text":"第二段"}]}}`

	texts := nativeLogLineTexts("codex", line)

	assert.Equal(t, []string{"AI: 第一段\n第二段"}, texts)
}

func TestNativeLogLineTextsCodexSkipsEventMessagesToAvoidDuplicates(t *testing.T) {
	line := `{"timestamp":"2026-05-26T00:00:00Z","type":"event_msg","payload":{"type":"agent_message","message":"重复内容","phase":"commentary"}}`

	texts := nativeLogLineTexts("codex", line)

	assert.Empty(t, texts)
}

func TestNativeLogLineTextsCodexFiltersToolItems(t *testing.T) {
	lines := []string{
		`{"timestamp":"2026-05-26T00:00:00Z","type":"response_item","payload":{"type":"function_call","name":"shell","arguments":"{}"}}`,
		`{"timestamp":"2026-05-26T00:00:00Z","type":"response_item","payload":{"type":"function_call_output","output":"done"}}`,
	}

	for _, line := range lines {
		texts := nativeLogLineTexts("codex", line)

		assert.Empty(t, texts)
	}
}

func TestReadHermesListenMessagesFiltersToolMessages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session_1.json")
	data := `{
		"messages": [
			{"role": "user", "content": "你好"},
			{"role": "tool", "content": "{\"output\":\"工具输出\"}"},
			{"role": "assistant", "content": "回复"}
		]
	}`
	require.NoError(t, os.WriteFile(path, []byte(data), 0o644))

	messages := readHermesListenMessages(path)

	assert.Equal(t, []string{"用户: 你好", "AI: 回复"}, messages)
}

func TestHandleMessageListenStartsNativeLogListenerAndUnlistenStopsIt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))

	sessionPath := filepath.Join(home, ".codex", "sessions", "2026", "05", "26", "rollout-test-thread.jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(sessionPath), 0o755))
	require.NoError(t, os.WriteFile(sessionPath, []byte(`{"timestamp":"2026-05-26T00:00:00Z","type":"session_meta","payload":{"id":"thread-1","cwd":"/tmp/project"}}`+"\n"), 0o644))

	platform := &listenTestPlatform{}
	sessionMgr := session.NewManager(session.ManagerConfig{Timeout: 3600, MaxSize: 10})
	agentMgr := agent.NewManager()
	agentMgr.Register(&MockAgent{name: "codex", available: true})
	router := NewRouter(platform, sessionMgr, agentMgr)

	err := router.HandleMessage(context.Background(), &weibo.Message{
		ID:      "listen-1",
		Type:    weibo.MessageTypeText,
		Content: "/listen 1",
		UserID:  "user-1",
	})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return mockPlatformContains(platform, "开始监听")
	}, time.Second, 10*time.Millisecond)

	appendLine(t, sessionPath, `{"timestamp":"2026-05-26T00:00:01Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"监听输出"}]}}`)
	require.Eventually(t, func() bool {
		return mockPlatformContains(platform, "监听输出")
	}, 2*time.Second, 20*time.Millisecond)

	err = router.HandleMessage(context.Background(), &weibo.Message{
		ID:      "unlisten-1",
		Type:    weibo.MessageTypeText,
		Content: "/unlisten",
		UserID:  "user-1",
	})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return mockPlatformContains(platform, "已停止监听")
	}, time.Second, 10*time.Millisecond)

	before := platform.Count()
	appendLine(t, sessionPath, `{"timestamp":"2026-05-26T00:00:02Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"不应输出"}]}}`)
	time.Sleep(150 * time.Millisecond)

	for _, reply := range platform.Replies()[before:] {
		assert.NotContains(t, reply, "不应输出")
	}
}

type listenTestPlatform struct {
	mu      sync.Mutex
	replies []string
}

func (p *listenTestPlatform) Reply(ctx context.Context, messageID string, content string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.replies = append(p.replies, content)
	return nil
}

func (p *listenTestPlatform) Replies() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.replies...)
}

func (p *listenTestPlatform) Count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.replies)
}

func appendLine(t *testing.T, path, line string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	defer f.Close()
	_, err = f.WriteString(line + "\n")
	require.NoError(t, err)
}

func mockPlatformContains(platform *listenTestPlatform, needle string) bool {
	for _, reply := range platform.Replies() {
		if strings.Contains(reply, needle) {
			return true
		}
	}
	return false
}
