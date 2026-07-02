package agent

import (
	"sort"
	"strings"

	"github.com/yurika0211/luckyagent/internal/contextx"
	"github.com/yurika0211/luckyagent/internal/provider"
	"github.com/yurika0211/luckyagent/internal/session"
)

const (
	maxCompactToolAttachments = 5
	maxCompactFileAttachments = 10
)

func buildPostCompactAttachments(sess *session.Session, messages []provider.Message, est *contextx.TokenEstimator) []session.CompactAttachment {
	var attachments []session.CompactAttachment
	attachments = append(attachments, compactShellAttachment(sess, est)...)
	attachments = append(attachments, compactFileAttachments(messages, est)...)
	attachments = append(attachments, compactToolAttachments(messages, est)...)
	sort.SliceStable(attachments, func(i, j int) bool {
		if attachments[i].Priority == attachments[j].Priority {
			return attachments[i].Kind < attachments[j].Kind
		}
		return attachments[i].Priority > attachments[j].Priority
	})
	return attachments
}

func compactShellAttachment(sess *session.Session, est *contextx.TokenEstimator) []session.CompactAttachment {
	if sess == nil {
		return nil
	}
	var lines []string
	if cwd := strings.TrimSpace(sess.GetCwd()); cwd != "" {
		lines = append(lines, "cwd: "+cwd)
	}
	env := sess.GetEnv()
	if len(env) > 0 {
		keys := make([]string, 0, len(env))
		for key := range env {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		lines = append(lines, "env keys: "+strings.Join(keys, ", "))
	}
	if len(lines) == 0 {
		return nil
	}
	content := strings.Join(lines, "\n")
	return []session.CompactAttachment{{
		Kind:     "shell_state",
		Source:   "session",
		Content:  content,
		Priority: 90,
		Tokens:   compactAttachmentTokens(est, content),
	}}
}

func compactFileAttachments(messages []provider.Message, est *contextx.TokenEstimator) []session.CompactAttachment {
	seen := make(map[string]struct{})
	var files []string
	for i := len(messages) - 1; i >= 0 && len(files) < maxCompactFileAttachments; i-- {
		for _, match := range compactFileRefPattern.FindAllString(messages[i].Content, -1) {
			file := strings.Trim(match, " \t\n\r\"'(:")
			if file == "" {
				continue
			}
			if _, ok := seen[file]; ok {
				continue
			}
			seen[file] = struct{}{}
			files = append(files, file)
			if len(files) >= maxCompactFileAttachments {
				break
			}
		}
	}
	if len(files) == 0 {
		return nil
	}
	sort.Strings(files)
	content := strings.Join(files, "\n")
	return []session.CompactAttachment{{
		Kind:     "file_state",
		Source:   "recent_history",
		Content:  content,
		Priority: 80,
		Tokens:   compactAttachmentTokens(est, content),
	}}
}

func compactToolAttachments(messages []provider.Message, est *contextx.TokenEstimator) []session.CompactAttachment {
	out := make([]session.CompactAttachment, 0, maxCompactToolAttachments)
	for i := len(messages) - 1; i >= 0 && len(out) < maxCompactToolAttachments; i-- {
		msg := messages[i]
		if msg.Role != "tool" {
			continue
		}
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}
		summary := summarizeToolResult(msg.Name, content)
		if strings.TrimSpace(summary) == "" {
			summary = truncate(content, 320)
		}
		out = append(out, session.CompactAttachment{
			Kind:     "tool_result",
			Source:   strings.TrimSpace(msg.Name),
			Content:  summary,
			Priority: 70,
			Tokens:   compactAttachmentTokens(est, summary),
		})
	}
	reverseCompactAttachments(out)
	return out
}

func reverseCompactAttachments(items []session.CompactAttachment) {
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
}

func compactAttachmentTokens(est *contextx.TokenEstimator, content string) int {
	if est == nil {
		return 0
	}
	return est.Estimate(content)
}
