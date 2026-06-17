package router

import (
	"fmt"
	"regexp"
	"strconv"
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

// formatQuestionPrompt 把一个 AskUserQuestion 问题渲染成带编号选项的纯文本提示。
// idx 为 0 基问题序号，total 为问题总数。
func formatQuestionPrompt(q agent.UserQuestion, idx, total int) string {
	var sb strings.Builder

	sb.WriteString("❓ ")
	sb.WriteString(strings.TrimSpace(q.Question))
	if total > 1 {
		sb.WriteString(fmt.Sprintf("（%d/%d）", idx+1, total))
	}
	if q.MultiSelect {
		sb.WriteString("（可多选）")
	}
	sb.WriteString("\n\n")

	for i, opt := range q.Options {
		sb.WriteString(fmt.Sprintf("%d. %s", i+1, strings.TrimSpace(opt.Label)))
		if desc := strings.TrimSpace(opt.Description); desc != "" {
			sb.WriteString(" — ")
			sb.WriteString(desc)
		}
		sb.WriteString("\n")
	}

	if q.MultiSelect {
		sb.WriteString("\n请回复编号选择（多选用逗号分隔），或直接回复内容。")
	} else {
		sb.WriteString("\n请回复编号选择，或直接回复内容。")
	}

	return sb.String()
}

// resolveQuestionAnswer 把用户回复解析成 AskUserQuestion 的答案文本。
// 单选解析单个编号，多选按逗号/空格拆分多个编号；无法解析为编号时回退原文。
func resolveQuestionAnswer(q agent.UserQuestion, reply string) string {
	reply = strings.TrimSpace(reply)

	if q.MultiSelect {
		parts := strings.FieldsFunc(reply, func(r rune) bool {
			return r == ',' || r == '，' || r == ' ' || r == '\t' || r == '　'
		})
		var labels []string
		allNumeric := len(parts) > 0
		for _, p := range parts {
			idx, err := strconv.Atoi(strings.TrimSpace(p))
			if err != nil || idx < 1 || idx > len(q.Options) {
				allNumeric = false
				break
			}
			labels = append(labels, q.Options[idx-1].Label)
		}
		if allNumeric && len(labels) > 0 {
			return strings.Join(labels, ", ")
		}
		return reply
	}

	if idx, err := strconv.Atoi(reply); err == nil && idx >= 1 && idx <= len(q.Options) {
		return q.Options[idx-1].Label
	}
	return reply
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
