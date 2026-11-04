package artifact

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/yurika0211/luckyagent-tools/execpolicy"
)

var (
	fileWritePathPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?m)^Written \d+ bytes to (.+?) \(sha256 [a-fA-F0-9]+\)$`),
		regexp.MustCompile(`(?m)^Skipped write to (.+?); content already matches sha256 [a-fA-F0-9]+$`),
	}
	mediaPathPattern    = regexp.MustCompile(`(?im)^[\t ` + "`" + `"'` + `]*MEDIA:\s*((?:sandbox:(?:/|[A-Za-z]:[\\/])|file://|~/|/|[A-Za-z]:[\\/])\S+(?:[^\S\n]+\S+)*?)[\t ` + "`" + `"',.;:)\]}]*$`)
	artifactPathPattern = regexp.MustCompile("(?i)(?:sandbox:)?(?:~[/\\\\]\\.luckyagent[/\\\\](?:workspace|uploads)[/\\\\][^\\s`\"'<>]+|/(?:[^\\s`\"'<>/]+/)+\\.luckyagent/(?:workspace|uploads)/[^\\s`\"'<>]+)")
)

type Guard struct {
	required bool
	Paths    map[string]struct{}
}

func NewGuard(userInput string) *Guard {
	if !HasIntent(userInput) {
		return nil
	}
	return &Guard{
		required: true,
		Paths:    make(map[string]struct{}),
	}
}

func HasIntent(text string) bool {
	text = strings.ToLower(strings.TrimSpace(stripArtifactDeliveryGuidance(text)))
	if text == "" {
		return false
	}
	if execpolicy.ContainsAny(text, "保存到记忆", "保存记忆", "记住", "remember this") {
		return false
	}
	if execpolicy.ContainsAny(text, "不保存", "不要保存", "不用保存", "无需保存", "不需要保存", "只贴内容", "直接贴出来") {
		return false
	}
	return execpolicy.ContainsAny(text,
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

func (g *Guard) RecordToolResult(name, arguments, result string) {
	if g == nil || !g.required {
		return
	}
	name = strings.TrimSpace(name)
	if name != "file_write" && name != "file_send" {
		return
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(result)), "error:") {
		return
	}
	var paths []string
	if name == "file_send" {
		paths = fileSendPaths(result)
	} else {
		paths = fileWritePaths(arguments, result)
	}
	for _, path := range paths {
		if pathExists(path) {
			g.Paths[path] = struct{}{}
		}
	}
}

func fileSendPaths(result string) []string {
	var payload struct {
		FilePath string `json:"file_path"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(result)), &payload); err != nil {
		return nil
	}
	path := cleanArtifactPath(payload.FilePath)
	if path == "" {
		return nil
	}
	return []string{path}
}

func (g *Guard) BlockMessage(finalResponse string) (string, bool) {
	if g == nil || !g.required {
		return "", false
	}
	missing := MissingMediaPaths(finalResponse)
	if len(missing) > 0 {
		return fmt.Sprintf("Artifact finalization guard blocked the final answer: it references missing media file(s): %s. Create the file with file_write or remove the MEDIA line and explain the failure.", strings.Join(missing, ", ")), true
	}
	if len(g.Paths) == 0 {
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
	args := execpolicy.ParseArgs(arguments)
	if raw := strings.TrimSpace(execpolicy.StringArg(args, "path")); raw != "" {
		paths = append(paths, cleanArtifactPath(raw))
	}
	return uniqueNonEmptyStrings(paths)
}

func MissingMediaPaths(text string) []string {
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

func MediaReferences(text string) []string {
	var references []string
	for _, line := range strings.Split(text, "\n") {
		if reference, ok := mediaReferenceFromLine(line); ok {
			references = append(references, cleanArtifactPath(reference))
		}
	}
	return uniqueNonEmptyStrings(references)
}

func ArtifactReferences(text string) []string {
	var references []string
	for _, reference := range artifactPathPattern.FindAllString(text, -1) {
		references = append(references, cleanArtifactPath(reference))
	}
	return uniqueNonEmptyStrings(references)
}

func StripMediaReferences(text string, references []string) string {
	if len(references) == 0 {
		return text
	}
	resolved := make(map[string]struct{}, len(references))
	for _, reference := range references {
		resolved[cleanArtifactPath(reference)] = struct{}{}
	}
	lines := strings.Split(text, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if reference, ok := mediaReferenceFromLine(line); ok {
			if _, resolvedOK := resolved[cleanArtifactPath(reference)]; resolvedOK {
				continue
			}
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

func mediaReferenceFromLine(line string) (string, bool) {
	line = strings.TrimSpace(line)
	line = strings.Trim(line, "`\"'")
	if len(line) < len("MEDIA:") || !strings.EqualFold(line[:len("MEDIA:")], "MEDIA:") {
		return "", false
	}
	reference := strings.TrimSpace(line[len("MEDIA:"):])
	reference = strings.Trim(reference, "`\"',.;:)]}")
	if reference == "" {
		return "", false
	}
	lower := strings.ToLower(reference)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(lower, "sandbox:") || strings.HasPrefix(lower, "file://") ||
		strings.HasPrefix(reference, "~/") || strings.HasPrefix(reference, "/") ||
		(len(reference) >= 3 && isASCIIAlpha(reference[0]) && reference[1] == ':' && (reference[2] == '\\' || reference[2] == '/')) {
		return reference, true
	}
	return "", false
}

// sanitizeHistoricalMediaReferences removes standalone MEDIA directives that
// point at files which no longer exist. Session history can outlive temporary
// screenshots; retaining those old paths makes a later, unrelated turn
// repeat the stale directive and causes a Telegram upload failure.
func SanitizeHistory(text string) string {
	if strings.TrimSpace(text) == "" {
		return text
	}
	return mediaPathPattern.ReplaceAllStringFunc(text, func(match string) string {
		submatches := mediaPathPattern.FindStringSubmatch(match)
		if len(submatches) < 2 || pathExists(submatches[1]) {
			return match
		}
		return ""
	})
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
