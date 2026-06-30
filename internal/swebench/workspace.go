package swebench

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// WorkspaceConfig controls how one SWE-bench instance checkout is prepared.
type WorkspaceConfig struct {
	WorkDir       string
	ReposDir      string
	GitBinary     string
	ResetWorktree bool
}

// Workspace describes the checkout used for one benchmark instance.
type Workspace struct {
	InstanceID string `json:"instance_id"`
	Repo       string `json:"repo"`
	SourcePath string `json:"source_path"`
	Worktree   string `json:"worktree"`
}

// PrepareWorkspace clones a local cached repository into an instance worktree
// and checks out the SWE-bench base commit.
func PrepareWorkspace(ctx context.Context, cfg WorkspaceConfig, inst Instance) (Workspace, error) {
	if err := inst.Validate(); err != nil {
		return Workspace{}, err
	}
	cfg = normalizeWorkspaceConfig(cfg)
	if strings.TrimSpace(cfg.WorkDir) == "" {
		return Workspace{}, fmt.Errorf("work dir is required")
	}
	if strings.TrimSpace(cfg.ReposDir) == "" {
		return Workspace{}, fmt.Errorf("repos dir is required")
	}

	source, err := ResolveRepoSource(cfg.ReposDir, inst.Repo)
	if err != nil {
		return Workspace{}, err
	}
	worktree := filepath.Join(cfg.WorkDir, "worktrees", SafeID(inst.InstanceID))
	if cfg.ResetWorktree {
		if err := os.RemoveAll(worktree); err != nil {
			return Workspace{}, fmt.Errorf("reset worktree: %w", err)
		}
	}
	if _, err := os.Stat(worktree); err == nil {
		return Workspace{}, fmt.Errorf("worktree already exists: %s", worktree)
	} else if !os.IsNotExist(err) {
		return Workspace{}, fmt.Errorf("stat worktree: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(worktree), 0o700); err != nil {
		return Workspace{}, fmt.Errorf("create worktree parent: %w", err)
	}

	if _, err := runGit(ctx, "", cfg.GitBinary, "clone", "--no-checkout", source, worktree); err != nil {
		return Workspace{}, err
	}
	if _, err := runGit(ctx, worktree, cfg.GitBinary, "checkout", inst.BaseCommit); err != nil {
		return Workspace{}, err
	}
	if _, err := runGit(ctx, worktree, cfg.GitBinary, "reset", "--hard", inst.BaseCommit); err != nil {
		return Workspace{}, err
	}
	if _, err := runGit(ctx, worktree, cfg.GitBinary, "clean", "-fdx"); err != nil {
		return Workspace{}, err
	}

	return Workspace{
		InstanceID: inst.InstanceID,
		Repo:       inst.Repo,
		SourcePath: source,
		Worktree:   worktree,
	}, nil
}

// ResolveRepoSource maps a SWE-bench repo name like "django/django" to a local
// repository cache. It accepts either nested owner/repo paths or flattened
// owner__repo paths.
func ResolveRepoSource(reposDir, repo string) (string, error) {
	reposDir = strings.TrimSpace(reposDir)
	repo = strings.Trim(strings.TrimSpace(repo), "/")
	if reposDir == "" {
		return "", fmt.Errorf("repos dir is required")
	}
	if repo == "" {
		return "", fmt.Errorf("repo is required")
	}

	candidates := []string{
		filepath.Join(reposDir, repo),
		filepath.Join(reposDir, strings.ReplaceAll(repo, "/", "__")),
		filepath.Join(reposDir, SafeID(repo)),
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && info.IsDir() {
			return filepath.Clean(candidate), nil
		}
	}
	return "", fmt.Errorf("local repo cache not found for %q under %s", repo, reposDir)
}

// CollectModelPatch returns the git diff used as the SWE-bench model_patch. It
// includes untracked files via intent-to-add before reading the diff.
func CollectModelPatch(ctx context.Context, gitBinary, worktree string) (string, error) {
	worktree = strings.TrimSpace(worktree)
	if worktree == "" {
		return "", fmt.Errorf("worktree is required")
	}
	gitBinary = normalizeGitBinary(gitBinary)
	if _, err := runGit(ctx, worktree, gitBinary, "add", "-N", "."); err != nil {
		return "", err
	}
	return runGit(ctx, worktree, gitBinary, "diff", "--binary")
}

func normalizeWorkspaceConfig(cfg WorkspaceConfig) WorkspaceConfig {
	cfg.WorkDir = cleanNonEmptyPath(cfg.WorkDir)
	cfg.ReposDir = cleanNonEmptyPath(cfg.ReposDir)
	cfg.GitBinary = normalizeGitBinary(cfg.GitBinary)
	return cfg
}

func cleanNonEmptyPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}

func normalizeGitBinary(gitBinary string) string {
	gitBinary = strings.TrimSpace(gitBinary)
	if gitBinary == "" {
		return "git"
	}
	return gitBinary
}

func runGit(ctx context.Context, dir, gitBinary string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, normalizeGitBinary(gitBinary), args...)
	if strings.TrimSpace(dir) != "" {
		cmd.Dir = dir
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		out := strings.TrimSpace(stdout.String())
		errOut := strings.TrimSpace(stderr.String())
		if errOut != "" {
			if out != "" {
				out += "\n"
			}
			out += errOut
		}
		return out, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, out)
	}
	return stdout.String(), nil
}
