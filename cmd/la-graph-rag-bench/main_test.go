package main

import (
	"context"
	"math"
	"testing"
)

func TestEvaluateSet(t *testing.T) {
	got := evaluateSet(
		[]string{"Lin", "Orion Labs", "Neo Shanghai"},
		[]string{"lin", "Noise", "Neo Shanghai"},
	)
	if !closeFloat(got.Recall, 2.0/3.0) {
		t.Fatalf("Recall = %v, want %v", got.Recall, 2.0/3.0)
	}
	if !closeFloat(got.Precision, 2.0/3.0) {
		t.Fatalf("Precision = %v, want %v", got.Precision, 2.0/3.0)
	}
	if !closeFloat(got.Noise, 1.0/3.0) {
		t.Fatalf("Noise = %v, want %v", got.Noise, 1.0/3.0)
	}
}

func closeFloat(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

func TestExpandScenarios(t *testing.T) {
	got, err := expandScenarios("all")
	if err != nil {
		t.Fatalf("expandScenarios: %v", err)
	}
	want := []string{"direct", "bridge", "multihop", "distractor"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("scenario[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if _, err := expandScenarios("unknown"); err == nil {
		t.Fatal("expected unknown scenario error")
	}
}

func TestBuildBenchmarkManagerCreatesGraph(t *testing.T) {
	manager, err := buildBenchmarkManager(context.Background(), 3)
	if err != nil {
		t.Fatalf("buildBenchmarkManager: %v", err)
	}
	defer manager.CloseStore()

	graph := manager.Graph()
	if graph == nil {
		t.Fatal("expected graph to be enabled")
	}
	stats := graph.Stats()
	if stats.NodeCount < 8 {
		t.Fatalf("NodeCount = %d, want at least 8", stats.NodeCount)
	}
	if stats.EdgeCount < 7 {
		t.Fatalf("EdgeCount = %d, want at least 7", stats.EdgeCount)
	}
}

func TestSummarizeRecordsComputesGraphLift(t *testing.T) {
	cfg := benchConfig{MinRecall: 0.75, MaxNoise: 0.60}
	records := []benchRecord{
		{Mode: "vector", SourceRecall: 0.5, DurationNS: 100, Clean: true, QualityPass: true},
		{Mode: "graph", SourceRecall: 1.0, NodeRecall: 1.0, RelRecall: 1.0, SourceNoise: 0.2, NodeNoise: 0.2, DurationNS: 150, Clean: true, QualityPass: true},
	}

	summary := summarizeRecords(cfg, "bridge", records)
	if summary.AvgVectorSourceRecall != 0.5 {
		t.Fatalf("AvgVectorSourceRecall = %v", summary.AvgVectorSourceRecall)
	}
	if summary.AvgGraphSourceRecall != 1.0 {
		t.Fatalf("AvgGraphSourceRecall = %v", summary.AvgGraphSourceRecall)
	}
	if summary.GraphSourceRecallLift != 0.5 {
		t.Fatalf("GraphSourceRecallLift = %v", summary.GraphSourceRecallLift)
	}
	if summary.GraphLatencyOverheadPct != 50 {
		t.Fatalf("GraphLatencyOverheadPct = %v", summary.GraphLatencyOverheadPct)
	}
	if !summary.Clean || !summary.QualityPass {
		t.Fatalf("summary should pass: %+v", summary)
	}
}
