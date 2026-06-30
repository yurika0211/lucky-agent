package memory

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTidalRerankerNoFeedbackKeepsScores(t *testing.T) {
	now := time.Now()
	reranker := NewTidalMemoryReranker(TidalRerankerConfig{Beta: 1})
	scores := []ActivationScore{
		{EntryID: "a", Entry: Entry{ID: "a", Content: "alpha", Tier: TierLong, CreatedAt: now}, Score: 1.2},
		{EntryID: "b", Entry: Entry{ID: "b", Content: "beta", Tier: TierLong, CreatedAt: now}, Score: 0.8},
	}

	got := reranker.RerankMemoryActivations("anything", scores, now)
	if len(got) != len(scores) {
		t.Fatalf("expected %d scores, got %#v", len(scores), got)
	}
	for i := range scores {
		if got[i].Score != scores[i].Score {
			t.Fatalf("score changed without feedback: got %#v want %#v", got, scores)
		}
		if got[i].Components.TidalBoost != 0 {
			t.Fatalf("unexpected boost without feedback: %#v", got[i].Components)
		}
	}
}

func TestTidalRerankerPositiveFeedbackCanPromoteCurrentIntent(t *testing.T) {
	now := time.Now()
	config := DefaultTidalRerankerConfig()
	config.Beta = 1
	config.MaxBoost = 1
	config.LearningRate = 1
	reranker := NewTidalMemoryReranker(config)

	graphEntry := Entry{
		ID:        "graph",
		Content:   "Graph RAG indexing benchmark",
		Category:  "project",
		Tier:      TierMedium,
		Tags:      []string{"graph-rag"},
		CreatedAt: now.Add(-2 * 24 * time.Hour),
	}
	pdfEntry := Entry{
		ID:        "pdf",
		Content:   "PDF export benchmark",
		Category:  "docs",
		Tier:      TierLong,
		Tags:      []string{"pdf"},
		CreatedAt: now.Add(-2 * 24 * time.Hour),
	}
	reranker.ObserveFeedback(TidalFeedback{
		Query: "benchmark",
		Entry: graphEntry,
		Value: 1,
		At:    now,
	})

	scores := []ActivationScore{
		{EntryID: "pdf", Entry: pdfEntry, Score: 1.0},
		{EntryID: "graph", Entry: graphEntry, Score: 0.8},
	}
	got := reranker.RerankMemoryActivations("benchmark", scores, now)
	sortActivationScores(got)
	if got[0].EntryID != "graph" {
		t.Fatalf("expected graph entry to be promoted, got %#v", got)
	}
	if got[0].Components.TidalBoost <= got[1].Components.TidalBoost {
		t.Fatalf("expected stronger graph boost, got %#v", got)
	}
}

func TestTidalRerankerNegativeFeedbackCanSuppressStaleMemory(t *testing.T) {
	now := time.Now()
	config := DefaultTidalRerankerConfig()
	config.Beta = 1
	config.MaxBoost = 1
	config.LearningRate = 1
	reranker := NewTidalMemoryReranker(config)

	staleEntry := Entry{
		ID:        "stale",
		Content:   "Old transcription model works",
		Category:  "stale",
		Tier:      TierShort,
		Tags:      []string{"obsolete"},
		CreatedAt: now.Add(-30 * time.Minute),
	}
	currentEntry := Entry{
		ID:        "current",
		Content:   "Current transcription request returns 404",
		Category:  "evidence",
		Tier:      TierLong,
		Tags:      []string{"api"},
		CreatedAt: now.Add(-30 * time.Minute),
	}
	reranker.ObserveFeedback(TidalFeedback{
		Query: "transcription status",
		Entry: staleEntry,
		Value: -1,
		At:    now,
	})

	scores := []ActivationScore{
		{EntryID: "stale", Entry: staleEntry, Score: 1.0},
		{EntryID: "current", Entry: currentEntry, Score: 0.9},
	}
	got := reranker.RerankMemoryActivations("transcription status", scores, now)
	sortActivationScores(got)
	if got[0].EntryID != "current" {
		t.Fatalf("expected stale entry to be suppressed below current evidence, got %#v", got)
	}
	for _, score := range got {
		if score.EntryID == "stale" && score.Components.TidalBoost >= 0 {
			t.Fatalf("expected negative stale boost, got %#v", score.Components)
		}
	}
}

func TestTidalRerankerSnapshotsExposeLearnedKernels(t *testing.T) {
	now := time.Now()
	reranker := NewTidalMemoryReranker(TidalRerankerConfig{LearningRate: 1})
	entry := Entry{
		ID:        "health",
		Content:   "Pollen allergy",
		Category:  "health",
		Tier:      TierLong,
		Tags:      []string{"allergy"},
		CreatedAt: now,
	}
	reranker.ObserveFeedback(TidalFeedback{
		Query: "女儿过敏",
		Entry: entry,
		Value: 1,
		At:    now,
	})

	snapshots := reranker.KernelSnapshots()
	var found bool
	for _, snapshot := range snapshots {
		if strings.HasPrefix(snapshot.Key, "tag:allergy") {
			found = true
			if len(snapshot.Weights) == 0 || len(snapshot.Counts) == 0 {
				t.Fatalf("expected populated snapshot, got %#v", snapshot)
			}
		}
	}
	if !found {
		t.Fatalf("expected allergy kernel snapshot, got %#v", snapshots)
	}
}

func TestPersistentTidalRerankerRestoresKernelsAndRecordsTelemetry(t *testing.T) {
	now := time.Now()
	dbPath := filepath.Join(t.TempDir(), "tidal_memory.db")
	store, err := OpenTidalStore(dbPath)
	if err != nil {
		t.Fatalf("OpenTidalStore() error = %v", err)
	}

	config := DefaultTidalRerankerConfig()
	config.LearningRate = 1
	config.Beta = 1
	config.MaxBoost = 1
	reranker, err := NewPersistentTidalMemoryReranker(config, store)
	if err != nil {
		t.Fatalf("NewPersistentTidalMemoryReranker() error = %v", err)
	}

	entry := Entry{
		ID:        "graph",
		Content:   "Graph RAG benchmark matters for the current project",
		Category:  "project",
		Tier:      TierLong,
		Tags:      []string{"graph-rag"},
		CreatedAt: now,
	}
	reranker.RecordMemoryActivation("graph rag benchmark", []ActivationScore{
		{EntryID: entry.ID, Entry: entry, Score: 1.2, Components: ActivationComponents{TidalBoost: 1}},
	}, now.Add(time.Second))
	reranker.ObserveFeedback(TidalFeedback{
		Query:  "graph rag benchmark",
		Entry:  entry,
		Signal: "accepted",
		Value:  1,
		At:     now,
	})

	stats, err := reranker.StoreStats()
	if err != nil {
		t.Fatalf("StoreStats() error = %v", err)
	}
	if stats.QueryEvents != 1 || stats.RecallEvents != 1 || stats.FeedbackEvents != 1 || stats.Kernels == 0 {
		t.Fatalf("unexpected stats before reopen: %#v", stats)
	}
	var feedbackQueryID string
	if err := store.db.QueryRow(`SELECT query_id FROM feedback_events LIMIT 1`).Scan(&feedbackQueryID); err != nil {
		t.Fatalf("read feedback query id: %v", err)
	}
	if feedbackQueryID == "" {
		t.Fatal("expected feedback to be linked to a query event")
	}
	var feature, bins string
	if err := store.db.QueryRow(`SELECT feature, bins FROM response_kernels WHERE key = ?`, "tag:graph-rag").Scan(&feature, &bins); err != nil {
		t.Fatalf("read response kernel metadata: %v", err)
	}
	if feature != "tag" || bins == "" || bins == "[]" {
		t.Fatalf("expected response kernel feature and bins, got feature=%q bins=%q", feature, bins)
	}
	if err := reranker.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopenedStore, err := OpenTidalStore(dbPath)
	if err != nil {
		t.Fatalf("reopen OpenTidalStore() error = %v", err)
	}
	reopened, err := NewPersistentTidalMemoryReranker(config, reopenedStore)
	if err != nil {
		t.Fatalf("reopen NewPersistentTidalMemoryReranker() error = %v", err)
	}
	defer reopened.Close()

	snapshots := reopened.KernelSnapshots()
	var restored bool
	for _, snapshot := range snapshots {
		if snapshot.Key == "tag:graph-rag" {
			restored = true
			break
		}
	}
	if !restored {
		t.Fatalf("expected persisted tag kernel after reopen, got %#v", snapshots)
	}

	got := reopened.RerankMemoryActivations("graph rag benchmark", []ActivationScore{
		{EntryID: entry.ID, Entry: entry, Score: 1.0},
	}, now)
	if len(got) != 1 || got[0].Components.TidalBoost <= 0 || got[0].Score <= 1.0 {
		t.Fatalf("expected restored kernel to boost activation, got %#v", got)
	}
}
