package memory

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"gopkg.in/yaml.v3"
)

// --- 三层记忆架构 ---
//
// Layer 1: 短期 (Short-term) — 当前会话的对话历史，会话结束即消失
// Layer 2: 中期 (Medium-term) — 日常记忆，自动摘要压缩，按时间衰减
// Layer 3: 长期 (Long-term) — 持久核心记忆，类似 MEMORY.md，手动或自动提升

// Tier 记忆层级
type Tier int

const (
	TierShort  Tier = iota // 短期：会话内
	TierMedium             // 中期：日常
	TierLong               // 长期：持久
)

func (t Tier) String() string {
	switch t {
	case TierShort:
		return "short"
	case TierMedium:
		return "medium"
	case TierLong:
		return "long"
	default:
		return "unknown"
	}
}

// Entry 代表一条记忆
type Entry struct {
	ID          string     `json:"id"`
	Content     string     `json:"content"`
	Category    string     `json:"category"`
	Tier        Tier       `json:"tier"`
	Importance  float64    `json:"importance"`   // 0.0 ~ 1.0，越高越重要
	AccessCount int        `json:"access_count"` // 被检索次数
	CreatedAt   time.Time  `json:"created_at"`
	AccessedAt  time.Time  `json:"accessed_at"` // 最后被检索时间
	Tags        []string   `json:"tags,omitempty"`
	SummaryOf   []string   `json:"summary_of,omitempty"` // 如果是摘要，记录原始条目 ID
	ExpiresAt   *time.Time `json:"expires_at,omitempty"` // 过期时间，nil 表示永不过期
	Status      string     `json:"status,omitempty"`     // active/superseded/archived/conflict
	ValidFrom   time.Time  `json:"valid_from,omitempty"`
	ValidUntil  *time.Time `json:"valid_until,omitempty"`
	Links       []string   `json:"links,omitempty"`     // Obsidian wikilinks referenced by this note
	Aliases     []string   `json:"aliases,omitempty"`   // Obsidian note aliases / concept aliases
	StateKey    string     `json:"state_key,omitempty"` // Stable key for temporal state resolution
	StateValue  string     `json:"state_value,omitempty"`
	Confidence  float64    `json:"confidence,omitempty"`
	Supersedes  []string   `json:"supersedes,omitempty"`
	BlockID     string     `json:"block_id,omitempty"` // Stable Obsidian block id for exact references
	Path        string     `json:"path,omitempty"`     // Path relative to the memory vault
}

// Weight 计算记忆权重（用于排序和衰减）
// 考虑：重要性 × 时间衰减 × 访问频率加成
func (e *Entry) Weight(now time.Time) float64 {
	return e.Importance * e.recencyFactor(now) * e.accessBoost()
}

func (e *Entry) recencyFactor(now time.Time) float64 {
	halflife := e.halflife()
	if halflife <= 0 {
		return 1
	}
	age := now.Sub(e.CreatedAt).Hours()
	if age <= 0 {
		return 1
	}
	return math.Pow(0.5, age/halflife)
}

func (e *Entry) accessBoost() float64 {
	if e.AccessCount <= 0 {
		return 1
	}
	return 1 + min(math.Log1p(float64(e.AccessCount))*0.12, 0.75)
}

// halflife 返回该层级记忆的半衰期（小时）
func (e *Entry) halflife() float64 {
	switch e.Tier {
	case TierShort:
		return 1.0 // 1 小时
	case TierMedium:
		return 24.0 * 7 // 1 周
	case TierLong:
		return 24.0 * 365 // 1 年
	default:
		return 24.0
	}
}

// Store 管理三层持久记忆
type Store struct {
	mu      sync.RWMutex
	entries map[string]*Entry // key: entry ID
	paths   map[string]string // key: entry ID, value: relative note path
	graph   *GraphIndex
	dir     string
	nextID  int64
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
// The Markdown notes remain the source of truth; this is a deterministic layer
// that helps the agent convert graph recall into tool and answer constraints.
type RouteAnalysis struct {
	Query             string   `json:"query"`
	Entries           []Entry  `json:"entries"`
	RequiredTools     []string `json:"required_tools,omitempty"`
	SuggestedSearches []string `json:"suggested_searches,omitempty"`
	RiskFlags         []string `json:"risk_flags,omitempty"`
	Constraints       []string `json:"constraints,omitempty"`
	Clarifications    []string `json:"clarifications,omitempty"`
	TemporalNotes     []string `json:"temporal_notes,omitempty"`
	EvidenceRefs      []string `json:"evidence_refs,omitempty"`
	SupersededRefs    []string `json:"superseded_refs,omitempty"`
	ConflictRefs      []string `json:"conflict_refs,omitempty"`
	ExpiredRefs       []string `json:"expired_refs,omitempty"`
	FutureRefs        []string `json:"future_refs,omitempty"`
}

// SaveOptions carries optional Obsidian and temporal-state metadata.
type SaveOptions struct {
	Tags       []string
	Links      []string
	Aliases    []string
	Status     string
	ValidFrom  time.Time
	ValidUntil *time.Time
	ExpiresAt  *time.Time
	StateKey   string
	StateValue string
	Confidence float64
	Supersedes []string
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
	s.mu.Lock()
	defer s.mu.Unlock()

	content = sanitizeDurableMemoryContent(content)
	category = strings.TrimSpace(category)
	if content == "" {
		return nil
	}
	opts = enrichSaveOptionsWithConcepts(content, category, opts)
	if !strings.EqualFold(strings.TrimSpace(category), "concept") {
		s.ensureConceptEntriesLocked(opts.Links)
	}

	// 去重检查：同 category + 同 content（忽略前后空白）不重复写入
	normalized := strings.TrimSpace(content)
	for _, e := range s.entries {
		if strings.EqualFold(strings.TrimSpace(e.Content), normalized) &&
			strings.EqualFold(e.Category, category) {
			// 已存在：更新访问时间和标签，但不重复写入
			e.AccessedAt = time.Now()
			if len(opts.Tags) > 0 {
				e.Tags = mergeTags(e.Tags, opts.Tags)
			}
			if len(opts.Links) > 0 {
				e.Links = normalizeLinks(append(e.Links, opts.Links...))
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
			e.Links = normalizeLinks(append(e.Links, extractWikiLinks(e.Content)...))
			e.Aliases = dedupSlice(e.Aliases)
			return s.persist()
		}
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
		ID:         s.generateID(),
		Content:    content,
		Category:   category,
		Tier:       tier,
		Importance: importance,
		CreatedAt:  now,
		AccessedAt: now,
		Tags:       opts.Tags,
		Aliases:    dedupSlice(opts.Aliases),
		ExpiresAt:  opts.ExpiresAt,
		Status:     status,
		ValidFrom:  validFrom,
		ValidUntil: opts.ValidUntil,
		StateKey:   strings.TrimSpace(opts.StateKey),
		StateValue: strings.TrimSpace(opts.StateValue),
		Confidence: clampFloat(opts.Confidence, 0, 1),
		Supersedes: dedupSlice(opts.Supersedes),
	}
	entry.BlockID = blockIDForEntry(entry.ID)
	entry.Links = normalizeLinks(append(opts.Links, extractWikiLinks(content)...))
	s.entries[entry.ID] = entry
	return s.persist()
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
	// 去重检查
	normalized := strings.TrimSpace(content)
	for _, e := range s.entries {
		if strings.EqualFold(strings.TrimSpace(e.Content), normalized) &&
			strings.EqualFold(e.Category, category) {
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
			return s.persist()
		}
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
	return s.persist()
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

	if len(toDelete) > 0 {
		s.persist()
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

// Route searches memory and derives deterministic tool/answer constraints.
func (s *Store) Route(query string) RouteAnalysis {
	entries := activationScoresToEntries(s.Activate(query, RouteActivationOptions()))
	route := RouteAnalysis{
		Query:   strings.TrimSpace(query),
		Entries: entries,
	}
	if len(entries) == 0 {
		return route
	}
	resolution := s.ResolveTemporal(query, entries)
	entries = resolution.Entries
	route.Entries = entries
	route.TemporalNotes = resolution.Notes
	route.SupersededRefs = resolution.SupersededRefs
	route.ConflictRefs = resolution.ConflictRefs
	route.ExpiredRefs = resolution.ExpiredRefs
	route.FutureRefs = resolution.FutureRefs

	text := routeAnalysisText(query, entries)
	hasOutdoor := routeTextHasAny(text, "outdoor plan", "outdoor", "park", "公园", "户外", "出门", "外出", "踏青", "郊游")
	hasChild := routeTextHasAny(text, "daughter", "child", "kid", "女儿", "孩子", "小孩", "儿童", "小朋友")
	if hasOutdoor {
		entries = s.includeRouteLocationEntries(entries, 3)
		route.Entries = entries
		text = routeAnalysisText(query, entries)
	}
	hasPollen := routeTextHasAny(text, "pollen allergy", "pollen", "hay fever", "allergy", "花粉", "花粉过敏", "花粉症", "过敏")
	pollenInactive := routeStateInactive(entries, "pollen")
	hasActivePollenRisk := hasPollen && !pollenInactive
	hasWeather := routeTextHasAny(text, "weather forecast", "weather", "forecast", "天气", "气温", "风力", "下雨")
	hasAirQuality := routeTextHasAny(text, "air quality", "aqi", "pm2.5", "空气质量", "空气", "雾霾")
	location := routeLocationHint(query, entries)

	if hasChild {
		route.RiskFlags = append(route.RiskFlags, "child_or_family_context")
		route.Constraints = append(route.Constraints, "Apply family/child-related memories when judging this request.")
	}
	if pollenInactive {
		route.RiskFlags = append(route.RiskFlags, "pollen_allergy_inactive_or_resolved")
		route.Constraints = append(route.Constraints, "Latest temporal memory says the pollen-allergy state is inactive/resolved; do not apply older allergy risk unless new evidence contradicts it.")
	}
	if len(route.SupersededRefs) > 0 && hasPollen {
		route.Constraints = append(route.Constraints, "Do not apply older allergy risk unless new evidence contradicts it.")
	}
	if hasActivePollenRisk {
		route.RiskFlags = append(route.RiskFlags, "pollen_allergy")
		route.Constraints = append(route.Constraints, "Account for the remembered pollen allergy risk before recommending outdoor activity.")
	}
	if hasOutdoor {
		route.RiskFlags = append(route.RiskFlags, "outdoor_exposure")
	}
	if hasOutdoor && hasChild && hasActivePollenRisk {
		route.RiskFlags = append(route.RiskFlags, "child_health_outdoor_plan")
		route.RequiredTools = append(route.RequiredTools, "current_time", "web_search")
		route.Constraints = append(route.Constraints,
			"Before the final answer, check current or forecast conditions relevant to outdoor exposure.",
			"Include pollen exposure, wind/weather, and air quality uncertainty in the recommendation.",
		)
		route.SuggestedSearches = append(route.SuggestedSearches,
			routeSearchQuery(location, "weather forecast wind outdoor afternoon"),
			routeSearchQuery(location, "pollen forecast allergy level"),
			routeSearchQuery(location, "air quality AQI PM2.5"),
		)
	}
	if hasWeather && hasOutdoor {
		route.RequiredTools = append(route.RequiredTools, "current_time", "web_search")
		route.Constraints = append(route.Constraints, "Use live or forecast weather instead of relying only on static memory.")
	}
	if hasAirQuality && hasOutdoor {
		route.RequiredTools = append(route.RequiredTools, "web_search")
		route.Constraints = append(route.Constraints, "Check air quality when outdoor health risk is part of the request.")
	}
	if hasOutdoor && location == "" {
		route.Clarifications = append(route.Clarifications, "Ask for the city or area if no remembered or user-provided location is available.")
	} else if location != "" {
		route.Constraints = append(route.Constraints, "Use location hint: "+location+". If the user provides another location, prefer the current user-provided location.")
	}

	route.RequiredTools = dedupSlice(route.RequiredTools)
	route.SuggestedSearches = dedupSlice(route.SuggestedSearches)
	route.RiskFlags = prioritizeRiskFlags(dedupSlice(route.RiskFlags))
	route.Constraints = dedupSlice(route.Constraints)
	route.Clarifications = dedupSlice(route.Clarifications)
	route.EvidenceRefs = routeEvidenceRefs(entries, 6)
	return route
}

func (s *Store) includeRouteLocationEntries(entries []Entry, limit int) []Entry {
	if s == nil || limit <= 0 {
		return entries
	}
	seen := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		seen[e.ID] = struct{}{}
	}
	now := time.Now()
	s.mu.RLock()
	defer s.mu.RUnlock()
	var added int
	for _, e := range s.entries {
		if e == nil || !entryIsActive(e, now) {
			continue
		}
		if _, ok := seen[e.ID]; ok {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(e.Category), "location") {
			continue
		}
		entries = append(entries, *e)
		seen[e.ID] = struct{}{}
		added++
		if added >= limit {
			break
		}
	}
	return entries
}

func prioritizeRiskFlags(flags []string) []string {
	if len(flags) <= 1 {
		return flags
	}
	out := append([]string(nil), flags...)
	sort.SliceStable(out, func(i, j int) bool {
		return riskFlagRank(out[i]) > riskFlagRank(out[j])
	})
	return out
}

func riskFlagRank(flag string) int {
	switch strings.ToLower(strings.TrimSpace(flag)) {
	case "child_health_outdoor_plan":
		return 100
	case "pollen_allergy":
		return 80
	case "pollen_allergy_inactive_or_resolved":
		return 75
	case "outdoor_exposure":
		return 60
	case "child_or_family_context":
		return 50
	default:
		return 0
	}
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
	return s.persist()
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

	return s.persist()
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

	if len(toDelete) > 0 {
		s.persist()
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

	// 删除原始条目
	for _, id := range sourceIDs {
		s.removeEntryFileLocked(id)
		delete(s.entries, id)
	}

	return s.persist()
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

	if len(toDelete) > 0 {
		s.persist()
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

	if len(toDelete) > 0 {
		s.persist()
	}
	return len(toDelete)
}

// Count 返回总记忆数
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.entries)
}

// Dir returns the root directory of the LuckyHarness memory vault.
func (s *Store) Dir() string {
	if s == nil {
		return ""
	}
	return s.dir
}

// --- 内部方法 ---

type entryScore struct {
	entry Entry
	score float64
}

func (s *Store) generateID() string {
	s.nextID++
	return fmt.Sprintf("mem_%d_%d", time.Now().Unix(), s.nextID)
}

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
	s.rebuildGraphLocked()
	return nil
}

func (s *Store) persist() error {
	if err := s.ensureVaultDirs(); err != nil {
		return err
	}
	for _, e := range s.entries {
		normalizeEntryForNote(e)
		rel := e.Path
		if rel == "" {
			rel = notePathForEntry(e)
			e.Path = rel
		}
		s.paths[e.ID] = rel
		path := filepath.Join(s.dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return fmt.Errorf("create note dir: %w", err)
		}
		if err := os.WriteFile(path, []byte(renderMemoryNote(e)), 0600); err != nil {
			return fmt.Errorf("write memory note %s: %w", path, err)
		}
	}
	s.rebuildGraphLocked()
	return nil
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

type memoryNoteFrontmatter struct {
	ID          string     `yaml:"id"`
	Type        string     `yaml:"type"`
	Tier        string     `yaml:"tier"`
	Category    string     `yaml:"category"`
	Importance  float64    `yaml:"importance"`
	AccessCount int        `yaml:"access_count"`
	CreatedAt   time.Time  `yaml:"created_at"`
	AccessedAt  time.Time  `yaml:"accessed_at"`
	Tags        []string   `yaml:"tags,omitempty"`
	SummaryOf   []string   `yaml:"summary_of,omitempty"`
	ExpiresAt   *time.Time `yaml:"expires_at,omitempty"`
	Status      string     `yaml:"status,omitempty"`
	ValidFrom   time.Time  `yaml:"valid_from,omitempty"`
	ValidUntil  *time.Time `yaml:"valid_until,omitempty"`
	Links       []string   `yaml:"links,omitempty"`
	Aliases     []string   `yaml:"aliases,omitempty"`
	StateKey    string     `yaml:"state_key,omitempty"`
	StateValue  string     `yaml:"state_value,omitempty"`
	Confidence  float64    `yaml:"confidence,omitempty"`
	Supersedes  []string   `yaml:"supersedes,omitempty"`
	BlockID     string     `yaml:"block_id,omitempty"`
}

var wikiLinkPattern = regexp.MustCompile(`!?\[\[([^\]|#]+)(?:[#|][^\]]*)?\]\]`)
var blockIDPattern = regexp.MustCompile(`(?m)(?:\s+\^[A-Za-z0-9_-]+|\n\^[A-Za-z0-9_-]+\s*)$`)

func newGraphIndex() *GraphIndex {
	return &GraphIndex{
		Forward:   make(map[string][]string),
		Backlinks: make(map[string][]string),
		Tags:      make(map[string][]string),
		Names:     make(map[string][]string),
	}
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
	path := filepath.Join(s.dir, "00_Index", "LuckyHarness Memory Vault.md")
	if st, err := os.Stat(path); err == nil && !st.IsDir() {
		return nil
	}
	body := strings.TrimSpace(`# LuckyHarness Memory Vault

This directory is the LuckyHarness durable memory source of truth.

- Memory notes are Obsidian-compatible Markdown files under the category folders.
- Authoritative memory notes use YAML frontmatter with type: memory.
- Wikilinks, tags, aliases, temporal state fields, and block IDs are part of the memory graph.
- The RAG SQLite database is for indexed documents, not durable user memory.
- An external Obsidian app vault, .obsidian directory, or OBSIDIAN_VAULT_PATH is not required for LuckyHarness memory.
`) + "\n"
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		return fmt.Errorf("write memory vault readme: %w", err)
	}
	return nil
}

func normalizeEntryForNote(e *Entry) {
	now := time.Now()
	if e.ID == "" {
		e.ID = fmt.Sprintf("mem_%d", now.UnixNano())
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = now
	}
	if e.AccessedAt.IsZero() {
		e.AccessedAt = e.CreatedAt
	}
	if e.Status == "" {
		e.Status = "active"
	}
	if e.ValidFrom.IsZero() {
		e.ValidFrom = e.CreatedAt
	}
	if e.BlockID == "" {
		e.BlockID = blockIDForEntry(e.ID)
	}
	e.Tags = dedupSlice(e.Tags)
	e.Aliases = dedupSlice(e.Aliases)
	e.Supersedes = dedupSlice(e.Supersedes)
	e.StateKey = strings.TrimSpace(e.StateKey)
	e.StateValue = strings.TrimSpace(e.StateValue)
	if e.Confidence < 0 || e.Confidence > 1 {
		e.Confidence = clampFloat(e.Confidence, 0, 1)
	}
	e.Links = normalizeLinks(append(e.Links, extractWikiLinks(e.Content)...))
}

func blockIDForEntry(id string) string {
	id = strings.TrimSpace(id)
	id = strings.ReplaceAll(id, "_", "-")
	if id == "" {
		return "mem-block"
	}
	return id
}

func notePathForEntry(e *Entry) string {
	if strings.EqualFold(strings.TrimSpace(e.Category), "concept") {
		return conceptNotePath(e.Content)
	}
	created := e.CreatedAt
	if created.IsZero() {
		created = time.Now()
	}
	dir := noteDirForEntry(e)
	slug := slugify(truncateRunes(stripWikiSyntax(e.Content), 48))
	if slug == "" {
		slug = strings.ReplaceAll(e.ID, "_", "-")
	}
	name := fmt.Sprintf("%s-%s-%s.md", created.Format("20060102-150405"), slug, e.ID)
	return filepath.ToSlash(filepath.Join(dir, name))
}

func noteDirForEntry(e *Entry) string {
	category := strings.ToLower(strings.TrimSpace(e.Category))
	switch category {
	case "identity", "preference", "profile", "user":
		return "10_Profile"
	case "project", "context", "code", "repo":
		return "20_Projects"
	case "decision", "architecture":
		return "40_Decisions"
	case "rule", "tool", "workflow":
		return "60_Rules"
	case "concept":
		return "70_Concepts"
	case "conversation", "task", "session":
		return "30_Sessions"
	case "archive":
		return "90_Archive"
	default:
		if e.Tier == TierLong {
			return "50_Facts"
		}
		return "50_Facts"
	}
}

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func stripWikiSyntax(s string) string {
	return wikiLinkPattern.ReplaceAllStringFunc(s, func(match string) string {
		parts := wikiLinkPattern.FindStringSubmatch(match)
		if len(parts) < 2 {
			return match
		}
		return parts[1]
	})
}

func truncateRunes(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

func renderMemoryNote(e *Entry) string {
	fm := memoryNoteFrontmatter{
		ID:          e.ID,
		Type:        "memory",
		Tier:        e.Tier.String(),
		Category:    e.Category,
		Importance:  e.Importance,
		AccessCount: e.AccessCount,
		CreatedAt:   e.CreatedAt,
		AccessedAt:  e.AccessedAt,
		Tags:        e.Tags,
		SummaryOf:   e.SummaryOf,
		ExpiresAt:   e.ExpiresAt,
		Status:      e.Status,
		ValidFrom:   e.ValidFrom,
		ValidUntil:  e.ValidUntil,
		Links:       e.Links,
		Aliases:     e.Aliases,
		StateKey:    e.StateKey,
		StateValue:  e.StateValue,
		Confidence:  e.Confidence,
		Supersedes:  e.Supersedes,
		BlockID:     e.BlockID,
	}
	yml, _ := yaml.Marshal(fm)
	title := strings.TrimSpace(stripWikiSyntax(e.Content))
	if title == "" {
		title = e.ID
	}
	title = truncateRunes(strings.ReplaceAll(title, "\n", " "), 80)

	var b strings.Builder
	b.WriteString("---\n")
	b.Write(yml)
	b.WriteString("---\n\n")
	b.WriteString("# " + title + "\n\n")
	b.WriteString("## Memory\n\n")
	b.WriteString(strings.TrimSpace(e.Content))
	b.WriteString("\n\n^" + e.BlockID + "\n")
	if len(e.Links) > 0 || len(e.SummaryOf) > 0 {
		b.WriteString("\n## Links\n\n")
		for _, link := range e.Links {
			b.WriteString("- [[" + link + "]]\n")
		}
		for _, id := range e.SummaryOf {
			b.WriteString("- Summary of [[" + id + "]]\n")
		}
	}
	return b.String()
}

func parseMemoryNote(path, root string) (*Entry, bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, false, err
	}
	fmRaw, body, ok := splitFrontmatter(string(raw))
	if !ok {
		return nil, false, nil
	}
	var fm memoryNoteFrontmatter
	if err := yaml.Unmarshal([]byte(fmRaw), &fm); err != nil {
		return nil, false, err
	}
	if fm.Type != "memory" || fm.ID == "" {
		return nil, false, nil
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		rel = path
	}
	content := extractMarkdownSection(body, "Memory")
	if content == "" {
		content = strings.TrimSpace(bodyWithoutTitle(body))
	}
	content = strings.TrimSpace(blockIDPattern.ReplaceAllString(content, ""))

	entry := &Entry{
		ID:          fm.ID,
		Content:     content,
		Category:    fm.Category,
		Tier:        parseTier(fm.Tier),
		Importance:  fm.Importance,
		AccessCount: fm.AccessCount,
		CreatedAt:   fm.CreatedAt,
		AccessedAt:  fm.AccessedAt,
		Tags:        fm.Tags,
		SummaryOf:   fm.SummaryOf,
		ExpiresAt:   fm.ExpiresAt,
		Status:      fm.Status,
		ValidFrom:   fm.ValidFrom,
		ValidUntil:  fm.ValidUntil,
		Links:       normalizeLinks(append(fm.Links, extractWikiLinks(content)...)),
		Aliases:     dedupSlice(fm.Aliases),
		StateKey:    fm.StateKey,
		StateValue:  fm.StateValue,
		Confidence:  fm.Confidence,
		Supersedes:  dedupSlice(fm.Supersedes),
		BlockID:     fm.BlockID,
		Path:        filepath.ToSlash(rel),
	}
	normalizeEntryForNote(entry)
	return entry, true, nil
}

func splitFrontmatter(md string) (string, string, bool) {
	md = strings.TrimPrefix(md, "\ufeff")
	if !strings.HasPrefix(md, "---\n") {
		return "", md, false
	}
	rest := md[len("---\n"):]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", md, false
	}
	fm := rest[:end]
	body := rest[end+len("\n---"):]
	return fm, strings.TrimLeft(body, "\r\n"), true
}

func bodyWithoutTitle(body string) string {
	lines := strings.Split(body, "\n")
	if len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[0]), "# ") {
		lines = lines[1:]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func extractMarkdownSection(body, heading string) string {
	lines := strings.Split(body, "\n")
	target := "## " + heading
	start := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == target {
			start = i + 1
			break
		}
	}
	if start == -1 {
		return ""
	}
	end := len(lines)
	for i := start; i < len(lines); i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "## ") {
			end = i
			break
		}
	}
	return strings.TrimSpace(strings.Join(lines[start:end], "\n"))
}

func parseTier(raw string) Tier {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "short", "0":
		return TierShort
	case "medium", "mid", "1", "":
		return TierMedium
	case "long", "2":
		return TierLong
	default:
		return TierMedium
	}
}

func extractWikiLinks(text string) []string {
	matches := wikiLinkPattern.FindAllStringSubmatch(text, -1)
	links := make([]string, 0, len(matches))
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		link := strings.TrimSpace(m[1])
		if link != "" {
			links = append(links, link)
		}
	}
	return normalizeLinks(links)
}

func normalizeLinks(links []string) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, len(links))
	for _, link := range links {
		link = strings.TrimSpace(link)
		if link == "" {
			continue
		}
		key := strings.ToLower(link)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, link)
	}
	return out
}

func extractQueryTerms(query string) []string {
	var terms []string
	var latin strings.Builder
	var han []rune

	flushLatin := func() {
		if latin.Len() == 0 {
			return
		}
		token := strings.ToLower(latin.String())
		if len([]rune(token)) >= 2 {
			terms = append(terms, token)
		}
		latin.Reset()
	}
	flushHan := func() {
		if len(han) == 0 {
			return
		}
		if len(han) == 1 {
			han = han[:0]
			return
		}
		if len(han) <= 4 {
			terms = append(terms, string(han))
		}
		for n := 2; n <= 4; n++ {
			if len(han) < n {
				continue
			}
			for i := 0; i+n <= len(han); i++ {
				terms = append(terms, string(han[i:i+n]))
			}
		}
		han = han[:0]
	}

	for _, r := range query {
		switch {
		case unicode.Is(unicode.Han, r):
			flushLatin()
			han = append(han, r)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			flushHan()
			latin.WriteRune(unicode.ToLower(r))
		default:
			flushLatin()
			flushHan()
		}
	}
	flushLatin()
	flushHan()
	return expandQueryTerms(query, dedupSlice(terms))
}

type queryAliasRule struct {
	Triggers []string
	Aliases  []string
}

var queryAliasRules = []queryAliasRule{
	{Triggers: []string{"女儿", "闺女", "daughter"}, Aliases: []string{"daughter", "child", "family"}},
	{Triggers: []string{"儿子", "son"}, Aliases: []string{"son", "child", "family"}},
	{Triggers: []string{"孩子", "小孩", "儿童", "小朋友", "带娃", "child", "kid"}, Aliases: []string{"child", "daughter", "son", "family"}},
	{Triggers: []string{"花粉", "花粉症", "pollen", "hay fever"}, Aliases: []string{"pollen allergy", "pollen", "allergy", "hay fever"}},
	{Triggers: []string{"过敏", "allergy", "allergic"}, Aliases: []string{"pollen allergy", "allergy", "hay fever"}},
	{Triggers: []string{"出门", "外出", "户外", "公园", "踏青", "郊游", "outdoor", "park"}, Aliases: []string{"outdoor plan", "outdoor", "park"}},
	{Triggers: []string{"天气", "下雨", "气温", "温度", "forecast", "weather"}, Aliases: []string{"weather forecast", "weather"}},
	{Triggers: []string{"空气质量", "空气", "雾霾", "aqi", "pm2.5"}, Aliases: []string{"air quality", "aqi"}},
	{Triggers: []string{"上海", "shanghai"}, Aliases: []string{"shanghai"}},
}

type conceptRule struct {
	Concept  string
	Triggers []string
	Aliases  []string
	Tags     []string
}

var builtInConceptRules = []conceptRule{
	{
		Concept:  "LuckyHarness",
		Triggers: []string{"luckyagent", "lh", "l h"},
		Aliases:  []string{"lh"},
		Tags:     []string{"concept/luckyagent"},
	},
	{
		Concept:  "LuckyHarness Memory",
		Triggers: []string{"luckyagent memory", "lh memory", "记忆库", "durable memory", "working memory", "memory vault", "obsidian-first", "graph memory", "双链记忆"},
		Aliases:  []string{"记忆库", "graph memory", "working memory"},
		Tags:     []string{"concept/memory"},
	},
	{
		Concept:  "Obsidian",
		Triggers: []string{"obsidian", "双链", "wikilink", "backlink", "vault"},
		Aliases:  []string{"双链", "wikilink", "backlink"},
		Tags:     []string{"concept/obsidian"},
	},
	{
		Concept:  "Message Gateway",
		Triggers: []string{"msg-gateway", "message gateway", "gateway", "网关", "消息网关", "渠道"},
		Aliases:  []string{"网关", "消息网关", "gateway"},
		Tags:     []string{"concept/gateway"},
	},
	{
		Concept:  "QQ Official",
		Triggers: []string{"qq official", "qqofficial", "qq 官方", "qq官方", "官方渠道", "官方频道"},
		Aliases:  []string{"QQ官方", "官方渠道", "官方频道"},
		Tags:     []string{"concept/gateway", "gateway/qqofficial"},
	},
	{
		Concept:  "Reasoning Content",
		Triggers: []string{"reasoning_content", "reasoning content", "chain-of-thought", "chain of thought", "cot", "思维链", "推理内容"},
		Aliases:  []string{"思维链", "推理内容", "chain-of-thought"},
		Tags:     []string{"concept/reasoning"},
	},
	{
		Concept:  "Gateway Trace",
		Triggers: []string{"trace", "progress trace", "tool trace", "reasoning trace", "轨迹", "进度卡片", "工具轨迹"},
		Aliases:  []string{"trace", "进度轨迹", "工具轨迹"},
		Tags:     []string{"concept/trace"},
	},
	{
		Concept:  "Session Memory",
		Triggers: []string{"session", "sessions", "会话", "历史会话", "session history"},
		Aliases:  []string{"会话", "session history"},
		Tags:     []string{"concept/session"},
	},
	{
		Concept:  "RAG",
		Triggers: []string{"rag", "retrieval augmented", "检索增强", "向量召回"},
		Aliases:  []string{"检索增强", "向量召回"},
		Tags:     []string{"concept/rag"},
	},
}

func enrichSaveOptionsWithConcepts(content, category string, opts SaveOptions) SaveOptions {
	links, aliases, tags := inferConceptMetadata(content, category)
	if len(links) > 0 {
		opts.Links = normalizeLinks(append(opts.Links, links...))
	}
	if len(aliases) > 0 {
		opts.Aliases = dedupSlice(append(opts.Aliases, aliases...))
	}
	if len(tags) > 0 {
		opts.Tags = mergeTags(opts.Tags, tags)
	}
	return opts
}

func inferConceptMetadata(content, category string) (links, aliases, tags []string) {
	text := strings.ToLower(strings.Join([]string{category, content}, "\n"))
	for _, rule := range builtInConceptRules {
		if !conceptRuleMatches(text, rule.Triggers) {
			continue
		}
		links = append(links, rule.Concept)
		aliases = append(aliases, rule.Aliases...)
		tags = append(tags, rule.Tags...)
	}
	return normalizeLinks(links), dedupSlice(aliases), dedupSlice(tags)
}

func conceptRuleMatches(text string, triggers []string) bool {
	for _, trigger := range triggers {
		trigger = strings.ToLower(strings.TrimSpace(trigger))
		if trigger != "" && strings.Contains(text, trigger) {
			return true
		}
	}
	return false
}

func init() {
	for _, rule := range builtInConceptRules {
		queryAliasRules = append(queryAliasRules, queryAliasRule{
			Triggers: append([]string{rule.Concept}, rule.Aliases...),
			Aliases:  append([]string{rule.Concept}, rule.Aliases...),
		})
	}
}

func (s *Store) ensureConceptEntriesLocked(links []string) {
	for _, link := range normalizeLinks(links) {
		rule, ok := conceptRuleForName(link)
		if !ok {
			continue
		}
		if s.hasConceptEntryLocked(rule.Concept) {
			continue
		}
		now := time.Now()
		entry := &Entry{
			ID:         conceptEntryID(rule.Concept),
			Content:    rule.Concept,
			Category:   "concept",
			Tier:       TierLong,
			Importance: 0.85,
			CreatedAt:  now,
			AccessedAt: now,
			Tags:       mergeTags([]string{"concept"}, rule.Tags),
			Aliases:    dedupSlice(rule.Aliases),
			Status:     "active",
			ValidFrom:  now,
			BlockID:    blockIDForEntry(conceptEntryID(rule.Concept)),
			Path:       conceptNotePath(rule.Concept),
		}
		entry.Links = normalizeLinks(conceptRelatedLinks(rule))
		s.entries[entry.ID] = entry
		s.paths[entry.ID] = entry.Path
	}
}

func conceptRuleForName(name string) (conceptRule, bool) {
	key := graphKey(name)
	for _, rule := range builtInConceptRules {
		if graphKey(rule.Concept) == key {
			return rule, true
		}
		for _, alias := range rule.Aliases {
			if graphKey(alias) == key {
				return rule, true
			}
		}
	}
	return conceptRule{}, false
}

func (s *Store) hasConceptEntryLocked(concept string) bool {
	id := conceptEntryID(concept)
	if _, ok := s.entries[id]; ok {
		return true
	}
	key := graphKey(concept)
	for _, entry := range s.entries {
		if entry == nil || !strings.EqualFold(strings.TrimSpace(entry.Category), "concept") {
			continue
		}
		if graphKey(entry.Content) == key {
			return true
		}
		for _, alias := range entry.Aliases {
			if graphKey(alias) == key {
				return true
			}
		}
	}
	return false
}

func conceptEntryID(concept string) string {
	slug := slugify(concept)
	if slug == "" {
		slug = "concept"
	}
	return "concept_" + strings.ReplaceAll(slug, "-", "_")
}

func conceptNotePath(concept string) string {
	slug := slugify(concept)
	if slug == "" {
		slug = "concept"
	}
	return filepath.ToSlash(filepath.Join("70_Concepts", slug+".md"))
}

func conceptRelatedLinks(rule conceptRule) []string {
	switch rule.Concept {
	case "QQ Official":
		return []string{"Message Gateway", "Gateway Trace", "Reasoning Content"}
	case "Reasoning Content":
		return []string{"Gateway Trace", "QQ Official"}
	case "Gateway Trace":
		return []string{"Message Gateway", "Reasoning Content"}
	case "LuckyHarness Memory":
		return []string{"LuckyHarness", "Obsidian", "RAG"}
	case "Obsidian":
		return []string{"LuckyHarness Memory"}
	case "Session Memory":
		return []string{"LuckyHarness Memory"}
	case "RAG":
		return []string{"LuckyHarness Memory"}
	case "Message Gateway":
		return []string{"LuckyHarness"}
	default:
		return nil
	}
}

func expandQueryTerms(queryLower string, terms []string) []string {
	out := append([]string(nil), terms...)
	for _, rule := range queryAliasRules {
		if queryAliasRuleMatches(queryLower, terms, rule.Triggers) {
			out = append(out, rule.Aliases...)
		}
	}
	return dedupSlice(out)
}

func queryAliasRuleMatches(queryLower string, terms []string, triggers []string) bool {
	for _, trigger := range triggers {
		trigger = strings.ToLower(strings.TrimSpace(trigger))
		if trigger == "" {
			continue
		}
		if strings.Contains(queryLower, trigger) {
			return true
		}
		for _, term := range terms {
			if term == trigger {
				return true
			}
		}
	}
	return false
}

func memoryMatchScore(e *Entry, queryLower string, queryTerms []string) float64 {
	if e == nil {
		return 0
	}
	contentLower := strings.ToLower(e.Content)
	categoryLower := strings.ToLower(e.Category)

	matchScore := 0.0
	if queryLower != "" && strings.Contains(contentLower, queryLower) {
		matchScore = 1.0
		if contentLower == queryLower {
			matchScore = 2.0
		}
	}
	if queryLower != "" && strings.Contains(categoryLower, queryLower) {
		matchScore += 0.5
	}

	termHits := 0
	for _, term := range queryTerms {
		if term == "" {
			continue
		}
		if strings.Contains(contentLower, term) {
			matchScore += 0.22
			termHits++
			continue
		}
		if strings.Contains(categoryLower, term) {
			matchScore += 0.12
			termHits++
		}
	}
	if termHits >= 2 {
		matchScore += 0.25
	}

	for _, tag := range e.Tags {
		tagLower := strings.ToLower(tag)
		if queryLower != "" && strings.Contains(tagLower, queryLower) {
			matchScore += 0.3
			break
		}
		for _, term := range queryTerms {
			if strings.Contains(tagLower, term) {
				matchScore += 0.12
				break
			}
		}
	}
	for _, alias := range e.Aliases {
		aliasLower := strings.ToLower(alias)
		if queryLower != "" && (strings.Contains(aliasLower, queryLower) || strings.Contains(queryLower, aliasLower)) {
			matchScore += 0.5
			break
		}
		for _, term := range queryTerms {
			if strings.Contains(aliasLower, term) || strings.Contains(term, aliasLower) {
				matchScore += 0.16
				break
			}
		}
	}
	for _, link := range e.Links {
		linkLower := strings.ToLower(link)
		if queryLower != "" && (strings.Contains(linkLower, queryLower) || strings.Contains(queryLower, linkLower)) {
			matchScore += 0.6
			break
		}
		for _, term := range queryTerms {
			if strings.Contains(linkLower, term) || strings.Contains(term, linkLower) {
				matchScore += 0.18
				break
			}
		}
	}
	return matchScore
}

func resolveActiveTemporalEntries(entries []Entry) ([]Entry, []string, []string) {
	if len(entries) <= 1 {
		return entries, nil, nil
	}
	latestByState := make(map[string]Entry)
	explicitSuperseded := make(map[string]string)
	for _, e := range entries {
		stateKey := strings.ToLower(strings.TrimSpace(e.StateKey))
		if stateKey != "" {
			if current, ok := latestByState[stateKey]; !ok || temporalEntryAfter(e, current) {
				latestByState[stateKey] = e
			}
		}
		for _, id := range e.Supersedes {
			id = strings.TrimSpace(id)
			if id != "" {
				explicitSuperseded[id] = refForEntry(&e)
			}
		}
	}

	selected := make([]Entry, 0, len(entries))
	var notes []string
	var supersededRefs []string
	for _, e := range entries {
		ref := refForEntry(&e)
		if by, ok := explicitSuperseded[e.ID]; ok {
			supersededRefs = append(supersededRefs, ref)
			notes = append(notes, "Superseded memory ignored: "+ref+"; replaced by "+by+".")
			continue
		}
		stateKey := strings.ToLower(strings.TrimSpace(e.StateKey))
		if stateKey != "" {
			latest := latestByState[stateKey]
			if latest.ID != e.ID {
				supersededRefs = append(supersededRefs, ref)
				notes = append(notes, "For state "+stateKey+", prefer latest memory "+refForEntry(&latest)+" over older memory "+ref+".")
				continue
			}
		}
		selected = append(selected, e)
	}
	return selected, dedupSlice(notes), dedupSlice(supersededRefs)
}

func temporalEntryAfter(a, b Entry) bool {
	at := entryTemporalTime(a)
	bt := entryTemporalTime(b)
	if !at.Equal(bt) {
		return at.After(bt)
	}
	if a.Confidence != b.Confidence {
		return a.Confidence > b.Confidence
	}
	if a.Importance != b.Importance {
		return a.Importance > b.Importance
	}
	return a.CreatedAt.After(b.CreatedAt)
}

func entryTemporalTime(e Entry) time.Time {
	if !e.ValidFrom.IsZero() {
		return e.ValidFrom
	}
	return e.CreatedAt
}

func temporalCandidateMatches(e *Entry, queryLower string, queryTerms []string, activeLinks, activeStateKeys map[string]bool) bool {
	if e == nil {
		return false
	}
	if e.StateKey != "" && activeStateKeys[strings.ToLower(e.StateKey)] {
		return true
	}
	if memoryMatchScore(e, queryLower, queryTerms) > 0 {
		return true
	}
	for _, link := range e.Links {
		if activeLinks[graphKey(link)] {
			return true
		}
	}
	return false
}

func temporalInactiveReason(e *Entry, now time.Time) string {
	if e == nil {
		return ""
	}
	status := strings.ToLower(strings.TrimSpace(e.Status))
	switch status {
	case "conflict":
		return "conflict"
	case "superseded":
		return "superseded"
	}
	if !e.ValidFrom.IsZero() && e.ValidFrom.After(now) {
		return "future"
	}
	if e.ValidUntil != nil && !e.ValidUntil.After(now) {
		return "expired"
	}
	if e.ExpiresAt != nil && !e.ExpiresAt.After(now) {
		return "expired"
	}
	return ""
}

func refForEntry(e *Entry) string {
	if e == nil {
		return ""
	}
	ref := e.ID
	if e.Path != "" {
		ref = e.Path
		if e.BlockID != "" {
			ref += "#" + e.BlockID
		}
	}
	return ref
}

func routeAnalysisText(query string, entries []Entry) string {
	var b strings.Builder
	b.WriteString(strings.ToLower(query))
	for _, e := range entries {
		b.WriteByte('\n')
		b.WriteString(strings.ToLower(e.Content))
		b.WriteByte('\n')
		b.WriteString(strings.ToLower(e.Category))
		for _, tag := range e.Tags {
			b.WriteByte(' ')
			b.WriteString(strings.ToLower(tag))
		}
		for _, link := range e.Links {
			b.WriteByte(' ')
			b.WriteString(strings.ToLower(link))
		}
		for _, alias := range e.Aliases {
			b.WriteByte(' ')
			b.WriteString(strings.ToLower(alias))
		}
	}
	return b.String()
}

func routeTextHasAny(text string, needles ...string) bool {
	for _, needle := range needles {
		needle = strings.ToLower(strings.TrimSpace(needle))
		if needle != "" && strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func routeStateInactive(entries []Entry, keyNeedle string) bool {
	keyNeedle = strings.ToLower(strings.TrimSpace(keyNeedle))
	if keyNeedle == "" {
		return false
	}
	for _, e := range entries {
		key := strings.ToLower(e.StateKey + " " + strings.Join(e.Links, " ") + " " + e.Content)
		if !strings.Contains(key, keyNeedle) {
			continue
		}
		value := strings.ToLower(strings.TrimSpace(e.StateValue))
		switch value {
		case "resolved", "inactive", "none", "negative", "false", "no", "无", "已缓解", "已解除":
			return true
		}
	}
	return false
}

func routeLocationHint(query string, entries []Entry) string {
	queryLower := strings.ToLower(query)
	switch {
	case strings.Contains(query, "上海") || strings.Contains(queryLower, "shanghai"):
		return "Shanghai"
	case strings.Contains(query, "北京") || strings.Contains(queryLower, "beijing"):
		return "Beijing"
	case strings.Contains(query, "杭州") || strings.Contains(queryLower, "hangzhou"):
		return "Hangzhou"
	case strings.Contains(query, "深圳") || strings.Contains(queryLower, "shenzhen"):
		return "Shenzhen"
	case strings.Contains(query, "广州") || strings.Contains(queryLower, "guangzhou"):
		return "Guangzhou"
	}
	for _, e := range entries {
		if strings.EqualFold(strings.TrimSpace(e.Category), "location") {
			if loc := firstKnownLocation(append(e.Links, append(e.Aliases, e.Content)...)); loc != "" {
				return loc
			}
		}
	}
	return firstKnownLocationFromEntries(entries)
}

func firstKnownLocationFromEntries(entries []Entry) string {
	for _, e := range entries {
		if loc := firstKnownLocation(append(e.Links, append(e.Aliases, e.Content)...)); loc != "" {
			return loc
		}
	}
	return ""
}

func firstKnownLocation(values []string) string {
	for _, value := range values {
		lower := strings.ToLower(value)
		switch {
		case strings.Contains(value, "上海") || strings.Contains(lower, "shanghai"):
			return "Shanghai"
		case strings.Contains(value, "北京") || strings.Contains(lower, "beijing"):
			return "Beijing"
		case strings.Contains(value, "杭州") || strings.Contains(lower, "hangzhou"):
			return "Hangzhou"
		case strings.Contains(value, "深圳") || strings.Contains(lower, "shenzhen"):
			return "Shenzhen"
		case strings.Contains(value, "广州") || strings.Contains(lower, "guangzhou"):
			return "Guangzhou"
		}
	}
	return ""
}

func routeSearchQuery(location, topic string) string {
	topic = strings.TrimSpace(topic)
	if location == "" {
		return topic
	}
	return strings.TrimSpace(location + " " + topic)
}

func routeEvidenceRefs(entries []Entry, limit int) []string {
	if limit <= 0 || len(entries) == 0 {
		return nil
	}
	capacity := limit
	if len(entries) < capacity {
		capacity = len(entries)
	}
	refs := make([]string, 0, capacity)
	for _, e := range entries {
		ref := refForEntry(&e)
		if ref != "" {
			refs = append(refs, ref)
		}
		if len(refs) >= limit {
			break
		}
	}
	return dedupSlice(refs)
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

func entryIsActive(e *Entry, asOf time.Time) bool {
	if e == nil {
		return false
	}
	status := strings.ToLower(strings.TrimSpace(e.Status))
	if status != "" && status != "active" {
		return false
	}
	if !e.ValidFrom.IsZero() && e.ValidFrom.After(asOf) {
		return false
	}
	if e.ValidUntil != nil && !e.ValidUntil.After(asOf) {
		return false
	}
	if e.ExpiresAt != nil && !e.ExpiresAt.After(asOf) {
		return false
	}
	return true
}

func (s *Store) rebuildGraphLocked() {
	graph := newGraphIndex()
	for id, entry := range s.entries {
		links := normalizeLinks(append(entry.Links, extractWikiLinks(entry.Content)...))
		graph.Forward[id] = links
		for _, link := range links {
			for _, key := range graphKeysForLink(link) {
				graph.Backlinks[key] = append(graph.Backlinks[key], id)
			}
		}
		for _, alias := range graphAliasesForEntry(entry) {
			key := graphKey(alias)
			if key != "" {
				graph.Names[key] = append(graph.Names[key], id)
			}
		}
		for _, tag := range entry.Tags {
			tag = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(tag)), "#")
			if tag == "" {
				continue
			}
			graph.Tags[tag] = append(graph.Tags[tag], id)
		}
	}
	s.graph = graph
}

func (s *Store) removeEntryFileLocked(id string) {
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
