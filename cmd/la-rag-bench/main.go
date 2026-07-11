package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/yurika0211/luckyagent/internal/logger"
	"github.com/yurika0211/luckyagent/internal/rag"
)

type config struct {
	Rounds    int
	NoiseDocs int
	TopK      int
	Inner     int
	Out       string
}

type document struct{ source, title, content string }
type query struct{ id, group, text, want string }

type record struct {
	Type       string   `json:"type"`
	Mode       string   `json:"mode"`
	Round      int      `json:"round"`
	QueryID    string   `json:"query_id"`
	Group      string   `json:"group"`
	DurationNS int64    `json:"duration_ns"`
	Inner      int      `json:"inner_iterations"`
	Sources    []string `json:"sources,omitempty"`
	Rank       int      `json:"rank"`
	HitAt1     bool     `json:"hit_at_1"`
	Error      string   `json:"error,omitempty"`
}

type metric struct {
	Mode          string  `json:"mode"`
	Group         string  `json:"group"`
	Queries       int     `json:"queries"`
	RecallAt1     float64 `json:"recall_at_1"`
	MRR           float64 `json:"mrr"`
	EmptyRate     float64 `json:"empty_rate"`
	ErrorRate     float64 `json:"error_rate"`
	P50LatencyUS  float64 `json:"p50_latency_us"`
	P95LatencyUS  float64 `json:"p95_latency_us"`
	MeanLatencyUS float64 `json:"mean_latency_us"`
}

type summary struct {
	Type      string   `json:"type"`
	Generated string   `json:"generated_at"`
	Rounds    int      `json:"rounds"`
	NoiseDocs int      `json:"noise_docs"`
	Documents int      `json:"documents"`
	TopK      int      `json:"top_k"`
	Inner     int      `json:"inner_iterations"`
	Metrics   []metric `json:"metrics"`
	Clean     bool     `json:"clean"`
}

type controlledEmbedder struct {
	mu   sync.RWMutex
	fail bool
}

func (e *controlledEmbedder) Dimension() int { return 16 }
func (e *controlledEmbedder) Name() string   { return "rag-bench-controlled" }
func (e *controlledEmbedder) Model() string  { return "semantic-features-v1" }
func (e *controlledEmbedder) SetFail(v bool) { e.mu.Lock(); e.fail = v; e.mu.Unlock() }

func (e *controlledEmbedder) Embed(ctx context.Context, text string) ([]float64, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	e.mu.RLock()
	fail := e.fail
	e.mu.RUnlock()
	if fail {
		return nil, errors.New("simulated embedding outage")
	}
	return semanticVector(text), nil
}

func (e *controlledEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float64, error) {
	out := make([][]float64, len(texts))
	for i, text := range texts {
		vec, err := e.Embed(ctx, text)
		if err != nil {
			return nil, err
		}
		out[i] = vec
	}
	return out, nil
}

func semanticVector(text string) []float64 {
	lower := strings.ToLower(text)
	groups := [][]string{
		{"database", "sqlite", "persistent", "persistence", "transaction", "数据库", "事务", "持久化"},
		{"telegram", "gateway", "webhook", "bot", "message", "网关", "消息"},
		{"concurrency", "concurrent", "goroutine", "goroutines", "channel", "channels", "并发", "协程", "通道"},
		{"cache", "checkpoint", "session", "prompt", "缓存", "会话", "上下文"},
		{"retrieval", "search", "rag", "knowledge", "检索", "知识库", "召回"},
		{"stream", "watch", "watches", "indexer", "scan", "scans", "流式", "监听", "扫描"},
	}
	vec := make([]float64, 16)
	for i, terms := range groups {
		for _, term := range terms {
			if hasSemanticTerm(lower, term) {
				vec[i]++
			}
		}
	}
	return vec
}

func hasSemanticTerm(text, term string) bool {
	for _, r := range term {
		if unicode.Is(unicode.Han, r) {
			return strings.Contains(text, term)
		}
	}
	words := strings.FieldsFunc(text, func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-')
	})
	for _, word := range words {
		if word == term {
			return true
		}
	}
	return false
}

func main() {
	cfg := config{}
	flag.IntVar(&cfg.Rounds, "rounds", 30, "number of repetitions per query and mode")
	flag.IntVar(&cfg.NoiseDocs, "noise-docs", 500, "number of irrelevant documents")
	flag.IntVar(&cfg.TopK, "top-k", 3, "retrieval limit used for MRR")
	flag.IntVar(&cfg.Inner, "inner", 10, "searches per timed sample (reduces timer quantization)")
	flag.StringVar(&cfg.Out, "out", "", "optional JSONL output path")
	flag.Parse()
	if cfg.Rounds <= 0 || cfg.NoiseDocs < 0 || cfg.TopK <= 0 || cfg.Inner <= 0 {
		fmt.Fprintln(os.Stderr, "rounds, top-k, and inner must be positive; noise-docs must be non-negative")
		os.Exit(2)
	}
	logger.InitLogger(logger.Config{Level: "error", Output: "stderr"})

	docs := benchmarkDocuments(cfg.NoiseDocs)
	queries := benchmarkQueries()
	modes := []struct {
		name         string
		hybrid, fail bool
	}{
		{"dense-only", false, false},
		{"hybrid", true, false},
		{"hybrid-embedding-outage", true, true},
	}

	var records []record
	for _, mode := range modes {
		emb := &controlledEmbedder{}
		rc := rag.DefaultRetrieverConfig()
		rc.TopK = cfg.TopK
		rc.UseHybrid = mode.hybrid
		mgr := rag.NewRAGManager(emb, rag.RAGConfig{EmbeddingDim: emb.Dimension(), RetrieverConfig: rc})
		for _, doc := range docs {
			if _, err := mgr.IndexText(doc.source, doc.title, doc.content); err != nil {
				fmt.Fprintf(os.Stderr, "index %s: %v\n", doc.source, err)
				os.Exit(1)
			}
		}
		emb.SetFail(mode.fail)
		inner := cfg.Inner
		if !mode.hybrid {
			inner *= 10
		}
		for round := 1; round <= cfg.Rounds; round++ {
			for _, q := range queries {
				started := time.Now()
				var results []rag.RetrievalResult
				var err error
				for i := 0; i < inner; i++ {
					results, err = mgr.Search(context.Background(), q.text)
					if err != nil {
						break
					}
				}
				duration := time.Since(started).Nanoseconds() / int64(inner)
				rec := record{Type: "query", Mode: mode.name, Round: round, QueryID: q.id, Group: q.group, DurationNS: duration, Inner: inner}
				if err != nil {
					rec.Error = err.Error()
				}
				for i, result := range results {
					rec.Sources = append(rec.Sources, result.DocSource)
					if rec.Rank == 0 && result.DocSource == q.want {
						rec.Rank = i + 1
					}
				}
				rec.HitAt1 = rec.Rank == 1
				records = append(records, rec)
			}
		}
	}

	s := summarize(cfg, len(docs), records)
	if err := emit(cfg.Out, records, s); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	printSummary(s)
	if !s.Clean {
		os.Exit(1)
	}
}

func benchmarkDocuments(noise int) []document {
	docs := []document{
		{"sqlite-atomic.md", "Atomic SQLite rebuild", "SQLite persistence stages embeddings before replacing vectors and chunks in one database transaction."},
		{"telegram-gateway.md", "Telegram delivery", "The Telegram gateway receives bot webhook messages and routes each message into a conversation session."},
		{"go-concurrency.md", "Go concurrency", "Goroutines coordinate through channels to implement safe concurrent worker processing."},
		{"prompt-cache.md", "Prompt cache", "Session checkpoints keep stable conversation context before dynamic prompt content to improve cache reuse."},
		{"rag-search.md", "Knowledge retrieval", "RAG knowledge retrieval combines semantic search with relevance scoring and source evidence."},
		{"stream-indexer.md", "Stream indexer", "The stream indexer watches directories and scans changed files before updating the knowledge index."},
		{"error-catalog.md", "Worker errors", "Operational failure catalog: ERR_LEASE_409 means that a worker lease expired during renewal."},
		{"config-reference.md", "Configuration reference", "The configuration key cfg_rag_dense_weight controls the dense contribution to hybrid ranking."},
		{"tracing-guide.md", "Tracing guide", "Use trace_id_7F3A to locate the sample request across distributed logs."},
		{"orders.md", "订单故障手册", "订单码 ZX-8842 表示库存预留在提交阶段已经过期。"},
	}
	for i := 0; i < noise; i++ {
		docs = append(docs, document{
			source:  fmt.Sprintf("noise/archive-%04d.md", i),
			title:   fmt.Sprintf("Archive %04d", i),
			content: fmt.Sprintf("Archived unrelated record %04d contains routine notes, calendar entries, and generic status text.", i),
		})
	}
	return docs
}

func benchmarkQueries() []query {
	return []query{
		{"semantic-sqlite", "semantic", "How does persistent database replacement remain atomic?", "sqlite-atomic.md"},
		{"semantic-gateway", "semantic", "How are bot messages delivered through the gateway?", "telegram-gateway.md"},
		{"semantic-concurrency", "semantic", "How do goroutines coordinate concurrent workers?", "go-concurrency.md"},
		{"semantic-cache", "semantic", "会话上下文如何保持缓存稳定？", "prompt-cache.md"},
		{"semantic-rag", "semantic", "知识库如何检索相关证据？", "rag-search.md"},
		{"semantic-stream", "semantic", "Which component watches and scans changed files?", "stream-indexer.md"},
		{"exact-error", "exact", "ERR_LEASE_409", "error-catalog.md"},
		{"exact-config", "exact", "cfg_rag_dense_weight", "config-reference.md"},
		{"exact-trace", "exact", "trace_id_7F3A", "tracing-guide.md"},
		{"exact-order", "exact", "订单码 ZX-8842", "orders.md"},
	}
}

func summarize(cfg config, documents int, records []record) summary {
	s := summary{Type: "summary", Generated: time.Now().Format(time.RFC3339), Rounds: cfg.Rounds, NoiseDocs: cfg.NoiseDocs, Documents: documents, TopK: cfg.TopK, Inner: cfg.Inner, Clean: true}
	modes := []string{"dense-only", "hybrid", "hybrid-embedding-outage"}
	groups := []string{"all", "semantic", "exact"}
	for _, mode := range modes {
		for _, group := range groups {
			var selected []record
			for _, rec := range records {
				if rec.Mode == mode && (group == "all" || rec.Group == group) {
					selected = append(selected, rec)
				}
			}
			s.Metrics = append(s.Metrics, calculateMetric(mode, group, selected))
		}
	}
	for _, m := range s.Metrics {
		if m.Mode == "hybrid" && m.Group == "all" && (m.RecallAt1 < 0.99 || m.ErrorRate > 0) {
			s.Clean = false
		}
		if m.Mode == "hybrid-embedding-outage" && m.Group == "exact" && (m.RecallAt1 < 0.99 || m.ErrorRate > 0) {
			s.Clean = false
		}
	}
	return s
}

func calculateMetric(mode, group string, records []record) metric {
	m := metric{Mode: mode, Group: group, Queries: len(records)}
	if len(records) == 0 {
		return m
	}
	latencies := make([]int64, 0, len(records))
	for _, rec := range records {
		if rec.HitAt1 {
			m.RecallAt1++
		}
		if rec.Rank > 0 {
			m.MRR += 1 / float64(rec.Rank)
		}
		if len(rec.Sources) == 0 {
			m.EmptyRate++
		}
		if rec.Error != "" {
			m.ErrorRate++
		}
		latencies = append(latencies, rec.DurationNS)
		m.MeanLatencyUS += float64(rec.DurationNS) / 1000
	}
	n := float64(len(records))
	m.RecallAt1 /= n
	m.MRR /= n
	m.EmptyRate /= n
	m.ErrorRate /= n
	m.MeanLatencyUS /= n
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	m.P50LatencyUS = float64(percentile(latencies, 0.50)) / 1000
	m.P95LatencyUS = float64(percentile(latencies, 0.95)) / 1000
	return m
}

func percentile(values []int64, p float64) int64 {
	if len(values) == 0 {
		return 0
	}
	idx := int(math.Ceil(p*float64(len(values)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(values) {
		idx = len(values) - 1
	}
	return values[idx]
}

func emit(path string, records []record, s summary) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	path = filepath.Clean(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	for _, rec := range records {
		if err := enc.Encode(rec); err != nil {
			return err
		}
	}
	return enc.Encode(s)
}

func printSummary(s summary) {
	fmt.Printf("RAG benchmark: %d docs (%d noise), %d rounds, inner=%d, top-k=%d\n", s.Documents, s.NoiseDocs, s.Rounds, s.Inner, s.TopK)
	fmt.Printf("%-27s %-9s %9s %9s %9s %10s %10s\n", "MODE", "GROUP", "R@1", "MRR", "EMPTY", "P50(us)", "P95(us)")
	for _, m := range s.Metrics {
		fmt.Printf("%-27s %-9s %9.3f %9.3f %9.3f %10.1f %10.1f\n", m.Mode, m.Group, m.RecallAt1, m.MRR, m.EmptyRate, m.P50LatencyUS, m.P95LatencyUS)
	}
	fmt.Printf("clean=%v\n", s.Clean)
}
