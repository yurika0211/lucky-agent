package note

// RoutePolicy is a typed rule stored on a durable note.
type RoutePolicy struct {
	ID             string                 `json:"id" yaml:"id"`
	Match          RoutePolicyMatch       `json:"match,omitempty" yaml:"match,omitempty"`
	Risks          []RouteRisk            `json:"risks,omitempty" yaml:"risks,omitempty"`
	RequiredTools  []RouteToolRequirement `json:"required_tools,omitempty" yaml:"required_tools,omitempty"`
	Constraints    []string               `json:"constraints,omitempty" yaml:"constraints,omitempty"`
	Clarifications []string               `json:"clarifications,omitempty" yaml:"clarifications,omitempty"`
}

// RoutePolicyMatch combines query term groups and temporal state predicates.
type RoutePolicyMatch struct {
	QueryAll  []RouteTermGroup  `json:"query_all,omitempty" yaml:"query_all,omitempty"`
	QueryAny  []string          `json:"query_any,omitempty" yaml:"query_any,omitempty"`
	QueryNone []string          `json:"query_none,omitempty" yaml:"query_none,omitempty"`
	States    []RouteStateMatch `json:"states,omitempty" yaml:"states,omitempty"`
}

// RouteTermGroup is one required semantic slot with alternative terms.
type RouteTermGroup struct {
	Any []string `json:"any" yaml:"any"`
}

// RouteStateMatch requires the current value of an exact memory state key.
type RouteStateMatch struct {
	Key       string   `json:"key" yaml:"key"`
	Values    []string `json:"values,omitempty" yaml:"values,omitempty"`
	NotValues []string `json:"not_values,omitempty" yaml:"not_values,omitempty"`
}

// RouteRisk is a named policy signal with a data-defined priority.
type RouteRisk struct {
	Name     string `json:"name" yaml:"name"`
	Priority int    `json:"priority,omitempty" yaml:"priority,omitempty"`
}

// RouteToolRequirement describes one tool and the calls needed before synthesis.
type RouteToolRequirement struct {
	Name  string          `json:"name" yaml:"name"`
	Calls []RouteToolCall `json:"calls,omitempty" yaml:"calls,omitempty"`
}

// RouteToolCall holds provider-independent structured tool arguments.
type RouteToolCall struct {
	Arguments map[string]any `json:"arguments,omitempty" yaml:"arguments,omitempty"`
}

// AppliedRoutePolicy identifies the note rule that affected routing.
type AppliedRoutePolicy struct {
	ID          string `json:"id"`
	EntryID     string `json:"entry_id"`
	EvidenceRef string `json:"evidence_ref,omitempty"`
}
