package config

import "testing"

func TestAutonomyRecoveryConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	m, err := NewManagerWithDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	settings := map[string]string{
		"autonomy.recovery.resume_on_start":       "false",
		"autonomy.recovery.max_retries":           "0",
		"autonomy.recovery.retry_initial_seconds": "7",
		"autonomy.recovery.retry_max_seconds":     "60",
		"autonomy.recovery.max_slices":            "80",
		"autonomy.recovery.max_total_seconds":     "7200",
	}
	for key, value := range settings {
		if err := m.Set(key, value); err != nil {
			t.Fatalf("%s: %v", key, err)
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
	got := reloaded.Get().Autonomy.Recovery
	if got.ResumeOnStart == nil || *got.ResumeOnStart || got.MaxRetries == nil || *got.MaxRetries != 0 || got.RetryInitialSeconds != 7 || got.RetryMaxSeconds != 60 || got.MaxSlices != 80 || got.MaxTotalSeconds != 7200 {
		t.Fatalf("recovery config did not round-trip: %+v", got)
	}
	if err := m.Set("autonomy.recovery.max_slices", "0"); err == nil {
		t.Fatal("zero execution budget accepted")
	}
	if err := m.Set("autonomy.recovery.max_retries", "-1"); err == nil {
		t.Fatal("negative retry budget accepted")
	}
	copy := reloaded.Get()
	*copy.Autonomy.Recovery.MaxRetries = 99
	*copy.Autonomy.Recovery.ResumeOnStart = true
	if *reloaded.Get().Autonomy.Recovery.MaxRetries != 0 || *reloaded.Get().Autonomy.Recovery.ResumeOnStart {
		t.Fatal("configuration snapshot shares recovery pointers")
	}
}
