package main

import (
	"testing"

	"github.com/yurika0211/luckyagent/internal/memory"
)

func TestExpandScenarios(t *testing.T) {
	got, err := expandScenarios("all")
	if err != nil {
		t.Fatalf("expandScenarios: %v", err)
	}
	want := []string{"lexical", "graph", "temporal", "scale", "route", "tidal"}
	if len(got) != len(want) {
		t.Fatalf("expected %d scenarios, got %#v", len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("scenario %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestEvaluateEntriesComputesQualityMetrics(t *testing.T) {
	query := benchQuery{
		WantIDs:   []string{"a", "b"},
		ForbidIDs: []string{"stale"},
	}
	entries := []memory.Entry{
		{ID: "noise"},
		{ID: "a"},
		{ID: "stale"},
	}

	got := evaluateEntries(query, entries, 3)
	if got.ResultCount != 3 {
		t.Fatalf("ResultCount = %d", got.ResultCount)
	}
	if got.HitCount != 1 {
		t.Fatalf("HitCount = %d", got.HitCount)
	}
	if got.ForbidHitCount != 1 {
		t.Fatalf("ForbidHitCount = %d", got.ForbidHitCount)
	}
	if got.RecallAtK != 0.5 {
		t.Fatalf("RecallAtK = %v", got.RecallAtK)
	}
	if got.PrecisionAtK != float64(1)/float64(3) {
		t.Fatalf("PrecisionAtK = %v", got.PrecisionAtK)
	}
	if got.MRR != 0.5 {
		t.Fatalf("MRR = %v", got.MRR)
	}
}

func TestSummarizeRecordsDetectsGraphLift(t *testing.T) {
	cfg := benchConfig{
		Variant:      "test",
		Dataset:      "synthetic",
		Size:         100,
		Rounds:       1,
		MinRecall:    0.5,
		MaxNoise:     1.0,
		MaxStaleHits: 0,
	}
	records := []benchRecord{
		{
			Mode:          "graph_off",
			DurationNS:    100,
			ExpectedCount: 2,
			RecallAtK:     0.5,
			NoiseAtK:      0.5,
			Clean:         true,
			QualityPass:   true,
		},
		{
			Mode:          "graph_on",
			DurationNS:    200,
			ExpectedCount: 2,
			RecallAtK:     1.0,
			NoiseAtK:      0.25,
			Clean:         true,
			QualityPass:   true,
		},
	}

	got := summarizeRecords(cfg, "graph", records)
	if !got.Clean || !got.QualityPass {
		t.Fatalf("expected clean quality summary: %#v", got)
	}
	if got.GraphRecallLift != 0.5 {
		t.Fatalf("GraphRecallLift = %v", got.GraphRecallLift)
	}
	if got.AvgGraphOnDurationNS != 200 || got.AvgGraphOffDurationNS != 100 {
		t.Fatalf("unexpected graph durations: %#v", got)
	}
}

func TestSyntheticDatasetLoadsMemoryStore(t *testing.T) {
	cfg := benchConfig{
		Dataset:     "synthetic",
		Size:        32,
		Seed:        42,
		KeepDataset: false,
	}
	ds, cleanup, err := loadSyntheticDataset(cfg)
	if err != nil {
		t.Fatalf("loadSyntheticDataset: %v", err)
	}
	defer cleanup()

	if ds.Store == nil {
		t.Fatalf("expected store")
	}
	if ds.Size < 32 {
		t.Fatalf("expected at least 32 notes, got %d", ds.Size)
	}
	if len(ds.QueriesForScenario("graph", "")) == 0 {
		t.Fatalf("expected graph queries")
	}
	if len(ds.QueriesForScenario("tidal", "")) == 0 {
		t.Fatalf("expected tidal queries")
	}
	results := ds.Store.Activate("telegram channel", memory.ActivationOptions{Limit: 3, IncludeGraph: true})
	if len(results) == 0 {
		t.Fatalf("expected activation results")
	}
}

func TestTidalScenarioReportsMRRLift(t *testing.T) {
	cfg := benchConfig{
		Variant:      "test",
		Dataset:      "synthetic",
		Size:         100,
		Rounds:       1,
		MinRecall:    0.5,
		MaxNoise:     1.0,
		MaxStaleHits: 0,
	}
	records := []benchRecord{
		{
			Mode:          "tidal_off",
			DurationNS:    100,
			ExpectedCount: 1,
			RecallAtK:     1.0,
			MRR:           0.5,
			NoiseAtK:      0.5,
			StaleHitRate:  0.25,
			Clean:         true,
			QualityPass:   true,
		},
		{
			Mode:          "tidal_on",
			DurationNS:    200,
			ExpectedCount: 1,
			RecallAtK:     1.0,
			MRR:           1.0,
			NoiseAtK:      0.5,
			StaleHitRate:  0.10,
			Clean:         true,
			QualityPass:   true,
		},
	}

	got := summarizeRecords(cfg, "tidal", records)
	if got.TidalMRRLift != 0.5 {
		t.Fatalf("TidalMRRLift = %v", got.TidalMRRLift)
	}
	if got.AvgTidalOnDurationNS != 200 || got.AvgTidalOffDurationNS != 100 {
		t.Fatalf("unexpected tidal durations: %#v", got)
	}
	if got.TidalStaleRateDelta >= 0 {
		t.Fatalf("expected tidal stale rate not to increase: %#v", got)
	}
}

func TestAllSummaryIgnoresTidalControlGroupDirtyRecords(t *testing.T) {
	cfg := benchConfig{
		Variant:      "test",
		Dataset:      "synthetic",
		Size:         100,
		Rounds:       1,
		MinRecall:    0.5,
		MaxNoise:     0.5,
		MaxStaleHits: 0,
	}
	records := []benchRecord{
		{
			Scenario:      "graph",
			Mode:          "graph_on",
			DurationNS:    100,
			ExpectedCount: 1,
			RecallAtK:     1.0,
			NoiseAtK:      0.4,
			Clean:         true,
		},
		{
			Scenario:      "tidal",
			Mode:          "tidal_off",
			DurationNS:    100,
			ExpectedCount: 1,
			RecallAtK:     1.0,
			MRR:           0.5,
			NoiseAtK:      0.8,
			StaleHitRate:  0.25,
			Clean:         false,
		},
		{
			Scenario:      "tidal",
			Mode:          "tidal_on",
			DurationNS:    100,
			ExpectedCount: 1,
			RecallAtK:     1.0,
			MRR:           1.0,
			NoiseAtK:      0.8,
			StaleHitRate:  0.25,
			Clean:         false,
		},
	}

	got := summarizeRecords(cfg, "all", records)
	if !got.Clean || !got.QualityPass {
		t.Fatalf("expected all summary to use candidate quality, got %#v", got)
	}
	if got.TidalMRRLift <= 0 || got.TidalStaleRateDelta > 0 {
		t.Fatalf("expected tidal comparison metrics, got %#v", got)
	}
}
