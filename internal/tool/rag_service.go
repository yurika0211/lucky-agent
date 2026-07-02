package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/yurika0211/luckyagent/internal/rag"
	"github.com/yurika0211/luckyagent/internal/utils"
)

const (
	defaultRAGSearchTopK          = 5
	maxRAGSearchTopK              = 20
	maxRAGSearchQueryRunes        = 2000
	defaultRAGSearchTimeout       = 30 * time.Second
	maxRAGSearchTimeoutSeconds    = 120
	defaultRAGIndexMaxFiles       = 200
	defaultRAGIndexMaxFileBytes   = 5 * 1024 * 1024
	defaultRAGIndexMaxTotalBytes  = 100 * 1024 * 1024
	maxRAGIndexPlanPreviewEntries = 50
)

// RAGToolService implements rag_search/rag_index handlers in the tool layer.
type RAGToolService struct {
	manager *rag.RAGManager
}

// NewRAGToolService creates a tool-layer RAG service.
func NewRAGToolService(manager *rag.RAGManager) *RAGToolService {
	return &RAGToolService{manager: manager}
}

func (s *RAGToolService) HandleSearch(args map[string]any) (string, error) {
	if s == nil || s.manager == nil {
		return "", fmt.Errorf("rag manager not initialized")
	}
	query, _ := args["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return "", fmt.Errorf("query is required")
	}
	if len([]rune(query)) > maxRAGSearchQueryRunes {
		return "", fmt.Errorf("query exceeds %d rune limit", maxRAGSearchQueryRunes)
	}

	topK := defaultRAGSearchTopK
	if raw, ok := args["top_k"]; ok {
		switch v := raw.(type) {
		case float64:
			if int(v) > 0 {
				topK = int(v)
			}
		case int:
			if v > 0 {
				topK = v
			}
		}
	}
	if topK > maxRAGSearchTopK {
		return "", fmt.Errorf("top_k exceeds maximum %d", maxRAGSearchTopK)
	}

	timeout := defaultRAGSearchTimeout
	if raw, ok := numberArg(args["timeout_seconds"]); ok {
		if raw <= 0 || raw > maxRAGSearchTimeoutSeconds {
			return "", fmt.Errorf("timeout_seconds must be between 1 and %d", maxRAGSearchTimeoutSeconds)
		}
		timeout = time.Duration(raw * float64(time.Second))
	}
	var minScore float64
	if raw, ok := numberArg(args["min_score"]); ok {
		if raw < 0 || raw > 1 {
			return "", fmt.Errorf("min_score must be between 0 and 1")
		}
		minScore = raw
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	results, err := s.manager.SearchWithOptions(ctx, query, rag.SearchOptions{
		TopK:     topK,
		MinScore: minScore,
	})
	if err != nil {
		return "", err
	}
	format := strings.ToLower(strings.TrimSpace(stringArg(args["format"])))
	if format == "" {
		format = "text"
	}
	if format != "text" && format != "json" {
		return "", fmt.Errorf("format must be text or json")
	}
	if format == "json" {
		return formatRAGSearchJSON(query, topK, minScore, results)
	}
	if len(results) == 0 {
		return fmt.Sprintf("没有找到关于「%s」的 RAG 结果；请确认已运行 rag_index，当前 top_k=%d min_score=%0.2f", query, topK, minScore), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("找到 %d 条关于「%s」的知识片段：\n", len(results), query))
	for i, r := range results {
		title := strings.TrimSpace(r.DocTitle)
		if title == "" {
			title = strings.TrimSpace(r.DocSource)
		}
		if title == "" {
			title = "(unknown source)"
		}
		content := utils.Truncate(strings.TrimSpace(r.Content), 160)
		ref := ragResultRef(r)
		if ref != "" {
			title = title + " (" + ref + ")"
		}
		sb.WriteString(fmt.Sprintf("%d. [%0.2f] %s — %s\n", i+1, r.Score, title, content))
	}
	return strings.TrimSpace(sb.String()), nil
}

type ragSearchJSONResult struct {
	Score      float64           `json:"score"`
	DocID      string            `json:"doc_id,omitempty"`
	ChunkID    string            `json:"chunk_id"`
	ChunkIndex string            `json:"chunk_index,omitempty"`
	Title      string            `json:"title,omitempty"`
	Source     string            `json:"source,omitempty"`
	Content    string            `json:"content"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

type ragSearchJSONResponse struct {
	Query    string                `json:"query"`
	TopK     int                   `json:"top_k"`
	MinScore float64               `json:"min_score,omitempty"`
	Count    int                   `json:"count"`
	Results  []ragSearchJSONResult `json:"results"`
}

func formatRAGSearchJSON(query string, topK int, minScore float64, results []rag.RetrievalResult) (string, error) {
	payload := ragSearchJSONResponse{
		Query:    query,
		TopK:     topK,
		MinScore: minScore,
		Count:    len(results),
		Results:  make([]ragSearchJSONResult, 0, len(results)),
	}
	for _, r := range results {
		payload.Results = append(payload.Results, ragSearchJSONResult{
			Score:      r.Score,
			DocID:      r.Metadata["doc_id"],
			ChunkID:    r.ChunkID,
			ChunkIndex: r.Metadata["chunk_i"],
			Title:      r.DocTitle,
			Source:     r.DocSource,
			Content:    r.Content,
			Metadata:   r.Metadata,
		})
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func ragResultRef(r rag.RetrievalResult) string {
	source := strings.TrimSpace(r.DocSource)
	chunk := strings.TrimSpace(r.ChunkID)
	if source == "" {
		return chunk
	}
	if chunk == "" {
		return source
	}
	return source + "#" + chunk
}

func (s *RAGToolService) HandleIndex(args map[string]any) (string, error) {
	if s == nil || s.manager == nil {
		return "", fmt.Errorf("rag manager not initialized")
	}
	path, _ := args["path"].(string)
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("path is required")
	}

	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("path not found: %w", err)
	}
	if info.IsDir() {
		docs, err := s.manager.IndexDirectory(path)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("✅ Indexed %d documents from %s", len(docs), path), nil
	}
	doc, err := s.manager.IndexFile(path)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("✅ Indexed %s (%d chunks)", doc.Title, len(doc.Chunks)), nil
}
