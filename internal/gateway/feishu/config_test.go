package feishu

import "testing"

func TestConfigNormalizationAndAllowlists(t *testing.T) {
	cfg := Config{
		Path:             "events",
		APIBaseURL:       "https://example.test/",
		GroupTriggerMode: "OPEN",
		AllowedChats:     []string{" oc_allowed "},
		AllowedUsers:     []string{"ou_allowed", "u_allowed"},
	}
	if got := cfg.normalizedPath(); got != "/events" {
		t.Fatalf("normalizedPath() = %q", got)
	}
	if got := cfg.normalizedAPIBaseURL(); got != "https://example.test" {
		t.Fatalf("normalizedAPIBaseURL() = %q", got)
	}
	if got := cfg.normalizedGroupTriggerMode(); got != "all" {
		t.Fatalf("normalizedGroupTriggerMode() = %q", got)
	}
	if !cfg.isChatAllowed("oc_allowed") || cfg.isChatAllowed("oc_denied") {
		t.Fatal("chat allowlist was not enforced")
	}
	if !cfg.isUserAllowed("ou_other", "u_allowed") || cfg.isUserAllowed("ou_denied") {
		t.Fatal("user allowlist was not enforced across Feishu ID forms")
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.normalizedListenAddr() != defaultListenAddr || cfg.normalizedPath() != defaultPath {
		t.Fatalf("unexpected callback defaults: %+v", cfg)
	}
	if !cfg.RemoveAt || cfg.normalizedGroupTriggerMode() != "mention" {
		t.Fatalf("unexpected message defaults: %+v", cfg)
	}
}
