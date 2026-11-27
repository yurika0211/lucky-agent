package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	delegateruntime "github.com/yurika0211/luckyagent-orchestrator/delegate"
	"github.com/yurika0211/luckyagent-runtime/tool"
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

func TestDelegateReviewConflictKeepsWorktreeAndParent(t *testing.T) {
	repo := initTempGitRepo(t)
	a, taskID, item := completedReviewAgent(t, repo, "completed with evidence")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("parent\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(item.Path, "README.md"), []byte("child\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.collaborationModel = func(context.Context, string, string, providerSnapshot) (string, error) {
		return `{"accepted":true,"reason":"child changed README.md"}`, nil
	}
	raw, err := a.reviewDelegatedTask(context.Background(), "parent-session", map[string]any{
		"task_id": taskID, "action": "merge",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, `"merged":false`) || !strings.Contains(raw, `"conflict":true`) || !strings.Contains(raw, `"kept_worktree"`) {
		t.Fatalf("conflict response: %s", raw)
	}
	parent, err := os.ReadFile(filepath.Join(repo, "README.md"))
	if err != nil || string(parent) != "parent\n" {
		t.Fatalf("parent changed on conflict: %q err=%v", parent, err)
	}
	if _, err := os.Stat(item.Path); err != nil {
		t.Fatalf("conflict removed the worktree: %v", err)
	}
	kept, err := os.ReadFile(filepath.Join(item.Path, "README.md"))
	if err != nil || string(kept) != "child\n" {
		t.Fatalf("child worktree lost: %q err=%v", kept, err)
	}
}

func TestDelegateReviewRejectionReturnsTaskWithoutMerge(t *testing.T) {
	repo := initTempGitRepo(t)
	a, taskID, item := completedReviewAgent(t, repo, "completed with evidence")
	if err := os.WriteFile(filepath.Join(item.Path, "child.txt"), []byte("child\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.collaborationModel = func(context.Context, string, string, providerSnapshot) (string, error) {
		return `{"accepted":false,"reason":"missing verification"}`, nil
	}
	raw, err := a.reviewDelegatedTask(context.Background(), "parent-session", map[string]any{
		"task_id": taskID, "action": "merge",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, `"merged":false`) || !strings.Contains(raw, `"rework":true`) || !strings.Contains(raw, `"acceptance":"rejected"`) {
		t.Fatalf("rejection response: %s", raw)
	}
	if _, err := os.Stat(filepath.Join(repo, "child.txt")); err == nil {
		t.Fatal("rejected review merged the child file")
	}
	if _, err := os.Stat(item.Path); err != nil {
		t.Fatalf("rejected review removed the worktree: %v", err)
	}
}

func completedReviewAgent(t *testing.T, repo, result string) (*Agent, string, delegateWorktree) {
	t.Helper()
	manager := tool.NewDelegateManager(tool.DefaultDelegateConfig())
	manager.SetAgentExecutorWithOptions(func(context.Context, string, string, delegateruntime.AgentExecutorOptions) (string, error) {
		return result, nil
	})
	started, err := manager.HandleDelegateWithOwner(map[string]any{
		"description": "change the notes", "context": "evidence",
	}, delegateruntime.Owner{SessionID: "parent-session"})
	if err != nil {
		t.Fatal(err)
	}
	var start struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal([]byte(started), &start); err != nil || start.TaskID == "" {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := manager.HandleStatus(map[string]any{"task_id": start.TaskID, "include_result": true})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(raw, `"status":"completed"`) {
			item, err := createDelegateWorktree(repo, start.TaskID)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = removeDelegateWorktree(repo, item.Path) })
			a := &Agent{delegate: manager}
			a.worktrees.remember(start.TaskID, item)
			return a, start.TaskID, item
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("delegate did not complete")
	return nil, "", delegateWorktree{}
}


