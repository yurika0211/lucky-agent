package tool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillReadListJSONIncludesMetadata(t *testing.T) {
	svc := NewSkillToolService([]*SkillInfo{
		{
			Name:        "content",
			Description: "Parent hub for multi-stage content workflows.",
			Summary:     "Parent hub for multi-stage content workflows with reusable sub-steps.",
			Dir:         "/tmp/content-skill",
			Aliases:     []string{"content-hub"},
			Available:   true,
		},
	})

	out, err := svc.HandleRead(map[string]any{
		"format": "json",
	})
	if err != nil {
		t.Fatalf("HandleRead list json: %v", err)
	}

	var payload struct {
		Skills []struct {
			Name    string   `json:"name"`
			Summary string   `json:"summary"`
			Aliases []string `json:"aliases"`
		} `json:"skills"`
		Count int `json:"count"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal json: %v", err)
	}
	if payload.Count != 1 || len(payload.Skills) != 1 {
		t.Fatalf("expected 1 skill, got count=%d len=%d", payload.Count, len(payload.Skills))
	}
	if payload.Skills[0].Name != "content" {
		t.Fatalf("expected content skill, got %q", payload.Skills[0].Name)
	}
	if !strings.Contains(payload.Skills[0].Summary, "multi-stage content") {
		t.Fatalf("expected summary in payload, got %q", payload.Skills[0].Summary)
	}
	if len(payload.Skills[0].Aliases) != 1 || payload.Skills[0].Aliases[0] != "content-hub" {
		t.Fatalf("expected aliases in payload, got %#v", payload.Skills[0].Aliases)
	}
}

func TestSkillReadNamedJSONIncludesContentAndPath(t *testing.T) {
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "content")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	skillMD := filepath.Join(skillDir, "SKILL.md")
	skillContent := "# content\n\nParent hub.\n\n## Steps\n\nRun it.\n"
	if err := os.WriteFile(skillMD, []byte(skillContent), 0o644); err != nil {
		t.Fatalf("write skill md: %v", err)
	}

	svc := NewSkillToolService([]*SkillInfo{
		{
			Name:        "content",
			Description: "Parent hub.",
			Summary:     "Parent hub for multi-stage content workflows.",
			Dir:         skillDir,
			Aliases:     []string{"content-hub"},
			Available:   true,
			Tools: []SkillToolDef{
				{Name: "run", Description: "Run the content workflow.", ExposeToModel: true, Command: []string{"run.sh"}},
			},
		},
	})

	out, err := svc.HandleRead(map[string]any{
		"name":   "content",
		"format": "json",
	})
	if err != nil {
		t.Fatalf("HandleRead named json: %v", err)
	}

	var payload struct {
		Found       bool   `json:"found"`
		Name        string `json:"name"`
		Dir         string `json:"dir"`
		SkillMDPath string `json:"skill_md_path"`
		Content     struct {
			Text       string `json:"text"`
			Truncated  bool   `json:"truncated"`
			TotalChars int    `json:"total_chars"`
		} `json:"content"`
		Tools []struct {
			Name    string `json:"name"`
			Command string `json:"command"`
		} `json:"tools"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal json: %v", err)
	}
	if !payload.Found {
		t.Fatal("expected found=true")
	}
	if payload.Dir != skillDir {
		t.Fatalf("expected dir %q, got %q", skillDir, payload.Dir)
	}
	if payload.SkillMDPath != skillMD {
		t.Fatalf("expected skill_md_path %q, got %q", skillMD, payload.SkillMDPath)
	}
	if payload.Content.Text != skillContent || payload.Content.Truncated || payload.Content.TotalChars == 0 {
		t.Fatalf("expected content object to round-trip, got %+v", payload.Content)
	}
	if len(payload.Tools) != 1 || payload.Tools[0].Name != "run" {
		t.Fatalf("expected tool metadata, got %#v", payload.Tools)
	}
	if payload.Tools[0].Command != "" {
		t.Fatalf("command should be hidden by default, got %q", payload.Tools[0].Command)
	}
}

func TestSkillReadRejectsInvalidFormat(t *testing.T) {
	svc := NewSkillToolService(nil)
	if _, err := svc.HandleRead(map[string]any{"format": "jsno"}); err == nil {
		t.Fatal("expected invalid format error")
	}
}

func TestSkillReadSectionAndTruncation(t *testing.T) {
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "content")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	skillMD := filepath.Join(skillDir, "SKILL.md")
	skillContent := "# content\n\nIntro.\n\n## Steps\n\n1234567890\n\n## Safety\n\nSafe.\n"
	if err := os.WriteFile(skillMD, []byte(skillContent), 0o644); err != nil {
		t.Fatalf("write skill md: %v", err)
	}
	svc := NewSkillToolService([]*SkillInfo{{Name: "content", Dir: skillDir, Available: true}})

	out, err := svc.HandleRead(map[string]any{
		"name":      "content",
		"section":   "steps",
		"max_chars": 5,
		"format":    "json",
	})
	if err != nil {
		t.Fatalf("HandleRead section: %v", err)
	}
	var payload struct {
		Content struct {
			Text       string `json:"text"`
			NextOffset int    `json:"next_offset"`
			Truncated  bool   `json:"truncated"`
		} `json:"content"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal section json: %v\n%s", err, out)
	}
	if payload.Content.Text != "## St" || !payload.Content.Truncated || payload.Content.NextOffset != 5 {
		t.Fatalf("unexpected content slice: %+v", payload.Content)
	}
}

func TestSkillReadSectionsAction(t *testing.T) {
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "content")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# content\n\n## Steps\n"), 0o644); err != nil {
		t.Fatalf("write skill md: %v", err)
	}
	svc := NewSkillToolService([]*SkillInfo{{Name: "content", Dir: skillDir, Available: true}})
	out, err := svc.HandleRead(map[string]any{"action": "sections", "name": "content"})
	if err != nil {
		t.Fatalf("HandleRead sections: %v", err)
	}
	if !strings.Contains(out, `"heading": "Steps"`) {
		t.Fatalf("expected Steps section, got %s", out)
	}
}

func TestSkillReadIncludeCommandsOptIn(t *testing.T) {
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "content")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# content\n"), 0o644); err != nil {
		t.Fatalf("write skill md: %v", err)
	}
	svc := NewSkillToolService([]*SkillInfo{{
		Name: "content",
		Dir:  skillDir,
		Tools: []SkillToolDef{{
			Name:    "run",
			Command: []string{"run.sh"},
		}},
	}})
	out, err := svc.HandleRead(map[string]any{
		"name":             "content",
		"action":           "metadata",
		"include_commands": true,
	})
	if err != nil {
		t.Fatalf("HandleRead include commands: %v", err)
	}
	if !strings.Contains(out, `"command": [`) || !strings.Contains(out, `"run.sh"`) {
		t.Fatalf("expected command when include_commands=true, got %s", out)
	}
}

func TestSkillReadRejectsSymlinkOutsideSkillDir(t *testing.T) {
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "content")
	outside := filepath.Join(tmpDir, "outside.md")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	if err := os.WriteFile(outside, []byte("# outside\n"), 0o644); err != nil {
		t.Fatalf("write outside: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(skillDir, "SKILL.md")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	svc := NewSkillToolService([]*SkillInfo{{Name: "content", Dir: skillDir}})
	if _, err := svc.HandleRead(map[string]any{"name": "content"}); err == nil {
		t.Fatal("expected symlink outside skill dir error")
	}
}
