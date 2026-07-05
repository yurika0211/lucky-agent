package memory

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRenameNotesDryRunAndApply(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.SaveWithOptions("用户身份是 shiokou", "identity", TierLong, 1, SaveOptions{}); err != nil {
		t.Fatalf("SaveWithOptions: %v", err)
	}

	var entry *Entry
	for _, candidate := range store.entries {
		entry = candidate
		break
	}
	if entry == nil {
		t.Fatal("expected saved entry")
	}
	oldRel := filepath.ToSlash(filepath.Join("10_Profile", "20260530-083947-shiokou-mem_1780101587_23.md"))
	oldPath := filepath.Join(dir, filepath.FromSlash(oldRel))
	if err := os.MkdirAll(filepath.Dir(oldPath), 0o700); err != nil {
		t.Fatalf("mkdir old path: %v", err)
	}
	if err := os.Rename(filepath.Join(dir, filepath.FromSlash(entry.Path)), oldPath); err != nil {
		t.Fatalf("move to old path: %v", err)
	}
	entry.Path = oldRel
	store.paths[entry.ID] = oldRel

	dryRun, err := store.RenameNotes(NoteRenameOptions{})
	if err != nil {
		t.Fatalf("RenameNotes dry-run: %v", err)
	}
	wantRel := filepath.ToSlash(filepath.Join("10_Profile", "用户身份是 shiokou.md"))
	if dryRun.WouldRename != 1 || len(dryRun.Entries) != 1 || dryRun.Entries[0].To != wantRel {
		t.Fatalf("unexpected dry-run report: %#v", dryRun)
	}
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatalf("dry-run should keep old path: %v", err)
	}

	applied, err := store.RenameNotes(NoteRenameOptions{Apply: true})
	if err != nil {
		t.Fatalf("RenameNotes apply: %v", err)
	}
	if applied.Renamed != 1 {
		t.Fatalf("expected one applied rename, got %#v", applied)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("expected old path removed, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(wantRel))); err != nil {
		t.Fatalf("expected new readable path: %v", err)
	}

	reloaded, err := NewStore(dir)
	if err != nil {
		t.Fatalf("reload store: %v", err)
	}
	results := reloaded.Search("shiokou")
	if len(results) != 1 || results[0].Path != wantRel {
		t.Fatalf("expected reloaded memory at readable path, got %#v", results)
	}
}
