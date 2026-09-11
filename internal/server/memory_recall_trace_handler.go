package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/yurika0211/luckyagent/internal/memory"
)

const (
	defaultRecallTraceLimit      = 10
	maxRecallTraceLimit          = 50
	defaultRecallTraceGraphDepth = 1
	maxRecallTraceGraphDepth     = 3
)

// handleMemoryRecallTrace runs a memory search and returns the structured
// SearchTrace behind it (seeds, hop-by-hop graph spread, final results) so a
// UI can animate a recall instead of only showing the flat result list that
// /api/v1/memory/recall returns. Logic mirrors buildRecallTrace in
// internal/tool/memory_service.go.
func (s *Server) handleMemoryRecallTrace(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.sendError(w, "method not allowed", http.StatusMethodNotAllowed, "")
		return
	}

	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		s.sendError(w, "query parameter 'q' is required", http.StatusBadRequest, "")
		return
	}

	store := s.agent.Memory()
	if store == nil {
		s.sendError(w, "memory store not initialized", http.StatusServiceUnavailable, "")
		return
	}

	var tierPtr *memory.Tier
	if rawTier := strings.TrimSpace(r.URL.Query().Get("tier")); rawTier != "" {
		tier, ok := parseRecallTraceTier(rawTier)
		if !ok {
			s.sendError(w, "tier must be short, medium, or long", http.StatusBadRequest, "")
			return
		}
		tierPtr = &tier
	}

	graphDepth := boundedQueryInt(r, "graph_depth", defaultRecallTraceGraphDepth, 0, maxRecallTraceGraphDepth)
	opts := memory.SearchOptions{
		Limit:           boundedQueryInt(r, "limit", defaultRecallTraceLimit, 1, maxRecallTraceLimit),
		Category:        r.URL.Query().Get("category"),
		Tier:            tierPtr,
		IncludeGraph:    graphDepth > 0,
		GraphDepth:      graphDepth,
		Explain:         true,
		SkipAccessStats: true,
	}

	start := time.Now()
	results := store.SearchWithOptions(query, opts)
	trace := store.BuildSearchTrace(query, "search", recallTraceVaultPath(store), opts, results, time.Since(start))

	s.sendJSON(w, http.StatusOK, trace)
}

func recallTraceVaultPath(store *memory.Store) string {
	if store == nil || strings.TrimSpace(store.Dir()) == "" {
		return "~/.luckyagent/memory"
	}
	return store.Dir()
}

func parseRecallTraceTier(raw string) (memory.Tier, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "short", "短期":
		return memory.TierShort, true
	case "medium", "中期":
		return memory.TierMedium, true
	case "long", "长期":
		return memory.TierLong, true
	default:
		return memory.TierMedium, false
	}
}
