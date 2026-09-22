package config

import "testing"

func TestForegroundConfigRoundTripAndIsolation(t *testing.T) {
	dir := t.TempDir()
	m, err := NewManagerWithDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	d := m.Get().Agent.Foreground
	if d.Enabled == nil || !*d.Enabled || d.MaxSlices != 48 || d.MaxTotalSeconds != 3600 || d.MaxRetries == nil || *d.MaxRetries != 3 {
		t.Fatalf("defaults=%+v", d)
	}
	for k, v := range map[string]string{"agent.foreground.enabled": "false", "agent.foreground.max_retries": "0", "agent.foreground.max_slices": "16", "agent.foreground.max_total_seconds": "900"} {
		if err := m.Set(k, v); err != nil {
			t.Fatal(err)
		}
	}
	for k, v := range map[string]string{"agent.foreground.enabled": "nonsense", "agent.foreground.max_retries": "-1", "agent.foreground.max_slices": "0", "agent.foreground.max_total_seconds": "bad"} {
		if err := m.Set(k, v); err == nil {
			t.Fatalf("accepted invalid %s", k)
		}
	}
	if err := m.Save(); err != nil {
		t.Fatal(err)
	}
	reloaded, err := NewManagerWithDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	d = reloaded.Get().Agent.Foreground
	if *d.Enabled || *d.MaxRetries != 0 || d.MaxSlices != 16 || d.MaxTotalSeconds != 900 {
		t.Fatalf("round-trip=%+v", d)
	}
	*d.Enabled = true
	*d.MaxRetries = 99
	if *reloaded.Get().Agent.Foreground.Enabled || *reloaded.Get().Agent.Foreground.MaxRetries != 0 {
		t.Fatal("snapshot pointers alias live configuration")
	}
}
