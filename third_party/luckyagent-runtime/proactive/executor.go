package proactive

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	ActionStatusDryRun  = "dry_run"
	ActionStatusSkipped = "skipped"
	ActionStatusBlocked = "blocked"
	ActionStatusSuccess = "success"
	ActionStatusFailed  = "failed"
)

// ActionPolicy keeps proactive execution conservative and explicit.
type ActionPolicy struct {
	Enabled        bool
	DryRun         bool
	MaxActions     int
	Cooldown       time.Duration
	WorkspaceDir   string
	HomeDir        string
	Allowed        map[string]bool
	ExecutionStore ActionExecutionStore
}

type ActionExecutionStore interface {
	LatestSuccessfulActionExecution(action string) (ActionExecution, bool, error)
}

type ActionExecutor struct {
	Policy ActionPolicy
	Now    func() time.Time
}

func NewActionExecutor(policy ActionPolicy) ActionExecutor {
	if policy.MaxActions <= 0 {
		policy.MaxActions = 2
	}
	if policy.Cooldown <= 0 {
		policy.Cooldown = 5 * time.Minute
	}
	if policy.Allowed == nil {
		policy.Allowed = defaultAllowedActions()
	}
	return ActionExecutor{Policy: policy}
}

func defaultAllowedActions() map[string]bool {
	return map[string]bool{
		"preload_recent_project_context": true,
		"warm_memory_context":            true,
		"preload_recent_session_summary": true,
		"prefer_lightweight_tasks":       true,
	}
}

func (e ActionExecutor) Execute(ctx context.Context, decision Decision) ([]ActionExecution, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	now := time.Now()
	if e.Now != nil {
		now = e.Now()
	}
	limit := e.Policy.MaxActions
	if limit <= 0 {
		limit = 2
	}
	executions := make([]ActionExecution, 0, len(decision.Actions))
	executed := 0
	for _, action := range decision.Actions {
		if err := ctx.Err(); err != nil {
			return executions, err
		}
		base := ActionExecution{
			ID:        fmt.Sprintf("%s-exec", action.ID),
			ActionID:  action.ID,
			StateID:   action.StateID,
			Action:    action.Action,
			CreatedAt: now,
			Metadata:  map[string]string{},
		}
		switch {
		case action.Action == "observe_only":
			base.Status = ActionStatusSkipped
			base.Reason = "observe-only action"
		case !action.Allowed:
			base.Status = ActionStatusSkipped
			base.Reason = "gate did not allow action"
		case !decision.Enabled || !e.Policy.Enabled:
			base.Status = ActionStatusSkipped
			base.Reason = "proactive disabled"
		case decision.DryRun || e.Policy.DryRun:
			base.Status = ActionStatusDryRun
			base.Reason = "dry-run only"
		case !e.Policy.Allowed[action.Action]:
			base.Status = ActionStatusBlocked
			base.Reason = "action is not in allowlist"
		case executed >= limit:
			base.Status = ActionStatusSkipped
			base.Reason = "max actions per run reached"
		default:
			coolingDown, reason, err := e.cooldownActive(action.Action, now)
			if err != nil {
				return executions, err
			}
			if coolingDown {
				base.Status = ActionStatusSkipped
				base.Reason = reason
				executions = append(executions, base)
				continue
			}
			result := e.executeAllowed(ctx, action)
			base.Status = result.Status
			base.Reason = result.Reason
			base.Metadata = result.Metadata
			if result.Status == ActionStatusSuccess {
				executed++
			}
		}
		executions = append(executions, base)
	}
	return executions, nil
}

func (e ActionExecutor) cooldownActive(action string, now time.Time) (bool, string, error) {
	if e.Policy.ExecutionStore == nil || e.Policy.Cooldown <= 0 {
		return false, "", nil
	}
	latest, ok, err := e.Policy.ExecutionStore.LatestSuccessfulActionExecution(action)
	if err != nil {
		return false, "", err
	}
	if !ok || latest.CreatedAt.IsZero() {
		return false, "", nil
	}
	age := now.Sub(latest.CreatedAt)
	if age < 0 {
		age = 0
	}
	if age >= e.Policy.Cooldown {
		return false, "", nil
	}
	remaining := e.Policy.Cooldown - age
	return true, fmt.Sprintf("action cooldown active for %s", remaining.Round(time.Second)), nil
}

func (e ActionExecutor) executeAllowed(ctx context.Context, action DryRunAction) ActionExecution {
	switch action.Action {
	case "preload_recent_project_context":
		return e.preloadRecentProjectContext(ctx)
	case "warm_memory_context":
		return e.warmMemoryContext()
	case "preload_recent_session_summary":
		return e.preloadRecentSessionSummary()
	case "prefer_lightweight_tasks":
		return ActionExecution{
			Status:   ActionStatusSuccess,
			Reason:   "recorded lightweight-task preference",
			Metadata: map[string]string{"mode": "lightweight"},
		}
	default:
		return ActionExecution{
			Status:   ActionStatusBlocked,
			Reason:   "no safe executor for action",
			Metadata: map[string]string{},
		}
	}
}

func (e ActionExecutor) preloadRecentProjectContext(ctx context.Context) ActionExecution {
	dir := strings.TrimSpace(e.Policy.WorkspaceDir)
	if dir == "" {
		if wd, err := os.Getwd(); err == nil {
			dir = wd
		}
	}
	if dir == "" {
		return ActionExecution{Status: ActionStatusSkipped, Reason: "workspace directory unavailable", Metadata: map[string]string{}}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ActionExecution{Status: ActionStatusFailed, Reason: err.Error(), Metadata: map[string]string{"path": dir}}
	}
	type fileInfo struct {
		name    string
		modTime time.Time
	}
	files := make([]fileInfo, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return ActionExecution{Status: ActionStatusFailed, Reason: err.Error(), Metadata: map[string]string{"path": dir}}
		}
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, fileInfo{name: entry.Name(), modTime: info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].modTime.After(files[j].modTime) })
	recent := make([]string, 0, minInt(5, len(files)))
	for i := 0; i < len(files) && i < 5; i++ {
		recent = append(recent, files[i].name)
	}
	return ActionExecution{
		Status: ActionStatusSuccess,
		Reason: "project file metadata preloaded",
		Metadata: map[string]string{
			"path":         dir,
			"file_count":   fmt.Sprintf("%d", len(files)),
			"recent_files": strings.Join(recent, ","),
		},
	}
}

func (e ActionExecutor) warmMemoryContext() ActionExecution {
	home := strings.TrimSpace(e.Policy.HomeDir)
	if home == "" {
		return ActionExecution{Status: ActionStatusSkipped, Reason: "home directory unavailable", Metadata: map[string]string{}}
	}
	memoryDir := filepath.Join(home, "memory")
	entries, err := os.ReadDir(memoryDir)
	if err != nil {
		return ActionExecution{Status: ActionStatusFailed, Reason: err.Error(), Metadata: map[string]string{"path": memoryDir}}
	}
	return ActionExecution{
		Status: ActionStatusSuccess,
		Reason: "memory directory metadata preloaded",
		Metadata: map[string]string{
			"path":        memoryDir,
			"entry_count": fmt.Sprintf("%d", len(entries)),
		},
	}
}

func (e ActionExecutor) preloadRecentSessionSummary() ActionExecution {
	home := strings.TrimSpace(e.Policy.HomeDir)
	if home == "" {
		return ActionExecution{Status: ActionStatusSkipped, Reason: "home directory unavailable", Metadata: map[string]string{}}
	}
	sessionsDir := filepath.Join(home, "sessions")
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return ActionExecution{Status: ActionStatusFailed, Reason: err.Error(), Metadata: map[string]string{"path": sessionsDir}}
	}
	return ActionExecution{
		Status: ActionStatusSuccess,
		Reason: "session directory metadata preloaded",
		Metadata: map[string]string{
			"path":        sessionsDir,
			"entry_count": fmt.Sprintf("%d", len(entries)),
		},
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
