package tool

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/yurika0211/luckyharness/internal/rag"
	"github.com/yurika0211/luckyharness/internal/utils"
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
	if strings.TrimSpace(query) == "" {
		return "", fmt.Errorf("query is required")
	}

	topK := 5
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

	prev := s.manager.RetrieverConfig()
	cfg := prev
	cfg.TopK = topK
	s.manager.UpdateRetrieverConfig(cfg)
	defer s.manager.UpdateRetrieverConfig(prev)

	results, err := s.manager.Search(context.Background(), query)
	if err != nil {
		return "", err
	}
	if len(results) == 0 {
		return fmt.Sprintf("没有找到关于「%s」的 RAG 结果", query), nil
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
		sb.WriteString(fmt.Sprintf("%d. [%0.2f] %s — %s\n", i+1, r.Score, title, content))
	}
	return strings.TrimSpace(sb.String()), nil
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
