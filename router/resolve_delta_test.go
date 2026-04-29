package router

import (
	"testing"
)

func TestResolveDeltaFromSnapshot(t *testing.T) {
	tests := []struct {
		name         string
		previous     string
		next         string
		wantDelta    string
		wantSnapshot string
	}{
		{
			name:         "empty next",
			previous:     "hello",
			next:         "",
			wantDelta:    "",
			wantSnapshot: "",
		},
		{
			name:         "no change",
			previous:     "hello",
			next:         "hello",
			wantDelta:    "",
			wantSnapshot: "hello",
		},
		{
			name:         "ascii prefix match",
			previous:     "hello",
			next:         "hello world",
			wantDelta:    " world",
			wantSnapshot: "hello world",
		},
		{
			name:         "previous longer than next",
			previous:     "hello world",
			next:         "hello",
			wantDelta:    "",
			wantSnapshot: "hello",
		},
		{
			name:         "chinese prefix match",
			previous:     "你好",
			next:         "你好世界",
			wantDelta:    "世界",
			wantSnapshot: "你好世界",
		},
		{
			name:         "chinese partial change - shared first rune",
			previous:     "abc你",
			next:         "abc他",
			wantDelta:    "他",
			wantSnapshot: "abc他",
		},
		{
			name:         "chinese partial change - shared first two runes",
			previous:     "你好吗",
			next:         "你好啊",
			wantDelta:    "啊",
			wantSnapshot: "你好啊",
		},
		{
			name:         "chinese no common rune",
			previous:     "你",
			next:         "好",
			wantDelta:    "好",
			wantSnapshot: "好",
		},
		{
			name:         "mixed ascii and chinese",
			previous:     "回复：你好",
			next:         "回复：你好，世界",
			wantDelta:    "，世界",
			wantSnapshot: "回复：你好，世界",
		},
		{
			name:         "emoji prefix match",
			previous:     "🎉",
			next:         "🎉🎊",
			wantDelta:    "🎊",
			wantSnapshot: "🎉🎊",
		},
		{
			name:         "empty previous",
			previous:     "",
			next:         "hello",
			wantDelta:    "hello",
			wantSnapshot: "hello",
		},
		{
			name:         "chinese byte-boundary corruption case",
			previous:     "测试中文内容第一部分",
			next:         "测试中文内容第二部分",
			wantDelta:    "二部分",
			wantSnapshot: "测试中文内容第二部分",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			delta, snapshot := resolveDeltaFromSnapshot(tt.previous, tt.next)
			if delta != tt.wantDelta {
				t.Errorf("resolveDeltaFromSnapshot(%q, %q) delta = %q, want %q", tt.previous, tt.next, delta, tt.wantDelta)
			}
			if snapshot != tt.wantSnapshot {
				t.Errorf("resolveDeltaFromSnapshot(%q, %q) snapshot = %q, want %q", tt.previous, tt.next, snapshot, tt.wantSnapshot)
			}
		})
	}
}
