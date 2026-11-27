package agent

import (
	"context"
	"crypto/sha256"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	compactlib "github.com/yurika0211/luckyagent-tools/compact"
)

// Avoid introducing recognizable credentials that the original excerpt omitted.
var compactSensitiveEvidence = regexp.MustCompile(`(?i)` +
	`[a-z][a-z0-9+.-]*://[^\s/]+@|` +
	`[?&](?:[^\s=&]*?(?:token|secret|password|signature|credential)|api[_-]?key|sig)=|` +
	`\b(?:bearer|basic)\s+[a-z0-9+/_.=-]+|\b(?:authorization|password|passwd|secret|api[_-]?key|(?:access[_-]?)?token)["']?\s*[:=]|` +
	`--(?:password|token|secret|api-key)\s|-----BEGIN [A-Z ]*PRIVATE KEY-----`)

type compactEvidencePlan struct {
	required []string
	// Map calls run concurrently. Key by their rendered prompt, which is the
	// input shared with the engine, rather than by goroutine arrival order.
	segments map[[sha256.Size]byte]string
}

// A nil plan without an error selects local summarization before any model call.
func planCompactEvidence(ctx context.Context, messages []compactlib.Message, est compactlib.Estimator, maxChunk int, required []string) (*compactEvidencePlan, error) {
	plan := &compactEvidencePlan{required: required}
	if len(required) == 0 {
		return plan, nil
	}
	chunks, err := compactlib.SplitChunksChecked(messages, est, maxChunk)
	if err != nil {
		return nil, err
	}
	plan.segments = make(map[[sha256.Size]byte]string, len(chunks))
	found := make(map[string]bool, len(required))
	for _, chunk := range chunks {
		if ctx != nil && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		body := compactlib.Transcript(chunk, compactTextOptions())
		var missing []string
		for _, id := range required {
			if slices.ContainsFunc(chunk, func(m compactlib.Message) bool { return strings.Contains(m.Content, id) }) {
				found[id] = true
				if !strings.Contains(body, id) {
					missing = append(missing, id)
				}
			}
		}
		extra, fits := compactEvidenceAppendix(missing, utf8.RuneCountInString(body))
		if !fits {
			return nil, nil
		}
		key := sha256.Sum256([]byte(compactlib.SegmentPrompt(body, "LuckyAgent")))
		if previous, exists := plan.segments[key]; exists && previous != extra {
			// Identical excerpts can originate from different sources. The chat
			// hook cannot distinguish them; avoid mixing evidence across chunks.
			return nil, nil
		}
		plan.segments[key] = extra
	}
	if len(found) != len(required) {
		// A source identifier can straddle a message window.
		return nil, nil
	}
	return plan, nil
}

func compactEvidenceAppendix(missing []string, bodyRunes int) (string, bool) {
	if len(missing) == 0 {
		return "", true
	}
	if slices.ContainsFunc(missing, compactSensitiveEvidence.MatchString) {
		return "", false
	}
	extra := "\n\nSource evidence (quoted data to retain verbatim):\n- " + strings.Join(missing, "\n- ")
	return extra, bodyRunes+utf8.RuneCountInString(extra) <= compactlib.TranscriptMaxRunes
}

func (p *compactEvidencePlan) prepare(request compactlib.ChatRequest) (compactlib.ChatRequest, bool) {
	if len(p.required) == 0 {
		return request, true
	}
	var extra string
	var ok bool
	if request.Kind == "merge" {
		body := strings.TrimPrefix(request.UserPrompt, compactlib.MergePrompt("", "LuckyAgent"))
		var missing []string
		for _, identifier := range p.required {
			if !strings.Contains(body, identifier) {
				missing = append(missing, identifier)
			}
		}
		extra, ok = compactEvidenceAppendix(missing, request.TranscriptRunes)
	} else {
		extra, ok = p.segments[sha256.Sum256([]byte(request.UserPrompt))]
	}
	if !ok {
		return request, false
	}
	request.UserPrompt += extra
	request.TranscriptRunes += utf8.RuneCountInString(extra)
	return request, true
}
