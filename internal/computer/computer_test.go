package computer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type fakeBackend struct {
	mu        sync.Mutex
	captures  int
	performs  []Action
	active    int
	maxActive int
	data      []byte
}

func (b *fakeBackend) Name() string { return "fake" }
func (b *fakeBackend) Capabilities(context.Context) (Capabilities, error) {
	return Capabilities{Capture: true, Click: true, TypeText: true}, nil
}
func (b *fakeBackend) Capture(context.Context, Target) (Observation, error) {
	b.mu.Lock()
	b.captures++
	data := append([]byte(nil), b.data...)
	b.mu.Unlock()
	if len(data) == 0 {
		data = []byte("fake-png")
	}
	return Observation{ImageData: data, MimeType: "image/png", Width: 100, Height: 80}, nil
}
func (b *fakeBackend) Perform(ctx context.Context, action Action) error {
	b.mu.Lock()
	b.active++
	if b.active > b.maxActive {
		b.maxActive = b.active
	}
	b.performs = append(b.performs, action)
	b.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(time.Millisecond):
	}
	b.mu.Lock()
	b.active--
	b.mu.Unlock()
	return nil
}
func (b *fakeBackend) Close() error { return nil }

func TestActionValidate(t *testing.T) {
	tests := []struct {
		name    string
		action  Action
		wantErr bool
	}{
		{"click", Action{Kind: ActionClick, X: 1, Y: 2}, false},
		{"type requires text", Action{Kind: ActionTypeText}, true},
		{"keypress requires keys", Action{Kind: ActionKeypress}, true},
		{"scroll requires delta", Action{Kind: ActionScroll}, true},
		{"negative coordinate", Action{Kind: ActionClick, X: -1, Y: 2}, true},
		{"unknown kind", Action{Kind: "launch"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.action.Validate(); (got != nil) != tt.wantErr {
				t.Fatalf("Validate() error=%v, wantErr=%v", got, tt.wantErr)
			}
		})
	}
}

func newTestManager(t *testing.T, backend Backend, options ...ManagerOption) *Manager {
	t.Helper()
	dir := t.TempDir()
	options = append([]ManagerOption{WithStorageDir(filepath.Join(dir, "frames")), WithSettleDelay(0)}, options...)
	m, err := NewManager(backend, options...)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestManagerObserveStepAndStaleFrame(t *testing.T) {
	b := &fakeBackend{}
	m := newTestManager(t, b, WithMaxSteps(2))
	ctx := context.Background()
	first, err := m.Observe(ctx, "session/a", ObserveRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if first.FrameID != "frame-1" || first.FilePath == "" || first.SHA256 == "" {
		t.Fatalf("unexpected first observation: %+v", first)
	}
	if mode := func() os.FileMode { info, _ := os.Stat(first.FilePath); return info.Mode().Perm() }(); mode != 0600 {
		t.Fatalf("frame permissions=%#o, want 0600", mode)
	}
	if _, err := m.Step(ctx, "session/a", Action{Kind: ActionClick, FrameID: "frame-0", X: 1, Y: 2}); !errors.Is(err, ErrStaleFrame) {
		t.Fatalf("stale error=%v", err)
	}
	second, err := m.Step(ctx, "session/a", Action{Kind: ActionClick, FrameID: first.FrameID, X: 1, Y: 2})
	if err != nil {
		t.Fatal(err)
	}
	if second.FrameID != "frame-2" {
		t.Fatalf("step frame=%q", second.FrameID)
	}
	third, err := m.Step(ctx, "session/a", Action{Kind: ActionClick, FrameID: second.FrameID, X: 1, Y: 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Step(ctx, "session/a", Action{Kind: ActionClick, FrameID: third.FrameID, X: 1, Y: 2}); !errors.Is(err, ErrStepLimit) {
		t.Fatalf("step limit error=%v", err)
	}
}

func TestManagerSessionIsolationAndClose(t *testing.T) {
	b := &fakeBackend{}
	m := newTestManager(t, b)
	ctx := context.Background()
	a, err := m.Observe(ctx, "a", ObserveRequest{})
	if err != nil {
		t.Fatal(err)
	}
	bobs, err := m.Observe(ctx, "b", ObserveRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if a.FrameID != "frame-1" || bobs.FrameID != "frame-1" {
		t.Fatalf("frames not isolated: %s %s", a.FrameID, bobs.FrameID)
	}
	if err := m.CloseSession("a"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Step(ctx, "a", Action{Kind: ActionClick, FrameID: a.FrameID, X: 1, Y: 1}); err == nil {
		t.Fatal("closed session unexpectedly recreated")
	}
	if _, err := os.Stat(a.FilePath); !os.IsNotExist(err) {
		t.Fatalf("closed session frame still exists: %v", err)
	}
}

func TestManagerSerializesDesktopActions(t *testing.T) {
	b := &fakeBackend{}
	m := newTestManager(t, b)
	ctx := context.Background()
	first, err := m.Observe(ctx, "a", ObserveRequest{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.Observe(ctx, "b", ObserveRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = m.Step(ctx, "a", Action{Kind: ActionClick, FrameID: first.FrameID, X: 1, Y: 1})
	}()
	go func() {
		defer wg.Done()
		_, _ = m.Step(ctx, "b", Action{Kind: ActionClick, FrameID: second.FrameID, X: 2, Y: 2})
	}()
	wg.Wait()
	if b.maxActive != 1 {
		t.Fatalf("backend actions overlapped: maxActive=%d", b.maxActive)
	}
}

func TestFrameStoreKeepAndTTL(t *testing.T) {
	dir := t.TempDir()
	s, err := NewFrameStore(dir, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	for i := uint64(1); i <= 2; i++ {
		if _, err := s.Save("x", i, Observation{ImageData: []byte{byte(i)}}); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(s.SessionDir("x"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("kept %d frames, want 1", len(entries))
	}
	if err := os.WriteFile(filepath.Join(s.SessionDir("x"), "frame-old.png"), []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(s.SessionDir("x"), "frame-old.png")
	if err := os.Chtimes(old, time.Now().Add(-time.Hour), time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	s.ttl = time.Minute
	if err := s.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("expired frame remains: %v", err)
	}
}
