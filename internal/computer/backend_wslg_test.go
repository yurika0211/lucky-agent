package computer

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const fakeWSLGPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="

func TestBackendAutoSelectsWSLGWhenWSLGUIIsPresent(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("WSLg backend selection")
	}
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	t.Setenv("WSL_INTEROP", "/run/WSL/interop")
	t.Setenv("WSL2_GUI_APPS_ENABLED", "1")
	t.Setenv("WSL_DISTRO_NAME", "Ubuntu")
	b, err := NewBackend("auto", BackendOptions{ObserveOnly: true, PowerShellPath: "/bin/true"})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if b.Name() != "wslg" {
		t.Fatalf("expected WSLg backend, got %q", b.Name())
	}
}

func TestWSLGBackendUsesJSONBridgeForCaptureAndAction(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("WSLg bridge is launched from Linux")
	}
	if _, err := exec.LookPath("wslpath"); err != nil {
		t.Skip("WSLg bridge tests require wslpath")
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "powershell")
	script := `#!/bin/sh
request=""
previous=""
for arg in "$@"; do
  if [ "$previous" = "-RequestPath" ]; then request="$arg"; fi
  previous="$arg"
done
linux_request=$(wslpath -u "$request" 2>/dev/null || printf '%s' "$request")
line=$(cat "$linux_request")
if echo "$line" | grep -q '"op":"probe"'; then
  printf '%s\n' '{"result":{"x":-10,"y":0,"width":100,"height":60}}'
elif echo "$line" | grep -q '"op":"capture"'; then
  printf '%s\n' '{"result":{"width":1,"height":1,"display_id":"wslg:virtual","active_window":"WSLg test","window_bounds":{"x":1,"y":2,"width":3,"height":4},"capture_bounds":{"x":-10,"y":0,"width":100,"height":60},"png_base64":"` + fakeWSLGPNGBase64 + `"}}'
elif echo "$line" | grep -q '"op":"action"'; then
  printf '%s\n' '{"result":{}}'
else
  printf '%s\n' '{"error":"unexpected request"}'
fi
`
	if err := os.WriteFile(fake, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	bAny, err := newWSLGBackend(fake, false)
	if err != nil {
		t.Fatal(err)
	}
	b := bAny.(*WSLGBackend)
	defer b.Close()

	caps, err := b.Capabilities(context.Background())
	if err != nil || !caps.Capture || !caps.Click {
		t.Fatalf("unexpected WSLg capabilities: %#v, %v", caps, err)
	}
	obs, err := b.Capture(context.Background(), Target{})
	if err != nil {
		t.Fatal(err)
	}
	if len(obs.ImageData) == 0 || obs.DisplayID != "wslg:virtual" || obs.CaptureBounds.X != -10 || obs.OriginX != -10 || obs.ActiveWindow != "WSLg test" {
		t.Fatalf("unexpected WSLg observation: %+v", obs)
	}
	if err := b.ValidateObservation(context.Background(), obs, Action{Kind: ActionClick}); err != nil {
		t.Fatal(err)
	}
	if err := b.Perform(context.Background(), Action{Kind: ActionClick, X: 10, Y: 20}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.bridge.log.String(), "10") {
		t.Fatalf("unexpected request data in helper diagnostics: %s", b.bridge.log.String())
	}
}

func TestWSLGDesktopIntegration(t *testing.T) {
	if os.Getenv("LA_WSLG_SMOKE") != "1" {
		t.Skip("set LA_WSLG_SMOKE=1 in a WSLg session")
	}
	actionSmoke := os.Getenv("LA_WSLG_ACTION_SMOKE") == "1"
	bAny, err := NewWSLGBackend(!actionSmoke)
	if err != nil {
		t.Fatal(err)
	}
	b := bAny.(*WSLGBackend)
	defer b.Close()
	if _, err := b.Capabilities(context.Background()); err != nil {
		t.Fatal(err)
	}
	obs, err := b.Capture(context.Background(), Target{})
	if err != nil {
		t.Fatal(err)
	}
	if obs.Width <= 0 || obs.Height <= 0 || len(obs.ImageData) == 0 {
		t.Fatalf("WSLg returned an invalid observation: %+v", obs)
	}
	t.Logf("WSLg capture: %dx%d bounds=%+v active=%q", obs.Width, obs.Height, obs.CaptureBounds, obs.ActiveWindow)
	if actionSmoke {
		if err := b.Perform(context.Background(), Action{Kind: ActionMove, X: 0, Y: 0}); err != nil {
			t.Fatalf("WSLg input smoke failed: %v", err)
		}
		t.Log("WSLg move action completed")
	}
}
