package memory

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yurika0211/luckyagent/internal/memory/maintain"
	"github.com/yurika0211/luckyagent/internal/memory/note"
	"github.com/yurika0211/luckyagent/internal/memory/policy"
)

// Durable vault API.
//
// The note record lives in package note, route-policy evaluation in package
// policy, and turn cadence in package maintain. Conversation buffers, session
// summaries, and the optional reranker live in shortterm, midterm, and tidal.

type (
	Tier                 = note.Tier
	Entry                = note.Entry
	RoutePolicy          = note.RoutePolicy
	RoutePolicyMatch     = note.RoutePolicyMatch
	RouteTermGroup       = note.RouteTermGroup
	RouteStateMatch      = note.RouteStateMatch
	RouteRisk            = note.RouteRisk
	RouteToolRequirement = note.RouteToolRequirement
	RouteToolCall        = note.RouteToolCall
	AppliedRoutePolicy   = note.AppliedRoutePolicy
)

const (
	TierShort  = note.TierShort
	TierMedium = note.TierMedium
	TierLong   = note.TierLong
)

// Store 管理三层持久记忆
type Store struct {
	mu                 sync.RWMutex
	entries            map[string]*Entry // key: entry ID
	paths              map[string]string // key: entry ID, value: relative note path
	graph              *GraphIndex
	lexical            *lexicalIndex
	duplicates         map[string]string // category+content -> entry ID
	activationReranker ActivationReranker
	dir                string
	nextID             int64
	// maintenance owns turn cadence for this process-wide memory store. It is
	// initialized lazily so the Store zero value remains useful in tests.
	maintenanceOnce sync.Once
	maintenance     *maintain.Coordinator
}

type closeableActivationReranker interface {
	Close() error
}

// GraphIndex is derived from Obsidian wikilinks. Markdown notes remain the
// source of truth; this graph is rebuilt from note bodies/frontmatter.
type GraphIndex struct {
	Forward   map[string][]string // entry ID -> linked note names
	Backlinks map[string][]string // linked note name -> entry IDs
	Tags      map[string][]string // tag -> entry IDs
	Names     map[string][]string // normalized note/block aliases -> entry IDs
}

// RouteAnalysis turns retrieved memories into action-facing routing signals.
type RouteAnalysis = policy.Analysis

// RouteOptions controls which recalled entries can affect routing.
type RouteOptions = policy.RouteOptions

// SaveOptions carries optional Obsidian and temporal-state metadata.
type SaveOptions struct {
	Tags          []string
	Links         []string
	Aliases       []string
	Status        string
	ValidFrom     time.Time
	ValidUntil    *time.Time
	ExpiresAt     *time.Time
	StateKey      string
	StateValue    string
	Confidence    float64
	Supersedes    []string
	RoutePolicies []RoutePolicy
}

// SaveResult describes whether a durable memory save created a new note or
// updated an existing duplicate.
type SaveResult struct {
	ID              string `json:"id,omitempty"`
	Path            string `json:"path,omitempty"`
	Created         bool   `json:"created"`
	UpdatedExisting bool   `json:"updated_existing"`
	DuplicateOf     string `json:"duplicate_of,omitempty"`
}

// NewStore 创建记忆存储
func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create memory dir: %w", err)
	}
	s := &Store{
		entries: make(map[string]*Entry),
		paths:   make(map[string]string),
		graph:   newGraphIndex(),
		dir:     dir,
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// Save 保存一条记忆（默认中期层级）
func (s *Store) Save(content, category string) error {
	return s.SaveWithTier(content, category, TierMedium, 0.5)
}

// SaveWithTier 保存一条指定层级的记忆（带去重）
func (s *Store) SaveWithTier(content, category string, tier Tier, importance float64) error {
	return s.SaveWithTierAndTags(content, category, tier, importance, nil)
}

// SaveWithTierAndTags 保存一条指定层级和标签的记忆（带去重）
func (s *Store) SaveWithTierAndTags(content, category string, tier Tier, importance float64, tags []string) error {
	return s.SaveWithMetadata(content, category, tier, importance, tags, nil, nil)
}

// SaveWithMetadata saves a memory note with Obsidian graph metadata.
func (s *Store) SaveWithMetadata(content, category string, tier Tier, importance float64, tags, links, aliases []string) error {
	return s.SaveWithOptions(content, category, tier, importance, SaveOptions{Tags: tags, Links: links, Aliases: aliases})
}

// SaveWithOptions saves a memory note with graph and temporal-state metadata.
func (s *Store) SaveWithOptions(content, category string, tier Tier, importance float64, opts SaveOptions) error {
	_, err := s.SaveWithOptionsResult(content, category, tier, importance, opts)
	return err
}

// SaveWithOptionsResult saves a memory note and reports whether it was created
// or merged into an existing duplicate.
func (s *Store) SaveWithOptionsResult(content, category string, tier Tier, importance float64, opts SaveOptions) (SaveResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	content = sanitizeDurableMemoryContent(content)
	category = strings.TrimSpace(category)
	if content == "" {
		return SaveResult{}, nil
	}
	policies, err := policy.Normalize(opts.RoutePolicies)
	if err != nil {
		return SaveResult{}, err
	}
	opts.RoutePolicies = policies
	opts = enrichSaveOptionsWithConcepts(content, category, opts)
	entryLinks := normalizeMemoryLinks(append(opts.Links, extractWikiLinks(content)...))
	conceptIDs := s.ensureConceptEntriesLocked(entryLinks)

	// 去重检查：同 category + 同 content（忽略前后空白）不重复写入
	normalized := strings.TrimSpace(content)
	if e := s.duplicateOfLocked(normalized, category); e != nil {
		s.unindexEntryLocked(e)
		// 已存在：更新访问时间和标签，但不重复写入
		e.AccessedAt = time.Now()
		if len(opts.Tags) > 0 {
			e.Tags = mergeTags(e.Tags, opts.Tags)
		}
		if len(entryLinks) > 0 {
			e.Links = normalizeMemoryLinks(append(e.Links, entryLinks...))
		}
		if len(opts.Aliases) > 0 {
			e.Aliases = dedupSlice(append(e.Aliases, opts.Aliases...))
		}
		// 如果新层级更高，提升
		if tier > e.Tier {
			e.Tier = tier
		}
		// 如果新重要性更高，更新
		if importance > e.Importance {
			e.Importance = importance
		}
		if e.Status == "" {
			e.Status = "active"
		}
		if opts.Status != "" {
			e.Status = strings.TrimSpace(opts.Status)
		}
		if e.ValidFrom.IsZero() {
			e.ValidFrom = e.CreatedAt
		}
		if !opts.ValidFrom.IsZero() {
			e.ValidFrom = opts.ValidFrom
		}
		if opts.ValidUntil != nil {
			e.ValidUntil = opts.ValidUntil
		}
		if opts.ExpiresAt != nil {
			e.ExpiresAt = opts.ExpiresAt
		}
		if strings.TrimSpace(opts.StateKey) != "" {
			e.StateKey = strings.TrimSpace(opts.StateKey)
		}
		if strings.TrimSpace(opts.StateValue) != "" {
			e.StateValue = strings.TrimSpace(opts.StateValue)
		}
		if opts.Confidence > 0 {
			e.Confidence = clampFloat(opts.Confidence, 0, 1)
		}
		if len(opts.Supersedes) > 0 {
			e.Supersedes = dedupSlice(append(e.Supersedes, opts.Supersedes...))
		}
		if len(opts.RoutePolicies) > 0 {
			e.RoutePolicies = policy.Merge(e.RoutePolicies, opts.RoutePolicies)
		}
		e.Links = normalizeMemoryLinks(append(e.Links, extractWikiLinks(e.Content)...))
		e.Aliases = dedupSlice(e.Aliases)
		s.indexEntryLocked(e)
		if err := s.persistEntriesLocked(append(conceptIDs, e.ID)); err != nil {
			return SaveResult{}, err
		}
		return SaveResult{
			ID:              e.ID,
			Path:            e.Path,
			Created:         false,
			UpdatedExisting: true,
			DuplicateOf:     e.ID,
		}, nil
	}

	now := time.Now()
	status := strings.TrimSpace(opts.Status)
	if status == "" {
		status = "active"
	}
	validFrom := opts.ValidFrom
	if validFrom.IsZero() {
		validFrom = now
	}
	entry := &Entry{
		ID:            s.generateID(),
		Content:       content,
		Category:      category,
		Tier:          tier,
		Importance:    importance,
		CreatedAt:     now,
		AccessedAt:    now,
		Tags:          opts.Tags,
		Aliases:       dedupSlice(opts.Aliases),
		ExpiresAt:     opts.ExpiresAt,
		Status:        status,
		ValidFrom:     validFrom,
		ValidUntil:    opts.ValidUntil,
		StateKey:      strings.TrimSpace(opts.StateKey),
		StateValue:    strings.TrimSpace(opts.StateValue),
		Confidence:    clampFloat(opts.Confidence, 0, 1),
		Supersedes:    dedupSlice(opts.Supersedes),
		RoutePolicies: append([]RoutePolicy(nil), opts.RoutePolicies...),
	}
	entry.BlockID = blockIDForEntry(entry.ID)
	entry.Links = entryLinks
	s.entries[entry.ID] = entry
	s.indexEntryLocked(entry)
	if err := s.persistEntriesLocked(append(conceptIDs, entry.ID)); err != nil {
		return SaveResult{}, err
	}
	return SaveResult{
		ID:      entry.ID,
		Path:    entry.Path,
		Created: true,
	}, nil
}

// mergeTags 合并标签，去重
func mergeTags(existing, newTags []string) []string {
	seen := make(map[string]bool)
	for _, t := range existing {
		seen[strings.ToLower(t)] = true
	}
	for _, t := range newTags {
		if !seen[strings.ToLower(t)] {
			existing = append(existing, t)
			seen[strings.ToLower(t)] = true
		}
	}
	return existing
}

// SaveLongTerm 保存长期记忆（高重要性）
func (s *Store) SaveLongTerm(content, category string) error {
	return s.SaveWithTier(content, category, TierLong, 0.9)
}

// SaveShortTerm 保存短期记忆（低重要性，默认 1 小时过期）
func (s *Store) SaveShortTerm(content, category string) error {
	return s.SaveWithTier(content, category, TierShort, 0.3)
}

// SaveShortTermTTL 保存短期记忆，指定 TTL
func (s *Store) SaveShortTermTTL(content, category string, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	content = sanitizeDurableMemoryContent(content)
	category = strings.TrimSpace(category)
	if content == "" {
		return nil
	}
	normalized := strings.TrimSpace(content)
	if e := s.duplicateOfLocked(normalized, category); e != nil {
		s.unindexEntryLocked(e)
		e.AccessedAt = time.Now()
		if tier := TierShort; tier > e.Tier {
			e.Tier = tier
		}
		if e.Status == "" {
			e.Status = "active"
		}
		if e.ValidFrom.IsZero() {
			e.ValidFrom = e.CreatedAt
		}
		s.indexEntryLocked(e)
		return s.persistEntriesLocked([]string{e.ID})
	}

	now := time.Now()
	expiresAt := now.Add(ttl)
	entry := &Entry{
		ID:         s.generateID(),
		Content:    content,
		Category:   category,
		Tier:       TierShort,
		Importance: 0.3,
		CreatedAt:  now,
		AccessedAt: now,
		ExpiresAt:  &expiresAt,
		Status:     "active",
		ValidFrom:  now,
	}
	entry.BlockID = blockIDForEntry(entry.ID)
	entry.Links = normalizeLinks(extractWikiLinks(content))
	s.entries[entry.ID] = entry
	s.indexEntryLocked(entry)
	return s.persistEntriesLocked([]string{entry.ID})
}

// Expire 清除已过期的记忆
func (s *Store) Expire() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	var toDelete []string

	for id, e := range s.entries {
		if e.ExpiresAt != nil && now.After(*e.ExpiresAt) {
			toDelete = append(toDelete, id)
		}
	}

	for _, id := range toDelete {
		s.removeEntryFileLocked(id)
		delete(s.entries, id)
	}
	return len(toDelete)
}

// Get 按 ID 获取记忆
func (s *Store) Get(id string) (*Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	e, ok := s.entries[id]
	if !ok {
		return nil, fmt.Errorf("memory not found: %s", id)
	}
	return e, nil
}

// ActiveStateIDs returns active memory IDs for a temporal state key.
func (s *Store) ActiveStateIDs(stateKey string) []string {
	if s == nil {
		return nil
	}
	stateKey = strings.ToLower(strings.TrimSpace(stateKey))
	if stateKey == "" {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	now := time.Now()
	ids := make([]string, 0, 2)
	for id, e := range s.entries {
		if e == nil || !entryIsActive(e, now) {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(e.StateKey), stateKey) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// SupersedeEntries marks active entries as superseded without deleting their
// Markdown notes.
func (s *Store) SupersedeEntries(ids []string) ([]string, error) {
	if s == nil || len(ids) == 0 {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		e := s.entries[id]
		if e == nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(e.Status), "superseded") {
			continue
		}
		s.unindexEntryLocked(e)
		e.Status = "superseded"
		s.indexEntryLocked(e)
		changed = append(changed, id)
	}
	if len(changed) == 0 {
		return nil, nil
	}
	if err := s.persistEntriesLocked(changed); err != nil {
		return changed, err
	}
	return changed, nil
}

// SearchParallel 并行检索三层记忆，按相关度排序返回 top-N 条
// 使用 goroutine 并发检索 short/medium/long 三层记忆
// 限制返回条数为 2-3 条最相关记忆
func (s *Store) SearchParallel(query string, limit int) []Entry {
	// 限制返回条数为 2-3 条
	if limit < 2 {
		limit = 2
	}
	if limit > 3 {
		limit = 3
	}
	scores := s.Activate(query, ActivationOptions{
		Limit:             limit,
		IncludeGraph:      true,
		MaxGraphDepth:     1,
		MaxGraphBoost:     0.45,
		UpdateAccessStats: false,
	})
	return activationScoresToEntries(scores)
}

// Search 搜索记忆（关键词匹配 + 权重排序）
func (s *Store) Search(query string) []Entry {
	return activationScoresToEntries(s.Activate(query, DefaultActivationOptions()))
}

// SearchOptions controls durable memory search from tool/API callers.
type SearchOptions struct {
	Limit           int
	Category        string
	Tier            *Tier
	IncludeInactive bool
	IncludeExpired  bool
	AsOf            time.Time
	IncludeGraph    bool
	GraphDepth      int
	Explain         bool
	SkipAccessStats bool
}

// SearchResult contains a memory entry plus ranking/explanation signals.
type SearchResult struct {
	Entry       Entry            `json:"entry"`
	Score       float64          `json:"score"`
	DirectScore float64          `json:"direct_score,omitempty"`
	GraphScore  float64          `json:"graph_score,omitempty"`
	Paths       []ActivationPath `json:"paths,omitempty"`
}

// SearchWithOptions searches memory with bounded filters and structured scores.
func (s *Store) SearchWithOptions(query string, opts SearchOptions) []SearchResult {
	if s == nil {
		return nil
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil
	}
	customAsOf := !opts.AsOf.IsZero()
	if opts.AsOf.IsZero() {
		opts.AsOf = time.Now()
	}
	if opts.GraphDepth < 0 {
		opts.GraphDepth = 0
	}
	if opts.GraphDepth == 0 {
		opts.IncludeGraph = false
	}
	if opts.IncludeGraph && opts.GraphDepth == 0 {
		opts.GraphDepth = 1
	}
	activationLimit := opts.Limit
	if activationLimit > 0 {
		activationLimit *= 3
	}
	if opts.IncludeInactive || opts.IncludeExpired || customAsOf {
		return s.searchEntriesWithOptions(query, opts)
	}
	scores := s.Activate(query, ActivationOptions{
		Limit:             activationLimit,
		IncludeGraph:      opts.IncludeGraph,
		MaxGraphDepth:     opts.GraphDepth,
		MaxGraphBoost:     0.45,
		MaxGraphSeeds:     12,
		UpdateAccessStats: !opts.SkipAccessStats,
		Explain:           opts.Explain,
	})
	results := make([]SearchResult, 0, len(scores))
	for _, score := range scores {
		if !memorySearchResultAllowed(score.Entry, opts) {
			continue
		}
		result := SearchResult{
			Entry:       score.Entry,
			Score:       score.Score,
			DirectScore: score.DirectScore,
			GraphScore:  score.Components.GraphBoost,
		}
		if opts.Explain {
			result.Paths = score.Paths
		}
		results = append(results, result)
		if opts.Limit > 0 && len(results) >= opts.Limit {
			break
		}
	}
	return results
}

func (s *Store) searchEntriesWithOptions(query string, opts SearchOptions) []SearchResult {
	queryLower := strings.ToLower(strings.TrimSpace(query))
	queryTerms := extractQueryTerms(queryLower)
	s.mu.RLock()
	defer s.mu.RUnlock()
	candidates := s.activationCandidatesLocked(queryLower, queryTerms)
	results := make([]SearchResult, 0, len(candidates))
	for _, e := range candidates {
		if e == nil || isConceptEntry(e) {
			continue
		}
		if !opts.IncludeInactive && !entryIsActive(e, opts.AsOf) {
			continue
		}
		if !opts.IncludeExpired && e.ExpiresAt != nil && !e.ExpiresAt.After(opts.AsOf) {
			continue
		}
		if !memorySearchResultAllowed(*e, opts) {
			continue
		}
		components := matchActivation(e, queryLower, queryTerms)
		matchScore := components.MatchScore()
		if matchScore <= 0 {
			continue
		}
		score := matchScore * e.Weight(opts.AsOf) * tierActivationMultiplier(e.Tier)
		results = append(results, SearchResult{
			Entry:       *e,
			Score:       score,
			DirectScore: score,
		})
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			return results[i].Entry.CreatedAt.After(results[j].Entry.CreatedAt)
		}
		return results[i].Score > results[j].Score
	})
	if opts.Limit > 0 && len(results) > opts.Limit {
		results = results[:opts.Limit]
	}
	return results
}

func memorySearchResultAllowed(e Entry, opts SearchOptions) bool {
	if strings.TrimSpace(opts.Category) != "" && !strings.EqualFold(e.Category, opts.Category) {
		return false
	}
	if opts.Tier != nil && e.Tier != *opts.Tier {
		return false
	}
	return true
}

// SetActivationReranker installs an optional post-recall reranker. Passing nil
// restores the default activation ordering.
func (s *Store) SetActivationReranker(reranker ActivationReranker) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activationReranker = reranker
}

// ActivationReranker returns the currently installed post-recall reranker.
func (s *Store) ActivationReranker() ActivationReranker {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.activationReranker
}

// Close releases optional resources owned by pluggable memory components.
func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	reranker := s.activationReranker
	s.mu.RUnlock()
	if closer, ok := reranker.(closeableActivationReranker); ok {
		return closer.Close()
	}
	return nil
}

// RecordActivationFeedback sends weak downstream usage feedback to the active
// reranker when it supports online learning.
func (s *Store) RecordActivationFeedback(query string, entries []Entry, signal string, value float64, now time.Time) {
	if s == nil || len(entries) == 0 || value == 0 {
		return
	}
	if now.IsZero() {
		now = time.Now()
	}
	s.mu.RLock()
	reranker := s.activationReranker
	s.mu.RUnlock()
	observer, ok := reranker.(ActivationFeedbackObserver)
	if !ok || observer == nil {
		return
	}
	for _, entry := range entries {
		if strings.TrimSpace(entry.ID) == "" {
			continue
		}
		observer.ObserveActivationFeedback(ActivationFeedback{
			Query:  query,
			Entry:  entry,
			Signal: signal,
			Value:  value,
			At:     now,
		})
	}
}

// Route searches memory and derives deterministic tool/answer constraints.
func (s *Store) Route(query string) RouteAnalysis {
	return s.RouteWithOptions(query, RouteOptions{})
}

// RouteWithOptions evaluates typed policies on recalled memories visible to the caller.
func (s *Store) RouteWithOptions(query string, opts RouteOptions) RouteAnalysis {
	entries := activationScoresToEntries(s.Activate(query, RouteActivationOptions()))
	if opts.EntryFilter != nil {
		filtered := make([]Entry, 0, len(entries))
		for _, entry := range entries {
			if opts.EntryFilter(entry) {
				filtered = append(filtered, entry)
			}
		}
		entries = filtered
	}
	route := RouteAnalysis{
		Query:   strings.TrimSpace(query),
		Entries: entries,
	}
	if len(entries) == 0 {
		return route
	}
	resolution := s.resolveTemporal(query, entries, opts.EntryFilter)
	entries = resolution.Entries
	route.Entries = entries
	route.TemporalNotes = resolution.Notes
	route.SupersededRefs = resolution.SupersededRefs
	route.ConflictRefs = resolution.ConflictRefs
	route.ExpiredRefs = resolution.ExpiredRefs
	route.FutureRefs = resolution.FutureRefs
	policy.Apply(&route, query, entries)
	route.EvidenceRefs = routeEvidenceRefs(entries, 6)
	return route
}

// TemporalResolution is the deterministic current-state view for recalled memories.
type TemporalResolution struct {
	Entries        []Entry
	Notes          []string
	SupersededRefs []string
	ConflictRefs   []string
	ExpiredRefs    []string
	FutureRefs     []string
}

// ResolveTemporal keeps the current memory state and reports inactive/conflict notes.
func (s *Store) ResolveTemporal(query string, activeEntries []Entry) TemporalResolution {
	return s.resolveTemporal(query, activeEntries, nil)
}

func (s *Store) resolveTemporal(query string, activeEntries []Entry, filter func(Entry) bool) TemporalResolution {
	now := time.Now()
	resolution := TemporalResolution{
		Entries: append([]Entry(nil), activeEntries...),
	}
	if len(activeEntries) == 0 {
		return resolution
	}

	selected, notes, superseded := resolveActiveTemporalEntries(activeEntries)
	resolution.Entries = selected
	resolution.Notes = append(resolution.Notes, notes...)
	resolution.SupersededRefs = append(resolution.SupersededRefs, superseded...)

	queryLower := strings.ToLower(query)
	queryTerms := extractQueryTerms(queryLower)
	activeIDs := make(map[string]bool, len(activeEntries))
	activeLinks := make(map[string]bool)
	activeStateKeys := make(map[string]bool)
	for _, e := range activeEntries {
		activeIDs[e.ID] = true
		if e.StateKey != "" {
			activeStateKeys[strings.ToLower(e.StateKey)] = true
		}
		for _, link := range e.Links {
			activeLinks[graphKey(link)] = true
		}
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	for id, e := range s.entries {
		if activeIDs[id] || e == nil {
			continue
		}
		if filter != nil && !filter(*e) {
			continue
		}
		if !temporalCandidateMatches(e, queryLower, queryTerms, activeLinks, activeStateKeys) {
			continue
		}
		ref := refForEntry(e)
		switch temporalInactiveReason(e, now) {
		case "conflict":
			resolution.ConflictRefs = append(resolution.ConflictRefs, ref)
			resolution.Notes = append(resolution.Notes, "Conflict memory present: "+ref+". Do not silently merge it with active memories.")
		case "superseded":
			resolution.SupersededRefs = append(resolution.SupersededRefs, ref)
			resolution.Notes = append(resolution.Notes, "Superseded memory ignored: "+ref+".")
		case "expired":
			resolution.ExpiredRefs = append(resolution.ExpiredRefs, ref)
			resolution.Notes = append(resolution.Notes, "Expired memory ignored: "+ref+".")
		case "future":
			resolution.FutureRefs = append(resolution.FutureRefs, ref)
			resolution.Notes = append(resolution.Notes, "Future-dated memory not yet active: "+ref+".")
		}
	}

	resolution.Notes = dedupSlice(resolution.Notes)
	resolution.SupersededRefs = dedupSlice(resolution.SupersededRefs)
	resolution.ConflictRefs = dedupSlice(resolution.ConflictRefs)
	resolution.ExpiredRefs = dedupSlice(resolution.ExpiredRefs)
	resolution.FutureRefs = dedupSlice(resolution.FutureRefs)
	return resolution
}

// Recent 返回最近的 N 条记忆（按权重排序）
func (s *Store) Recent(n int) []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now()
	all := make([]entryScore, 0, len(s.entries))
	for _, e := range s.entries {
		if !entryIsActive(e, now) || isConceptEntry(e) {
			continue
		}
		all = append(all, entryScore{entry: *e, score: e.Weight(now)})
	}

	sort.Slice(all, func(i, j int) bool {
		return all[i].score > all[j].score
	})

	if n > len(all) {
		n = len(all)
	}

	results := make([]Entry, n)
	for i := 0; i < n; i++ {
		results[i] = all[i].entry
	}
	return results
}

// ByTier 返回指定层级的记忆
func (s *Store) ByTier(tier Tier) []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var results []Entry
	now := time.Now()
	for _, e := range s.entries {
		if e.Tier == tier && entryIsActive(e, now) && !isConceptEntry(e) {
			results = append(results, *e)
		}
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].CreatedAt.After(results[j].CreatedAt)
	})
	return results
}

// ByCategory 返回指定分类的记忆
func (s *Store) ByCategory(category string) []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var results []Entry
	now := time.Now()
	for _, e := range s.entries {
		if strings.EqualFold(e.Category, category) && entryIsActive(e, now) {
			results = append(results, *e)
		}
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].CreatedAt.After(results[j].CreatedAt)
	})
	return results
}

// Delete 删除一条记忆
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.entries[id]; !ok {
		return fmt.Errorf("memory not found: %s", id)
	}
	s.removeEntryFileLocked(id)
	delete(s.entries, id)
	return nil
}

// Promote 将记忆提升到更高层级
func (s *Store) Promote(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, ok := s.entries[id]
	if !ok {
		return fmt.Errorf("memory not found: %s", id)
	}

	switch e.Tier {
	case TierShort:
		e.Tier = TierMedium
		e.Importance = max(e.Importance, 0.5)
	case TierMedium:
		e.Tier = TierLong
		e.Importance = max(e.Importance, 0.8)
	case TierLong:
		// 已经是最高层级
		return nil
	}
	return s.persistEntriesLocked([]string{id})
}

// Decay 执行记忆衰减：删除权重过低的记忆
func (s *Store) Decay(threshold float64) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	var toDelete []string

	for id, e := range s.entries {
		// 长期记忆不衰减
		if e.Tier == TierLong {
			continue
		}
		if e.Weight(now) < threshold {
			toDelete = append(toDelete, id)
		}
	}

	for _, id := range toDelete {
		s.removeEntryFileLocked(id)
		delete(s.entries, id)
	}
	return len(toDelete)
}

// Summarize 将多条记忆压缩为一条摘要
func (s *Store) Summarize(ids []string, summary string, category string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 验证原始条目存在
	var sourceIDs []string
	for _, id := range ids {
		if _, ok := s.entries[id]; ok {
			sourceIDs = append(sourceIDs, id)
		}
	}

	if len(sourceIDs) == 0 {
		return fmt.Errorf("no valid source entries to summarize")
	}

	// 创建摘要条目
	now := time.Now()
	entry := &Entry{
		ID:         s.generateID(),
		Content:    summary,
		Category:   category,
		Tier:       TierMedium,
		Importance: 0.6,
		CreatedAt:  now,
		AccessedAt: now,
		SummaryOf:  sourceIDs,
	}
	s.entries[entry.ID] = entry
	s.indexEntryLocked(entry)

	// 删除原始条目
	for _, id := range sourceIDs {
		s.removeEntryFileLocked(id)
		delete(s.entries, id)
	}

	return s.persistEntriesLocked([]string{entry.ID})
}

// Stats 返回记忆统计
func (s *Store) Stats() map[Tier]int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := map[Tier]int{
		TierShort:  0,
		TierMedium: 0,
		TierLong:   0,
	}
	now := time.Now()
	for _, e := range s.entries {
		if !entryIsActive(e, now) || isConceptEntry(e) {
			continue
		}
		stats[e.Tier]++
	}
	return stats
}

func isConceptEntry(e *Entry) bool {
	return e != nil && strings.EqualFold(strings.TrimSpace(e.Category), "concept")
}

// Dedup 去重：删除同 category + 同 content 的重复条目，保留权重最高的
func (s *Store) Dedup() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	type dedupKey struct {
		content  string
		category string
	}
	// 每组保留权重最高的
	best := make(map[dedupKey]*Entry)
	for _, e := range s.entries {
		key := dedupKey{
			content:  strings.ToLower(strings.TrimSpace(e.Content)),
			category: strings.ToLower(strings.TrimSpace(e.Category)),
		}
		if existing, ok := best[key]; ok {
			if e.Weight(now) > existing.Weight(now) {
				best[key] = e
			}
		} else {
			best[key] = e
		}
	}

	// 收集要保留的 ID
	keep := make(map[string]bool)
	for _, e := range best {
		keep[e.ID] = true
	}

	// 删除不在保留列表中的
	var toDelete []string
	for id := range s.entries {
		if !keep[id] {
			toDelete = append(toDelete, id)
		}
	}

	for _, id := range toDelete {
		s.removeEntryFileLocked(id)
		delete(s.entries, id)
	}
	return len(toDelete)
}

// PurgeCategory 删除指定分类的所有记忆
func (s *Store) PurgeCategory(category string) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	var toDelete []string
	for id, e := range s.entries {
		if strings.EqualFold(e.Category, category) {
			toDelete = append(toDelete, id)
		}
	}

	for _, id := range toDelete {
		s.removeEntryFileLocked(id)
		delete(s.entries, id)
	}
	return len(toDelete)
}

// Count 返回总记忆数
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.entries)
}

// RecordTurn records a completed turn against the store's memory runtime.
func (s *Store) RecordTurn(sessionID string) MaintenanceEvent {
	if s == nil {
		return MaintenanceEvent{}
	}
	s.maintenanceOnce.Do(func() {
		s.maintenance = NewMaintenanceCoordinator(DefaultMaintenanceConfig())
	})
	return s.maintenance.RecordTurn(sessionID)
}

// MaintenanceCoordinator returns the store-owned coordinator.
func (s *Store) MaintenanceCoordinator() *MaintenanceCoordinator {
	if s == nil {
		return nil
	}
	s.maintenanceOnce.Do(func() {
		s.maintenance = NewMaintenanceCoordinator(DefaultMaintenanceConfig())
	})
	return s.maintenance
}

// ForgetSession releases the coordinator's per-session attribution after a
// session is deleted. The process-wide cadence is intentionally preserved.
func (s *Store) ForgetSession(sessionID string) {
	if s == nil {
		return
	}
	s.MaintenanceCoordinator().ForgetSession(sessionID)
}

// Dir returns the root directory of the Aestus memory vault.
func (s *Store) Dir() string {
	if s == nil {
		return ""
	}
	return s.dir
}

type entryScore struct {
	entry Entry
	score float64
}

func (s *Store) generateID() string {
	s.nextID++
	return fmt.Sprintf("mem_%d_%d", time.Now().Unix(), s.nextID)
}

func max(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func clampFloat(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
