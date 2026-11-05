package config

import "testing"

func TestContextLimitPreservesLegacyFilesAndCanonicalPrecedence(t *testing.T) {
	for _, tc := range []struct {
		body string
		want int
	}{
		{`{"max_tokens":16000}`, 16000},
		{`{"max_tokens":4096,"context":{"max_context_tokens":32000}}`, 32000},
		{`{"context":{"max_context_tokens":12000}}`, 12000},
		{`{}`, 8000},
	} {
		cfg, err := parseConfigData([]byte(tc.body))
		if err != nil || cfg.Context.MaxContextTokens != tc.want {
			t.Fatalf("%s: cfg=%+v err=%v", tc.body, cfg, err)
		}
	}
	mgr, err := NewManagerWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Set("max_tokens", "24000"); err != nil {
		t.Fatal(err)
	}
	if mgr.Get().Context.MaxContextTokens != 24000 {
		t.Fatal("legacy CLI setter no longer changes the actual window")
	}
}
