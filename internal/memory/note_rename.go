package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// NoteRenameOptions controls human-readable Obsidian note renaming.
type NoteRenameOptions struct {
	Apply bool
	Limit int
}

// NoteRenameReport describes planned or applied memory note renames.
type NoteRenameReport struct {
	Scanned     int              `json:"scanned"`
	WouldRename int              `json:"would_rename"`
	Renamed     int              `json:"renamed,omitempty"`
	Entries     []NoteRenamePlan `json:"entries,omitempty"`
	Apply       bool             `json:"apply"`
}

// NoteRenamePlan is one memory note path change.
type NoteRenamePlan struct {
	ID       string `json:"id"`
	From     string `json:"from"`
	To       string `json:"to"`
	Category string `json:"category,omitempty"`
	Tier     string `json:"tier,omitempty"`
	Preview  string `json:"preview,omitempty"`
}

// RenameNotes plans or applies human-readable Markdown filenames while keeping
// memory IDs in frontmatter and block IDs stable.
func (s *Store) RenameNotes(opts NoteRenameOptions) (NoteRenameReport, error) {
	report := NoteRenameReport{Apply: opts.Apply}
	if s == nil {
		return report, nil
	}
	if opts.Limit <= 0 {
		opts.Limit = 1000
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	ids := sortedMemoryEntryIDs(s.entries)
	used := make(map[string]string, len(ids))
	for _, id := range ids {
		entry := s.entries[id]
		if entry == nil {
			continue
		}
		if rel := filepath.ToSlash(strings.TrimSpace(entry.Path)); rel != "" {
			used[rel] = id
		}
	}

	var plans []NoteRenamePlan
	for _, id := range ids {
		if len(plans) >= opts.Limit {
			break
		}
		entry := s.entries[id]
		if entry == nil || renameNotesSkipEntry(entry) {
			continue
		}
		report.Scanned++
		oldRel := filepath.ToSlash(strings.TrimSpace(entry.Path))
		if oldRel == "" {
			continue
		}
		delete(used, oldRel)
		newRel := s.uniqueNotePathForEntry(entry, used, oldRel)
		if newRel == oldRel {
			used[oldRel] = id
			continue
		}
		used[newRel] = id
		plan := NoteRenamePlan{
			ID:       entry.ID,
			From:     oldRel,
			To:       newRel,
			Category: entry.Category,
			Tier:     entry.Tier.String(),
			Preview:  truncateRunes(strings.ReplaceAll(strings.TrimSpace(entry.Content), "\n", " "), 120),
		}
		plans = append(plans, plan)
	}
	report.WouldRename = len(plans)
	report.Entries = plans
	if !opts.Apply {
		return report, nil
	}
	if len(plans) == 0 {
		return report, s.persist()
	}

	for _, plan := range plans {
		oldPath := filepath.Join(s.dir, filepath.FromSlash(plan.From))
		newPath := filepath.Join(s.dir, filepath.FromSlash(plan.To))
		if err := os.MkdirAll(filepath.Dir(newPath), 0o700); err != nil {
			return report, fmt.Errorf("create rename target dir: %w", err)
		}
		if err := os.Rename(oldPath, newPath); err != nil && !os.IsNotExist(err) {
			return report, fmt.Errorf("rename memory note %s to %s: %w", plan.From, plan.To, err)
		}
		if entry := s.entries[plan.ID]; entry != nil {
			entry.Path = plan.To
			s.paths[plan.ID] = plan.To
		}
		report.Renamed++
	}
	if err := s.persist(); err != nil {
		return report, err
	}
	return report, nil
}

func renameNotesSkipEntry(entry *Entry) bool {
	if entry == nil {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(entry.Status), "archived") {
		return true
	}
	rel := filepath.ToSlash(strings.TrimSpace(entry.Path))
	return strings.HasPrefix(rel, "90_Archive/dirty/")
}
