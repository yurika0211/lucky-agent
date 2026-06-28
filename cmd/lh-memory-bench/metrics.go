package main

import (
	"math"
	"strings"

	"github.com/yurika0211/luckyagent/internal/memory"
)

type entryMetrics struct {
	ResultCount    int
	ResultIDs      []string
	ExpectedCount  int
	HitCount       int
	ForbidCount    int
	ForbidHitCount int
	RecallAtK      float64
	PrecisionAtK   float64
	MRR            float64
	NDCGAtK        float64
	NoiseAtK       float64
	StaleHitRate   float64
}

func evaluateEntries(query benchQuery, entries []memory.Entry, limit int) entryMetrics {
	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}
	metrics := entryMetrics{
		ResultCount:   len(entries),
		ExpectedCount: len(query.WantIDs),
		ForbidCount:   len(query.ForbidIDs),
	}
	want := stringSet(query.WantIDs)
	forbid := stringSet(query.ForbidIDs)
	seenHits := map[string]struct{}{}

	dcg := 0.0
	for i, entry := range entries {
		id := strings.TrimSpace(entry.ID)
		if id == "" {
			continue
		}
		metrics.ResultIDs = append(metrics.ResultIDs, id)
		if _, ok := want[id]; ok {
			if _, seen := seenHits[id]; !seen {
				metrics.HitCount++
				seenHits[id] = struct{}{}
			}
			if metrics.MRR == 0 {
				metrics.MRR = 1.0 / float64(i+1)
			}
			dcg += 1.0 / math.Log2(float64(i)+2)
		}
		if _, ok := forbid[id]; ok {
			metrics.ForbidHitCount++
		}
	}

	if metrics.ExpectedCount > 0 {
		metrics.RecallAtK = float64(metrics.HitCount) / float64(metrics.ExpectedCount)
		idealHits := metrics.ExpectedCount
		if idealHits > len(entries) {
			idealHits = len(entries)
		}
		ideal := 0.0
		for i := 0; i < idealHits; i++ {
			ideal += 1.0 / math.Log2(float64(i)+2)
		}
		if ideal > 0 {
			metrics.NDCGAtK = dcg / ideal
		}
	} else {
		metrics.RecallAtK = 1
		metrics.MRR = 1
		metrics.NDCGAtK = 1
	}

	if len(entries) > 0 {
		metrics.PrecisionAtK = float64(metrics.HitCount) / float64(len(entries))
		noise := len(entries) - metrics.HitCount
		if noise < 0 {
			noise = 0
		}
		metrics.NoiseAtK = float64(noise) / float64(len(entries))
	}
	if metrics.ForbidCount > 0 {
		metrics.StaleHitRate = float64(metrics.ForbidHitCount) / float64(metrics.ForbidCount)
	}
	return metrics
}

func stringSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out[value] = struct{}{}
		}
	}
	return out
}

func countHits(got, want []string) int {
	gotSet := stringSet(got)
	count := 0
	for _, value := range want {
		if _, ok := gotSet[strings.TrimSpace(value)]; ok {
			count++
		}
	}
	return count
}
