package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/yurika0211/luckyagent/internal/embedder"
	"github.com/yurika0211/luckyagent/internal/rag"
)

type benchConfig struct {
	Variant    string
	Scenario   string
	OutPath    string
	Rounds     int
	Limit      int
	NoiseDocs  int
	MinRecall  float64
	MaxNoise   float64
	PrintJSONL bool
}

type benchDoc struct {
	Source  string
	Title   string
	Content string
}

type benchQuery struct {
	ID              string
	Scenario        string
	Text            string
	ExpectedSources []string
	ExpectedNodes   []string
	ExpectedRels    []string
}

type benchRecord struct {
	Type            string    `json:"type"`
	Variant         string    `json:"variant"`
	Scenario        string    `json:"scenario"`
	Mode            string    `json:"mode"`
	Round           int       `json:"round"`
	QueryID         string    `json:"query_id"`
	Query           string    `json:"query"`
	StartedAt       time.Time `json:"started_at"`
	DurationNS      int64     `json:"duration_ns"`
	DurationMS      float64   `json:"duration_ms"`
	Limit           int       `json:"limit"`
	ResultSources   []string  `json:"result_sources,omitempty"`
	ResultNodes     []string  `json:"result_nodes,omitempty"`
	ResultRels      []string  `json:"result_rels,omitempty"`
	ExpectedSources []string  `json:"expected_sources,omitempty"`
	ExpectedNodes   []string  `json:"expected_nodes,omitempty"`
	ExpectedRels    []string  `json:"expected_rels,omitempty"`
	SourceRecall    float64   `json:"source_recall"`
	SourcePrecision float64   `json:"source_precision"`
	SourceNoise     float64   `json:"source_noise"`
	NodeRecall      float64   `json:"node_recall"`
	NodePrecision   float64   `json:"node_precision"`
	NodeNoise       float64   `json:"node_noise"`
	RelRecall       float64   `json:"rel_recall"`
	Clean           bool      `json:"clean"`
	QualityPass     bool      `json:"quality_pass"`
	Error           string    `json:"error,omitempty"`
}

type benchSummary struct {
	Type                    string   `json:"type"`
	Variant                 string   `json:"variant"`
	Scenario                string   `json:"scenario"`
	Records                 int      `json:"records"`
	Errors                  int      `json:"errors"`
	Clean                   bool     `json:"clean"`
	QualityPass             bool     `json:"quality_pass"`
	MinRecall               float64  `json:"min_recall"`
	MaxNoise                float64  `json:"max_noise"`
	AvgVectorSourceRecall   float64  `json:"avg_vector_source_recall"`
	AvgGraphSourceRecall    float64  `json:"avg_graph_source_recall"`
	AvgGraphNodeRecall      float64  `json:"avg_graph_node_recall"`
	AvgGraphRelRecall       float64  `json:"avg_graph_rel_recall"`
	AvgVectorDurationNS     float64  `json:"avg_vector_duration_ns"`
	AvgGraphDurationNS      float64  `json:"avg_graph_duration_ns"`
	P95VectorDurationNS     int64    `json:"p95_vector_duration_ns"`
	P95GraphDurationNS      int64    `json:"p95_graph_duration_ns"`
	GraphNodeRecallLift     float64  `json:"graph_node_recall_lift"`
	GraphSourceRecallLift   float64  `json:"graph_source_recall_lift"`
	GraphLatencyOverheadPct float64  `json:"graph_latency_overhead_pct"`
	Scenarios               []string `json:"scenarios,omitempty"`
}

type setMetrics struct {
	Recall    float64
	Precision float64
	Noise     float64
}

type ruleLLMProvider struct{}

func (ruleLLMProvider) Complete(ctx context.Context, prompt string) (string, error) {
	lower := strings.ToLower(prompt)
	switch {
	case strings.Contains(lower, "lin works at orion labs"):
		return extractionJSON(
			[]entity{
				{"Lin", "person", "Lead engineer at Orion Labs", []string{"Lin Chen"}},
				{"Orion Labs", "organization", "Robotics research company", []string{"Orion"}},
				{"Project Helios", "concept", "Autonomous solar rover project", []string{"Helios"}},
			},
			[]relation{
				{"Lin", "Orion Labs", "works_at", "Lin works at Orion Labs"},
				{"Lin", "Project Helios", "related_to", "Lin leads Project Helios"},
				{"Project Helios", "Orion Labs", "part_of", "Project Helios belongs to Orion Labs"},
			},
		), nil
	case strings.Contains(lower, "orion labs is located in neo shanghai"):
		return extractionJSON(
			[]entity{
				{"Orion Labs", "organization", "Robotics research company", []string{"Orion"}},
				{"Neo Shanghai", "location", "City hosting Orion Labs", []string{"NeoShanghai"}},
				{"SkyNet Consortium", "organization", "Parent consortium of Orion Labs", []string{"SkyNet"}},
			},
			[]relation{
				{"Orion Labs", "Neo Shanghai", "located_in", "Orion Labs is located in Neo Shanghai"},
				{"Orion Labs", "SkyNet Consortium", "part_of", "Orion Labs is part of SkyNet Consortium"},
				{"SkyNet Consortium", "Neo Shanghai", "located_in", "SkyNet Consortium headquarters are in Neo Shanghai"},
			},
		), nil
	case strings.Contains(lower, "project helios depends on solar index"):
		return extractionJSON(
			[]entity{
				{"Project Helios", "concept", "Autonomous solar rover project", []string{"Helios"}},
				{"Solar Index", "concept", "Energy forecast used by Helios", []string{"SolarIndex"}},
				{"Orion Labs", "organization", "Robotics research company", []string{"Orion"}},
			},
			[]relation{
				{"Project Helios", "Solar Index", "related_to", "Helios depends on Solar Index"},
				{"Project Helios", "Orion Labs", "part_of", "Helios is part of Orion Labs"},
			},
		), nil
	case strings.Contains(lower, "mira works at quanta health"):
		return extractionJSON(
			[]entity{
				{"Mira", "person", "Researcher at Quanta Health", []string{"Mira Rao"}},
				{"Quanta Health", "organization", "Health organization in Harbor City", []string{"Quanta"}},
				{"Harbor City", "location", "Coastal city", nil},
				{"Pollen Allergy", "concept", "Outdoor allergy risk", []string{"Allergy"}},
			},
			[]relation{
				{"Mira", "Quanta Health", "works_at", "Mira works at Quanta Health"},
				{"Quanta Health", "Harbor City", "located_in", "Quanta Health is located in Harbor City"},
				{"Mira", "Pollen Allergy", "related_to", "Mira studies pollen allergy"},
			},
		), nil
	default:
		return `{"entities":[],"relations":[]}`, nil
	}
}

type entity struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Description string   `json:"description"`
	Aliases     []string `json:"aliases,omitempty"`
}

type relation struct {
	Source  string `json:"source"`
	Target  string `json:"target"`
	Type    string `json:"type"`
	Context string `json:"context"`
}

func extractionJSON(entities []entity, relations []relation) string {
	payload := struct {
		Entities  []entity   `json:"entities"`
		Relations []relation `json:"relations"`
	}{Entities: entities, Relations: relations}
	data, _ := json.Marshal(payload)
	return string(data)
}

func main() {
	cfg := parseFlags()
	if err := run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "la-graph-rag-bench: %v\n", err)
		os.Exit(1)
	}
}

func parseFlags() benchConfig {
	now := time.Now().Format("20060102-150405")
	defaultOut := filepath.Join(os.TempDir(), "lh-graph-rag-bench", "results-"+now+".jsonl")
	cfg := benchConfig{}
	flag.StringVar(&cfg.Variant, "variant", "manual", "label written to every record")
	flag.StringVar(&cfg.Scenario, "scenario", "all", "scenario: direct, bridge, multihop, distractor, or all")
	flag.StringVar(&cfg.OutPath, "out", defaultOut, "JSONL output path")
	flag.IntVar(&cfg.Rounds, "rounds", 5, "rounds per query")
	flag.IntVar(&cfg.Limit, "limit", 5, "top-k limit for metrics")
	flag.IntVar(&cfg.NoiseDocs, "noise-docs", 200, "number of irrelevant synthetic documents")
	flag.Float64Var(&cfg.MinRecall, "min-recall", 0.75, "quality threshold for graph node/source recall")
	flag.Float64Var(&cfg.MaxNoise, "max-noise", 0.60, "quality threshold for graph node/source noise")
	flag.BoolVar(&cfg.PrintJSONL, "print-jsonl", false, "also print raw JSONL records to stdout")
	flag.Parse()
	return cfg
}

func run(cfg benchConfig) error {
	if cfg.Rounds <= 0 {
		return fmt.Errorf("rounds must be positive")
	}
	if cfg.Limit <= 0 {
		return fmt.Errorf("limit must be positive")
	}
	if cfg.NoiseDocs < 0 {
		return fmt.Errorf("noise-docs must be non-negative")
	}

	if err := os.MkdirAll(filepath.Dir(cfg.OutPath), 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}
	out, err := os.Create(cfg.OutPath)
	if err != nil {
		return fmt.Errorf("create output: %w", err)
	}
	defer out.Close()

	enc := json.NewEncoder(out)
	ctx := context.Background()
	manager, err := buildBenchmarkManager(ctx, cfg.NoiseDocs)
	if err != nil {
		return err
	}
	defer manager.CloseStore()

	scenarios, err := expandScenarios(cfg.Scenario)
	if err != nil {
		return err
	}

	var all []benchRecord
	for _, scenario := range scenarios {
		records, err := runScenario(ctx, cfg, manager, scenario, enc)
		if err != nil {
			return err
		}
		all = append(all, records...)
		summary := summarizeRecords(cfg, scenario, records)
		if err := enc.Encode(summary); err != nil {
			return fmt.Errorf("write summary: %w", err)
		}
		printSummary(summary)
		if cfg.PrintJSONL {
			printAsJSON(summary)
		}
	}

	if len(scenarios) > 1 {
		summary := summarizeRecords(cfg, "all", all)
		summary.Scenarios = scenarios
		if err := enc.Encode(summary); err != nil {
			return fmt.Errorf("write all summary: %w", err)
		}
		printSummary(summary)
		if cfg.PrintJSONL {
			printAsJSON(summary)
		}
	}

	fmt.Fprintf(os.Stderr, "results: %s\n", cfg.OutPath)
	return nil
}

func buildBenchmarkManager(ctx context.Context, noiseDocs int) (*rag.RAGManager, error) {
	cfg := rag.DefaultRAGConfig()
	cfg.EnableGraph = true
	cfg.RetrieverConfig.TopK = 8
	cfg.RetrieverConfig.MinScore = 0.01
	cfg.RetrieverConfig.UseMMR = true

	manager := rag.NewRAGManagerWithGraph(embedder.NewMockEmbedder(128), cfg, ruleLLMProvider{})
	for _, doc := range benchmarkDocs(noiseDocs) {
		if _, err := manager.IndexTextWithGraph(ctx, doc.Source, doc.Title, doc.Content); err != nil {
			return nil, fmt.Errorf("index %s: %w", doc.Source, err)
		}
	}
	return manager, nil
}

func benchmarkDocs(noiseDocs int) []benchDoc {
	docs := []benchDoc{
		{
			Source:  "people/lin.md",
			Title:   "Lin profile",
			Content: "Lin works at Orion Labs. Lin leads Project Helios. Project Helios belongs to Orion Labs.",
		},
		{
			Source:  "org/orion.md",
			Title:   "Orion Labs",
			Content: "Orion Labs is located in Neo Shanghai. Orion Labs is part of SkyNet Consortium. SkyNet Consortium headquarters are in Neo Shanghai.",
		},
		{
			Source:  "project/helios.md",
			Title:   "Project Helios",
			Content: "Project Helios depends on Solar Index. Project Helios is part of Orion Labs. Solar Index forecasts energy for autonomous rover planning.",
		},
		{
			Source:  "health/mira.md",
			Title:   "Mira and Quanta Health",
			Content: "Mira works at Quanta Health. Quanta Health is located in Harbor City. Mira studies pollen allergy and outdoor exposure.",
		},
	}
	for i := 0; i < noiseDocs; i++ {
		docs = append(docs, benchDoc{
			Source:  fmt.Sprintf("noise/archive-%04d.md", i),
			Title:   fmt.Sprintf("Archive Topic %04d", i),
			Content: fmt.Sprintf("Archive Topic %04d contains unrelated notes about catalog shelves, meeting rooms, and generic project status markers.", i),
		})
	}
	return docs
}

func benchmarkQueries() []benchQuery {
	return []benchQuery{
		{
			ID:              "direct_lin",
			Scenario:        "direct",
			Text:            "Lin profile",
			ExpectedSources: []string{"people/lin.md"},
			ExpectedNodes:   []string{"Lin", "Orion Labs", "Project Helios"},
			ExpectedRels:    []string{"works_at", "related_to", "part_of"},
		},
		{
			ID:              "lin_employer_city",
			Scenario:        "bridge",
			Text:            "Lin employer city",
			ExpectedSources: []string{"people/lin.md", "org/orion.md"},
			ExpectedNodes:   []string{"Lin", "Orion Labs", "Neo Shanghai"},
			ExpectedRels:    []string{"works_at", "located_in"},
		},
		{
			ID:              "helios_parent_city",
			Scenario:        "multihop",
			Text:            "Project Helios parent city",
			ExpectedSources: []string{"project/helios.md", "org/orion.md"},
			ExpectedNodes:   []string{"Project Helios", "Orion Labs", "Solar Index"},
			ExpectedRels:    []string{"part_of", "located_in"},
		},
		{
			ID:              "mira_clinic_city_allergy",
			Scenario:        "multihop",
			Text:            "Mira health harbor allergy",
			ExpectedSources: []string{"health/mira.md"},
			ExpectedNodes:   []string{"Mira", "Quanta Health", "Harbor City", "Pollen Allergy"},
			ExpectedRels:    []string{"works_at", "located_in", "related_to"},
		},
		{
			ID:              "distractor_orion_solar",
			Scenario:        "distractor",
			Text:            "Orion Solar Index",
			ExpectedSources: []string{"org/orion.md", "project/helios.md"},
			ExpectedNodes:   []string{"Orion Labs", "Project Helios", "Solar Index"},
			ExpectedRels:    []string{"part_of", "related_to"},
		},
	}
}

func expandScenarios(raw string) ([]string, error) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" || raw == "all" {
		return []string{"direct", "bridge", "multihop", "distractor"}, nil
	}
	switch raw {
	case "direct", "bridge", "multihop", "distractor":
		return []string{raw}, nil
	default:
		return nil, fmt.Errorf("unknown scenario %q", raw)
	}
}

func queriesForScenario(scenario string) []benchQuery {
	var out []benchQuery
	for _, q := range benchmarkQueries() {
		if q.Scenario == scenario {
			out = append(out, q)
		}
	}
	return out
}

func runScenario(ctx context.Context, cfg benchConfig, manager *rag.RAGManager, scenario string, enc *json.Encoder) ([]benchRecord, error) {
	queries := queriesForScenario(scenario)
	if len(queries) == 0 {
		return nil, fmt.Errorf("no queries for scenario %s", scenario)
	}

	records := make([]benchRecord, 0, cfg.Rounds*len(queries)*2)
	for round := 1; round <= cfg.Rounds; round++ {
		for _, query := range queries {
			vectorRecord := runVectorRecord(ctx, cfg, manager, scenario, round, query)
			graphRecord := runGraphRecord(ctx, cfg, manager, scenario, round, query)
			for _, record := range []benchRecord{vectorRecord, graphRecord} {
				if err := enc.Encode(record); err != nil {
					return nil, fmt.Errorf("write record: %w", err)
				}
				printRecord(record)
				if cfg.PrintJSONL {
					printAsJSON(record)
				}
				records = append(records, record)
			}
		}
	}
	return records, nil
}

func runVectorRecord(ctx context.Context, cfg benchConfig, manager *rag.RAGManager, scenario string, round int, query benchQuery) benchRecord {
	start := time.Now()
	results, err := manager.Search(ctx, query.Text)
	duration := time.Since(start)
	record := baseRecord(cfg, scenario, "vector", round, query, start, duration)
	if err != nil {
		record.Error = err.Error()
		record.Clean = false
		record.QualityPass = false
		return record
	}
	record.ResultSources = uniqueStrings(retrievalSources(results, cfg.Limit))
	fillMetrics(&record, cfg)
	return record
}

func runGraphRecord(ctx context.Context, cfg benchConfig, manager *rag.RAGManager, scenario string, round int, query benchQuery) benchRecord {
	start := time.Now()
	result, err := manager.SearchWithGraph(ctx, query.Text)
	duration := time.Since(start)
	record := baseRecord(cfg, scenario, "graph", round, query, start, duration)
	if err != nil {
		record.Error = err.Error()
		record.Clean = false
		record.QualityPass = false
		return record
	}
	record.ResultSources = uniqueStrings(retrievalSources(result.ChunkResults, cfg.Limit))
	record.ResultNodes = uniqueStrings(activationNodeNames(result.ActivatedNodes, cfg.Limit))
	record.ResultRels = uniqueStrings(activationRelationTypes(result.ActivatedNodes, cfg.Limit))
	fillMetrics(&record, cfg)
	return record
}

func baseRecord(cfg benchConfig, scenario, mode string, round int, query benchQuery, started time.Time, duration time.Duration) benchRecord {
	return benchRecord{
		Type:            "round",
		Variant:         cfg.Variant,
		Scenario:        scenario,
		Mode:            mode,
		Round:           round,
		QueryID:         query.ID,
		Query:           query.Text,
		StartedAt:       started,
		DurationNS:      duration.Nanoseconds(),
		DurationMS:      float64(duration.Nanoseconds()) / 1_000_000,
		Limit:           cfg.Limit,
		ExpectedSources: append([]string(nil), query.ExpectedSources...),
		ExpectedNodes:   append([]string(nil), query.ExpectedNodes...),
		ExpectedRels:    append([]string(nil), query.ExpectedRels...),
		Clean:           true,
	}
}

func fillMetrics(record *benchRecord, cfg benchConfig) {
	sourceMetrics := evaluateSet(record.ExpectedSources, record.ResultSources)
	nodeMetrics := evaluateSet(record.ExpectedNodes, record.ResultNodes)
	relMetrics := evaluateSet(record.ExpectedRels, record.ResultRels)
	record.SourceRecall = sourceMetrics.Recall
	record.SourcePrecision = sourceMetrics.Precision
	record.SourceNoise = sourceMetrics.Noise
	record.NodeRecall = nodeMetrics.Recall
	record.NodePrecision = nodeMetrics.Precision
	record.NodeNoise = nodeMetrics.Noise
	record.RelRecall = relMetrics.Recall

	switch record.Mode {
	case "graph":
		record.QualityPass = record.SourceRecall >= cfg.MinRecall &&
			record.NodeRecall >= cfg.MinRecall &&
			record.NodeNoise <= cfg.MaxNoise
	case "vector":
		record.QualityPass = record.SourceRecall >= 0.50
	default:
		record.QualityPass = true
	}
}

func evaluateSet(expected, actual []string) setMetrics {
	expected = uniqueStrings(expected)
	actual = uniqueStrings(actual)
	if len(expected) == 0 {
		if len(actual) == 0 {
			return setMetrics{Recall: 1, Precision: 1, Noise: 0}
		}
		return setMetrics{Recall: 1, Precision: 0, Noise: 1}
	}
	expectedSet := make(map[string]struct{}, len(expected))
	for _, item := range expected {
		expectedSet[normalizeMetricKey(item)] = struct{}{}
	}
	hits := 0
	for _, item := range actual {
		if _, ok := expectedSet[normalizeMetricKey(item)]; ok {
			hits++
		}
	}
	recall := float64(hits) / float64(len(expected))
	precision := 0.0
	if len(actual) > 0 {
		precision = float64(hits) / float64(len(actual))
	}
	return setMetrics{Recall: recall, Precision: precision, Noise: 1 - precision}
}

func retrievalSources(results []rag.RetrievalResult, limit int) []string {
	if limit > len(results) {
		limit = len(results)
	}
	out := make([]string, 0, limit)
	for _, result := range results[:limit] {
		if result.DocSource != "" {
			out = append(out, result.DocSource)
		}
	}
	return out
}

func activationNodeNames(results []rag.NodeActivationScore, limit int) []string {
	if limit > len(results) {
		limit = len(results)
	}
	out := make([]string, 0, limit)
	for _, result := range results[:limit] {
		if result.Node != nil && result.Node.Name != "" {
			out = append(out, result.Node.Name)
		}
	}
	return out
}

func activationRelationTypes(results []rag.NodeActivationScore, limit int) []string {
	if limit > len(results) {
		limit = len(results)
	}
	out := make([]string, 0, limit)
	for _, result := range results[:limit] {
		for _, path := range result.Paths {
			if path.RelType != "" {
				out = append(out, path.RelType)
			}
		}
	}
	return out
}

func summarizeRecords(cfg benchConfig, scenario string, records []benchRecord) benchSummary {
	summary := benchSummary{
		Type:        "summary",
		Variant:     cfg.Variant,
		Scenario:    scenario,
		Records:     len(records),
		Clean:       true,
		QualityPass: true,
		MinRecall:   cfg.MinRecall,
		MaxNoise:    cfg.MaxNoise,
	}

	var vectorSourceRecall, graphSourceRecall, graphNodeRecall, graphRelRecall float64
	var vectorDuration, graphDuration float64
	var vectorDurations, graphDurations []int64
	var vectorN, graphN int

	for _, record := range records {
		if record.Error != "" {
			summary.Errors++
		}
		if !record.Clean {
			summary.Clean = false
		}
		if !record.QualityPass {
			summary.QualityPass = false
		}
		switch record.Mode {
		case "vector":
			vectorN++
			vectorSourceRecall += record.SourceRecall
			vectorDuration += float64(record.DurationNS)
			vectorDurations = append(vectorDurations, record.DurationNS)
		case "graph":
			graphN++
			graphSourceRecall += record.SourceRecall
			graphNodeRecall += record.NodeRecall
			graphRelRecall += record.RelRecall
			graphDuration += float64(record.DurationNS)
			graphDurations = append(graphDurations, record.DurationNS)
			if record.NodeRecall < cfg.MinRecall || record.NodeNoise > cfg.MaxNoise {
				summary.QualityPass = false
			}
		}
	}

	if vectorN > 0 {
		summary.AvgVectorSourceRecall = vectorSourceRecall / float64(vectorN)
		summary.AvgVectorDurationNS = vectorDuration / float64(vectorN)
		summary.P95VectorDurationNS = percentileDuration(vectorDurations, 0.95)
	}
	if graphN > 0 {
		summary.AvgGraphSourceRecall = graphSourceRecall / float64(graphN)
		summary.AvgGraphNodeRecall = graphNodeRecall / float64(graphN)
		summary.AvgGraphRelRecall = graphRelRecall / float64(graphN)
		summary.AvgGraphDurationNS = graphDuration / float64(graphN)
		summary.P95GraphDurationNS = percentileDuration(graphDurations, 0.95)
	}
	summary.GraphNodeRecallLift = summary.AvgGraphNodeRecall
	summary.GraphSourceRecallLift = summary.AvgGraphSourceRecall - summary.AvgVectorSourceRecall
	if summary.AvgVectorDurationNS > 0 {
		summary.GraphLatencyOverheadPct = (summary.AvgGraphDurationNS - summary.AvgVectorDurationNS) / summary.AvgVectorDurationNS * 100
	}
	return summary
}

func percentileDuration(values []int64, p float64) int64 {
	if len(values) == 0 {
		return 0
	}
	cp := append([]int64(nil), values...)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	idx := int(float64(len(cp)-1) * p)
	if idx < 0 {
		idx = 0
	}
	if idx >= len(cp) {
		idx = len(cp) - 1
	}
	return cp[idx]
}

func uniqueStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, item := range in {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		key := normalizeMetricKey(item)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func normalizeMetricKey(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func printRecord(record benchRecord) {
	fmt.Printf(
		"%s mode=%s round=%d query=%s duration=%.3fms source_recall=%.2f node_recall=%.2f rel_recall=%.2f source_noise=%.2f node_noise=%.2f quality=%t\n",
		record.Scenario,
		record.Mode,
		record.Round,
		record.QueryID,
		record.DurationMS,
		record.SourceRecall,
		record.NodeRecall,
		record.RelRecall,
		record.SourceNoise,
		record.NodeNoise,
		record.QualityPass,
	)
}

func printSummary(summary benchSummary) {
	fmt.Printf(
		"summary scenario=%s records=%d vector_source_recall=%.2f graph_source_recall=%.2f graph_node_recall=%.2f graph_rel_recall=%.2f graph_overhead=%.1f%% clean=%t quality=%t\n",
		summary.Scenario,
		summary.Records,
		summary.AvgVectorSourceRecall,
		summary.AvgGraphSourceRecall,
		summary.AvgGraphNodeRecall,
		summary.AvgGraphRelRecall,
		summary.GraphLatencyOverheadPct,
		summary.Clean,
		summary.QualityPass,
	)
}

func printAsJSON(v any) {
	data, err := json.Marshal(v)
	if err == nil {
		fmt.Println(string(data))
	}
}
