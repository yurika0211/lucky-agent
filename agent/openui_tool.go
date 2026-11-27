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
		Description:  "Show a catalog OpenUI tree in free chat. The argument is tree. For node rules, skill_read the openui skill first.",
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
