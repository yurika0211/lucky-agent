package agent

import (
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

const childToolCatalog = "file_read,file_list,file_write,terminal,subagent,autonomy,delegate_parallel,delegate_to_skill,delegate_to_mcp"

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
	a := &Agent{delegate: tool.NewDelegateManager(limits)}
	cfg, clean, err := a.childLoopForDelegate(limits, false, "plain context", "")
	if err != nil {
		t.Fatal(err)
	}
	if clean != "plain context" || cfg.RestrictTools || cfg.subagentModel != "" {
		t.Fatalf("untyped delegate changed the child loop: %+v clean=%q", cfg, clean)
	}
	want := buildDelegateChildLoopConfig(limits, false)
	if strings.Join(cfg.DisabledTools, ",") != strings.Join(want.DisabledTools, ",") {
		t.Fatalf("disabled = %v, want %v", cfg.DisabledTools, want.DisabledTools)
	}
	schema := childToolSchemaNames(strings.Split(childToolCatalog, ","), cfg)
	joined := "," + strings.Join(schema, ",") + ","
	if strings.Contains(joined, ",file_write,") || strings.Contains(joined, ",subagent,") || !strings.Contains(joined, ",file_read,") || !strings.Contains(joined, ",terminal,") {
		t.Fatalf("default child schema changed: %v", schema)
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
	writeRoleFile(t, filepath.Join(workspace, ".luckyagent", "subagents"), "wide.md", "---\nname: wide\ntools: file_read, subagent, autonomy, delegate_parallel, delegate_to_skill, delegate_to_mcp\n---\n")
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
