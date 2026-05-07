package profile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestManagerCreateAndGet(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, err := NewManager(tmpDir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	// default 应该自动创建
	p, err := mgr.Get("default")
	if err != nil {
		t.Fatalf("Get default: %v", err)
	}
	if p.Name != "default" {
		t.Errorf("expected name default, got %s", p.Name)
	}
	if p.Provider != "openai" {
		t.Errorf("expected provider openai, got %s", p.Provider)
	}
}

func TestManagerCreateProfile(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, err := NewManager(tmpDir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	p, err := mgr.Create("work", "Work profile")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if p.Name != "work" {
		t.Errorf("expected name work, got %s", p.Name)
	}
	if p.Description != "Work profile" {
		t.Errorf("expected description 'Work profile', got %s", p.Description)
	}

	// 验证数据目录创建
	dataDir := mgr.ProfileDataDir("work")
	if _, err := os.Stat(dataDir); os.IsNotExist(err) {
		t.Errorf("profile data dir not created: %s", dataDir)
	}
	for _, sub := range []string{"sessions", "memory", "logs", "skills"} {
		if _, err := os.Stat(filepath.Join(dataDir, sub)); os.IsNotExist(err) {
			t.Errorf("profile sub-dir not created: %s", sub)
		}
	}
}

func TestManagerDuplicateCreate(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, err := NewManager(tmpDir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	_, err = mgr.Create("test", "Test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = mgr.Create("test", "Test again")
	if err == nil {
		t.Error("expected error for duplicate profile")
	}
}

func TestManagerSwitch(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, err := NewManager(tmpDir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	_, err = mgr.Create("personal", "Personal profile")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := mgr.Switch("personal"); err != nil {
		t.Fatalf("Switch: %v", err)
	}

	if mgr.ActiveName() != "personal" {
		t.Errorf("expected active personal, got %s", mgr.ActiveName())
	}

	active := mgr.Active()
	if active.Name != "personal" {
		t.Errorf("expected active name personal, got %s", active.Name)
	}
}

func TestManagerSwitchNonExistent(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, err := NewManager(tmpDir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	err = mgr.Switch("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent profile")
	}
}

func TestManagerDelete(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, err := NewManager(tmpDir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	_, err = mgr.Create("temp", "Temporary")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := mgr.Delete("temp"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := mgr.Get("temp"); err == nil {
		t.Error("expected error after delete")
	}
}

func TestManagerDeleteDefault(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, err := NewManager(tmpDir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	err = mgr.Delete("default")
	if err == nil {
		t.Error("expected error when deleting default profile")
	}
}

func TestManagerDeleteActive(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, err := NewManager(tmpDir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	_, err = mgr.Create("active", "Active profile")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_ = mgr.Switch("active")

	err = mgr.Delete("active")
	if err == nil {
		t.Error("expected error when deleting active profile")
	}
}

func TestManagerList(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, err := NewManager(tmpDir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	_, _ = mgr.Create("beta", "Beta")
	_, _ = mgr.Create("alpha", "Alpha")

	names := mgr.List()
	if len(names) < 3 { // default + alpha + beta
		t.Errorf("expected at least 3 profiles, got %d", len(names))
	}

	// 应该排序
	if names[0] != "alpha" {
		t.Errorf("expected first alpha, got %s", names[0])
	}
}

func TestManagerListWithInfo(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, err := NewManager(tmpDir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	_, _ = mgr.Create("work", "Work profile")

	infos := mgr.ListWithInfo()
	found := false
	for _, info := range infos {
		if info.Name == "work" {
			found = true
			if info.Description != "Work profile" {
				t.Errorf("expected description 'Work profile', got %s", info.Description)
			}
			if info.Active {
				t.Error("work should not be active")
			}
		}
		if info.Name == "default" && !info.Active {
			t.Error("default should be active")
		}
	}
	if !found {
		t.Error("work profile not found in list")
	}
}

func TestManagerUpdate(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, err := NewManager(tmpDir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	_, _ = mgr.Create("test", "Test")

	err = mgr.Update("test", func(p *Profile) {
		p.Provider = "anthropic"
		p.Model = "claude-3"
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	p, _ := mgr.Get("test")
	if p.Provider != "anthropic" {
		t.Errorf("expected anthropic, got %s", p.Provider)
	}
	if p.Model != "claude-3" {
		t.Errorf("expected claude-3, got %s", p.Model)
	}
}

func TestManagerEnv(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, err := NewManager(tmpDir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	_, _ = mgr.Create("envtest", "Env test")

	if err := mgr.SetEnv("envtest", "API_KEY", "sk-test123"); err != nil {
		t.Fatalf("SetEnv: %v", err)
	}

	p, _ := mgr.Get("envtest")
	if p.Env["API_KEY"] != "sk-test123" {
		t.Errorf("expected API_KEY sk-test123, got %s", p.Env["API_KEY"])
	}

	if err := mgr.UnsetEnv("envtest", "API_KEY"); err != nil {
		t.Fatalf("UnsetEnv: %v", err)
	}

	p, _ = mgr.Get("envtest")
	if _, ok := p.Env["API_KEY"]; ok {
		t.Error("API_KEY should be unset")
	}
}

func TestManagerApplyEnv(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, err := NewManager(tmpDir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	_, _ = mgr.Create("envtest", "Env test")
	_ = mgr.SetEnv("envtest", "LH_TEST_VAR", "hello_world")

	if err := mgr.ApplyEnv("envtest"); err != nil {
		t.Fatalf("ApplyEnv: %v", err)
	}

	if os.Getenv("LH_TEST_VAR") != "hello_world" {
		t.Errorf("expected LH_TEST_VAR=hello_world, got %s", os.Getenv("LH_TEST_VAR"))
	}
}

func TestManagerPersistence(t *testing.T) {
	tmpDir := t.TempDir()

	// 创建 manager 并添加 profile
	mgr1, err := NewManager(tmpDir)
	if err != nil {
		t.Fatalf("NewManager1: %v", err)
	}

	_, _ = mgr1.Create("persist", "Persistence test")
	_ = mgr1.Switch("persist")

	// 重新加载
	mgr2, err := NewManager(tmpDir)
	if err != nil {
		t.Fatalf("NewManager2: %v", err)
	}

	if mgr2.ActiveName() != "persist" {
		t.Errorf("expected active persist, got %s", mgr2.ActiveName())
	}

	p, err := mgr2.Get("persist")
	if err != nil {
		t.Fatalf("Get persist: %v", err)
	}
	if p.Description != "Persistence test" {
		t.Errorf("expected 'Persistence test', got %s", p.Description)
	}
}

func TestManagerInvalidName(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, err := NewManager(tmpDir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	_, err = mgr.Create("", "Empty name")
	if err == nil {
		t.Error("expected error for empty name")
	}

	_, err = mgr.Create("a/b", "Slash name")
	if err == nil {
		t.Error("expected error for slash in name")
	}

	_, err = mgr.Create("..", "Dotdot name")
	if err == nil {
		t.Error("expected error for .. in name")
	}
}

func TestManagerCount(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, err := NewManager(tmpDir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	if mgr.Count() != 1 { // default
		t.Errorf("expected 1 profile, got %d", mgr.Count())
	}

	_, _ = mgr.Create("extra", "Extra")
	if mgr.Count() != 2 {
		t.Errorf("expected 2 profiles, got %d", mgr.Count())
	}
}
