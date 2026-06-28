package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestConfigWatcherNoChange(t *testing.T) {
	tmpDir := t.TempDir()
	homeDir := filepath.Join(tmpDir, ".luckyagent")
	cfgPath := filepath.Join(homeDir, "config.yaml")

	mgr, err := NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	mgr.homeDir = homeDir
	mgr.cfgPath = cfgPath

	// Save initial config
	mgr.Set("provider", "openai")
	mgr.Set("model", "gpt-5.4-mini")
	if err := mgr.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	watcher := NewConfigWatcher(mgr, 100*time.Millisecond)

	changed := false
	watcher.OnChange(func(oldCfg, newCfg *Config) {
		changed = true
	})

	if err := watcher.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer watcher.Stop()

	// Wait a bit, no change should trigger
	time.Sleep(200 * time.Millisecond)

	if changed {
		t.Error("should not trigger change without file modification")
	}
}

func TestConfigWatcherDetectsChange(t *testing.T) {
	tmpDir := t.TempDir()
	homeDir := filepath.Join(tmpDir, ".luckyagent")
	cfgPath := filepath.Join(homeDir, "config.yaml")

	mgr, err := NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	mgr.homeDir = homeDir
	mgr.cfgPath = cfgPath

	// Save initial config
	mgr.Set("provider", "openai")
	mgr.Set("model", "gpt-5.4-mini")
	if err := mgr.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	watcher := NewConfigWatcher(mgr, 50*time.Millisecond)

	changeCh := make(chan struct{}, 1)
	watcher.OnChange(func(oldCfg, newCfg *Config) {
		select {
		case changeCh <- struct{}{}:
		default:
		}
	})

	if err := watcher.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer watcher.Stop()

	// Wait for initial scan
	time.Sleep(100 * time.Millisecond)

	// Modify config file
	mgr2, _ := NewManager()
	mgr2.homeDir = homeDir
	mgr2.cfgPath = cfgPath
	mgr2.Set("provider", "anthropic")
	mgr2.Set("model", "claude-3")
	mgr2.Save()

	// Wait for watcher to detect change
	select {
	case <-changeCh:
		// Got change notification
	case <-time.After(2 * time.Second):
		t.Error("timeout waiting for config change")
	}

	cfg := watcher.GetConfig()
	if cfg.Provider != "anthropic" {
		t.Errorf("expected anthropic, got %s", cfg.Provider)
	}
}

func TestConfigWatcherForceReload(t *testing.T) {
	tmpDir := t.TempDir()
	homeDir := filepath.Join(tmpDir, ".luckyagent")
	cfgPath := filepath.Join(homeDir, "config.yaml")

	mgr, err := NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	mgr.homeDir = homeDir
	mgr.cfgPath = cfgPath

	mgr.Set("provider", "openai")
	mgr.Save()

	watcher := NewConfigWatcher(mgr, 10*time.Second) // long interval, won't auto-detect

	// Modify config externally
	mgr2, _ := NewManager()
	mgr2.homeDir = homeDir
	mgr2.cfgPath = cfgPath
	mgr2.Set("provider", "ollama")
	mgr2.Save()

	// Force reload
	if err := watcher.ForceReload(); err != nil {
		t.Fatalf("ForceReload: %v", err)
	}

	cfg := watcher.GetConfig()
	if cfg.Provider != "ollama" {
		t.Errorf("expected ollama after force reload, got %s", cfg.Provider)
	}
}

func TestConfigWatcherStop(t *testing.T) {
	mgr, err := NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	watcher := NewConfigWatcher(mgr, 100*time.Millisecond)

	if err := watcher.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if !watcher.IsRunning() {
		t.Error("expected running")
	}

	watcher.Stop()

	if watcher.IsRunning() {
		t.Error("expected stopped")
	}
}

func TestDiffConfig(t *testing.T) {
	oldCfg := &Config{
		Provider:    "openai",
		Model:       "gpt-5.4-mini",
		MaxTokens:   40960,
		Temperature: 0.1,
	}

	newCfg := &Config{
		Provider:    "anthropic",
		Model:       "gpt-5.4-mini",
		MaxTokens:   81920,
		Temperature: 0.1,
	}

	diff := DiffConfig(oldCfg, newCfg)

	if !diff.HasChanged() {
		t.Error("expected changes")
	}
	if len(diff.ChangedFields) != 2 {
		t.Errorf("expected 2 changed fields, got %d", len(diff.ChangedFields))
	}

	formatted := diff.Format()
	if formatted == "" {
		t.Error("expected non-empty format")
	}
}

func TestDiffConfigNoChange(t *testing.T) {
	cfg1 := &Config{Provider: "openai", Model: "gpt-5.4-mini"}
	cfg2 := &Config{Provider: "openai", Model: "gpt-5.4-mini"}

	diff := DiffConfig(cfg1, cfg2)
	if diff.HasChanged() {
		t.Error("expected no changes")
	}
}

func TestManagerReload(t *testing.T) {
	tmpDir := t.TempDir()
	homeDir := filepath.Join(tmpDir, ".luckyagent")
	cfgPath := filepath.Join(homeDir, "config.json")

	mgr, err := NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	mgr.homeDir = homeDir
	mgr.cfgPath = cfgPath

	mgr.Set("provider", "openai")
	mgr.Save()

	// Modify externally using the current persisted schema.
	os.WriteFile(cfgPath, []byte(`{"llm_provider":{"name":"anthropic","model":"claude-3"}}`+"\n"), 0o600)

	// Reload
	if err := mgr.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	cfg := mgr.Get()
	if cfg.Provider != "anthropic" {
		t.Errorf("expected anthropic after reload, got %s", cfg.Provider)
	}
}

func TestManagerWatchConfig(t *testing.T) {
	mgr, err := NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	watcher, err := mgr.WatchConfig(1 * time.Second)
	if err != nil {
		t.Fatalf("WatchConfig: %v", err)
	}
	if watcher == nil {
		t.Error("expected non-nil watcher")
	}
}
