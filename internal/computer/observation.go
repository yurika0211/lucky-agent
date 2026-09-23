package computer

import "time"

// Rect is a screen/window rectangle in physical pixels.
type Rect struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// Target scopes an observation. Empty Target means the active/root display.
type Target struct {
	DisplayID string `json:"display_id,omitempty"`
	Window    string `json:"window,omitempty"`
	// Region is relative to the selected window/display, before downscaling.
	Region *Rect `json:"region,omitempty"`
}

type ObserveRequest struct {
	Target Target
	Wait   time.Duration
	Format string // image (default), tree, or both
}

// ActionExecution records the backend-visible outcome of one action. It is
// attached to the post-action observation so callers can distinguish a missing
// act call, a rejected act call, and an executed action that did not produce
// the expected UI change.
type ActionExecution struct {
	RequestID          string     `json:"request_id"`
	Index              int        `json:"index"`
	Kind               ActionKind `json:"kind"`
	StartedAt          time.Time  `json:"started_at"`
	CompletedAt        time.Time  `json:"completed_at"`
	DurationMS         int64      `json:"duration_ms"`
	InputX             int        `json:"input_x,omitempty"`
	InputY             int        `json:"input_y,omitempty"`
	InputEndX          int        `json:"input_end_x,omitempty"`
	InputEndY          int        `json:"input_end_y,omitempty"`
	BackendX           int        `json:"backend_x,omitempty"`
	BackendY           int        `json:"backend_y,omitempty"`
	BackendEndX        int        `json:"backend_end_x,omitempty"`
	BackendEndY        int        `json:"backend_end_y,omitempty"`
	ActiveWindowBefore string     `json:"active_window_before,omitempty"`
	ActiveWindowAfter  string     `json:"active_window_after,omitempty"`
	CursorBeforeX      int        `json:"cursor_before_x,omitempty"`
	CursorBeforeY      int        `json:"cursor_before_y,omitempty"`
	CursorAfterX       int        `json:"cursor_after_x,omitempty"`
	CursorAfterY       int        `json:"cursor_after_y,omitempty"`
	Completed          bool       `json:"completed"`
	Error              string     `json:"error,omitempty"`
}

// Observation is a persisted visual frame returned by a backend.
// After Manager processing, Width and Height describe the delivered image and
// ScaleFactor includes any downscaling. Actions use the delivered image's pixels;
// Manager maps them back to the original capture before calling the backend.
// ImageData is accepted by test and in-process backends; normal backends should
// set FilePath so large image bytes never enter an agent message.
type Observation struct {
	FrameID      string    `json:"frame_id"`
	CapturedAt   time.Time `json:"captured_at"`
	FilePath     string    `json:"file_path,omitempty"`
	MimeType     string    `json:"mime_type,omitempty"`
	Width        int       `json:"width,omitempty"`
	Height       int       `json:"height,omitempty"`
	ScaleFactor  float64   `json:"scale_factor,omitempty"`
	DisplayID    string    `json:"display_id,omitempty"`
	ActiveWindow string    `json:"active_window,omitempty"`
	WindowBounds Rect      `json:"window_bounds,omitempty"`
	// CaptureBounds describes the backend frame before region selection/resizing.
	CaptureBounds Rect               `json:"capture_bounds"`
	SHA256        string             `json:"sha256,omitempty"`
	ImageData     []byte             `json:"-"`
	CleanupFile   bool               `json:"-"`
	OriginX       int                `json:"origin_x,omitempty"`
	OriginY       int                `json:"origin_y,omitempty"`
	CursorX       int                `json:"cursor_x,omitempty"`
	CursorY       int                `json:"cursor_y,omitempty"`
	CursorVisible bool               `json:"cursor_visible,omitempty"`
	WindowID      string             `json:"window_id,omitempty"`
	Accessibility *AccessibilityTree `json:"accessibility,omitempty"`
	Stable        bool               `json:"stable,omitempty"`
	ActionResults []ActionExecution  `json:"action_results,omitempty"`

	// Original capture dimensions, retained by Manager for action mapping when
	// the delivered screenshot has been downscaled. Zero means no transform.
	sourceWidth  int
	sourceHeight int
	windowScoped bool
}
