package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yurika0211/luckyagent-runtime/openui"
	"github.com/yurika0211/luckyagent-runtime/tool"
)

const presentUIToolName = "present_ui"

const openUIMetadataKey = "openui_delta"

const presentUIPolicy = `OpenUI policy:

When a free-chat answer is clearer as a table, chart, list, citation, map, or a small set of choices, call present_ui once with a catalog tree, then write the normal Markdown answer. Skip the tool for a plain sentence.

The tree is a JSON object: tree_id, state, nodes. Each node has id, type, and props. Allowed types and required props:

- Table: columns, rows
- Chart: kind, series
- List: items
- Actions: items (choices only; a write still goes through the existing approval flow)
- Image: src
- Svg: markup
- Citation: source
- Map: points
- Markdown: text

state is complete, or waiting_input when Actions needs a choice. Do not invent component names and do not send HTML as the tree.`

func (a *Agent) registerPresentUITool() {
	if a == nil || a.tools == nil {
		return
	}
	a.tools.Register(&tool.Tool{
		Name:         presentUIToolName,
		Category:     tool.CatBuiltin,
		Enabled:      true,
		Permission:   tool.PermAuto,
		ParallelSafe: true,
		Description:  "Show a catalog OpenUI tree beside the Markdown answer in free chat. Use it for a table, chart, list, citation, map, or choices. Pass tree as an object with tree_id, state, and nodes. Unknown components are dropped; an empty tree keeps Markdown only.",
		Parameters: map[string]tool.Param{
			"tree": {
				Type:        "object",
				Required:    true,
				Description: "OpenUI delta: tree_id, state (complete or waiting_input), nodes[{id,type,props}].",
			},
		},
		Handler: func(args map[string]any) (string, error) {
			raw, err := presentUIRaw(args["tree"])
			if err != nil {
				return "", err
			}
			event, dropped, err := EmitUIDelta(raw)
			if err != nil {
				return "", err
			}
			if event == nil || event.UI == nil || len(event.UI.Nodes) == 0 {
				return presentUIRejected(dropped), nil
			}
			encoded, err := json.Marshal(event.UI)
			if err != nil {
				return "", err
			}
			note := fmt.Sprintf("OpenUI tree %s accepted (%d nodes, state %s).", event.UI.TreeID, len(event.UI.Nodes), event.UI.State)
			if len(dropped) > 0 {
				note += " Dropped: " + strings.Join(dropped, ", ") + "."
			}
			note += " Continue with the Markdown answer. The tree is not a tool result the user must read."
			// The validated delta rides in a prefix the loop strips before the model sees the result.
			return openUIMetadataKey + ":" + string(encoded) + "\n" + note, nil
		},
	})
}

func presentUIRaw(value any) (json.RawMessage, error) {
	switch tree := value.(type) {
	case nil:
		return nil, fmt.Errorf("present_ui requires tree")
	case string:
		text := strings.TrimSpace(tree)
		if text == "" {
			return nil, fmt.Errorf("present_ui requires tree")
		}
		return json.RawMessage(text), nil
	case json.RawMessage:
		if len(tree) == 0 {
			return nil, fmt.Errorf("present_ui requires tree")
		}
		return tree, nil
	default:
		raw, err := json.Marshal(tree)
		if err != nil {
			return nil, fmt.Errorf("present_ui tree: %w", err)
		}
		return raw, nil
	}
}

func presentUIRejected(dropped []string) string {
	msg := "No catalog nodes remained. Answer in Markdown."
	if len(dropped) > 0 {
		msg = "Dropped " + strings.Join(dropped, ", ") + ". " + msg
	}
	return msg
}

// takeOpenUIDelta splits a present_ui handler result into a validated tree and
// the note returned to the model. Other tool results pass through unchanged.
func takeOpenUIDelta(toolName, result string) (*openui.Delta, string) {
	if strings.TrimSpace(toolName) != presentUIToolName {
		return nil, result
	}
	prefix, note, ok := strings.Cut(result, "\n")
	if !ok || !strings.HasPrefix(prefix, openUIMetadataKey+":") {
		return nil, result
	}
	raw := strings.TrimPrefix(prefix, openUIMetadataKey+":")
	var delta openui.Delta
	if err := json.Unmarshal([]byte(raw), &delta); err != nil || len(delta.Nodes) == 0 {
		return nil, note
	}
	return &delta, note
}
