package tool

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestSkillRegistryDiscover(t *testing.T) {
	tmpDir := t.TempDir()
	for _, name := range []string{"skill-a", "skill-b"} {
		skillDir := filepath.Join(tmpDir, name)
		os.MkdirAll(skillDir, 0755)
		os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# "+name+"\n\nDesc.\n\n## Tools\n\n- `search`: Search\n"), 0644)
	}

	r := NewRegistry()
	loader := NewSkillLoader(tmpDir)
	sr := NewSkillRegistry(r, loader)

	discovered, err := sr.Discover()
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(discovered) != 2 {
		t.Errorf("expected 2 discovered skills, got %d", len(discovered))
	}
	if sr.Count() != 2 {
		t.Errorf("expected count 2, got %d", sr.Count())
	}
}

func TestSkillRegistryLoad(t *testing.T) {
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "test-skill")
	os.MkdirAll(skillDir, 0755)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# test-skill\n\nA test skill.\n\n## Tools\n\n- `search`: Search\n- `analyze`: Analyze\n"), 0644)

	r := NewRegistry()
	loader := NewSkillLoader(tmpDir)
	sr := NewSkillRegistry(r, loader)

	sr.Discover()

	err := sr.Load("test-skill")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	meta, ok := sr.Get("test-skill")
	if !ok {
		t.Fatal("skill not found after load")
	}
	if meta.State != SkillLoaded {
		t.Errorf("expected SkillLoaded, got %s", meta.State)
	}
	if len(meta.Tools) != 2 {
		t.Errorf("expected 2 tools, got %d", len(meta.Tools))
	}
}

func TestSkillRegistryRegister(t *testing.T) {
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "test-skill")
	os.MkdirAll(skillDir, 0755)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# test-skill\n\nDesc.\n\n## Tools\n\n- `search`: Search\n"), 0644)

	r := NewRegistry()
	loader := NewSkillLoader(tmpDir)
	sr := NewSkillRegistry(r, loader)

	sr.Discover()
	sr.Load("test-skill")

	err := sr.Register("test-skill")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	meta, _ := sr.Get("test-skill")
	if meta.State != SkillRegistered {
		t.Errorf("expected SkillRegistered, got %s", meta.State)
	}

	// Check tool is in registry
	tool, ok := r.Get("skill_test-skill_search")
	if !ok {
		t.Error("tool not registered in registry")
	}
	if tool.Enabled {
		t.Error("tool should be disabled until skill is enabled")
	}
}

func TestSkillRegistryRegisterUsesSkillExecutor(t *testing.T) {
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "script-skill")
	scriptsDir := filepath.Join(skillDir, "scripts")
	os.MkdirAll(scriptsDir, 0755)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# script-skill\n\nDesc.\n\n## Tools\n\n- `echo`: Echo\n"), 0644)
	os.WriteFile(filepath.Join(scriptsDir, "echo.sh"), []byte("echo skill-executed\n"), 0644)

	r := NewRegistry()
	loader := NewSkillLoader(tmpDir)
	sr := NewSkillRegistry(r, loader)

	if _, err := sr.Discover(); err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if err := sr.Load("script-skill"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := sr.Validate("script-skill"); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if err := sr.Register("script-skill"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := sr.Enable("script-skill"); err != nil {
		t.Fatalf("Enable: %v", err)
	}

	out, err := r.Call("skill_script-skill_echo", nil)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if out != "skill-executed\n" {
		t.Fatalf("expected script output, got %q", out)
	}
}

func TestSkillRegistryValidateDuplicateTool(t *testing.T) {
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "bad-skill")
	os.MkdirAll(skillDir, 0755)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# bad-skill\n\nDesc.\n\n## Tools\n\n- `dup`: First\n- `dup`: Second\n"), 0644)

	r := NewRegistry()
	loader := NewSkillLoader(tmpDir)
	sr := NewSkillRegistry(r, loader)

	if _, err := sr.Discover(); err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if err := sr.Load("bad-skill"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := sr.Validate("bad-skill"); err == nil {
		t.Fatal("expected duplicate tool validation error")
	}
}

func TestSkillRegistryEnable(t *testing.T) {
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "test-skill")
	os.MkdirAll(skillDir, 0755)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# test-skill\n\nDesc.\n\n## Tools\n\n- `search`: Search\n"), 0644)

	r := NewRegistry()
	loader := NewSkillLoader(tmpDir)
	sr := NewSkillRegistry(r, loader)

	sr.Discover()
	sr.Load("test-skill")
	sr.Register("test-skill")

	err := sr.Enable("test-skill")
	if err != nil {
		t.Fatalf("Enable: %v", err)
	}

	meta, _ := sr.Get("test-skill")
	if meta.State != SkillEnabled {
		t.Errorf("expected SkillEnabled, got %s", meta.State)
	}

	tool, _ := r.Get("skill_test-skill_search")
	if !tool.Enabled {
		t.Error("tool should be enabled")
	}
}

func TestSkillRegistryDisable(t *testing.T) {
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "test-skill")
	os.MkdirAll(skillDir, 0755)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# test-skill\n\nDesc.\n\n## Tools\n\n- `search`: Search\n"), 0644)

	r := NewRegistry()
	loader := NewSkillLoader(tmpDir)
	sr := NewSkillRegistry(r, loader)

	sr.Discover()
	sr.Load("test-skill")
	sr.Register("test-skill")
	sr.Enable("test-skill")

	err := sr.Disable("test-skill")
	if err != nil {
		t.Fatalf("Disable: %v", err)
	}

	meta, _ := sr.Get("test-skill")
	if meta.State != SkillDisabled {
		t.Errorf("expected SkillDisabled, got %s", meta.State)
	}

	tool, _ := r.Get("skill_test-skill_search")
	if tool.Enabled {
		t.Error("tool should be disabled")
	}
}

func TestSkillRegistryUnload(t *testing.T) {
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "test-skill")
	os.MkdirAll(skillDir, 0755)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# test-skill\n\nDesc.\n\n## Tools\n\n- `search`: Search\n"), 0644)

	r := NewRegistry()
	loader := NewSkillLoader(tmpDir)
	sr := NewSkillRegistry(r, loader)

	sr.Discover()
	sr.Load("test-skill")
	sr.Register("test-skill")
	sr.Enable("test-skill")

	err := sr.Unload("test-skill")
	if err != nil {
		t.Fatalf("Unload: %v", err)
	}

	meta, _ := sr.Get("test-skill")
	if meta.State != SkillUnloaded {
		t.Errorf("expected SkillUnloaded, got %s", meta.State)
	}

	_, ok := r.Get("skill_test-skill_search")
	if ok {
		t.Error("tool should be unregistered after unload")
	}
}

func TestSkillRegistryLifecycle(t *testing.T) {
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "my-skill")
	os.MkdirAll(skillDir, 0755)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# my-skill\n\nDesc.\n\n## Tools\n\n- `do_thing`: Do something\n"), 0644)

	r := NewRegistry()
	loader := NewSkillLoader(tmpDir)
	sr := NewSkillRegistry(r, loader)

	// Full lifecycle
	sr.Discover()
	sr.Load("my-skill")
	sr.Register("my-skill")
	sr.Enable("my-skill")

	meta, _ := sr.Get("my-skill")
	if meta.State != SkillEnabled {
		t.Errorf("expected enabled, got %s", meta.State)
	}

	// Disable
	sr.Disable("my-skill")
	meta, _ = sr.Get("my-skill")
	if meta.State != SkillDisabled {
		t.Errorf("expected disabled, got %s", meta.State)
	}

	// Re-enable
	sr.Enable("my-skill")
	meta, _ = sr.Get("my-skill")
	if meta.State != SkillEnabled {
		t.Errorf("expected enabled after re-enable, got %s", meta.State)
	}
}

func TestSkillRegistryLoadOrder(t *testing.T) {
	r := NewRegistry()
	loader := NewSkillLoader(t.TempDir())
	sr := NewSkillRegistry(r, loader)

	// Manually add skills with dependencies
	sr.skills["base"] = &SkillMetadata{Name: "base", State: SkillLoaded, Dependencies: []string{}}
	sr.skills["middleware"] = &SkillMetadata{Name: "middleware", State: SkillLoaded, Dependencies: []string{"base"}}
	sr.skills["app"] = &SkillMetadata{Name: "app", State: SkillLoaded, Dependencies: []string{"middleware"}}

	order, err := sr.ResolveLoadOrder()
	if err != nil {
		t.Fatalf("ResolveLoadOrder: %v", err)
	}

	if len(order) != 3 {
		t.Fatalf("expected 3 skills in order, got %d", len(order))
	}

	// base should come before middleware, middleware before app
	baseIdx := indexOf(order, "base")
	midIdx := indexOf(order, "middleware")
	appIdx := indexOf(order, "app")

	if baseIdx > midIdx {
		t.Errorf("base should come before middleware: base=%d, middleware=%d", baseIdx, midIdx)
	}
	if midIdx > appIdx {
		t.Errorf("middleware should come before app: middleware=%d, app=%d", midIdx, appIdx)
	}
}

func TestSkillRegistryCircularDependency(t *testing.T) {
	r := NewRegistry()
	loader := NewSkillLoader(t.TempDir())
	sr := NewSkillRegistry(r, loader)

	sr.skills["a"] = &SkillMetadata{Name: "a", State: SkillLoaded, Dependencies: []string{"b"}}
	sr.skills["b"] = &SkillMetadata{Name: "b", State: SkillLoaded, Dependencies: []string{"a"}}

	_, err := sr.ResolveLoadOrder()
	if err == nil {
		t.Error("expected error for circular dependency")
	}
}

func TestSkillRegistryMissingDependency(t *testing.T) {
	r := NewRegistry()
	loader := NewSkillLoader(t.TempDir())
	sr := NewSkillRegistry(r, loader)

	sr.skills["a"] = &SkillMetadata{Name: "a", State: SkillLoaded, Dependencies: []string{"nonexistent"}}

	_, err := sr.ResolveLoadOrder()
	if err == nil {
		t.Error("expected error for missing dependency")
	}
}

func TestSkillRegistryHealthCheck(t *testing.T) {
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "healthy-skill")
	os.MkdirAll(skillDir, 0755)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# healthy-skill\n\nDesc.\n\n## Tools\n\n- `search`: Search\n"), 0644)

	r := NewRegistry()
	loader := NewSkillLoader(tmpDir)
	sr := NewSkillRegistry(r, loader)

	sr.Discover()
	sr.Load("healthy-skill")
	sr.Register("healthy-skill")
	sr.Enable("healthy-skill")

	unhealthy := sr.HealthCheck()
	if len(unhealthy) != 0 {
		t.Errorf("expected no unhealthy skills, got %v", unhealthy)
	}

	// Manually disable a tool to make it unhealthy
	r.Disable("skill_healthy-skill_search")
	unhealthy = sr.HealthCheck()
	if len(unhealthy) == 0 {
		t.Error("expected unhealthy skill after disabling tool")
	}
	if _, ok := unhealthy["healthy-skill"]; !ok {
		t.Error("expected healthy-skill to be unhealthy")
	}
}

func TestSkillRegistrySetMetadata(t *testing.T) {
	r := NewRegistry()
	loader := NewSkillLoader(t.TempDir())
	sr := NewSkillRegistry(r, loader)

	sr.skills["test"] = &SkillMetadata{
		Name:   "test",
		State:  SkillLoaded,
		Labels: make(map[string]string),
	}

	err := sr.SetMetadata("test", "1.0.0", "author1", []string{"dep1"}, map[string]string{"env": "prod"})
	if err != nil {
		t.Fatalf("SetMetadata: %v", err)
	}

	meta, _ := sr.Get("test")
	if meta.Version != "1.0.0" {
		t.Errorf("expected version 1.0.0, got %s", meta.Version)
	}
	if meta.Author != "author1" {
		t.Errorf("expected author author1, got %s", meta.Author)
	}
	if len(meta.Dependencies) != 1 || meta.Dependencies[0] != "dep1" {
		t.Errorf("expected dependencies [dep1], got %v", meta.Dependencies)
	}
	if meta.Labels["env"] != "prod" {
		t.Errorf("expected label env=prod, got %s", meta.Labels["env"])
	}
}

func TestSkillRegistryWatchState(t *testing.T) {
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "watched-skill")
	os.MkdirAll(skillDir, 0755)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# watched-skill\n\nDesc.\n\n## Tools\n\n- `do`: Do\n"), 0644)

	r := NewRegistry()
	loader := NewSkillLoader(tmpDir)
	sr := NewSkillRegistry(r, loader)

	var transitions []string
	sr.WatchState("test-watcher", func(name string, from, to SkillState) {
		transitions = append(transitions, fmt.Sprintf("%s:%s->%s", name, from, to))
	})

	sr.Discover()
	sr.Load("watched-skill")
	sr.Register("watched-skill")
	sr.Enable("watched-skill")

	if len(transitions) < 3 {
		t.Errorf("expected at least 3 transitions, got %d: %v", len(transitions), transitions)
	}

	// Unwatch
	sr.UnwatchState("test-watcher")
}

func TestSkillRegistryListByState(t *testing.T) {
	r := NewRegistry()
	loader := NewSkillLoader(t.TempDir())
	sr := NewSkillRegistry(r, loader)

	sr.skills["a"] = &SkillMetadata{Name: "a", State: SkillEnabled}
	sr.skills["b"] = &SkillMetadata{Name: "b", State: SkillLoaded}
	sr.skills["c"] = &SkillMetadata{Name: "c", State: SkillEnabled}

	enabled := sr.ListByState(SkillEnabled)
	if len(enabled) != 2 {
		t.Errorf("expected 2 enabled, got %d", len(enabled))
	}

	loaded := sr.ListByState(SkillLoaded)
	if len(loaded) != 1 {
		t.Errorf("expected 1 loaded, got %d", len(loaded))
	}
}

func TestSkillRegistryCountByState(t *testing.T) {
	r := NewRegistry()
	loader := NewSkillLoader(t.TempDir())
	sr := NewSkillRegistry(r, loader)

	sr.skills["a"] = &SkillMetadata{Name: "a", State: SkillEnabled}
	sr.skills["b"] = &SkillMetadata{Name: "b", State: SkillEnabled}
	sr.skills["c"] = &SkillMetadata{Name: "c", State: SkillLoaded}

	if sr.CountByState(SkillEnabled) != 2 {
		t.Errorf("expected 2 enabled, got %d", sr.CountByState(SkillEnabled))
	}
	if sr.CountByState(SkillLoaded) != 1 {
		t.Errorf("expected 1 loaded, got %d", sr.CountByState(SkillLoaded))
	}
}

func TestSkillRegistryLoadNotFound(t *testing.T) {
	r := NewRegistry()
	loader := NewSkillLoader(t.TempDir())
	sr := NewSkillRegistry(r, loader)

	err := sr.Load("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent skill")
	}
}

func TestSkillRegistryEnableNotRegistered(t *testing.T) {
	r := NewRegistry()
	loader := NewSkillLoader(t.TempDir())
	sr := NewSkillRegistry(r, loader)

	sr.skills["test"] = &SkillMetadata{Name: "test", State: SkillLoaded}

	err := sr.Enable("test")
	if err == nil {
		t.Error("expected error when enabling non-registered skill")
	}
}

func TestSkillRegistryDisableNotEnabled(t *testing.T) {
	r := NewRegistry()
	loader := NewSkillLoader(t.TempDir())
	sr := NewSkillRegistry(r, loader)

	sr.skills["test"] = &SkillMetadata{Name: "test", State: SkillRegistered}

	err := sr.Disable("test")
	if err == nil {
		t.Error("expected error when disabling non-enabled skill")
	}
}

func TestSkillStateString(t *testing.T) {
	tests := []struct {
		state    SkillState
		expected string
	}{
		{SkillDiscovered, "discovered"},
		{SkillLoaded, "loaded"},
		{SkillRegistered, "registered"},
		{SkillEnabled, "enabled"},
		{SkillDisabled, "disabled"},
		{SkillError, "error"},
		{SkillUnloaded, "unloaded"},
	}
	for _, tt := range tests {
		if got := tt.state.String(); got != tt.expected {
			t.Errorf("SkillState(%d).String() = %q, want %q", tt.state, got, tt.expected)
		}
	}
}

func TestSkillVersion(t *testing.T) {
	if SkillVersion != "v0.5.0" {
		t.Errorf("expected SkillVersion v0.5.0, got %s", SkillVersion)
	}
}

func TestSkillRegistryReload(t *testing.T) {
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "reload-skill")
	os.MkdirAll(skillDir, 0755)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# reload-skill\n\nDesc.\n\n## Tools\n\n- `do`: Do\n"), 0644)

	r := NewRegistry()
	loader := NewSkillLoader(tmpDir)
	sr := NewSkillRegistry(r, loader)

	sr.Discover()
	sr.Load("reload-skill")
	sr.Register("reload-skill")
	sr.Enable("reload-skill")

	meta, _ := sr.Get("reload-skill")
	if meta.State != SkillEnabled {
		t.Errorf("expected enabled before reload, got %s", meta.State)
	}

	err := sr.Reload("reload-skill")
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}

	meta, _ = sr.Get("reload-skill")
	if meta.State != SkillEnabled {
		t.Errorf("expected enabled after reload (was enabled before), got %s", meta.State)
	}
}

func TestSkillRegistryLoadAll(t *testing.T) {
	tmpDir := t.TempDir()
	for _, name := range []string{"s1", "s2"} {
		skillDir := filepath.Join(tmpDir, name)
		os.MkdirAll(skillDir, 0755)
		os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# "+name+"\n\nDesc.\n\n## Tools\n\n- `do`: Do\n"), 0644)
	}

	r := NewRegistry()
	loader := NewSkillLoader(tmpDir)
	sr := NewSkillRegistry(r, loader)

	sr.Discover()
	err := sr.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	loaded := sr.ListByState(SkillLoaded)
	if len(loaded) != 2 {
		t.Errorf("expected 2 loaded, got %d", len(loaded))
	}
}

func TestSkillRegistryRegisterAll(t *testing.T) {
	tmpDir := t.TempDir()
	for _, name := range []string{"s1", "s2"} {
		skillDir := filepath.Join(tmpDir, name)
		os.MkdirAll(skillDir, 0755)
		os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# "+name+"\n\nDesc.\n\n## Tools\n\n- `do`: Do\n"), 0644)
	}

	r := NewRegistry()
	loader := NewSkillLoader(tmpDir)
	sr := NewSkillRegistry(r, loader)

	sr.Discover()
	sr.LoadAll()
	err := sr.RegisterAll()
	if err != nil {
		t.Fatalf("RegisterAll: %v", err)
	}

	registered := sr.ListByState(SkillRegistered)
	if len(registered) != 2 {
		t.Errorf("expected 2 registered, got %d", len(registered))
	}
}

// Helper function
func indexOf(slice []string, item string) int {
	for i, s := range slice {
		if s == item {
			return i
		}
	}
	return -1
}
