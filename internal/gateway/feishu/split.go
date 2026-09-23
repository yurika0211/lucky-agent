package feishu

import (
	"strings"
)

// feishuTextChunkLimit keeps messages readable while staying comfortably
// below the platform's text payload ceiling.
const feishuTextChunkLimit = 4000

type textBlock struct {
	text  string
	fence string
	info  string
}

func splitFeishuText(message string) []string {
	if message == "" {
		return nil
	}
	if runeLen(message) <= feishuTextChunkLimit {
		return []string{message}
	}

	blocks := splitMarkdownBlocks(message)
	chunks := make([]string, 0, runeLen(message)/feishuTextChunkLimit+1)
	var current strings.Builder
	currentRunes := 0
	flush := func() {
		if current.Len() == 0 {
			return
		}
		chunks = append(chunks, current.String())
		current.Reset()
		currentRunes = 0
	}
	for _, block := range blocks {
		if runeLen(block.text) > feishuTextChunkLimit {
			flush()
			if block.fence != "" {
				chunks = append(chunks, splitLargeCodeBlock(block)...)
			} else {
				chunks = append(chunks, splitRawText(block.text, feishuTextChunkLimit)...)
			}
			continue
		}
		blockRunes := runeLen(block.text)
		if currentRunes > 0 && currentRunes+blockRunes > feishuTextChunkLimit {
			flush()
		}
		current.WriteString(block.text)
		currentRunes += blockRunes
	}
	flush()
	return chunks
}

func splitMarkdownBlocks(message string) []textBlock {
	lines := strings.SplitAfter(message, "\n")
	blocks := make([]textBlock, 0, len(lines))
	for index := 0; index < len(lines); {
		line := lines[index]
		if fence, info, ok := openingFence(line); ok {
			var code strings.Builder
			code.WriteString(line)
			index++
			for index < len(lines) {
				code.WriteString(lines[index])
				closed := closingFence(lines[index], fence)
				index++
				if closed {
					break
				}
			}
			blocks = append(blocks, textBlock{text: code.String(), fence: fence, info: info})
			continue
		}

		var paragraph strings.Builder
		paragraph.WriteString(line)
		index++
		for index < len(lines) {
			if _, _, ok := openingFence(lines[index]); ok || strings.TrimSpace(lines[index]) == "" {
				break
			}
			paragraph.WriteString(lines[index])
			index++
		}
		blocks = append(blocks, textBlock{text: paragraph.String()})
		if index < len(lines) && strings.TrimSpace(lines[index]) == "" {
			var blank strings.Builder
			for index < len(lines) && strings.TrimSpace(lines[index]) == "" {
				blank.WriteString(lines[index])
				index++
			}
			blocks = append(blocks, textBlock{text: blank.String()})
		}
	}
	return blocks
}

func openingFence(line string) (fence, info string, ok bool) {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 || len(trimmed) < 3 {
		return "", "", false
	}
	marker := trimmed[0]
	if marker != '`' && marker != '~' {
		return "", "", false
	}
	count := 0
	for count < len(trimmed) && trimmed[count] == marker {
		count++
	}
	if count < 3 {
		return "", "", false
	}
	fence = strings.Repeat(string(marker), count)
	info = strings.TrimSpace(strings.TrimSuffix(trimmed[count:], "\n"))
	return fence, info, true
}

func closingFence(line, fence string) bool {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, fence) {
		return false
	}
	return strings.TrimSpace(strings.TrimPrefix(trimmed, fence)) == ""
}

func splitLargeCodeBlock(block textBlock) []string {
	lines := strings.SplitAfter(block.text, "\n")
	if len(lines) == 0 {
		return splitRawText(block.text, feishuTextChunkLimit)
	}
	opening := lines[0]
	closing := ""
	bodyEnd := len(lines)
	trailingEmptyLine := bodyEnd > 0 && lines[bodyEnd-1] == ""
	if trailingEmptyLine {
		bodyEnd--
	}
	if bodyEnd > 1 && closingFence(lines[bodyEnd-1], block.fence) {
		closing = lines[bodyEnd-1]
		bodyEnd--
		if trailingEmptyLine {
			closing += lines[len(lines)-1]
		}
	}
	body := strings.Join(lines[1:bodyEnd], "")
	if body == "" {
		return []string{block.text}
	}
	closeLine := block.fence
	if strings.HasSuffix(opening, "\n") {
		closeLine = "\n" + block.fence
	}
	maxBodyRunes := feishuTextChunkLimit - runeLen(opening) - runeLen(closeLine)
	if maxBodyRunes < 1 {
		return splitRawText(block.text, feishuTextChunkLimit)
	}
	bodyChunks := splitRawText(body, maxBodyRunes)
	chunks := make([]string, 0, len(bodyChunks))
	for _, part := range bodyChunks {
		chunks = append(chunks, opening+part+closeLine)
	}
	if closing != "" && len(chunks) > 0 {
		chunks[len(chunks)-1] += strings.TrimPrefix(closing, block.fence)
	}
	return chunks
}

func splitRawText(message string, limit int) []string {
	if message == "" {
		return nil
	}
	if limit <= 0 {
		limit = feishuTextChunkLimit
	}
	remaining := message
	chunks := make([]string, 0, runeLen(message)/limit+1)
	for runeLen(remaining) > limit {
		prefix := string([]rune(remaining)[:limit])
		if newline := strings.LastIndex(prefix, "\n"); newline >= 0 {
			prefix = prefix[:newline+1]
		}
		if prefix == "" {
			prefix = string([]rune(remaining)[:limit])
		}
		chunks = append(chunks, prefix)
		remaining = strings.TrimPrefix(remaining, prefix)
	}
	if remaining != "" {
		chunks = append(chunks, remaining)
	}
	return chunks
}

func runeLen(value string) int { return len([]rune(value)) }
