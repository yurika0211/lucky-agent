package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrateGraphMemoryDryRunDoesNotMutate(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.SaveWithOptions("QQ official should summarize reasoning_content in trace replies.", "rule", TierLong, 0.9, SaveOptions{}); err != nil {
		t.Fatalf("SaveWithOptions: %v", err)
	}

	var id string
	for entryID, entry := range store.entries {
		id = entryID
		entry.Links = nil
		entry.Aliases = nil
		entry.Tags = nil
	}

	report, err := store.MigrateGraphMemory(GraphMigrationOptions{})
	if err != nil {
		t.Fatalf("MigrateGraphMemory: %v", err)
	}
	if report.Scanned != 1 || report.WouldUpdateLinks != 1 || report.UpdatedLinks != 0 {
		t.Fatalf("unexpected dry-run report: %#v", report)
	}
	if got := store.entries[id].Links; len(got) != 0 {
		t.Fatalf("dry-run should not mutate links, got %#v", got)
	}
}

func TestMigrateGraphMemoryApplyEnrichesAndArchivesDirty(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.SaveWithOptions("QQ official should summarize reasoning_content in trace replies.", "rule", TierLong, 0.9, SaveOptions{}); err != nil {
		t.Fatalf("save clean: %v", err)
	}
	if err := store.SaveWithOptions("api_key sk-1234567890abcdef should never be stored in memory.", "conversation", TierMedium, 0.6, SaveOptions{}); err != nil {
		t.Fatalf("save dirty: %v", err)
	}

	for _, entry := range store.entries {
		if strings.Contains(entry.Content, "QQ official") {
			entry.Links = nil
			entry.Aliases = nil
			entry.Tags = nil
			continue
		}
	}
	if err := store.persist(); err != nil {
		t.Fatalf("persist setup: %v", err)
	}

	report, err := store.MigrateGraphMemory(GraphMigrationOptions{Apply: true, ArchiveDirty: true})
	if err != nil {
		t.Fatalf("MigrateGraphMemory apply: %v", err)
	}
	if report.UpdatedLinks != 1 {
		t.Fatalf("expected one enriched clean memory, got %#v", report)
	}
	if report.Archived != 1 {
		t.Fatalf("expected one archived dirty memory, got %#v", report)
	}

	var foundClean, foundArchived bool
	for _, entry := range store.entries {
		if strings.Contains(entry.Content, "QQ official") {
			foundClean = true
			for _, want := range []string{"QQ Official", "Reasoning Content", "Gateway Trace"} {
				if !stringSliceContains(entry.Links, want) {
					t.Fatalf("expected clean entry link %q, got %#v", want, entry.Links)
				}
			}
		}
		if strings.Contains(entry.Content, "api_key") {
			foundArchived = true
			if entry.Status != "archived" {
				t.Fatalf("expected dirty entry archived, got %#v", entry.Status)
			}
			if !strings.HasPrefix(entry.Path, "90_Archive/dirty/") {
				t.Fatalf("expected dirty archive path, got %q", entry.Path)
			}
			if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(entry.Path))); err != nil {
				t.Fatalf("expected archived file to exist: %v", err)
			}
		}
	}
	if !foundClean || !foundArchived {
		t.Fatalf("expected clean and archived entries, clean=%v archived=%v", foundClean, foundArchived)
	}
	if _, ok := store.entries["concept_qq_official"]; !ok {
		t.Fatalf("expected QQ Official concept entry")
	}
	if _, err := os.Stat(filepath.Join(dir, "70_Concepts", "qq-official.md")); err != nil {
		t.Fatalf("expected QQ Official concept note: %v", err)
	}
}
