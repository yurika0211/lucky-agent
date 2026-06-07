package main

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yurika0211/luckyharness/internal/memory"
	"gopkg.in/yaml.v3"
)

type benchDataset struct {
	Name    string
	Dir     string
	Size    int
	Store   *memory.Store
	Queries []benchQuery
}

type benchQuery struct {
	ID            string
	Scenario      string
	Text          string
	WantIDs       []string
	ForbidIDs     []string
	WantRiskFlags []string
	WantTools     []string
}

type diskMemoryNote struct {
	ID          string     `yaml:"id"`
	Type        string     `yaml:"type"`
	Tier        string     `yaml:"tier"`
	Category    string     `yaml:"category"`
	Importance  float64    `yaml:"importance"`
	AccessCount int        `yaml:"access_count"`
	CreatedAt   time.Time  `yaml:"created_at"`
	AccessedAt  time.Time  `yaml:"accessed_at"`
	Tags        []string   `yaml:"tags,omitempty"`
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
	Content     string     `yaml:"-"`
}

func loadBenchmarkDataset(cfg benchConfig) (*benchDataset, func(), error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Dataset)) {
	case "synthetic", "":
		return loadSyntheticDataset(cfg)
	case "real":
		return loadRealDataset(cfg)
	default:
		return nil, nil, fmt.Errorf("unknown dataset %q", cfg.Dataset)
	}
}

func loadSyntheticDataset(cfg benchConfig) (*benchDataset, func(), error) {
	dir, cleanup, err := syntheticMemoryDir(cfg)
	if err != nil {
		return nil, nil, err
	}
	notes := syntheticNotes(cfg)
	if err := writeMemoryNotes(dir, notes); err != nil {
		cleanup()
		return nil, nil, err
	}
	store, err := memory.NewStore(dir)
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("load synthetic memory store: %w", err)
	}
	ds := &benchDataset{
		Name:    "synthetic",
		Dir:     dir,
		Size:    len(notes),
		Store:   store,
		Queries: syntheticQueries(),
	}
	return ds, cleanup, nil
}

func syntheticMemoryDir(cfg benchConfig) (string, func(), error) {
	if strings.TrimSpace(cfg.MemoryDir) != "" {
		dir := filepath.Clean(cfg.MemoryDir)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", nil, fmt.Errorf("create synthetic memory dir: %w", err)
		}
		empty, err := memoryDirIsEmpty(dir)
		if err != nil {
			return "", nil, err
		}
		if !empty {
			return "", nil, fmt.Errorf("refusing to generate synthetic dataset into non-empty dir %s", dir)
		}
		return dir, func() {}, nil
	}
	dir, err := os.MkdirTemp("", "lh-memory-bench-*")
	if err != nil {
		return "", nil, fmt.Errorf("create synthetic memory dir: %w", err)
	}
	cleanup := func() {
		if !cfg.KeepDataset {
			_ = os.RemoveAll(dir)
		}
	}
	return dir, cleanup, nil
}

func memoryDirIsEmpty(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, fmt.Errorf("read memory dir: %w", err)
	}
	return len(entries) == 0, nil
}

func loadRealDataset(cfg benchConfig) (*benchDataset, func(), error) {
	dir := strings.TrimSpace(cfg.MemoryDir)
	if dir == "" {
		return nil, nil, fmt.Errorf("memory-dir is required for dataset=real")
	}
	store, err := memory.NewStore(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("load real memory store: %w", err)
	}
	name := strings.TrimSpace(cfg.RealDatasetTag)
	if name == "" {
		name = "real"
	}
	ds := &benchDataset{
		Name:    name,
		Dir:     dir,
		Size:    store.Count(),
		Store:   store,
		Queries: realDatasetQueries(),
	}
	return ds, func() {}, nil
}

func (ds *benchDataset) QueriesForScenario(scenario, override string) []benchQuery {
	var queries []benchQuery
	for _, query := range ds.Queries {
		if query.Scenario == scenario {
			queries = append(queries, query)
		}
	}
	if strings.TrimSpace(override) == "" {
		return queries
	}
	text := strings.TrimSpace(override)
	if len(queries) == 0 {
		return []benchQuery{{ID: "override", Scenario: scenario, Text: text}}
	}
	query := queries[0]
	query.ID = query.ID + "_override"
	query.Text = text
	return []benchQuery{query}
}

func syntheticNotes(cfg benchConfig) []diskMemoryNote {
	now := time.Now().UTC().Truncate(time.Second)
	past := now.Add(-24 * time.Hour)
	future := now.Add(24 * time.Hour)
	expired := now.Add(-2 * time.Hour)
	notes := []diskMemoryNote{
		{
			ID:         "mem_bench_telegram",
			Tier:       "long",
			Category:   "preference",
			Importance: 0.92,
			Tags:       []string{"gateway", "telegram"},
			Aliases:    []string{"telegram channel", "Telegram Gateway"},
			Content:    "User prefers [[Telegram Gateway]] for message delivery and status updates.",
		},
		{
			ID:         "mem_bench_outdoor_plan",
			Tier:       "medium",
			Category:   "plan",
			Importance: 0.72,
			Tags:       []string{"family", "outdoor"},
			Links:      []string{"Daughter"},
			Aliases:    []string{"outdoor plan", "户外计划"},
			Content:    "Outdoor walks often include [[Daughter]] and nearby parks.",
		},
		{
			ID:         "mem_bench_daughter",
			Tier:       "long",
			Category:   "profile",
			Importance: 0.95,
			Tags:       []string{"family", "child"},
			Links:      []string{"Pollen Allergy"},
			Aliases:    []string{"Daughter", "女儿", "child"},
			Content:    "[[Daughter]] is the user's child and should be considered in family plans.",
		},
		{
			ID:         "mem_bench_pollen_allergy",
			Tier:       "long",
			Category:   "health",
			Importance: 0.98,
			Tags:       []string{"health", "allergy"},
			Links:      []string{"Daughter", "Weather", "Air Quality"},
			Aliases:    []string{"Pollen Allergy", "花粉过敏", "hay fever"},
			StateKey:   "daughter.pollen_allergy",
			StateValue: "active",
			Confidence: 0.95,
			Content:    "[[Daughter]] has active [[Pollen Allergy]] risk during outdoor exposure.",
		},
		{
			ID:         "mem_bench_weather",
			Tier:       "medium",
			Category:   "weather",
			Importance: 0.70,
			Tags:       []string{"weather", "outdoor"},
			Aliases:    []string{"Weather", "weather forecast", "天气"},
			Content:    "[[Weather]] and wind conditions should be checked before outdoor plans.",
		},
		{
			ID:         "mem_bench_air_quality",
			Tier:       "medium",
			Category:   "environment",
			Importance: 0.68,
			Tags:       []string{"aqi", "health"},
			Aliases:    []string{"Air Quality", "AQI", "空气质量"},
			Content:    "[[Air Quality]] and PM2.5 can affect outdoor health risk.",
		},
		{
			ID:         "mem_bench_scale_anchor",
			Tier:       "medium",
			Category:   "benchmark",
			Importance: 0.80,
			Tags:       []string{"scale", "benchmark"},
			Aliases:    []string{"scale benchmark anchor"},
			Content:    "Scale benchmark anchor memory for deterministic latency measurement.",
		},
		{
			ID:         "mem_bench_old_allergy",
			Tier:       "long",
			Category:   "health",
			Importance: 0.90,
			Tags:       []string{"health", "allergy"},
			Status:     "superseded",
			Aliases:    []string{"old pollen allergy"},
			StateKey:   "daughter.pollen_allergy",
			StateValue: "inactive",
			Confidence: 0.4,
			Supersedes: []string{"mem_bench_pollen_allergy_legacy"},
			Content:    "Superseded note saying pollen allergy is inactive.",
		},
		{
			ID:         "mem_bench_expired_location",
			Tier:       "medium",
			Category:   "location",
			Importance: 0.60,
			Tags:       []string{"location"},
			ExpiresAt:  &expired,
			Aliases:    []string{"old location"},
			Content:    "Expired memory: family outdoor plans were in Beijing.",
		},
		{
			ID:         "mem_bench_future_location",
			Tier:       "medium",
			Category:   "location",
			Importance: 0.60,
			Tags:       []string{"location"},
			ValidFrom:  future,
			Aliases:    []string{"future location"},
			Content:    "Future memory: family outdoor plans will be in Hangzhou.",
		},
	}

	rng := rand.New(rand.NewSource(cfg.Seed))
	target := cfg.Size
	if target < len(notes) {
		target = len(notes)
	}
	templates := []struct {
		category string
		tier     string
		tags     []string
		content  string
	}{
		{"project", "medium", []string{"project", "code"}, "Project note %04d records a routine LuckyHarness implementation detail about module boundaries."},
		{"preference", "long", []string{"preference"}, "Preference note %04d captures a harmless UI or workflow preference unrelated to family health."},
		{"tool", "medium", []string{"tool"}, "Tool note %04d explains a command-line helper and expected output shape."},
		{"session", "short", []string{"session"}, "Session note %04d summarizes an old conversation turn without benchmark relevance."},
		{"fact", "medium", []string{"reference"}, "Reference note %04d contains neutral background text for retrieval noise measurement."},
	}
	for i := len(notes); i < target; i++ {
		tpl := templates[rng.Intn(len(templates))]
		id := fmt.Sprintf("mem_bench_noise_%06d", i)
		importance := 0.25 + rng.Float64()*0.45
		notes = append(notes, diskMemoryNote{
			ID:          id,
			Tier:        tpl.tier,
			Category:    tpl.category,
			Importance:  importance,
			AccessCount: rng.Intn(8),
			Tags:        append([]string(nil), tpl.tags...),
			Aliases:     []string{fmt.Sprintf("noise alias %04d", i)},
			Content:     fmt.Sprintf(tpl.content, i),
		})
	}
	for i := range notes {
		if notes[i].Type == "" {
			notes[i].Type = "memory"
		}
		if notes[i].Status == "" {
			notes[i].Status = "active"
		}
		if notes[i].CreatedAt.IsZero() {
			notes[i].CreatedAt = past.Add(time.Duration(i) * time.Second)
		}
		if notes[i].AccessedAt.IsZero() {
			notes[i].AccessedAt = notes[i].CreatedAt
		}
		if notes[i].ValidFrom.IsZero() {
			notes[i].ValidFrom = notes[i].CreatedAt
		}
		if notes[i].BlockID == "" {
			notes[i].BlockID = strings.ReplaceAll(notes[i].ID, "_", "-")
		}
	}
	return notes
}

func syntheticQueries() []benchQuery {
	return []benchQuery{
		{
			ID:        "telegram_alias",
			Scenario:  "lexical",
			Text:      "telegram channel",
			WantIDs:   []string{"mem_bench_telegram"},
			ForbidIDs: []string{"mem_bench_old_allergy", "mem_bench_expired_location", "mem_bench_future_location"},
		},
		{
			ID:        "family_outdoor_graph",
			Scenario:  "graph",
			Text:      "nearby parks",
			WantIDs:   []string{"mem_bench_outdoor_plan", "mem_bench_daughter", "mem_bench_pollen_allergy"},
			ForbidIDs: []string{"mem_bench_old_allergy", "mem_bench_expired_location", "mem_bench_future_location"},
		},
		{
			ID:        "allergy_temporal",
			Scenario:  "temporal",
			Text:      "女儿花粉过敏出门",
			WantIDs:   []string{"mem_bench_daughter", "mem_bench_pollen_allergy", "mem_bench_outdoor_plan"},
			ForbidIDs: []string{"mem_bench_old_allergy", "mem_bench_expired_location", "mem_bench_future_location"},
		},
		{
			ID:        "scale_anchor",
			Scenario:  "scale",
			Text:      "scale benchmark anchor",
			ForbidIDs: []string{"mem_bench_old_allergy", "mem_bench_expired_location", "mem_bench_future_location"},
		},
		{
			ID:            "route_family_outdoor",
			Scenario:      "route",
			Text:          "明天下午适合和女儿出门吗",
			WantIDs:       []string{"mem_bench_outdoor_plan", "mem_bench_daughter", "mem_bench_pollen_allergy"},
			ForbidIDs:     []string{"mem_bench_old_allergy", "mem_bench_expired_location", "mem_bench_future_location"},
			WantRiskFlags: []string{"child_health_outdoor_plan", "pollen_allergy", "outdoor_exposure", "child_or_family_context"},
			WantTools:     []string{"current_time", "web_search"},
		},
	}
}

func realDatasetQueries() []benchQuery {
	return []benchQuery{
		{ID: "real_lexical", Scenario: "lexical", Text: "memory benchmark"},
		{ID: "real_graph", Scenario: "graph", Text: "女儿户外活动"},
		{ID: "real_temporal", Scenario: "temporal", Text: "当前状态"},
		{ID: "real_scale", Scenario: "scale", Text: "benchmark"},
		{ID: "real_route", Scenario: "route", Text: "明天下午适合和女儿出门吗"},
	}
}

func writeMemoryNotes(root string, notes []diskMemoryNote) error {
	for _, note := range notes {
		if err := writeMemoryNote(root, note); err != nil {
			return err
		}
	}
	return nil
}

func writeMemoryNote(root string, note diskMemoryNote) error {
	dir := noteDirForCategory(note.Category, note.Tier)
	path := filepath.Join(root, dir, note.ID+".md")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create note dir: %w", err)
	}
	yml, err := yaml.Marshal(note)
	if err != nil {
		return fmt.Errorf("marshal note frontmatter: %w", err)
	}
	title := strings.TrimSpace(stripInlineWiki(note.Content))
	if title == "" {
		title = note.ID
	}
	if len([]rune(title)) > 80 {
		title = string([]rune(title)[:80])
	}
	var b strings.Builder
	b.WriteString("---\n")
	b.Write(yml)
	b.WriteString("---\n\n")
	b.WriteString("# ")
	b.WriteString(title)
	b.WriteString("\n\n## Memory\n\n")
	b.WriteString(strings.TrimSpace(note.Content))
	b.WriteString("\n\n^")
	b.WriteString(note.BlockID)
	b.WriteString("\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("write note %s: %w", path, err)
	}
	return nil
}

func noteDirForCategory(category, tier string) string {
	switch strings.ToLower(strings.TrimSpace(category)) {
	case "profile", "preference":
		return "10_Profile"
	case "project":
		return "20_Projects"
	case "session":
		return "30_Sessions"
	case "decision", "architecture":
		return "40_Decisions"
	case "rule", "tool":
		return "60_Rules"
	default:
		if strings.EqualFold(strings.TrimSpace(tier), "long") {
			return "50_Facts"
		}
		return "50_Facts"
	}
}

func stripInlineWiki(s string) string {
	s = strings.ReplaceAll(s, "[[", "")
	s = strings.ReplaceAll(s, "]]", "")
	return s
}
