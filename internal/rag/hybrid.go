package rag

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

type lexicalDocument struct {
	id       string
	metadata map[string]string
	terms    []string
	counts   map[string]int
}

func (r *Retriever) hybridRerank(query string, queryVec []float64, dense []SearchResult, limit int, config RetrieverConfig) []SearchResult {
	queryTerms := uniqueStrings(lexicalTerms(query))
	if len(queryTerms) == 0 {
		return dense
	}
	chunks := r.indexer.AllChunks()
	docs := make([]lexicalDocument, 0, len(chunks))
	docFreq := make(map[string]int, len(queryTerms))
	totalLength := 0
	for id, chunk := range chunks {
		if chunk == nil || (config.FilterSource != "" && chunk.Metadata["source"] != config.FilterSource) {
			continue
		}
		if shouldExcludeRAGSearchResult(SearchResult{ID: id, Metadata: chunk.Metadata}) {
			continue
		}
		terms := lexicalTerms(chunk.Metadata["title"] + "\n" + chunk.Content)
		counts := make(map[string]int)
		for _, term := range terms {
			counts[term]++
		}
		for _, term := range queryTerms {
			if counts[term] > 0 {
				docFreq[term]++
			}
		}
		docs = append(docs, lexicalDocument{id: id, metadata: chunk.Metadata, terms: terms, counts: counts})
		totalLength += len(terms)
	}
	if len(docs) == 0 {
		return dense
	}
	avgLength := float64(totalLength) / float64(len(docs))
	if avgLength < 1 {
		avgLength = 1
	}
	lexicalScores := make(map[string]float64, len(docs))
	maxLexical := 0.0
	const k1, b = 1.2, 0.75
	for _, doc := range docs {
		score := 0.0
		for _, term := range queryTerms {
			tf := float64(doc.counts[term])
			if tf == 0 {
				continue
			}
			df := float64(docFreq[term])
			idf := math.Log(1 + (float64(len(docs))-df+0.5)/(df+0.5))
			lengthNorm := 1 - b + b*float64(len(doc.terms))/avgLength
			score += idf * (tf * (k1 + 1)) / (tf + k1*lengthNorm)
		}
		if score > 0 {
			lexicalScores[doc.id] = score
			if score > maxLexical {
				maxLexical = score
			}
		}
	}

	denseScores := make(map[string]SearchResult, len(dense))
	for _, result := range dense {
		denseScores[result.ID] = result
	}
	ids := make(map[string]struct{}, len(dense)+len(lexicalScores))
	for id := range denseScores {
		ids[id] = struct{}{}
	}
	for id := range lexicalScores {
		ids[id] = struct{}{}
	}

	weight := config.DenseWeight
	results := make([]SearchResult, 0, len(ids))
	for id := range ids {
		chunk := chunks[id]
		if chunk == nil {
			continue
		}
		denseScore := 0.0
		if result, ok := denseScores[id]; ok {
			denseScore = result.Score
		} else if entry, ok := r.store.Get(id); ok {
			denseScore = cosineSimilarity(queryVec, entry.Vector)
		}
		if denseScore < 0 {
			denseScore = 0
		}
		lexicalScore := 0.0
		if maxLexical > 0 {
			lexicalScore = lexicalScores[id] / maxLexical
		}
		results = append(results, SearchResult{
			ID:       id,
			Score:    weight*denseScore + (1-weight)*lexicalScore,
			Metadata: copyMap(chunk.Metadata),
		})
	}
	sort.SliceStable(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	return results
}

func lexicalTerms(text string) []string {
	text = strings.ToLower(text)
	terms := make([]string, 0, len(text)/4)
	word := make([]rune, 0, 16)
	flush := func() {
		if len(word) > 0 {
			terms = append(terms, string(word))
			word = word[:0]
		}
	}
	var previousHan rune
	for _, r := range text {
		switch {
		case unicode.Is(unicode.Han, r):
			flush()
			terms = append(terms, string(r))
			if previousHan != 0 {
				terms = append(terms, string([]rune{previousHan, r}))
			}
			previousHan = r
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-':
			previousHan = 0
			word = append(word, r)
		default:
			previousHan = 0
			flush()
		}
	}
	flush()
	return terms
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
