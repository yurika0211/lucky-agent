// In-memory wikilink graph rebuilt from notes. Notes stay the source of truth.
package memory

import (
	"path/filepath"
	"strings"
)

func newGraphIndex() *GraphIndex {
	return &GraphIndex{
		Forward:   make(map[string][]string),
		Backlinks: make(map[string][]string),
		Tags:      make(map[string][]string),
		Names:     make(map[string][]string),
	}
}

func graphKey(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimSuffix(raw, ".md")
	raw = strings.ReplaceAll(raw, "\\", "/")
	raw = strings.Trim(raw, "/")
	return strings.ToLower(raw)
}

func graphKeysForLink(link string) []string {
	link = strings.TrimSpace(link)
	if link == "" {
		return nil
	}
	keys := []string{graphKey(link)}
	base := strings.TrimSuffix(filepath.Base(strings.ReplaceAll(link, "\\", "/")), ".md")
	if base != "" {
		keys = append(keys, graphKey(base))
	}
	return dedupSlice(keys)
}

func graphAliasesForEntry(e *Entry) []string {
	if e == nil {
		return nil
	}
	aliases := []string{e.ID, e.BlockID}
	aliases = append(aliases, e.Aliases...)
	if e.Path != "" {
		pathNoExt := strings.TrimSuffix(filepath.ToSlash(e.Path), ".md")
		aliases = append(aliases, pathNoExt, filepath.Base(pathNoExt))
	}
	return dedupSlice(aliases)
}

func (s *Store) rebuildGraphLocked() {
	graph := newGraphIndex()
	s.graph = graph
	for _, entry := range s.entries {
		s.indexGraphEntryLocked(entry)
	}
}

func (s *Store) indexGraphEntryLocked(entry *Entry) {
	if s == nil || entry == nil || entry.ID == "" {
		return
	}
	if s.graph == nil {
		s.graph = newGraphIndex()
	}
	links := normalizeLinks(append(entry.Links, extractWikiLinks(entry.Content)...))
	s.graph.Forward[entry.ID] = links
	for _, link := range links {
		for _, key := range graphKeysForLink(link) {
			s.graph.Backlinks[key] = append(s.graph.Backlinks[key], entry.ID)
		}
	}
	for _, alias := range graphAliasesForEntry(entry) {
		key := graphKey(alias)
		if key != "" {
			s.graph.Names[key] = append(s.graph.Names[key], entry.ID)
		}
	}
	for _, tag := range entry.Tags {
		tag = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(tag)), "#")
		if tag == "" {
			continue
		}
		s.graph.Tags[tag] = append(s.graph.Tags[tag], entry.ID)
	}
}

func (s *Store) unindexGraphEntryLocked(entry *Entry) {
	if s == nil || s.graph == nil || entry == nil || entry.ID == "" {
		return
	}
	links := s.graph.Forward[entry.ID]
	delete(s.graph.Forward, entry.ID)
	for _, link := range links {
		for _, key := range graphKeysForLink(link) {
			s.graph.Backlinks[key] = removeGraphID(s.graph.Backlinks[key], entry.ID)
			if len(s.graph.Backlinks[key]) == 0 {
				delete(s.graph.Backlinks, key)
			}
		}
	}
	for _, alias := range graphAliasesForEntry(entry) {
		key := graphKey(alias)
		if key == "" {
			continue
		}
		s.graph.Names[key] = removeGraphID(s.graph.Names[key], entry.ID)
		if len(s.graph.Names[key]) == 0 {
			delete(s.graph.Names, key)
		}
	}
	for _, tag := range entry.Tags {
		tag = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(tag)), "#")
		if tag == "" {
			continue
		}
		s.graph.Tags[tag] = removeGraphID(s.graph.Tags[tag], entry.ID)
		if len(s.graph.Tags[tag]) == 0 {
			delete(s.graph.Tags, tag)
		}
	}
}

func removeGraphID(ids []string, id string) []string {
	out := ids[:0]
	for _, existing := range ids {
		if existing != id {
			out = append(out, existing)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
