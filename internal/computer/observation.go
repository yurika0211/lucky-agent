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
}

type ObserveRequest struct {
	Target Target
	Wait   time.Duration
}

// Observation is a persisted visual frame returned by a backend.
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
	SHA256       string    `json:"sha256,omitempty"`
	ImageData    []byte    `json:"-"`
	CleanupFile  bool      `json:"-"`
}
