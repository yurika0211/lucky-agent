package rag

import (
	"context"
	"fmt"
	"strings"
	"sync"

	embedderpkg "github.com/yurika0211/luckyagent/internal/embedder"
)

// VectorStoreBackend is the interface that both in-memory and persistent
// vector stores must implement. This enables swapping backends without
// changing the RAG pipeline.
type VectorStoreBackend interface {
	Dimension() int
	Len() int
	Upsert(id string, vector []float64, metadata map[string]string) error
	Delete(id string) bool
	Get(id string) (*VectorEntry, bool)
	Search(query []float64, topK int) []SearchResult
	SearchWithFilter(query []float64, topK int, filterKey, filterValue string) []SearchResult
	AllIDs() []string
	Clear()
}

// Ensure VectorStore implements VectorStoreBackend
var _ VectorStoreBackend = (*VectorStore)(nil)

// Ensure SQLiteStore implements VectorStoreBackend
var _ VectorStoreBackend = (*SQLiteStore)(nil)

// RAGManager is the top-level RAG system manager.
type RAGManager struct {
	store     VectorStoreBackend // v0.20.0: supports both in-memory and SQLite backends
	indexer   *Indexer
	retriever *Retriever
	embedder  embedderpkg.Embedder

	// Graph RAG support
	graph          *KnowledgeGraph  // 知识图谱（可选）
	graphExtractor *EntityExtractor // 实体提取器（可选）

	mu sync.RWMutex
}

type RAGConfig struct {
	EmbeddingDim    int
	RetrieverConfig RetrieverConfig
	EnableGraph     bool // 是否启用知识图谱
}

func DefaultRAGConfig() RAGConfig {
	return RAGConfig{
		EmbeddingDim:    0, // 0 = auto-detect from embedder
		RetrieverConfig: DefaultRetrieverConfig(),
	}
}

// NewRAGManager creates a new RAG system with the given embedder and in-memory store.
func NewRAGManager(embedder embedderpkg.Embedder, config RAGConfig) *RAGManager {
	dim := config.EmbeddingDim
	if dim <= 0 {
		dim = embedder.Dimension()
	}
	if dim <= 0 {
		dim = 128
	}

	store := NewVectorStore(dim)
	indexer := NewIndexer(store, embedder)
	retriever := NewRetriever(store, indexer, embedder, config.RetrieverConfig)

	return &RAGManager{
		store:     store,
		indexer:   indexer,
		retriever: retriever,
		embedder:  embedder,
	}
}

// NewRAGManagerWithSQLite creates a new RAG system with SQLite-backed persistent store.
func NewRAGManagerWithSQLite(embedder embedderpkg.Embedder, config RAGConfig, dbPath string) (*RAGManager, error) {
	dim := config.EmbeddingDim
	if dim <= 0 {
		dim = embedder.Dimension()
	}
	if dim <= 0 {
		dim = 128
	}

	store, err := NewSQLiteStore(dim, dbPath)
	if err != nil {
		return nil, fmt.Errorf("create sqlite store: %w", err)
	}

	indexer := NewIndexerWithBackend(store, embedder)

	// Load persisted documents and chunks from SQLite
	if docs, err := store.LoadDocuments(); err == nil && len(docs) > 0 {
		indexer.mu.Lock()
		for id, doc := range docs {
			indexer.documents[id] = doc
		}
		indexer.stats.DocumentCount = len(docs)
		indexer.mu.Unlock()
	}
	if chunks, err := store.LoadChunks(); err == nil && len(chunks) > 0 {
		indexer.mu.Lock()
		for id, chunk := range chunks {
			indexer.chunks[id] = chunk
		}
		indexer.stats.ChunkCount = len(chunks)
		indexer.mu.Unlock()
	}
	if stats, err := store.LoadIndexStats(); err == nil {
		indexer.mu.Lock()
		indexer.stats.Sources = stats.Sources
		if !stats.LastIndexed.IsZero() {
			indexer.stats.LastIndexed = stats.LastIndexed
		}
		indexer.mu.Unlock()
	}

	retriever := NewRetrieverWithBackend(store, indexer, embedder, config.RetrieverConfig)

	return &RAGManager{
		store:     store,
		indexer:   indexer,
		retriever: retriever,
		embedder:  embedder,
	}, nil
}

// IndexFile indexes a single file.
func (m *RAGManager) IndexFile(path string) (*Document, error) {
	return m.indexer.IndexFile(path)
}

// IndexText indexes raw text content.
func (m *RAGManager) IndexText(source, title, content string) (*Document, error) {
	return m.indexer.IndexText(source, title, content)
}

// IndexDirectory indexes all .md/.txt files in a directory.
func (m *RAGManager) IndexDirectory(dir string) ([]*Document, error) {
	return m.indexer.IndexDirectory(dir)
}

// Search queries the knowledge base.
func (m *RAGManager) Search(ctx context.Context, query string) ([]RetrievalResult, error) {
	return m.retriever.Search(ctx, query)
}

// SearchWithContext queries and returns assembled context string.
func (m *RAGManager) SearchWithContext(ctx context.Context, query string) (string, []RetrievalResult, error) {
	results, err := m.retriever.Search(ctx, query)
	if err != nil {
		return "", nil, err
	}
	context := m.retriever.BuildContext(results)
	return context, results, nil
}

// RemoveDocument removes a document from the index.
func (m *RAGManager) RemoveDocument(docID string) bool {
	return m.indexer.RemoveDocument(docID)
}

// Stats returns index statistics.
func (m *RAGManager) Stats() IndexStats {
	return m.indexer.Stats()
}

// ListDocuments returns all document IDs.
func (m *RAGManager) ListDocuments() []string {
	return m.indexer.ListDocuments()
}

// GetDocument returns a document by ID.
func (m *RAGManager) GetDocument(docID string) (*Document, bool) {
	return m.indexer.GetDocument(docID)
}

// UpdateRetrieverConfig updates the retriever configuration.
func (m *RAGManager) UpdateRetrieverConfig(config RetrieverConfig) {
	m.retriever.UpdateConfig(config)
}

// RetrieverConfig returns the current retriever configuration.
func (m *RAGManager) RetrieverConfig() RetrieverConfig {
	return m.retriever.Config()
}

// Store returns the underlying vector store backend (for advanced use).
func (m *RAGManager) Store() VectorStoreBackend {
	return m.store
}

// Indexer returns the underlying indexer (for advanced use).
func (m *RAGManager) Indexer() *Indexer {
	return m.indexer
}

// Retriever returns the underlying retriever (for advanced use).
func (m *RAGManager) Retriever() *Retriever {
	return m.retriever
}

// IsSQLite returns true if the store backend is SQLite-backed.
func (m *RAGManager) IsSQLite() bool {
	_, ok := m.store.(*SQLiteStore)
	return ok
}

// SQLiteStore returns the underlying SQLiteStore, or nil if using in-memory store.
func (m *RAGManager) SQLiteStore() *SQLiteStore {
	if s, ok := m.store.(*SQLiteStore); ok {
		return s
	}
	return nil
}

// CloseStore closes the underlying store (for SQLite, this closes the DB connection).
func (m *RAGManager) CloseStore() error {
	if s, ok := m.store.(*SQLiteStore); ok {
		return s.Close()
	}
	return nil
}

// String returns a summary of the RAG system.
func (m *RAGManager) String() string {
	stats := m.Stats()
	backend := "memory"
	if m.IsSQLite() {
		backend = "sqlite"
	}
	graphInfo := ""
	if m.graph != nil {
		graphStats := m.graph.Stats()
		graphInfo = fmt.Sprintf(", graph: %d nodes, %d edges", graphStats.NodeCount, graphStats.EdgeCount)
	}
	return fmt.Sprintf("RAGManager{docs=%d, chunks=%d, embedder=%s, dim=%d, backend=%s%s}",
		stats.DocumentCount, stats.ChunkCount, m.embedder.Name(), m.store.Dimension(), backend, graphInfo)
}

// --- Graph RAG Extensions ---

// NewRAGManagerWithGraph 创建带知识图谱的 RAG 系统
func NewRAGManagerWithGraph(embedder embedderpkg.Embedder, config RAGConfig, llmProvider LLMProvider) *RAGManager {
	m := NewRAGManager(embedder, config)
	if config.EnableGraph && llmProvider != nil {
		m.graph = NewKnowledgeGraph()
		m.graphExtractor = NewEntityExtractor(llmProvider)
	}
	return m
}

// NewRAGManagerWithSQLiteAndGraph 创建带 SQLite 和知识图谱的 RAG 系统
func NewRAGManagerWithSQLiteAndGraph(embedder embedderpkg.Embedder, config RAGConfig, dbPath string, llmProvider LLMProvider) (*RAGManager, error) {
	m, err := NewRAGManagerWithSQLite(embedder, config, dbPath)
	if err != nil {
		return nil, err
	}
	if config.EnableGraph && llmProvider != nil {
		m.graph = NewKnowledgeGraph()
		m.graphExtractor = NewEntityExtractor(llmProvider)
	}
	return m, nil
}

// EnableGraph 启用知识图谱（如果尚未启用）
func (m *RAGManager) EnableGraph(llmProvider LLMProvider) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.graph == nil {
		m.graph = NewKnowledgeGraph()
	}
	if m.graphExtractor == nil && llmProvider != nil {
		m.graphExtractor = NewEntityExtractor(llmProvider)
	}
}

// DisableGraph 禁用知识图谱
func (m *RAGManager) DisableGraph() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.graph = nil
	m.graphExtractor = nil
}

// Graph 返回知识图谱（如果启用）
func (m *RAGManager) Graph() *KnowledgeGraph {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.graph
}

// IndexFileWithGraph 索引文件并提取知识图谱
func (m *RAGManager) IndexFileWithGraph(ctx context.Context, path string) (*Document, error) {
	// 1. 执行标准的向量索引
	doc, err := m.IndexFile(path)
	if err != nil {
		return nil, err
	}

	// 2. 如果启用了图谱，提取实体和关系
	if m.graph != nil && m.graphExtractor != nil {
		m.mu.RLock()
		chunks := make([]*Chunk, 0, len(doc.Chunks))
		for _, chunkID := range doc.Chunks {
			if chunk, exists := m.indexer.chunks[chunkID]; exists {
				chunks = append(chunks, chunk)
			}
		}
		m.mu.RUnlock()

		// 批量提取（简化版：顺序提取）
		for _, chunk := range chunks {
			result, err := m.graphExtractor.ExtractEntitiesAndRelations(ctx, chunk)
			if err != nil {
				// 记录错误但不阻塞索引
				continue
			}

			// 转换为图节点和边
			nodes, edges := m.graphExtractor.ConvertToGraphNodes(result, chunk.ID)

			// 添加到知识图谱
			for _, node := range nodes {
				_ = m.graph.AddNode(node)
			}
			for _, edge := range edges {
				_ = m.graph.AddEdge(edge)
			}
		}
	}

	return doc, nil
}

// IndexTextWithGraph 索引文本并提取知识图谱
func (m *RAGManager) IndexTextWithGraph(ctx context.Context, source, title, content string) (*Document, error) {
	// 1. 执行标准的向量索引
	doc, err := m.IndexText(source, title, content)
	if err != nil {
		return nil, err
	}

	// 2. 如果启用了图谱，提取实体和关系
	if m.graph != nil && m.graphExtractor != nil {
		m.mu.RLock()
		chunks := make([]*Chunk, 0, len(doc.Chunks))
		for _, chunkID := range doc.Chunks {
			if chunk, exists := m.indexer.chunks[chunkID]; exists {
				chunks = append(chunks, chunk)
			}
		}
		m.mu.RUnlock()

		// 批量提取（简化版：顺序提取）
		for _, chunk := range chunks {
			result, err := m.graphExtractor.ExtractEntitiesAndRelations(ctx, chunk)
			if err != nil {
				// 记录错误但不阻塞索引
				continue
			}

			// 转换为图节点和边
			nodes, edges := m.graphExtractor.ConvertToGraphNodes(result, chunk.ID)

			// 添加到知识图谱
			for _, node := range nodes {
				_ = m.graph.AddNode(node)
			}
			for _, edge := range edges {
				_ = m.graph.AddEdge(edge)
			}
		}
	}

	return doc, nil
}

// GraphRAGSearchResult 融合向量和图检索的结果
type GraphRAGSearchResult struct {
	// 向量检索结果
	ChunkResults []RetrievalResult

	// 图检索结果
	ActivatedNodes []NodeActivationScore

	// 融合上下文
	Context string
}

// SearchWithGraph 使用图增强的检索
func (m *RAGManager) SearchWithGraph(ctx context.Context, query string) (*GraphRAGSearchResult, error) {
	result := &GraphRAGSearchResult{}

	// 路径1：向量检索（现有逻辑）
	chunkResults, err := m.retriever.Search(ctx, query)
	if err != nil {
		return nil, err
	}
	result.ChunkResults = chunkResults

	// 路径2：图激活（如果启用）
	if m.graph != nil {
		m.mu.RLock()
		graph := m.graph
		m.mu.RUnlock()

		if graph != nil {
			graphOpts := DefaultGraphActivationOptions()
			activatedNodes := graph.ActivateGraph(query, graphOpts)
			result.ActivatedNodes = activatedNodes
		}
	}

	// 融合上下文
	result.Context = m.buildGraphEnhancedContext(result)

	return result, nil
}

// buildGraphEnhancedContext 构建图增强的上下文
func (m *RAGManager) buildGraphEnhancedContext(result *GraphRAGSearchResult) string {
	var sb strings.Builder

	// 1. 相关文档块（来自向量检索）
	if len(result.ChunkResults) > 0 {
		sb.WriteString("## Relevant Document Chunks\n\n")
		for i, chunk := range result.ChunkResults {
			if i >= 5 { // 限制数量
				break
			}
			content := chunk.Content
			if len(content) > 200 {
				content = content[:200] + "..."
			}
			sb.WriteString(fmt.Sprintf("### Chunk %d (Score: %.2f)\n", i+1, chunk.Score))
			sb.WriteString(fmt.Sprintf("Source: %s\n", chunk.DocSource))
			sb.WriteString(fmt.Sprintf("%s\n\n", content))
		}
	}

	// 2. 相关实体和关系（来自图激活）
	if len(result.ActivatedNodes) > 0 {
		sb.WriteString("## Activated Knowledge Entities\n\n")
		for i, nodeScore := range result.ActivatedNodes {
			if i >= 8 { // 限制数量
				break
			}
			node := nodeScore.Node
			sb.WriteString(fmt.Sprintf("### %s (%s) [Score: %.2f]\n", node.Name, node.Type, nodeScore.Score))
			if node.Description != "" {
				sb.WriteString(fmt.Sprintf("%s\n", node.Description))
			}

			// 显示激活路径
			if len(nodeScore.Paths) > 0 && i < 5 {
				sb.WriteString("\nActivation paths:\n")
				for j, path := range nodeScore.Paths {
					if j >= 3 { // 限制路径数量
						break
					}
					m.mu.RLock()
					fromNode := m.graph.Nodes[path.FromID]
					m.mu.RUnlock()
					if fromNode != nil {
						sb.WriteString(fmt.Sprintf("  - %s -[%s]-> %s\n",
							fromNode.Name, path.RelType, node.Name))
					}
				}
			}
			sb.WriteString("\n")
		}
	}

	return sb.String()
}
