package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/yurika0211/luckyagent-runtime/sandbox"
	"github.com/yurika0211/luckyagent-runtime/session"
	"github.com/yurika0211/luckyagent-runtime/tool"
)

const (
	delegateIsolationNone      = "none"
	delegateIsolationWorktree  = "worktree"
	delegateWorktreeContextKey = "LuckyAgent delegate isolation: worktree"
)

// delegateWorktree is one child checkout created from the parent HEAD.
// ParentRoot is the checkout the child must not write back into.
type delegateWorktree struct {
	Path       string
	BaseCommit string
	ParentRoot string
}

type delegateWorktreeBook struct {
	mu    sync.Mutex
	items map[string]delegateWorktree
}

func (b *delegateWorktreeBook) remember(taskID string, item delegateWorktree) {
	if b == nil || strings.TrimSpace(taskID) == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.items == nil {
		b.items = map[string]delegateWorktree{}
	}
	b.items[taskID] = item
}

func (b *delegateWorktreeBook) get(taskID string) (delegateWorktree, bool) {
	if b == nil {
		return delegateWorktree{}, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	item, ok := b.items[taskID]
	return item, ok
}

func (b *delegateWorktreeBook) forget(taskID string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.items, taskID)
}

// installDelegateIsolation wraps subagent so action=delegate accepts an
// optional isolation value even when the published tool schema omits it.
// Only none and worktree are valid. Omitting the field stays none.
func (a *Agent) installDelegateIsolation() {
	if a == nil || a.tools == nil || a.delegate == nil {
		return
	}
	current, ok := a.tools.Get("subagent")
	if !ok || current == nil || current.ContextDetailedHandler == nil {
		return
	}
	inner := current.ContextDetailedHandler
	wrapped := *current
	copied := make(map[string]tool.Param, len(wrapped.Parameters)+1)
	for key, value := range wrapped.Parameters {
		copied[key] = value
	}
	copied["isolation"] = tool.Param{
		Type:        "string",
		Description: "Workspace isolation for action=delegate: none (default, share the parent workspace) or worktree (git worktree from the parent HEAD). Other values fail.",
		Required:    false,
		Default:     delegateIsolationNone,
	}
	copied["resume_from"] = tool.Param{
		Type:        "string",
		Description: "Optional task_id of a completed, failed, or cancelled subagent in this session. The new task continues that child session. description is the new work. A pending, running, missing, or other-session id fails and creates no task.",
		Required:    false,
	}
	wrapped.Parameters = copied
	wrapped.ContextDetailedHandler = func(exec tool.ExecutionContext, args map[string]any) (tool.ToolCallResult, error) {
		if !strings.EqualFold(delegateStringArg(args, "action"), "delegate") {
			return inner(exec, args)
		}
		mode, err := parseDelegateIsolation(args["isolation"])
		if err != nil {
			return tool.ToolCallResult{}, err
		}
		if mode != delegateIsolationWorktree {
			result, err := inner(exec, args)
			if err != nil {
				return result, err
			}
			// Remember the caller workspace so an omitted isolation keeps
			// writing there. The delegate engine's own temp directory is only
			// a fallback when the caller has no workspace.
			if parent := callerWorkspace(exec.Context); parent != "" {
				if taskID := delegateTaskIDFromOutput(result.Output); taskID != "" {
					a.worktrees.remember(taskID, delegateWorktree{ParentRoot: parent})
				}
			}
			return result, nil
		}
		parent, err := parentGitCheckout(exec.Context)
		if err != nil {
			return tool.ToolCallResult{}, err
		}
		forward := copyToolArgs(args)
		delete(forward, "isolation")
		forward["context"] = appendDelegateContext(delegateStringArg(forward, "context"), delegateWorktreeContextKey+"\nparent="+parent)
		result, err := inner(exec, forward)
		if err != nil {
			return result, err
		}
		if taskID := delegateTaskIDFromOutput(result.Output); taskID != "" {
			a.worktrees.remember(taskID, delegateWorktree{ParentRoot: parent})
		}
		return result, nil
	}
	wrapped.Handler = func(args map[string]any) (string, error) {
		result, err := wrapped.ContextDetailedHandler(tool.ExecutionContext{Context: context.Background()}, args)
		return result.Output, err
	}
	a.tools.Register(&wrapped)
}

func parseDelegateIsolation(raw any) (string, error) {
	if raw == nil {
		return delegateIsolationNone, nil
	}
	text, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("invalid isolation %v (use none or worktree)", raw)
	}
	value := strings.ToLower(strings.TrimSpace(text))
	if value == "" {
		return delegateIsolationNone, nil
	}
	switch value {
	case delegateIsolationNone, delegateIsolationWorktree:
		return value, nil
	default:
		return "", fmt.Errorf("invalid isolation %q (use none or worktree)", text)
	}
}

func (a *Agent) prepareDelegatedWorkspace(ctx context.Context, taskID, contextStr string, sess *session.Session) (delegateWorktree, *sandbox.Snapshot, error) {
	if !strings.Contains(contextStr, delegateWorktreeContextKey) {
		return delegateWorktree{}, nil, nil
	}
	pending, ok := a.worktrees.get(taskID)
	parent := strings.TrimSpace(pending.ParentRoot)
	if !ok || parent == "" {
		var err error
		parent, err = parentGitCheckout(ctx)
		if err != nil {
			return delegateWorktree{}, nil, err
		}
	}
	created, err := createDelegateWorktree(parent, taskID)
	if err != nil {
		return delegateWorktree{}, nil, err
	}
	a.worktrees.forget(taskID)
	if sess != nil {
		sess.SetCwd(created.Path)
	}
	// workspace-write rejects absolute paths that leave the worktree, including
	// writes back into the parent checkout. Dev mode would allow them.
	snap, err := sandbox.Prepare(sandbox.ModeWorkspaceWrite, created.Path)
	if err != nil {
		_ = removeDelegateWorktree(parent, created.Path)
		return delegateWorktree{}, nil, err
	}
	return created, snap, nil
}

func (a *Agent) finishDelegatedWorktree(item delegateWorktree, childErr error) string {
	if item.Path == "" {
		return ""
	}
	keep := childErr != nil || worktreeHasChanges(item.Path, item.BaseCommit)
	if !keep {
		if err := removeDelegateWorktree(item.ParentRoot, item.Path); err != nil {
			return fmt.Sprintf("isolation=worktree base_commit=%s worktree_path=%s cleanup_error=%s", item.BaseCommit, item.Path, err.Error())
		}
		return fmt.Sprintf("isolation=worktree base_commit=%s worktree_removed=true", item.BaseCommit)
	}
	return fmt.Sprintf("isolation=worktree base_commit=%s worktree_path=%s", item.BaseCommit, item.Path)
}

func callerWorkspace(ctx context.Context) string {
	if snap := sandbox.SnapshotFromContext(ctx); snap != nil {
		return strings.TrimSpace(snap.Root)
	}
	return ""
}

func parentGitCheckout(ctx context.Context) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	start := ""
	if snap := sandbox.SnapshotFromContext(ctx); snap != nil && strings.TrimSpace(snap.Root) != "" {
		start = snap.Root
	}
	if start == "" {
		var err error
		start, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("isolation=worktree requires a git checkout: %w", err)
		}
	}
	top, err := gitOutput(ctx, start, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("isolation=worktree requires a git checkout: %s", trimGitError(err))
	}
	return top, nil
}

func createDelegateWorktree(parent, taskID string) (delegateWorktree, error) {
	parent = strings.TrimSpace(parent)
	if parent == "" {
		return delegateWorktree{}, fmt.Errorf("isolation=worktree requires a git checkout")
	}
	commit, err := gitOutput(context.Background(), parent, "rev-parse", "HEAD")
	if err != nil {
		return delegateWorktree{}, fmt.Errorf("isolation=worktree requires a git checkout: %s", trimGitError(err))
	}
	path, err := delegateWorktreePath(parent, taskID)
	if err != nil {
		return delegateWorktree{}, err
	}
	if _, statErr := os.Stat(path); statErr == nil {
		return delegateWorktree{}, fmt.Errorf("delegate worktree already exists: %s", path)
	}
	if _, err := gitOutput(context.Background(), parent, "worktree", "add", "--detach", path, commit); err != nil {
		return delegateWorktree{}, fmt.Errorf("create delegate worktree: %s", trimGitError(err))
	}
	return delegateWorktree{Path: path, BaseCommit: commit, ParentRoot: parent}, nil
}

func delegateWorktreePath(parent, taskID string) (string, error) {
	root, err := filepath.Abs(parent)
	if err != nil {
		return "", err
	}
	name := sanitizeDelegateWorktreeName(taskID)
	// Keep the checkout outside the parent repository so it is not a nested
	// untracked directory of the parent worktree.
	return filepath.Join(filepath.Dir(root), ".luckyagent-delegate-worktrees", filepath.Base(root), name), nil
}

func sanitizeDelegateWorktreeName(taskID string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(taskID) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "task"
	}
	return b.String()
}

func worktreeHasChanges(path, base string) bool {
	out, err := gitOutput(context.Background(), path, "status", "--porcelain")
	if err != nil || strings.TrimSpace(out) != "" {
		return true
	}
	if strings.TrimSpace(base) == "" {
		return true
	}
	head, err := gitOutput(context.Background(), path, "rev-parse", "HEAD")
	if err != nil || head != strings.TrimSpace(base) {
		return true
	}
	return false
}

func removeDelegateWorktree(parent, path string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if _, err := gitOutput(context.Background(), parent, "worktree", "remove", "--force", path); err != nil {
		_ = os.RemoveAll(path)
		_, pruneErr := gitOutput(context.Background(), parent, "worktree", "prune")
		if pruneErr != nil {
			return fmt.Errorf("%s", trimGitError(err))
		}
	}
	return nil
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s", msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

func trimGitError(err error) string {
	if err == nil {
		return ""
	}
	text := strings.TrimSpace(err.Error())
	if len(text) > 240 {
		return text[:240]
	}
	return text
}

func copyToolArgs(args map[string]any) map[string]any {
	out := make(map[string]any, len(args))
	for key, value := range args {
		out[key] = value
	}
	return out
}

func appendDelegateContext(existing, block string) string {
	existing = strings.TrimSpace(existing)
	if existing == "" {
		return block
	}
	if strings.Contains(existing, block) {
		return existing
	}
	return existing + "\n\n" + block
}

func delegateTaskWasCancelled(mgr *tool.DelegateManager, taskID string) bool {
	if mgr == nil || strings.TrimSpace(taskID) == "" {
		return false
	}
	raw, err := mgr.HandleStatus(map[string]any{"task_id": taskID, "include_result": false})
	if err != nil {
		return false
	}
	var payload struct {
		Status string `json:"status"`
	}
	if json.Unmarshal([]byte(raw), &payload) != nil {
		return false
	}
	return strings.EqualFold(payload.Status, "cancelled")
}

func delegateTaskIDFromOutput(output string) string {
	var payload struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.TaskID)
}
