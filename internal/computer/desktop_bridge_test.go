package computer

import (
	"context"
	"errors"
	"os/exec"
	"testing"
	"time"
)

func TestDesktopBridgeReuseCancellationAndClose(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("Python is optional for X11 screenshots")
	}
	b := newDesktopBridge("")
	b.script = `import sys,json,os,time
for line in sys.stdin:
 r=json.loads(line)
 if r.get("wait"): time.sleep(30)
 print(json.dumps({"result":{"pid":os.getpid()}}),flush=True)
`
	defer b.Close()
	var a, next struct {
		PID int `json:"pid"`
	}
	if err := b.call(context.Background(), map[string]any{}, &a); err != nil {
		t.Fatal(err)
	}
	if err := b.call(context.Background(), map[string]any{}, &next); err != nil {
		t.Fatal(err)
	}
	if a.PID != next.PID {
		t.Fatal("helper restarted between observations")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := b.call(ctx, map[string]any{"wait": true}, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if err := b.call(context.Background(), map[string]any{}, &next); err != nil {
		t.Fatal(err)
	}
	if a.PID == next.PID {
		t.Fatal("cancelled helper was not replaced")
	}
	_ = b.Close()
	if err := b.call(context.Background(), map[string]any{}, nil); err == nil {
		t.Fatal("closed helper restarted")
	}
}

func TestDesktopPythonProtocols(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("Python is optional")
	}
	cmd := exec.Command("python3", "-B", "desktop_helper_test.py")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("desktop helper tests: %v\n%s", err, out)
	}
}
