package agent

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/yurika0211/luckyagent/internal/provider"
)

const (
	minCompactSummaryRunes = 120
	maxCompactSummaryRunes = 12000
)

type CompactSummaryValidation struct {
	Valid       bool
	Missing     []string
	Reason      string
	CharCount   int
	HasFileRef  bool
	HasNextStep bool
}

func validateCompactSummary(summary string, messages []provider.Message) CompactSummaryValidation {
	summary = strings.TrimSpace(summary)
	validation := CompactSummaryValidation{
		CharCount:  len([]rune(summary)),
		HasFileRef: compactTextHasFileRef(summary),
	}
	lower := strings.ToLower(summary)
	validation.HasNextStep = compactContainsAny(lower, "pending work", "未完成事项", "next step", "待办", "后续")

	if summary == "" {
		validation.Missing = append(validation.Missing, "summary")
	}
	if validation.CharCount < minCompactSummaryRunes {
		validation.Missing = append(validation.Missing, fmt.Sprintf("summary length >= %d runes", minCompactSummaryRunes))
	}
	if validation.CharCount > maxCompactSummaryRunes {
		validation.Missing = append(validation.Missing, fmt.Sprintf("summary length <= %d runes", maxCompactSummaryRunes))
	}
	if !compactContainsAny(lower, "current user goal", "当前用户目标", "user goal", "目标") {
		validation.Missing = append(validation.Missing, "current user goal section")
	}
	if !validation.HasNextStep {
		validation.Missing = append(validation.Missing, "pending work section")
	}
	if compactMessagesNeedEvidence(messages) && !compactSummaryHasEvidence(summary) {
		validation.Missing = append(validation.Missing, "file/tool/error/test evidence")
	}

	if len(validation.Missing) > 0 {
		validation.Reason = "missing " + strings.Join(validation.Missing, ", ")
		return validation
	}
	validation.Valid = true
	return validation
}

func compactMessagesNeedEvidence(messages []provider.Message) bool {
	for _, msg := range messages {
		if msg.Role == "tool" || msg.Name != "" || len(msg.ToolCalls) > 0 {
			return true
		}
		content := strings.ToLower(msg.Content)
		if compactContainsAny(content, "error", "failed", "panic", "go test", "npm test", "pytest") || compactTextHasFileRef(msg.Content) {
			return true
		}
	}
	return false
}

func compactSummaryHasEvidence(summary string) bool {
	lower := strings.ToLower(summary)
	return compactTextHasFileRef(summary) || compactContainsAny(lower, "tool", "command", "go test", "npm test", "pytest", "error", "failed", "测试", "命令")
}

func compactContainsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, strings.ToLower(needle)) {
			return true
		}
	}
	return false
}

var compactFileRefPattern = regexp.MustCompile(`(?:^|[\s"'(:])(?:[A-Za-z0-9_.-]+/)+[A-Za-z0-9_.-]+\.[A-Za-z0-9]+`)

func compactTextHasFileRef(text string) bool {
	return compactFileRefPattern.MatchString(text)
}
