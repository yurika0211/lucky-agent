package main

import "testing"

func TestSemanticTermDoesNotTreatOpaqueIdentifierAsVocabulary(t *testing.T) {
	if hasSemanticTerm("cfg_rag_dense_weight", "rag") {
		t.Fatal("opaque identifier should remain out of vocabulary")
	}
	if !hasSemanticTerm("rag knowledge retrieval", "rag") {
		t.Fatal("standalone semantic term should match")
	}
	if !hasSemanticTerm("会话上下文如何保持缓存稳定", "缓存") {
		t.Fatal("Chinese semantic term should match")
	}
}

func TestCalculateMetric(t *testing.T) {
	records := []record{
		{Mode: "hybrid", Group: "exact", Rank: 1, HitAt1: true, Sources: []string{"a"}, DurationNS: 1000},
		{Mode: "hybrid", Group: "exact", Rank: 2, Sources: []string{"b", "a"}, DurationNS: 3000},
	}
	m := calculateMetric("hybrid", "exact", records)
	if m.RecallAt1 != 0.5 || m.MRR != 0.75 || m.P50LatencyUS != 1 || m.P95LatencyUS != 3 {
		t.Fatalf("unexpected metric: %+v", m)
	}
}
