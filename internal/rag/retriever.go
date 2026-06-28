package rag

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	embedderpkg "github.com/yurika0211/luckyharness/internal/embedder"
	"github.com/yurika0211/luckyharness/internal/logger"
)

var ragRetrievalLogMu sync.Mutex

// RetrievalResult is a result from the RAG retriever.
type RetrievalResult struct {
	ChunkID   string
	Content   string
	Score     float64
	Metadata  map[string]string
	DocTitle  string
	DocSource string
}

// RetrieverConfig holds retriever configuration.
type RetrieverConfig struct {
	TopK         int     // number of results to return (default 5)
	MinScore     float64 // minimum similarity score (default 0.5)
	UseMMR       bool    // use Maximal Marginal Relevance for diversity
	MMRLambda    float64 // MMR trade-off: 0=max diversity, 1=max relevance (default 0.5)
	FilterSource string  // filter by source
}

func DefaultRetrieverConfig() RetrieverConfig {
	return RetrieverConfig{
		TopK:      5,
		MinScore:  0.3,
		UseMMR:    false,
		MMRLambda: 0.5,
	}
}

// Retriever searches the vector store and returns relevant chunks.
type Retriever struct {
	store    VectorStoreBackend
	indexer  *Indexer
	embedder embedderpkg.Embedder
	config   RetrieverConfig
}

func NewRetriever(store VectorStoreBackend, indexer *Indexer, embedder embedderpkg.Embedder, config RetrieverConfig) *Retriever {
	if config.TopK <= 0 {
		config.TopK = 5
	}
	if config.MinScore <= 0 {
		config.MinScore = 0.3
	}
	if config.MMRLambda <= 0 {
		config.MMRLambda = 0.5
	}
	return &Retriever{
		store:    store,
		indexer:  indexer,
		embedder: embedder,
		config:   config,
	}
}

// NewRetrieverWithBackend creates a retriever with a VectorStoreBackend (alias for NewRetriever).
func NewRetrieverWithBackend(store VectorStoreBackend, indexer *Indexer, embedder embedderpkg.Embedder, config RetrieverConfig) *Retriever {
	return NewRetriever(store, indexer, embedder, config)
}

// Search queries the knowledge base and returns relevant chunks.
func (r *Retriever) Search(ctx context.Context, query string) ([]RetrievalResult, error) {
	start := time.Now()

	// Embed the query
	queryVec, err := r.embedder.Embed(ctx, query)
	if err != nil {
		durationMs := time.Since(start).Milliseconds()
		logger.Warn("rag retrieval failed",
			"query", query,
			"query_len", len(query),
			"top_k", r.config.TopK,
			"min_score", r.config.MinScore,
			"use_mmr", r.config.UseMMR,
			"filter_source", r.config.FilterSource,
			"duration_ms", durationMs,
			"error", err,
		)
		appendRAGRetrievalFileLog(map[string]any{
			"status":        "failed",
			"query":         query,
			"query_len":     len(query),
			"top_k":         r.config.TopK,
			"min_score":     r.config.MinScore,
			"use_mmr":       r.config.UseMMR,
			"filter_source": r.config.FilterSource,
			"duration_ms":   durationMs,
			"error":         err.Error(),
		})
		return nil, fmt.Errorf("embed query: %w", err)
	}

	// Search with a larger pool for MMR
	fetchK := r.config.TopK
	if r.config.UseMMR {
		fetchK = r.config.TopK * 4 // fetch more candidates for MMR reranking
	}

	var results []SearchResult
	if r.config.FilterSource != "" {
		results = r.store.SearchWithFilter(queryVec, fetchK, "source", r.config.FilterSource)
	} else {
		results = r.store.Search(queryVec, fetchK)
	}

	// Filter by minimum score
	var filtered []SearchResult
	for _, sr := range results {
		if shouldExcludeRAGSearchResult(sr) {
			continue
		}
		if sr.Score >= r.config.MinScore {
			filtered = append(filtered, sr)
		}
	}

	// MMR reranking
	if r.config.UseMMR && len(filtered) > r.config.TopK {
		filtered = r.mmrRerank(queryVec, filtered)
	}

	// Limit to TopK
	if len(filtered) > r.config.TopK {
		filtered = filtered[:r.config.TopK]
	}

	// Enrich with chunk content
	out := make([]RetrievalResult, len(filtered))
	for i, sr := range filtered {
		chunk, _ := r.indexer.GetChunk(sr.ID)
		content := ""
		docTitle := ""
		docSource := ""
		if chunk != nil {
			content = chunk.Content
			docTitle = chunk.Metadata["title"]
			docSource = chunk.Metadata["source"]
		} else {
			// Fallback to vector metadata when chunk cache is unavailable.
			docTitle = sr.Metadata["title"]
			docSource = sr.Metadata["source"]
		}
		if docTitle == "" {
			docTitle = docSource
		}
		if content == "" {
			content = "(chunk content unavailable; reindex may be required)"
		}
		out[i] = RetrievalResult{
			ChunkID:   sr.ID,
			Content:   content,
			Score:     sr.Score,
			Metadata:  sr.Metadata,
			DocTitle:  docTitle,
			DocSource: docSource,
		}
	}

	durationMs := time.Since(start).Milliseconds()
	logger.Info("rag retrieval completed",
		"query", query,
		"query_len", len(query),
		"candidates", len(results),
		"matched", len(filtered),
		"returned", len(out),
		"top_k", r.config.TopK,
		"min_score", r.config.MinScore,
		"use_mmr", r.config.UseMMR,
		"filter_source", r.config.FilterSource,
		"duration_ms", durationMs,
	)
	appendRAGRetrievalFileLog(map[string]any{
		"status":        "completed",
		"query":         query,
		"query_len":     len(query),
		"candidates":    len(results),
		"matched":       len(filtered),
		"returned":      len(out),
		"top_k":         r.config.TopK,
		"min_score":     r.config.MinScore,
		"use_mmr":       r.config.UseMMR,
		"filter_source": r.config.FilterSource,
		"duration_ms":   durationMs,
	})

	return out, nil
}

func shouldExcludeRAGSearchResult(sr SearchResult) bool {
	if includeGeneratedRAGSources() {
		return false
	}
	source := strings.TrimSpace(sr.Metadata["source"])
	return strings.HasPrefix(source, "conversation/final/")
}

func includeGeneratedRAGSources() bool {
	value := strings.TrimSpace(os.Getenv("LH_RAG_INCLUDE_GENERATED"))
	if value == "" {
		return false
	}
	switch strings.ToLower(value) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func appendRAGRetrievalFileLog(fields map[string]any) {
	logPath := os.Getenv("LH_RAG_RETRIEVAL_LOG")
	if logPath == "" {
		return
	}

	fields["timestamp"] = time.Now().Format(time.RFC3339Nano)
	data, err := json.Marshal(fields)
	if err != nil {
		logger.Warn("rag retrieval file log marshal failed", "error", err)
		return
	}
	data = append(data, '\n')

	ragRetrievalLogMu.Lock()
	defer ragRetrievalLogMu.Unlock()

	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		logger.Warn("rag retrieval file log open failed", "path", logPath, "error", err)
		return
	}
	defer f.Close()

	if _, err := f.Write(data); err != nil {
		logger.Warn("rag retrieval file log write failed", "path", logPath, "error", err)
	}
}

// mmrRerank applies Maximal Marginal Relevance to diversify results.
func (r *Retriever) mmrRerank(queryVec []float64, candidates []SearchResult) []SearchResult {
	lambda := r.config.MMRLambda
	selected := make([]SearchResult, 0, r.config.TopK)
	remaining := make([]SearchResult, len(candidates))
	copy(remaining, candidates)

	for len(selected) < r.config.TopK && len(remaining) > 0 {
		bestIdx := 0
		bestScore := math.Inf(-1)

		for i, cand := range remaining {
			// Relevance component
			relevance := cand.Score

			// Diversity component: max similarity to already selected
			maxSim := 0.0
			if len(selected) > 0 {
				candVec, exists := r.store.Get(cand.ID)
				if exists {
					for _, sel := range selected {
						selVec, selExists := r.store.Get(sel.ID)
						if selExists {
							sim := cosineSimilarity(candVec.Vector, selVec.Vector)
							if sim > maxSim {
								maxSim = sim
							}
						}
					}
				}
			}

			// MMR score = λ * relevance - (1-λ) * max_similarity
			mmrScore := lambda*relevance - (1-lambda)*maxSim
			if mmrScore > bestScore {
				bestScore = mmrScore
				bestIdx = i
			}
		}

		selected = append(selected, remaining[bestIdx])
		remaining = append(remaining[:bestIdx], remaining[bestIdx+1:]...)
	}

	return selected
}

// BuildContext assembles retrieved chunks into a context string for the agent.
func (r *Retriever) BuildContext(results []RetrievalResult) string {
	if len(results) == 0 {
		return ""
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	context := "## Retrieved Knowledge\n\n"
	for i, res := range results {
		context += fmt.Sprintf("### Source %d: %s (score: %.2f)\n", i+1, res.DocTitle, res.Score)
		context += res.Content + "\n\n"
	}

	return context
}

// UpdateConfig updates the retriever configuration.
func (r *Retriever) UpdateConfig(config RetrieverConfig) {
	if config.TopK > 0 {
		r.config.TopK = config.TopK
	}
	if config.MinScore > 0 {
		r.config.MinScore = config.MinScore
	}
	r.config.UseMMR = config.UseMMR
	if config.MMRLambda > 0 {
		r.config.MMRLambda = config.MMRLambda
	}
	r.config.FilterSource = config.FilterSource
}

// Config returns the current retriever configuration.
func (r *Retriever) Config() RetrieverConfig {
	return r.config
}
