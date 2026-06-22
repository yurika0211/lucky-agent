package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// GraphMigrationOptions controls the Obsidian-first graph-memory migration.
type GraphMigrationOptions struct {
	Apply            bool
	ArchiveDirty     bool
	MaxDirtyFindings int
	Now              time.Time
}

// GraphMigrationReport describes the dry-run or applied migration result.
type GraphMigrationReport struct {
	Scanned          int                       `json:"scanned"`
	WouldUpdateLinks int                       `json:"would_update_links"`
	UpdatedLinks     int                       `json:"updated_links,omitempty"`
	WouldArchive     int                       `json:"would_archive"`
	Archived         int                       `json:"archived,omitempty"`
	DirtyFindings    []HygieneIssue            `json:"dirty_findings,omitempty"`
	Entries          []GraphMigrationEntryPlan `json:"entries,omitempty"`
	Apply            bool                      `json:"apply"`
}

// GraphMigrationEntryPlan is the per-entry graph enrichment plan.
type GraphMigrationEntryPlan struct {
	ID          string   `json:"id"`
	Path        string   `json:"path,omitempty"`
	Category    string   `json:"category"`
	Tier        string   `json:"tier"`
	AddLinks    []string `json:"add_links,omitempty"`
	AddAliases  []string `json:"add_aliases,omitempty"`
	AddTags     []string `json:"add_tags,omitempty"`
	Archive     bool     `json:"archive,omitempty"`
	ArchivePath string   `json:"archive_path,omitempty"`
	Reason      string   `json:"reason,omitempty"`
	Preview     string   `json:"preview,omitempty"`
}

// MigrateGraphMemory enriches existing memories with inferred Obsidian concept
// links and optionally archives high-risk dirty notes. The default call is a
// dry-run; pass Apply=true to mutate files.
func (s *Store) MigrateGraphMemory(opts GraphMigrationOptions) (GraphMigrationReport, error) {
	report := GraphMigrationReport{Apply: opts.Apply}
	if s == nil {
		return report, nil
	}
	if opts.MaxDirtyFindings <= 0 {
		opts.MaxDirtyFindings = 200
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	dirty := limitHygieneIssues(filterHygieneSeverity(s.hygieneIssuesLocked(HygieneOptions{
		IncludeInactive: true,
		MinSeverity:     "high",
		MaxFindings:     opts.MaxDirtyFindings,
	}, now), "high"), opts.MaxDirtyFindings)
	report.DirtyFindings = dirty
	dirtyByID := make(map[string]HygieneIssue, len(dirty))
	for _, issue := range dirty {
		dirtyByID[issue.ID] = issue
	}

	ids := make([]string, 0, len(s.entries))
	for id := range s.entries {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var changed bool
	var conceptLinks []string
	for _, id := range ids {
		e := s.entries[id]
		if e == nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(e.Category), "concept") {
			continue
		}
		report.Scanned++
		plan := GraphMigrationEntryPlan{
			ID:       e.ID,
			Path:     e.Path,
			Category: e.Category,
			Tier:     e.Tier.String(),
			Preview:  truncateRunes(strings.ReplaceAll(strings.TrimSpace(e.Content), "\n", " "), 120),
		}
		if issue, ok := dirtyByID[e.ID]; ok && opts.ArchiveDirty {
			plan.Archive = true
			plan.Reason = issue.Reason
			plan.ArchivePath = archivePathForDirtyMemory(e)
			report.WouldArchive++
			if opts.Apply {
				if err := s.archiveEntryLocked(e, plan.ArchivePath, issue.Reason); err != nil {
					return report, err
				}
				report.Archived++
				changed = true
			}
			report.Entries = append(report.Entries, plan)
			continue
		}

		links, aliases, tags := inferConceptMetadata(e.Content, e.Category)
		plan.AddLinks = missingNormalizedLinks(e.Links, links)
		plan.AddAliases = missingStrings(e.Aliases, aliases)
		plan.AddTags = missingStrings(e.Tags, tags)
		if len(plan.AddLinks) == 0 && len(plan.AddAliases) == 0 && len(plan.AddTags) == 0 {
			continue
		}
		report.WouldUpdateLinks++
		if opts.Apply {
			conceptLinks = append(conceptLinks, plan.AddLinks...)
			e.Links = normalizeLinks(append(e.Links, plan.AddLinks...))
			e.Aliases = dedupSlice(append(e.Aliases, plan.AddAliases...))
			e.Tags = mergeTags(e.Tags, plan.AddTags)
			report.UpdatedLinks++
			changed = true
		}
		report.Entries = append(report.Entries, plan)
	}

	if opts.Apply && changed {
		s.ensureConceptEntriesLocked(conceptLinks)
		if err := s.persist(); err != nil {
			return report, err
		}
	}
	return report, nil
}

func archivePathForDirtyMemory(e *Entry) string {
	name := strings.TrimSpace(filepath.Base(filepath.FromSlash(e.Path)))
	if name == "" || name == "." {
		name = strings.ReplaceAll(e.ID, "_", "-") + ".md"
	}
	return filepath.ToSlash(filepath.Join("90_Archive", "dirty", name))
}

func (s *Store) archiveEntryLocked(e *Entry, relArchivePath, reason string) error {
	if e == nil {
		return nil
	}
	oldRel := e.Path
	if oldRel != "" {
		oldPath := filepath.Join(s.dir, filepath.FromSlash(oldRel))
		newPath := filepath.Join(s.dir, filepath.FromSlash(relArchivePath))
		if err := os.MkdirAll(filepath.Dir(newPath), 0o700); err != nil {
			return fmt.Errorf("create archive dir: %w", err)
		}
		if err := os.Rename(oldPath, newPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("archive memory %s: %w", e.ID, err)
		}
	}
	e.Path = relArchivePath
	e.Status = "archived"
	e.Tags = mergeTags(e.Tags, []string{"hygiene", "dirty", "hygiene-" + reason})
	if e.Confidence == 0 || e.Confidence > 0.25 {
		e.Confidence = 0.25
	}
	s.paths[e.ID] = relArchivePath
	return nil
}

func missingNormalizedLinks(existing, candidates []string) []string {
	seen := make(map[string]bool, len(existing))
	for _, value := range existing {
		key := graphKey(value)
		if key != "" {
			seen[key] = true
		}
	}
	var out []string
	for _, value := range normalizeLinks(candidates) {
		key := graphKey(value)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, value)
	}
	return out
}

func missingStrings(existing, candidates []string) []string {
	seen := make(map[string]bool, len(existing))
	for _, value := range existing {
		key := strings.ToLower(strings.TrimSpace(value))
		if key != "" {
			seen[key] = true
		}
	}
	var out []string
	for _, value := range dedupSlice(candidates) {
		key := strings.ToLower(strings.TrimSpace(value))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, value)
	}
	return out
}
