package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yurika0211/luckyagent-runtime/config"
	"github.com/yurika0211/luckyagent-runtime/provider"
	"github.com/yurika0211/luckyagent-runtime/tool"
)

func TestExploreBuiltinSchemaExcludesWriteTools(t *testing.T) {
	workspace := t.TempDir()
	registered := []string{"file_read", "file_list", "rag_search", "memory", "web_search", "web_fetch", "file_write", "file_patch", "file_delete", "terminal", "subagent"}
	a := &Agent{tools: roleRegistry(registered...)}
	cfg, err := a.resolveSubagentRole("explore", workspace, false)
	if err != nil {
		t.Fatal(err)
	}
	schema := childToolSchemaNames(registered, cfg)
	assertNamedTools(t, schema, exploreReadTools, exploreWriteTools)
	if !strings.Contains(cfg.subagentRolePrompt, "Paths") || !strings.Contains(cfg.subagentRolePrompt, "Gaps") {
		t.Fatalf("explore prompt missing required sections: %s", cfg.subagentRolePrompt)
	}
	guard := configuredTurnToolGuard("look", cfg)
	for _, name := range []string{"file_write", "file_patch", "terminal"} {
		if _, blocked := guard.blockMessage(provider.ToolCall{Name: name}); !blocked {
			t.Fatalf("%s was callable on explore", name)
		}
	}
	if cfg.MaxIterations < 8 || cfg.MaxIterations > 12 {
		t.Fatalf("explore max_iterations = %d, want 8-12", cfg.MaxIterations)
	}
}

func TestPlanBuiltinIsReadOnly(t *testing.T) {
	workspace := t.TempDir()
	registered := []string{"file_read", "file_list", "rag_search", "memory", "web_search", "web_fetch", "file_write", "terminal"}
	a := &Agent{tools: roleRegistry(registered...)}
	cfg, err := a.resolveSubagentRole("plan", workspace, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cfg.subagentRolePrompt, "change plan") && !strings.Contains(cfg.subagentRolePrompt, "Do not edit") {
		t.Fatalf("plan prompt = %s", cfg.subagentRolePrompt)
	}
	schema := childToolSchemaNames(registered, cfg)
	if extra := extraNames(schema, []string{"file_write", "terminal"}); len(extra) > 0 {
		t.Fatalf("plan schema includes %v: %v", extra, schema)
	}
}

func TestExploreThoroughnessRejectsInvalidPlanAndUntyped(t *testing.T) {
	workspace := t.TempDir()
	registered := []string{"file_read", "file_list", "rag_search", "memory", "web_search", "web_fetch", "file_write", "terminal"}
	registry := roleRegistry(registered...)
	created := 0
	registry.Register(&tool.Tool{
		Name: "subagent",
		ContextDetailedHandler: func(tool.ExecutionContext, map[string]any) (tool.ToolCallResult, error) {
			created++
			return tool.ToolCallResult{Output: "delegated"}, nil
		},
	})
	a := &Agent{tools: registry}
	a.installSubagentDelegateGuard()
	sub, _ := registry.Get("subagent")
	if !strings.Contains(sub.Parameters["thoroughness"].Description, "explore") {
		t.Fatalf("thoroughness was not advertised: %+v", sub.Parameters["thoroughness"])
	}
	cases := []map[string]any{
		{"action": "delegate", "description": "look", "subagent_type": "explore", "thoroughness": "deep"},
		{"action": "delegate", "description": "plan it", "subagent_type": "plan", "thoroughness": "quick"},
		{"action": "delegate", "description": "just do it", "thoroughness": "medium"},
	}
	for _, args := range cases {
		_, err := sub.ContextDetailedHandler(tool.ExecutionContext{}, args)
		if err == nil {
			t.Fatalf("thoroughness accepted: %#v", args)
		}
	}
	if created != 0 {
		t.Fatalf("rejected thoroughness created %d tasks", created)
	}
	role, err := a.prepareSubagentDelegate(map[string]any{
		"action": "delegate", "subagent_type": "explore", "thoroughness": "very_thorough",
	}, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(role.Prompt, "very thorough") {
		t.Fatalf("thoroughness did not change the prompt: %s", role.Prompt)
	}
	cfg, err := a.loopConfigForRole(role, false)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxIterations != DefaultLoopConfig().MaxIterations && cfg.MaxIterations != 0 {
		base := buildDelegateChildLoopConfig(tool.DefaultDelegateConfig(), false)
		if cfg.MaxIterations != base.MaxIterations {
			t.Fatalf("thoroughness changed max_iterations to %d", cfg.MaxIterations)
		}
	}
}

func TestProjectExploreRoleReplacesBuiltin(t *testing.T) {
	workspace := t.TempDir()
	writeRoleFile(t, filepath.Join(workspace, ".luckyagent", "subagents"), "explore.md", "---\nname: explore\ntools: file_list\n---\nProject explore only lists.\n")
	registered := []string{"file_read", "file_list", "file_write", "terminal"}
	a := &Agent{tools: roleRegistry(registered...)}
	cfg, err := a.resolveSubagentRole("explore", workspace, false)
	if err != nil {
		t.Fatal(err)
	}
	schema := childToolSchemaNames(registered, cfg)
	if strings.Join(schema, ",") != "file_list" {
		t.Fatalf("project explore did not replace builtin: %v", schema)
	}
	if !strings.Contains(cfg.subagentRolePrompt, "Project explore") {
		t.Fatalf("prompt = %s", cfg.subagentRolePrompt)
	}
	limits := tool.DefaultDelegateConfig()
	limits.ChildDisabledTools = []string{"file_list"}
	a.delegate = tool.NewDelegateManager(limits)
	if _, err := a.resolveSubagentRole("explore", workspace, false); err == nil {
		t.Fatal("project explore re-enabled a disabled tool")
	}
}

func TestBuiltinAgentsDisabledRejectsExplore(t *testing.T) {
	home := t.TempDir()
	cfg, err := config.NewManagerWithDir(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.json"), []byte("{\"delegate\":{\"builtin_agents\":false}}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	registered := []string{"file_read", "file_list", "file_write", "terminal"}
	registry := roleRegistry(registered...)
	created := 0
	registry.Register(&tool.Tool{
		Name: "subagent",
		ContextDetailedHandler: func(tool.ExecutionContext, map[string]any) (tool.ToolCallResult, error) {
			created++
			return tool.ToolCallResult{Output: "delegated"}, nil
		},
	})
	a := &Agent{cfg: cfg, tools: registry}
	a.installSubagentDelegateGuard()
	sub, _ := registry.Get("subagent")
	_, err = sub.ContextDetailedHandler(tool.ExecutionContext{}, map[string]any{
		"action": "delegate", "description": "look", "subagent_type": "explore",
	})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("disabled builtins err = %v", err)
	}
	if created != 0 {
		t.Fatalf("unknown builtin created a task")
	}
}

func TestBuiltinAgentsEnabledAllowsExplore(t *testing.T) {
	t.Run("missing key", func(t *testing.T) {
		home := t.TempDir()
		if err := os.WriteFile(filepath.Join(home, "config.json"), []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		assertBuiltinExploreCreates(t, home)
	})
	t.Run("explicit true", func(t *testing.T) {
		home := t.TempDir()
		if err := os.WriteFile(filepath.Join(home, "config.json"), []byte("{\"delegate\":{\"builtin_agents\":true}}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		assertBuiltinExploreCreates(t, home)
	})
}

func TestBuiltinAgentsReloadFollowsConfigFile(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "config.json")
	if err := os.WriteFile(path, []byte("{\"delegate\":{\"builtin_agents\":false}}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a, sub, created := newBuiltinExploreHarness(t, home)
	_, err := sub.ContextDetailedHandler(tool.ExecutionContext{}, map[string]any{
		"action": "delegate", "description": "look", "subagent_type": "explore",
	})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("disabled builtins err = %v", err)
	}
	if *created != 0 {
		t.Fatalf("disabled reload created %d tasks", *created)
	}
	if err := os.WriteFile(path, []byte("{\"delegate\":{\"builtin_agents\":true}}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !a.builtinAgentsEnabled() {
		t.Fatal("reload did not pick up builtin_agents true")
	}
	out, err := sub.ContextDetailedHandler(tool.ExecutionContext{}, map[string]any{
		"action": "delegate", "description": "look", "subagent_type": "explore",
	})
	if err != nil {
		t.Fatalf("enabled reload err = %v", err)
	}
	if *created != 1 {
		t.Fatalf("enabled reload created %d tasks, output=%q", *created, out.Output)
	}
}

func assertBuiltinExploreCreates(t *testing.T, home string) {
	t.Helper()
	a, sub, created := newBuiltinExploreHarness(t, home)
	if !a.builtinAgentsEnabled() {
		t.Fatal("builtin_agents should be on")
	}
	cfg, err := a.resolveSubagentRole("explore", t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	schema := childToolSchemaNames([]string{"file_read", "file_list", "rag_search", "memory", "web_search", "web_fetch", "file_write", "terminal"}, cfg)
	assertNamedTools(t, schema, []string{"file_read", "file_list"}, []string{"file_write", "terminal", "subagent"})
	out, err := sub.ContextDetailedHandler(tool.ExecutionContext{}, map[string]any{
		"action": "delegate", "description": "look", "subagent_type": "explore",
	})
	if err != nil {
		t.Fatalf("enabled builtins err = %v", err)
	}
	if *created != 1 {
		t.Fatalf("enabled builtins created %d tasks, output=%q", *created, out.Output)
	}
}

func newBuiltinExploreHarness(t *testing.T, home string) (*Agent, *tool.Tool, *int) {
	t.Helper()
	cfg, err := config.NewManagerWithDir(home)
	if err != nil {
		t.Fatal(err)
	}
	registered := []string{"file_read", "file_list", "file_write", "terminal"}
	registry := roleRegistry(registered...)
	created := 0
	registry.Register(&tool.Tool{
		Name: "subagent",
		ContextDetailedHandler: func(tool.ExecutionContext, map[string]any) (tool.ToolCallResult, error) {
			created++
			return tool.ToolCallResult{Output: "delegated"}, nil
		},
	})
	a := &Agent{cfg: cfg, tools: registry}
	a.installSubagentDelegateGuard()
	sub, ok := registry.Get("subagent")
	if !ok {
		t.Fatal("subagent missing")
	}
	return a, sub, &created
}

