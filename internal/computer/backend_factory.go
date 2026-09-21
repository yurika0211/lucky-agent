package computer

import (
	"fmt"
	"os"
	"runtime"
	"strings"
)

// NewBackend selects the platform backend without making the agent package
// depend on platform-specific implementation details.
//
// The first implementation exposed X11 on Linux. Windows now uses native
// Win32 screen capture and input; Wayland uses the desktop portal.
type BackendOptions struct{ ObserveOnly bool }

func NewBackend(name string, options ...BackendOptions) (Backend, error) {
	observeOnly := len(options) > 0 && options[0].ObserveOnly
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" || name == "auto" {
		switch runtime.GOOS {
		case "linux":
			if strings.TrimSpace(os.Getenv("WAYLAND_DISPLAY")) != "" || strings.EqualFold(os.Getenv("XDG_SESSION_TYPE"), "wayland") {
				return NewWaylandBackend(observeOnly)
			}
			return NewX11Backend(), nil
		case "windows":
			return NewWindowsBackend()
		case "darwin":
			return nil, fmt.Errorf("computer: macOS backend is not available yet")
		default:
			return nil, fmt.Errorf("computer: unsupported platform %q", runtime.GOOS)
		}
	}

	switch name {
	case "x11":
		if runtime.GOOS != "linux" {
			return nil, fmt.Errorf("computer: x11 backend requires linux, got %s", runtime.GOOS)
		}
		return NewX11Backend(), nil
	case "wayland":
		return NewWaylandBackend(observeOnly)
	case "windows", "win32":
		return NewWindowsBackend()
	case "darwin", "macos", "mac":
		return nil, fmt.Errorf("computer: macOS backend is not available yet")
	default:
		return nil, fmt.Errorf("computer: unsupported backend %q", name)
	}
}
