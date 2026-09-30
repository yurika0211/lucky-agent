// Active-state checks and current-versus-superseded resolution.
package memory

import (
	"strings"
	"time"

	"github.com/yurika0211/luckyagent/internal/memory/note"
)

func entryIsActive(e *Entry, asOf time.Time) bool {
	if e == nil {
		return false
	}
	status := strings.ToLower(strings.TrimSpace(e.Status))
	if status != "" && status != "active" {
		return false
	}
	if !e.ValidFrom.IsZero() && e.ValidFrom.After(asOf) {
		return false
	}
	if e.ValidUntil != nil && !e.ValidUntil.After(asOf) {
		return false
	}
	if e.ExpiresAt != nil && !e.ExpiresAt.After(asOf) {
		return false
	}
	return true
}

func resolveActiveTemporalEntries(entries []Entry) ([]Entry, []string, []string) {
	if len(entries) <= 1 {
		return entries, nil, nil
	}
	latestByState := make(map[string]Entry)
	explicitSuperseded := make(map[string]string)
	for _, e := range entries {
		stateKey := strings.ToLower(strings.TrimSpace(e.StateKey))
		if stateKey != "" {
			if current, ok := latestByState[stateKey]; !ok || temporalEntryAfter(e, current) {
				latestByState[stateKey] = e
			}
		}
		for _, id := range e.Supersedes {
			id = strings.TrimSpace(id)
			if id != "" {
				explicitSuperseded[id] = refForEntry(&e)
			}
		}
	}

	selected := make([]Entry, 0, len(entries))
	var notes []string
	var supersededRefs []string
	for _, e := range entries {
		ref := refForEntry(&e)
		if by, ok := explicitSuperseded[e.ID]; ok {
			supersededRefs = append(supersededRefs, ref)
			notes = append(notes, "Superseded memory ignored: "+ref+"; replaced by "+by+".")
			continue
		}
		stateKey := strings.ToLower(strings.TrimSpace(e.StateKey))
		if stateKey != "" {
			latest := latestByState[stateKey]
			if latest.ID != e.ID {
				supersededRefs = append(supersededRefs, ref)
				notes = append(notes, "For state "+stateKey+", prefer latest memory "+refForEntry(&latest)+" over older memory "+ref+".")
				continue
			}
		}
		selected = append(selected, e)
	}
	return selected, dedupSlice(notes), dedupSlice(supersededRefs)
}

func temporalEntryAfter(a, b Entry) bool {
	at := entryTemporalTime(a)
	bt := entryTemporalTime(b)
	if !at.Equal(bt) {
		return at.After(bt)
	}
	if a.Confidence != b.Confidence {
		return a.Confidence > b.Confidence
	}
	if a.Importance != b.Importance {
		return a.Importance > b.Importance
	}
	return a.CreatedAt.After(b.CreatedAt)
}

func entryTemporalTime(e Entry) time.Time {
	if !e.ValidFrom.IsZero() {
		return e.ValidFrom
	}
	return e.CreatedAt
}

func temporalCandidateMatches(e *Entry, queryLower string, queryTerms []string, activeLinks, activeStateKeys map[string]bool) bool {
	if e == nil {
		return false
	}
	if e.StateKey != "" && activeStateKeys[strings.ToLower(e.StateKey)] {
		return true
	}
	if memoryMatchScore(e, queryLower, queryTerms) > 0 {
		return true
	}
	for _, link := range e.Links {
		if activeLinks[graphKey(link)] {
			return true
		}
	}
	return false
}

func temporalInactiveReason(e *Entry, now time.Time) string {
	if e == nil {
		return ""
	}
	status := strings.ToLower(strings.TrimSpace(e.Status))
	switch status {
	case "conflict":
		return "conflict"
	case "superseded":
		return "superseded"
	}
	if !e.ValidFrom.IsZero() && e.ValidFrom.After(now) {
		return "future"
	}
	if e.ValidUntil != nil && !e.ValidUntil.After(now) {
		return "expired"
	}
	if e.ExpiresAt != nil && !e.ExpiresAt.After(now) {
		return "expired"
	}
	return ""
}

func refForEntry(e *Entry) string {
	if e == nil {
		return ""
	}
	return note.Ref(*e)
}

func routeEvidenceRefs(entries []Entry, limit int) []string {
	if limit <= 0 || len(entries) == 0 {
		return nil
	}
	capacity := limit
	if len(entries) < capacity {
		capacity = len(entries)
	}
	refs := make([]string, 0, capacity)
	for _, e := range entries {
		ref := refForEntry(&e)
		if ref != "" {
			refs = append(refs, ref)
		}
		if len(refs) >= limit {
			break
		}
	}
	return dedupSlice(refs)
}
