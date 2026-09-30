package tidal

import (
	"strings"
	"time"
)

// Tier is the retention class copied onto telemetry. Values match memory.Tier.
type Tier int

const (
	TierShort Tier = iota
	TierMedium
	TierLong
)

func (t Tier) String() string {
	switch t {
	case TierShort:
		return "short"
	case TierMedium:
		return "medium"
	case TierLong:
		return "long"
	default:
		return "unknown"
	}
}

// Note is the slice of a durable memory entry that ranking and telemetry use.
type Note struct {
	ID         string
	Content    string
	Category   string
	Tier       Tier
	Tags       []string
	CreatedAt  time.Time
	AccessedAt time.Time
}

// Score is one recalled note plus the fields a reranker may adjust.
type Score struct {
	EntryID    string
	Entry      Note
	Score      float64
	Components Components
}

// Components carries the boost this package writes. Other score parts stay
// owned by the caller and are preserved across rerank.
type Components struct {
	GraphBoost float64
	TidalBoost float64
}

// Feedback is weak supervision delivered through the memory package observer.
type Feedback struct {
	Query   string
	QueryID string
	Entry   Note
	Signal  string
	Value   float64
	At      time.Time
	Keys    []string
}

func tidalQueryTerms(query string) []string {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(query)))
	if len(fields) == 0 {
		return nil
	}
	return fields
}
