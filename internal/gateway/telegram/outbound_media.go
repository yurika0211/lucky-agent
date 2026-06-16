package telegram

import (
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
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
	tgMediaDirectivePattern = regexp.MustCompile(`(?i)^tg://(photo|document)\s+(\S+)(?:\s+(.*))?$`)
	mediaTagPattern         = regexp.MustCompile(`(?im)^[\s` + "`" + `"'“”‘’]*MEDIA:\s*(?P<path>(?:sandbox:/|file://|~/|/)\S+(?:[^\S\n]+\S+)*?|https?://\S+)[\s` + "`" + `"'“”‘’,.;:)\]}]*$`)
	markdownImagePattern    = regexp.MustCompile(`!\[([^\]]*)\]\(([^)\s]+)\)`)
	fencedCodePattern       = regexp.MustCompile("(?s)```.*?```")
	inlineCodePattern       = regexp.MustCompile("`[^`\n]+`")
	localFilePattern        = regexp.MustCompile(`(?i)(?:sandbox:/|file://|~/|/)\S+(?:[^\S\n]+\S+)*?\.(?:png|jpe?g|gif|webp|mp3|wav|opus|ogg|aac|flac|m4a|pdf|txt|md|json|csv|docx?|xlsx?|pptx?|zip|rar|7z|svg|xml|html?|js|ts|py|go|ya?ml)\b`)
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
		if m := tgMediaDirectivePattern.FindStringSubmatch(trimmed); m != nil {
			source := strings.TrimSpace(m[2])
			if isSensitiveOutboundMediaPath(source) {
				kept = append(kept, line)
				continue
			}
			media = append(media, outboundMedia{
				Kind:    outboundMediaKind(strings.ToLower(m[1])),
				Source:  source,
				Caption: strings.TrimSpace(m[3]),
			})
			continue
		}
		kept = append(kept, line)
	}

	text = strings.TrimSpace(strings.Join(kept, "\n"))
	text, media = extractExplicitMediaTags(text, media)
	text, media = extractMarkdownMedia(text, media)
	text = normalizeOutboundText(text)
	return text, dedupeOutboundMedia(media)
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
		existing = append(existing, outboundMedia{
			Kind:   kind,
			Source: rawPath,
		})
		ranges = append(ranges, [2]int{match[0], match[1]})
	}

	return removeRanges(text, ranges), existing
}

func extractMarkdownMedia(text string, existing []outboundMedia) (string, []outboundMedia) {
	if strings.Contains(text, "![") {
		text = markdownImagePattern.ReplaceAllStringFunc(text, func(match string) string {
			m := markdownImagePattern.FindStringSubmatch(match)
			if m == nil {
				return match
			}
			source := strings.TrimSpace(m[2])
			kind, ok := inferMediaKind(source)
			if !ok || kind != outboundMediaPhoto {
				return match
			}
			existing = append(existing, outboundMedia{
				Kind:    kind,
				Source:  source,
				Caption: strings.TrimSpace(m[1]),
			})
			return ""
		})
	}

	return strings.TrimSpace(text), existing
}

func extractLocalFiles(content string) (string, []outboundMedia, error) {
	text := strings.TrimSpace(content)
	if text == "" {
		return "", nil, nil
	}

	masked := maskCodeRegions(text)
	indexes := localFilePattern.FindAllStringIndex(masked, -1)
	if len(indexes) == 0 {
		return normalizeOutboundText(text), nil, nil
	}

	var media []outboundMedia
	var ranges [][2]int
	for _, idx := range indexes {
		if len(idx) != 2 {
			continue
		}
		rawPath := strings.TrimSpace(text[idx[0]:idx[1]])
		rawPath = trimWrappedPath(rawPath)
		pathForFS := normalizeLocalMediaPath(rawPath)
		if pathForFS == "" {
			continue
		}
		info, err := os.Stat(pathForFS)
		if err != nil || info.IsDir() {
			continue
		}
		kind, ok := inferMediaKind(rawPath)
		if !ok {
			continue
		}
		media = append(media, outboundMedia{
			Kind:   kind,
			Source: rawPath,
		})
		ranges = append(ranges, [2]int{idx[0], idx[1]})
	}

	if len(media) == 0 {
		return normalizeOutboundText(text), nil, nil
	}

	return removeRanges(text, ranges), dedupeOutboundMedia(media), nil
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
	if strings.HasPrefix(strings.ToLower(source), "file://") {
		if u, err := url.Parse(source); err == nil {
			return path.Ext(u.Path)
		}
	}
	if u, err := url.Parse(source); err == nil && u.Scheme != "" {
		return path.Ext(u.Path)
	}
	return filepath.Ext(source)
}

func normalizeLocalMediaPath(source string) string {
	source = trimWrappedPath(source)
	if source == "" {
		return ""
	}
	lower := strings.ToLower(source)
	if strings.HasPrefix(lower, "sandbox:/") {
		return strings.TrimPrefix(source, "sandbox:")
	}
	if strings.HasPrefix(lower, "file://") {
		u, err := url.Parse(source)
		if err == nil && strings.TrimSpace(u.Path) != "" {
			return u.Path
		}
	}
	if strings.HasPrefix(source, "~/") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return filepath.Join(home, strings.TrimPrefix(source, "~/"))
		}
	}
	return source
}

func trimWrappedPath(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "`\"'“”‘’")
	s = strings.TrimRight(s, ",.;:)}]")
	return strings.TrimSpace(s)
}

func maskCodeRegions(s string) string {
	runes := []rune(s)
	mask := func(start, end int) {
		for i := start; i < end && i < len(runes); i++ {
			if runes[i] != '\n' {
				runes[i] = ' '
			}
		}
	}

	for _, idx := range fencedCodePattern.FindAllStringIndex(s, -1) {
		mask(idx[0], idx[1])
	}
	for _, idx := range inlineCodePattern.FindAllStringIndex(string(runes), -1) {
		mask(idx[0], idx[1])
	}
	return string(runes)
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

	var merged [][2]int
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
	cursor := 0
	for _, r := range merged {
		if cursor < r[0] {
			b.WriteString(text[cursor:r[0]])
		}
		cursor = r[1]
	}
	if cursor < len(text) {
		b.WriteString(text[cursor:])
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

func summarizeOutboundMedia(media []outboundMedia) string {
	if len(media) == 0 {
		return "📎 已发送附件"
	}
	photos := 0
	docs := 0
	for _, item := range media {
		switch item.Kind {
		case outboundMediaPhoto:
			photos++
		case outboundMediaDocument:
			docs++
		}
	}

	parts := make([]string, 0, 2)
	if photos > 0 {
		if photos == 1 {
			parts = append(parts, "1 张图片")
		} else {
			parts = append(parts, strconv.Itoa(photos)+" 张图片")
		}
	}
	if docs > 0 {
		if docs == 1 {
			parts = append(parts, "1 个文件")
		} else {
			parts = append(parts, strconv.Itoa(docs)+" 个文件")
		}
	}
	if len(parts) == 0 {
		return "📎 已发送附件"
	}
	return "📎 已发送 " + strings.Join(parts, "、")
}

func telegramMediaDeliveryGuidance(text string) string {
	text = strings.TrimSpace(text)
	const guidance = "[Telegram delivery rule]\nIf you want Telegram to send a file, image, or other artifact, save it to a real local file first and include a standalone line exactly like MEDIA:/absolute/path/to/file.ext . Do not use markdown links for local files. Do not paste full file contents unless the user explicitly asks for inline content."
	if text == "" {
		return guidance
	}
	return text + "\n\n" + guidance
}
