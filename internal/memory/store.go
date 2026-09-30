// Vault load, persist, directory layout, and note deletion.
package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func (s *Store) load() error {
	if err := s.ensureVaultDirs(); err != nil {
		return err
	}

	maxID := int64(0)
	err := filepath.Walk(s.dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		if info.IsDir() {
			if info.Name() == ".lh-index" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.ToLower(filepath.Ext(path)) != ".md" {
			return nil
		}

		entry, ok, err := parseMemoryNote(path, s.dir)
		if err != nil {
			return fmt.Errorf("parse note %s: %w", path, err)
		}
		if !ok {
			return nil
		}
		s.entries[entry.ID] = entry
		s.paths[entry.ID] = entry.Path

		var idNum int64
		fmt.Sscanf(entry.ID, "mem_%d_%d", new(int64), &idNum)
		if idNum > maxID {
			maxID = idNum
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.nextID = maxID
	s.rebuildDerivedLocked()
	return nil
}

// persist writes every note. Callers that already know the changed IDs should
// use persistEntriesLocked so an unchanged vault is not rewritten.
func (s *Store) persist() error {
	if err := s.ensureVaultDirs(); err != nil {
		return err
	}
	ids := sortedMemoryEntryIDs(s.entries)
	usedPaths := make(map[string]string, len(ids))
	for _, id := range ids {
		e := s.entries[id]
		if e == nil {
			continue
		}
		if rel := strings.TrimSpace(e.Path); rel != "" {
			usedPaths[filepath.ToSlash(rel)] = id
		}
	}
	for _, id := range ids {
		e := s.entries[id]
		if e == nil {
			continue
		}
		normalizeEntryForNote(e)
		rel := filepath.ToSlash(strings.TrimSpace(e.Path))
		if rel == "" {
			rel = s.uniqueNotePathForEntry(e, usedPaths, "")
			usedPaths[rel] = e.ID
		}
		e.Path = rel
		s.paths[e.ID] = rel
		path := filepath.Join(s.dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return fmt.Errorf("create note dir: %w", err)
		}
		if err := os.WriteFile(path, []byte(renderMemoryNote(e)), 0600); err != nil {
			return fmt.Errorf("write memory note %s: %w", path, err)
		}
	}
	s.rebuildDerivedLocked()
	return nil
}

func (s *Store) persistEntriesLocked(ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	if err := s.ensureVaultDirs(); err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		e := s.entries[id]
		if e == nil {
			continue
		}
		if err := s.writeEntryLocked(e); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) writeEntryLocked(e *Entry) error {
	if e == nil {
		return nil
	}
	normalizeEntryForNote(e)
	rel := filepath.ToSlash(strings.TrimSpace(e.Path))
	if rel == "" {
		rel = s.uniqueNotePathForEntry(e, pathOwners(s.paths), "")
	}
	e.Path = rel
	s.paths[e.ID] = rel
	path := filepath.Join(s.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create note dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(renderMemoryNote(e)), 0600); err != nil {
		return fmt.Errorf("write memory note %s: %w", path, err)
	}
	return nil
}

func pathOwners(paths map[string]string) map[string]string {
	used := make(map[string]string, len(paths))
	for id, rel := range paths {
		rel = filepath.ToSlash(strings.TrimSpace(rel))
		if rel != "" {
			used[rel] = id
		}
	}
	return used
}

func sortedMemoryEntryIDs(entries map[string]*Entry) []string {
	ids := make([]string, 0, len(entries))
	for id := range entries {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (s *Store) ensureVaultDirs() error {
	dirs := []string{
		"00_Index",
		"10_Profile",
		"20_Projects",
		"30_Sessions",
		"40_Decisions",
		"50_Facts",
		"60_Rules",
		"70_Concepts",
		"70_Trajectories",
		"90_Archive",
		".lh-index",
	}
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return fmt.Errorf("create memory vault: %w", err)
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(filepath.Join(s.dir, dir), 0700); err != nil {
			return fmt.Errorf("create memory vault dir %s: %w", dir, err)
		}
	}
	if err := s.ensureVaultReadme(); err != nil {
		return err
	}
	return nil
}

func (s *Store) ensureVaultReadme() error {
	path := filepath.Join(s.dir, "00_Index", "Aestus Memory Vault.md")
	if st, err := os.Stat(path); err == nil && !st.IsDir() {
		return nil
	}
	// Keep an existing LuckyAgent index note intact when opening a vault
	// created by the source project. New vaults receive an Aestus-branded index.
	legacyPath := filepath.Join(s.dir, "00_Index", "LuckyAgent Memory Vault.md")
	if st, err := os.Stat(legacyPath); err == nil && !st.IsDir() {
		return nil
	}
	body := strings.TrimSpace(`# Aestus Memory Vault

This directory is the Aestus durable memory source of truth.

- Memory notes are Obsidian-compatible Markdown files under the category folders.
- Authoritative memory notes use YAML frontmatter with type: memory.
- Wikilinks, tags, aliases, temporal state fields, typed route policies, and block IDs are part of the memory graph.
- The RAG SQLite database is for indexed documents, not durable user memory.
- An external Obsidian app vault, .obsidian directory, or OBSIDIAN_VAULT_PATH is not required for Aestus memory.
`) + "\n"
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		return fmt.Errorf("write memory vault readme: %w", err)
	}
	return nil
}

func (s *Store) removeEntryFileLocked(id string) {
	if e := s.entries[id]; e != nil {
		s.unindexEntryLocked(e)
	}
	rel := s.paths[id]
	if rel == "" {
		if e, ok := s.entries[id]; ok {
			rel = e.Path
		}
	}
	if rel != "" {
		_ = os.Remove(filepath.Join(s.dir, filepath.FromSlash(rel)))
		delete(s.paths, id)
	}
}
