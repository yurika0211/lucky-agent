package swebench

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunInstanceDryRun(t *testing.T) {
	inst := Instance{
		InstanceID:       "dry-1",
		Repo:             "owner/repo",
		BaseCommit:       "abc",
		ProblemStatement: "Fix it",
	}
	record, pred := RunInstance(context.Background(), RunOptions{
		Variant:   "dry",
		ModelName: "luckyagent/dry",
		DryRun:    true,
	}, inst)
	if record.Error != "" {
		t.Fatalf("dry-run error: %s", record.Error)
	}
	if !record.PatchEmpty || record.PatchBytes != 0 {
		t.Fatalf("expected empty patch record, got %#v", record)
	}
	if pred.InstanceID != inst.InstanceID || pred.ModelNameOrPath != "luckyagent/dry" || pred.ModelPatch != "" {
		t.Fatalf("unexpected prediction: %#v", pred)
	}
	if record.DurationMS <= 0 {
		t.Fatalf("expected duration to be recorded")
	}
}

func TestRunInstanceWithSolver(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx := context.Background()
	dir := t.TempDir()
	reposDir := filepath.Join(dir, "repos")
	source := filepath.Join(reposDir, "owner__repo")
	initGitRepo(t, source)

	inst := Instance{
		InstanceID:       "owner__repo-2",
		Repo:             "owner/repo",
		BaseCommit:       strings.TrimSpace(gitOutput(t, source, "rev-parse", "HEAD")),
		ProblemStatement: "Change hello.txt",
	}
	solver := SolverFunc(func(ctx context.Context, task SolveTask) (SolveResult, error) {
		if !strings.Contains(task.Prompt, "Change hello.txt") {
			t.Fatalf("prompt missing issue: %s", task.Prompt)
		}
		if err := os.WriteFile(filepath.Join(task.Worktree, "hello.txt"), []byte("solver change\n"), 0o600); err != nil {
			return SolveResult{}, err
		}
		return SolveResult{Response: "changed", Iterations: 2, TokensUsed: 42}, nil
	})

	record, pred := RunInstance(ctx, RunOptions{
		Variant:       "fake",
		ModelName:     "luckyagent/fake",
		WorkDir:       filepath.Join(dir, "bench"),
		ReposDir:      reposDir,
		ResetWorktree: true,
		Solver:        solver,
	}, inst)
	if record.Error != "" {
		t.Fatalf("RunInstance error: %s", record.Error)
	}
	if record.PatchEmpty || record.PatchBytes == 0 {
		t.Fatalf("expected non-empty patch: %#v", record)
	}
	if record.Iterations != 2 || record.TokensUsed != 42 {
		t.Fatalf("solver metrics not recorded: %#v", record)
	}
	if !strings.Contains(pred.ModelPatch, "solver change") {
		t.Fatalf("prediction missing solver patch:\n%s", pred.ModelPatch)
	}
}
