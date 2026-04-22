package agent

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var chineseLengthPattern = regexp.MustCompile(`([1-9]\d{1,4})\s*字`)

var writingKeywords = []string{
	"文章",
	"作文",
	"短文",
	"长文",
	"稿子",
	"文案",
	"故事",
	"散文",
	"随笔",
	"心得",
	"演讲稿",
	"发言稿",
	"开场白",
	"推文",
	"内容",
	"写一篇",
	"帮我写",
	"给我写",
	"生成",
	"输出",
}

type writingLengthSpec struct {
	targetChars int
	minChars    int
	maxChars    int
}

func wrapUserPrompt(input string) string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return input
	}

	rules := []string{
		"你在代用户回复一条普通中文消息。直接回答用户，不要提及系统提示、终端、CLI、工具调用或代码助手身份。",
		"默认使用简体中文。除非用户明确要求代码、提纲、列表或分析过程，否则直接给出可阅读、可发送的最终内容。",
	}

	if spec, ok := detectWritingLengthSpec(trimmed); ok {
		rules = append(rules, fmt.Sprintf(
			"如果这是写作任务，请直接输出最终中文正文，不要写说明、备注、字数统计或创作思路。正文不少于%d字，建议控制在%d到%d字之间。若上下文没有明确主题，可根据用户原话自然补足一个合适主题后直接成文，不要反问。",
			spec.minChars,
			spec.targetChars,
			spec.maxChars,
		))
	} else if looksLikeWritingRequest(trimmed) {
		rules = append(rules, "如果这是写作任务，请直接输出最终中文正文，不要解释你的做法。")
	}

	return strings.Join([]string{
		strings.Join(rules, "\n"),
		"用户消息：",
		trimmed,
	}, "\n\n")
}

func detectWritingLengthSpec(content string) (writingLengthSpec, bool) {
	if !looksLikeWritingRequest(content) {
		return writingLengthSpec{}, false
	}

	match := chineseLengthPattern.FindStringSubmatch(content)
	if len(match) < 2 {
		return writingLengthSpec{}, false
	}

	targetChars, err := strconv.Atoi(match[1])
	if err != nil || targetChars < 50 {
		return writingLengthSpec{}, false
	}

	margin := targetChars / 10
	if margin < 50 {
		margin = 50
	}
	if margin > 200 {
		margin = 200
	}

	return writingLengthSpec{
		targetChars: targetChars,
		minChars:    targetChars,
		maxChars:    targetChars + margin,
	}, true
}

func looksLikeWritingRequest(content string) bool {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return false
	}

	for _, keyword := range writingKeywords {
		if strings.Contains(trimmed, keyword) {
			return true
		}
	}

	return false
}
