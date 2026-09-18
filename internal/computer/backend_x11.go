package computer

import (
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/png"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// X11Config allows tests and distributions to provide explicit binaries.
type X11Config struct {
	Display          string
	ScreenshotBinary string
	InputBinary      string
	PythonBinary     string
}

type X11Backend struct {
	display string
	screen  string
	input   string
	helper  *desktopBridge
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
	return &X11Backend{display: c.Display, screen: c.ScreenshotBinary, input: c.InputBinary, helper: newDesktopBridge(c.PythonBinary)}
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
	return Capabilities{Capture: true, Click: true, DoubleClick: true, Move: true, Drag: true, TypeText: true, Keypress: true, Scroll: true, WindowCapture: true, Accessibility: true, ScaleFactor: 1, BackendDetail: "ImageMagick import + xdotool; optional AT-SPI via Python GI"}, nil
}

func (b *X11Backend) Capture(ctx context.Context, target Target) (Observation, error) {
	if b.display == "" {
		return Observation{}, errors.New("computer: x11 screenshot unavailable: DISPLAY is not set")
	}
	if _, err := exec.LookPath(b.screen); err != nil {
		return Observation{}, fmt.Errorf("computer: x11 screenshot requires %q (ImageMagick import): %w", b.screen, err)
	}
	if target.DisplayID != "" && target.DisplayID != b.display {
		return Observation{}, fmt.Errorf("computer: x11 captures the root display %q; unknown display_id %q", b.display, target.DisplayID)
	}
	windowID, title, bounds, windowErr := b.windowDetails(ctx, target.Window)
	if target.Window != "" && windowErr != nil {
		return Observation{}, windowErr
	}
	var crop image.Rectangle
	root, rootErr := b.rootBounds(ctx)
	if target.Window != "" {
		if rootErr != nil {
			return Observation{}, rootErr
		}
		crop = image.Rect(bounds.X, bounds.Y, bounds.X+bounds.Width, bounds.Y+bounds.Height).Intersect(root)
		if crop.Empty() {
			return Observation{}, errors.New("computer: target window is outside the visible desktop")
		}
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
	args := []string{"-display", b.display, "-window", "root"}
	if !crop.Empty() {
		args = append(args, "-crop", fmt.Sprintf("%dx%d+%d+%d", crop.Dx(), crop.Dy(), crop.Min.X, crop.Min.Y), "+repage")
	}
	args = append(args, path)
	cmd := exec.CommandContext(ctx, b.screen, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = os.Remove(path)
		return Observation{}, fmt.Errorf("computer: x11 screenshot command failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	obs := Observation{FilePath: path, MimeType: "image/png", DisplayID: target.DisplayID, CleanupFile: true, ActiveWindow: title, WindowBounds: bounds, WindowID: windowID, OriginX: crop.Min.X, OriginY: crop.Min.Y, windowScoped: target.Window != ""}
	if obs.DisplayID == "" {
		obs.DisplayID = b.display
	}
	frame, err := os.Open(path)
	if err != nil {
		discardCapture(obs)
		return Observation{}, err
	}
	decoded, decodeErr := decodeImageBounds(frame)
	_ = frame.Close()
	if decodeErr != nil {
		discardCapture(obs)
		return Observation{}, fmt.Errorf("computer: invalid x11 screenshot: %w", decodeErr)
	}
	obs.Width, obs.Height = decoded.Dx(), decoded.Dy()
	expected := root
	if target.Window != "" {
		expected = crop
	}
	if rootErr == nil && (obs.Width != expected.Dx() || obs.Height != expected.Dy()) {
		discardCapture(obs)
		return Observation{}, fmt.Errorf("computer: X11 screenshot %dx%d does not match requested display/window %dx%d; check DISPLAY and the session backend", obs.Width, obs.Height, expected.Dx(), expected.Dy())
	}
	return obs, nil
}

func (b *X11Backend) rootBounds(ctx context.Context) (image.Rectangle, error) {
	out, err := b.runInput(ctx, "getdisplaygeometry")
	if err != nil {
		return image.Rectangle{}, err
	}
	var width, height int
	if _, err := fmt.Sscanf(out, "%d %d", &width, &height); err != nil || width <= 0 || height <= 0 {
		return image.Rectangle{}, errors.New("computer: invalid root display geometry")
	}
	return image.Rect(0, 0, width, height), nil
}

func (b *X11Backend) runInput(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, b.input, args...)
	cmd.Env = append(os.Environ(), "DISPLAY="+b.display)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("computer: x11 command %s: %w (%s)", args[0], err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func (b *X11Backend) windowDetails(ctx context.Context, selector string) (string, string, Rect, error) {
	id := strings.TrimSpace(selector)
	var err error
	if id == "" || id == "active" {
		id, err = b.runInput(ctx, "getactivewindow")
	} else if n, parseErr := strconv.ParseUint(id, 0, 64); parseErr == nil && n > 0 {
		id = strconv.FormatUint(n, 10)
	} else {
		id, err = b.runInput(ctx, "search", "--onlyvisible", "--name", regexp.QuoteMeta(id))
		if len(strings.Fields(id)) != 1 && err == nil {
			err = fmt.Errorf("computer: window title matches multiple windows; use an explicit window ID")
		}
	}
	if err != nil {
		return "", "", Rect{}, err
	}
	if n, parseErr := strconv.ParseUint(id, 10, 64); parseErr != nil || n == 0 {
		return "", "", Rect{}, errors.New("computer: no active/selected X11 window")
	}
	geometry, err := b.runInput(ctx, "getwindowgeometry", "--shell", id)
	if err != nil {
		return "", "", Rect{}, err
	}
	values := map[string]int{}
	for _, line := range strings.Split(geometry, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			values[key], _ = strconv.Atoi(value)
		}
	}
	bounds := Rect{X: values["X"], Y: values["Y"], Width: values["WIDTH"], Height: values["HEIGHT"]}
	if bounds.Width <= 0 || bounds.Height <= 0 {
		return "", "", Rect{}, errors.New("computer: invalid X11 window geometry")
	}
	title, err := b.runInput(ctx, "getwindowname", id)
	return id, title, bounds, err
}

func (b *X11Backend) ValidateObservation(ctx context.Context, obs Observation, action Action) error {
	if obs.WindowID == "" || action.ElementID != "" {
		return nil
	}
	id, _, _, err := b.windowDetails(ctx, "active")
	if err != nil {
		return err
	}
	if id != obs.WindowID && (!obs.windowScoped || !pointerAction(action.Kind)) {
		return fmt.Errorf("%w: active window changed", ErrStaleFrame)
	}
	_, _, bounds, err := b.windowDetails(ctx, obs.WindowID)
	if err != nil {
		return err
	}
	if bounds != obs.WindowBounds {
		return fmt.Errorf("%w: window moved or resized", ErrStaleFrame)
	}
	root, err := b.rootBounds(ctx)
	if err != nil {
		return err
	}
	expected := root
	if obs.windowScoped {
		expected = image.Rect(bounds.X, bounds.Y, bounds.X+bounds.Width, bounds.Y+bounds.Height).Intersect(root)
	}
	if obs.CaptureBounds != (Rect{X: expected.Min.X, Y: expected.Min.Y, Width: expected.Dx(), Height: expected.Dy()}) {
		return fmt.Errorf("%w: display geometry changed", ErrStaleFrame)
	}
	return nil
}

func (b *X11Backend) Accessibility(ctx context.Context, target Target) (AccessibilityTree, error) {
	_, title, _, err := b.windowDetails(ctx, target.Window)
	if err != nil {
		return AccessibilityTree{}, err
	}
	var tree AccessibilityTree
	err = b.helper.call(ctx, map[string]any{"op": "a11y_observe", "window": title}, &tree)
	return tree, err
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
	if action.Kind == ActionInvoke || action.Kind == ActionSetText || action.Kind == ActionFocus {
		return b.helper.call(ctx, map[string]any{"op": "a11y_action", "action": action}, nil)
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
		args = []string{"mousemove", "--sync", strconv.Itoa(action.X), strconv.Itoa(action.Y), "click", "--repeat", "1", button}
	case ActionDoubleClick:
		args = []string{"mousemove", "--sync", strconv.Itoa(action.X), strconv.Itoa(action.Y), "click", "--repeat", "2", "--delay", "80", button}
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
		for _, axis := range []struct {
			delta              int
			negative, positive string
		}{{action.DeltaY, "4", "5"}, {action.DeltaX, "6", "7"}} {
			if axis.delta == 0 {
				continue
			}
			buttonNum := axis.positive
			if axis.delta < 0 {
				buttonNum = axis.negative
			}
			args = append(args, "click", "--repeat", strconv.Itoa(abs(axis.delta)), buttonNum)
		}
	}
	_, err := b.runInput(ctx, args...)
	if err != nil && action.Kind == ActionDrag {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, _ = b.runInput(cleanup, "mouseup", button)
	}
	return err
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

func (b *X11Backend) Close() error { return b.helper.Close() }
