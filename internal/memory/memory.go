package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yurika0211/luckyharness/internal/utils"
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
}

// Weight 计算记忆权重（用于排序和衰减）
// 考虑：重要性 × 时间衰减 × 访问频率加成
func (e *Entry) Weight(now time.Time) float64 {
	// 时间衰减：指数衰减，半衰期取决于层级
	halflife := e.halflife()
	age := now.Sub(e.CreatedAt).Hours()
	decay := 0.5 * (age / halflife) // log2 衰减
	if decay < 0 {
		decay = 0
	}
	timeFactor := 1.0 / (1.0 + decay)

	// 访问频率加成：每被检索一次，权重 +5%
	accessBonus := 1.0 + float64(e.AccessCount)*0.05
	if accessBonus > 2.0 {
		accessBonus = 2.0 // 上限 2x
	}

	return e.Importance * timeFactor * accessBonus
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
	dir     string
	nextID  int64
}

// NewStore 创建记忆存储
func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create memory dir: %w", err)
	}
	s := &Store{
		entries: make(map[string]*Entry),
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
	s.mu.Lock()
	defer s.mu.Unlock()

	// 去重检查：同 category + 同 content（忽略前后空白）不重复写入
	normalized := strings.TrimSpace(content)
	for _, e := range s.entries {
		if strings.EqualFold(strings.TrimSpace(e.Content), normalized) &&
			strings.EqualFold(e.Category, category) {
			// 已存在：更新访问时间和标签，但不重复写入
			e.AccessedAt = time.Now()
			if len(tags) > 0 {
				e.Tags = mergeTags(e.Tags, tags)
			}
			// 如果新层级更高，提升
			if tier > e.Tier {
				e.Tier = tier
			}
			// 如果新重要性更高，更新
			if importance > e.Importance {
				e.Importance = importance
			}
			return s.persist()
		}
	}

	now := time.Now()
	entry := &Entry{
		ID:         s.generateID(),
		Content:    content,
		Category:   category,
		Tier:       tier,
		Importance: importance,
		CreatedAt:  now,
		AccessedAt: now,
		Tags:       tags,
	}
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

	// 去重检查
	normalized := strings.TrimSpace(content)
	for _, e := range s.entries {
		if strings.EqualFold(strings.TrimSpace(e.Content), normalized) &&
			strings.EqualFold(e.Category, category) {
			e.AccessedAt = time.Now()
			if tier := TierShort; tier > e.Tier {
				e.Tier = tier
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
	}
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

	now := time.Now()
	queryLower := strings.ToLower(query)

	// 按层级分组记忆
	shortEntries := make([]*Entry, 0)
	mediumEntries := make([]*Entry, 0)
	longEntries := make([]*Entry, 0)

	s.mu.RLock()
	for _, e := range s.entries {
		switch e.Tier {
		case TierShort:
			shortEntries = append(shortEntries, e)
		case TierMedium:
			mediumEntries = append(mediumEntries, e)
		case TierLong:
			longEntries = append(longEntries, e)
		}
	}
	s.mu.RUnlock()

	// 使用 channel 收集各层级的检索结果
	type tierResult struct {
		tier    Tier
		entries []entryScore
	}
	resultCh := make(chan tierResult, 3)

	// 并发检索三层记忆
	searchTier := func(tier Tier, entries []*Entry) {
		var scored []entryScore
		for _, e := range entries {
			contentLower := strings.ToLower(e.Content)
			categoryLower := strings.ToLower(e.Category)

			// 关键词匹配评分
			matchScore := 0.0
			if strings.Contains(contentLower, queryLower) {
				matchScore = 1.0
				// 精确匹配加分
				if contentLower == queryLower {
					matchScore = 2.0
				}
			}
			if strings.Contains(categoryLower, queryLower) {
				matchScore += 0.5
			}
			// 标签匹配
			for _, tag := range e.Tags {
				if strings.Contains(strings.ToLower(tag), queryLower) {
					matchScore += 0.3
					break
				}
			}

			if matchScore > 0 {
				// 综合分 = 匹配分 × 权重 × 层级系数
				// 长期记忆权重更高
				tierMultiplier := 1.0
				switch tier {
				case TierShort:
					tierMultiplier = 0.8
				case TierMedium:
					tierMultiplier = 1.0
				case TierLong:
					tierMultiplier = 1.2
				}
				totalScore := matchScore * e.Weight(now) * tierMultiplier
				scored = append(scored, entryScore{entry: *e, score: totalScore})
			}
		}
		resultCh <- tierResult{tier: tier, entries: scored}
	}

	// 启动三个 goroutine 并发检索
	go searchTier(TierShort, shortEntries)
	go searchTier(TierMedium, mediumEntries)
	go searchTier(TierLong, longEntries)

	// 收集所有结果
	var allScored []entryScore
	for i := 0; i < 3; i++ {
		result := <-resultCh
		allScored = append(allScored, result.entries...)
	}

	// 按综合分降序排序
	sort.Slice(allScored, func(i, j int) bool {
		return allScored[i].score > allScored[j].score
	})

	// 取 top-N
	if limit > len(allScored) {
		limit = len(allScored)
	}
	results := make([]Entry, limit)
	for i := 0; i < limit; i++ {
		results[i] = allScored[i].entry
	}

	// 异步持久化访问计数更新
	go func() {
		s.mu.RLock()
		defer s.mu.RUnlock()
		_ = s.persist()
	}()

	return results
}

// Search 搜索记忆（关键词匹配 + 权重排序）
func (s *Store) Search(query string) []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	queryLower := strings.ToLower(query)

	var scored []entryScore
	for _, e := range s.entries {
		contentLower := strings.ToLower(e.Content)
		categoryLower := strings.ToLower(e.Category)

		// 关键词匹配
		matchScore := 0.0
		if strings.Contains(contentLower, queryLower) {
			matchScore = 1.0
			// 精确匹配加分
			if contentLower == queryLower {
				matchScore = 2.0
			}
		}
		if strings.Contains(categoryLower, queryLower) {
			matchScore += 0.5
		}
		// 标签匹配
		for _, tag := range e.Tags {
			if strings.Contains(strings.ToLower(tag), queryLower) {
				matchScore += 0.3
				break
			}
		}

		if matchScore > 0 {
			// 综合分 = 匹配分 × 权重
			totalScore := matchScore * e.Weight(now)
			scored = append(scored, entryScore{entry: *e, score: totalScore})

			// 更新访问计数
			e.AccessCount++
			e.AccessedAt = now
		}
	}

	// 按综合分降序排序
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	results := make([]Entry, len(scored))
	for i, s := range scored {
		results[i] = s.entry
	}

	// 持久化访问计数更新
	_ = s.persist()

	return results
}

// Recent 返回最近的 N 条记忆（按权重排序）
func (s *Store) Recent(n int) []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now()
	all := make([]entryScore, 0, len(s.entries))
	for _, e := range s.entries {
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
	for _, e := range s.entries {
		if e.Tier == tier {
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
	for _, e := range s.entries {
		if strings.EqualFold(e.Category, category) {
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
	for _, e := range s.entries {
		stats[e.Tier]++
	}
	return stats
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
	path := filepath.Join(s.dir, "memory.md")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// 尝试迁移旧格式
			return s.migrateOldFormat()
		} else {
			return fmt.Errorf("load memory: %w", err)
		}
	}

	if strings.Contains(string(data), "```json") {
		data = []byte(extractJSONCodeFence(string(data)))
	}

	var entries []*Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return fmt.Errorf("parse memory: %w", err)
	}

	maxID := int64(0)
	for _, e := range entries {
		s.entries[e.ID] = e
		// 追踪最大 ID
		var idNum int64
		fmt.Sscanf(e.ID, "mem_%d_%d", new(int64), &idNum)
		if idNum > maxID {
			maxID = idNum
		}
	}
	s.nextID = maxID

	return nil
}

// migrateOldFormat 从 v0.1.0 的纯文本格式迁移
func (s *Store) migrateOldFormat() error {
	oldPath := filepath.Join(s.dir, "memory.txt")
	data, err := os.ReadFile(oldPath)
	if err != nil {
		return nil // 没有旧文件也正常
	}

	lines := utils.SplitLines(string(data))
	now := time.Now()
	for i, line := range lines {
		if line == "" {
			continue
		}
		entry := &Entry{
			ID:         fmt.Sprintf("mem_migrated_%d", i),
			Content:    line,
			Category:   "migrated",
			Tier:       TierMedium,
			Importance: 0.5,
			CreatedAt:  now,
			AccessedAt: now,
		}
		s.entries[entry.ID] = entry
	}

	if len(s.entries) > 0 {
		s.nextID = int64(len(s.entries))
		return s.persist()
	}
	return nil
}

func (s *Store) persist() error {
	path := filepath.Join(s.dir, "memory.md")

	entries := make([]*Entry, 0, len(s.entries))
	for _, e := range s.entries {
		entries = append(entries, e)
	}

	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal memory: %w", err)
	}

	var b strings.Builder
	b.WriteString("# LuckyHarness Memory\n\n")
	b.WriteString("自动生成，请勿手动编辑 JSON 块。\n\n")
	b.WriteString("```json\n")
	b.Write(data)
	b.WriteString("\n```\n")

	return os.WriteFile(path, []byte(b.String()), 0600)
}

func extractJSONCodeFence(md string) string {
	start := strings.Index(md, "```json")
	if start == -1 {
		return md
	}
	start += len("```json")
	rest := md[start:]
	end := strings.LastIndex(rest, "\n```")
	if end == -1 {
		end = strings.LastIndex(rest, "```")
	}
	if end == -1 {
		return strings.TrimSpace(rest)
	}
	return strings.TrimSpace(rest[:end])
}

func max(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
