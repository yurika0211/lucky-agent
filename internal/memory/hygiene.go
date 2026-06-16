package memory

import (
	"regexp"
	"sort"
	"strings"
	"time"
)

// HygieneOptions configures deterministic memory hygiene scans.
type HygieneOptions struct {
	IncludeInactive bool
	MinSeverity     string
	MaxFindings     int
	Now             time.Time
}

// HygieneIssue describes one memory entry that should be reviewed or cleaned.
type HygieneIssue struct {
	ID              string  `json:"id"`
	Path            string  `json:"path,omitempty"`
	Category        string  `json:"category"`
	Tier            string  `json:"tier"`
	Severity        string  `json:"severity"`
	Reason          string  `json:"reason"`
	SuggestedAction string  `json:"suggested_action"`
	Score           float64 `json:"score"`
	Preview         string  `json:"preview"`
}

// HygieneReport is returned by audit, quarantine, and delete operations.
type HygieneReport struct {
	Scanned     int            `json:"scanned"`
	Findings    []HygieneIssue `json:"findings"`
	Quarantined int            `json:"quarantined,omitempty"`
	Deleted     int            `json:"deleted,omitempty"`
	Action      string         `json:"action"`
}

var (
	rawConversationPrefixRe = regexp.MustCompile(`(?i)^\s*(user|assistant|system)\s*:`)
	secretLikeRe            = regexp.MustCompile(`(?i)(api[_-]?key|access[_-]?token|secret|password|passwd|bearer\s+[a-z0-9._~+/-]{16,}|sk-[a-z0-9]{16,}|xox[baprs]-[a-z0-9-]{10,})`)
	promptInjectionRe       = regexp.MustCompile(`(?i)(ignore (all )?(previous|prior) instructions|system prompt|developer message|you are now|jailbreak|do not obey|泄露.*提示词|忽略.*指令|越狱)`)
)

// AuditHygiene scans memory entries for deterministic dirty-memory signals.
func (s *Store) AuditHygiene(opts HygieneOptions) HygieneReport {
	if s == nil {
		return HygieneReport{Action: "audit"}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	now := hygieneNow(opts)
	issues := s.hygieneIssuesLocked(opts, now)
	return HygieneReport{
		Scanned:  s.hygieneScannedLocked(opts, now),
		Findings: limitHygieneIssues(filterHygieneSeverity(issues, opts.MinSeverity), opts.MaxFindings),
		Action:   "audit",
	}
}

// QuarantineDirty marks dirty active memories as archived so they stop
// participating in recall while preserving the source note for review.
func (s *Store) QuarantineDirty(opts HygieneOptions) (HygieneReport, error) {
	return s.applyHygiene(opts, "quarantine")
}

// DeleteDirty deletes matching dirty memories. Prefer QuarantineDirty unless
// the caller explicitly requested physical deletion.
func (s *Store) DeleteDirty(opts HygieneOptions) (HygieneReport, error) {
	return s.applyHygiene(opts, "delete")
}

func (s *Store) applyHygiene(opts HygieneOptions, action string) (HygieneReport, error) {
	if s == nil {
		return HygieneReport{Action: action}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := hygieneNow(opts)
	issues := limitHygieneIssues(filterHygieneSeverity(s.hygieneIssuesLocked(opts, now), opts.MinSeverity), opts.MaxFindings)
	report := HygieneReport{
		Scanned:  s.hygieneScannedLocked(opts, now),
		Findings: issues,
		Action:   action,
	}
	if len(issues) == 0 {
		return report, nil
	}
	for _, issue := range issues {
		entry := s.entries[issue.ID]
		if entry == nil {
			continue
		}
		switch action {
		case "delete":
			s.removeEntryFileLocked(issue.ID)
			delete(s.entries, issue.ID)
			report.Deleted++
		default:
			entry.Status = "archived"
			entry.Tags = mergeTags(entry.Tags, []string{"hygiene", "dirty", "hygiene-" + issue.Reason})
			if entry.Confidence == 0 || entry.Confidence > 0.25 {
				entry.Confidence = 0.25
			}
			report.Quarantined++
		}
	}
	if report.Quarantined > 0 || report.Deleted > 0 {
		if err := s.persist(); err != nil {
			return report, err
		}
	}
	return report, nil
}

func (s *Store) hygieneIssuesLocked(opts HygieneOptions, now time.Time) []HygieneIssue {
	var issues []HygieneIssue
	seenIssue := make(map[string]bool)
	normalizedByContent := make(map[string]*Entry)
	activeState := make(map[string]*Entry)
	ids := make([]string, 0, len(s.entries))
	for id := range s.entries {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		e := s.entries[id]
		if e == nil || (!opts.IncludeInactive && !entryIsActive(e, now)) {
			continue
		}
		content := strings.TrimSpace(e.Content)
		if content == "" {
			issues = appendHygieneIssue(issues, seenIssue, e, "high", "empty", "delete", 0.85, "")
			continue
		}
		if rawConversationPrefixRe.MatchString(content) || rawConversationMemory(e) {
			issues = appendHygieneIssue(issues, seenIssue, e, "high", "raw_conversation", "quarantine", 0.82, content)
		}
		if secretLikeRe.MatchString(content) {
			issues = appendHygieneIssue(issues, seenIssue, e, "critical", "secret_like", "quarantine", 0.98, content)
		}
		if promptInjectionRe.MatchString(content) {
			issues = appendHygieneIssue(issues, seenIssue, e, "high", "prompt_injection", "quarantine", 0.86, content)
		}
		if e.ExpiresAt != nil && !e.ExpiresAt.After(now) {
			issues = appendHygieneIssue(issues, seenIssue, e, "medium", "expired", "delete", 0.65, content)
		}
		if e.Tier == TierLong && e.Confidence > 0 && e.Confidence < 0.35 {
			issues = appendHygieneIssue(issues, seenIssue, e, "medium", "low_confidence_long_term", "quarantine", 0.62, content)
		}
		key := hygieneDuplicateKey(e)
		if key != "" {
			if existing := normalizedByContent[key]; existing != nil {
				loser := lowerValueEntry(existing, e, now)
				issues = appendHygieneIssue(issues, seenIssue, loser, "medium", "duplicate", "delete", 0.70, loser.Content)
			} else {
				normalizedByContent[key] = e
			}
		}
		stateKey := strings.ToLower(strings.TrimSpace(e.StateKey))
		if stateKey != "" && entryIsActive(e, now) {
			if existing := activeState[stateKey]; existing != nil && !strings.EqualFold(strings.TrimSpace(existing.StateValue), strings.TrimSpace(e.StateValue)) {
				loser := lowerConfidenceEntry(existing, e, now)
				issues = appendHygieneIssue(issues, seenIssue, loser, "high", "state_conflict", "quarantine", 0.88, loser.Content)
			} else {
				activeState[stateKey] = e
			}
		}
		if len([]rune(content)) > 4000 && e.Tier != TierLong {
			issues = appendHygieneIssue(issues, seenIssue, e, "low", "oversized", "quarantine", 0.40, content)
		}
	}
	sort.SliceStable(issues, func(i, j int) bool {
		if issues[i].Score == issues[j].Score {
			return issues[i].ID < issues[j].ID
		}
		return issues[i].Score > issues[j].Score
	})
	return issues
}

func (s *Store) hygieneScannedLocked(opts HygieneOptions, now time.Time) int {
	scanned := 0
	for _, e := range s.entries {
		if e == nil || (!opts.IncludeInactive && !entryIsActive(e, now)) {
			continue
		}
		scanned++
	}
	return scanned
}

func appendHygieneIssue(issues []HygieneIssue, seen map[string]bool, e *Entry, severity, reason, action string, score float64, preview string) []HygieneIssue {
	if e == nil {
		return issues
	}
	key := e.ID + "\x00" + reason
	if seen[key] {
		return issues
	}
	seen[key] = true
	if preview == "" {
		preview = e.Content
	}
	return append(issues, HygieneIssue{
		ID:              e.ID,
		Path:            e.Path,
		Category:        e.Category,
		Tier:            e.Tier.String(),
		Severity:        severity,
		Reason:          reason,
		SuggestedAction: action,
		Score:           score,
		Preview:         truncateRunes(strings.ReplaceAll(strings.TrimSpace(preview), "\n", " "), 160),
	})
}

func rawConversationMemory(e *Entry) bool {
	if e == nil {
		return false
	}
	category := strings.ToLower(strings.TrimSpace(e.Category))
	if category != "conversation" && category != "session" {
		return false
	}
	content := strings.TrimSpace(e.Content)
	return strings.Contains(content, "\nUser:") || strings.Contains(content, "\nAssistant:") || strings.HasPrefix(content, "User:") || strings.HasPrefix(content, "Assistant:")
}

func hygieneDuplicateKey(e *Entry) string {
	if e == nil {
		return ""
	}
	content := strings.ToLower(strings.Join(strings.Fields(stripWikiSyntax(e.Content)), " "))
	if content == "" {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(e.Category)) + "\x00" + content
}

func lowerValueEntry(a, b *Entry, now time.Time) *Entry {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if a.Weight(now) == b.Weight(now) {
		if a.CreatedAt.After(b.CreatedAt) {
			return b
		}
		return a
	}
	if a.Weight(now) < b.Weight(now) {
		return a
	}
	return b
}

func lowerConfidenceEntry(a, b *Entry, now time.Time) *Entry {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	aConf := a.Confidence
	bConf := b.Confidence
	if aConf == 0 {
		aConf = a.Importance
	}
	if bConf == 0 {
		bConf = b.Importance
	}
	if aConf == bConf {
		return lowerValueEntry(a, b, now)
	}
	if aConf < bConf {
		return a
	}
	return b
}

func filterHygieneSeverity(issues []HygieneIssue, minSeverity string) []HygieneIssue {
	minRank := hygieneSeverityRank(minSeverity)
	if minRank <= 0 {
		return issues
	}
	out := make([]HygieneIssue, 0, len(issues))
	for _, issue := range issues {
		if hygieneSeverityRank(issue.Severity) >= minRank {
			out = append(out, issue)
		}
	}
	return out
}

func hygieneSeverityRank(severity string) int {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "critical":
		return 4
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}

func limitHygieneIssues(issues []HygieneIssue, limit int) []HygieneIssue {
	if limit <= 0 || len(issues) <= limit {
		return issues
	}
	return append([]HygieneIssue(nil), issues[:limit]...)
}

func hygieneNow(opts HygieneOptions) time.Time {
	if opts.Now.IsZero() {
		return time.Now()
	}
	return opts.Now
}
