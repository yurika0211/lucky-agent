package bootstrap

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/yurika0211/luckyagent-runtime/config"
	"github.com/yurika0211/luckyagent-tools/memory"
)

func TestApplicationStartupIsExplicitAndRunsHooksOnce(t *testing.T) {
	mgr, err := config.NewManagerWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(t.TempDir(), "custom-skills")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := mgr.Get()
	cfg.Skills.Dir = directory
	if err := mgr.Replace(cfg); err != nil {
		t.Fatal(err)
	}
	components := &Components{Config: mgr}
	var loaded, installed, restored, heartbeat int
	hooks := Hooks{
		LoadSkills: func(path string) (int, error) {
			if path != directory {
				t.Fatalf("skills directory=%q", path)
			}
			loaded++
			return 0, nil
		},
		InstallCronEvents: func() { installed++ },
		RestoreCron:       func() (int, error) { restored++; return 0, nil },
		StartHeartbeat:    func() error { heartbeat++; return nil },
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := components.Start(ctx, hooks); !errors.Is(err, context.Canceled) || loaded != 0 {
		t.Fatal("cancelled startup ran hooks")
	}
	for i := 0; i < 2; i++ {
		if err := components.Start(context.Background(), hooks); err != nil {
			t.Fatal(err)
		}
	}
	if loaded != 1 || installed != 1 || restored != 1 || heartbeat != 1 {
		t.Fatalf("startup hooks repeated: %d %d %d %d", loaded, installed, restored, heartbeat)
	}
	if err := components.Close(); err != nil {
		t.Fatal(err)
	}
	if err := components.Start(context.Background(), hooks); err == nil {
		t.Fatal("closed components accepted startup")
	}
}

type failingCloseStore struct {
	memory.RuntimeStore
	err   error
	calls int
}

func (s *failingCloseStore) Close() error { s.calls++; return s.err }

func TestPartialComponentsCloseRetainsFailureAndIsIdempotent(t *testing.T) {
	failed := errors.New("injected close error")
	store := &failingCloseStore{err: failed}
	components := &Components{Memory: memory.Runtime{Store: store}}
	for i := 0; i < 2; i++ {
		if err := components.Close(); !errors.Is(err, failed) {
			t.Fatalf("close error lost: %v", err)
		}
	}
	if store.calls != 1 {
		t.Fatalf("close calls=%d", store.calls)
	}
}
