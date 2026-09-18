package computer

import (
	"context"
	"errors"
	"image"
	"os"
	"sync"
	"testing"
	"time"
)

type workflowBackend struct {
	fakeBackend
	frames  []Observation
	targets []Target
	failAt  int
	tree    AccessibilityTree
}

func (b *workflowBackend) Capture(_ context.Context, target Target) (Observation, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.targets = append(b.targets, target)
	i := min(b.captures, len(b.frames)-1)
	b.captures++
	if i < 0 {
		return Observation{ImageData: []byte("frame"), Width: 100, Height: 80}, nil
	}
	return b.frames[i], nil
}
func (b *workflowBackend) Perform(ctx context.Context, action Action) error {
	if err := b.fakeBackend.Perform(ctx, action); err != nil {
		return err
	}
	if b.failAt > 0 && len(b.performs) == b.failAt {
		return errors.New("input failed")
	}
	return nil
}
func (b *workflowBackend) Accessibility(context.Context, Target) (AccessibilityTree, error) {
	return b.tree, nil
}

func TestRegionScaleAndWindowOffsetSurviveStep(t *testing.T) {
	frame := pngFrame(t, image.NewRGBA(image.Rect(0, 0, 400, 300)))
	frame.OriginX, frame.OriginY, frame.WindowID = 100, 200, "42"
	b := &workflowBackend{frames: []Observation{frame}}
	m := newTestManager(t, b, func(c *ManagerConfig) { c.MaxScreenshotWidth = 100 })
	req := ObserveRequest{Target: Target{Window: "active", Region: &Rect{X: 50, Y: 60, Width: 200, Height: 100}}}
	obs, err := m.Observe(context.Background(), "region", req)
	if err != nil {
		t.Fatal(err)
	}
	if obs.Width != 100 || obs.Height != 50 || obs.OriginX != 150 || obs.OriginY != 260 {
		t.Fatalf("wrong frame geometry: %+v", obs)
	}
	obs, err = m.Step(context.Background(), "region", Action{Kind: ActionDrag, FrameID: obs.FrameID, X: 10, Y: 20, EndX: 99, EndY: 49})
	if err != nil {
		t.Fatal(err)
	}
	got := b.performs[0]
	if got.X != 170 || got.Y != 300 || got.EndX != 348 || got.EndY != 358 {
		t.Fatalf("wrong desktop coordinates: %+v", got)
	}
	if b.targets[1].Window != "42" || b.targets[1].Region == nil || obs.Width != 100 {
		t.Fatalf("step lost selected window/region: %+v, %+v", b.targets, obs)
	}
	if _, err := m.Observe(context.Background(), "bad", ObserveRequest{Target: Target{Region: &Rect{Width: 401, Height: 1}}}); err == nil {
		t.Fatal("out-of-bounds region accepted")
	}
}

func TestBatchCapturesOnceAndStopsWithoutReplay(t *testing.T) {
	b := &workflowBackend{}
	m := newTestManager(t, b)
	obs, err := m.Observe(context.Background(), "batch", ObserveRequest{})
	if err != nil {
		t.Fatal(err)
	}
	actions := []Action{{Kind: ActionClick, X: 1, Y: 2}, {Kind: ActionTypeText, Text: "hello"}, {Kind: ActionKeypress, Keys: []string{"ENTER"}}}
	for i := range actions {
		actions[i].FrameID = obs.FrameID
	}
	obs, err = m.StepBatch(context.Background(), "batch", actions)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.performs) != 3 || b.captures != 2 {
		t.Fatalf("performs=%d captures=%d", len(b.performs), b.captures)
	}
	b.failAt = 5
	for i := range actions {
		actions[i].FrameID = obs.FrameID
	}
	_, err = m.StepBatch(context.Background(), "batch", actions)
	var partial *BatchError
	if !errors.As(err, &partial) || partial.Completed != 1 || len(b.performs) != 5 {
		t.Fatalf("partial failure: %v performs=%d", err, len(b.performs))
	}
	if _, err := m.StepBatch(context.Background(), "batch", actions); err == nil {
		t.Fatal("stale partial batch was replayed")
	}
	if len(b.performs) != 5 {
		t.Fatal("retry injected input")
	}
}

func TestBatchValidatesEveryActionBeforeInput(t *testing.T) {
	for _, invalid := range []Action{{Kind: ActionClick, X: 3}, {Kind: ActionTypeText}, {Kind: ActionSetText, ElementID: "unknown"}} {
		b := &workflowBackend{}
		m := newTestManager(t, b)
		obs, err := m.Observe(context.Background(), "batch", ObserveRequest{})
		if err != nil {
			t.Fatal(err)
		}
		invalid.FrameID = obs.FrameID
		_, err = m.StepBatch(context.Background(), "batch", []Action{{Kind: ActionClick, FrameID: obs.FrameID}, invalid})
		if err == nil || len(b.performs) > 0 {
			t.Fatalf("invalid batch reached backend: %v %+v", err, b.performs)
		}
	}
	b := &workflowBackend{}
	m := newTestManager(t, b, WithMaxSteps(1))
	obs, _ := m.Observe(context.Background(), "limit", ObserveRequest{})
	_, err := m.StepBatch(context.Background(), "limit", []Action{{Kind: ActionClick, FrameID: obs.FrameID}, {Kind: ActionTypeText, FrameID: obs.FrameID, Text: "x"}})
	if !errors.Is(err, ErrStepLimit) || len(b.performs) > 0 {
		t.Fatal("batch exceeded remaining step budget", err)
	}
}

func TestOtherSessionInvalidatesDesktopFrame(t *testing.T) {
	b := &workflowBackend{}
	m := newTestManager(t, b)
	a, _ := m.Observe(context.Background(), "a", ObserveRequest{})
	other, _ := m.Observe(context.Background(), "b", ObserveRequest{})
	if _, err := m.Step(context.Background(), "a", Action{Kind: ActionClick, FrameID: a.FrameID}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Step(context.Background(), "b", Action{Kind: ActionClick, FrameID: other.FrameID}); !errors.Is(err, ErrStaleFrame) {
		t.Fatalf("expected desktop revision rejection, got %v", err)
	}
}

func TestExpiredObservationCannotInjectInput(t *testing.T) {
	b := &workflowBackend{}
	m := newTestManager(t, b, WithFrameTTL(time.Nanosecond))
	obs, err := m.Observe(context.Background(), "expired", ObserveRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Step(context.Background(), "expired", Action{Kind: ActionClick, FrameID: obs.FrameID}); !errors.Is(err, ErrStaleFrame) {
		t.Fatalf("expired frame accepted: %v", err)
	}
	if len(b.performs) != 0 {
		t.Fatal("expired frame reached input backend")
	}
}

func TestDesktopLockWaitCanBeCanceled(t *testing.T) {
	m := newTestManager(t, &workflowBackend{})
	if err := m.lockDesktop(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := m.Observe(ctx, "blocked", ObserveRequest{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting for desktop ignored deadline: %v", err)
	}
	m.unlockDesktop()
	if _, err := m.Observe(context.Background(), "unblocked", ObserveRequest{}); err != nil {
		t.Fatal(err)
	}
}

func TestAdaptiveSettleSamplesChangesAndPrunesTemps(t *testing.T) {
	var frames []Observation
	var paths []string
	for _, data := range []string{"old", "moving", "new", "new", "new"} {
		f, err := os.CreateTemp(t.TempDir(), "frame-")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.WriteString(data)
		_ = f.Close()
		paths = append(paths, f.Name())
		frames = append(frames, Observation{FilePath: f.Name(), CleanupFile: true, Width: 100, Height: 80})
	}
	b := &workflowBackend{frames: frames}
	m := newTestManager(t, b, WithSettleDelay(500*time.Millisecond))
	obs, err := m.Observe(context.Background(), "settle", ObserveRequest{})
	if err != nil {
		t.Fatal(err)
	}
	obs, err = m.Step(context.Background(), "settle", Action{Kind: ActionClick, FrameID: obs.FrameID})
	if err != nil {
		t.Fatal(err)
	}
	if !obs.Stable || b.captures != 5 {
		t.Fatalf("settled prematurely: stable=%v captures=%d", obs.Stable, b.captures)
	}
	for _, path := range paths {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("capture temp remains: %s, %v", path, err)
		}
	}
}

func TestTreeObservationDoesNotCapturePixels(t *testing.T) {
	b := &workflowBackend{tree: AccessibilityTree{Window: "Test", Nodes: []AccessibilityNode{{ID: "entry", Role: "text", Enabled: true, Editable: true}}}}
	m := newTestManager(t, b)
	obs, err := m.Observe(context.Background(), "tree", ObserveRequest{Format: "tree"})
	if err != nil {
		t.Fatal(err)
	}
	if obs.FilePath != "" || b.captures != 0 || obs.Accessibility == nil {
		t.Fatalf("tree query captured an image: %+v", obs)
	}
	if _, err := m.Step(context.Background(), "tree", Action{Kind: ActionClick, FrameID: obs.FrameID}); err == nil {
		t.Fatal("pixel click used a tree-only frame")
	}
	if _, err := m.Step(context.Background(), "tree", Action{Kind: ActionSetText, FrameID: obs.FrameID, ElementID: "entry", Text: "你好"}); err != nil {
		t.Fatal(err)
	}
	if len(b.performs) != 1 || b.performs[0].Text != "你好" || b.captures != 0 {
		t.Fatal("accessibility action failed", b.performs)
	}
}

func BenchmarkComputerSequence(b *testing.B) {
	for _, batch := range []bool{false, true} {
		name := "single_actions"
		if batch {
			name = "batch"
		}
		b.Run(name, func(b *testing.B) {
			backend := &workflowBackend{}
			m, err := NewManager(backend, WithStorageDir(b.TempDir()), WithSettleDelay(0), WithMaxSteps(0))
			if err != nil {
				b.Fatal(err)
			}
			defer m.Close()
			obs, err := m.Observe(context.Background(), "bench", ObserveRequest{})
			if err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				actions := []Action{{Kind: ActionClick}, {Kind: ActionTypeText, Text: "hello"}, {Kind: ActionKeypress, Keys: []string{"ENTER"}}}
				if batch {
					for i := range actions {
						actions[i].FrameID = obs.FrameID
					}
					obs, err = m.StepBatch(context.Background(), "bench", actions)
				} else {
					for _, action := range actions {
						action.FrameID = obs.FrameID
						obs, err = m.Step(context.Background(), "bench", action)
						if err != nil {
							break
						}
					}
				}
				if err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(backend.captures-1)/float64(b.N), "captures/sequence")
		})
	}
}

func BenchmarkComputerSettle(b *testing.B) {
	for _, mode := range []string{"fixed", "adaptive"} {
		b.Run(mode, func(b *testing.B) {
			m, err := NewManager(&workflowBackend{}, WithStorageDir(b.TempDir()), WithMaxSteps(0), func(c *ManagerConfig) { c.SettleMode = mode })
			if err != nil {
				b.Fatal(err)
			}
			defer m.Close()
			obs, _ := m.Observe(context.Background(), "bench", ObserveRequest{})
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				obs, err = m.Step(context.Background(), "bench", Action{Kind: ActionClick, FrameID: obs.FrameID})
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// Keep race coverage for parallel observations and actions while frames are
// invalidated across sessions; backend calls must remain serialized.
func TestConcurrentObserveAndAction(t *testing.T) {
	m := newTestManager(t, &workflowBackend{})
	var wg sync.WaitGroup
	for _, id := range []string{"a", "b", "c"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 5; i++ {
				obs, err := m.Observe(context.Background(), id, ObserveRequest{})
				if err == nil {
					_, _ = m.Step(context.Background(), id, Action{Kind: ActionClick, FrameID: obs.FrameID})
				}
			}
		}()
	}
	wg.Wait()
}
