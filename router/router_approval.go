package router

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/kangjinshan/weibo-ai-bridge/agent"
)

var approvalMentionPattern = regexp.MustCompile(`@\S+`)

func formatApprovalPrompt(toolName, toolInput string) string {
	toolName = strings.TrimSpace(toolName)
	toolInput = strings.TrimSpace(toolInput)

	if toolName == "" && toolInput == "" {
		return approvalHintMessage()
	}

	if toolInput == "" {
		return fmt.Sprintf("⚠️ 需要授权\n\nAgent 想执行：`%s`\n\n请回复：允许 / 取消 / 允许所有\n允许所有表示本对话内后续授权将自动通过。", toolName)
	}

	return fmt.Sprintf("⚠️ 需要授权\n\nAgent 想执行：`%s`\n\n```text\n%s\n```\n\n请回复：允许 / 取消 / 允许所有\n允许所有表示本对话内后续授权将自动通过。", toolName, toolInput)
}

func approvalHintMessage() string {
	return "当前正在等待授权，请回复：取消 / 允许 / 允许所有。"
}

func parseApprovalAction(content string) (agent.ApprovalAction, bool) {
	normalized := normalizeApprovalResponse(content)

	for _, word := range []string{
		"allow all", "allowall", "approve all", "yes all",
		"允许所有", "允许全部", "全部允许", "所有允许", "都允许", "全部同意",
	} {
		if normalized == word {
			return agent.ApprovalActionAllowAll, true
		}
	}

	for _, word := range []string{"allow", "yes", "y", "ok", "允许", "同意", "可以", "好", "好的", "是", "确认", "approve"} {
		if normalized == word {
			return agent.ApprovalActionAllow, true
		}
	}

	for _, word := range []string{"deny", "no", "n", "reject", "拒绝", "不允许", "不行", "不", "否", "取消", "cancel"} {
		if normalized == word {
			return agent.ApprovalActionCancel, true
		}
	}

	return "", false
}

func normalizeApprovalResponse(content string) string {
	content = strings.ReplaceAll(content, "\u3000", " ")
	content = strings.TrimSpace(strings.ToLower(content))
	content = approvalMentionPattern.ReplaceAllString(content, " ")
	content = strings.Join(strings.Fields(content), " ")
	content = strings.Trim(content, " \t\r\n,.!?;:，。！？；：")
	return content
}
