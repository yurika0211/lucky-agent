package main

import "testing"

func TestQualityScorePenalizesNoise(t *testing.T) {
	clean := benchRecord{
		CMR:        1,
		CR:         1,
		TWR:        1,
		ERR:        1,
		ToolRecall: 1,
	}
	noisy := clean
	noisy.ContextNoise = 1

	if qualityScore(clean) <= qualityScore(noisy) {
		t.Fatalf("expected clean quality to be higher: clean=%f noisy=%f", qualityScore(clean), qualityScore(noisy))
	}
}

func TestSummarizeRecordsComputesP95AndPasses(t *testing.T) {
	cfg := benchConfig{
		Variant:    "test",
		Rounds:     2,
		MinQuality: 0.75,
		MaxNoise:   0.25,
		MaxP95MS:   10,
	}
	records := []benchRecord{
		{DurationMS: 1, PromptTokens: 100, CMR: 1, CR: 1, TWR: 1, ERR: 1, ToolRecall: 1, Quality: 1, Clean: true, BucketTokens: map[string]int{"memory": 10}},
		{DurationMS: 3, PromptTokens: 200, CMR: 1, CR: 1, TWR: 1, ERR: 1, ToolRecall: 1, Quality: 1, Clean: true, BucketTokens: map[string]int{"memory": 20}},
	}

	summary := summarizeRecords(cfg, "all", records)
	if summary.P95DurationMS <= 0 {
		t.Fatalf("expected p95 duration, got %f", summary.P95DurationMS)
	}
	if !summary.Clean {
		t.Fatalf("expected clean summary: %+v", summary)
	}
	if summary.AvgPromptTokens != 150 {
		t.Fatalf("expected avg prompt tokens 150, got %f", summary.AvgPromptTokens)
	}
}

func TestSummarizeRecordsRequiresEveryRecordClean(t *testing.T) {
	cfg := benchConfig{
		Variant:    "test",
		Rounds:     2,
		MinQuality: 0.75,
		MaxNoise:   0.25,
		MaxP95MS:   10,
	}
	records := []benchRecord{
		{DurationMS: 1, PromptTokens: 100, CMR: 1, CR: 1, TWR: 1, ERR: 1, ToolRecall: 1, Quality: 1, Clean: true, BucketTokens: map[string]int{"memory": 10}},
		{DurationMS: 2, PromptTokens: 100, CMR: 1, CR: 0.5, TWR: 1, ERR: 1, ToolRecall: 1, Quality: 0.9, Clean: false, BucketTokens: map[string]int{"memory": 10}},
	}

	summary := summarizeRecords(cfg, "all", records)
	if summary.Clean {
		t.Fatalf("expected summary to fail when any record is not clean: %+v", summary)
	}
	if !summary.QualityPass || !summary.LatencyPass {
		t.Fatalf("expected quality and latency averages to pass: %+v", summary)
	}
}

func TestParseTraceCasesJSONEnvelope(t *testing.T) {
	data := []byte(`{
		"memories": [
			{"content": "Global trace memory", "tier": "long"}
		],
		"cases": [
			{
				"id": "real-session-01",
				"scenario": "real trace",
				"query": "继续 context packer trace replay",
				"messages": [
					{"role": "user", "content": "trace replay acceptance: CMR >= 0.95"},
					{"role": "assistant", "content": "keep docs/reports evidence"}
				],
				"constraints": ["CMR >= 0.95"],
				"evidence": ["docs/reports"]
				}
			]
		}`)

	cases, memories, err := parseTraceCases(data)
	if err != nil {
		t.Fatalf("parseTraceCases() error = %v", err)
	}
	if len(cases) != 1 {
		t.Fatalf("expected 1 case, got %d", len(cases))
	}
	if len(memories) != 1 {
		t.Fatalf("expected 1 global memory, got %d", len(memories))
	}
	if cases[0].Scenario != "real trace" {
		t.Fatalf("unexpected scenario: %q", cases[0].Scenario)
	}
}

func TestParseTraceCasesJSONL(t *testing.T) {
	data := []byte(`{"id":"a","scenario":"trace_a","query":"q1","messages":[{"role":"user","content":"q1 history"}]}
{"id":"b","scenario":"trace_b","query":"q2","messages":[{"role":"user","content":"q2 history"}]}`)

	cases, memories, err := parseTraceCases(data)
	if err != nil {
		t.Fatalf("parseTraceCases() error = %v", err)
	}
	if len(memories) != 0 {
		t.Fatalf("expected no global memories, got %d", len(memories))
	}
	if len(cases) != 2 {
		t.Fatalf("expected 2 cases, got %d", len(cases))
	}
	if cases[1].ID != "b" {
		t.Fatalf("expected second case id b, got %q", cases[1].ID)
	}
}

func TestNormalizeTraceScenario(t *testing.T) {
	if got := normalizeTraceScenario("Real Trace-Case", "id"); got != "real_trace_case" {
		t.Fatalf("unexpected normalized scenario: %q", got)
	}
	if got := normalizeTraceScenario("", "id"); got != "trace" {
		t.Fatalf("expected default trace scenario, got %q", got)
	}
}
