package computer

import "context"

// AccessibilityNode identifies a control in one snapshot. IDs are opaque and
// must not be invented or reused after a new observation.
type AccessibilityNode struct {
	ID        string   `json:"id"`
	ParentID  string   `json:"parent_id,omitempty"`
	Name      string   `json:"name,omitempty"`
	Role      string   `json:"role"`
	Bounds    Rect     `json:"bounds"`
	Actions   []string `json:"actions,omitempty"`
	Editable  bool     `json:"editable,omitempty"`
	Focusable bool     `json:"focusable,omitempty"`
	Enabled   bool     `json:"enabled"`
}

type AccessibilityTree struct {
	Window    string              `json:"window,omitempty"`
	Nodes     []AccessibilityNode `json:"nodes"`
	Truncated bool                `json:"truncated,omitempty"`
}

type accessibilityBackend interface {
	Accessibility(context.Context, Target) (AccessibilityTree, error)
}

type observationValidator interface {
	ValidateObservation(context.Context, Observation, Action) error
}
