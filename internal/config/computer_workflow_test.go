package config

import "testing"

func TestComputerWorkflowConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	m, err := NewManagerWithDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if defaults := m.Get().Tools.ComputerUse; defaults.SettleMode != "adaptive" || defaults.MaxBatchActions != 5 {
		t.Fatalf("missing workflow defaults: %+v", defaults)
	}
	for key, value := range map[string]string{"tools.computer_use.settle_mode": "fixed", "tools.computer_use.max_batch_actions": "3"} {
		if err := m.Set(key, value); err != nil {
			t.Fatal(err)
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
	got := reloaded.Get().Tools.ComputerUse
	if got.SettleMode != "fixed" || got.MaxBatchActions != 3 {
		t.Fatalf("workflow settings did not persist: %+v", got)
	}
	for key, value := range map[string]string{"tools.computer_use.settle_mode": "forever", "tools.computer_use.max_batch_actions": "11"} {
		if err := m.Set(key, value); err == nil {
			t.Fatalf("accepted invalid %s=%s", key, value)
		}
	}
	if got := m.Get().Tools.ComputerUse; got.SettleMode != "fixed" || got.MaxBatchActions != 3 {
		t.Fatal("invalid setting modified live config")
	}
}
