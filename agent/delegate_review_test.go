package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDelegateReviewReworkPausesAndResumeIncorporatesParent(t *testing.T) {
	repo := initTempGitRepo(t)
	item, err := createDelegateWorktree(repo, "task-review")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = removeDelegateWorktree(repo, item.Path) })
	if err := os.WriteFile(filepath.Join(item.Path, "child.txt"), []byte("child\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := &Agent{}
	a.worktrees.remember("task-review", item)
	response := map[string]any{"task_id": "task-review", "action": "rework"}
	pausedRaw, err := a.reviewRework(item, true, "rework", map[string]any{"progress": "none"}, response)
	if err != nil {
		t.Fatal(err)
	}
	var paused map[string]any
	if err := json.Unmarshal([]byte(pausedRaw), &paused); err != nil {
		t.Fatal(err)
	}
	if paused["paused"] != true || paused["rework"] != false {
		t.Fatalf("pause response: %s", pausedRaw)
	}
	if _, err := os.Stat(item.Path); err != nil {
		t.Fatalf("paused review removed the worktree: %v", err)
	}

	if err := os.WriteFile(filepath.Join(repo, "later.txt"), []byte("next-day\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, repo, "add", "later.txt")
	gitCmd(t, repo, "commit", "-m", "later")

	resumeResponse := map[string]any{"task_id": "task-review", "action": "resume"}
	resumedRaw, err := a.reviewRework(item, true, "resume", nil, resumeResponse)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resumedRaw, `"rework":true`) || !strings.Contains(resumedRaw, `"incorporated_parent":true`) {
		t.Fatalf("resume response: %s", resumedRaw)
	}
	body, err := os.ReadFile(filepath.Join(item.Path, "later.txt"))
	if err != nil || string(body) != "next-day\n" {
		t.Fatalf("parent commit was not incorporated: %q err=%v", body, err)
	}
	child, err := os.ReadFile(filepath.Join(item.Path, "child.txt"))
	if err != nil || string(child) != "child\n" {
		t.Fatalf("child work lost: %q err=%v", child, err)
	}
}


