package computer

import (
	"fmt"
	"strings"
)

// ActionKind identifies one atomic desktop operation.
type ActionKind string

const (
	ActionClick       ActionKind = "click"
	ActionDoubleClick ActionKind = "double_click"
	ActionMove        ActionKind = "move"
	ActionDrag        ActionKind = "drag"
	ActionTypeText    ActionKind = "type"
	ActionKeypress    ActionKind = "keypress"
	ActionScroll      ActionKind = "scroll"
	// Short aliases keep adapters readable while preserving the wire names.
	ActionType = ActionTypeText
	ActionKey  = ActionKeypress
)

// Action is an atomic operation in the observation coordinate system.
// FrameID is checked by Manager before the operation reaches a backend.
type Action struct {
	Kind       ActionKind `json:"kind"`
	FrameID    string     `json:"frame_id,omitempty"`
	DisplayID  string     `json:"display_id,omitempty"`
	X          int        `json:"x,omitempty"`
	Y          int        `json:"y,omitempty"`
	EndX       int        `json:"end_x,omitempty"`
	EndY       int        `json:"end_y,omitempty"`
	DeltaX     int        `json:"delta_x,omitempty"`
	DeltaY     int        `json:"delta_y,omitempty"`
	Button     string     `json:"button,omitempty"`
	Text       string     `json:"text,omitempty"`
	Keys       []string   `json:"keys,omitempty"`
	DurationMS int        `json:"duration_ms,omitempty"`
}

// Validate checks action shape without consulting the current desktop frame.
func (a Action) Validate() error {
	if strings.TrimSpace(string(a.Kind)) == "" {
		return fmt.Errorf("computer: action kind is required")
	}
	switch a.Kind {
	case ActionClick, ActionDoubleClick, ActionMove:
		if a.X < 0 || a.Y < 0 {
			return fmt.Errorf("computer: %s coordinates must be non-negative", a.Kind)
		}
	case ActionDrag:
		if a.X < 0 || a.Y < 0 || a.EndX < 0 || a.EndY < 0 {
			return fmt.Errorf("computer: drag coordinates must be non-negative")
		}
	case ActionTypeText:
		if a.Text == "" {
			return fmt.Errorf("computer: type action requires text")
		}
	case ActionKeypress:
		if len(a.Keys) == 0 {
			return fmt.Errorf("computer: keypress action requires keys")
		}
		for _, key := range a.Keys {
			if strings.TrimSpace(key) == "" {
				return fmt.Errorf("computer: keypress contains an empty key")
			}
		}
	case ActionScroll:
		if a.DeltaX == 0 && a.DeltaY == 0 {
			return fmt.Errorf("computer: scroll action requires a non-zero delta")
		}
	default:
		return fmt.Errorf("computer: unsupported action kind %q", a.Kind)
	}
	if a.DurationMS < 0 {
		return fmt.Errorf("computer: duration_ms must be non-negative")
	}
	if a.Button != "" && a.Button != "left" && a.Button != "middle" && a.Button != "right" {
		return fmt.Errorf("computer: unsupported mouse button %q", a.Button)
	}
	return nil
}

func (a Action) buttonOrDefault() string {
	if a.Button == "" {
		return "left"
	}
	return a.Button
}
