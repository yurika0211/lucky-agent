package tool

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/yurika0211/luckyagent/internal/computer"
	"github.com/yurika0211/luckyagent/internal/config"
)

// ComputerUseConfig is exported from the tool package for callers that do not
// otherwise need to import the config package.
type ComputerUseConfig = config.ComputerUseToolConfig

// ComputerManager is the narrow manager contract required by the tools. The
// concrete computer.Manager satisfies it, while tests can provide a fake
// implementation without touching the desktop.
type ComputerManager interface {
	Observe(context.Context, string, computer.ObserveRequest) (computer.Observation, error)
	Step(context.Context, string, computer.Action) (computer.Observation, error)
}

// ComputerUseToolService binds the model-facing tools to a stateful desktop
// manager and applies the config-level source/mode/input policy.
type ComputerUseToolService struct {
	manager ComputerManager
	config  ComputerUseConfig
}

// ApprovalRequiredError preserves the proposed action when a control request
// is intentionally stopped before it reaches the platform backend.
type ApprovalRequiredError struct {
	Tool   string
	Action computer.Action
	Reason string
}

func (e *ApprovalRequiredError) Error() string {
	return fmt.Sprintf("computer: approval required for action (reason=%q)", e.Reason)
}

func NewComputerUseToolService(manager ComputerManager, cfg ComputerUseConfig) *ComputerUseToolService {
	return &ComputerUseToolService{manager: manager, config: cfg}
}

func (s *ComputerUseToolService) RegisterTools(r *Registry) {
	if s == nil || r == nil {
		return
	}
	r.Register(s.ObserveTool())
	r.Register(s.ActTool())
}

func (s *ComputerUseToolService) ObserveTool() *Tool {
	return &Tool{
		Name:         "computer_observe",
		Description:  "Capture the current local desktop or a selected display/window. Use this before computer_act and whenever the interface may have changed.",
		Category:     CatBuiltin,
		Source:       "builtin",
		Permission:   PermAuto,
		ParallelSafe: false,
		Parameters: map[string]Param{
			"display_id": {Type: "string", Description: "Optional display identifier.", Required: false},
			"window":     {Type: "string", Description: "Optional target window title or identifier.", Required: false},
			"wait_ms":    {Type: "number", Description: "Optional delay before capture in milliseconds.", Required: false, Default: 0},
			"reason":     {Type: "string", Description: "Why the current screen is needed.", Required: false},
		},
		Handler: func(args map[string]any) (string, error) {
			result, err := s.observe(ExecutionContext{Context: context.Background()}, args)
			return result.Output, err
		},
		ContextDetailedHandler: s.observe,
	}
}

func (s *ComputerUseToolService) ActTool() *Tool {
	return &Tool{
		Name:         "computer_act",
		Description:  "Perform one atomic mouse or keyboard action on the local desktop, then capture the resulting screen for visual verification.",
		Category:     CatBuiltin,
		Source:       "builtin",
		Permission:   PermApprove,
		ParallelSafe: false,
		Parameters: map[string]Param{
			"action":      {Type: "string", Description: "One atomic action: click (also left_click/right_click/middle_click), double_click, move (also mouse_move), drag, type (also type_text/write), keypress (also key/hotkey), or scroll (also wheel).", Required: true},
			"frame_id":    {Type: "string", Description: "Frame ID from the latest computer_observe result.", Required: true},
			"display_id":  {Type: "string", Description: "Optional target display identifier.", Required: false},
			"x":           {Type: "number", Description: "Screen X coordinate for pointer actions.", Required: false},
			"y":           {Type: "number", Description: "Screen Y coordinate for pointer actions.", Required: false},
			"end_x":       {Type: "number", Description: "Drag destination X coordinate.", Required: false},
			"end_y":       {Type: "number", Description: "Drag destination Y coordinate.", Required: false},
			"delta_x":     {Type: "number", Description: "Horizontal scroll delta.", Required: false},
			"delta_y":     {Type: "number", Description: "Vertical scroll delta.", Required: false},
			"button":      {Type: "string", Description: "Mouse button: left, middle, or right.", Required: false, Default: "left"},
			"text":        {Type: "string", Description: "Text for a type action.", Required: false},
			"keys":        {Type: "array", Description: "Keys for a keypress action, for example [CTRL, L].", Required: false},
			"duration_ms": {Type: "number", Description: "Optional action duration in milliseconds.", Required: false},
			"reason":      {Type: "string", Description: "Why this action is needed; shown by approval interfaces.", Required: true},
		},
		Handler: func(args map[string]any) (string, error) {
			result, err := s.act(ExecutionContext{Context: context.Background()}, args)
			return result.Output, err
		},
		ContextDetailedHandler: s.act,
	}
}

func (s *ComputerUseToolService) observe(exec ExecutionContext, args map[string]any) (ToolCallResult, error) {
	if err := s.checkEnabled(exec); err != nil {
		return ToolCallResult{}, err
	}
	if s.manager == nil {
		return ToolCallResult{}, fmt.Errorf("computer: manager is not configured")
	}
	ctx := exec.Context
	if ctx == nil {
		ctx = context.Background()
	}
	sessionID := strings.TrimSpace(exec.SessionID)
	if sessionID == "" {
		return ToolCallResult{}, fmt.Errorf("computer: session id is required")
	}
	waitMS := computerIntArg(args, "wait_ms")
	if waitMS < 0 || waitMS > 30000 {
		return ToolCallResult{}, fmt.Errorf("computer: wait_ms must be between 0 and 30000")
	}
	obs, err := s.manager.Observe(ctx, sessionID, computer.ObserveRequest{
		Target: computer.Target{DisplayID: computerStringArg(args, "display_id"), Window: computerStringArg(args, "window")},
		Wait:   time.Duration(waitMS) * time.Millisecond,
	})
	if err != nil {
		return ToolCallResult{}, err
	}
	return observationResult(obs), nil
}

func (s *ComputerUseToolService) act(exec ExecutionContext, args map[string]any) (ToolCallResult, error) {
	if err := s.checkEnabled(exec); err != nil {
		return ToolCallResult{}, err
	}
	if s.manager == nil {
		return ToolCallResult{}, fmt.Errorf("computer: manager is not configured")
	}
	mode := strings.ToLower(strings.TrimSpace(s.config.Mode))
	if mode == "observe" {
		return ToolCallResult{}, fmt.Errorf("computer: control actions are disabled in observe mode")
	}
	action, err := parseComputerAction(args)
	if err != nil {
		return ToolCallResult{}, err
	}
	if mode != "assist" && mode != "control" {
		return ToolCallResult{}, fmt.Errorf("computer: unsupported mode %q", s.config.Mode)
	}
	if (s.config.RequireApproval || mode == "assist") && !exec.AutoApprove {
		return ToolCallResult{}, &ApprovalRequiredError{Tool: "computer_act", Action: action, Reason: computerStringArg(args, "reason")}
	}
	if action.Kind == computer.ActionTypeText && !s.config.AllowTextInput {
		return ToolCallResult{}, fmt.Errorf("computer: text input is disabled by policy")
	}
	ctx := exec.Context
	if ctx == nil {
		ctx = context.Background()
	}
	sessionID := strings.TrimSpace(exec.SessionID)
	if sessionID == "" {
		return ToolCallResult{}, fmt.Errorf("computer: session id is required")
	}
	obs, err := s.manager.Step(ctx, sessionID, action)
	if err != nil {
		return ToolCallResult{}, err
	}
	result := observationResult(obs)
	if result.Metadata == nil {
		result.Metadata = map[string]any{}
	}
	result.Metadata["reason"] = computerStringArg(args, "reason")
	return result, nil
}

func (s *ComputerUseToolService) checkEnabled(exec ExecutionContext) error {
	if s == nil {
		return fmt.Errorf("computer: tool service is not configured")
	}
	if !s.config.Enabled {
		return fmt.Errorf("computer: use is disabled in configuration")
	}
	if len(s.config.AllowedSources) == 0 || strings.TrimSpace(exec.Source) == "" {
		return nil
	}
	source := strings.ToLower(strings.TrimSpace(exec.Source))
	for _, allowed := range s.config.AllowedSources {
		if strings.EqualFold(strings.TrimSpace(allowed), source) {
			return nil
		}
	}
	return fmt.Errorf("computer: source %q is not allowed", exec.Source)
}

func observationResult(obs computer.Observation) ToolCallResult {
	meta := map[string]any{
		"frame_id":      obs.FrameID,
		"captured_at":   obs.CapturedAt,
		"width":         obs.Width,
		"height":        obs.Height,
		"scale_factor":  obs.ScaleFactor,
		"display_id":    obs.DisplayID,
		"active_window": obs.ActiveWindow,
		"sha256":        obs.SHA256,
	}
	result := ToolCallResult{
		Output:   fmt.Sprintf("Observed frame=%s size=%dx%d display=%s active_window=%q", obs.FrameID, obs.Width, obs.Height, obs.DisplayID, obs.ActiveWindow),
		Metadata: meta,
		Observations: []Observation{{
			Kind: "image", FrameID: obs.FrameID, CapturedAt: obs.CapturedAt, FilePath: obs.FilePath,
			MimeType: obs.MimeType, Width: obs.Width, Height: obs.Height, ScaleFactor: obs.ScaleFactor,
			DisplayID: obs.DisplayID, ActiveWindow: obs.ActiveWindow,
			WindowBounds: Rect{X: obs.WindowBounds.X, Y: obs.WindowBounds.Y, Width: obs.WindowBounds.Width, Height: obs.WindowBounds.Height},
			SHA256:       obs.SHA256, ImageData: obs.ImageData,
		}},
	}
	return result
}

func parseComputerAction(args map[string]any) (computer.Action, error) {
	if computerStringArg(args, "reason") == "" {
		return computer.Action{}, fmt.Errorf("computer: reason is required")
	}
	rawKind := strings.ToLower(strings.TrimSpace(computerStringArg(args, "action")))
	button := computerStringArg(args, "button")
	if button == "" {
		switch rawKind {
		case "right_click", "rightclick":
			button = "right"
		case "middle_click", "middleclick":
			button = "middle"
		}
	}
	action := computer.Action{
		Kind:      normalizeComputerActionKind(computerStringArg(args, "action")),
		FrameID:   computerStringArg(args, "frame_id"),
		DisplayID: computerStringArg(args, "display_id"),
		X:         computerIntArg(args, "x"), Y: computerIntArg(args, "y"), EndX: computerIntArg(args, "end_x"), EndY: computerIntArg(args, "end_y"),
		DeltaX: computerIntArg(args, "delta_x"), DeltaY: computerIntArg(args, "delta_y"), Button: button,
		Text: computerStringArg(args, "text"), Keys: computerStringSliceArg(args, "keys"), DurationMS: computerIntArg(args, "duration_ms"),
	}
	if action.FrameID == "" {
		return computer.Action{}, fmt.Errorf("computer: frame_id is required")
	}
	if action.Kind == "" {
		return computer.Action{}, fmt.Errorf("computer: action is required")
	}
	return action, nil
}

// normalizeComputerActionKind accepts the aliases commonly emitted by model
// providers and messaging adapters. The backend keeps one canonical wire
// vocabulary, while Telegram/LLM callers may naturally say left_click,
// mouse_move, key, or wheel.
func normalizeComputerActionKind(raw string) computer.ActionKind {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "left_click", "right_click", "middle_click", "mouse_click":
		return computer.ActionClick
	case "double_click", "doubleclick", "left_double_click":
		return computer.ActionDoubleClick
	case "mouse_move", "mousemove", "move_mouse":
		return computer.ActionMove
	case "mouse_drag", "drag_mouse":
		return computer.ActionDrag
	case "key", "keys", "key_press", "keypress", "press_key", "hotkey":
		return computer.ActionKeypress
	case "wheel", "scroll_wheel", "mouse_scroll":
		return computer.ActionScroll
	case "text", "type_text", "write":
		return computer.ActionTypeText
	default:
		return computer.ActionKind(strings.TrimSpace(raw))
	}
}

func computerStringArg(args map[string]any, key string) string {
	if value, ok := args[key].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func computerIntArg(args map[string]any, key string) int {
	switch value := args[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case float32:
		return int(value)
	}
	return 0
}

func computerStringSliceArg(args map[string]any, key string) []string {
	switch value := args[key].(type) {
	case string:
		return splitComputerKeys(value)
	case []string:
		out := make([]string, 0, len(value))
		for _, item := range value {
			out = append(out, splitComputerKeys(item)...)
		}
		return out
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			if text, ok := item.(string); ok {
				out = append(out, splitComputerKeys(text)...)
			}
		}
		return out
	}
	return nil
}

func splitComputerKeys(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == '+' || r == ',' || r == ' ' })
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
