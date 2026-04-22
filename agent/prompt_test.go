package agent

import (
	"strings"
	"testing"
)

func TestWrapUserPrompt_AddsLongFormConstraintForChineseLengthRequests(t *testing.T) {
	input := "再试一下输出1000字的文章"

	got := wrapUserPrompt(input)

	if !strings.Contains(got, "正文不少于1000字") {
		t.Fatalf("expected long-form constraint, got %q", got)
	}
	if !strings.Contains(got, "用户消息：\n\n再试一下输出1000字的文章") {
		t.Fatalf("expected original user input to be preserved, got %q", got)
	}
}

func TestWrapUserPrompt_KeepsGenericMessagesDirect(t *testing.T) {
	input := "今天天气怎么样"

	got := wrapUserPrompt(input)

	if strings.Contains(got, "正文不少于") {
		t.Fatalf("did not expect long-form constraint, got %q", got)
	}
	if !strings.Contains(got, "用户消息：\n\n今天天气怎么样") {
		t.Fatalf("expected original user input to be preserved, got %q", got)
	}
}

func TestWrapUserPrompt_EncouragesMarkdownAndParagraphs(t *testing.T) {
	input := "请介绍一下软件工程实践"

	got := wrapUserPrompt(input)

	if !strings.Contains(got, "优先使用简洁 Markdown 列表或小标题") {
		t.Fatalf("expected markdown guidance, got %q", got)
	}
	if !strings.Contains(got, "段落之间保留空行") {
		t.Fatalf("expected paragraph guidance, got %q", got)
	}
}
