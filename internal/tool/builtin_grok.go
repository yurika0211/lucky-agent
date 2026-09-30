package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/yurika0211/luckyagent/internal/grok"
)

// GrokToolService exposes the local grok agent lifecycle as a small, stateful
// tool family. It returns summaries and cursors instead of forwarding the
// unbounded ACP stream directly into the model context.
type GrokToolService struct {
	manager *grok.Manager
}

func NewGrokToolService(manager *grok.Manager) *GrokToolService {
	return &GrokToolService{manager: manager}
}

func (s *GrokToolService) RegisterTools(r *Registry) {
	if s == nil || s.manager == nil || r == nil {
		return
	}
	r.Register(s.startSessionTool())
	r.Register(s.resumeSessionTool())
	r.Register(s.startTurnTool())
	r.Register(s.subscribeEventsTool())
	r.Register(s.respondApprovalTool())
	r.Register(s.turnSummaryTool())
}

func (s *GrokToolService) Close() error {
	if s == nil || s.manager == nil {
		return nil
	}
	return s.manager.Close()
}

func (s *GrokToolService) startSessionTool() *Tool {
	return &Tool{
		Name:        "grok.start_session",
		Description: "Start a Grok agent session in an allowed workspace and return its session ID. The session stays open for later turns.",
		Category:    CatBuiltin, Source: "grok", Permission: PermAuto, ParallelSafe: false,
		Parameters: map[string]Param{
			"cwd":   {Type: "string", Description: "Absolute workspace directory for the Grok session.", Required: true},
			"model": {Type: "string", Description: "Optional Grok model override."},
		},
		Handler: func(args map[string]any) (string, error) {
			return s.startSession(context.Background(), args)
		},
		ContextDetailedHandler: grokContextHandler(s.startSession),
	}
}

func (s *GrokToolService) resumeSessionTool() *Tool {
	return &Tool{
		Name:        "grok.resume_session",
		Description: "Resume an existing Grok agent session by ID.",
		Category:    CatBuiltin, Source: "grok", Permission: PermAuto, ParallelSafe: false,
		Parameters: map[string]Param{
			"session_id": {Type: "string", Description: "Grok session ID.", Required: true},
			"cwd":        {Type: "string", Description: "Optional workspace directory override."},
		},
		Handler: func(args map[string]any) (string, error) {
			return s.resumeSession(context.Background(), args)
		},
		ContextDetailedHandler: grokContextHandler(s.resumeSession),
	}
}

func (s *GrokToolService) startTurnTool() *Tool {
	return &Tool{
		Name:        "grok.start_turn",
		Description: "Send a coding instruction to a Grok session. In gateway approval mode this returns immediately so pending approvals can be resolved with grok.respond_approval; poll grok.subscribe_events or grok.get_turn_summary until the turn completes.",
		Category:    CatBuiltin, Source: "grok", Permission: PermApprove, ParallelSafe: false,
		Parameters: map[string]Param{
			"session_id": {Type: "string", Description: "Grok session ID.", Required: true},
			"input":      {Type: "string", Description: "Coding instruction to send to Grok.", Required: true},
			"model":      {Type: "string", Description: "Optional Grok model override."},
		},
		Handler: func(args map[string]any) (string, error) {
			return s.startTurn(context.Background(), args)
		},
		ContextDetailedHandler: grokContextHandler(s.startTurn),
	}
}

func (s *GrokToolService) subscribeEventsTool() *Tool {
	return &Tool{
		Name:        "grok.subscribe_events",
		Description: "Read compact Grok agent event summaries after a cursor.",
		Category:    CatBuiltin, Source: "grok", Permission: PermAuto, ParallelSafe: true,
		Parameters: map[string]Param{
			"session_id": {Type: "string", Description: "Optional Grok session filter."},
			"turn_id":    {Type: "string", Description: "Optional Grok turn filter."},
			"cursor":     {Type: "number", Description: "Return events after this cursor.", Default: 0},
			"limit":      {Type: "number", Description: "Maximum number of events.", Default: 50},
		},
		Handler: func(args map[string]any) (string, error) {
			return s.subscribeEvents(args)
		},
	}
}

func (s *GrokToolService) respondApprovalTool() *Tool {
	return &Tool{
		Name:        "grok.respond_approval",
		Description: "Resolve a pending Grok tool permission request. Use allow, allow_always, deny, or cancel only after showing the request to a human when policy requires it.",
		Category:    CatBuiltin, Source: "grok", Permission: PermApprove, ParallelSafe: false,
		Parameters: map[string]Param{
			"approval_id": {Type: "string", Description: "Approval request ID returned by grok events or summary.", Required: true},
			"decision":    {Type: "string", Description: "Grok approval decision.", Required: true, Schema: map[string]any{"enum": []string{"allow", "allow_always", "deny", "cancel"}}},
		},
		Handler: func(args map[string]any) (string, error) {
			return s.respondApproval(args)
		},
	}
}

func (s *GrokToolService) turnSummaryTool() *Tool {
	return &Tool{
		Name:        "grok.get_turn_summary",
		Description: "Get the current compact summary, output, status, and pending approvals for a Grok session or turn.",
		Category:    CatBuiltin, Source: "grok", Permission: PermAuto, ParallelSafe: true,
		Parameters: map[string]Param{
			"session_id": {Type: "string", Description: "Grok session ID.", Required: true},
			"turn_id":    {Type: "string", Description: "Optional Grok turn ID."},
		},
		Handler: func(args map[string]any) (string, error) {
			return s.turnSummary(args)
		},
	}
}

func (s *GrokToolService) startSession(ctx context.Context, args map[string]any) (string, error) {
	session, err := s.manager.StartSession(ctx, grokStringArg(args, "cwd"), grokStringArg(args, "model"))
	if err != nil {
		return "", err
	}
	return marshalGrokJSON(session)
}

func (s *GrokToolService) resumeSession(ctx context.Context, args map[string]any) (string, error) {
	session, err := s.manager.ResumeSession(ctx, grokStringArg(args, "session_id"), grokStringArg(args, "cwd"))
	if err != nil {
		return "", err
	}
	return marshalGrokJSON(session)
}

func (s *GrokToolService) startTurn(ctx context.Context, args map[string]any) (string, error) {
	var (
		turn grok.Turn
		err  error
	)
	sessionID := grokStringArg(args, "session_id")
	input := grokStringArg(args, "input")
	model := grokStringArg(args, "model")
	if s.manager.ApprovalMode() == "gateway" {
		turn, err = s.manager.StartTurnAsync(ctx, sessionID, input, model)
	} else {
		turn, err = s.manager.StartTurn(ctx, sessionID, input, model)
	}
	if err != nil {
		return "", err
	}
	return marshalGrokJSON(turn)
}

func (s *GrokToolService) subscribeEvents(args map[string]any) (string, error) {
	result, err := s.manager.Events(grokStringArg(args, "session_id"), grokStringArg(args, "turn_id"), grokIntArg(args, "cursor"), int(grokIntArg(args, "limit")))
	if err != nil {
		return "", err
	}
	return marshalGrokJSON(result)
}

func (s *GrokToolService) respondApproval(args map[string]any) (string, error) {
	approval, err := s.manager.RespondApproval(context.Background(), grokStringArg(args, "approval_id"), grokStringArg(args, "decision"))
	if err != nil {
		return "", err
	}
	return marshalGrokJSON(map[string]any{"status": "resolved", "approval": approval})
}

func (s *GrokToolService) turnSummary(args map[string]any) (string, error) {
	result, err := s.manager.Summary(grokStringArg(args, "session_id"), grokStringArg(args, "turn_id"))
	if err != nil {
		return "", err
	}
	return marshalGrokJSON(result)
}

func grokStringArg(args map[string]any, key string) string {
	value, _ := args[key].(string)
	return strings.TrimSpace(value)
}

func grokIntArg(args map[string]any, key string) int64 {
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

func marshalGrokJSON(value any) (string, error) {
	if value == nil {
		return "null", nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode grok tool result: %w", err)
	}
	return string(raw), nil
}

func grokContextHandler(handler func(context.Context, map[string]any) (string, error)) func(ExecutionContext, map[string]any) (ToolCallResult, error) {
	return func(exec ExecutionContext, args map[string]any) (ToolCallResult, error) {
		ctx := exec.Context
		if ctx == nil {
			ctx = context.Background()
		}
		output, err := handler(ctx, args)
		return ToolCallResult{Output: output}, err
	}
}
