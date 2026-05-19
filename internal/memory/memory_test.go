package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSaveAndSearch(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	if err := s.Save("user prefers Chinese", "preference"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.Save("project uses Go", "context"); err != nil {
		t.Fatalf("Save: %v", err)
	}

	results := s.Search("Chinese")
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}

	results = s.Search("Go")
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
}

func TestRecent(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	for i := 0; i < 10; i++ {
		s.Save(fmt.Sprintf("memory item %d", i), "test")
	}

	recent := s.Recent(3)
	if len(recent) != 3 {
		t.Errorf("expected 3, got %d", len(recent))
	}
}

func TestPersistence(t *testing.T) {
	dir := t.TempDir()

	s1, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore1: %v", err)
	}
	s1.Save("persistent memory", "test")

	// Reload
	s2, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore2: %v", err)
	}
	results := s2.Search("persistent")
	if len(results) != 1 {
		t.Errorf("expected 1 persistent result, got %d", len(results))
	}
}

func TestObsidianVaultPersistenceWritesNotes(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	if err := s.SaveWithTierAndTags("My [[Daughter]] has [[Pollen Allergy]].", "health", TierLong, 0.95, []string{"health"}); err != nil {
		t.Fatalf("SaveWithTierAndTags: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "memory.md")); !os.IsNotExist(err) {
		t.Fatalf("expected no legacy memory.md, stat err=%v", err)
	}

	matches, err := filepath.Glob(filepath.Join(dir, "50_Facts", "*.md"))
	if err != nil {
		t.Fatalf("glob notes: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected one Obsidian note, got %d: %v", len(matches), matches)
	}
	raw, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("read note: %v", err)
	}
	note := string(raw)
	for _, want := range []string{"type: memory", "tier: long", "[[Daughter]]", "[[Pollen Allergy]]", "^mem-"} {
		if !strings.Contains(note, want) {
			t.Fatalf("expected note to contain %q:\n%s", want, note)
		}
	}

	reloaded, err := NewStore(dir)
	if err != nil {
		t.Fatalf("reload store: %v", err)
	}
	results := reloaded.Search("Pollen Allergy")
	if len(results) != 1 {
		t.Fatalf("expected linked memory after reload, got %d", len(results))
	}
	if len(results[0].Links) != 2 {
		t.Fatalf("expected two parsed wikilinks, got %#v", results[0].Links)
	}
}

func TestLegacyRootMemoryFilesAreArchived(t *testing.T) {
	dir := t.TempDir()
	legacyFiles := map[string]string{
		"memory.md":   "# LuckyHarness Memory\n\n```json\n[]\n```\n",
		"memory.json": "[]",
		"memory.txt":  "old memory line\n",
	}
	for name, body := range legacyFiles {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if s.Count() != 0 {
		t.Fatalf("expected legacy files not to load as memory entries, got %d", s.Count())
	}
	for name := range legacyFiles {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("expected root legacy file %s to be archived, stat err=%v", name, err)
		}
	}
	archived, err := filepath.Glob(filepath.Join(dir, "90_Archive", "legacy-*"))
	if err != nil {
		t.Fatalf("glob archive: %v", err)
	}
	if len(archived) != len(legacyFiles) {
		t.Fatalf("expected %d archived legacy files, got %d: %v", len(legacyFiles), len(archived), archived)
	}
	readme := filepath.Join(dir, "00_Index", "LuckyHarness Memory Vault.md")
	raw, err := os.ReadFile(readme)
	if err != nil {
		t.Fatalf("read vault readme: %v", err)
	}
	if !strings.Contains(string(raw), "durable memory source of truth") || !strings.Contains(string(raw), "OBSIDIAN_VAULT_PATH is not required") {
		t.Fatalf("unexpected vault readme:\n%s", raw)
	}
}

func TestSearchPropagatesAcrossSharedWikilinks(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	if err := s.SaveWithTierAndTags("Outdoor walks often include [[Daughter]].", "plan", TierMedium, 0.6, []string{"family"}); err != nil {
		t.Fatalf("save plan: %v", err)
	}
	if err := s.SaveWithTierAndTags("[[Daughter]] has [[Pollen Allergy]].", "health", TierLong, 0.95, []string{"health"}); err != nil {
		t.Fatalf("save health: %v", err)
	}

	results := s.Search("Outdoor walks")
	if len(results) < 2 {
		t.Fatalf("expected graph propagation to add related memory, got %#v", results)
	}

	foundAllergy := false
	for _, result := range results {
		if strings.Contains(result.Content, "Pollen Allergy") {
			foundAllergy = true
			break
		}
	}
	if !foundAllergy {
		t.Fatalf("expected propagation through [[Daughter]] to recall allergy fact, got %#v", results)
	}
}

func TestSearchTokenizesNaturalChineseQuery(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	if err := s.SaveWithTierAndTags("用户的女儿对应实体是 [[Daughter]]。", "identity", TierLong, 0.9, []string{"family"}); err != nil {
		t.Fatalf("save profile: %v", err)
	}
	if err := s.SaveWithTierAndTags("[[Daughter]] 被诊断出有 [[Pollen Allergy]]。涉及 [[Outdoor Plan]]、公园、踏青、户外活动时，应考虑花粉暴露风险。", "health", TierLong, 0.98, []string{"health"}); err != nil {
		t.Fatalf("save allergy: %v", err)
	}

	results := s.Search("今天下午适合和女儿户外活动吗")
	if len(results) < 2 {
		t.Fatalf("expected natural Chinese query to recall linked memories, got %#v", results)
	}
	foundAllergy := false
	for _, result := range results {
		if strings.Contains(result.Content, "Pollen Allergy") {
			foundAllergy = true
			break
		}
	}
	if !foundAllergy {
		t.Fatalf("expected allergy memory for natural Chinese query, got %#v", results)
	}
}

func TestSearchExpandsChineseQueryToObsidianConceptAliases(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	if err := s.SaveWithTierAndTags("My [[Daughter]] has [[Pollen Allergy]].", "health", TierLong, 0.98, []string{"health"}); err != nil {
		t.Fatalf("save allergy: %v", err)
	}
	if err := s.SaveWithTierAndTags("When [[Outdoor Plan]] involves [[Daughter]] and [[Pollen Allergy]], check [[Weather Forecast]] and [[Air Quality]].", "rule", TierLong, 0.92, []string{"tool-routing"}); err != nil {
		t.Fatalf("save rule: %v", err)
	}

	results := s.Search("今天下午适合和女儿出门吗")
	if len(results) < 2 {
		t.Fatalf("expected Chinese query aliases to recall linked memories, got %#v", results)
	}
	foundAllergy := false
	foundRule := false
	for _, result := range results {
		if strings.Contains(result.Content, "Pollen Allergy") {
			foundAllergy = true
		}
		if strings.Contains(result.Content, "Weather Forecast") {
			foundRule = true
		}
	}
	if !foundAllergy || !foundRule {
		t.Fatalf("expected allergy and tool-routing memories, allergy=%v rule=%v results=%#v", foundAllergy, foundRule, results)
	}
}

func TestSaveWithMetadataPersistsAliasesAndLinks(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	err = s.SaveWithMetadata("孩子出门要考虑过敏。", "health", TierLong, 0.9, []string{"health"}, []string{"Daughter", "Pollen Allergy"}, []string{"女儿", "孩子"})
	if err != nil {
		t.Fatalf("SaveWithMetadata: %v", err)
	}

	reloaded, err := NewStore(dir)
	if err != nil {
		t.Fatalf("reload store: %v", err)
	}
	results := reloaded.Search("Daughter")
	if len(results) != 1 {
		t.Fatalf("expected linked memory after reload, got %#v", results)
	}
	foundAlias := false
	for _, alias := range results[0].Aliases {
		if alias == "女儿" {
			foundAlias = true
			break
		}
	}
	if !foundAlias {
		t.Fatalf("expected alias to persist, got %#v", results[0].Aliases)
	}
}

func TestRouteDerivesToolAndHealthConstraints(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	mustSave := func(content, category string, links []string) {
		t.Helper()
		if err := s.SaveWithMetadata(content, category, TierLong, 0.95, nil, links, nil); err != nil {
			t.Fatalf("save %s: %v", category, err)
		}
	}
	mustSave("[[Daughter]] has [[Pollen Allergy]].", "health", []string{"Daughter", "Pollen Allergy"})
	mustSave("When [[Outdoor Plan]] involves [[Daughter]] and [[Pollen Allergy]], check [[Weather Forecast]] and [[Air Quality]].", "rule", []string{"Outdoor Plan", "Daughter", "Pollen Allergy", "Weather Forecast", "Air Quality"})
	mustSave("Default family [[Outdoor Plan]] location is [[Shanghai]].", "location", []string{"Outdoor Plan", "Shanghai"})

	route := s.Route("明天下午适合和女儿出门吗")
	for _, want := range []string{"current_time", "web_search"} {
		if !stringSliceContains(route.RequiredTools, want) {
			t.Fatalf("expected required tool %q, got %#v", want, route.RequiredTools)
		}
	}
	for _, want := range []string{"pollen_allergy", "child_health_outdoor_plan"} {
		if !stringSliceContains(route.RiskFlags, want) {
			t.Fatalf("expected risk flag %q, got %#v", want, route.RiskFlags)
		}
	}
	if len(route.SuggestedSearches) == 0 || !strings.Contains(strings.Join(route.SuggestedSearches, "\n"), "Shanghai") {
		t.Fatalf("expected Shanghai suggested searches, got %#v", route.SuggestedSearches)
	}
	if len(route.EvidenceRefs) == 0 {
		t.Fatalf("expected evidence refs")
	}
}

func TestRouteTemporalResolutionPrefersLatestState(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	oldTime := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	newTime := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)

	if err := s.SaveWithOptions("[[Daughter]] has active [[Pollen Allergy]].", "health", TierLong, 0.95, SaveOptions{
		Links:      []string{"Daughter", "Pollen Allergy", "Outdoor Plan"},
		StateKey:   "family.daughter.pollen_allergy",
		StateValue: "active",
		ValidFrom:  oldTime,
	}); err != nil {
		t.Fatalf("save old state: %v", err)
	}
	oldID := ""
	for id := range s.entries {
		oldID = id
	}
	if err := s.SaveWithOptions("[[Daughter]] pollen allergy state is resolved.", "health", TierLong, 0.95, SaveOptions{
		Links:      []string{"Daughter", "Pollen Allergy", "Outdoor Plan"},
		StateKey:   "family.daughter.pollen_allergy",
		StateValue: "resolved",
		ValidFrom:  newTime,
		Supersedes: []string{oldID},
	}); err != nil {
		t.Fatalf("save new state: %v", err)
	}

	route := s.Route("明天下午适合和女儿出门吗")
	if stringSliceContains(route.RiskFlags, "pollen_allergy") {
		t.Fatalf("expected resolved pollen state not to route active allergy risk, got %#v", route.RiskFlags)
	}
	if !stringSliceContains(route.RiskFlags, "pollen_allergy_inactive_or_resolved") {
		t.Fatalf("expected inactive/resolved risk flag, got %#v", route.RiskFlags)
	}
	if len(route.SupersededRefs) == 0 || len(route.TemporalNotes) == 0 {
		t.Fatalf("expected superseded refs and temporal notes, refs=%#v notes=%#v", route.SupersededRefs, route.TemporalNotes)
	}
}

func TestRouteReportsConflictMemories(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := s.SaveWithOptions("[[Daughter]] has [[Pollen Allergy]].", "health", TierLong, 0.95, SaveOptions{
		Links:      []string{"Daughter", "Pollen Allergy"},
		StateKey:   "family.daughter.pollen_allergy",
		StateValue: "active",
	}); err != nil {
		t.Fatalf("save active state: %v", err)
	}
	if err := s.SaveWithOptions("Conflicting note about [[Daughter]] and [[Pollen Allergy]].", "health", TierLong, 0.6, SaveOptions{
		Links:      []string{"Daughter", "Pollen Allergy"},
		Status:     "conflict",
		StateKey:   "family.daughter.pollen_allergy",
		StateValue: "unknown",
	}); err != nil {
		t.Fatalf("save conflict state: %v", err)
	}

	route := s.Route("女儿花粉过敏出门")
	if len(route.ConflictRefs) == 0 {
		t.Fatalf("expected conflict refs, got route=%#v", route)
	}
	if len(route.TemporalNotes) == 0 || !strings.Contains(strings.Join(route.TemporalNotes, "\n"), "Conflict memory") {
		t.Fatalf("expected conflict temporal note, got %#v", route.TemporalNotes)
	}
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// --- v0.4.0 新测试 ---

func TestThreeTierSave(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	// 短期记忆
	if err := s.SaveShortTerm("current task: fix bug #42", "task"); err != nil {
		t.Fatalf("SaveShortTerm: %v", err)
	}

	// 中期记忆（默认）
	if err := s.Save("user likes dark mode", "preference"); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// 长期记忆
	if err := s.SaveLongTerm("project name: LuckyHarness", "identity"); err != nil {
		t.Fatalf("SaveLongTerm: %v", err)
	}

	stats := s.Stats()
	if stats[TierShort] != 1 {
		t.Errorf("expected 1 short, got %d", stats[TierShort])
	}
	if stats[TierMedium] != 1 {
		t.Errorf("expected 1 medium, got %d", stats[TierMedium])
	}
	if stats[TierLong] != 1 {
		t.Errorf("expected 1 long, got %d", stats[TierLong])
	}
}

func TestSaveWithTier(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	if err := s.SaveWithTier("high importance note", "critical", TierLong, 0.95); err != nil {
		t.Fatalf("SaveWithTier: %v", err)
	}

	results := s.Search("high importance")
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Tier != TierLong {
		t.Errorf("expected TierLong, got %v", results[0].Tier)
	}
	if results[0].Importance < 0.9 {
		t.Errorf("expected importance >= 0.9, got %f", results[0].Importance)
	}
}

func TestByTier(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	s.SaveShortTerm("short1", "test")
	s.SaveShortTerm("short2", "test")
	s.Save("medium1", "test")
	s.SaveLongTerm("long1", "test")

	shorts := s.ByTier(TierShort)
	if len(shorts) != 2 {
		t.Errorf("expected 2 short, got %d", len(shorts))
	}

	mediums := s.ByTier(TierMedium)
	if len(mediums) != 1 {
		t.Errorf("expected 1 medium, got %d", len(mediums))
	}

	longs := s.ByTier(TierLong)
	if len(longs) != 1 {
		t.Errorf("expected 1 long, got %d", len(longs))
	}
}

func TestByCategory(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	s.Save("pref1", "preference")
	s.Save("pref2", "preference")
	s.Save("ctx1", "context")

	prefs := s.ByCategory("preference")
	if len(prefs) != 2 {
		t.Errorf("expected 2 preference, got %d", len(prefs))
	}

	ctxs := s.ByCategory("context")
	if len(ctxs) != 1 {
		t.Errorf("expected 1 context, got %d", len(ctxs))
	}
}

func TestPromote(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	s.SaveShortTerm("temp note", "test")

	// 找到 ID
	shorts := s.ByTier(TierShort)
	if len(shorts) != 1 {
		t.Fatalf("expected 1 short, got %d", len(shorts))
	}
	id := shorts[0].ID

	// 提升到中期
	if err := s.Promote(id); err != nil {
		t.Fatalf("Promote: %v", err)
	}

	// 验证层级变化
	entry, err := s.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if entry.Tier != TierMedium {
		t.Errorf("expected TierMedium after promote, got %v", entry.Tier)
	}

	// 再提升到长期
	if err := s.Promote(id); err != nil {
		t.Fatalf("Promote2: %v", err)
	}
	entry, _ = s.Get(id)
	if entry.Tier != TierLong {
		t.Errorf("expected TierLong after second promote, got %v", entry.Tier)
	}

	// 长期再提升应该无操作
	if err := s.Promote(id); err != nil {
		t.Fatalf("Promote3: %v", err)
	}
	entry, _ = s.Get(id)
	if entry.Tier != TierLong {
		t.Errorf("expected still TierLong, got %v", entry.Tier)
	}
}

func TestDecay(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	// 保存一条短期记忆（低重要性）
	s.SaveWithTier("unimportant temp", "test", TierShort, 0.1)
	// 保存一条长期记忆
	s.SaveLongTerm("important core", "test")

	// 短期记忆在创建时权重 = 0.1
	// 衰减阈值 0.05 应该不会删除刚创建的
	deleted := s.Decay(0.05)
	if deleted != 0 {
		t.Errorf("expected 0 deleted (too recent), got %d", deleted)
	}

	// 手动修改创建时间模拟老化
	shorts := s.ByTier(TierShort)
	if len(shorts) != 1 {
		t.Fatalf("expected 1 short, got %d", len(shorts))
	}

	// 直接操作 entry 模拟时间流逝
	s.mu.Lock()
	for _, e := range s.entries {
		if e.Tier == TierShort {
			// 设为 10 小时前（超过短期半衰期 1h）
			e.CreatedAt = time.Now().Add(-10 * time.Hour)
		}
	}
	s.mu.Unlock()

	// 现在衰减应该删除短期记忆
	deleted = s.Decay(0.05)
	if deleted != 1 {
		t.Errorf("expected 1 deleted, got %d", deleted)
	}

	// 长期记忆不应被衰减
	stats := s.Stats()
	if stats[TierLong] != 1 {
		t.Errorf("expected 1 long (not decayed), got %d", stats[TierLong])
	}
}

func TestSummarize(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	s.Save("user likes Go", "preference")
	s.Save("user likes Rust", "preference")
	s.Save("user likes Vim", "preference")

	if s.Count() != 3 {
		t.Errorf("expected 3 entries, got %d", s.Count())
	}

	// 收集 ID
	prefs := s.ByCategory("preference")
	ids := make([]string, len(prefs))
	for i, p := range prefs {
		ids[i] = p.ID
	}

	// 压缩为摘要
	err = s.Summarize(ids, "user prefers Go, Rust, and Vim", "preference")
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}

	// 应该只剩 1 条摘要
	if s.Count() != 1 {
		t.Errorf("expected 1 entry after summarize, got %d", s.Count())
	}

	// 摘要条目应该有 SummaryOf
	all := s.ByCategory("preference")
	if len(all) != 1 {
		t.Fatalf("expected 1 preference, got %d", len(all))
	}
	if len(all[0].SummaryOf) != 3 {
		t.Errorf("expected SummaryOf 3, got %d", len(all[0].SummaryOf))
	}
}

func TestSearchWeighted(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	// 高重要性长期记忆
	s.SaveWithTier("critical: API key rotation needed", "security", TierLong, 0.95)
	// 低重要性短期记忆
	s.SaveWithTier("todo: fix typo", "task", TierShort, 0.1)

	results := s.Search("fix")
	if len(results) != 1 {
		t.Fatalf("expected 1 result for 'fix', got %d", len(results))
	}

	// 搜索 "API" 应该找到高重要性条目
	results = s.Search("API")
	if len(results) != 1 {
		t.Fatalf("expected 1 result for 'API', got %d", len(results))
	}
	if results[0].Importance < 0.9 {
		t.Errorf("expected high importance result, got %f", results[0].Importance)
	}
}

func TestAccessCount(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	s.Save("frequently accessed memory", "test")

	// 多次搜索同一内容
	for i := 0; i < 5; i++ {
		s.Search("frequently")
	}

	// 验证访问计数
	results := s.Search("frequently")
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].AccessCount < 5 {
		t.Errorf("expected access count >= 5, got %d", results[0].AccessCount)
	}
}

func TestDelete(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	s.Save("to be deleted", "test")
	if s.Count() != 1 {
		t.Errorf("expected 1, got %d", s.Count())
	}

	results := s.Search("to be deleted")
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	if err := s.Delete(results[0].ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if s.Count() != 0 {
		t.Errorf("expected 0 after delete, got %d", s.Count())
	}

	// 删除不存在的
	if err := s.Delete("nonexistent"); err == nil {
		t.Error("expected error for nonexistent delete")
	}
}

func TestGet(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	s.Save("test content", "test")
	all := s.ByCategory("test")
	if len(all) != 1 {
		t.Fatalf("expected 1, got %d", len(all))
	}

	entry, err := s.Get(all[0].ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if entry.Content != "test content" {
		t.Errorf("expected 'test content', got '%s'", entry.Content)
	}

	// 不存在的
	_, err = s.Get("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent get")
	}
}

func TestStats(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	s.SaveShortTerm("s1", "test")
	s.SaveShortTerm("s2", "test")
	s.Save("m1", "test")
	s.SaveLongTerm("l1", "test")

	stats := s.Stats()
	total := stats[TierShort] + stats[TierMedium] + stats[TierLong]
	if total != 4 {
		t.Errorf("expected total 4, got %d", total)
	}
}

func TestTierString(t *testing.T) {
	tests := []struct {
		tier   Tier
		expect string
	}{
		{TierShort, "short"},
		{TierMedium, "medium"},
		{TierLong, "long"},
		{Tier(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.tier.String(); got != tt.expect {
			t.Errorf("Tier(%d).String() = %q, want %q", tt.tier, got, tt.expect)
		}
	}
}

func TestEntryWeight(t *testing.T) {
	now := time.Now()

	// 新创建的高重要性长期记忆
	e1 := &Entry{
		Tier:        TierLong,
		Importance:  0.9,
		CreatedAt:   now,
		AccessCount: 0,
	}
	w1 := e1.Weight(now)
	if w1 < 0.8 {
		t.Errorf("high importance long-term weight too low: %f", w1)
	}

	// 很旧的低重要性短期记忆
	e2 := &Entry{
		Tier:        TierShort,
		Importance:  0.1,
		CreatedAt:   now.Add(-10 * time.Hour),
		AccessCount: 0,
	}
	w2 := e2.Weight(now)
	if w2 >= w1 {
		t.Errorf("old short-term weight should be less than new long-term: %f >= %f", w2, w1)
	}

	// 高访问次数加成
	e3 := &Entry{
		Tier:        TierMedium,
		Importance:  0.5,
		CreatedAt:   now,
		AccessCount: 10,
	}
	w3 := e3.Weight(now)
	if w3 <= 0.5 {
		t.Errorf("frequently accessed weight should have bonus: %f", w3)
	}
}

func TestOldFlatTextFormatIgnored(t *testing.T) {
	dir := t.TempDir()

	// 旧 memory.txt 不再作为事实源；Obsidian Markdown note 才是事实源。
	oldData := "old memory line 1\nold memory line 2\nold memory line 3\n"
	oldPath := dir + "/memory.txt"
	if err := os.WriteFile(oldPath, []byte(oldData), 0600); err != nil {
		t.Fatalf("write old format: %v", err)
	}

	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore with old format: %v", err)
	}

	if s.Count() != 0 {
		t.Errorf("expected old flat text to be ignored, got %d entries", s.Count())
	}
}

func TestPersistenceWithNewFields(t *testing.T) {
	dir := t.TempDir()

	s1, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore1: %v", err)
	}

	s1.SaveWithTier("tier test", "test", TierLong, 0.8)
	s1.SaveShortTerm("short test", "test")

	// Reload
	s2, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore2: %v", err)
	}

	stats := s2.Stats()
	if stats[TierLong] != 1 {
		t.Errorf("expected 1 long after reload, got %d", stats[TierLong])
	}
	if stats[TierShort] != 1 {
		t.Errorf("expected 1 short after reload, got %d", stats[TierShort])
	}

	longs := s2.ByTier(TierLong)
	if len(longs) != 1 {
		t.Fatalf("expected 1 long, got %d", len(longs))
	}
	if longs[0].Importance < 0.7 {
		t.Errorf("expected importance preserved, got %f", longs[0].Importance)
	}
}

func TestLoadObsidianNoteWithEmbeddedCodeFenceText(t *testing.T) {
	dir := t.TempDir()
	content := `---
id: mem_1_1
type: memory
tier: medium
category: conversation
importance: 0.2
access_count: 0
created_at: 2026-04-30T00:00:00Z
accessed_at: 2026-04-30T00:00:00Z
status: active
valid_from: 2026-04-30T00:00:00Z
block_id: mem-1-1
---

# Tool Protocol Memory

## Memory

Assistant: ` + "```tool" + `
{"name":"cron_list"}
` + "```" + `

^mem-1-1
`
	noteDir := filepath.Join(dir, "30_Sessions")
	if err := os.MkdirAll(noteDir, 0700); err != nil {
		t.Fatalf("mkdir note dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(noteDir, "tool-protocol.md"), []byte(content), 0600); err != nil {
		t.Fatalf("write obsidian note: %v", err)
	}

	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if s.Count() != 1 {
		t.Fatalf("expected 1 entry, got %d", s.Count())
	}
	got := s.Recent(1)
	if len(got) != 1 || !strings.Contains(got[0].Content, "cron_list") {
		t.Fatalf("unexpected content: %#v", got)
	}
}

// --- v0.42.0 新测试：去重、TTL、过期清理 ---

func TestDedup(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	// 写入 5 条完全相同的记忆
	for i := 0; i < 5; i++ {
		s.Save("duplicate content", "test")
	}

	if s.Count() != 1 {
		t.Errorf("expected 1 after dedup save, got %d", s.Count())
	}

	// 不同 category 的相同内容应该共存
	s.Save("duplicate content", "other")
	if s.Count() != 2 {
		t.Errorf("expected 2 (different category), got %d", s.Count())
	}
}

func TestDedupExisting(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	// 手动写入重复数据（模拟旧数据）
	s.mu.Lock()
	for i := 0; i < 10; i++ {
		entry := &Entry{
			ID:         fmt.Sprintf("mem_dup_%d", i),
			Content:    "same content",
			Category:   "test",
			Tier:       TierMedium,
			Importance: 0.5,
			CreatedAt:  time.Now(),
			AccessedAt: time.Now(),
		}
		s.entries[entry.ID] = entry
	}
	s.mu.Unlock()

	if s.Count() != 10 {
		t.Fatalf("expected 10 before dedup, got %d", s.Count())
	}

	removed := s.Dedup()
	if removed != 9 {
		t.Errorf("expected 9 removed, got %d", removed)
	}
	if s.Count() != 1 {
		t.Errorf("expected 1 after dedup, got %d", s.Count())
	}
}

func TestPurgeCategory(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	s.Save("test1", "test")
	s.Save("test2", "test")
	s.Save("keep me", "important")

	removed := s.PurgeCategory("test")
	if removed != 2 {
		t.Errorf("expected 2 removed, got %d", removed)
	}
	if s.Count() != 1 {
		t.Errorf("expected 1 remaining, got %d", s.Count())
	}
}

func TestExpire(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	// 保存一条短期记忆，TTL 1 毫秒
	if err := s.SaveShortTermTTL("will expire", "test", 1*time.Millisecond); err != nil {
		t.Fatalf("SaveShortTermTTL: %v", err)
	}

	// 保存一条不过期的记忆
	s.Save("will stay", "test")

	if s.Count() != 2 {
		t.Fatalf("expected 2 before expire, got %d", s.Count())
	}

	// 等待过期
	time.Sleep(10 * time.Millisecond)

	expired := s.Expire()
	if expired != 1 {
		t.Errorf("expected 1 expired, got %d", expired)
	}
	if s.Count() != 1 {
		t.Errorf("expected 1 after expire, got %d", s.Count())
	}
}

func TestSaveWithTierDedupUpgrade(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	// 先存一条中期
	s.SaveWithTier("important fact", "knowledge", TierMedium, 0.5)

	// 再存同内容但更高层级
	s.SaveWithTier("important fact", "knowledge", TierLong, 0.9)

	if s.Count() != 1 {
		t.Errorf("expected 1 (dedup), got %d", s.Count())
	}

	// 应该被提升到长期
	longs := s.ByTier(TierLong)
	if len(longs) != 1 {
		t.Errorf("expected 1 long (upgraded), got %d", len(longs))
	}
	if longs[0].Importance < 0.9 {
		t.Errorf("expected importance 0.9, got %f", longs[0].Importance)
	}
}

func TestMergeTags(t *testing.T) {
	existing := []string{"go", "rust"}
	newTags := []string{"Rust", "python"}
	result := mergeTags(existing, newTags)

	if len(result) != 3 {
		t.Errorf("expected 3 tags, got %d: %v", len(result), result)
	}
}
