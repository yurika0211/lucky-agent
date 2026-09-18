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
	WindowID      string             `json:"window_id,omitempty"`
	Accessibility *AccessibilityTree `json:"accessibility,omitempty"`
	Stable        bool               `json:"stable,omitempty"`

	// Original capture dimensions, retained by Manager for action mapping when
	// the delivered screenshot has been downscaled. Zero means no transform.
	sourceWidth  int
	sourceHeight int
	windowScoped bool
}
