package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIncrementalSaveLeavesUnchangedNotesUntouched(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := s.Save("project uses Go", "context"); err != nil {
		t.Fatalf("Save first: %v", err)
	}
	first := s.Search("Go")
	if len(first) != 1 {
		t.Fatalf("expected the first note, got %d", len(first))
	}
	firstPath := filepath.Join(dir, filepath.FromSlash(first[0].Path))
	before, err := os.Stat(firstPath)
	if err != nil {
		t.Fatalf("stat first note: %v", err)
	}
	time.Sleep(20 * time.Millisecond)

	if err := s.Save("user prefers Chinese", "preference"); err != nil {
		t.Fatalf("Save second: %v", err)
	}
	after, err := os.Stat(firstPath)
	if err != nil {
		t.Fatalf("stat first note after second save: %v", err)
	}
	if !after.ModTime().Equal(before.ModTime()) || after.Size() != before.Size() {
		t.Fatalf("second save rewrote unchanged note %s", first[0].Path)
	}
	if got := s.Search("Chinese"); len(got) != 1 {
		t.Fatalf("expected the new note to be searchable, got %d", len(got))
	}
}

func TestLexicalCandidatesMatchFullScan(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	notes := []struct {
		content  string
		category string
		tags     []string
	}{
		{"用户偏好用中文回答", "preference", []string{"language"}},
		{"项目部署前要跑测试", "project", nil},
		{"女儿对花粉过敏", "health", []string{"family"}},
		{"Outdoor walks include the park", "plan", nil},
		{"LuckyAgent memory vault uses wikilinks", "project", nil},
		{"上海周末天气需要看空气质量", "plan", nil},
	}
	for i := 0; i < 8; i++ {
		for _, note := range notes {
			content := note.content
			if i > 0 {
				content = content + " " + strings.Repeat("背景", i)
			}
			if err := s.SaveWithTierAndTags(content, note.category, TierLong, 0.7, note.tags); err != nil {
				t.Fatalf("Save: %v", err)
			}
		}
	}
	if len(s.entries) <= 32 {
		t.Fatalf("expected the candidate index to be active, got %d entries", len(s.entries))
	}

	queries := []string{"中文", "部署测试", "花粉", "park", "记忆库", "空气", "Go", "不存在的词xyz"}
	for _, query := range queries {
		queryLower := strings.ToLower(query)
		terms := extractQueryTerms(queryLower)
		got := map[string]struct{}{}
		for id := range s.activationCandidatesLocked(queryLower, terms) {
			got[id] = struct{}{}
		}
		for id, entry := range s.entries {
			if entry == nil || isConceptEntry(entry) {
				continue
			}
			if matchActivation(entry, queryLower, terms).MatchScore() <= 0 {
				continue
			}
			if _, ok := got[id]; !ok {
				t.Errorf("query %q missed scoring note %s (%s)", query, id, entry.Content)
			}
		}
	}
}

func TestDeleteRemovesNoteWithoutRewritingVault(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := s.Save("keep this fact", "fact"); err != nil {
		t.Fatalf("Save keep: %v", err)
	}
	if err := s.Save("remove this fact", "fact"); err != nil {
		t.Fatalf("Save remove: %v", err)
	}
	keep := s.Search("keep")
	remove := s.Search("remove")
	if len(keep) != 1 || len(remove) != 1 {
		t.Fatalf("setup search keep=%d remove=%d", len(keep), len(remove))
	}
	keepPath := filepath.Join(dir, filepath.FromSlash(keep[0].Path))
	removePath := filepath.Join(dir, filepath.FromSlash(remove[0].Path))
	before, err := os.Stat(keepPath)
	if err != nil {
		t.Fatalf("stat keep: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if err := s.Delete(remove[0].ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(removePath); !os.IsNotExist(err) {
		t.Fatalf("deleted note still exists: %v", err)
	}
	after, err := os.Stat(keepPath)
	if err != nil {
		t.Fatalf("stat keep after delete: %v", err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("delete rewrote an unchanged note")
	}
	if got := s.Search("remove"); len(got) != 0 {
		t.Fatalf("deleted note still searchable: %+v", got)
	}
}
