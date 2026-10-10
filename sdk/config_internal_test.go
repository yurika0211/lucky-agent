package sdk

import (
	"testing"

	"github.com/yurika0211/luckyagent/internal/config"
)

func TestConfigApplyToSetsMaxContextTokens(t *testing.T) {
	mgr, err := config.NewManagerWithDir(t.TempDir())
	if err != nil {
		t.Fatalf("NewManagerWithDir: %v", err)
	}
	if err := (Config{MaxContextTokens: 32000}).applyTo(mgr); err != nil {
		t.Fatalf("applyTo: %v", err)
	}
	if got := mgr.Get().Context.MaxContextTokens; got != 32000 {
		t.Fatalf("MaxContextTokens = %d, want 32000", got)
	}
}

func TestConfigApplyToKeepsDefaultMaxContextTokens(t *testing.T) {
	mgr, err := config.NewManagerWithDir(t.TempDir())
	if err != nil {
		t.Fatalf("NewManagerWithDir: %v", err)
	}
	want := mgr.Get().Context.MaxContextTokens
	if err := (Config{}).applyTo(mgr); err != nil {
		t.Fatalf("applyTo: %v", err)
	}
	if got := mgr.Get().Context.MaxContextTokens; got != want {
		t.Fatalf("MaxContextTokens = %d, want %d", got, want)
	}
}
