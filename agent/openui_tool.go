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

When a free-chat answer is clearer as a table, chart, list, citation, map, or a small set of choices, call present_ui once with a catalog tree. Skip the tool for a plain sentence.

On a client that renders OpenUI, a tree the tool accepts is already shown. Keep the Markdown answer to the conclusion and facts the tree does not contain. Do not repeat an accepted Table, Chart, or List as a Markdown table, chart, or list with the same rows. Citation, Map, Image, Svg, Markdown, and Actions nodes do not replace the prose around them.

Telegram, CLI, TUI, and any channel that does not render the catalog never shows the tree. Those channels still need the full Markdown answer, including the table. The tool result says which case this call is.

The tree is a JSON object: tree_id, state, nodes. Each node has id, type, and props. Allowed types and required props:

- Table: columns, rows
- Chart: kind, series. kind is bar|line|area|pie|scatter. series[].kind may differ so one chart mixes those kinds; missing or Chinese/unknown follows the node kind. pie cannot mix with cartesian — the client then draws pie only. Optional series[].yAxis "right". Optional range / brush crops already present points; dragging does not call the model. When the user applies, they re-ask via ui_action with values.window, optional kind, and optional start (brushed mid-span).
- List: items
- Actions: items (choices only; a write still goes through the existing approval flow). A choice is ui_action; the tree stays so the user can choose again.
- Image: src
- Svg: markup
- Citation: source
- Map: points
- Markdown: text

state is complete, or waiting_input when Actions needs a choice. Do not invent Box, Row, Carousel, AppBlock, or HTML as the tree.`

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
		Description:  "Show a catalog OpenUI tree in free chat. Use it for a table, chart, list, citation, map, or choices. On an OpenUI client, do not repeat an accepted Table, Chart, or List in Markdown. Channels without the catalog still need that Markdown. Chart kind includes scatter; series.kind may mix; a range stays local until the user applies window, kind, and optional start. Actions stay on the tree after a choice. Pass tree as an object with tree_id, state, and nodes. Unknown components are dropped; an empty tree keeps Markdown only.\n\n" + presentUIPolicy,
		Parameters: map[string]tool.Param{
			"tree": {
				Type:        "object",
				Required:    true,
				Description: "OpenUI delta: tree_id, state (complete or waiting_input), nodes[{id,type,props}].",
			},
		},
		// Source on the execution context selects the receipt. A plain Handler
		// call has no channel, so it keeps the Markdown receipt.
		ContextDetailedHandler: func(exec tool.ExecutionContext, args map[string]any) (tool.ToolCallResult, error) {
			note, err := presentUIResult(args["tree"], exec.Source)
			if err != nil {
				return tool.ToolCallResult{}, err
			}
			return tool.ToolCallResult{Output: note}, nil
		},
		Handler: func(args map[string]any) (string, error) {
			return presentUIResult(args["tree"], "")
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

func presentUIResult(tree any, source string) (string, error) {
	raw, err := presentUIRaw(tree)
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
	note += " " + presentUIFollowUp(source, event.UI.Nodes)
	note += " The tree is not a tool result the user must read."
	// The validated delta rides in a prefix the loop strips before the model sees the result.
	return openUIMetadataKey + ":" + string(encoded) + "\n" + note, nil
}

// presentUIFollowUp tells the model whether the accepted tree is already on
// screen. OpenUI clients must not redraw Table, Chart, or List in Markdown.
// Telegram, CLI, TUI, and any other channel still need that Markdown because
// they never render the catalog.
func presentUIFollowUp(source string, nodes []openui.Node) string {
	if !channelRendersOpenUI(source) {
		return "This channel has no OpenUI. Continue with the Markdown answer, including any table, chart, or list."
	}
	repeated := openUIRepeatedTypes(nodes)
	if len(repeated) == 0 {
		return "The tree is on screen. Continue with the Markdown answer for anything the tree does not show."
	}
	return "The tree is on screen. Do not repeat " + strings.Join(repeated, ", ") + " as Markdown. Keep only the conclusion and facts the tree does not show."
}

func channelRendersOpenUI(source string) bool {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "", "cli", "tui", "telegram", "qq", "qqofficial", "napcat", "onebot", "feishu", "lark", "weixin", "wechat":
		return false
	default:
		// Web, Android, and other app transports deliver ChatEventUIDelta.
		return true
	}
}

func openUIRepeatedTypes(nodes []openui.Node) []string {
	seen := map[string]bool{}
	var types []string
	for _, node := range nodes {
		if node.Type != "Table" && node.Type != "Chart" && node.Type != "List" {
			continue
		}
		if seen[node.Type] {
			continue
		}
		seen[node.Type] = true
		types = append(types, node.Type)
	}
	return types
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
