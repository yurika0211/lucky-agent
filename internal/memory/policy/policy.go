package policy

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/yurika0211/luckyagent/internal/memory/textutil"
	"github.com/yurika0211/luckyagent/internal/memory/note"
)

type (
	RoutePolicy          = note.RoutePolicy
	RoutePolicyMatch     = note.RoutePolicyMatch
	RouteTermGroup       = note.RouteTermGroup
	RouteStateMatch      = note.RouteStateMatch
	RouteRisk            = note.RouteRisk
	RouteToolRequirement = note.RouteToolRequirement
	RouteToolCall        = note.RouteToolCall
	AppliedRoutePolicy   = note.AppliedRoutePolicy
)

// RouteOptions controls which recalled entries can affect routing.
type RouteOptions struct {
	EntryFilter func(note.Entry) bool
}

// Normalize checks policy ids, match groups, and effects.
func Normalize(policies []RoutePolicy) ([]RoutePolicy, error) {
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

// Merge replaces policies that share an id and appends the rest.
func Merge(existing, incoming []RoutePolicy) []RoutePolicy {
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

func Apply(route *Analysis, query string, entries []note.Entry) {
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
				EvidenceRef: note.Ref(entry),
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
	route.RequiredTools = textutil.DedupNonEmptyStrings(route.RequiredTools)
	route.SuggestedSearches = textutil.DedupNonEmptyStrings(route.SuggestedSearches)
	route.RiskFlags = textutil.DedupNonEmptyStrings(route.RiskFlags)
	route.Constraints = textutil.DedupNonEmptyStrings(route.Constraints)
	route.Clarifications = textutil.DedupNonEmptyStrings(route.Clarifications)
}

func routePolicyMatches(match RoutePolicyMatch, query string, entries []note.Entry) bool {
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

func routeStateMatches(entries []note.Entry, match RouteStateMatch) bool {
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
