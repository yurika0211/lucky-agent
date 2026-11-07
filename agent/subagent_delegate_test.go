package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yurika0211/luckyagent-runtime/config"
	"github.com/yurika0211/luckyagent-runtime/provider"
	"github.com/yurika0211/luckyagent-runtime/session"
	"github.com/yurika0211/luckyagent-runtime/tool"
)

func TestDelegateRoleGuardRejectsUnknownBeforeTask(t *testing.T) {
	registry := tool.NewRegistry()
	created := 0
	registry.Register(&tool.Tool{
		Name: "subagent",
		ContextDetailedHandler: func(tool.ExecutionContext, map[string]any) (tool.ToolCallResult, error) {
			created++
			return tool.ToolCallResult{Output: `{"task_id":"task-1","status":"running"}`}, nil
		},
	})
	a := &Agent{tools: registry}
	a.installSubagentDelegateGuard()
	sub, ok := registry.Get("subagent")
	if !ok {
		t.Fatal("subagent tool missing")
	}
	if sub.Parameters["subagent_type"].Description == "" || !strings.Contains(sub.Parameters["subagent_type"].Description, "subagents/<name>.md") {
		t.Fatalf("subagent_type was not advertised: %+v", sub.Parameters["subagent_type"])
	}
	_, err := sub.ContextDetailedHandler(tool.ExecutionContext{}, map[string]any{
		"action": "delegate", "description": "look around", "subagent_type": "missing",
	})
	if err == nil || created != 0 {
		t.Fatalf("unknown role created a task: created=%d err=%v", created, err)
	}
}

func TestDelegateRoleGuardLeavesUntypedDelegateUnchanged(t *testing.T) {
	registry := tool.NewRegistry()
	var got map[string]any
	registry.Register(&tool.Tool{
		Name: "subagent",
		ContextDetailedHandler: func(_ tool.ExecutionContext, args map[string]any) (tool.ToolCallResult, error) {
			got = args
			return tool.ToolCallResult{Output: "delegated"}, nil
		},
	})
	a := &Agent{tools: registry}
	a.installSubagentDelegateGuard()
	sub, _ := registry.Get("subagent")
	if _, err := sub.ContextDetailedHandler(tool.ExecutionContext{}, map[string]any{
		"action": "delegate", "description": "do the work", "context": "notes",
	}); err != nil {
		t.Fatal(err)
	}
	if got["context"] != "notes" || got["subagent_type"] != nil {
		t.Fatalf("untyped delegate was rewritten: %#v", got)
	}
}

func TestDelegateRoleModelIsChildOnly(t *testing.T) {
	workspace := t.TempDir()
	writeRoleFile(t, filepath.Join(workspace, ".luckyagent", "subagents"), "reader.md", "---\nname: reader\ntools: file_read\nmodel: review-model\n---\nRead only.\n")
	cfg, err := config.NewManagerWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Set("provider", "openai"); err != nil || cfg.Set("model", "main-model") != nil {
		t.Fatal(err)
	}
	registry := provider.NewRegistry()
	registry.RegisterFactory("openai", func(c provider.Config) provider.Provider {
		return &roleModelTestProvider{model: c.LlmProvider.Model}
	})
	parent := &mockProvider{name: "default-provider"}
	tools := roleRegistry("file_read", "file_write", "terminal")
	a := &Agent{cfg: cfg, registry: registry, provider: parent, activeModel: "main-model", tools: tools}
	resolved, err := a.resolveSubagentRole("reader", workspace, false)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.subagentModel != "review-model" {
		t.Fatalf("child model = %q", resolved.subagentModel)
	}
	snapshot, err := a.childProviderForRole(a.baseProviderSnapshot(), resolved.subagentModel)
	if err != nil {
		t.Fatal(err)
	}
	response, err := snapshot.provider.Chat(context.Background(), nil)
	if err != nil || response.Content != "review-model" || snapshot.model != "review-model" {
		t.Fatalf("child request did not use the role model: response=%+v snapshot=%q err=%v", response, snapshot.model, err)
	}
	if a.baseProviderSnapshot().provider != parent || a.baseProviderSnapshot().model != "main-model" || cfg.Get().Model != "main-model" {
		t.Fatal("role model replaced the parent provider")
	}
	schema := childToolSchemaNames([]string{"file_read", "file_write", "terminal"}, resolved)
	if strings.Join(schema, ",") != "file_read" {
		t.Fatalf("role schema = %v", schema)
	}
}

func TestDelegateRoleGuardStampsRoleBeforeCreate(t *testing.T) {
	workspace := t.TempDir()
	writeRoleFile(t, filepath.Join(workspace, ".luckyagent", "subagents"), "reader.md", "---\nname: reader\ntools: file_read,file_list\nmodel: child-model\n---\nStay narrow.\n")
	sessions, err := session.NewManager(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	parent := sessions.New()
	parent.SetCwd(workspace)
	registry := roleRegistry("file_read", "file_list", "file_write", "terminal")
	var stamped string
	registry.Register(&tool.Tool{
		Name: "subagent",
		ContextDetailedHandler: func(_ tool.ExecutionContext, args map[string]any) (tool.ToolCallResult, error) {
			stamped, _ = args["context"].(string)
			return tool.ToolCallResult{Output: "delegated"}, nil
		},
	})
	a := &Agent{sessions: sessions, tools: registry, delegate: tool.NewDelegateManager(tool.DefaultDelegateConfig())}
	a.installSubagentDelegateGuard()
	sub, _ := registry.Get("subagent")
	if _, err := sub.ContextDetailedHandler(tool.ExecutionContext{SessionID: parent.ID}, map[string]any{
		"action": "delegate", "description": "list files", "subagent_type": "reader",
	}); err != nil {
		t.Fatal(err)
	}
	cfg, clean, err := a.childLoopForDelegate(tool.DefaultDelegateConfig(), false, stamped, "")
	if err != nil {
		t.Fatal(err)
	}
	if clean != "" || cfg.subagentModel != "child-model" || !strings.Contains(cfg.subagentRolePrompt, "Stay narrow.") {
		t.Fatalf("marker did not reach the child loop: clean=%q cfg=%+v", clean, cfg)
	}
	schema := childToolSchemaNames([]string{"file_read", "file_list", "file_write", "terminal"}, cfg)
	if strings.Join(schema, ",") != "file_read,file_list" {
		t.Fatalf("stamped role did not tighten the child schema: %v", schema)
	}
	guard := configuredTurnToolGuard("list files", cfg)
	if _, blocked := guard.blockMessage(provider.ToolCall{Name: "file_write"}); !blocked {
		t.Fatal("file_write succeeded under a read-only role")
	}
	if _, blocked := guard.blockMessage(provider.ToolCall{Name: "terminal"}); !blocked {
		t.Fatal("terminal succeeded under a read-only role")
	}
}
