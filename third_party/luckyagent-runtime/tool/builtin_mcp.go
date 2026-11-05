package tool

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yurika0211/luckyagent-runtime/mcpclient"
)

// MCPAdapter translates catalogs and results; connection ownership stays with
// the host's mcpclient.Manager. It can also wrap another Caller implementation.
type MCPAdapter struct {
	registry *Registry
	caller   mcpclient.Caller
}

func NewMCPAdapter(registry *Registry, caller mcpclient.Caller) *MCPAdapter {
	return &MCPAdapter{registry: registry, caller: caller}
}

func MCPToolName(server, name string) string {
	sum := sha256.Sum256([]byte(server + "\x00" + name))
	label := ToOpenAIName(server + "_" + name)
	if len(label) > 42 {
		label = label[:42]
	}
	return fmt.Sprintf("mcp_%s_%x", label, sum[:8])
}

func (a *MCPAdapter) Sync(catalog []mcpclient.ToolDescriptor) error {
	tools := make([]*Tool, 0, len(catalog))
	for _, descriptor := range catalog {
		d := descriptor
		var schema map[string]any
		if err := json.Unmarshal(d.InputSchema, &schema); err != nil || schema == nil {
			return fmt.Errorf("invalid MCP input schema for %s", d.Ref.Name)
		}
		tools = append(tools, &Tool{
			Name: MCPToolName(d.Ref.Server, d.Ref.Name), Description: d.Description,
			InputSchema: schema, Category: CatMCP, Source: d.Ref.Server, Permission: PermApprove,
			// Registry.Call is the legacy text-only entry point and does not
			// dispatch ContextDetailedHandler in the released tool module.
			Handler: func(args map[string]any) (string, error) {
				result, err := a.caller.CallTool(context.Background(), d.Ref, args)
				if err != nil {
					return "", err
				}
				adapted, err := mcpToolResult(result)
				return adapted.Output, err
			},
			ContextDetailedHandler: func(exec ExecutionContext, args map[string]any) (ToolCallResult, error) {
				result, err := a.caller.CallTool(exec.Context, d.Ref, args)
				if err != nil {
					return ToolCallResult{}, err
				}
				return mcpToolResult(result)
			},
		})
	}
	return a.registry.ReplaceCategory(CatMCP, tools)
}

func mcpToolResult(result mcpclient.CallResult) (ToolCallResult, error) {
	out := ToolCallResult{Metadata: map[string]any{"mcp_is_error": result.IsError}}
	// Retain protocol content and metadata even when the model adapter only
	// understands text and images. Keep structured data in the metadata too.
	var wire any
	if len(result.Raw) > 0 {
		if err := json.Unmarshal(result.Raw, &wire); err != nil {
			return out, err
		}
	} else {
		wire = result
	}
	out.Metadata["mcp_result"] = wire
	var text []string
	var other []json.RawMessage
	for _, raw := range result.Content {
		var block struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			Data     string `json:"data"`
			MimeType string `json:"mimeType"`
		}
		if err := json.Unmarshal(raw, &block); err != nil {
			return out, err
		}
		switch block.Type {
		case "text":
			text = append(text, block.Text)
		case "image":
			data, err := base64.StdEncoding.DecodeString(block.Data)
			if err != nil {
				return out, fmt.Errorf("invalid MCP image data")
			}
			out.Observations = append(out.Observations, Observation{Kind: "mcp_image", ImageData: data, MimeType: block.MimeType})
		default:
			other = append(other, raw)
		}
	}
	if len(result.StructuredContent) > 0 {
		text = append(text, string(result.StructuredContent))
	}
	for _, raw := range other {
		text = append(text, string(raw))
	}
	if len(out.Observations) > 0 {
		text = append(text, fmt.Sprintf("[MCP returned %d image(s)]", len(out.Observations)))
	}
	out.Output = strings.Join(text, "\n")
	if result.IsError {
		return out, fmt.Errorf("MCP tool reported an error: %s", out.Output)
	}
	return out, nil
}
