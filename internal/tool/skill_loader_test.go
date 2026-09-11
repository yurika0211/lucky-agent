package tool

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSkillLoaderLoad(t *testing.T) {
	// 创建临时 skill 目录
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "test-skill")
	os.MkdirAll(skillDir, 0755)

	skillContent := "# Test Skill\n\nThis is a test skill for unit testing.\n\n## Tools\n\n- `search`: Search for information\n- `analyze`: Analyze data\n- **format**: Format output\n"
	skillFile := filepath.Join(skillDir, "SKILL.md")
	os.WriteFile(skillFile, []byte(skillContent), 0644)

	loader := NewSkillLoader(tmpDir)
	info, err := loader.Load(skillFile)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if info.Name != "test_skill" {
		t.Errorf("expected name 'test_skill', got %s", info.Name)
	}
	if !info.Available {
		t.Error("skill should be available")
	}
	if len(info.Tools) < 2 {
		t.Errorf("expected at least 2 tools, got %d", len(info.Tools))
	}
}

func TestSkillLoaderLoadAll(t *testing.T) {
	tmpDir := t.TempDir()

	// 创建两个 skill
	for _, name := range []string{"skill-a", "skill-b"} {
		skillDir := filepath.Join(tmpDir, name)
		os.MkdirAll(skillDir, 0755)
		os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# "+name+"\n\nDesc.\n"), 0644)
	}

	loader := NewSkillLoader(tmpDir)
	skills, err := loader.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(skills) != 2 {
		t.Errorf("expected 2 skills, got %d", len(skills))
	}
}

func TestSkillLoaderPreservesFrontmatterNameAndAddsAliases(t *testing.T) {
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "research")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	skillContent := `---
name: web-search
description: Search the web
---

# Web Search — 联网搜索总入口
`
	skillFile := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillFile, []byte(skillContent), 0644); err != nil {
		t.Fatalf("write skill: %v", err)
	}

	loader := NewSkillLoader(tmpDir)
	info, err := loader.Load(skillFile)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if info.Name != "web-search" {
		t.Fatalf("expected frontmatter name to win, got %q", info.Name)
	}
	if len(info.Aliases) == 0 {
		t.Fatal("expected aliases to be captured")
	}

	foundTitleAlias := false
	for _, alias := range info.Aliases {
		if alias == "web_search___联网搜索总入口" {
			foundTitleAlias = true
		}
	}
	if !foundTitleAlias {
		t.Fatalf("expected title alias in aliases, got %v", info.Aliases)
	}
}

func TestSkillLoaderNoSkillsDir(t *testing.T) {
	loader := NewSkillLoader("/nonexistent/path")
	_, err := loader.LoadAll()
	if err == nil {
		t.Error("expected error for nonexistent dir")
	}
}

func TestRegisterSkillTools(t *testing.T) {
	r := NewRegistry()

	skills := []*SkillInfo{
		{
			Name: "web-search",
			Tools: []SkillToolDef{
				{Name: "search", Description: "Search the web"},
				{Name: "news", Description: "Get news"},
			},
			Available: true,
		},
	}

	RegisterSkillTools(r, skills, nil)

	if r.Count() != 2 {
		t.Errorf("expected 2 skill tools, got %d", r.Count())
	}

	tool, ok := r.Get("skill_web-search_search")
	if !ok {
		t.Error("skill tool not found")
	}
	if tool.Category != CatSkill {
		t.Errorf("expected CatSkill, got %s", tool.Category)
	}
	if tool.Source != "web-search" {
		t.Errorf("expected source=web-search, got %s", tool.Source)
	}
	if !tool.HiddenFromModel {
		t.Error("expected non-run skill tool to be hidden from model")
	}
}

func TestRegisterSkillToolsWithHandler(t *testing.T) {
	r := NewRegistry()

	skills := []*SkillInfo{
		{
			Name: "test",
			Tools: []SkillToolDef{
				{Name: "echo", Description: "Echo"},
			},
			Available: true,
		},
	}

	handler := func(toolName string, skillDir string) func(args map[string]any) (string, error) {
		return func(args map[string]any) (string, error) {
			return "handled: " + toolName, nil
		}
	}

	RegisterSkillTools(r, skills, handler)

	result, err := r.Call("skill_test_echo", nil)
	if err != nil {
		t.Fatalf("call skill tool: %v", err)
	}
	if result != "handled: echo" {
		t.Errorf("expected 'handled: echo', got %s", result)
	}
	tool, ok := r.Get("skill_test_echo")
	if !ok {
		t.Fatal("expected registered tool")
	}
	if !tool.HiddenFromModel {
		t.Error("expected non-run skill tool to be hidden from model")
	}
}

func TestParseToolEntry(t *testing.T) {
	tests := []struct {
		line        string
		expectName  string
		expectEmpty bool
	}{
		{"`search`: Search the web", "search", false},
		{"**format**: Format output", "format", false},
		{"analyze - Analyze data", "analyze", false},
		{"no tool here", "", true},
	}

	for _, tt := range tests {
		name, _ := parseToolEntry(tt.line)
		if tt.expectEmpty && name != "" {
			t.Errorf("expected empty name for %q, got %q", tt.line, name)
		}
		if !tt.expectEmpty && name != tt.expectName {
			t.Errorf("expected name %q for %q, got %q", tt.expectName, tt.line, name)
		}
	}
}

func TestSkillLoaderAutoGenerateToolsFromScripts(t *testing.T) {
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "script-skill")
	scriptsDir := filepath.Join(skillDir, "scripts")
	if err := os.MkdirAll(scriptsDir, 0755); err != nil {
		t.Fatalf("mkdir scripts: %v", err)
	}

	// 不提供 Tools section，触发 autoGenerateTools
	skillContent := "# Script Skill\n\nSkill with script only.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillContent), 0644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(scriptsDir, "calc.sh"), []byte("#!/bin/sh\necho ok\n"), 0755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	loader := NewSkillLoader(tmpDir)
	skills, err := loader.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(skills))
	}

	var hasRun, hasCalc bool
	for _, tool := range skills[0].Tools {
		if tool.Name == "run" {
			hasRun = true
		}
		if tool.Name == "calc" {
			hasCalc = true
		}
	}
	if !hasRun {
		t.Fatal("expected auto-generated run tool")
	}
	if !hasCalc {
		t.Fatal("expected auto-generated script tool 'calc'")
	}
}

func TestSkillLoaderDoesNotGenerateRunToolForDocOnlySkill(t *testing.T) {
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "doc-only")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# Doc Only\n\nJust docs.\n"), 0644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	loader := NewSkillLoader(tmpDir)
	info, err := loader.Load(filepath.Join(skillDir, "SKILL.md"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(info.Tools) != 0 {
		t.Fatalf("expected doc-only skill to expose no tools, got %d", len(info.Tools))
	}
}

func TestParseCLIHelpCommands(t *testing.T) {
	help := `usage: blog_api.py [-h] {health,login,list-posts} ...

positional arguments:
  {health,login,list-posts}
    health              GET /health
    login               POST /login
    list-posts          GET /posts/

options:
  -h, --help            show this help message and exit
`
	cmds := parseCLIHelpCommands(help)
	if len(cmds) != 3 {
		t.Fatalf("expected 3 commands, got %d", len(cmds))
	}
	if cmds[0].Name != "health" || cmds[1].Name != "login" || cmds[2].Name != "list-posts" {
		t.Fatalf("unexpected commands: %#v", cmds)
	}
}

func TestCLICommandToToolDef(t *testing.T) {
	def := cliCommandToToolDef([]string{"posts", "list"}, "List posts.")
	if def.Name != "posts_list" {
		t.Fatalf("expected posts_list, got %q", def.Name)
	}
	if len(def.Command) != 2 || def.Command[0] != "posts" || def.Command[1] != "list" {
		t.Fatalf("unexpected command path: %#v", def.Command)
	}
	if !def.ExposeToModel {
		t.Fatal("expected generated CLI tool to be model visible")
	}
	if _, ok := def.Parameters["args"]; !ok {
		t.Fatal("expected args parameter")
	}
}

func TestBuildSkillScriptCommandWindowsAvoidsBinSh(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-specific test")
	}

	cmd, err := buildSkillScriptCommand(`C:\tmp\skill\scripts\run.sh`)
	if err != nil {
		t.Fatalf("buildSkillScriptCommand: %v", err)
	}
	if len(cmd) == 0 {
		t.Fatal("expected non-empty command")
	}
	if cmd[0] == "/bin/sh" {
		t.Fatalf("expected windows command not to use /bin/sh, got %#v", cmd)
	}
}

// writeProbeSkill creates a skill whose scripts/ holds exactly one file, which is
// the shape that triggers the `--help` probe in autoGenerateTools.
func writeProbeSkill(t *testing.T, root, name, script string) string {
	t.Helper()
	skillDir := filepath.Join(root, name)
	scriptsDir := filepath.Join(skillDir, "scripts")
	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# "+name+"\n\nDesc.\n"), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(scriptsDir, "cli.sh"), []byte(script), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return filepath.Join(skillDir, "SKILL.md")
}

// TestSkillLoaderCLIInspectDisabled checks the parse-only mode: with the probe
// off, loading must not execute the skill's script at all.
func TestSkillLoaderCLIInspectDisabled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script fixture is POSIX-only")
	}
	tmpDir := t.TempDir()
	marker := filepath.Join(tmpDir, "executed.marker")
	mdPath := writeProbeSkill(t, tmpDir, "probe-skill",
		"#!/bin/sh\ntouch "+marker+"\necho 'positional arguments:'\necho '  list   List things'\n")

	loader := NewSkillLoader(tmpDir).WithCLIInspect(false, 0)
	if _, err := loader.Load(mdPath); err != nil {
		t.Fatalf("Load: %v", err)
	}

	if _, err := os.Stat(marker); err == nil {
		t.Error("script was executed even though CLI inspection is disabled")
	}
}

// TestSkillLoaderCLIInspectTimeout guards the unbounded-hang case: the probe used
// to run exec.Command with no deadline, so one wedged --help blocked startup
// forever.
func TestSkillLoaderCLIInspectTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script fixture is POSIX-only")
	}
	tmpDir := t.TempDir()
	mdPath := writeProbeSkill(t, tmpDir, "hang-skill", "#!/bin/sh\nsleep 60\n")

	loader := NewSkillLoader(tmpDir).WithCLIInspect(true, 300*time.Millisecond)

	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := loader.Load(mdPath); err != nil {
			t.Errorf("Load: %v", err)
		}
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Load did not return; the --help probe is unbounded")
	}
}

// TestSkillLoaderProbeEnvIsScrubbed checks that a replaced probe environment
// keeps the parent's credentials away from unvetted skill code.
func TestSkillLoaderProbeEnvIsScrubbed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script fixture is POSIX-only")
	}
	t.Setenv("LH_TEST_FAKE_SECRET", "topsecretvalue")

	tmpDir := t.TempDir()
	// The constant "carrier" keeps the help line matching the parser even when the
	// variable expands to nothing, so the assertion below cannot pass vacuously.
	script := "#!/bin/sh\necho 'positional arguments:'\necho \"  leak   carrier $LH_TEST_FAKE_SECRET\"\n"
	mdPath := writeProbeSkill(t, tmpDir, "env-skill", script)

	loader := NewSkillLoader(tmpDir).
		WithCLIInspect(true, 5*time.Second).
		WithProbeEnv([]string{"PATH=" + os.Getenv("PATH")})

	info, err := loader.Load(mdPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	var probed *SkillToolDef
	for i := range info.Tools {
		if strings.Contains(info.Tools[i].Description, "carrier") {
			probed = &info.Tools[i]
			break
		}
	}
	if probed == nil {
		t.Fatalf("probe produced no tool from the --help output; test would pass vacuously. tools=%+v", info.Tools)
	}
	if strings.Contains(probed.Description, "topsecretvalue") {
		t.Fatalf("probe leaked a parent env var into tool %q: %s", probed.Name, probed.Description)
	}
}
