package computer

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// WSLGBackend controls the Windows virtual desktop exposed by WSLg. WSLg's
// Weston RDP compositor does not publish the ScreenCast or RemoteDesktop
// portals, so a Linux process cannot use the native Wayland backend there.
// The embedded PowerShell helper uses fixed Win32 APIs and receives one JSON
// request file per operation; model supplied values are never placed in a
// shell command string.
type WSLGBackend struct {
	bridge      *wslgBridge
	desktop     *desktopBridge
	observeOnly bool
}

type wslgProbeResult struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

type wslgCaptureResult struct {
	Width         int    `json:"width"`
	Height        int    `json:"height"`
	DisplayID     string `json:"display_id"`
	ActiveWindow  string `json:"active_window"`
	WindowBounds  Rect   `json:"window_bounds"`
	CaptureBounds Rect   `json:"capture_bounds"`
	CursorX       int    `json:"cursor_x"`
	CursorY       int    `json:"cursor_y"`
	CursorVisible bool   `json:"cursor_visible"`
	PNGBase64     string `json:"png_base64"`
}

func NewWSLGBackend(observeOnly bool) (Backend, error) {
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("computer: wslg requires linux, got %s", runtime.GOOS)
	}
	return newWSLGBackend("", observeOnly)
}

func newWSLGBackend(powerShellPath string, observeOnly bool) (Backend, error) {
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("computer: wslg requires linux, got %s", runtime.GOOS)
	}
	path, err := resolveWSLGPowerShell(powerShellPath)
	if err != nil {
		return nil, err
	}
	return &WSLGBackend{
		bridge:      newWSLGBridge(path),
		desktop:     newDesktopBridge(""),
		observeOnly: observeOnly,
	}, nil
}

func resolveWSLGPowerShell(configured string) (string, error) {
	candidates := []string{}
	for _, value := range []string{configured, os.Getenv("LA_WSLG_POWERSHELL"), "powershell.exe", "/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe", "/mnt/c/Windows/System32/pwsh.exe"} {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		candidates = append(candidates, value)
	}
	for _, candidate := range candidates {
		if filepath.IsAbs(candidate) {
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate, nil
			}
			continue
		}
		if path, err := exec.LookPath(candidate); err == nil {
			return path, nil
		}
	}
	return "", errors.New("computer: WSLg requires powershell.exe; set LA_WSLG_POWERSHELL or run LA from a WSLg session with Windows interop enabled")
}

func (b *WSLGBackend) Name() string { return "wslg" }

func (b *WSLGBackend) Capabilities(ctx context.Context) (Capabilities, error) {
	var probe wslgProbeResult
	if err := b.bridge.call(ctx, map[string]any{"op": "probe"}, &probe); err != nil {
		return Capabilities{}, err
	}
	if probe.Width <= 0 || probe.Height <= 0 {
		return Capabilities{}, fmt.Errorf("computer: WSLg reported invalid virtual desktop %dx%d", probe.Width, probe.Height)
	}
	control := !b.observeOnly
	return Capabilities{
		Capture: true, Click: control, DoubleClick: control, Move: control,
		Drag: control, TypeText: control, Keypress: control, Scroll: control,
		MultiDisplay: true, Accessibility: true, ScaleFactor: 1,
		BackendDetail: "WSLg Windows virtual desktop via PowerShell/Win32; full host screen",
	}, nil
}

func (b *WSLGBackend) Capture(ctx context.Context, target Target) (Observation, error) {
	if strings.TrimSpace(target.Window) != "" {
		return Observation{}, fmt.Errorf("computer: WSLg captures the full Windows virtual desktop; window-scoped capture is not supported: %q", target.Window)
	}
	if displayID := strings.TrimSpace(target.DisplayID); displayID != "" && displayID != "wslg:virtual" {
		return Observation{}, fmt.Errorf("computer: WSLg only supports display_id %q, got %q", "wslg:virtual", displayID)
	}
	var result wslgCaptureResult
	if err := b.bridge.call(ctx, map[string]any{"op": "capture"}, &result); err != nil {
		return Observation{}, err
	}
	imageData, err := base64.StdEncoding.DecodeString(strings.TrimSpace(result.PNGBase64))
	if err != nil {
		return Observation{}, fmt.Errorf("computer: decode WSLg screenshot: %w", err)
	}
	if len(imageData) == 0 || result.Width <= 0 || result.Height <= 0 {
		return Observation{}, errors.New("computer: WSLg returned an empty screenshot")
	}
	captureBounds := result.CaptureBounds
	if captureBounds.Width <= 0 || captureBounds.Height <= 0 {
		captureBounds = Rect{X: result.CaptureBounds.X, Y: result.CaptureBounds.Y, Width: result.Width, Height: result.Height}
	}
	displayID := strings.TrimSpace(result.DisplayID)
	if displayID == "" {
		displayID = "wslg:virtual"
	}
	cursorX, cursorY := 0, 0
	if result.CursorVisible {
		cursorX = result.CursorX - captureBounds.X
		cursorY = result.CursorY - captureBounds.Y
	}
	return Observation{
		ImageData:     imageData,
		MimeType:      "image/png",
		Width:         result.Width,
		Height:        result.Height,
		ScaleFactor:   1,
		DisplayID:     displayID,
		ActiveWindow:  result.ActiveWindow,
		WindowBounds:  result.WindowBounds,
		CaptureBounds: captureBounds,
		OriginX:       captureBounds.X,
		OriginY:       captureBounds.Y,
		CursorX:       cursorX,
		CursorY:       cursorY,
		CursorVisible: result.CursorVisible,
	}, nil
}

func (b *WSLGBackend) ValidateObservation(ctx context.Context, obs Observation, action Action) error {
	if action.ElementID != "" {
		return nil
	}
	var probe wslgProbeResult
	if err := b.bridge.call(ctx, map[string]any{"op": "probe"}, &probe); err != nil {
		return err
	}
	current := Rect{X: probe.X, Y: probe.Y, Width: probe.Width, Height: probe.Height}
	if current != obs.CaptureBounds {
		return fmt.Errorf("%w: WSLg virtual desktop geometry changed", ErrStaleFrame)
	}
	return nil
}

func (b *WSLGBackend) Perform(ctx context.Context, action Action) error {
	if b.observeOnly {
		return errors.New("computer: WSLg input is disabled in observe mode")
	}
	if err := action.Validate(); err != nil {
		return err
	}
	if action.ElementID != "" {
		return b.desktop.call(ctx, map[string]any{"op": "a11y_action", "action": action}, nil)
	}
	return b.bridge.call(ctx, map[string]any{"op": "action", "action": action}, nil)
}

func (b *WSLGBackend) Accessibility(ctx context.Context, target Target) (AccessibilityTree, error) {
	var tree AccessibilityTree
	err := b.desktop.call(ctx, map[string]any{"op": "a11y_observe", "window": target.Window}, &tree)
	if err != nil {
		return AccessibilityTree{}, fmt.Errorf("computer: WSLg AT-SPI observation: %w", err)
	}
	return tree, nil
}

func (b *WSLGBackend) Close() error {
	var first error
	if b.bridge != nil {
		if err := b.bridge.Close(); err != nil {
			first = err
		}
	}
	if b.desktop != nil {
		if err := b.desktop.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}
