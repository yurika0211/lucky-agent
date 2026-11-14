package agent

import (
	"context"
	"testing"

	"github.com/yurika0211/luckyagent-runtime/sandbox"
	"github.com/yurika0211/luckyagent-runtime/tool"
)

func TestSandboxModeArgStaysOffMCPAndPluginTools(t *testing.T) {
	var mcpArgs, pluginArgs, fileArgs map[string]any
	registry := tool.NewRegistry()
	registry.Register(&tool.Tool{
		Name: "mcp_memory_queue", Enabled: true, Permission: tool.PermAuto, Category: tool.CatMCP,
		Handler: func(args map[string]any) (string, error) {
			mcpArgs = cloneArgs(args)
			return "queued", nil
		},
	})
	registry.Register(&tool.Tool{
		Name: "plugin_fixture_check_echo", Enabled: true, Permission: tool.PermAuto, Category: pluginCategory,
		Handler: func(args map[string]any) (string, error) {
			pluginArgs = cloneArgs(args)
			return "echoed", nil
		},
	})
	registry.Register(&tool.Tool{
		Name: "file_read", Enabled: true, Permission: tool.PermAuto, Category: tool.CatBuiltin,
		Handler: func(args map[string]any) (string, error) {
			fileArgs = cloneArgs(args)
			return "ok", nil
		},
	})
	a := &Agent{tools: registry, gateway: tool.NewGateway(registry)}
	ctx := sandbox.WithSnapshot(context.Background(), &sandbox.Snapshot{Mode: sandbox.ModeDev, Root: t.TempDir()})

	if _, err := a.executeToolWithSessionDetailedContext(ctx, "mcp_memory_queue", `{"query":"notes"}`, true, nil, "cli"); err != nil {
		t.Fatal(err)
	}
	if _, ok := mcpArgs["_sandbox_mode"]; ok {
		t.Fatalf("MCP args kept host metadata: %#v", mcpArgs)
	}
	if mcpArgs["query"] != "notes" {
		t.Fatalf("MCP model args changed: %#v", mcpArgs)
	}

	if _, err := a.executeToolWithSessionDetailedContext(ctx, "plugin_fixture_check_echo", `{"value":"x"}`, true, nil, "cli"); err != nil {
		t.Fatal(err)
	}
	if _, ok := pluginArgs["_sandbox_mode"]; ok {
		t.Fatalf("plugin args kept host metadata: %#v", pluginArgs)
	}

	if _, err := a.executeToolWithSessionDetailedContext(ctx, "file_read", `{"path":"notes.md"}`, true, nil, "cli"); err != nil {
		t.Fatal(err)
	}
	if fileArgs["_sandbox_mode"] != "dev" {
		t.Fatalf("filesystem tool lost sandbox mode: %#v", fileArgs)
	}
	if fileArgs["path"] != "notes.md" {
		t.Fatalf("filesystem model args changed: %#v", fileArgs)
	}
}

func TestSandboxModeArgIsStrippedWhenModelSmugglesHostKeys(t *testing.T) {
	var got map[string]any
	registry := tool.NewRegistry()
	registry.Register(&tool.Tool{
		Name: "mcp_memory_search", Enabled: true, Permission: tool.PermAuto, Category: tool.CatMCP,
		Handler: func(args map[string]any) (string, error) {
			got = cloneArgs(args)
			return "ok", nil
		},
	})
	a := &Agent{tools: registry, gateway: tool.NewGateway(registry)}
	ctx := sandbox.WithSnapshot(context.Background(), &sandbox.Snapshot{Mode: sandbox.ModeDev, Root: t.TempDir()})
	if _, err := a.executeToolWithSessionDetailedContext(ctx, "mcp_memory_search", `{"query":"notes","_sandbox_mode":"dev","_cwd":"/tmp"}`, true, nil, "cli"); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["_sandbox_mode"]; ok {
		t.Fatalf("smuggled sandbox mode reached MCP: %#v", got)
	}
	if _, ok := got["_cwd"]; ok {
		t.Fatalf("smuggled host cwd reached MCP: %#v", got)
	}
	if got["query"] != "notes" {
		t.Fatalf("MCP model args changed: %#v", got)
	}
}

func cloneArgs(args map[string]any) map[string]any {
	if args == nil {
		return nil
	}
	out := make(map[string]any, len(args))
	for key, value := range args {
		out[key] = value
	}
	return out
}
