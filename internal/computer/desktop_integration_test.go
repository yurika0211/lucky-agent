package computer

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Opt-in test: creates its own unfocused GTK window and touches only controls
// in that window. No synthetic keyboard input is sent to the user's apps.
func TestX11DesktopIntegration(t *testing.T) {
	if os.Getenv("LA_COMPUTER_X11_SMOKE") != "1" {
		t.Skip("set LA_COMPUTER_X11_SMOKE=1 on an X11 desktop with Python GI/GTK/AT-SPI")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	title := fmt.Sprintf("LuckyAgent Computer Test %d", time.Now().UnixNano())
	statePath := filepath.Join(t.TempDir(), "state.json")
	script := `import gi,json,sys
gi.require_version("Gtk","3.0")
from gi.repository import Gtk
window=Gtk.Window(title=sys.argv[1])
window.set_focus_on_map(False)
window.set_default_size(340,160)
box=Gtk.Box(orientation=Gtk.Orientation.VERTICAL,spacing=10)
entry=Gtk.Entry()
entry.get_accessible().set_name("LuckyAgent test input")
button=Gtk.Button(label="LuckyAgent test button")
def clicked(*args):
 with open(sys.argv[2],"w") as out: json.dump({"text":entry.get_text(),"clicked":True},out)
button.connect("clicked",clicked)
box.pack_start(entry,False,False,0)
box.pack_start(button,False,False,0)
window.add(box)
window.show_all()
Gtk.main()
`
	cmd := exec.CommandContext(ctx, "python3", "-B", "-c", script, title, statePath)
	cmd.Env = append(os.Environ(), "NO_AT_BRIDGE=0")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	backend := NewX11Backend()
	defer backend.Close()
	root, err := backend.Capture(ctx, Target{})
	if err != nil {
		t.Fatal(err)
	}
	defer discardCapture(root)
	t.Logf("root screenshot matches X11 display: %dx%d", root.Width, root.Height)
	manager := newTestManager(t, backend)
	var obs Observation
	for i := 0; i < 40; i++ {
		obs, err = manager.Observe(ctx, "smoke", ObserveRequest{Target: Target{Window: title}, Format: "both"})
		if err == nil && obs.Accessibility != nil && len(obs.Accessibility.Nodes) > 0 {
			break
		}
		if err := waitContext(ctx, 100*time.Millisecond); err != nil {
			t.Fatal(err)
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	if obs.Width <= 0 || obs.Height <= 0 {
		t.Fatal("window screenshot has no dimensions")
	}
	find := func(name string) string {
		t.Helper()
		for _, node := range obs.Accessibility.Nodes {
			if node.Name == name {
				return node.ID
			}
		}
		t.Fatalf("test control %q not exposed in AT-SPI", name)
		return ""
	}
	t.Logf("window screenshot %dx%d at (%d,%d), accessible controls=%d", obs.Width, obs.Height, obs.OriginX, obs.OriginY, len(obs.Accessibility.Nodes))
	obs, err = manager.Step(ctx, "smoke", Action{Kind: ActionSetText, FrameID: obs.FrameID, ElementID: find("LuckyAgent test input"), Text: " 你好 LuckyAgent "})
	if err != nil {
		t.Fatal(err)
	}
	obs, err = manager.Step(ctx, "smoke", Action{Kind: ActionInvoke, FrameID: obs.FrameID, ElementID: find("LuckyAgent test button")})
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		Text    string `json:"text"`
		Clicked bool   `json:"clicked"`
	}
	for i := 0; i < 20; i++ {
		data, readErr := os.ReadFile(statePath)
		if readErr == nil && json.Unmarshal(data, &state) == nil && state.Clicked {
			break
		}
		_ = waitContext(ctx, 50*time.Millisecond)
	}
	if !state.Clicked || state.Text != " 你好 LuckyAgent " {
		t.Fatalf("AT-SPI action did not reach the GTK controls: %+v", state)
	}
	t.Log("AT-SPI set_text preserved Unicode/whitespace; invoke reached the test button")
}
