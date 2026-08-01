package memory

import (
	"strings"
	"time"
)

const memoryTracePreviewRunes = 120

// SearchTrace is UI/telemetry-only evidence for a memory search. It is kept
// separate from the model-visible recall output so trace detail does not bloat
// the conversation context.
type SearchTrace struct {
	Query      string              `json:"query"`
	Mode       string              `json:"mode,omitempty"`
	Source     string              `json:"source,omitempty"`
	Limit      int                 `json:"limit,omitempty"`
	GraphDepth int                 `json:"graph_depth"`
	Filters    SearchTraceFilters  `json:"filters,omitempty"`
	Seeds      []SearchTraceNode   `json:"seeds,omitempty"`
	Hops       []SearchTraceHop    `json:"hops,omitempty"`
	Results    []SearchTraceResult `json:"results,omitempty"`
	Temporal   []string            `json:"temporal_notes,omitempty"`
	Warnings   []string            `json:"warnings,omitempty"`
	DurationMS int64               `json:"duration_ms,omitempty"`
}

type SearchTraceFilters struct {
	Category        string `json:"category,omitempty"`
	Tier            string `json:"tier,omitempty"`
	IncludeInactive bool   `json:"include_inactive,omitempty"`
	IncludeExpired  bool   `json:"include_expired,omitempty"`
	AsOf            string `json:"as_of,omitempty"`
}

type SearchTraceNode struct {
	ID             string  `json:"id"`
	Ref            string  `json:"ref,omitempty"`
	Category       string  `json:"category,omitempty"`
	Tier           string  `json:"tier,omitempty"`
	Score          float64 `json:"score,omitempty"`
	DirectScore    float64 `json:"direct_score,omitempty"`
	GraphScore     float64 `json:"graph_score,omitempty"`
	ContentPreview string  `json:"content_preview,omitempty"`
}

type SearchTraceHop struct {
	Depth       int     `json:"depth"`
	FromID      string  `json:"from_id"`
	FromRef     string  `json:"from_ref,omitempty"`
	ToID        string  `json:"to_id"`
	ToRef       string  `json:"to_ref,omitempty"`
	Via         string  `json:"via,omitempty"`
	Kind        string  `json:"kind,omitempty"`
	Weight      float64 `json:"weight,omitempty"`
	Boost       float64 `json:"boost,omitempty"`
	SourceScore float64 `json:"source_score,omitempty"`
	TargetScore float64 `json:"target_score,omitempty"`
}

type SearchTraceResult struct {
	SearchTraceNode
	Rank int `json:"rank"`
}

// BuildSearchTrace converts search results into a bounded, user-interface safe
// trace. It does not affect ranking or access stats.
func (s *Store) BuildSearchTrace(query, mode, source string, opts SearchOptions, results []SearchResult, duration time.Duration) SearchTrace {
	trace := SearchTrace{
		Query:      strings.TrimSpace(query),
		Mode:       strings.TrimSpace(mode),
		Source:     strings.TrimSpace(source),
		Limit:      opts.Limit,
		GraphDepth: opts.GraphDepth,
		Filters: SearchTraceFilters{
			Category:        strings.TrimSpace(opts.Category),
			IncludeInactive: opts.IncludeInactive,
			IncludeExpired:  opts.IncludeExpired,
		},
		DurationMS: duration.Milliseconds(),
	}
	if opts.Tier != nil {
		trace.Filters.Tier = opts.Tier.String()
	}
	if !opts.AsOf.IsZero() {
		trace.Filters.AsOf = opts.AsOf.Format(time.RFC3339)
	}
	if len(results) == 0 {
		return trace
	}

	scoreByID := make(map[string]SearchResult, len(results))
	entries := make([]Entry, 0, len(results))
	for i, result := range results {
		entry := result.Entry
		scoreByID[entry.ID] = result
		node := searchTraceNodeForEntry(entry, result)
		trace.Results = append(trace.Results, SearchTraceResult{
			SearchTraceNode: node,
			Rank:            i + 1,
		})
		if result.DirectScore > 0 {
			trace.Seeds = append(trace.Seeds, node)
		}
		entries = append(entries, entry)
	}

	refByID := make(map[string]string, len(results)*2)
	if s != nil {
		s.mu.RLock()
		for _, result := range results {
			for _, path := range result.Paths {
				if path.FromID != "" {
					if from := s.entries[path.FromID]; from != nil {
						refByID[path.FromID] = refForEntry(from)
					}
				}
				if path.ToID != "" {
					if to := s.entries[path.ToID]; to != nil {
						refByID[path.ToID] = refForEntry(to)
					}
				}
			}
		}
		s.mu.RUnlock()
	}
	for _, result := range results {
		for _, path := range result.Paths {
			depth := 1
			if path.Depth > 0 {
				depth = path.Depth
			}
			sourceScore := scoreByID[path.FromID].Score
			trace.Hops = append(trace.Hops, SearchTraceHop{
				Depth:       depth,
				FromID:      path.FromID,
				FromRef:     refByID[path.FromID],
				ToID:        path.ToID,
				ToRef:       firstNonEmpty(refByID[path.ToID], refForEntry(&result.Entry)),
				Via:         strings.TrimSpace(path.Via),
				Kind:        strings.TrimSpace(path.Kind),
				Weight:      path.Weight,
				Boost:       path.Boost,
				SourceScore: sourceScore,
				TargetScore: result.Score,
			})
		}
	}
	if s != nil {
		resolution := s.ResolveTemporal(query, entries)
		trace.Temporal = append(trace.Temporal, resolution.Notes...)
	}
	return trace
}

func searchTraceNodeForEntry(entry Entry, result SearchResult) SearchTraceNode {
	return SearchTraceNode{
		ID:             entry.ID,
		Ref:            refForEntry(&entry),
		Category:       entry.Category,
		Tier:           entry.Tier.String(),
		Score:          result.Score,
		DirectScore:    result.DirectScore,
		GraphScore:     result.GraphScore,
		ContentPreview: truncateRunes(strings.TrimSpace(entry.Content), memoryTracePreviewRunes),
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
