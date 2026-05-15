package qqofficial

import (
	"os"
	"path/filepath"
	"regexp"
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

var mediaTagPattern = regexp.MustCompile(`(?im)^[\s` + "`" + `"'“”‘’]*MEDIA:\s*(?P<path>(?:file://|~/|/)\S+(?:[^\S\n]+\S+)*?|https?://\S+)[\s` + "`" + `"'“”‘’,.;:)\]}]*$`)

func resolveOutboundMediaResponse(response string) (string, []outboundMedia, error) {
	text := strings.TrimSpace(response)
	if text == "" {
		return "", nil, nil
	}

	matches := mediaTagPattern.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return text, nil, nil
	}

	var media []outboundMedia
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
		media = append(media, outboundMedia{
			Kind:   kind,
			Source: rawPath,
		})
		ranges = append(ranges, [2]int{match[0], match[1]})
	}

	if len(media) == 0 {
		return text, nil, nil
	}
	return strings.TrimSpace(removeRanges(text, ranges)), dedupeOutboundMedia(media), nil
}

func inferMediaKind(source string) (outboundMediaKind, bool) {
	ext := strings.ToLower(mediaSourceExt(source))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".webp", ".gif":
		return outboundMediaPhoto, true
	case ".mp3", ".wav", ".opus", ".ogg", ".aac", ".flac", ".m4a", ".pdf", ".txt", ".md", ".json", ".csv", ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx", ".zip", ".rar", ".7z":
		return outboundMediaDocument, true
	default:
		return "", false
	}
}

func mediaSourceExt(source string) string {
	source = normalizeLocalMediaPath(source)
	if source == "" {
		return ""
	}
	return strings.ToLower(filepath.Ext(source))
}

func normalizeLocalMediaPath(source string) string {
	source = strings.TrimSpace(source)
	if strings.HasPrefix(strings.ToLower(source), "file://") {
		source = strings.TrimPrefix(source, "file://")
	}
	if strings.HasPrefix(source, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			source = filepath.Join(home, strings.TrimPrefix(source, "~/"))
		}
	}
	return source
}

func trimWrappedPath(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "\"'“”‘’`")
	return strings.TrimSpace(value)
}

func removeRanges(text string, ranges [][2]int) string {
	if len(ranges) == 0 {
		return text
	}
	var b strings.Builder
	last := 0
	for _, r := range ranges {
		if r[0] > last {
			b.WriteString(text[last:r[0]])
		}
		last = r[1]
	}
	if last < len(text) {
		b.WriteString(text[last:])
	}
	return strings.TrimSpace(b.String())
}

func dedupeOutboundMedia(media []outboundMedia) []outboundMedia {
	if len(media) <= 1 {
		return media
	}
	seen := make(map[string]struct{}, len(media))
	out := make([]outboundMedia, 0, len(media))
	for _, item := range media {
		key := string(item.Kind) + "|" + item.Source
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func isRemoteMedia(source string) bool {
	lower := strings.ToLower(strings.TrimSpace(source))
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}
