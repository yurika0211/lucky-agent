package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/yurika0211/luckyagent/internal/codex"
)

// CodexToolService exposes the app-server lifecycle as a small, stateful
// tool family. It returns summaries and cursors instead of forwarding the
// unbounded app-server stream directly into the model context.
type CodexToolService struct {
	manager *codex.Manager
}

func NewCodexToolService(manager *codex.Manager) *CodexToolService {
	return &CodexToolService{manager: manager}
}

func (s *CodexToolService) RegisterTools(r *Registry) {
	if s == nil || s.manager == nil || r == nil {
		return
	}
	r.Register(s.startThreadTool())
	r.Register(s.resumeThreadTool())
	r.Register(s.startTurnTool())
	r.Register(s.steerTurnTool())
	r.Register(s.subscribeEventsTool())
	r.Register(s.respondApprovalTool())
	r.Register(s.turnSummaryTool())
}

func (s *CodexToolService) Close() error {
	if s == nil || s.manager == nil {
		return nil
	}
	return s.manager.Close()
}

func (s *CodexToolService) startThreadTool() *Tool {
	return &Tool{
		Name:        "codex.start_thread",
		Description: "Start a Codex App Server thread in an allowed workspace and return its thread ID.",
		Category:    CatBuiltin, Source: "codex", Permission: PermAuto, ParallelSafe: false,
		Parameters: map[string]Param{
			"cwd":             {Type: "string", Description: "Absolute workspace directory for the Codex thread.", Required: true},
			"sandbox":         {Type: "string", Description: "Codex sandbox policy, for example workspace-write.", Default: "workspace-write"},
			"approval_policy": {Type: "string", Description: "Codex approval policy. on-request keeps write approvals visible to LuckyAgent.", Default: "on-request"},
			"model":           {Type: "string", Description: "Optional Codex model override."},
			"ephemeral":       {Type: "boolean", Description: "Whether the Codex thread should be ephemeral."},
		},
		Handler: func(args map[string]any) (string, error) {
			return s.startThread(context.Background(), args)
		},
		ContextDetailedHandler: codexContextHandler(s.startThread),
	}
}

func (s *CodexToolService) resumeThreadTool() *Tool {
	return &Tool{
		Name:        "codex.resume_thread",
		Description: "Resume an existing Codex App Server thread by ID.",
		Category:    CatBuiltin, Source: "codex", Permission: PermAuto, ParallelSafe: false,
		Parameters: map[string]Param{
			"thread_id": {Type: "string", Description: "Codex thread ID.", Required: true},
			"cwd":       {Type: "string", Description: "Optional workspace directory override."},
		},
		Handler: func(args map[string]any) (string, error) {
			return s.resumeThread(context.Background(), args)
		},
		ContextDetailedHandler: codexContextHandler(s.resumeThread),
	}
}

func (s *CodexToolService) startTurnTool() *Tool {
	return &Tool{
		Name:        "codex.start_turn",
		Description: "Start a coding turn on a Codex thread. The turn runs in Codex; poll codex.subscribe_events or codex.get_turn_summary for progress.",
		Category:    CatBuiltin, Source: "codex", Permission: PermApprove, ParallelSafe: false,
		Parameters: map[string]Param{
			"thread_id": {Type: "string", Description: "Codex thread ID.", Required: true},
			"input":     {Type: "string", Description: "Coding instruction to send to Codex.", Required: true},
			"model":     {Type: "string", Description: "Optional Codex model override."},
		},
		Handler: func(args map[string]any) (string, error) {
			return s.startTurn(context.Background(), args)
		},
		ContextDetailedHandler: codexContextHandler(s.startTurn),
	}
}

func (s *CodexToolService) steerTurnTool() *Tool {
	return &Tool{
		Name:        "codex.steer_turn",
		Description: "Send additional user input to an active Codex turn.",
		Category:    CatBuiltin, Source: "codex", Permission: PermApprove, ParallelSafe: false,
		Parameters: map[string]Param{
			"thread_id": {Type: "string", Description: "Codex thread ID.", Required: true},
			"turn_id":   {Type: "string", Description: "Active Codex turn ID.", Required: true},
			"input":     {Type: "string", Description: "Additional instruction for the active turn.", Required: true},
		},
		Handler: func(args map[string]any) (string, error) {
			return s.steerTurn(context.Background(), args)
		},
		ContextDetailedHandler: codexContextHandler(s.steerTurn),
	}
}

func (s *CodexToolService) subscribeEventsTool() *Tool {
	return &Tool{
		Name:        "codex.subscribe_events",
		Description: "Read compact Codex App Server event summaries after a cursor.",
		Category:    CatBuiltin, Source: "codex", Permission: PermAuto, ParallelSafe: true,
		Parameters: map[string]Param{
			"thread_id": {Type: "string", Description: "Optional Codex thread filter."},
			"turn_id":   {Type: "string", Description: "Optional Codex turn filter."},
			"cursor":    {Type: "number", Description: "Return events after this cursor.", Default: 0},
			"limit":     {Type: "number", Description: "Maximum number of events.", Default: 50},
		},
		Handler: func(args map[string]any) (string, error) {
			return s.subscribeEvents(args)
		},
	}
}

func (s *CodexToolService) respondApprovalTool() *Tool {
	return &Tool{
		Name:        "codex.respond_approval",
		Description: "Resolve a pending Codex command or file approval request. Use accept, acceptForSession, decline, or cancel only after showing the request to a human when policy requires it.",
		Category:    CatBuiltin, Source: "codex", Permission: PermApprove, ParallelSafe: false,
		Parameters: map[string]Param{
			"approval_id": {Type: "string", Description: "Approval request ID returned by codex events or summary.", Required: true},
			"decision":    {Type: "string", Description: "Codex approval decision.", Required: true, Schema: map[string]any{"enum": []string{"accept", "acceptForSession", "decline", "cancel"}}},
		},
		Handler: func(args map[string]any) (string, error) {
			return s.respondApproval(args)
		},
	}
}

func (s *CodexToolService) turnSummaryTool() *Tool {
	return &Tool{
		Name:        "codex.get_turn_summary",
		Description: "Get the current compact summary, output, status, and pending approvals for a Codex thread or turn.",
		Category:    CatBuiltin, Source: "codex", Permission: PermAuto, ParallelSafe: true,
		Parameters: map[string]Param{
			"thread_id": {Type: "string", Description: "Codex thread ID.", Required: true},
			"turn_id":   {Type: "string", Description: "Optional Codex turn ID."},
		},
		Handler: func(args map[string]any) (string, error) {
			return s.turnSummary(args)
		},
	}
}

func (s *CodexToolService) startThread(ctx context.Context, args map[string]any) (string, error) {
	thread, err := s.manager.StartThread(ctx, codexStringArg(args, "cwd"), codexStringArg(args, "sandbox"), codexStringArg(args, "approval_policy"), map[string]any{
		"model":     codexOptionalString(args, "model"),
		"ephemeral": optionalBool(args, "ephemeral"),
	})
	if err != nil {
		return "", err
	}
	return marshalToolJSON(thread)
}

func (s *CodexToolService) resumeThread(ctx context.Context, args map[string]any) (string, error) {
	thread, err := s.manager.ResumeThread(ctx, codexStringArg(args, "thread_id"), codexStringArg(args, "cwd"))
	if err != nil {
		return "", err
	}
	return marshalToolJSON(thread)
}

func (s *CodexToolService) startTurn(ctx context.Context, args map[string]any) (string, error) {
	turn, err := s.manager.StartTurn(ctx, codexStringArg(args, "thread_id"), codexStringArg(args, "input"), codexStringArg(args, "model"))
	if err != nil {
		return "", err
	}
	return marshalToolJSON(turn)
}

func (s *CodexToolService) steerTurn(ctx context.Context, args map[string]any) (string, error) {
	if err := s.manager.SteerTurn(ctx, codexStringArg(args, "thread_id"), codexStringArg(args, "turn_id"), codexStringArg(args, "input")); err != nil {
		return "", err
	}
	return `{"status":"steered"}`, nil
}

func (s *CodexToolService) subscribeEvents(args map[string]any) (string, error) {
	result, err := s.manager.Events(codexStringArg(args, "thread_id"), codexStringArg(args, "turn_id"), codexIntArg(args, "cursor"), int(codexIntArg(args, "limit")))
	if err != nil {
		return "", err
	}
	return marshalToolJSON(result)
}

func (s *CodexToolService) respondApproval(args map[string]any) (string, error) {
	approval, err := s.manager.RespondApproval(context.Background(), codexStringArg(args, "approval_id"), codexStringArg(args, "decision"))
	if err != nil {
		return "", err
	}
	return marshalToolJSON(map[string]any{"status": "resolved", "approval": approval})
}

func (s *CodexToolService) turnSummary(args map[string]any) (string, error) {
	result, err := s.manager.Summary(codexStringArg(args, "thread_id"), codexStringArg(args, "turn_id"))
	if err != nil {
		return "", err
	}
	return marshalToolJSON(result)
}

func codexStringArg(args map[string]any, key string) string {
	value, _ := args[key].(string)
	return strings.TrimSpace(value)
}

func codexOptionalString(args map[string]any, key string) any {
	value := codexStringArg(args, key)
	if value == "" {
		return nil
	}
	return value
}

func optionalBool(args map[string]any, key string) any {
	value, ok := args[key].(bool)
	if !ok {
		return nil
	}
	return value
}

func codexIntArg(args map[string]any, key string) int64 {
	switch value := args[key].(type) {
	case float64:
		return int64(value)
	case int:
		return int64(value)
	case int64:
		return value
	case string:
		n, _ := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		return n
	default:
		return 0
	}
}

func marshalToolJSON(value any) (string, error) {
	if value == nil {
		return "null", nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode codex tool result: %w", err)
	}
	return string(raw), nil
}

func codexContextHandler(handler func(context.Context, map[string]any) (string, error)) func(ExecutionContext, map[string]any) (ToolCallResult, error) {
	return func(exec ExecutionContext, args map[string]any) (ToolCallResult, error) {
		ctx := exec.Context
		if ctx == nil {
			ctx = context.Background()
		}
		output, err := handler(ctx, args)
		return ToolCallResult{Output: output}, err
	}
}
