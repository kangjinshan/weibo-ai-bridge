package router

import (
	"fmt"
	"os"
	"strings"
)

func setAgentEnabledInConfig(path, agentType string, enabled bool) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}

	updated, changed, err := updateAgentEnabledSection(string(content), agentType, enabled)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}

	if err := os.WriteFile(path, []byte(updated), 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

func updateAgentEnabledSection(content, agentType string, enabled bool) (string, bool, error) {
	section := fmt.Sprintf("[agent.%s]", agentType)
	lines := strings.Split(content, "\n")
	sectionStart := -1
	sectionEnd := len(lines)

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == section {
			sectionStart = i
			continue
		}
		if sectionStart >= 0 && strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			sectionEnd = i
			break
		}
	}

	if sectionStart == -1 {
		if !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		content += fmt.Sprintf("\n%s\nenabled = %t\n", section, enabled)
		return content, true, nil
	}

	targetLine := fmt.Sprintf("enabled = %t", enabled)
	for i := sectionStart + 1; i < sectionEnd; i++ {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "enabled") {
			if trimmed == targetLine {
				return content, false, nil
			}
			lines[i] = targetLine
			return strings.Join(lines, "\n"), true, nil
		}
	}

	insertAt := sectionEnd
	lines = append(lines[:insertAt], append([]string{targetLine}, lines[insertAt:]...)...)
	return strings.Join(lines, "\n"), true, nil
}
