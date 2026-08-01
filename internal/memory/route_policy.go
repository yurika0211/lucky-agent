package memory

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// RoutePolicy is a typed, data-owned rule attached to a durable memory note.
// The router evaluates only policies on memories activated for the current query.
type RoutePolicy struct {
	ID             string                 `json:"id" yaml:"id"`
	Match          RoutePolicyMatch       `json:"match,omitempty" yaml:"match,omitempty"`
	Risks          []RouteRisk            `json:"risks,omitempty" yaml:"risks,omitempty"`
	RequiredTools  []RouteToolRequirement `json:"required_tools,omitempty" yaml:"required_tools,omitempty"`
	Constraints    []string               `json:"constraints,omitempty" yaml:"constraints,omitempty"`
	Clarifications []string               `json:"clarifications,omitempty" yaml:"clarifications,omitempty"`
}

// RoutePolicyMatch combines query term groups and temporal state predicates.
// Every QueryAll group and every State predicate must match. QueryAny, when
// present, requires at least one match. QueryNone excludes a policy.
type RoutePolicyMatch struct {
	QueryAll  []RouteTermGroup  `json:"query_all,omitempty" yaml:"query_all,omitempty"`
	QueryAny  []string          `json:"query_any,omitempty" yaml:"query_any,omitempty"`
	QueryNone []string          `json:"query_none,omitempty" yaml:"query_none,omitempty"`
	States    []RouteStateMatch `json:"states,omitempty" yaml:"states,omitempty"`
}

// RouteTermGroup represents one required semantic slot with alternative terms.
type RouteTermGroup struct {
	Any []string `json:"any" yaml:"any"`
}

// RouteStateMatch requires the current value of an exact memory state key.
type RouteStateMatch struct {
	Key       string   `json:"key" yaml:"key"`
	Values    []string `json:"values,omitempty" yaml:"values,omitempty"`
	NotValues []string `json:"not_values,omitempty" yaml:"not_values,omitempty"`
}

// RouteRisk is a named policy signal with data-defined ordering priority.
type RouteRisk struct {
	Name     string `json:"name" yaml:"name"`
	Priority int    `json:"priority,omitempty" yaml:"priority,omitempty"`
}

// RouteToolRequirement describes one tool and the calls needed before synthesis.
// Empty Calls means one call with an empty argument object.
type RouteToolRequirement struct {
	Name  string          `json:"name" yaml:"name"`
	Calls []RouteToolCall `json:"calls,omitempty" yaml:"calls,omitempty"`
}

// RouteToolCall holds provider-independent structured tool arguments.
type RouteToolCall struct {
	Arguments map[string]any `json:"arguments,omitempty" yaml:"arguments,omitempty"`
}

// AppliedRoutePolicy identifies the durable memory rule that affected routing.
type AppliedRoutePolicy struct {
	ID          string `json:"id"`
	EntryID     string `json:"entry_id"`
	EvidenceRef string `json:"evidence_ref,omitempty"`
}

// RouteOptions controls which recalled entries can affect routing.
type RouteOptions struct {
	EntryFilter func(Entry) bool
}

func normalizeRoutePolicies(policies []RoutePolicy) ([]RoutePolicy, error) {
	if len(policies) == 0 {
		return nil, nil
	}
	out := make([]RoutePolicy, 0, len(policies))
	seen := make(map[string]struct{}, len(policies))
	for _, policy := range policies {
		policy.ID = strings.TrimSpace(policy.ID)
		if policy.ID == "" {
			return nil, fmt.Errorf("route policy id is required")
		}
		key := strings.ToLower(policy.ID)
		if _, ok := seen[key]; ok {
			return nil, fmt.Errorf("duplicate route policy id %q", policy.ID)
		}
		seen[key] = struct{}{}
		policy.Match.QueryAny = dedupTrimmed(policy.Match.QueryAny)
		policy.Match.QueryNone = dedupTrimmed(policy.Match.QueryNone)
		for i := range policy.Match.QueryAll {
			policy.Match.QueryAll[i].Any = dedupTrimmed(policy.Match.QueryAll[i].Any)
			if len(policy.Match.QueryAll[i].Any) == 0 {
				return nil, fmt.Errorf("route policy %q has an empty query_all group", policy.ID)
			}
		}
		for i := range policy.Match.States {
			state := &policy.Match.States[i]
			state.Key = strings.TrimSpace(state.Key)
			state.Values = dedupTrimmed(state.Values)
			state.NotValues = dedupTrimmed(state.NotValues)
			if state.Key == "" {
				return nil, fmt.Errorf("route policy %q has a state match without a key", policy.ID)
			}
		}
		for i := range policy.Risks {
			policy.Risks[i].Name = strings.TrimSpace(policy.Risks[i].Name)
			if policy.Risks[i].Name == "" {
				return nil, fmt.Errorf("route policy %q has a risk without a name", policy.ID)
			}
		}
		for i := range policy.RequiredTools {
			requirement := &policy.RequiredTools[i]
			requirement.Name = strings.TrimSpace(requirement.Name)
			if requirement.Name == "" {
				return nil, fmt.Errorf("route policy %q has a tool requirement without a name", policy.ID)
			}
			for _, call := range requirement.Calls {
				if _, err := json.Marshal(call.Arguments); err != nil {
					return nil, fmt.Errorf("route policy %q tool %q arguments: %w", policy.ID, requirement.Name, err)
				}
			}
		}
		policy.Constraints = dedupTrimmed(policy.Constraints)
		policy.Clarifications = dedupTrimmed(policy.Clarifications)
		if len(policy.Risks) == 0 && len(policy.RequiredTools) == 0 && len(policy.Constraints) == 0 && len(policy.Clarifications) == 0 {
			return nil, fmt.Errorf("route policy %q has no routing effects", policy.ID)
		}
		out = append(out, policy)
	}
	return out, nil
}

func mergeRoutePolicies(existing, incoming []RoutePolicy) []RoutePolicy {
	if len(incoming) == 0 {
		return existing
	}
	out := append([]RoutePolicy(nil), existing...)
	index := make(map[string]int, len(out))
	for i, policy := range out {
		index[strings.ToLower(strings.TrimSpace(policy.ID))] = i
	}
	for _, policy := range incoming {
		key := strings.ToLower(strings.TrimSpace(policy.ID))
		if i, ok := index[key]; ok {
			out[i] = policy
			continue
		}
		index[key] = len(out)
		out = append(out, policy)
	}
	return out
}

func applyRoutePolicies(route *RouteAnalysis, query string, entries []Entry) {
	if route == nil || len(entries) == 0 {
		return
	}
	riskByName := make(map[string]RouteRisk)
	toolIndex := make(map[string]int)
	toolCallSeen := make(map[string]map[string]struct{})
	for _, entry := range entries {
		for _, policy := range entry.RoutePolicies {
			if !routePolicyMatches(policy.Match, query, entries) {
				continue
			}
			variables := map[string]string{
				"query":          strings.TrimSpace(query),
				"policy.id":      policy.ID,
				"memory.id":      entry.ID,
				"memory.content": entry.Content,
			}
			for _, stateEntry := range entries {
				stateKey := strings.TrimSpace(stateEntry.StateKey)
				if stateKey != "" {
					variables["state."+stateKey] = strings.TrimSpace(stateEntry.StateValue)
				}
			}
			route.AppliedPolicies = append(route.AppliedPolicies, AppliedRoutePolicy{
				ID:          policy.ID,
				EntryID:     entry.ID,
				EvidenceRef: refForEntry(&entry),
			})
			for _, risk := range policy.Risks {
				key := strings.ToLower(risk.Name)
				if current, ok := riskByName[key]; !ok || risk.Priority > current.Priority {
					riskByName[key] = risk
				}
			}
			for _, requirement := range policy.RequiredTools {
				requirement = renderRouteToolRequirement(requirement, variables)
				key := strings.ToLower(requirement.Name)
				idx, ok := toolIndex[key]
				if !ok {
					idx = len(route.ToolRequirements)
					toolIndex[key] = idx
					toolCallSeen[key] = make(map[string]struct{})
					route.ToolRequirements = append(route.ToolRequirements, RouteToolRequirement{Name: requirement.Name})
				}
				for _, call := range requirement.Calls {
					sig := routeToolCallSignature(call)
					if _, exists := toolCallSeen[key][sig]; exists {
						continue
					}
					toolCallSeen[key][sig] = struct{}{}
					route.ToolRequirements[idx].Calls = append(route.ToolRequirements[idx].Calls, call)
				}
			}
			for _, constraint := range policy.Constraints {
				route.Constraints = append(route.Constraints, renderRouteTemplate(constraint, variables))
			}
			for _, clarification := range policy.Clarifications {
				route.Clarifications = append(route.Clarifications, renderRouteTemplate(clarification, variables))
			}
		}
	}

	for _, risk := range riskByName {
		route.Risks = append(route.Risks, risk)
	}
	sort.SliceStable(route.Risks, func(i, j int) bool {
		return route.Risks[i].Priority > route.Risks[j].Priority
	})
	for _, risk := range route.Risks {
		route.RiskFlags = append(route.RiskFlags, risk.Name)
	}
	for _, requirement := range route.ToolRequirements {
		route.RequiredTools = append(route.RequiredTools, requirement.Name)
		for _, call := range requirement.Calls {
			if queryValue, ok := call.Arguments["query"].(string); ok && strings.TrimSpace(queryValue) != "" {
				route.SuggestedSearches = append(route.SuggestedSearches, strings.TrimSpace(queryValue))
			}
		}
	}
	route.RequiredTools = dedupSlice(route.RequiredTools)
	route.SuggestedSearches = dedupSlice(route.SuggestedSearches)
	route.RiskFlags = dedupSlice(route.RiskFlags)
	route.Constraints = dedupSlice(route.Constraints)
	route.Clarifications = dedupSlice(route.Clarifications)
}

func routePolicyMatches(match RoutePolicyMatch, query string, entries []Entry) bool {
	query = strings.ToLower(query)
	if containsAnyFold(query, match.QueryNone) {
		return false
	}
	if len(match.QueryAny) > 0 && !containsAnyFold(query, match.QueryAny) {
		return false
	}
	for _, group := range match.QueryAll {
		if !containsAnyFold(query, group.Any) {
			return false
		}
	}
	for _, state := range match.States {
		if !routeStateMatches(entries, state) {
			return false
		}
	}
	return true
}

func routeStateMatches(entries []Entry, match RouteStateMatch) bool {
	for _, entry := range entries {
		if !strings.EqualFold(strings.TrimSpace(entry.StateKey), strings.TrimSpace(match.Key)) {
			continue
		}
		value := strings.TrimSpace(entry.StateValue)
		if len(match.Values) > 0 && !stringInFold(value, match.Values) {
			continue
		}
		if stringInFold(value, match.NotValues) {
			continue
		}
		return true
	}
	return false
}

func renderRouteToolRequirement(requirement RouteToolRequirement, variables map[string]string) RouteToolRequirement {
	for i := range requirement.Calls {
		requirement.Calls[i].Arguments = renderRouteMap(requirement.Calls[i].Arguments, variables)
	}
	return requirement
}

func renderRouteMap(values map[string]any, variables map[string]string) map[string]any {
	if len(values) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = renderRouteValue(value, variables)
	}
	return out
}

func renderRouteValue(value any, variables map[string]string) any {
	switch typed := value.(type) {
	case string:
		return renderRouteTemplate(typed, variables)
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = renderRouteValue(item, variables)
		}
		return out
	case map[string]any:
		return renderRouteMap(typed, variables)
	default:
		return value
	}
}

func renderRouteTemplate(value string, variables map[string]string) string {
	for key, replacement := range variables {
		value = strings.ReplaceAll(value, "{{"+key+"}}", replacement)
	}
	return strings.TrimSpace(value)
}

func routeToolCallSignature(call RouteToolCall) string {
	data, err := json.Marshal(call.Arguments)
	if err != nil {
		return fmt.Sprint(call.Arguments)
	}
	return string(data)
}

func containsAnyFold(text string, values []string) bool {
	text = strings.ToLower(text)
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" && strings.Contains(text, value) {
			return true
		}
	}
	return false
}

func stringInFold(value string, values []string) bool {
	for _, candidate := range values {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(candidate)) {
			return true
		}
	}
	return false
}

func dedupTrimmed(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}
