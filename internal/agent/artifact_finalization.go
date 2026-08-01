package agent

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

var (
	fileWritePathPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?m)^Written \d+ bytes to (.+?) \(sha256 [a-fA-F0-9]+\)$`),
		regexp.MustCompile(`(?m)^Skipped write to (.+?); content already matches sha256 [a-fA-F0-9]+$`),
	}
	mediaPathPattern = regexp.MustCompile(`(?im)^[\t ` + "`" + `"'` + `]*MEDIA:\s*((?:sandbox:(?:/|[A-Za-z]:[\\/])|file://|~/|/|[A-Za-z]:[\\/])\S+(?:[^\S\n]+\S+)*?)[\t ` + "`" + `"',.;:)\]}]*$`)
)

type artifactFinalizationGuard struct {
	required bool
	paths    map[string]struct{}
}

func newArtifactFinalizationGuard(userInput string) *artifactFinalizationGuard {
	if !hasArtifactIntent(userInput) {
		return nil
	}
	return &artifactFinalizationGuard{
		required: true,
		paths:    make(map[string]struct{}),
	}
}

func hasArtifactIntent(text string) bool {
	text = strings.ToLower(strings.TrimSpace(stripArtifactDeliveryGuidance(text)))
	if text == "" {
		return false
	}
	if intentTextContainsAny(text, "保存到记忆", "保存记忆", "记住", "remember this") {
		return false
	}
	if intentTextContainsAny(text, "不保存", "不要保存", "不用保存", "无需保存", "不需要保存", "只贴内容", "直接贴出来") {
		return false
	}
	return intentTextContainsAny(text,
		"保存为", "保存成", "保存到", "保存文件", "保存md", "保存 md", "保存markdown", "保存 markdown",
		"创建文件", "写入文件", "生成文件", "生成文档", "导出", "发给我", "发送附件",
		"media:/", "tg://document", "tg://photo",
	)
}

func stripArtifactDeliveryGuidance(text string) string {
	for _, marker := range []string{"[Telegram delivery rule]", "[QQ delivery rule]", "[Feishu delivery rule]"} {
		if idx := strings.Index(text, marker); idx >= 0 {
			return strings.TrimSpace(text[:idx])
		}
	}
	return text
}

func (g *artifactFinalizationGuard) recordToolResult(name, arguments, result string) {
	if g == nil || !g.required {
		return
	}
	name = strings.TrimSpace(name)
	if name != "file_write" {
		return
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(result)), "error:") {
		return
	}
	for _, path := range fileWritePaths(arguments, result) {
		if pathExists(path) {
			g.paths[path] = struct{}{}
		}
	}
}

func (g *artifactFinalizationGuard) blockMessage(finalResponse string) (string, bool) {
	if g == nil || !g.required {
		return "", false
	}
	missing := missingMediaPaths(finalResponse)
	if len(missing) > 0 {
		return fmt.Sprintf("Artifact finalization guard blocked the final answer: it references missing media file(s): %s. Create the file with file_write or remove the MEDIA line and explain the failure.", strings.Join(missing, ", ")), true
	}
	if len(g.paths) == 0 {
		return "Artifact finalization guard blocked the final answer: the user requested a saved file or artifact, but no successful file_write result was observed. Call file_write to create the requested file, then verify the path exists before finalizing. Do not claim the file is saved until the write succeeds.", true
	}
	return "", false
}

func fileWritePaths(arguments, result string) []string {
	var paths []string
	for _, re := range fileWritePathPatterns {
		for _, match := range re.FindAllStringSubmatch(result, -1) {
			if len(match) > 1 {
				paths = append(paths, cleanArtifactPath(match[1]))
			}
		}
	}
	args := parseToolCallArgs(arguments)
	if raw := strings.TrimSpace(guardStringArg(args, "path")); raw != "" {
		paths = append(paths, cleanArtifactPath(raw))
	}
	return uniqueNonEmptyStrings(paths)
}

func missingMediaPaths(text string) []string {
	var missing []string
	for _, match := range mediaPathPattern.FindAllStringSubmatch(text, -1) {
		if len(match) < 2 {
			continue
		}
		path := cleanArtifactPath(match[1])
		if !pathExists(path) {
			missing = append(missing, path)
		}
	}
	return uniqueNonEmptyStrings(missing)
}

func cleanArtifactPath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.Trim(path, "`\"',.;:)]}")
	if strings.HasPrefix(strings.ToLower(path), "sandbox:") {
		path = strings.TrimPrefix(path, "sandbox:")
	}
	if strings.HasPrefix(strings.ToLower(path), "file://") {
		path = strings.TrimPrefix(path, "file://")
		if len(path) >= 3 && path[0] == '/' && isASCIIAlpha(path[1]) && path[2] == ':' {
			path = path[1:]
		}
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			path = home + strings.TrimPrefix(path, "~")
		}
	}
	return path
}

func isASCIIAlpha(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}

func pathExists(path string) bool {
	path = cleanArtifactPath(path)
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func uniqueNonEmptyStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
