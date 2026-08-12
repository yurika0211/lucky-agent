package computer

import (
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/png"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// X11Config allows tests and distributions to provide explicit binaries.
type X11Config struct {
	Display          string
	ScreenshotBinary string
	InputBinary      string
}

type X11Backend struct {
	display string
	screen  string
	input   string
}

func NewX11Backend(cfg ...X11Config) *X11Backend {
	c := X11Config{}
	if len(cfg) > 0 {
		c = cfg[0]
	}
	if c.Display == "" {
		c.Display = os.Getenv("DISPLAY")
	}
	if c.ScreenshotBinary == "" {
		c.ScreenshotBinary = "import"
	}
	if c.InputBinary == "" {
		c.InputBinary = "xdotool"
	}
	return &X11Backend{display: c.Display, screen: c.ScreenshotBinary, input: c.InputBinary}
}

func (b *X11Backend) Name() string { return "x11" }

func (b *X11Backend) Capabilities(ctx context.Context) (Capabilities, error) {
	if runtime.GOOS != "linux" {
		return Capabilities{}, fmt.Errorf("computer: x11 backend requires linux, got %s", runtime.GOOS)
	}
	if b.display == "" {
		return Capabilities{}, errors.New("computer: x11 backend requires DISPLAY")
	}
	if _, err := exec.LookPath(b.screen); err != nil {
		return Capabilities{}, fmt.Errorf("computer: x11 screenshot requires %q (ImageMagick import): %w", b.screen, err)
	}
	if _, err := exec.LookPath(b.input); err != nil {
		return Capabilities{Capture: true, BackendDetail: "screenshot only"}, fmt.Errorf("computer: x11 input requires %q (xdotool): %w", b.input, err)
	}
	return Capabilities{Capture: true, Click: true, DoubleClick: true, Move: true, Drag: true, TypeText: true, Keypress: true, Scroll: true, ScaleFactor: 1, BackendDetail: "ImageMagick import + xdotool"}, nil
}

func (b *X11Backend) Capture(ctx context.Context, target Target) (Observation, error) {
	if b.display == "" {
		return Observation{}, errors.New("computer: x11 screenshot unavailable: DISPLAY is not set")
	}
	if _, err := exec.LookPath(b.screen); err != nil {
		return Observation{}, fmt.Errorf("computer: x11 screenshot requires %q (ImageMagick import): %w", b.screen, err)
	}
	if target.Window != "" {
		return Observation{}, fmt.Errorf("computer: x11 window-scoped capture is not implemented: %q", target.Window)
	}
	f, err := os.CreateTemp("", "luckyagent-computer-*.png")
	if err != nil {
		return Observation{}, fmt.Errorf("computer: create screenshot temp file: %w", err)
	}
	path := f.Name()
	_ = f.Chmod(0600)
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return Observation{}, err
	}
	// Target.DisplayID is a logical display selector. It is not an X11
	// connection string, so never substitute it for the backend DISPLAY value.
	args := []string{"-display", b.display, "-window", "root", path}
	cmd := exec.CommandContext(ctx, b.screen, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = os.Remove(path)
		return Observation{}, fmt.Errorf("computer: x11 screenshot command failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	obs := Observation{FilePath: path, MimeType: "image/png", DisplayID: target.DisplayID, CleanupFile: true}
	if obs.DisplayID == "" {
		obs.DisplayID = b.display
	}
	if f, err := os.Open(path); err == nil {
		if cfg, err := f.Stat(); err == nil && cfg.Size() == 0 {
			_ = f.Close()
			_ = os.Remove(path)
			return Observation{}, errors.New("computer: x11 screenshot command produced an empty file")
		}
		if decoded, err := decodeImageBounds(f); err == nil {
			obs.Width, obs.Height = decoded.Dx(), decoded.Dy()
		}
		_ = f.Close()
	}
	return obs, nil
}

func decodeImageBounds(f *os.File) (image.Rectangle, error) {
	if _, err := f.Seek(0, 0); err != nil {
		return image.Rectangle{}, err
	}
	img, _, err := image.DecodeConfig(f)
	if err != nil {
		return image.Rectangle{}, err
	}
	return image.Rect(0, 0, img.Width, img.Height), nil
}

func (b *X11Backend) Perform(ctx context.Context, action Action) error {
	if err := action.Validate(); err != nil {
		return err
	}
	if _, err := exec.LookPath(b.input); err != nil {
		return fmt.Errorf("computer: x11 input requires %q (xdotool): %w", b.input, err)
	}
	button := map[string]string{"left": "1", "middle": "2", "right": "3"}[action.buttonOrDefault()]
	args := []string{}
	switch action.Kind {
	case ActionMove:
		args = []string{"mousemove", "--sync", strconv.Itoa(action.X), strconv.Itoa(action.Y)}
	case ActionClick:
		args = []string{"mousemove", "--sync", strconv.Itoa(action.X), strconv.Itoa(action.Y), "click", "--repeat", "1", "--button", button}
	case ActionDoubleClick:
		args = []string{"mousemove", "--sync", strconv.Itoa(action.X), strconv.Itoa(action.Y), "click", "--repeat", "2", "--delay", "80", "--button", button}
	case ActionDrag:
		args = []string{"mousemove", "--sync", strconv.Itoa(action.X), strconv.Itoa(action.Y), "mousedown", button, "mousemove", "--sync", strconv.Itoa(action.EndX), strconv.Itoa(action.EndY), "mouseup", button}
	case ActionTypeText:
		args = []string{"type", "--clearmodifiers", "--", action.Text}
	case ActionKeypress:
		keys := make([]string, len(action.Keys))
		for i, key := range action.Keys {
			keys[i] = normalizeXDoToolKey(key)
		}
		args = []string{"key", strings.Join(keys, "+")}
	case ActionScroll:
		buttonNum := "5"
		count := action.DeltaY
		if count < 0 {
			buttonNum, count = "4", -count
		} else if count == 0 {
			count = action.DeltaX
			if count < 0 {
				buttonNum, count = "6", -count
			} else {
				buttonNum = "7"
			}
		}
		args = []string{"click", "--repeat", strconv.Itoa(count), "--button", buttonNum}
	}
	cmd := exec.CommandContext(ctx, b.input, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("computer: x11 input command failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func normalizeXDoToolKey(key string) string {
	key = strings.TrimSpace(key)
	if len(key) == 1 {
		return key
	}
	key = strings.ToLower(strings.ReplaceAll(key, " ", "_"))
	switch key {
	case "ctrl", "control":
		return "ctrl"
	case "alt", "option", "opt":
		return "alt"
	case "shift":
		return "shift"
	case "cmd", "command", "meta", "win", "windows", "super":
		return "super"
	case "esc", "escape":
		return "Escape"
	case "enter", "return":
		return "Return"
	case "backspace", "back_space":
		return "BackSpace"
	case "delete", "del":
		return "Delete"
	case "tab":
		return "Tab"
	case "space", "spacebar":
		return "space"
	case "pageup", "page_up":
		return "Page_Up"
	case "pagedown", "page_down":
		return "Page_Down"
	default:
		return key
	}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func (b *X11Backend) Close() error { return nil }
