package lhcmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveUIWorkspaceAcceptsUIWorkspace(t *testing.T) {
	root := t.TempDir()
	ui := filepath.Join(root, "UI")
	mustWrite(t, filepath.Join(ui, "package.json"), "{}")
	mustWrite(t, filepath.Join(ui, "TUI", "package.json"), "{}")
	mustWrite(t, filepath.Join(ui, "TUI", "src", "index.tsx"), "")

	got, err := resolveUIWorkspace(ui)
	if err != nil {
		t.Fatalf("resolveUIWorkspace() error = %v", err)
	}
	if got != ui {
		t.Fatalf("resolveUIWorkspace() = %q, want %q", got, ui)
	}
}

func TestResolveUIWorkspaceAcceptsRepoRoot(t *testing.T) {
	root := t.TempDir()
	ui := filepath.Join(root, "UI")
	mustWrite(t, filepath.Join(ui, "package.json"), "{}")
	mustWrite(t, filepath.Join(ui, "TUI", "package.json"), "{}")
	mustWrite(t, filepath.Join(ui, "TUI", "src", "index.tsx"), "")

	got, err := resolveUIWorkspace(root)
	if err != nil {
		t.Fatalf("resolveUIWorkspace() error = %v", err)
	}
	if got != ui {
		t.Fatalf("resolveUIWorkspace() = %q, want %q", got, ui)
	}
}

func mustWrite(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}
