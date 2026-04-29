package agent

import "strings"

func buildCodexThreadStartParams(approvalPolicy, sandbox, model string, rawEvents bool) map[string]any {
	params := map[string]any{
		"approvalPolicy":         strings.TrimSpace(approvalPolicy),
		"sandbox":                strings.TrimSpace(sandbox),
		"persistExtendedHistory": true,
		"experimentalRawEvents":  rawEvents,
	}

	if strings.TrimSpace(model) != "" {
		params["model"] = strings.TrimSpace(model)
	}

	return params
}

func buildCodexThreadResumeParams(threadID string) map[string]any {
	return map[string]any{
		"threadId":               strings.TrimSpace(threadID),
		"persistExtendedHistory": true,
	}
}
