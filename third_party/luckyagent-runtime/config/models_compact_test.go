package config

import "testing"

func TestCompactKindSyncsContextCompactModel(t *testing.T) {
	cfg := &Config{}
	normalizeModels(cfg)
	if err := cfg.SetModelSelection(ModelKindCompact, "compact-mini", ModelEndpointConfig{}); err != nil {
		t.Fatal(err)
	}
	if cfg.Context.CompactModel != "compact-mini" {
		t.Fatalf("compact_model = %q", cfg.Context.CompactModel)
	}
	selected, ok := cfg.ModelSelection(ModelKindCompact)
	if !ok || selected.ID != "compact-mini" {
		t.Fatalf("selection = %+v ok=%v", selected, ok)
	}
	cfg.Context.CompactModel = "from-legacy"
	normalizeModels(cfg)
	selected, ok = cfg.ModelSelection(ModelKindCompact)
	if !ok || selected.ID != "compact-mini" {
		t.Fatalf("active compact selection was overwritten: %+v", selected)
	}
}
