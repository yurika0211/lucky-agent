package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yurika0211/luckyagent-runtime/provider"
	"github.com/yurika0211/luckyagent-runtime/tool"
)

func writeRoleFile(t *testing.T, dir, filename, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, filename), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func roleRegistry(names ...string) *tool.Registry {
	registry := tool.NewRegistry()
	for _, name := range names {
		registry.Register(&tool.Tool{Name: name, Handler: func(map[string]any) (string, error) {
			return "ok", nil
		}})
	}
	return registry
}

const childToolCatalog = "file_read,file_list,file_write,terminal,subagent,autonomy,delegate_parallel,delegate_to_skill,delegate_to_mcp,wait_for_tasks,rag_search,memory,web_search,web_fetch"

func TestSubagentRoleProjectOverridesUser(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	writeRoleFile(t, filepath.Join(home, "subagents"), "reader.md", "---\nname: reader\ntools: file_read\nmodel: user-model\n---\nuser prompt\n")
	writeRoleFile(t, filepath.Join(workspace, ".luckyagent", "subagents"), "local.md", "---\nname: reader\ntools: file_list\nmodel: project-model\n---\nproject prompt\n")
	role, err := loadSubagentRole(home, workspace, "reader")
	if err != nil {
		t.Fatal(err)
	}
	if role.Model != "project-model" || !strings.Contains(role.Prompt, "project prompt") || strings.Join(role.Tools, ",") != "file_list" {
		t.Fatalf("project role did not replace the user role: %+v", role)
	}
}

func TestSubagentRoleMissingName(t *testing.T) {
	home := t.TempDir()
	writeRoleFile(t, filepath.Join(home, "subagents"), "blank.md", "---\ndescription: no name\n---\n")
	if _, err := loadSubagentRole(home, t.TempDir(), "blank"); err == nil {
		t.Fatal("role file without name loaded")
	}
}

func TestLoopConfigForRoleWhitelistDropsWriteTools(t *testing.T) {
	workspace := t.TempDir()
	writeRoleFile(t, filepath.Join(workspace, ".luckyagent", "subagents"), "reader.md", "---\nname: reader\ntools: file_read, file_list\n---\nRead only.\n")
	a := &Agent{tools: roleRegistry(strings.Split(childToolCatalog, ",")...)}
	cfg, err := a.resolveSubagentRole("reader", workspace, false)
	if err != nil {
		t.Fatal(err)
	}
	schema := childToolSchemaNames(strings.Split(childToolCatalog, ","), cfg)
	joined := "," + strings.Join(schema, ",") + ","
	if !strings.Contains(joined, ",file_read,") || !strings.Contains(joined, ",file_list,") {
		t.Fatalf("whitelist missing reads: %v", schema)
	}
	for _, banned := range []string{"file_write", "terminal", "subagent"} {
		if strings.Contains(joined, ","+banned+",") {
			t.Fatalf("%s remained in the child schema: %v", banned, schema)
		}
		guard := configuredTurnToolGuard("read", cfg)
		if msg, blocked := guard.blockMessage(provider.ToolCall{Name: banned, Arguments: "{}"}); !blocked || strings.Contains(msg, "ok") {
			t.Fatalf("%s was available: blocked=%v msg=%q", banned, blocked, msg)
		}
	}
}

func TestLoopConfigForRoleGlobalDisableIsAFloor(t *testing.T) {
	workspace := t.TempDir()
	writeRoleFile(t, filepath.Join(workspace, ".luckyagent", "subagents"), "reader.md", "---\nname: reader\ntools: file_read, file_list\n---\n")
	limits := tool.DefaultDelegateConfig()
	limits.ChildDisabledTools = []string{"file_read"}
	a := &Agent{delegate: tool.NewDelegateManager(limits), tools: roleRegistry(strings.Split(childToolCatalog, ",")...)}
	cfg, err := a.resolveSubagentRole("reader", workspace, false)
	if err != nil {
		t.Fatal(err)
	}
	schema := childToolSchemaNames(strings.Split(childToolCatalog, ","), cfg)
	for _, name := range schema {
		if name == "file_read" || name == "file_write" || name == "terminal" {
			t.Fatalf("role re-enabled or kept %s: %v", name, schema)
		}
	}
	if len(schema) != 1 || schema[0] != "file_list" {
		t.Fatalf("schema = %v, want [file_list]", schema)
	}
	guard := configuredTurnToolGuard("", cfg)
	if _, blocked := guard.blockMessage(provider.ToolCall{Name: "file_read"}); !blocked {
		t.Fatal("globally disabled file_read was callable")
	}
}

func TestLoopConfigForRoleWithoutTypeMatchesToday(t *testing.T) {
	limits := tool.DefaultDelegateConfig()
	limits.ChildDisabledTools = []string{"file_write"}
	registered := strings.Split(childToolCatalog, ",")
	a := &Agent{delegate: tool.NewDelegateManager(limits), tools: roleRegistry(registered...)}
	cfg, clean, err := a.childLoopForDelegate(limits, false, "plain context", "")
	if err != nil {
		t.Fatal(err)
	}
	if clean != "plain context" || !cfg.RestrictTools || cfg.ProfileSource != "subagent_role:explore" {
		t.Fatalf("untyped delegate did not use explore: %+v clean=%q", cfg, clean)
	}
	schema := childToolSchemaNames(registered, cfg)
	joined := "," + strings.Join(schema, ",") + ","
	for _, name := range []string{"file_read", "file_list", "rag_search", "memory", "web_search", "web_fetch"} {
		if !strings.Contains(joined, ","+name+",") {
			t.Fatalf("explore schema missing %s: %v", name, schema)
		}
	}
	for _, name := range []string{"terminal", "file_write", "wait_for_tasks", "subagent"} {
		if strings.Contains(joined, ","+name+",") {
			t.Fatalf("untyped schema includes %s: %v", name, schema)
		}
	}
	guard := configuredTurnToolGuard("look", cfg)
	if _, blocked := guard.blockMessage(provider.ToolCall{Name: "wait_for_tasks"}); !blocked {
		t.Fatal("wait_for_tasks was callable on the default explore child")
	}
}

func TestPrepareSubagentUnknownRoleCreatesNoTask(t *testing.T) {
	a := &Agent{tools: roleRegistry("file_read")}
	if _, err := a.prepareSubagentDelegate(map[string]any{"subagent_type": "missing"}, t.TempDir()); err == nil {
		t.Fatal("unknown role was accepted")
	}
}

func TestPrepareSubagentEmptyWhitelistCreatesNoTask(t *testing.T) {
	workspace := t.TempDir()
	writeRoleFile(t, filepath.Join(workspace, ".luckyagent", "subagents"), "empty.md", "---\nname: empty\ntools: file_read\n---\n")
	limits := tool.DefaultDelegateConfig()
	limits.ChildDisabledTools = []string{"file_read"}
	a := &Agent{delegate: tool.NewDelegateManager(limits), tools: roleRegistry("file_read", "file_list")}
	if _, err := a.prepareSubagentDelegate(map[string]any{"subagent_type": "empty"}, workspace); err == nil {
		t.Fatal("empty resolved whitelist was accepted")
	}
	writeRoleFile(t, filepath.Join(workspace, ".luckyagent", "subagents"), "unknown-tools.md", "---\nname: ghost\ntools: not_a_tool\n---\n")
	if _, err := a.prepareSubagentDelegate(map[string]any{"subagent_type": "ghost"}, workspace); err == nil {
		t.Fatal("whitelist of unknown tools was accepted")
	}
}

func TestLoopConfigForRoleKeepsRecursiveToolsClosed(t *testing.T) {
	workspace := t.TempDir()
	writeRoleFile(t, filepath.Join(workspace, ".luckyagent", "subagents"), "wide.md", "---\nname: wide\ntools: file_read, subagent, autonomy, delegate_parallel, delegate_to_skill, delegate_to_mcp, wait_for_tasks\n---\n")
	a := &Agent{delegate: tool.NewDelegateManager(tool.DefaultDelegateConfig()), tools: roleRegistry(strings.Split(childToolCatalog, ",")...)}
	cfg, err := a.resolveSubagentRole("wide", workspace, true)
	if err != nil {
		t.Fatal(err)
	}
	schema := childToolSchemaNames(strings.Split(childToolCatalog, ","), cfg)
	if len(schema) != 1 || schema[0] != "file_read" {
		t.Fatalf("recursive tools entered the child schema: %v", schema)
	}
	limits := tool.DefaultDelegateConfig()
	limits.ChildAllowRecursive = true
	a.delegate = tool.NewDelegateManager(limits)
	opened, err := a.resolveSubagentRole("wide", workspace, true)
	if err != nil {
		t.Fatal(err)
	}
	openedSchema := childToolSchemaNames(strings.Split(childToolCatalog, ","), opened)
	joined := "," + strings.Join(openedSchema, ",") + ","
	for _, name := range delegateRecursiveToolNames {
		if !strings.Contains(joined, ","+name+",") {
			t.Fatalf("recursive tool %s stayed closed after allow_recursive_delegate: %v", name, openedSchema)
		}
	}
	if strings.Contains(joined, ",wait_for_tasks,") {
		t.Fatalf("wait_for_tasks re-entered a recursive child schema: %v", openedSchema)
	}
}

func TestParallelChildLoopHidesWaitForTasks(t *testing.T) {
	registered := strings.Split(childToolCatalog, ",")
	a := &Agent{tools: roleRegistry(registered...)}
	cfg, _, err := a.childLoopForDelegate(tool.DefaultDelegateConfig(), false, "batch context", "")
	if err != nil {
		t.Fatal(err)
	}
	schema := childToolSchemaNames(registered, cfg)
	joined := "," + strings.Join(schema, ",") + ","
	if strings.Contains(joined, ",wait_for_tasks,") {
		t.Fatalf("parallel child schema includes wait_for_tasks: %v", schema)
	}
	if !containsToolName(cfg.DisabledTools, "wait_for_tasks") {
		t.Fatalf("parallel child DisabledTools = %v", cfg.DisabledTools)
	}
	guard := configuredTurnToolGuard("search the tree", cfg)
	if _, blocked := guard.blockMessage(provider.ToolCall{Name: "wait_for_tasks"}); !blocked {
		t.Fatal("parallel child could call wait_for_tasks")
	}
}

func TestPartialSubagentIterationLimitIsNotFailed(t *testing.T) {
	result, err := partialSubagentLimitResult(LoopConfig{Source: "subagent"}, &LoopResult{
		ToolCalls: []toolCallLog{
			{Name: "file_read", Arguments: `{"path":"agent/loop.go"}`, Result: "package agent"},
			{Name: "file_read", Result: "agent/request_profile.go"},
			{Name: "memory", Result: "saved a note"},
		},
		Response: "Max iterations reached, last response may be incomplete",
	}, fmt.Errorf("max iterations (10) reached"))
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || !strings.Contains(result.Response, "Partial result") || !strings.Contains(result.Response, "Unfinished") {
		t.Fatalf("partial result = %+v", result)
	}
	paths := partialResultSection(result.Response, "Paths:")
	for _, path := range []string{"agent/loop.go", "agent/request_profile.go"} {
		if !strings.Contains(paths, path) {
			t.Fatalf("partial Paths missing %s: %s", path, paths)
		}
	}
	if strings.Contains(paths, "saved a note") {
		t.Fatalf("non-path result was written as a path: %s", paths)
	}
	if !strings.Contains(paths, "memory") {
		t.Fatalf("tool name was dropped when no path was found: %s", paths)
	}
	parent, err := partialSubagentLimitResult(LoopConfig{Source: "user"}, &LoopResult{}, fmt.Errorf("max iterations (10) reached"))
	if err == nil || parent.Response != "" {
		t.Fatalf("parent max-iterations was rewritten: %+v err=%v", parent, err)
	}
}

func partialResultSection(body, heading string) string {
	start := strings.Index(body, heading)
	if start < 0 {
		return ""
	}
	rest := body[start+len(heading):]
	if end := strings.Index(rest, "\n\n"); end >= 0 {
		rest = rest[:end]
	}
	return rest
}

func TestSubagentRoleWhitelistWinsOverDisallow(t *testing.T) {
	workspace := t.TempDir()
	writeRoleFile(t, filepath.Join(workspace, ".luckyagent", "subagents"), "both.md", "---\nname: both\ntools: [file_read, file_list]\ndisallowed_tools: file_read\n---\n")
	a := &Agent{tools: roleRegistry("file_read", "file_list", "file_write")}
	cfg, err := a.resolveSubagentRole("both", workspace, false)
	if err != nil {
		t.Fatal(err)
	}
	schema := childToolSchemaNames([]string{"file_read", "file_list", "file_write"}, cfg)
	if strings.Join(schema, ",") != "file_read,file_list" {
		t.Fatalf("whitelist did not win: %v", schema)
	}
}
