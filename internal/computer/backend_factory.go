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
// Win32 screen capture and input; Wayland uses the desktop portal. WSLg uses
// a fixed PowerShell/Win32 bridge because its Weston RDP compositor does not
// expose the desktop portals required by the native Wayland backend.
type BackendOptions struct {
	ObserveOnly    bool
	PowerShellPath string
}

func NewBackend(name string, options ...BackendOptions) (Backend, error) {
	observeOnly := len(options) > 0 && options[0].ObserveOnly
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" || name == "auto" {
		switch runtime.GOOS {
		case "linux":
			if isWSLGSession() {
				return newWSLGBackend(optionsPowerShellPath(options), observeOnly)
			}
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
	case "wslg", "wsl":
		if runtime.GOOS != "linux" {
			return nil, fmt.Errorf("computer: wslg backend requires linux, got %s", runtime.GOOS)
		}
		return newWSLGBackend(optionsPowerShellPath(options), observeOnly)
	case "windows", "win32":
		return NewWindowsBackend()
	case "darwin", "macos", "mac":
		return nil, fmt.Errorf("computer: macOS backend is not available yet")
	default:
		return nil, fmt.Errorf("computer: unsupported backend %q", name)
	}
}

func optionsPowerShellPath(options []BackendOptions) string {
	if len(options) == 0 {
		return ""
	}
	return strings.TrimSpace(options[0].PowerShellPath)
}

func isWSLGSession() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	if strings.TrimSpace(os.Getenv("WAYLAND_DISPLAY")) == "" || strings.TrimSpace(os.Getenv("WSL_INTEROP")) == "" {
		return false
	}
	return os.Getenv("WSL2_GUI_APPS_ENABLED") == "1" || strings.TrimSpace(os.Getenv("WSL_DISTRO_NAME")) != ""
}
