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
// The first implementation intentionally exposes X11 on Linux. Wayland,
// Windows, and macOS return an explicit error until their permission-aware
// backends are added.
func NewBackend(name string) (Backend, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" || name == "auto" {
		switch runtime.GOOS {
		case "linux":
			if strings.TrimSpace(os.Getenv("WAYLAND_DISPLAY")) != "" && strings.TrimSpace(os.Getenv("DISPLAY")) == "" {
				return nil, fmt.Errorf("computer: wayland backend is not available yet")
			}
			return NewX11Backend(), nil
		case "windows":
			return nil, fmt.Errorf("computer: windows backend is not available yet")
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
		return nil, fmt.Errorf("computer: wayland backend is not available yet")
	case "windows", "win32":
		return nil, fmt.Errorf("computer: windows backend is not available yet")
	case "darwin", "macos", "mac":
		return nil, fmt.Errorf("computer: macOS backend is not available yet")
	default:
		return nil, fmt.Errorf("computer: unsupported backend %q", name)
	}
}
