package sdk

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/yurika0211/luckyagent/internal/rag"
)

// DocumentInfo is a lightweight indexed-document snapshot.
type DocumentInfo struct {
	ID        string
	Path      string
	Title     string
	Chunks    int
	IndexedAt time.Time
}

// RAGStats summarizes the embed-local knowledge base.
type RAGStats struct {
	DocumentCount int
	ChunkCount    int
	TotalTokens   int
	LastIndexed   time.Time
}

// RAGHit is one retrieval result from the knowledge base.
type RAGHit struct {
	ChunkID   string
	Content   string
	Score     float64
	DocTitle  string
	DocSource string
	Metadata  map[string]string
}

// RAGSearchOptions tunes a single SearchRAG call. Zero values use runtime defaults.
type RAGSearchOptions struct {
	TopK     int
	MinScore float64
	// Source filters results to one document source identifier when non-empty.
	Source string
}

func (a *Agent) ragOrErr() (*rag.RAGManager, error) {
	if err := a.require(); err != nil {
		return nil, err
	}
	mgr := a.inner.RAG()
	if mgr == nil {
		return nil, fmt.Errorf("sdk: rag is not initialized")
	}
	return mgr, nil
}

// IndexText indexes free-form text into the embed-local knowledge base.
// source is a stable host identifier (for example "wiki:onboarding"); title is display-only.
func (a *Agent) IndexText(ctx context.Context, source, title, content string) (*DocumentInfo, error) {
	mgr, err := a.ragOrErr()
	if err != nil {
		return nil, err
	}
	source = strings.TrimSpace(source)
	content = strings.TrimSpace(content)
	if source == "" {
		return nil, fmt.Errorf("sdk: rag source is empty")
	}
	if content == "" {
		return nil, fmt.Errorf("sdk: rag content is empty")
	}
	if strings.TrimSpace(title) == "" {
		title = source
	}
	if ctx == nil {
		ctx = context.Background()
	}
	doc, err := mgr.IndexTextContext(ctx, source, title, content)
	if err != nil {
		return nil, fmt.Errorf("sdk: index text: %w", err)
	}
	return mapDocument(doc), nil
}

// IndexFile indexes a local file path into the knowledge base.
func (a *Agent) IndexFile(ctx context.Context, path string) (*DocumentInfo, error) {
	mgr, err := a.ragOrErr()
	if err != nil {
		return nil, err
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("sdk: rag path is empty")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	doc, err := mgr.IndexFileContext(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("sdk: index file: %w", err)
	}
	return mapDocument(doc), nil
}

// IndexDirectory recursively indexes supported files under dir.
func (a *Agent) IndexDirectory(ctx context.Context, dir string) ([]DocumentInfo, error) {
	mgr, err := a.ragOrErr()
	if err != nil {
		return nil, err
	}
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, fmt.Errorf("sdk: rag dir is empty")
	}
	// IndexDirectory on the manager is not context-aware yet; honor cancel before start.
	if ctx != nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
	}
	docs, err := mgr.IndexDirectory(dir)
	if err != nil {
		return nil, fmt.Errorf("sdk: index directory: %w", err)
	}
	out := make([]DocumentInfo, 0, len(docs))
	for _, doc := range docs {
		if info := mapDocument(doc); info != nil {
			out = append(out, *info)
		}
	}
	return out, nil
}

// SearchRAG runs semantic retrieval against the embed-local knowledge base.
func (a *Agent) SearchRAG(ctx context.Context, query string, opts *RAGSearchOptions) ([]RAGHit, error) {
	mgr, err := a.ragOrErr()
	if err != nil {
		return nil, err
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("sdk: rag query is empty")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var results []rag.RetrievalResult
	if opts == nil {
		results, err = mgr.Search(ctx, query)
	} else {
		results, err = mgr.SearchWithOptions(ctx, query, rag.SearchOptions{
			TopK:         opts.TopK,
			MinScore:     opts.MinScore,
			FilterSource: strings.TrimSpace(opts.Source),
		})
	}
	if err != nil {
		return nil, fmt.Errorf("sdk: search rag: %w", err)
	}
	out := make([]RAGHit, 0, len(results))
	for _, r := range results {
		meta := r.Metadata
		if meta != nil {
			copied := make(map[string]string, len(meta))
			for k, v := range meta {
				copied[k] = v
			}
			meta = copied
		}
		out = append(out, RAGHit{
			ChunkID:   r.ChunkID,
			Content:   r.Content,
			Score:     r.Score,
			DocTitle:  r.DocTitle,
			DocSource: r.DocSource,
			Metadata:  meta,
		})
	}
	return out, nil
}

// RemoveDocument deletes one indexed document by id.
func (a *Agent) RemoveDocument(docID string) (bool, error) {
	mgr, err := a.ragOrErr()
	if err != nil {
		return false, err
	}
	docID = strings.TrimSpace(docID)
	if docID == "" {
		return false, fmt.Errorf("sdk: rag doc id is empty")
	}
	return mgr.RemoveDocument(docID), nil
}

// ListDocuments returns indexed document ids.
func (a *Agent) ListDocuments() ([]string, error) {
	mgr, err := a.ragOrErr()
	if err != nil {
		return nil, err
	}
	return mgr.ListDocuments(), nil
}

// RAGStats returns knowledge-base counters.
func (a *Agent) RAGStats() (RAGStats, error) {
	mgr, err := a.ragOrErr()
	if err != nil {
		return RAGStats{}, err
	}
	st := mgr.Stats()
	return RAGStats{
		DocumentCount: st.DocumentCount,
		ChunkCount:    st.ChunkCount,
		TotalTokens:   st.TotalTokens,
		LastIndexed:   st.LastIndexed,
	}, nil
}

func mapDocument(doc *rag.Document) *DocumentInfo {
	if doc == nil {
		return nil
	}
	return &DocumentInfo{
		ID:        doc.ID,
		Path:      doc.Path,
		Title:     doc.Title,
		Chunks:    len(doc.Chunks),
		IndexedAt: doc.IndexedAt,
	}
}
