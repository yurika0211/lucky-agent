package swebench

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yurika0211/luckyagent/internal/agent"
	"github.com/yurika0211/luckyagent/internal/session"
)

// SolveTask is the request passed to a SWE-bench solver.
type SolveTask struct {
	Instance Instance
	Worktree string
	Prompt   string
}

// SolveResult is the agent-side result before the runner collects git diff.
type SolveResult struct {
	Response   string
	Iterations int
	TokensUsed int
	ToolCalls  []ToolCallSummary
}

// Solver lets tests provide fake repair behavior while production uses
// LuckyAgentSolver.
type Solver interface {
	Solve(ctx context.Context, task SolveTask) (SolveResult, error)
}

// SolverFunc adapts a function into a Solver.
type SolverFunc func(ctx context.Context, task SolveTask) (SolveResult, error)

func (fn SolverFunc) Solve(ctx context.Context, task SolveTask) (SolveResult, error) {
	return fn(ctx, task)
}

// LuckyAgentSolver runs the existing Agent loop in an isolated session rooted at
// the benchmark worktree.
type LuckyAgentSolver struct {
	Agent      *agent.Agent
	LoopConfig agent.LoopConfig
	SessionDir string
}

func (s LuckyAgentSolver) Solve(ctx context.Context, task SolveTask) (SolveResult, error) {
	if s.Agent == nil {
		return SolveResult{}, fmt.Errorf("agent is required")
	}
	if strings.TrimSpace(task.Worktree) == "" {
		return SolveResult{}, fmt.Errorf("worktree is required")
	}
	prompt := strings.TrimSpace(task.Prompt)
	if prompt == "" {
		prompt = task.Instance.BuildPrompt()
	}
	sessionDir := strings.TrimSpace(s.SessionDir)
	if sessionDir == "" {
		sessionDir = filepath.Join(os.TempDir(), "lh-swe-bench-sessions")
	}
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		return SolveResult{}, fmt.Errorf("create session dir: %w", err)
	}

	sess := session.NewSession("swebench-"+SafeID(task.Instance.InstanceID), sessionDir)
	sess.SetTitle("SWE-bench " + task.Instance.InstanceID)
	sess.SetCwd(task.Worktree)

	loopCfg := s.LoopConfig
	loopCfg.Ephemeral = true
	result, err := s.Agent.RunLoopWithSessionInput(ctx, sess, agent.TextUserTurnInput(prompt), loopCfg)
	if result == nil {
		if err != nil {
			return SolveResult{}, err
		}
		return SolveResult{}, fmt.Errorf("agent returned nil result")
	}

	toolCalls := make([]ToolCallSummary, 0, len(result.ToolCalls))
	for _, call := range result.ToolCalls {
		toolCalls = append(toolCalls, ToolCallSummary{
			Name:       call.Name,
			Arguments:  call.Arguments,
			ResultSize: len(call.Result),
			DurationMS: float64(call.Duration) / float64(time.Millisecond),
		})
	}

	out := SolveResult{
		Response:   result.Response,
		Iterations: result.Iterations,
		TokensUsed: result.TokensUsed,
		ToolCalls:  toolCalls,
	}
	if err != nil {
		return out, err
	}
	return out, nil
}
