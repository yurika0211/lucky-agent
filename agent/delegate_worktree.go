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
	delegateSharedContextKey   = "LuckyAgent delegate isolation: none"
)

// delegateWorktree is one child checkout created from the parent HEAD.
// ParentRoot is the checkout the child must not write back into.
type delegateWorktree struct {
	TaskID     string
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

func (a *Agent) rememberDelegateOwner(taskID, sessionID string) {
	if a == nil || strings.TrimSpace(taskID) == "" || strings.TrimSpace(sessionID) == "" {
		return
	}
	a.delegateOwnerMu.Lock()
	defer a.delegateOwnerMu.Unlock()
	if a.delegateOwners == nil {
		a.delegateOwners = make(map[string]string)
	}
	a.delegateOwners[strings.TrimSpace(taskID)] = strings.TrimSpace(sessionID)
}

func (a *Agent) forgetDelegateOwner(taskID string) {
	if a == nil {
		return
	}
	a.delegateOwnerMu.Lock()
	defer a.delegateOwnerMu.Unlock()
	delete(a.delegateOwners, strings.TrimSpace(taskID))
}

func (a *Agent) delegateTasksForSession(sessionID string) []string {
	if a == nil {
		return nil
	}
	sessionID = strings.TrimSpace(sessionID)
	a.delegateOwnerMu.Lock()
	defer a.delegateOwnerMu.Unlock()
	var taskIDs []string
	for taskID, owner := range a.delegateOwners {
		if owner == sessionID {
			taskIDs = append(taskIDs, taskID)
		}
	}
	return taskIDs
}

// installDelegateIsolation wraps subagent so action=delegate accepts an
// optional isolation value even when the published tool schema omits it.
// Only none and worktree are valid. Untyped/code delegates default to worktree;
// read-only explore/plan roles remain shared unless explicitly overridden.
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
		Description: "Workspace isolation for action=delegate: worktree (default for code tasks, including the dispatch snapshot) or none (shared parent workspace). Explore/plan roles default to none. Other values fail.",
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
		mode, err := delegateIsolationForArgs(args)
		if err != nil {
			return tool.ToolCallResult{}, err
		}
		if mode != delegateIsolationWorktree {
			forward := copyToolArgs(args)
			delete(forward, "isolation")
			// Stamp the parent before the task starts. Remembering it after
			// inner() returns races the executor, which otherwise writes the
			// engine temp directory.
			if parent := callerWorkspace(exec.Context); parent != "" {
				forward["context"] = appendDelegateContext(delegateStringArg(forward, "context"), delegateSharedContextKey+"\nparent="+parent)
			}
			result, err := inner(exec, forward)
			if err == nil {
				if taskID := delegateTaskIDFromOutput(result.Output); taskID != "" {
					a.rememberDelegateOwner(taskID, exec.SessionID)
				}
			}
			return result, err
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
			a.rememberDelegateOwner(taskID, exec.SessionID)
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

func delegateIsolationForArgs(args map[string]any) (string, error) {
	if raw, ok := args["isolation"]; ok && raw != nil && strings.TrimSpace(fmt.Sprint(raw)) != "" {
		return parseDelegateIsolation(raw)
	}
	// Read-only built-in roles keep a shared checkout. Untyped delegates are
	// code-capable by default and receive a worktree unless explicitly opted
	// out with isolation=none.
	role := strings.ToLower(strings.TrimSpace(delegateStringArg(args, "subagent_type")))
	if role == "explore" || role == "plan" {
		return delegateIsolationNone, nil
	}
	return delegateIsolationWorktree, nil
}

func (a *Agent) prepareDelegatedWorkspace(ctx context.Context, taskID, contextStr string, sess *session.Session) (delegateWorktree, *sandbox.Snapshot, error) {
	if !strings.Contains(contextStr, delegateWorktreeContextKey) {
		return delegateWorktree{}, nil, nil
	}
	// The handler stamps parent= before the task starts. The in-memory book
	// is filled only after HandleDelegate returns, so it can lose the race.
	parent := delegateStampedParent(contextStr, delegateWorktreeContextKey)
	if parent == "" {
		if pending, ok := a.worktrees.get(taskID); ok {
			parent = strings.TrimSpace(pending.ParentRoot)
		}
	}
	if parent == "" {
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
		a.worktrees.forget(item.TaskID)
		if err := removeDelegateWorktree(item.ParentRoot, item.Path); err != nil {
			return fmt.Sprintf("isolation=worktree base_commit=%s worktree_path=%s cleanup_error=%s", item.BaseCommit, item.Path, err.Error())
		}
		return fmt.Sprintf("isolation=worktree base_commit=%s worktree_removed=true", item.BaseCommit)
	}
	if item.TaskID != "" {
		a.worktrees.remember(item.TaskID, item)
	}
	return fmt.Sprintf("isolation=worktree base_commit=%s worktree_path=%s", item.BaseCommit, item.Path)
}

func delegateSharedParent(contextStr string) string {
	return delegateStampedParent(contextStr, delegateSharedContextKey)
}

func delegateStampedParent(contextStr, key string) string {
	marker := key + "\nparent="
	at := strings.LastIndex(contextStr, marker)
	if at < 0 {
		return ""
	}
	rest := contextStr[at+len(marker):]
	if end := strings.Index(rest, "\n"); end >= 0 {
		rest = rest[:end]
	}
	return strings.TrimSpace(rest)
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
	if err := seedDelegateWorktree(parent, path); err != nil {
		_ = removeDelegateWorktree(parent, path)
		return delegateWorktree{}, fmt.Errorf("snapshot delegate worktree: %w", err)
	}
	return delegateWorktree{TaskID: taskID, Path: path, BaseCommit: commit, ParentRoot: parent}, nil
}

// seedDelegateWorktree copies tracked staged/unstaged changes and non-ignored
// untracked files into the child checkout without touching the parent's index
// or working tree. The snapshot is taken once at dispatch time.
func seedDelegateWorktree(parent, child string) error {
	// git diff HEAD omits changes that exist only in the index when those
	// changes match the worktree. Include the index explicitly, then the
	// unstaged worktree, so a staged-only edit is part of the dispatch snapshot.
	patch, err := gitOutputBytes(context.Background(), parent, "diff", "--cached", "--binary", "HEAD")
	if err != nil {
		return err
	}
	unstaged, err := gitOutputBytes(context.Background(), parent, "diff", "--binary")
	if err != nil {
		return err
	}
	patch = append(patch, unstaged...)
	if err != nil {
		return err
	}
	if len(patch) > 0 {
		if err := runGitInput(context.Background(), child, patch, "apply", "--binary"); err != nil {
			return fmt.Errorf("apply tracked changes: %w", err)
		}
	}
	files, err := gitOutputBytes(context.Background(), parent, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return err
	}
	for _, raw := range strings.Split(string(files), "\x00") {
		rel := strings.TrimSpace(raw)
		if rel == "" || filepath.IsAbs(rel) || rel == "." || strings.HasPrefix(filepath.Clean(rel), ".."+string(os.PathSeparator)) {
			continue
		}
		if delegateSnapshotSkips(rel) {
			continue
		}
		src := filepath.Join(parent, filepath.FromSlash(rel))
		dst := filepath.Join(child, filepath.FromSlash(rel))
		if err := copyDelegateSnapshotPath(src, dst); err != nil {
			return fmt.Errorf("copy untracked %s: %w", rel, err)
		}
	}
	return nil
}

// delegateSnapshotSkips drops credential-shaped paths even when they are not
// covered by gitignore. The snapshot is copied into another checkout.
func delegateSnapshotSkips(rel string) bool {
	base := strings.ToLower(filepath.Base(filepath.FromSlash(rel)))
	switch base {
	case ".env", ".env.local", ".netrc", "credentials", "credentials.json", "id_rsa", "id_ed25519":
		return true
	}
	if strings.HasPrefix(base, ".env.") || strings.Contains(base, "secret") || strings.HasSuffix(base, ".pem") || strings.HasSuffix(base, ".key") {
		return true
	}
	return false
}

func copyDelegateSnapshotPath(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return os.Symlink(target, dst)
	}
	if info.IsDir() {
		return filepath.Walk(src, func(path string, item os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			rel, err := filepath.Rel(src, path)
			if err != nil {
				return err
			}
			target := filepath.Join(dst, rel)
			if item.IsDir() {
				return os.MkdirAll(target, item.Mode().Perm())
			}
			return copyDelegateSnapshotPath(path, target)
		})
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, info.Mode().Perm())
}

func gitOutputBytes(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("%s", msg)
	}
	return stdout.Bytes(), nil
}

func runGitInput(ctx context.Context, dir string, input []byte, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
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
		if removeErr := os.RemoveAll(path); removeErr != nil {
			return fmt.Errorf("remove delegate worktree: %w", removeErr)
		}
		if _, pruneErr := gitOutput(context.Background(), parent, "worktree", "prune"); pruneErr != nil {
			return fmt.Errorf("%s", trimGitError(err))
		}
	}
	if info, statErr := os.Stat(path); statErr == nil && info.IsDir() {
		return fmt.Errorf("delegate worktree still exists: %s", path)
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
