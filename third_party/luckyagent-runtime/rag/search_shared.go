package rag

import "sort"

// rankEntries computes cosine similarity against query and returns topK results.
// When filterKey is non-empty, only entries whose metadata[filterKey] == filterValue are considered.
func rankEntries(entries map[string]*VectorEntry, query []float64, topK int, filterKey, filterValue string) []SearchResult {
	if topK <= 0 || len(entries) == 0 {
		return nil
	}

	normalized := normalizeVector(query)

	type scored struct {
		entry *VectorEntry
		score float64
	}

	results := make([]scored, 0, len(entries))
	for _, e := range entries {
		if filterKey != "" {
			val, ok := e.Metadata[filterKey]
			if !ok || val != filterValue {
				continue
			}
		}
		sim := cosineSimilarity(normalized, e.Vector)
		results = append(results, scored{entry: e, score: sim})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})

	if topK > len(results) {
		topK = len(results)
	}

	out := make([]SearchResult, topK)
	for i := 0; i < topK; i++ {
		out[i] = SearchResult{
			ID:       results[i].entry.ID,
			Score:    results[i].score,
			Metadata: copyMap(results[i].entry.Metadata),
		}
	}
	return out
}
