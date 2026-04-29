package tool

import (
	"os"
	"path/filepath"
	"testing"
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
