package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent-runtime/tool"
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

func TestUnapprovedSkillMutationLeavesFileUnchanged(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "SKILL.md")
	original := []byte("name: review\n")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	a := &Agent{skills: []*tool.SkillInfo{{Name: "review", Dir: root}}, hitl: newHITLGate()}
	done := make(chan error, 1)
	go func() {
		_, err := a.executeToolWithSessionDetailedContext(context.Background(), "file_patch", mustJSON(map[string]any{
			"path": path, "match": "name: review", "replace": "name: changed",
			"fault": "line 1 parse error", "cause": "title drifted",
		}), true, nil)
		done <- err
	}()
	deadline := time.Now().Add(2 * time.Second)
	var pending []tool.PendingApproval
	for time.Now().Before(deadline) {
		pending = a.hitl.list()
		if len(pending) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(pending) != 1 || !strings.Contains(pending[0].Reason, "line 1 parse error") || !strings.Contains(pending[0].Reason, "最小差异") {
		t.Fatalf("review prompt=%#v", pending)
	}
	if strings.Contains(pending[0].Reason, "sk-live-secret") {
		t.Fatalf("secret leaked: %s", pending[0].Reason)
	}
	if _, err := a.hitl.Resolve(pending[0].ID, "deny", ""); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil {
		t.Fatal("denied mutation returned success")
	}
	body, err := os.ReadFile(path)
	if err != nil || string(body) != string(original) {
		t.Fatalf("file changed: %q err=%v", body, err)
	}
}

func TestExternalFailureDoesNotEditInstalledSkill(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "SKILL.md")
	original := []byte("name: review\n")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "remote-error.txt")
	a := &Agent{skills: []*tool.SkillInfo{{Name: "review", Dir: root}}}
	if key, reason := a.installedSkillMutation("file_write", map[string]any{
		"path": outside, "content": "changed", "error": "network timeout sk-live-secret",
	}); key != "" || reason != "" {
		t.Fatalf("external failure was treated as a skill edit: %s %s", key, reason)
	}
	body, err := os.ReadFile(path)
	if err != nil || string(body) != string(original) {
		t.Fatalf("skill file changed: %q err=%v", body, err)
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
