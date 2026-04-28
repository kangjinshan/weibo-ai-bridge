package router

import (
	"strings"
)

// splitMessage 分割消息为多个块
func (r *Router) splitMessage(content string, maxSize int) []string {
	runes := []rune(content)
	if len(runes) <= maxSize {
		return []string{content}
	}

	var chunks []string
	var buffer strings.Builder

	lines := strings.Split(content, "\n")

	for _, line := range lines {
		lineRunes := []rune(line)
		if len(lineRunes) > maxSize {
			if buffer.Len() > 0 {
				chunks = append(chunks, buffer.String())
				buffer.Reset()
			}

			for len(lineRunes) > maxSize {
				chunks = append(chunks, string(lineRunes[:maxSize]))
				lineRunes = lineRunes[maxSize:]
			}
			if len(lineRunes) > 0 {
				buffer.WriteString(string(lineRunes))
				buffer.WriteString("\n")
			}
			continue
		}

		if utf8RuneCount(buffer.String())+len(lineRunes)+1 > maxSize {
			chunks = append(chunks, buffer.String())
			buffer.Reset()
		}

		buffer.WriteString(line)
		buffer.WriteString("\n")
	}

	if buffer.Len() > 0 {
		chunks = append(chunks, strings.TrimSuffix(buffer.String(), "\n"))
	}

	return chunks
}

func utf8RuneCount(value string) int {
	return len([]rune(value))
}
