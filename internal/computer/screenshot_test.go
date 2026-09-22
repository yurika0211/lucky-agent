package computer

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type screenshotBackend struct {
	fakeBackend
	frame     Observation
	onCapture func()
}

func (b *screenshotBackend) Capture(context.Context, Target) (Observation, error) {
	if b.onCapture != nil {
		b.onCapture()
	}
	return b.frame, nil
}

func pngFrame(t *testing.T, img image.Image) Observation {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return Observation{ImageData: buf.Bytes(), MimeType: "image/png", Width: img.Bounds().Dx(), Height: img.Bounds().Dy()}
}

func readFrame(t *testing.T, obs Observation) image.Image {
	t.Helper()
	f, err := os.Open(obs.FilePath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != obs.Width || img.Bounds().Dy() != obs.Height {
		t.Fatalf("metadata %dx%d does not match image %v", obs.Width, obs.Height, img.Bounds())
	}
	return img
}

func TestManagerFitsWideScreenshotWithoutCropping(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 3840, 2160))
	colors := []color.RGBA{{255, 0, 0, 255}, {0, 255, 0, 255}, {0, 0, 255, 255}, {255, 255, 0, 255}}
	for y := 0; y < 2160; y++ {
		for x := 0; x < 3840; x++ {
			img.SetRGBA(x, y, colors[(y/1080)*2+x/1920])
		}
	}
	b := &screenshotBackend{frame: pngFrame(t, img)}
	m := newTestManager(t, b, func(c *ManagerConfig) { c.MaxScreenshotWidth = 1920 })
	obs, err := m.Observe(context.Background(), "wide", ObserveRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if obs.Width != 1920 || obs.Height != 1080 || obs.ScaleFactor != 0.5 {
		t.Fatalf("unexpected scaled frame: %+v", obs)
	}
	got := readFrame(t, obs)
	for i, p := range []image.Point{{0, 0}, {1919, 0}, {0, 1079}, {1919, 1079}} {
		if c := color.RGBAModel.Convert(got.At(p.X, p.Y)); c != colors[i] {
			t.Errorf("corner %v = %v, want %v; screen content was lost", p, c, colors[i])
		}
	}

	for _, action := range []Action{
		{Kind: ActionClick, X: 960, Y: 540},
		{Kind: ActionDoubleClick, X: 1919, Y: 1079},
		{Kind: ActionMove, X: 0, Y: 0},
		{Kind: ActionDrag, X: 100, Y: 150, EndX: 1800, EndY: 1000},
		{Kind: ActionScroll, DeltaY: 3},
		{Kind: ActionKeypress, Keys: []string{"ENTER"}},
	} {
		action.FrameID = obs.FrameID
		obs, err = m.Step(context.Background(), "wide", action)
		if err != nil {
			t.Fatal(err)
		}
		gotAction := b.performs[len(b.performs)-1]
		if gotAction.X != action.X*2 || gotAction.Y != action.Y*2 || gotAction.EndX != action.EndX*2 || gotAction.EndY != action.EndY*2 || gotAction.DeltaY != action.DeltaY {
			t.Fatalf("action mapping: %+v -> %+v", action, gotAction)
		}
	}
	before := len(b.performs)
	if _, err := m.Step(context.Background(), "wide", Action{Kind: ActionClick, FrameID: obs.FrameID, X: 1920}); err == nil {
		t.Fatal("out-of-frame action was accepted")
	}
	if len(b.performs) != before {
		t.Fatal("invalid action reached backend")
	}
}

func TestManagerFitsScreenshotToByteBudget(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 513, 257))
	// Deterministic noise prevents PNG compression from hiding the byte limit.
	var state uint32 = 1
	for i := 0; i < len(img.Pix); i += 4 {
		state = state*1664525 + 1013904223
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = byte(state>>24), byte(state>>16), byte(state>>8), 255
	}
	frame := pngFrame(t, img)
	const budget = 48 << 10
	if len(frame.ImageData) <= budget {
		t.Fatal("fixture must exceed budget")
	}
	for _, fileBacked := range []bool{false, true} {
		t.Run(map[bool]string{false: "memory", true: "file"}[fileBacked], func(t *testing.T) {
			b := &screenshotBackend{frame: frame}
			if fileBacked {
				path := filepath.Join(t.TempDir(), "capture.png")
				if err := os.WriteFile(path, frame.ImageData, 0600); err != nil {
					t.Fatal(err)
				}
				b.frame.ImageData, b.frame.FilePath, b.frame.CleanupFile = nil, path, true
			}
			m := newTestManager(t, b, func(c *ManagerConfig) { c.MaxObservationBytes = budget })
			obs, err := m.Observe(context.Background(), "large", ObserveRequest{})
			if err != nil {
				t.Fatal(err)
			}
			readFrame(t, obs)
			info, err := os.Stat(obs.FilePath)
			if err != nil || info.Size() > budget {
				t.Fatalf("frame exceeds budget: %v, %v", info, err)
			}
			if obs.Width >= frame.Width || obs.Height >= frame.Height || obs.ScaleFactor >= 1 {
				t.Fatalf("frame was not reduced: %+v", obs)
			}
			if math.Abs(float64(obs.Height)-float64(frame.Height)*obs.ScaleFactor) > 1 {
				t.Fatalf("aspect ratio changed: %+v", obs)
			}
			if fileBacked {
				if _, err := os.Stat(b.frame.FilePath); !os.IsNotExist(err) {
					t.Fatalf("source temp file not cleaned up: %v", err)
				}
				// The next capture supplies a fresh frame, as real backends do.
				b.frame = frame
			}
			x, y := obs.Width-1, obs.Height-1
			if _, err := m.Step(context.Background(), "large", Action{Kind: ActionClick, FrameID: obs.FrameID, X: x, Y: y}); err != nil {
				t.Fatal(err)
			}
			got := b.performs[0]
			wantX := int(math.Round(float64(x) * float64(frame.Width) / float64(obs.Width)))
			wantY := int(math.Round(float64(y) * float64(frame.Height) / float64(obs.Height)))
			if got.X != wantX || got.Y != wantY || got.X >= frame.Width || got.Y >= frame.Height {
				t.Fatalf("non-integer mapping got (%d,%d), want (%d,%d)", got.X, got.Y, wantX, wantY)
			}
		})
	}
}

func TestManagerImpossibleScreenshotBudgetCleansUp(t *testing.T) {
	frame := pngFrame(t, image.NewRGBA(image.Rect(0, 0, 8, 8)))
	path := filepath.Join(t.TempDir(), "capture.png")
	if err := os.WriteFile(path, frame.ImageData, 0600); err != nil {
		t.Fatal(err)
	}
	frame.ImageData, frame.FilePath, frame.CleanupFile = nil, path, true
	m := newTestManager(t, &screenshotBackend{frame: frame}, func(c *ManagerConfig) { c.MaxObservationBytes = 1 })
	if _, err := m.Observe(context.Background(), "tiny", ObserveRequest{}); err == nil || !strings.Contains(err.Error(), "maximum") {
		t.Fatalf("expected byte limit error, got %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("failed capture left its temp file: %v", err)
	}
}

func TestManagerKeepsScreenshotWithinLimitsUnchanged(t *testing.T) {
	frame := pngFrame(t, image.NewRGBA(image.Rect(0, 0, 32, 17)))
	b := &screenshotBackend{frame: frame}
	m := newTestManager(t, b, func(c *ManagerConfig) {
		c.MaxScreenshotWidth = frame.Width
		c.MaxObservationBytes = len(frame.ImageData)
	})
	obs, err := m.Observe(context.Background(), "small", ObserveRequest{})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(obs.FilePath)
	if err != nil || !bytes.Equal(data, frame.ImageData) || obs.ScaleFactor != 1 {
		t.Fatalf("unnecessary screenshot transformation: scale=%v, err=%v", obs.ScaleFactor, err)
	}
	if _, err := m.Step(context.Background(), "small", Action{Kind: ActionClick, FrameID: obs.FrameID, X: 31, Y: 16}); err != nil {
		t.Fatal(err)
	}
	if action := b.performs[0]; action.X != 31 || action.Y != 16 {
		t.Fatalf("unscaled pointer coordinates changed: %+v", action)
	}
}

func TestManagerCanceledScreenshotCleansUp(t *testing.T) {
	frame := pngFrame(t, image.NewRGBA(image.Rect(0, 0, 32, 17)))
	path := filepath.Join(t.TempDir(), "capture.png")
	if err := os.WriteFile(path, frame.ImageData, 0600); err != nil {
		t.Fatal(err)
	}
	frame.ImageData, frame.FilePath, frame.CleanupFile = nil, path, true
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newTestManager(t, &screenshotBackend{frame: frame, onCapture: cancel}, func(c *ManagerConfig) { c.MaxScreenshotWidth = 16 })
	if _, err := m.Observe(ctx, "canceled", ObserveRequest{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("canceled capture left its temp file: %v", err)
	}
}
