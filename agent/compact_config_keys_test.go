package agent

import (
	"testing"

	"github.com/yurika0211/luckyagent-runtime/config"
)

// #531 gap 1: the agent used to read these six keys only from Config.Extra, so
// a key written through `lh config set` could be invisible to compaction.
func TestCompactSettingsReadsFormalContextFields(t *testing.T) {
	m, err := config.NewManagerWithDir(t.TempDir())
	if err != nil {
		t.Fatalf("NewManagerWithDir: %v", err)
	}
	for key, value := range map[string]string{
		"context.compact_keep_tokens":             "20000",
		"context.compact_precompute_lead_tokens":  "80000",
		"context.compact_precompute_max_failures": "3",
		"context.compact_cache_margin_seconds":    "45",
		"context.compact_background":              "false",
		"context.compact_chunk_fallback":          "false",
	} {
		if err := m.Set(key, value); err != nil {
			t.Fatalf("Set(%s): %v", key, err)
		}
	}

	a := &Agent{cfg: m}
	got := a.compactSettings()

	if got.KeepTokens != 20000 {
		t.Fatalf("KeepTokens = %d, want 20000", got.KeepTokens)
	}
	if got.LeadTokens != 80000 {
		t.Fatalf("LeadTokens = %d, want 80000", got.LeadTokens)
	}
	if got.MaxFailures != 3 {
		t.Fatalf("MaxFailures = %d, want 3", got.MaxFailures)
	}
	if got.CacheMargin.Seconds() != 45 {
		t.Fatalf("CacheMargin = %v, want 45s", got.CacheMargin)
	}
	if got.Background {
		t.Fatalf("Background = true, want false")
	}
	if got.ChunkFallback {
		t.Fatalf("ChunkFallback = true, want false")
	}
}

func TestCompactSettingsKeepsExtraCompatibility(t *testing.T) {
	cfg := config.DefaultConfig()
	if cfg.Extra == nil {
		cfg.Extra = map[string]string{}
	}
	// A config.json written before #531 only has these keys in the catch-all map.
	cfg.Extra["context.compact_keep_tokens"] = "18000"
	cfg.Extra["context.compact_background"] = "false"

	m, err := config.NewManagerWithDir(t.TempDir())
	if err != nil {
		t.Fatalf("NewManagerWithDir: %v", err)
	}
	m.SetConfig(cfg)

	a := &Agent{cfg: m}
	got := a.compactSettings()

	if got.KeepTokens != 18000 {
		t.Fatalf("KeepTokens = %d, want 18000 from Extra fallback", got.KeepTokens)
	}
	if got.Background {
		t.Fatalf("Background = true, want false from Extra fallback")
	}
}

func TestCompactSettingsPrefersFormalFieldOverExtra(t *testing.T) {
	cfg := config.DefaultConfig()
	if cfg.Extra == nil {
		cfg.Extra = map[string]string{}
	}
	cfg.Context.CompactKeepTokens = 30000
	cfg.Extra["context.compact_keep_tokens"] = "18000"

	m, err := config.NewManagerWithDir(t.TempDir())
	if err != nil {
		t.Fatalf("NewManagerWithDir: %v", err)
	}
	m.SetConfig(cfg)

	a := &Agent{cfg: m}
	if got := a.compactSettings().KeepTokens; got != 30000 {
		t.Fatalf("KeepTokens = %d, want the formal field value 30000", got)
	}
}

func TestCompactSettingsDefaultsWithoutConfig(t *testing.T) {
	a := &Agent{}
	got := a.compactSettings()
	if got.KeepTokens != defaultCompactKeepTokens || got.LeadTokens != defaultCompactPrecomputeLeadTokens {
		t.Fatalf("nil config = %+v, want built-in defaults", got)
	}
}