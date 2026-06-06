package openclawweixin

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAccount(t *testing.T) {
	stateDir := t.TempDir()
	accountDir := filepath.Join(stateDir, "openclaw-weixin", "accounts")
	if err := os.MkdirAll(accountDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	path := filepath.Join(accountDir, "acct-1.json")
	data := []byte(`{"token":"test-token","baseUrl":"https://ilink.example.com"}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write account: %v", err)
	}

	account, err := loadAccount(stateDir, "acct-1")
	if err != nil {
		t.Fatalf("loadAccount: %v", err)
	}
	if account.Token != "test-token" {
		t.Fatalf("unexpected token: %q", account.Token)
	}
	if account.BaseURL != "https://ilink.example.com" {
		t.Fatalf("unexpected baseURL: %q", account.BaseURL)
	}
}

func TestStartRequiresAccountID(t *testing.T) {
	adapter := NewAdapter(Config{})
	if err := adapter.Start(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestResolveStateDirPrefersConfig(t *testing.T) {
	t.Setenv("OPENCLAW_STATE_DIR", filepath.Join(t.TempDir(), "env-openclaw"))
	t.Setenv("CLAWDBOT_STATE_DIR", filepath.Join(t.TempDir(), "env-clawdbot"))

	got := resolveStateDir(`C:\custom-openclaw`)
	if got != `C:\custom-openclaw` {
		t.Fatalf("unexpected state dir: %q", got)
	}
}
