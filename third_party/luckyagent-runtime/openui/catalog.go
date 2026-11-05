// Package openui is the server-side OpenUI catalog: names, props, and
// inspect/mutate policy. It does not render pixels. Clients implement the
// same names in their own UI toolkit.
package openui

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	PolicyInspect = "inspect"
	PolicyMutate  = "mutate"

	StateStreaming     = "streaming"
	StateComplete      = "complete"
	StateWaitingInput  = "waiting_input"
)

// Spec is one allowed component.
type Spec struct {
	Type       string
	Policy     string
	Required   []string
	ArrayProps []string
}

var catalog = map[string]Spec{
	"Table":     {Type: "Table", Policy: PolicyInspect, Required: []string{"columns", "rows"}},
	"Chart":     {Type: "Chart", Policy: PolicyInspect, Required: []string{"kind", "series"}},
	"List":      {Type: "List", Policy: PolicyInspect, Required: []string{"items"}},
	"Actions":   {Type: "Actions", Policy: PolicyMutate, Required: []string{"items"}},
	"Image":     {Type: "Image", Policy: PolicyInspect, Required: []string{"src"}},
	"Svg":       {Type: "Svg", Policy: PolicyInspect, Required: []string{"markup"}},
	"Citation":  {Type: "Citation", Policy: PolicyInspect, Required: []string{"source"}},
	"Map":       {Type: "Map", Policy: PolicyInspect, Required: []string{"points"}},
	"Markdown":  {Type: "Markdown", Policy: PolicyInspect, Required: []string{"text"}},
}

// Lookup returns a catalog spec.
func Lookup(typeName string) (Spec, bool) {
	spec, ok := catalog[typeName]
	return spec, ok
}

// Node is one tree node before transport.
type Node struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Policy   string         `json:"policy,omitempty"`
	Props    map[string]any `json:"props,omitempty"`
	Children []string       `json:"children,omitempty"`
}

// Delta is a catalog-validated patch.
type Delta struct {
	TreeID string `json:"tree_id"`
	State  string `json:"state"`
	Nodes  []Node `json:"nodes,omitempty"`
	Patch  bool   `json:"patch,omitempty"`
}

// ValidateDelta drops unknown components and nodes missing required props.
// An empty remaining tree is not an error: callers keep the Markdown path.
func ValidateDelta(raw json.RawMessage) (Delta, []string, error) {
	var delta Delta
	if len(raw) == 0 {
		return Delta{}, nil, fmt.Errorf("empty ui delta")
	}
	if err := json.Unmarshal(raw, &delta); err != nil {
		return Delta{}, nil, err
	}
	return FilterDelta(delta)
}

// FilterDelta keeps only catalog nodes.
func FilterDelta(delta Delta) (Delta, []string, error) {
	delta.TreeID = strings.TrimSpace(delta.TreeID)
	if delta.TreeID == "" {
		return Delta{}, nil, fmt.Errorf("ui delta missing tree_id")
	}
	switch delta.State {
	case StateStreaming, StateComplete, StateWaitingInput, "":
		if delta.State == "" {
			delta.State = StateStreaming
		}
	default:
		return Delta{}, nil, fmt.Errorf("unknown ui state %q", delta.State)
	}
	var dropped []string
	kept := make([]Node, 0, len(delta.Nodes))
	for _, node := range delta.Nodes {
		node.Type = strings.TrimSpace(node.Type)
		spec, ok := catalog[node.Type]
		if !ok {
			dropped = append(dropped, node.Type)
			continue
		}
		if strings.TrimSpace(node.ID) == "" {
			dropped = append(dropped, node.Type+"(no id)")
			continue
		}
		if !hasRequired(node.Props, spec.Required) {
			dropped = append(dropped, node.Type+"/"+node.ID)
			continue
		}
		node.Policy = spec.Policy
		kept = append(kept, node)
	}
	delta.Nodes = kept
	return delta, dropped, nil
}

func hasRequired(props map[string]any, required []string) bool {
	if len(required) == 0 {
		return true
	}
	if props == nil {
		return false
	}
	for _, key := range required {
		value, ok := props[key]
		if !ok || value == nil {
			return false
		}
		if text, isText := value.(string); isText && strings.TrimSpace(text) == "" {
			return false
		}
	}
	return true
}

// Action is a client intent against one widget. It is not tool execution.
type Action struct {
	TreeID   string         `json:"tree_id"`
	WidgetID string         `json:"widget_id"`
	Values   map[string]any `json:"values,omitempty"`
}

// ValidateAction checks identifiers only. Policy is applied by the Agent.
func ValidateAction(action Action) error {
	if strings.TrimSpace(action.TreeID) == "" {
		return fmt.Errorf("ui action missing tree_id")
	}
	if strings.TrimSpace(action.WidgetID) == "" {
		return fmt.Errorf("ui action missing widget_id")
	}
	return nil
}
