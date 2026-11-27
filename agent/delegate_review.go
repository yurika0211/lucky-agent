package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yurika0211/luckyagent-runtime/tool"
	"github.com/yurika0211/luckyagent-tools/collab"
)

// installDelegateReviewTool exposes the parent-model review boundary for
// retained delegated worktrees. Execution is local to the parent checkout;
// it never performs a remote merge, push, or deployment.
func (a *Agent) installDelegateReviewTool() {
	if a == nil || a.tools == nil || a.delegate == nil {
		return
	}
	if _, ok := a.tools.Get("delegate_review"); ok {
		return
	}
	a.tools.Register(&tool.Tool{
		Name:        "delegate_review",
		Description: "Review a terminal delegated task and inspect, merge, or discard its retained local worktree. Merge is allowed only after evidence review and conflict-safe application.",
		Category:    tool.CatDelegate,
		Source:      "builtin",
		Permission:  tool.PermApprove,
		Enabled:     true,
		Parameters: map[string]tool.Param{
			"task_id":    {Type: "string", Description: "Terminal delegated task id.", Required: true},
			"action":     {Type: "string", Description: "inspect, merge, discard, rework, or resume.", Required: true},
			"acceptance": {Type: "string", Description: "Optional acceptance note for the parent review.", Required: false},
			"progress":   {Type: "string", Description: "rework only: none pauses; any other value records another attempt.", Required: false},
		},
		ContextDetailedHandler: func(exec tool.ExecutionContext, args map[string]any) (tool.ToolCallResult, error) {
			out, err := a.reviewDelegatedTask(exec.Context, exec.SessionID, args)
			return tool.ToolCallResult{Output: out}, err
		},
	})
}

func (a *Agent) reviewDelegatedTask(ctx context.Context, sessionID string, args map[string]any) (string, error) {
	taskID := strings.TrimSpace(delegateStringArg(args, "task_id"))
	action := strings.ToLower(strings.TrimSpace(delegateStringArg(args, "action")))
	if taskID == "" {
		return "", fmt.Errorf("task_id is required")
	}
	if action != "inspect" && action != "merge" && action != "discard" && action != "rework" && action != "resume" {
		return "", fmt.Errorf("unsupported review action %q", action)
	}
	statusJSON, err := a.delegate.HandleStatusForSession(map[string]any{"task_id": taskID, "include_result": true}, strings.TrimSpace(sessionID))
	if err != nil {
		return "", err
	}
	var status struct {
		TaskID             string `json:"task_id"`
		ExecutionSessionID string `json:"execution_session_id"`
		ParentSessionID    string `json:"parent_session_id"`
		Description        string `json:"description"`
		Status             string `json:"status"`
		Result             string `json:"result"`
		ResultSummary      string `json:"result_summary"`
		Workspace          string `json:"workspace"`
	}
	if err := json.Unmarshal([]byte(statusJSON), &status); err != nil {
		return "", fmt.Errorf("decode delegated status: %w", err)
	}
	item, hasWorktree := a.worktrees.get(taskID)
	response := map[string]any{
		"task_id": taskID, "status": status.Status, "action": action,
		"worktree_path": item.Path, "base_commit": item.BaseCommit,
		"has_changes": hasWorktree && worktreeHasChanges(item.Path, item.BaseCommit),
	}
	if action == "inspect" {
		response["result_summary"] = status.ResultSummary
		response["result"] = status.Result
		return marshalReviewResponse(response)
	}
	if action == "rework" || action == "resume" {
		return a.reviewRework(item, hasWorktree, action, args, response)
	}
	if action == "discard" {
		if !hasWorktree {
			response["discarded"] = false
			response["reason"] = "no retained managed worktree"
			return marshalReviewResponse(response)
		}
		if err := removeDelegateWorktree(item.ParentRoot, item.Path); err != nil {
			return "", fmt.Errorf("discard delegated worktree: %w", err)
		}
		a.worktrees.forget(taskID)
		response["discarded"] = true
		return marshalReviewResponse(response)
	}
	if status.Status != "completed" {
		return "", fmt.Errorf("task %s is %s; only completed tasks can be merged", taskID, status.Status)
	}
	if !hasWorktree || !worktreeHasChanges(item.Path, item.BaseCommit) {
		if hasWorktree {
			_ = removeDelegateWorktree(item.ParentRoot, item.Path)
			a.worktrees.forget(taskID)
		}
		response["merged"] = true
		response["changed"] = false
		return marshalReviewResponse(response)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	// The verifier receives the child transcript and acceptance criteria before
	// any parent files are changed. A model cannot approve an empty or failed
	// child merely because its worktree is dirty.
	if err := a.verifyCollaborationSubTask(ctx, &collab.SubTask{
		ID: taskID, ExecutionSessionID: status.ExecutionSessionID, ParentSessionID: status.ParentSessionID,
		Description: status.Description, Output: status.Result, Input: status.ResultSummary,
		State: collab.TaskCompleted, AcceptanceCriteria: []string{"fulfil the delegated task and provide evidence for changed files and verification"},
	}, status.Result); err != nil {
		response["merged"] = false
		response["reason"] = err.Error()
		return marshalReviewResponse(response)
	}
	patch, err := gitOutputBytes(ctx, item.Path, "diff", "--binary", item.BaseCommit)
	if err != nil {
		return "", fmt.Errorf("read delegated diff: %w", err)
	}
	if len(patch) == 0 {
		response["merged"] = true
		response["changed"] = false
		return marshalReviewResponse(response)
	}
	if err := runGitInput(ctx, item.ParentRoot, patch, "apply", "--3way", "--binary"); err != nil {
		response["merged"] = false
		response["conflict"] = true
		response["reason"] = err.Error()
		return marshalReviewResponse(response)
	}
	if err := removeDelegateWorktree(item.ParentRoot, item.Path); err != nil {
		response["merged"] = false
		response["reason"] = fmt.Sprintf("merged files but cleanup failed: %v", err)
		return marshalReviewResponse(response)
	}
	a.worktrees.forget(taskID)
	response["merged"] = true
	response["changed"] = true
	return marshalReviewResponse(response)
}

func (a *Agent) reviewRework(item delegateWorktree, hasWorktree bool, action string, args map[string]any, response map[string]any) (string, error) {
	if !hasWorktree || strings.TrimSpace(item.Path) == "" {
		response["rework"] = false
		response["reason"] = "no retained worktree"
		return marshalReviewResponse(response)
	}
	progress := strings.ToLower(strings.TrimSpace(delegateStringArg(args, "progress")))
	if action == "rework" && (progress == "none" || progress == "no") {
		a.delegatePaused.Store(item.TaskID, true)
		response["rework"] = false
		response["paused"] = true
		response["reason"] = "no progress; paused until an explicit resume"
		return marshalReviewResponse(response)
	}
	if _, paused := a.delegatePaused.Load(item.TaskID); paused && action != "resume" {
		response["rework"] = false
		response["paused"] = true
		response["reason"] = "paused after no progress"
		return marshalReviewResponse(response)
	}
	if action == "resume" {
		a.delegatePaused.Delete(item.TaskID)
		if err := incorporateParentIntoWorktree(item); err != nil {
			response["rework"] = false
			response["conflict"] = true
			response["reason"] = err.Error()
			return marshalReviewResponse(response)
		}
		response["incorporated_parent"] = true
	}
	response["rework"] = true
	response["paused"] = false
	response["kept_worktree"] = item.Path
	return marshalReviewResponse(response)
}

func incorporateParentIntoWorktree(item delegateWorktree) error {
	if strings.TrimSpace(item.ParentRoot) == "" || strings.TrimSpace(item.Path) == "" {
		return fmt.Errorf("retained worktree is incomplete")
	}
	patch, err := gitOutputBytes(context.Background(), item.ParentRoot, "diff", "--binary", item.BaseCommit)
	if err != nil {
		return err
	}
	if len(patch) == 0 {
		return nil
	}
	if err := runGitInput(context.Background(), item.Path, patch, "apply", "--3way", "--binary"); err != nil {
		return fmt.Errorf("parent changes conflict with the retained worktree: %s", err.Error())
	}
	return nil
}

func marshalReviewResponse(value map[string]any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}
