package memory

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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

func TestRenameNotesPrunesDuplicateMachineNamedCopies(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.SaveWithOptions("Readable duplicate memory", "project", TierLong, 0.9, SaveOptions{}); err != nil {
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
	readablePath := filepath.Join(dir, filepath.FromSlash(entry.Path))
	raw, err := os.ReadFile(readablePath)
	if err != nil {
		t.Fatalf("read readable note: %v", err)
	}
	oldRel := filepath.ToSlash(filepath.Join("20_Projects", "20260705-120000-readable-duplicate-memory-"+entry.ID+".md"))
	oldPath := filepath.Join(dir, filepath.FromSlash(oldRel))
	if err := os.MkdirAll(filepath.Dir(oldPath), 0o700); err != nil {
		t.Fatalf("mkdir old path: %v", err)
	}
	if err := os.WriteFile(oldPath, raw, 0o600); err != nil {
		t.Fatalf("write duplicate old note: %v", err)
	}

	dryRun, err := store.RenameNotes(NoteRenameOptions{})
	if err != nil {
		t.Fatalf("RenameNotes dry-run: %v", err)
	}
	if dryRun.WouldPruneDuplicates != 1 || len(dryRun.DuplicatePruneEntries) != 1 {
		t.Fatalf("expected one duplicate prune plan, got %#v", dryRun)
	}
	if dryRun.DuplicatePruneEntries[0].Remove != oldRel {
		t.Fatalf("expected old machine note removed, got %#v", dryRun.DuplicatePruneEntries[0])
	}
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatalf("dry-run should keep old duplicate: %v", err)
	}

	applied, err := store.RenameNotes(NoteRenameOptions{Apply: true})
	if err != nil {
		t.Fatalf("RenameNotes apply: %v", err)
	}
	if applied.PrunedDuplicates != 1 {
		t.Fatalf("expected one duplicate pruned, got %#v", applied)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("expected duplicate old path moved, stat err=%v", err)
	}
	backup := filepath.Join(dir, filepath.FromSlash(duplicateNoteBackupPath(oldRel)))
	if _, err := os.Stat(backup); err != nil {
		t.Fatalf("expected duplicate backup: %v", err)
	}
	if _, err := os.Stat(readablePath); err != nil {
		t.Fatalf("expected readable note preserved: %v", err)
	}
}

func TestRenameNotesKeepsArchivedDirtyNotesInArchive(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.SaveWithOptions("Archived assistant note", "conversation", TierMedium, 0.4, SaveOptions{
		Status: "archived",
	}); err != nil {
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
	oldRel := filepath.ToSlash(filepath.Join("90_Archive", "dirty", "20260609-182120-assistant-x86-64-"+entry.ID+".md"))
	oldPath := filepath.Join(dir, filepath.FromSlash(oldRel))
	if err := os.MkdirAll(filepath.Dir(oldPath), 0o700); err != nil {
		t.Fatalf("mkdir archive path: %v", err)
	}
	if err := os.Rename(filepath.Join(dir, filepath.FromSlash(entry.Path)), oldPath); err != nil {
		t.Fatalf("move to dirty archive path: %v", err)
	}
	entry.Path = oldRel
	store.paths[entry.ID] = oldRel

	report, err := store.RenameNotes(NoteRenameOptions{Apply: true})
	if err != nil {
		t.Fatalf("RenameNotes apply: %v", err)
	}
	wantRel := filepath.ToSlash(filepath.Join("90_Archive", "dirty", "Archived assistant note.md"))
	if report.Renamed != 1 || store.entries[entry.ID].Path != wantRel {
		t.Fatalf("expected archived dirty note renamed in place, report=%#v path=%q", report, store.entries[entry.ID].Path)
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(wantRel))); err != nil {
		t.Fatalf("expected renamed archived note: %v", err)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("expected old archived machine path removed, stat err=%v", err)
	}
}

func TestRenameNotesApplyCreatesMissingConceptNotes(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	entry := &Entry{
		ID:         "mem_1_1",
		Content:    "Legacy memory references [[Legacy Concept]].",
		Category:   "fact",
		Tier:       TierLong,
		Importance: 0.8,
		CreatedAt:  now,
		AccessedAt: now,
		Status:     "active",
		ValidFrom:  now,
		BlockID:    "mem-1-1",
		Links:      []string{"Legacy Concept"},
	}
	store.entries[entry.ID] = entry
	if err := store.persist(); err != nil {
		t.Fatalf("persist legacy entry: %v", err)
	}
	conceptPath := filepath.Join(dir, "70_Concepts", "Legacy Concept.md")
	if _, err := os.Stat(conceptPath); !os.IsNotExist(err) {
		t.Fatalf("legacy setup should not create concept note, stat err=%v", err)
	}

	if _, err := store.RenameNotes(NoteRenameOptions{Apply: true}); err != nil {
		t.Fatalf("RenameNotes apply: %v", err)
	}
	if _, err := os.Stat(conceptPath); err != nil {
		t.Fatalf("expected missing concept note to be created: %v", err)
	}
}

func TestRenameNotesPrunesDuplicateConceptUsingStorePath(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	conceptID := conceptEntryID("Legacy Concept")
	concept := &Entry{
		ID:         conceptID,
		Content:    "Legacy Concept",
		Category:   "concept",
		Tier:       TierLong,
		Importance: 0.85,
		CreatedAt:  now,
		AccessedAt: now,
		Status:     "active",
		ValidFrom:  now,
		BlockID:    blockIDForEntry(conceptID),
		Path:       filepath.ToSlash(filepath.Join("70_Concepts", "Legacy Concept.md")),
	}
	store.entries[concept.ID] = concept
	store.paths[concept.ID] = concept.Path
	if err := store.persist(); err != nil {
		t.Fatalf("persist canonical concept: %v", err)
	}

	oldRel := filepath.ToSlash(filepath.Join("70_Concepts", "Legacy Concept 2.md"))
	oldPath := filepath.Join(dir, filepath.FromSlash(oldRel))
	if err := os.MkdirAll(filepath.Dir(oldPath), 0o700); err != nil {
		t.Fatalf("mkdir old concept path: %v", err)
	}
	oldConcept := *concept
	oldConcept.Path = oldRel
	if err := os.WriteFile(oldPath, []byte(renderMemoryNote(&oldConcept)), 0o600); err != nil {
		t.Fatalf("write old concept duplicate: %v", err)
	}

	report, err := store.RenameNotes(NoteRenameOptions{Apply: true})
	if err != nil {
		t.Fatalf("RenameNotes apply: %v", err)
	}
	if report.PrunedDuplicates != 1 {
		t.Fatalf("expected duplicate concept prune, got %#v", report)
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(concept.Path))); err != nil {
		t.Fatalf("expected canonical concept note: %v", err)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("expected old concept duplicate moved, stat err=%v", err)
	}
}
