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
			Dir     string   `json:"dir"`
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
	if payload.Skills[0].Dir != "/tmp/content-skill" {
		t.Fatalf("expected dir in payload, got %q", payload.Skills[0].Dir)
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
	skillContent := "# content\n\nParent hub.\n"
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
				{Name: "run", Description: "Run the content workflow.", ExposeToModel: true},
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
		Content     string `json:"content"`
		Tools       []struct {
			Name string `json:"name"`
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
	if payload.Content != skillContent {
		t.Fatalf("expected content to round-trip, got %q", payload.Content)
	}
	if len(payload.Tools) != 1 || payload.Tools[0].Name != "run" {
		t.Fatalf("expected tool metadata, got %#v", payload.Tools)
	}
}
