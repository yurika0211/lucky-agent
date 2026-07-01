package swebench

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// RunOptions controls one instance execution.
type RunOptions struct {
	Variant       string
	ModelName     string
	WorkDir       string
	ReposDir      string
	GitBinary     string
	ResetWorktree bool
	DryRun        bool
	Solver        Solver
}

// RunInstance executes one SWE-bench instance and returns both LuckyAgent trace
// and evaluator-compatible prediction data.
func RunInstance(ctx context.Context, opts RunOptions, inst Instance) (record Record, pred Prediction) {
	start := time.Now()
	variant := strings.TrimSpace(opts.Variant)
	if variant == "" {
		variant = "manual"
	}
	modelName := strings.TrimSpace(opts.ModelName)
	if modelName == "" {
		modelName = "luckyagent/" + variant
	}

	record = Record{
		Type:       "record",
		Variant:    variant,
		InstanceID: inst.InstanceID,
		Repo:       inst.Repo,
		BaseCommit: inst.BaseCommit,
		DryRun:     opts.DryRun,
		StartedAt:  start.UTC().Format(time.RFC3339Nano),
	}
	pred = Prediction{
		InstanceID:      inst.InstanceID,
		ModelNameOrPath: modelName,
	}
	defer func() {
		record.DurationMS = float64(time.Since(start)) / float64(time.Millisecond)
	}()

	if err := inst.Validate(); err != nil {
		record.Error = err.Error()
		record.PatchEmpty = true
		return record, pred
	}
	prompt := inst.BuildPrompt()
	if opts.DryRun {
		record.PatchEmpty = true
		record.Extra = map[string]any{"prompt_chars": len(prompt)}
		return record, pred
	}
	if opts.Solver == nil {
		record.Error = "solver is required"
		record.PatchEmpty = true
		return record, pred
	}

	workspace, err := PrepareWorkspace(ctx, WorkspaceConfig{
		WorkDir:       opts.WorkDir,
		ReposDir:      opts.ReposDir,
		GitBinary:     opts.GitBinary,
		ResetWorktree: opts.ResetWorktree,
	}, inst)
	if err != nil {
		record.Error = err.Error()
		record.PatchEmpty = true
		return record, pred
	}
	record.Worktree = workspace.Worktree
	record.SourcePath = workspace.SourcePath

	solveResult, solveErr := opts.Solver.Solve(ctx, SolveTask{
		Instance: inst,
		Worktree: workspace.Worktree,
		Prompt:   prompt,
	})
	record.AgentResponse = truncateForRecord(solveResult.Response, 4000)
	record.Iterations = solveResult.Iterations
	record.TokensUsed = solveResult.TokensUsed
	record.ToolCalls = solveResult.ToolCalls

	patch, diffErr := CollectModelPatch(ctx, opts.GitBinary, workspace.Worktree)
	pred.ModelPatch = patch
	record.PatchBytes = len(patch)
	record.PatchEmpty = strings.TrimSpace(patch) == ""

	switch {
	case solveErr != nil && diffErr != nil:
		record.Error = fmt.Sprintf("solve: %v; collect diff: %v", solveErr, diffErr)
	case solveErr != nil:
		record.Error = solveErr.Error()
	case diffErr != nil:
		record.Error = diffErr.Error()
	}

	return record, pred
}

func truncateForRecord(s string, limit int) string {
	s = strings.TrimSpace(s)
	if limit <= 0 || len(s) <= limit {
		return s
	}
	return s[:limit] + "\n... (truncated)"
}
