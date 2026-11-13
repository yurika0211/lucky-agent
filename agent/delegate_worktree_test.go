package agent

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent-runtime/config"
	"github.com/yurika0211/luckyagent-runtime/sandbox"
	"github.com/yurika0211/luckyagent-runtime/session"
	"github.com/yurika0211/luckyagent-runtime/tool"
)

func TestParseDelegateIsolation(t *testing.T) {
	mode, err := parseDelegateIsolation(nil)
	if err != nil || mode != delegateIsolationNone {
		t.Fatalf("nil: mode=%q err=%v", mode, err)
	}
	mode, err = parseDelegateIsolation("")
	if err != nil || mode != delegateIsolationNone {
		t.Fatalf("empty: mode=%q err=%v", mode, err)
	}
	mode, err = parseDelegateIsolation(" Worktree ")
	if err != nil || mode != delegateIsolationWorktree {
		t.Fatalf("worktree: mode=%q err=%v", mode, err)
	}
	if _, err := parseDelegateIsolation("copy"); err == nil || !strings.Contains(err.Error(), "invalid isolation") {
		t.Fatalf("unknown: %v", err)
	}
}

func TestDelegateWorktreeChildWriteStaysOutOfParent(t *testing.T) {
	repo := initTempGitRepo(t)
	childFile := ""
	agent := newWorktreeTestAgent(t, func(ctx context.Context, sess *session.Session) error {
		target := filepath.Join(sess.GetCwd(), "child.txt")
		childFile = target
		return os.WriteFile(target, []byte("from-child\n"), 0o644)
	})
	started := delegateInRepo(t, agent, repo, map[string]any{
		"description": "write child.txt",
		"isolation":   "worktree",
		"timeout":     30,
	})
	status := waitDelegate(t, agent, started.TaskID)
	if status.Status != "completed" {
		t.Fatalf("status=%s error=%s result=%s", status.Status, status.Error, status.Result)
	}
	if !strings.Contains(status.Result, "worktree_path=") || !strings.Contains(status.Result, "base_commit=") {
		t.Fatalf("result missing worktree fields: %s", status.Result)
	}
	worktreePath := fieldAfter(status.Result, "worktree_path=")
	base := fieldAfter(status.Result, "base_commit=")
	if base == "" || worktreePath == "" {
		t.Fatalf("parsed path=%q base=%q from %s", worktreePath, base, status.Result)
	}
	if _, err := os.Stat(filepath.Join(repo, "child.txt")); !os.IsNotExist(err) {
		t.Fatalf("parent contains child.txt: %v", err)
	}
	parentStatus := gitCmd(t, repo, "status", "--porcelain")
	if strings.Contains(parentStatus, "child.txt") {
		t.Fatalf("parent git status mentions child.txt:\n%s", parentStatus)
	}
	body, err := os.ReadFile(filepath.Join(worktreePath, "child.txt"))
	if err != nil || string(body) != "from-child\n" {
		t.Fatalf("worktree file: %q err=%v path=%s recorded=%s", body, err, worktreePath, childFile)
	}
	if !strings.HasPrefix(childFile, worktreePath+string(os.PathSeparator)) {
		t.Fatalf("child wrote %s, worktree %s", childFile, worktreePath)
	}
	if head := strings.TrimSpace(gitCmd(t, repo, "rev-parse", "HEAD")); head != base {
		t.Fatalf("base_commit=%s parent HEAD=%s", base, head)
	}
}

func TestDelegateWorktreeRejectsAbsoluteParentWrite(t *testing.T) {
	repo := initTempGitRepo(t)
	var toolErr error
	var agent *Agent
	agent = newWorktreeTestAgent(t, func(ctx context.Context, sess *session.Session) error {
		tool.RegisterBuiltinTools(agent.tools)
		agent.gateway = tool.NewGateway(agent.tools)
		parentFile := filepath.Join(repo, "sneak.txt")
		_, toolErr = agent.executeToolWithSessionDetailedContext(ctx, "file_write", mustJSON(map[string]any{
			"path":    parentFile,
			"content": "nope",
		}), true, sess)
		return toolErr
	})
	started := delegateInRepo(t, agent, repo, map[string]any{
		"description": "write parent",
		"isolation":   "worktree",
		"timeout":     30,
	})
	status := waitDelegate(t, agent, started.TaskID)
	if toolErr == nil {
		t.Fatal("absolute write into the parent worktree succeeded")
	}
	if _, err := os.Stat(filepath.Join(repo, "sneak.txt")); !os.IsNotExist(err) {
		t.Fatalf("parent file exists after rejected write: %v", err)
	}
	if status.Status != "failed" {
		t.Fatalf("status=%s result=%s err=%s", status.Status, status.Result, status.Error)
	}
	if !strings.Contains(status.Result, "worktree_path=") {
		t.Fatalf("failed task should keep the worktree: %s", status.Result)
	}
	if _, err := os.Stat(fieldAfter(status.Result, "worktree_path=")); err != nil {
		t.Fatalf("kept worktree missing: %v result=%s", err, status.Result)
	}
}

func TestDelegateWorktreeCleanCheckoutIsRemoved(t *testing.T) {
	repo := initTempGitRepo(t)
	agent := newWorktreeTestAgent(t, func(ctx context.Context, sess *session.Session) error {
		return nil
	})
	started := delegateInRepo(t, agent, repo, map[string]any{
		"description": "look only",
		"isolation":   "worktree",
		"timeout":     30,
	})
	status := waitDelegate(t, agent, started.TaskID)
	if status.Status != "completed" {
		t.Fatalf("status=%s error=%s result=%s", status.Status, status.Error, status.Result)
	}
	if !strings.Contains(status.Result, "worktree_removed=true") || !strings.Contains(status.Result, "base_commit=") {
		t.Fatalf("result=%s", status.Result)
	}
	if strings.Contains(status.Result, "worktree_path=") {
		t.Fatalf("clean task kept a path: %s", status.Result)
	}
	listed := gitCmd(t, repo, "worktree", "list", "--porcelain")
	if strings.Contains(listed, ".luckyagent-delegate-worktrees") {
		t.Fatalf("worktree still registered:\n%s", listed)
	}
}

func TestDelegateWorktreeRequiresGitCheckout(t *testing.T) {
	dir := t.TempDir()
	agent := newWorktreeTestAgent(t, func(ctx context.Context, sess *session.Session) error {
		t.Fatal("executor ran for a non-git workspace")
		return nil
	})
	ctx := sandbox.WithSnapshot(context.Background(), &sandbox.Snapshot{Mode: sandbox.ModeDev, Root: dir})
	_, err := agent.tools.Get("subagent")
	if !err {
		t.Fatal("subagent tool missing")
	}
	sub, _ := agent.tools.Get("subagent")
	_, callErr := sub.ContextDetailedHandler(tool.ExecutionContext{Context: ctx}, map[string]any{
		"action":      "delegate",
		"description": "should fail",
		"isolation":   "worktree",
	})
	if callErr == nil || !strings.Contains(callErr.Error(), "git checkout") {
		t.Fatalf("err=%v", callErr)
	}
	raw, listErr := agent.delegate.HandleList(map[string]any{})
	if listErr != nil {
		t.Fatal(listErr)
	}
	if strings.Contains(raw, "should fail") {
		t.Fatalf("task was created: %s", raw)
	}
}

func TestDelegateWithoutIsolationWritesParentWorkspace(t *testing.T) {
	repo := initTempGitRepo(t)
	agent := newWorktreeTestAgent(t, func(ctx context.Context, sess *session.Session) error {
		return os.WriteFile(filepath.Join(sess.GetCwd(), "shared.txt"), []byte("parent\n"), 0o644)
	})
	started := delegateInRepo(t, agent, repo, map[string]any{
		"description": "write shared.txt",
		"timeout":     30,
	})
	status := waitDelegate(t, agent, started.TaskID)
	if status.Status != "completed" {
		t.Fatalf("status=%s error=%s result=%s", status.Status, status.Error, status.Result)
	}
	if strings.Contains(status.Result, "worktree_path=") || strings.Contains(status.Result, "isolation=worktree") {
		t.Fatalf("default isolation created a worktree: %s", status.Result)
	}
	body, err := os.ReadFile(filepath.Join(repo, "shared.txt"))
	if err != nil || string(body) != "parent\n" {
		t.Fatalf("parent file: %q err=%v cwd-result=%s", body, err, status.Result)
	}
}

func TestWorktreeHasChangesKeepsDirtyTree(t *testing.T) {
	repo := initTempGitRepo(t)
	item, err := createDelegateWorktree(repo, "task-dirty")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = removeDelegateWorktree(repo, item.Path) })
	if err := os.WriteFile(filepath.Join(item.Path, "dirty.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !worktreeHasChanges(item.Path, item.BaseCommit) {
		t.Fatal("dirty worktree reported clean")
	}
	note := (&Agent{}).finishDelegatedWorktree(item, nil)
	if !strings.Contains(note, "worktree_path="+item.Path) || !strings.Contains(note, "base_commit="+item.BaseCommit) {
		t.Fatalf("note=%s", note)
	}
	if _, err := os.Stat(item.Path); err != nil {
		t.Fatal(err)
	}
}

type delegateStart struct {
	TaskID string `json:"task_id"`
}

type delegateStatus struct {
	Status string `json:"status"`
	Result string `json:"result"`
	Error  string `json:"error"`
}

func initTempGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitCmd(t, dir, "init")
	gitCmd(t, dir, "config", "user.email", "delegate@example.com")
	gitCmd(t, dir, "config", "user.name", "delegate")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, dir, "add", "README.md")
	gitCmd(t, dir, "commit", "-m", "base")
	return dir
}

func gitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func newWorktreeTestAgent(t *testing.T, run func(context.Context, *session.Session) error) *Agent {
	t.Helper()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.json"), []byte("{\"delegate\":{\"builtin_agents\":false}}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mgr, err := config.NewManagerWithDir(home)
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := session.NewManager(filepath.Join(home, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	registry := tool.NewRegistry()
	agent := &Agent{
		cfg:      mgr,
		tools:    registry,
		gateway:  tool.NewGateway(registry),
		sessions: sessions,
		delegate: tool.NewDelegateManager(tool.DefaultDelegateConfig()),
	}
	agent.toolServices = &tool.Services{Delegate: agent.delegate}
	agent.bindOptionalToolServices()
	agent.bindOptionalExecutionServices()
	agent.delegateChildRun = func(ctx context.Context, sess *session.Session, _ UserTurnInput, cfg LoopConfig) (*LoopResult, error) {
		if cfg.Sandbox != nil {
			ctx = sandbox.WithSnapshot(ctx, cfg.Sandbox)
		}
		if run != nil {
			if err := run(ctx, sess); err != nil {
				return &LoopResult{Response: "child failed"}, err
			}
		}
		return &LoopResult{Response: "child done"}, nil
	}
	return agent
}

func delegateInRepo(t *testing.T, agent *Agent, repo string, args map[string]any) delegateStart {
	t.Helper()
	sub, ok := agent.tools.Get("subagent")
	if !ok || sub.ContextDetailedHandler == nil {
		t.Fatal("subagent handler missing")
	}
	ctx := sandbox.WithSnapshot(context.Background(), &sandbox.Snapshot{Mode: sandbox.ModeDev, Root: repo})
	if args["action"] == nil {
		args["action"] = "delegate"
	}
	result, err := sub.ContextDetailedHandler(tool.ExecutionContext{Context: ctx, SessionID: "parent"}, args)
	if err != nil {
		t.Fatalf("delegate: %v", err)
	}
	var started delegateStart
	if err := json.Unmarshal([]byte(result.Output), &started); err != nil || started.TaskID == "" {
		t.Fatalf("start output: %s err=%v", result.Output, err)
	}
	return started
}

func waitDelegate(t *testing.T, agent *Agent, taskID string) delegateStatus {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var status delegateStatus
	for time.Now().Before(deadline) {
		raw, err := agent.delegate.HandleStatus(map[string]any{"task_id": taskID, "include_result": true})
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(raw), &status); err != nil {
			t.Fatal(err)
		}
		switch status.Status {
		case "completed", "failed", "cancelled":
			return status
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatalf("task %s still %s", taskID, status.Status)
	return status
}

func fieldAfter(text, key string) string {
	idx := strings.Index(text, key)
	if idx < 0 {
		return ""
	}
	rest := text[idx+len(key):]
	if cut := strings.IndexAny(rest, " \n\t"); cut >= 0 {
		rest = rest[:cut]
	}
	return strings.TrimSpace(rest)
}
