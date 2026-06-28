package lhcmd

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/yurika0211/luckyagent/internal/gateway"
)

var localPathPattern = regexp.MustCompile(`(?m)(?:^|\s)([A-Za-z]:\\[^\s]+|/[^\s]+|\./[^\s]+|\.\\[^\s]+|~/[^\s]+)`)

func parseAttachmentsFromInput(text string) (string, []gateway.Attachment) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", nil
	}

	matches := localPathPattern.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return text, nil
	}

	var attachments []gateway.Attachment
	cleaned := text
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		raw := strings.TrimSpace(match[1])
		path := expandAttachmentPath(raw)
		if path == "" {
			continue
		}
		if _, err := os.Stat(path); err != nil {
			continue
		}
		att := attachmentFromLocalPath(path)
		attachments = append(attachments, att)
		cleaned = strings.ReplaceAll(cleaned, raw, "")
	}

	cleaned = strings.Join(strings.Fields(cleaned), " ")
	return strings.TrimSpace(cleaned), attachments
}

func expandAttachmentPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if strings.HasPrefix(path, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, strings.TrimPrefix(path, "~"))
		}
	}
	return filepath.Clean(path)
}

func attachmentFromLocalPath(path string) gateway.Attachment {
	name := filepath.Base(path)
	ext := strings.ToLower(filepath.Ext(name))
	att := gateway.Attachment{
		FilePath: path,
		FileName: name,
	}
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".tif", ".tiff":
		att.Type = gateway.AttachmentImage
	case ".pdf", ".txt", ".md", ".json", ".csv", ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx":
		att.Type = gateway.AttachmentDocument
	default:
		att.Type = gateway.AttachmentDocument
	}
	return att
}
