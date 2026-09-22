package computer

import (
	"context"
	"image"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestX11WindowCaptureAndInputCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX command fixture")
	}
	dir := t.TempDir()
	frame := pngFrame(t, image.NewRGBA(image.Rect(0, 0, 100, 60)))
	pngPath, logPath := filepath.Join(dir, "fixture.png"), filepath.Join(dir, "commands")
	if err := os.WriteFile(pngPath, frame.ImageData, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LA_TEST_PNG", pngPath)
	t.Setenv("LA_TEST_LOG", logPath)
	input := filepath.Join(dir, "input")
	screen := filepath.Join(dir, "screen")
	for path, script := range map[string]string{
		input: `#!/bin/sh
printf 'DISPLAY=%s\n' "$DISPLAY" >> "$LA_TEST_LOG"
printf '%s\n' "$@" >> "$LA_TEST_LOG"
case "$1" in
getactivewindow|search) echo 42 ;;
getwindowgeometry) printf 'WINDOW=42\nX=25\nY=20\nWIDTH=100\nHEIGHT=60\nSCREEN=0\n' ;;
getwindowname) echo 'LuckyAgent test' ;;
getdisplaygeometry) echo '200 100' ;;
esac
`,
		screen: `#!/bin/sh
printf '%s\n' "$@" >> "$LA_TEST_LOG"
for dest do :; done
cp "$LA_TEST_PNG" "$dest"
`,
	} {
		if err := os.WriteFile(path, []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	b := NewX11Backend(X11Config{Display: ":77", ScreenshotBinary: screen, InputBinary: input})
	defer b.Close()
	m := newTestManager(t, b)
	obs, err := m.Observe(context.Background(), "window", ObserveRequest{Target: Target{Window: "active"}})
	if err != nil {
		t.Fatal(err)
	}
	if obs.WindowID != "42" || obs.OriginX != 25 || obs.OriginY != 20 || obs.ActiveWindow != "LuckyAgent test" {
		t.Fatalf("missing window metadata: %+v", obs)
	}
	if _, err := m.Step(context.Background(), "window", Action{Kind: ActionClick, FrameID: obs.FrameID, X: 10, Y: 15}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	log := string(data)
	for _, expected := range []string{"DISPLAY=:77", "-crop\n100x60+25+20\n+repage", "mousemove\n--sync\n35\n35\nclick\n--repeat\n1\n1"} {
		if !strings.Contains(log, expected) {
			t.Fatalf("expected %q in command log:\n%s", expected, log)
		}
	}
	if strings.Contains(log, "--button") {
		t.Fatal("invalid xdotool --button flag remains")
	}
	if _, err := b.Capture(context.Background(), Target{}); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("partial root screenshot was not rejected: %v", err)
	}
}

func TestBackendAutoPrefersWaylandOverXWayland(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux backend selection")
	}
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	t.Setenv("DISPLAY", ":0")
	b, err := NewBackend("auto", BackendOptions{ObserveOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if b.Name() != "wayland" {
		t.Fatal("auto selected the incomplete XWayland desktop")
	}
	if err := b.Perform(context.Background(), Action{Kind: ActionClick}); err == nil {
		t.Fatal("read-only Wayland backend accepted input")
	}
}
