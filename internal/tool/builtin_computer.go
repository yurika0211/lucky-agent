package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
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
	Tool    string
	Action  computer.Action
	Reason  string
	Actions []computer.Action
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
		Description:  "Capture the current local desktop or a selected display/window. Large screenshots may be downscaled to fit configured limits while preserving the full frame. Use this before computer_act and whenever the interface may have changed.",
		Category:     CatBuiltin,
		Source:       "builtin",
		Permission:   PermAuto,
		ParallelSafe: false,
		Parameters: map[string]Param{
			"display_id": {Type: "string", Description: "Optional display identifier.", Required: false},
			"window":     {Type: "string", Description: "X11: active, a window ID, or a unique title. Tree format: target window title. Wayland images use the portal-selected monitor.", Required: false},
			"region": {Type: "object", Description: "Optional {x,y,width,height} region in the selected window/display's original pixels, before scaling. Returned pointer coordinates are relative to this image.", Schema: map[string]any{
				"properties": map[string]any{"x": map[string]any{"type": "integer", "minimum": 0}, "y": map[string]any{"type": "integer", "minimum": 0}, "width": map[string]any{"type": "integer", "minimum": 1}, "height": map[string]any{"type": "integer", "minimum": 1}},
				"required":   []string{"x", "y", "width", "height"}, "additionalProperties": false,
			}},
			"format":  {Type: "string", Description: "image (default), tree (AT-SPI control names/roles/actions without an image), or both. Prefer tree for accessible controls and image for visual details.", Default: "image"},
			"wait_ms": {Type: "number", Description: "Optional delay before capture in milliseconds.", Required: false, Default: 0},
			"reason":  {Type: "string", Description: "Why the current screen is needed.", Required: false},
		},
		Handler: func(args map[string]any) (string, error) {
			result, err := s.observe(ExecutionContext{Context: context.Background()}, args)
			return result.Output, err
		},
		ContextDetailedHandler: s.observe,
	}
}

func (s *ComputerUseToolService) ActTool() *Tool {
	t := &Tool{
		Name:         "computer_act",
		Description:  "Perform a desktop action or a short predetermined sequence, then return one fresh observation. Prefer an observed accessibility element over coordinates when available. Stop and observe between decisions that depend on a changed interface.",
		Category:     CatBuiltin,
		Source:       "builtin",
		Permission:   PermApprove,
		ParallelSafe: false,
		Parameters: map[string]Param{
			"action":         {Type: "string", Description: "Single action: click, double_click, move, drag, type, keypress, scroll, or accessibility invoke/set_text/focus. Supply either action or actions."},
			"actions":        {Type: "array", Description: "Optional array of action objects, e.g. [{action:click,x:100,y:80},{action:type,text:hello}]. Default maximum 5. Only the first action may point, invoke, or focus; subsequent steps may type, set_text, keypress, or scroll. All use the outer frame_id. No branching or blind replay."},
			"element_id":     {Type: "string", Description: "ID from the latest accessibility tree for invoke, set_text, or focus."},
			"element_action": {Type: "string", Description: "For invoke, an action name listed on the observed element, such as click or press."},
			"frame_id":       {Type: "string", Description: "Frame ID from the latest computer_observe result.", Required: true},
			"display_id":     {Type: "string", Description: "Optional target display identifier.", Required: false},
			"x":              {Type: "number", Description: "X pixel coordinate in the latest returned screenshot; desktop scaling is handled automatically.", Required: false},
			"y":              {Type: "number", Description: "Y pixel coordinate in the latest returned screenshot; desktop scaling is handled automatically.", Required: false},
			"end_x":          {Type: "number", Description: "Drag destination X pixel coordinate in the latest returned screenshot.", Required: false},
			"end_y":          {Type: "number", Description: "Drag destination Y pixel coordinate in the latest returned screenshot.", Required: false},
			"delta_x":        {Type: "number", Description: "Horizontal scroll delta.", Required: false},
			"delta_y":        {Type: "number", Description: "Vertical scroll delta.", Required: false},
			"button":         {Type: "string", Description: "Mouse button: left, middle, or right.", Required: false, Default: "left"},
			"click_count":    {Type: "number", Description: "Number of clicks for click actions, from 1 to 5.", Required: false, Default: 1},
			"text":           {Type: "string", Description: "Text for a type action.", Required: false},
			"keys":           {Type: "array", Description: "Keys for a keypress action, for example [CTRL, L].", Required: false},
			"duration_ms":    {Type: "number", Description: "Optional action duration in milliseconds.", Required: false},
			"reason":         {Type: "string", Description: "Why this action is needed; shown by approval interfaces.", Required: true},
		},
		Handler: func(args map[string]any) (string, error) {
			result, err := s.act(ExecutionContext{Context: context.Background()}, args)
			return result.Output, err
		},
		ContextDetailedHandler: s.act,
	}
	properties := make(map[string]any)
	for name, param := range t.Parameters {
		if name != "actions" && name != "frame_id" && name != "reason" && name != "display_id" {
			properties[name] = paramSchema(param)
		}
	}
	limit := s.config.MaxBatchActions
	if limit <= 0 {
		limit = 5
	}
	batchParam := t.Parameters["actions"]
	batchParam.Schema = map[string]any{"items": map[string]any{"type": "object", "properties": properties, "required": []string{"action"}, "additionalProperties": false}, "minItems": 1, "maxItems": min(limit, 10)}
	t.Parameters["actions"] = batchParam
	return t
}

func (s *ComputerUseToolService) observe(exec ExecutionContext, args map[string]any) (ToolCallResult, error) {
	if err := s.checkEnabled(exec); err != nil {
		return ToolCallResult{}, err
	}
	if s.manager == nil {
		return ToolCallResult{}, fmt.Errorf("computer: manager is not configured")
	}
	ctx, cancel := s.operationContext(exec)
	defer cancel()
	sessionID := strings.TrimSpace(exec.SessionID)
	if sessionID == "" {
		return ToolCallResult{}, fmt.Errorf("computer: session id is required")
	}
	waitMS := computerIntArg(args, "wait_ms")
	if waitMS < 0 || waitMS > 30000 {
		return ToolCallResult{}, fmt.Errorf("computer: wait_ms must be between 0 and 30000")
	}
	var region *computer.Rect
	if raw, exists := args["region"]; exists {
		values, ok := raw.(map[string]any)
		if !ok {
			return ToolCallResult{}, fmt.Errorf("computer: region must be an object")
		}
		coords := make([]int, 4)
		for i, key := range []string{"x", "y", "width", "height"} {
			value, exists := values[key]
			if !exists {
				return ToolCallResult{}, fmt.Errorf("computer: region.%s is required", key)
			}
			n, valid := strictComputerInt(value)
			if !valid {
				return ToolCallResult{}, fmt.Errorf("computer: region.%s must be an integer", key)
			}
			coords[i] = n
		}
		region = &computer.Rect{X: coords[0], Y: coords[1], Width: coords[2], Height: coords[3]}
		if region.X < 0 || region.Y < 0 || region.Width <= 0 || region.Height <= 0 {
			return ToolCallResult{}, fmt.Errorf("computer: region requires non-negative x/y and positive width/height")
		}
	}
	obs, err := s.manager.Observe(ctx, sessionID, computer.ObserveRequest{
		Target: computer.Target{DisplayID: computerStringArg(args, "display_id"), Window: computerStringArg(args, "window"), Region: region},
		Wait:   time.Duration(waitMS) * time.Millisecond,
		Format: computerStringArg(args, "format"),
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
	actions, err := s.parseActions(args)
	if err != nil {
		return ToolCallResult{}, err
	}
	if mode != "assist" && mode != "control" {
		return ToolCallResult{}, fmt.Errorf("computer: unsupported mode %q", s.config.Mode)
	}
	if (s.config.RequireApproval || mode == "assist") && !exec.AutoApprove {
		return ToolCallResult{}, &ApprovalRequiredError{Tool: "computer_act", Action: actions[0], Actions: actions, Reason: computerStringArg(args, "reason")}
	}
	for _, action := range actions {
		if (action.Kind == computer.ActionTypeText || action.Kind == computer.ActionSetText) && !s.config.AllowTextInput {
			return ToolCallResult{}, fmt.Errorf("computer: text input is disabled by policy")
		}
	}
	ctx, cancel := s.operationContext(exec)
	defer cancel()
	sessionID := strings.TrimSpace(exec.SessionID)
	if sessionID == "" {
		return ToolCallResult{}, fmt.Errorf("computer: session id is required")
	}
	var obs computer.Observation
	if len(actions) == 1 {
		obs, err = s.manager.Step(ctx, sessionID, actions[0])
	} else if manager, ok := s.manager.(interface {
		StepBatch(context.Context, string, []computer.Action) (computer.Observation, error)
	}); ok {
		obs, err = manager.StepBatch(ctx, sessionID, actions)
	} else {
		return ToolCallResult{}, fmt.Errorf("computer: this manager does not support batches")
	}
	if err != nil {
		return ToolCallResult{}, err
	}
	result := observationResult(obs)
	if result.Metadata == nil {
		result.Metadata = map[string]any{}
	}
	result.Metadata["reason"] = computerStringArg(args, "reason")
	result.Metadata["completed_actions"] = len(actions)
	result.Metadata["action_results"] = obs.ActionResults
	for _, execution := range obs.ActionResults {
		result.Output += fmt.Sprintf("\nAction request_id=%s index=%d kind=%s completed=%t input=(%d,%d) backend=(%d,%d) active_window=%q→%q duration_ms=%d", execution.RequestID, execution.Index, execution.Kind, execution.Completed, execution.InputX, execution.InputY, execution.BackendX, execution.BackendY, execution.ActiveWindowBefore, execution.ActiveWindowAfter, execution.DurationMS)
	}
	return result, nil
}

func (s *ComputerUseToolService) operationContext(exec ExecutionContext) (context.Context, context.CancelFunc) {
	ctx := exec.Context
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := s.config.StepTimeoutSeconds
	if timeout <= 0 {
		timeout = 30
	}
	return context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
}

func (s *ComputerUseToolService) parseActions(args map[string]any) ([]computer.Action, error) {
	raw, batch := args["actions"]
	if !batch {
		action, err := parseComputerAction(args)
		if err != nil {
			return nil, err
		}
		return []computer.Action{action}, action.Validate()
	}
	if computerStringArg(args, "action") != "" {
		return nil, fmt.Errorf("computer: supply action or actions, not both")
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("computer: actions must be an array of objects")
	}
	limit := s.config.MaxBatchActions
	if limit <= 0 {
		limit = 5
	}
	if len(items) == 0 || len(items) > min(limit, 10) {
		return nil, fmt.Errorf("computer: actions requires 1..%d items", min(limit, 10))
	}
	actions := make([]computer.Action, 0, len(items))
	for _, item := range items {
		values, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("computer: each action must be an object")
		}
		argsCopy := make(map[string]any, len(values)+2)
		for key, value := range values {
			argsCopy[key] = value
		}
		argsCopy["reason"] = args["reason"]
		for _, key := range []string{"frame_id", "display_id"} {
			if value := computerStringArg(values, key); value != "" && value != computerStringArg(args, key) {
				return nil, fmt.Errorf("computer: batch %s must match the outer value", key)
			}
			argsCopy[key] = args[key]
		}
		action, err := parseComputerAction(argsCopy)
		if err != nil {
			return nil, err
		}
		if err := action.Validate(); err != nil {
			return nil, err
		}
		actions = append(actions, action)
	}
	return actions, nil
}

func strictComputerInt(value any) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, true
	case int64:
		return int(v), int64(int(v)) == v
	case float64:
		return int(v), !math.IsNaN(v) && !math.IsInf(v, 0) && math.Trunc(v) == v && v >= float64(math.MinInt32) && v <= float64(math.MaxInt32)
	default:
		return 0, false
	}
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
		"frame_id":       obs.FrameID,
		"captured_at":    obs.CapturedAt,
		"width":          obs.Width,
		"height":         obs.Height,
		"scale_factor":   obs.ScaleFactor,
		"display_id":     obs.DisplayID,
		"active_window":  obs.ActiveWindow,
		"sha256":         obs.SHA256,
		"origin_x":       obs.OriginX,
		"origin_y":       obs.OriginY,
		"cursor_x":       obs.CursorX,
		"cursor_y":       obs.CursorY,
		"cursor_visible": obs.CursorVisible,
		"window_id":      obs.WindowID,
		"capture_bounds": obs.CaptureBounds,
		"stable":         obs.Stable,
	}
	result := ToolCallResult{
		Output:   fmt.Sprintf("Observed frame=%s size=%dx%d capture=%dx%d display=%s active_window=%q window_id=%s origin=(%d,%d) cursor=(%d,%d visible=%t) stable=%t", obs.FrameID, obs.Width, obs.Height, obs.CaptureBounds.Width, obs.CaptureBounds.Height, obs.DisplayID, obs.ActiveWindow, obs.WindowID, obs.OriginX, obs.OriginY, obs.CursorX, obs.CursorY, obs.CursorVisible, obs.Stable),
		Metadata: meta,
		Observations: []Observation{{
			Kind: "image", FrameID: obs.FrameID, CapturedAt: obs.CapturedAt, FilePath: obs.FilePath,
			MimeType: obs.MimeType, Width: obs.Width, Height: obs.Height, ScaleFactor: obs.ScaleFactor,
			DisplayID: obs.DisplayID, ActiveWindow: obs.ActiveWindow,
			WindowBounds: Rect{X: obs.WindowBounds.X, Y: obs.WindowBounds.Y, Width: obs.WindowBounds.Width, Height: obs.WindowBounds.Height},
			SHA256:       obs.SHA256, ImageData: obs.ImageData,
			Metadata: map[string]any{"cursor_x": obs.CursorX, "cursor_y": obs.CursorY, "cursor_visible": obs.CursorVisible},
		}},
	}
	if obs.FilePath == "" && len(obs.ImageData) == 0 {
		result.Observations = nil
	}
	if obs.Accessibility != nil {
		meta["accessibility"] = obs.Accessibility
		data, _ := json.Marshal(obs.Accessibility)
		result.Output += "\nAccessibility (use element IDs from this frame): " + string(data)
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
		ClickCount: computerIntArg(args, "click_count"),
		Text:       rawComputerText(args), Keys: computerStringSliceArg(args, "keys"), DurationMS: computerIntArg(args, "duration_ms"),
		ElementID: computerStringArg(args, "element_id"), ElementAction: computerStringArg(args, "element_action"),
	}
	if action.FrameID == "" {
		return computer.Action{}, fmt.Errorf("computer: frame_id is required")
	}
	if action.Kind == "" {
		return computer.Action{}, fmt.Errorf("computer: action is required")
	}
	for _, key := range []string{"x", "y", "end_x", "end_y", "delta_x", "delta_y", "duration_ms", "click_count"} {
		if value, exists := args[key]; exists {
			if _, valid := strictComputerInt(value); !valid {
				return computer.Action{}, fmt.Errorf("computer: %s must be an integer", key)
			}
		}
	}
	if action.Kind == computer.ActionClick || action.Kind == computer.ActionDoubleClick || action.Kind == computer.ActionMove || action.Kind == computer.ActionDrag {
		keys := []string{"x", "y"}
		if action.Kind == computer.ActionDrag {
			keys = append(keys, "end_x", "end_y")
		}
		for _, key := range keys {
			if _, exists := args[key]; !exists {
				return computer.Action{}, fmt.Errorf("computer: %s is required for %s", key, action.Kind)
			}
		}
	}
	if action.Kind == computer.ActionTypeText || action.Kind == computer.ActionSetText {
		if _, ok := args["text"].(string); !ok {
			return computer.Action{}, fmt.Errorf("computer: text must be supplied as a string (empty is allowed for set_text)")
		}
	}
	return action, nil
}

func rawComputerText(args map[string]any) string { text, _ := args["text"].(string); return text }

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
