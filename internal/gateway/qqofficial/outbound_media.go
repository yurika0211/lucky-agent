package qqofficial

import (
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

type outboundMediaKind string

const (
	outboundMediaPhoto    outboundMediaKind = "photo"
	outboundMediaDocument outboundMediaKind = "document"
)

type outboundMedia struct {
	Kind    outboundMediaKind
	Source  string
	Caption string
}

var (
	mediaTagPattern        = regexp.MustCompile(`(?im)^[\t ` + "`" + `"'` + `]*MEDIA:\s*(?P<path>(?:(?:sandbox:(?:/|[A-Za-z]:[\\/])|file://|~/|/|[A-Za-z]:[\\/])\S+(?:[^\S\n]+\S+)*?|https?://\S+))[\t ` + "`" + `"',.;:)\]}]*$`)
	markdownImagePattern   = regexp.MustCompile(`!\[([^\]]*)\]\(([^)\s]+)\)`)
	generatedReferenceLine = regexp.MustCompile(`(?i)^\[\d+\]\s+(tool result\.|local file\.|local directory listing\.)`)
)

func resolveOutboundMediaResponse(response string) (string, []outboundMedia, error) {
	text, media := parseOutboundMediaResponse(response)
	return text, media, nil
}

func parseOutboundMediaResponse(response string) (string, []outboundMedia) {
	text := strings.TrimSpace(response)
	if text == "" {
		return "", nil
	}

	var media []outboundMedia
	lines := strings.Split(text, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if source, ok := parseStandaloneMediaLine(trimmed); ok {
			if kind, kindOK := inferMediaKind(source); kindOK && outboundMediaSourceAvailable(source) {
				media = append(media, outboundMedia{Kind: kind, Source: source})
				continue
			}
		}
		kept = append(kept, line)
	}
	text = strings.TrimSpace(strings.Join(kept, "\n"))
	text, media = extractExplicitMediaTags(text, media)
	text, media = extractMarkdownMedia(text, media)
	text = normalizeOutboundText(text)
	if len(media) > 0 {
		text = stripGeneratedReferencesForMedia(text)
	}
	return text, dedupeOutboundMedia(media)
}

func parseStandaloneMediaLine(line string) (string, bool) {
	line = strings.TrimSpace(line)
	line = strings.Trim(line, "`\"'")
	if len(line) < len("MEDIA:") || !strings.EqualFold(line[:len("MEDIA:")], "MEDIA:") {
		return "", false
	}
	source := trimWrappedPath(strings.TrimSpace(line[len("MEDIA:"):]))
	if source == "" || !isExplicitMediaSource(source) {
		return "", false
	}
	return source, true
}

func isExplicitMediaSource(source string) bool {
	source = strings.TrimSpace(source)
	lower := strings.ToLower(source)
	return strings.HasPrefix(lower, "http://") ||
		strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(lower, "sandbox:") ||
		strings.HasPrefix(lower, "file://") ||
		strings.HasPrefix(source, "~/") ||
		strings.HasPrefix(source, "/") ||
		isWindowsDrivePath(source)
}

func outboundMediaSourceAvailable(source string) bool {
	if isRemoteMedia(source) {
		return true
	}
	info, err := os.Stat(normalizeLocalMediaPath(source))
	return err == nil && !info.IsDir()
}

func extractExplicitMediaTags(text string, existing []outboundMedia) (string, []outboundMedia) {
	matches := mediaTagPattern.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return text, existing
	}

	var ranges [][2]int
	for _, match := range matches {
		if len(match) < 4 {
			continue
		}
		rawPath := strings.TrimSpace(text[match[2]:match[3]])
		rawPath = trimWrappedPath(rawPath)
		kind, ok := inferMediaKind(rawPath)
		if !ok {
			continue
		}
		if !isRemoteMedia(rawPath) {
			pathForFS := normalizeLocalMediaPath(rawPath)
			info, err := os.Stat(pathForFS)
			if err != nil || info.IsDir() {
				continue
			}
		}
		existing = append(existing, outboundMedia{
			Kind:   kind,
			Source: rawPath,
		})
		ranges = append(ranges, [2]int{match[0], match[1]})
	}

	return removeRanges(text, ranges), existing
}

func stripGeneratedReferencesForMedia(text string) string {
	text = normalizeOutboundText(text)
	if text == "" {
		return ""
	}
	idx := strings.LastIndex(text, "\nReferences:")
	headerLen := len("\nReferences:")
	if idx < 0 && strings.HasPrefix(text, "References:") {
		idx = 0
		headerLen = len("References:")
	}
	if idx < 0 {
		return text
	}
	refs := strings.TrimSpace(text[idx+headerLen:])
	if refs == "" {
		return strings.TrimSpace(text[:idx])
	}
	for _, line := range strings.Split(refs, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !generatedReferenceLine.MatchString(line) {
			return text
		}
	}
	return strings.TrimSpace(text[:idx])
}

func extractMarkdownMedia(text string, existing []outboundMedia) (string, []outboundMedia) {
	if strings.Contains(text, "![") {
		text = markdownImagePattern.ReplaceAllStringFunc(text, func(match string) string {
			m := markdownImagePattern.FindStringSubmatch(match)
			if m == nil {
				return match
			}
			source := strings.TrimSpace(m[2])
			if kind, ok := inferMediaKind(source); ok {
				if kind != outboundMediaPhoto {
					return match
				}
				existing = append(existing, outboundMedia{
					Kind:    kind,
					Source:  source,
					Caption: strings.TrimSpace(m[1]),
				})
				return ""
			}
			return match
		})
	}

	return strings.TrimSpace(text), existing
}

func inferMediaKind(source string) (outboundMediaKind, bool) {
	if isSensitiveOutboundMediaPath(source) {
		return "", false
	}
	ext := strings.ToLower(mediaSourceExt(source))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".webp", ".gif":
		return outboundMediaPhoto, true
	case ".mp3", ".wav", ".opus", ".ogg", ".aac", ".flac", ".m4a", ".pdf", ".txt", ".md", ".json", ".csv", ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx", ".zip", ".rar", ".7z", ".svg", ".xml", ".html", ".htm", ".js", ".ts", ".py", ".go", ".yaml", ".yml":
		return outboundMediaDocument, true
	default:
		return "", false
	}
}

func isSensitiveOutboundMediaPath(source string) bool {
	source = strings.TrimSpace(normalizeLocalMediaPath(source))
	if source == "" {
		return false
	}
	base := strings.ToLower(filepath.Base(source))
	switch base {
	case "config.json", ".env", ".env.local", ".env.production", ".env.development", "credentials.json", "token.json", "tokens.json", "secrets.json":
		return true
	default:
		return strings.HasPrefix(base, ".env.")
	}
}

func mediaSourceExt(source string) string {
	source = normalizeLocalMediaPath(source)
	if source == "" {
		return ""
	}
	if isWindowsDrivePath(source) {
		return strings.ToLower(filepath.Ext(source))
	}
	if u, err := url.Parse(source); err == nil && u.Scheme != "" {
		return strings.ToLower(path.Ext(u.Path))
	}
	return strings.ToLower(filepath.Ext(source))
}

func isWindowsDrivePath(source string) bool {
	source = strings.TrimSpace(source)
	return len(source) >= 3 && isASCIIAlpha(source[0]) && source[1] == ':' && (source[2] == '\\' || source[2] == '/')
}

func normalizeLocalMediaPath(source string) string {
	source = trimWrappedPath(source)
	if source == "" {
		return ""
	}
	lower := strings.ToLower(source)
	if strings.HasPrefix(lower, "sandbox:") {
		return strings.TrimPrefix(source, "sandbox:")
	}
	if strings.HasPrefix(lower, "file://") {
		if u, err := url.Parse(source); err == nil && strings.TrimSpace(u.Path) != "" {
			return cleanFileURLPath(u.Path)
		}
	}
	if strings.HasPrefix(source, "~/") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			source = filepath.Join(home, strings.TrimPrefix(source, "~/"))
		}
	}
	return source
}

func cleanFileURLPath(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 3 && value[0] == '/' && isASCIIAlpha(value[1]) && value[2] == ':' {
		value = value[1:]
	}
	return filepath.FromSlash(value)
}

func isASCIIAlpha(value byte) bool {
	return (value >= 'A' && value <= 'Z') || (value >= 'a' && value <= 'z')
}

func trimWrappedPath(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "\"'“”‘’`")
	value = strings.TrimRight(value, ",.;:)}]")
	return strings.TrimSpace(value)
}

func removeRanges(text string, ranges [][2]int) string {
	if len(ranges) == 0 {
		return normalizeOutboundText(text)
	}
	slices.SortFunc(ranges, func(a, b [2]int) int {
		switch {
		case a[0] < b[0]:
			return -1
		case a[0] > b[0]:
			return 1
		default:
			return 0
		}
	})

	merged := make([][2]int, 0, len(ranges))
	for _, r := range ranges {
		if len(merged) == 0 || r[0] > merged[len(merged)-1][1] {
			merged = append(merged, r)
			continue
		}
		if r[1] > merged[len(merged)-1][1] {
			merged[len(merged)-1][1] = r[1]
		}
	}

	var b strings.Builder
	last := 0
	for _, r := range merged {
		if r[0] > last {
			b.WriteString(text[last:r[0]])
		}
		last = r[1]
	}
	if last < len(text) {
		b.WriteString(text[last:])
	}
	return normalizeOutboundText(b.String())
}

func dedupeOutboundMedia(media []outboundMedia) []outboundMedia {
	if len(media) <= 1 {
		return media
	}
	seen := make(map[string]struct{}, len(media))
	out := make([]outboundMedia, 0, len(media))
	for _, item := range media {
		key := string(item.Kind) + "\x00" + item.Source + "\x00" + item.Caption
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func normalizeOutboundText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	prevBlank := false
	for _, line := range lines {
		line = strings.TrimRight(line, " \t")
		blank := strings.TrimSpace(line) == ""
		if blank {
			if prevBlank {
				continue
			}
			prevBlank = true
			out = append(out, "")
			continue
		}
		prevBlank = false
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func isRemoteMedia(source string) bool {
	lower := strings.ToLower(strings.TrimSpace(source))
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

func qqMediaDeliveryGuidance(text string) string {
	text = strings.TrimSpace(text)
	if strings.Contains(text, "[QQ delivery rule]") {
		return text
	}
	const guidance = "[QQ delivery rule]\nIf the user should receive a file, image, or other artifact, save it to a real local file first and include a standalone line exactly like MEDIA:/absolute/path/to/file.ext or MEDIA:C:\\absolute\\path\\to\\file.ext on Windows. If a tool reports a sandbox path, that form is also accepted. Do not paste full file contents unless the user explicitly asks for inline content."
	if text == "" {
		return guidance
	}
	return fmt.Sprintf("%s\n\n%s", text, guidance)
}
