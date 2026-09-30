// Package note is the durable memory record shared by the vault and its policies.
package note

import (
	"math"
	"time"
)

// Tier is the retention class of one durable note.
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

// Entry is one durable memory note.
type Entry struct {
	ID            string        `json:"id"`
	Content       string        `json:"content"`
	Category      string        `json:"category"`
	Tier          Tier          `json:"tier"`
	Importance    float64       `json:"importance"`
	AccessCount   int           `json:"access_count"`
	CreatedAt     time.Time     `json:"created_at"`
	AccessedAt    time.Time     `json:"accessed_at"`
	Tags          []string      `json:"tags,omitempty"`
	SummaryOf     []string      `json:"summary_of,omitempty"`
	ExpiresAt     *time.Time    `json:"expires_at,omitempty"`
	Status        string        `json:"status,omitempty"`
	ValidFrom     time.Time     `json:"valid_from,omitempty"`
	ValidUntil    *time.Time    `json:"valid_until,omitempty"`
	Links         []string      `json:"links,omitempty"`
	Aliases       []string      `json:"aliases,omitempty"`
	StateKey      string        `json:"state_key,omitempty"`
	StateValue    string        `json:"state_value,omitempty"`
	Confidence    float64       `json:"confidence,omitempty"`
	Supersedes    []string      `json:"supersedes,omitempty"`
	RoutePolicies []RoutePolicy `json:"route_policies,omitempty"`
	BlockID       string        `json:"block_id,omitempty"`
	Path          string        `json:"path,omitempty"`
}

// Weight ranks a note by importance, age, and how often it has been recalled.
func (e *Entry) Weight(now time.Time) float64 {
	if e == nil {
		return 0
	}
	return e.Importance * e.RecencyFactor(now) * e.AccessBoost()
}

// RecencyFactor is the age decay for one note. A missing half-life keeps the note at full strength.
func (e *Entry) RecencyFactor(now time.Time) float64 {
	halflife := e.HalfLife()
	if halflife <= 0 {
		return 1
	}
	age := now.Sub(e.CreatedAt).Hours()
	if age <= 0 {
		return 1
	}
	return math.Pow(0.5, age/halflife)
}

// AccessBoost raises a note that has been recalled, up to a fixed cap.
func (e *Entry) AccessBoost() float64 {
	if e.AccessCount <= 0 {
		return 1
	}
	return 1 + min(math.Log1p(float64(e.AccessCount))*0.12, 0.75)
}

// HalfLife returns the note's half-life in hours.
func (e *Entry) HalfLife() float64 {
	switch e.Tier {
	case TierShort:
		return 1.0
	case TierMedium:
		return 24.0 * 7
	case TierLong:
		return 24.0 * 365
	default:
		return 24.0
	}
}

// Ref is the stable path and block reference for a note.
func Ref(e Entry) string {
	ref := e.ID
	if e.Path != "" {
		ref = e.Path
		if e.BlockID != "" {
			ref += "#" + e.BlockID
		}
	}
	return ref
}
