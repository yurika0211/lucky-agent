package agent

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/yurika0211/luckyagent-tools"
)

func TestInstalledSkillMutationRequiresExactApproval(t *testing.T) {
	root := t.TempDir()
	a := &Agent{skills: []*tool.SkillInfo{{Name: "review", Dir: root}}}
	key, reason := a.installedSkillMutation("file_patch", map[string]any{
		"path":    filepath.Join(root, "SKILL.md"),
		"match":   "old",
		"replace": "new",
	})
	if key == "" || reason == "" {
		t.Fatalf("missing mutation approval data: key=%q reason=%q", key, reason)
	}
	if !strings.Contains(reason, "review") || !strings.Contains(reason, "SKILL.md") {
		t.Fatalf("reason lacks reviewable target: %q", reason)
	}
	if a.skillMutationApprovalSeen(key) {
		t.Fatal("unapproved mutation was marked approved")
	}
	a.rememberSkillMutationApproval(key)
	if !a.skillMutationApprovalSeen(key) {
		t.Fatal("exact mutation approval was not remembered")
	}

	otherKey, _ := a.installedSkillMutation("file_patch", map[string]any{
		"path": filepath.Join(root, "other.md"), "match": "old", "replace": "new",
	})
	if otherKey == key {
		t.Fatal("different file mutation reused the same approval key")
	}
}

func TestInstalledSkillMutationIgnoresReadsAndExternalPaths(t *testing.T) {
	root := t.TempDir()
	a := &Agent{skills: []*tool.SkillInfo{{Name: "review", Dir: root}}}
	if key, _ := a.installedSkillMutation("file_read", map[string]any{"path": filepath.Join(root, "SKILL.md")}); key != "" {
		t.Fatal("file reads must not require skill mutation approval")
	}
	if key, _ := a.installedSkillMutation("file_write", map[string]any{"path": filepath.Join(t.TempDir(), "outside.md"), "content": "x"}); key != "" {
		t.Fatal("external paths must not be treated as installed skill edits")
	}
}
