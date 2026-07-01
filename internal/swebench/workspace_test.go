package swebench

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareWorkspaceAndCollectModelPatch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx := context.Background()
	dir := t.TempDir()
	reposDir := filepath.Join(dir, "repos")
	source := filepath.Join(reposDir, "owner__repo")
	initGitRepo(t, source)

	baseCommit := gitOutput(t, source, "rev-parse", "HEAD")
	inst := Instance{
		InstanceID:       "owner__repo-1",
		Repo:             "owner/repo",
		BaseCommit:       strings.TrimSpace(baseCommit),
		ProblemStatement: "Change hello.txt",
	}
	ws, err := PrepareWorkspace(ctx, WorkspaceConfig{
		WorkDir:       filepath.Join(dir, "bench"),
		ReposDir:      reposDir,
		ResetWorktree: true,
	}, inst)
	if err != nil {
		t.Fatalf("PrepareWorkspace: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws.Worktree, "hello.txt")); err != nil {
		t.Fatalf("expected checkout file: %v", err)
	}

	if err := os.WriteFile(filepath.Join(ws.Worktree, "hello.txt"), []byte("changed\n"), 0o600); err != nil {
		t.Fatalf("modify file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ws.Worktree, "new_file.txt"), []byte("new\n"), 0o600); err != nil {
		t.Fatalf("write new file: %v", err)
	}
	patch, err := CollectModelPatch(ctx, "git", ws.Worktree)
	if err != nil {
		t.Fatalf("CollectModelPatch: %v", err)
	}
	if !strings.Contains(patch, "diff --git a/hello.txt b/hello.txt") {
		t.Fatalf("patch missing modified file:\n%s", patch)
	}
	if !strings.Contains(patch, "diff --git a/new_file.txt b/new_file.txt") {
		t.Fatalf("patch missing untracked file:\n%s", patch)
	}
}

func TestResolveRepoSourceLayouts(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "owner", "repo")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	got, err := ResolveRepoSource(dir, "owner/repo")
	if err != nil {
		t.Fatalf("ResolveRepoSource nested: %v", err)
	}
	if got != filepath.Clean(nested) {
		t.Fatalf("expected nested path, got %s", got)
	}

	dir = t.TempDir()
	flat := filepath.Join(dir, "owner__repo")
	if err := os.MkdirAll(flat, 0o700); err != nil {
		t.Fatalf("mkdir flat: %v", err)
	}
	got, err = ResolveRepoSource(dir, "owner/repo")
	if err != nil {
		t.Fatalf("ResolveRepoSource flat: %v", err)
	}
	if got != filepath.Clean(flat) {
		t.Fatalf("expected flat path, got %s", got)
	}
}

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	gitOutput(t, dir, "init")
	gitOutput(t, dir, "config", "user.email", "bench@example.com")
	gitOutput(t, dir, "config", "user.name", "Bench")
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello\n"), 0o600); err != nil {
		t.Fatalf("write repo file: %v", err)
	}
	gitOutput(t, dir, "add", ".")
	gitOutput(t, dir, "commit", "-m", "initial")
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, string(out))
	}
	return string(out)
}
