package memory

import (
	"sort"
	"strings"
	"time"
)

// ActivationOptions controls memory activation. The zero value is useful for
// diagnostic callers; Search-like behavior should use DefaultActivationOptions.
type ActivationOptions struct {
	Limit        int
	IncludeGraph bool
	// MaxGraphDepth is currently capped to shallow one-hop graph spread. The
	// field is kept in options so later path expansion can grow without API churn.
	MaxGraphDepth     int
	MaxGraphBoost     float64
	MaxGraphSeeds     int
	UpdateAccessStats bool
	Explain           bool
}

// DefaultActivationOptions returns the behavior used by normal memory search.
func DefaultActivationOptions() ActivationOptions {
	return ActivationOptions{
		IncludeGraph:      true,
		MaxGraphDepth:     1,
		MaxGraphBoost:     0.45,
		MaxGraphSeeds:     12,
		UpdateAccessStats: true,
	}
}

// RouteActivationOptions bounds recall for deterministic routing decisions.
// Route is a read path, so it avoids access-stat persistence and keeps graph
// spread focused on the strongest directly activated seeds.
func RouteActivationOptions() ActivationOptions {
	return ActivationOptions{
		Limit:             12,
		IncludeGraph:      true,
		MaxGraphDepth:     1,
		MaxGraphBoost:     0.45,
		MaxGraphSeeds:     6,
		UpdateAccessStats: false,
	}
}

// ActivationScore explains why a memory entry was activated for a query.
type ActivationScore struct {
	EntryID     string
	Entry       Entry
	Score       float64
	Components  ActivationComponents
	Paths       []ActivationPath
	DirectScore float64
}

// ActivationComponents is a decomposed score vector for memory activation.
type ActivationComponents struct {
	Lexical    float64
	Category   float64
	Tags       float64
	Aliases    float64
	Links      float64
	Importance float64
	Tier       float64
	Recency    float64
	Access     float64
	GraphBoost float64
}

// MatchScore returns the direct query-match portion before entry modifiers.
func (c ActivationComponents) MatchScore() float64 {
	return c.Lexical + c.Category + c.Tags + c.Aliases + c.Links
}

// ActivationPath records graph spread evidence for an activated memory.
type ActivationPath struct {
	FromID string
	ToID   string
	Via    string
	Kind   string
	Weight float64
}

func normalizeActivationOptions(opts ActivationOptions) ActivationOptions {
	if opts.MaxGraphDepth <= 0 {
		opts.MaxGraphDepth = 1
	}
	if opts.MaxGraphBoost <= 0 {
		opts.MaxGraphBoost = 0.45
	}
	if opts.MaxGraphSeeds <= 0 {
		opts.MaxGraphSeeds = 12
	}
	return opts
}

// Activate computes query activation over active memories. It is the shared
// scoring core for Search, SearchParallel, and future explain/debug paths.
func (s *Store) Activate(query string, opts ActivationOptions) []ActivationScore {
	if s == nil {
		return nil
	}
	opts = normalizeActivationOptions(opts)
	query = strings.TrimSpace(query)
	queryLower := strings.ToLower(query)
	queryTerms := extractQueryTerms(queryLower)
	now := time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()

	activated := s.activateLocked(queryLower, queryTerms, now, opts)
	if len(activated) == 0 {
		return []ActivationScore{}
	}

	sortActivationScores(activated)

	if opts.UpdateAccessStats {
		for _, score := range activated {
			entry := s.entries[score.EntryID]
			if entry == nil || !entryIsActive(entry, now) {
				continue
			}
			entry.AccessCount++
			entry.AccessedAt = now
		}
		_ = s.persist()
	}

	return activated
}

func activationScoresToEntries(scores []ActivationScore) []Entry {
	if len(scores) == 0 {
		return []Entry{}
	}
	entries := make([]Entry, len(scores))
	for i, score := range scores {
		entries[i] = score.Entry
	}
	return entries
}

func (s *Store) activateLocked(queryLower string, queryTerms []string, now time.Time, opts ActivationOptions) []ActivationScore {
	scores := make(map[string]*ActivationScore)
	collectGraphSeeds := opts.IncludeGraph && s.graph != nil && opts.MaxGraphDepth > 0
	var graphSeeds []string
	if collectGraphSeeds {
		graphSeeds = make([]string, 0, min(opts.MaxGraphSeeds, 16))
	}

	for id, e := range s.entries {
		if !entryIsActive(e, now) {
			continue
		}
		components := matchActivation(e, queryLower, queryTerms)
		matchScore := components.MatchScore()
		if matchScore <= 0 {
			continue
		}
		components.Importance = e.Importance
		components.Tier = tierActivationMultiplier(e.Tier)
		components.Recency = e.recencyFactor(now)
		components.Access = e.accessBoost()
		total := matchScore * e.Weight(now) * components.Tier
		scores[id] = &ActivationScore{
			EntryID:     id,
			Entry:       *e,
			Score:       total,
			Components:  components,
			DirectScore: total,
		}
		if collectGraphSeeds {
			graphSeeds = insertActivationSeed(graphSeeds, id, scores, opts.MaxGraphSeeds)
		}
	}

	if len(scores) == 0 {
		return nil
	}
	if collectGraphSeeds {
		s.spreadActivationGraphLocked(scores, graphSeeds, now, opts)
	}

	outLimit := len(scores)
	if opts.Limit > 0 && opts.Limit < outLimit {
		outLimit = opts.Limit
	}
	out := make([]ActivationScore, 0, outLimit)
	for id, score := range scores {
		entry := s.entries[id]
		if !entryIsActive(entry, now) {
			continue
		}
		score.Entry = *entry
		if opts.Limit > 0 {
			out = insertActivationResult(out, *score, opts.Limit)
			continue
		}
		out = append(out, *score)
	}
	return out
}

func sortActivationScores(scores []ActivationScore) {
	sort.SliceStable(scores, func(i, j int) bool {
		return activationScoreBetter(scores[i], scores[j])
	})
}

func insertActivationResult(results []ActivationScore, candidate ActivationScore, limit int) []ActivationScore {
	if limit <= 0 {
		return append(results, candidate)
	}
	insertAt := len(results)
	for i, result := range results {
		if activationScoreBetter(candidate, result) {
			insertAt = i
			break
		}
	}
	if len(results) >= limit && insertAt >= limit {
		return results
	}
	results = append(results, ActivationScore{})
	copy(results[insertAt+1:], results[insertAt:])
	results[insertAt] = candidate
	if len(results) > limit {
		results = results[:limit]
	}
	return results
}

func activationScoreBetter(left, right ActivationScore) bool {
	if left.Score == right.Score {
		return left.Entry.CreatedAt.After(right.Entry.CreatedAt)
	}
	return left.Score > right.Score
}

func insertActivationSeed(seeds []string, candidate string, scores map[string]*ActivationScore, limit int) []string {
	if candidate == "" || limit <= 0 {
		return seeds
	}
	insertAt := len(seeds)
	for i, seed := range seeds {
		if activationSeedBetter(candidate, seed, scores) {
			insertAt = i
			break
		}
	}
	if len(seeds) >= limit && insertAt >= limit {
		return seeds
	}
	seeds = append(seeds, "")
	copy(seeds[insertAt+1:], seeds[insertAt:])
	seeds[insertAt] = candidate
	if len(seeds) > limit {
		seeds = seeds[:limit]
	}
	return seeds
}

func activationSeedBetter(leftID, rightID string, scores map[string]*ActivationScore) bool {
	left := scores[leftID]
	right := scores[rightID]
	if left == nil {
		return false
	}
	if right == nil {
		return true
	}
	if left.DirectScore == right.DirectScore {
		return left.Entry.CreatedAt.After(right.Entry.CreatedAt)
	}
	return left.DirectScore > right.DirectScore
}

func matchActivation(e *Entry, queryLower string, queryTerms []string) ActivationComponents {
	if e == nil {
		return ActivationComponents{}
	}
	contentLower := strings.ToLower(e.Content)
	categoryLower := strings.ToLower(e.Category)
	components := ActivationComponents{}

	if queryLower != "" && strings.Contains(contentLower, queryLower) {
		components.Lexical = 1.0
		if contentLower == queryLower {
			components.Lexical = 2.0
		}
	}
	if queryLower != "" && strings.Contains(categoryLower, queryLower) {
		components.Category += 0.5
	}

	termHits := 0
	for _, term := range queryTerms {
		if term == "" {
			continue
		}
		if strings.Contains(contentLower, term) {
			components.Lexical += 0.22
			termHits++
			continue
		}
		if strings.Contains(categoryLower, term) {
			components.Category += 0.12
			termHits++
		}
	}
	if termHits >= 2 {
		components.Lexical += 0.25
	}

	for _, tag := range e.Tags {
		tagLower := strings.ToLower(tag)
		if queryLower != "" && strings.Contains(tagLower, queryLower) {
			components.Tags += 0.3
			break
		}
		for _, term := range queryTerms {
			if strings.Contains(tagLower, term) {
				components.Tags += 0.12
				break
			}
		}
	}
	for _, alias := range e.Aliases {
		aliasLower := strings.ToLower(alias)
		if queryLower != "" && (strings.Contains(aliasLower, queryLower) || strings.Contains(queryLower, aliasLower)) {
			components.Aliases += 0.5
			break
		}
		for _, term := range queryTerms {
			if strings.Contains(aliasLower, term) || strings.Contains(term, aliasLower) {
				components.Aliases += 0.16
				break
			}
		}
	}
	for _, link := range e.Links {
		linkLower := strings.ToLower(link)
		if queryLower != "" && (strings.Contains(linkLower, queryLower) || strings.Contains(queryLower, linkLower)) {
			components.Links += 0.6
			break
		}
		for _, term := range queryTerms {
			if strings.Contains(linkLower, term) || strings.Contains(term, linkLower) {
				components.Links += 0.18
				break
			}
		}
	}
	return components
}

func tierActivationMultiplier(t Tier) float64 {
	switch t {
	case TierShort:
		return 0.8
	case TierLong:
		return 1.2
	default:
		return 1.0
	}
}

func (s *Store) spreadActivationGraphLocked(scores map[string]*ActivationScore, seeds []string, now time.Time, opts ActivationOptions) {
	for _, id := range seeds {
		source := scores[id]
		if source == nil {
			continue
		}
		s.spreadActivationFromLocked(scores, source, id, now, opts)
	}
}

func (s *Store) spreadActivationFromLocked(scores map[string]*ActivationScore, source *ActivationScore, id string, now time.Time, opts ActivationOptions) {
	entry := s.entries[id]
	if !entryIsActive(entry, now) {
		return
	}

	for _, link := range s.graph.Forward[id] {
		for _, key := range graphKeysForLink(link) {
			for _, targetID := range s.graph.Names[key] {
				s.addActivationBoostLocked(scores, source, targetID, id, "wikilink_target", link, 0.55, now, opts)
			}
			for _, targetID := range s.graph.Backlinks[key] {
				s.addActivationBoostLocked(scores, source, targetID, id, "backlink", link, 0.35, now, opts)
			}
		}
	}

	for _, alias := range graphAliasesForEntry(entry) {
		key := graphKey(alias)
		for _, targetID := range s.graph.Backlinks[key] {
			s.addActivationBoostLocked(scores, source, targetID, id, "alias_backlink", alias, 0.45, now, opts)
		}
	}

	for _, tag := range entry.Tags {
		key := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(tag)), "#")
		for _, targetID := range s.graph.Tags[key] {
			s.addActivationBoostLocked(scores, source, targetID, id, "shared_tag", tag, 0.18, now, opts)
		}
	}
}

func (s *Store) addActivationBoostLocked(scores map[string]*ActivationScore, source *ActivationScore, targetID, sourceID, kind, via string, coefficient float64, now time.Time, opts ActivationOptions) {
	if targetID == "" || targetID == sourceID {
		return
	}
	target := s.entries[targetID]
	if !entryIsActive(target, now) {
		return
	}
	boost := source.Score * coefficient * max(target.Weight(now), 0.05)
	if boost <= 0 {
		return
	}

	score := scores[targetID]
	if score == nil {
		score = &ActivationScore{
			EntryID: targetID,
			Entry:   *target,
			Components: ActivationComponents{
				Importance: target.Importance,
				Tier:       tierActivationMultiplier(target.Tier),
				Recency:    target.recencyFactor(now),
				Access:     target.accessBoost(),
			},
		}
		scores[targetID] = score
	}
	if opts.MaxGraphBoost > 0 {
		remaining := opts.MaxGraphBoost - score.Components.GraphBoost
		if remaining <= 0 {
			return
		}
		if boost > remaining {
			boost = remaining
		}
	}
	score.Score += boost
	score.Components.GraphBoost += boost
	score.Paths = append(score.Paths, ActivationPath{
		FromID: sourceID,
		ToID:   targetID,
		Via:    strings.TrimSpace(via),
		Kind:   kind,
		Weight: coefficient,
	})
}
