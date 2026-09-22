package computer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
)

// WaylandBackend uses the desktop portal's user-selected monitor and input
// session. It never falls back to XWayland's incomplete view of the desktop.
type WaylandBackend struct {
	helper      *desktopBridge
	observeOnly bool
}

func NewWaylandBackend(observeOnly bool) (*WaylandBackend, error) {
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("computer: wayland requires linux, got %s", runtime.GOOS)
	}
	return &WaylandBackend{helper: newDesktopBridge(""), observeOnly: observeOnly}, nil
}
func (b *WaylandBackend) Name() string { return "wayland" }
func (b *WaylandBackend) Capabilities(ctx context.Context) (Capabilities, error) {
	err := b.helper.call(ctx, map[string]any{"op": "wayland_probe", "observe_only": b.observeOnly}, nil)
	if err != nil {
		return Capabilities{}, err
	}
	control := !b.observeOnly
	return Capabilities{Capture: true, Click: control, DoubleClick: control, Move: control, Drag: control, TypeText: control, Keypress: control, Scroll: control, Accessibility: true, ScaleFactor: 1, BackendDetail: "XDG Desktop Portal + PipeWire; one user-selected monitor; Python GI/GStreamer required"}, nil
}
func (b *WaylandBackend) Capture(ctx context.Context, target Target) (Observation, error) {
	if os.Getenv("WAYLAND_DISPLAY") == "" {
		return Observation{}, errors.New("computer: Wayland capture requires WAYLAND_DISPLAY in the graphical user session")
	}
	if target.Window != "" {
		return Observation{}, errors.New("computer: Wayland visual capture uses the portal-selected monitor; use region for a smaller image or format=tree for a named window")
	}
	f, err := os.CreateTemp("", "luckyagent-wayland-*.png")
	if err != nil {
		return Observation{}, err
	}
	path := f.Name()
	_ = f.Close()
	obs := Observation{FilePath: path, MimeType: "image/png", CleanupFile: true}
	err = b.helper.call(ctx, map[string]any{"op": "wayland_capture", "path": path, "observe_only": b.observeOnly, "display_id": target.DisplayID}, &obs)
	if err != nil {
		_ = os.Remove(path)
		return Observation{}, err
	}
	obs.FilePath, obs.MimeType, obs.CleanupFile = path, "image/png", true
	return obs, nil
}
func (b *WaylandBackend) Perform(ctx context.Context, action Action) error {
	if b.observeOnly {
		return errors.New("computer: Wayland input is disabled in observe mode")
	}
	if err := action.Validate(); err != nil {
		return err
	}
	op := "wayland_action"
	if action.ElementID != "" {
		op = "a11y_action"
	}
	return b.helper.call(ctx, map[string]any{"op": op, "action": action}, nil)
}

func (b *WaylandBackend) ValidateObservation(ctx context.Context, obs Observation, action Action) error {
	if action.ElementID != "" {
		return nil
	}
	return b.helper.call(ctx, map[string]any{"op": "wayland_validate", "display_id": obs.DisplayID, "width": obs.CaptureBounds.Width, "height": obs.CaptureBounds.Height}, nil)
}
func (b *WaylandBackend) Accessibility(ctx context.Context, target Target) (AccessibilityTree, error) {
	var tree AccessibilityTree
	err := b.helper.call(ctx, map[string]any{"op": "a11y_observe", "window": target.Window}, &tree)
	return tree, err
}
func (b *WaylandBackend) Close() error { return b.helper.Close() }
