package rag

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// TestGraphPersistence 测试知识图谱持久化
func TestGraphPersistence(t *testing.T) {
	// 创建临时数据库
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_graph.db")

	// 创建带持久化的图谱存储
	store, err := NewSQLiteStore(128, dbPath)
	if err != nil {
		t.Fatalf("create sqlite store: %v", err)
	}
	defer store.Close()

	graphStore, err := NewGraphStore(store.DB())
	if err != nil {
		t.Fatalf("create graph store: %v", err)
	}

	// 测试节点持久化
	node1 := &KnowledgeNode{
		ID:          "node_1",
		Type:        "person",
		Name:        "张三",
		Aliases:     []string{"Zhang San", "小张"},
		Description: "软件工程师",
		Importance:  0.8,
		AccessCount: 5,
		CreatedAt:   time.Now(),
		AccessedAt:  time.Now(),
		SourceChunks: []string{"chunk_1", "chunk_2"},
		Tags:        []string{"engineering", "software"},
	}

	if err := graphStore.SaveNode(node1); err != nil {
		t.Fatalf("save node: %v", err)
	}

	// 读取节点
	loadedNode, err := graphStore.LoadNode("node_1")
	if err != nil {
		t.Fatalf("load node: %v", err)
	}

	if loadedNode == nil {
		t.Fatal("loaded node is nil")
	}

	if loadedNode.Name != "张三" {
		t.Errorf("expected name '张三', got '%s'", loadedNode.Name)
	}

	if len(loadedNode.Aliases) != 2 {
		t.Errorf("expected 2 aliases, got %d", len(loadedNode.Aliases))
	}

	// 测试边持久化
	node2 := &KnowledgeNode{
		ID:         "node_2",
		Type:       "organization",
		Name:       "阿里巴巴",
		CreatedAt:  time.Now(),
		AccessedAt: time.Now(),
	}

	if err := graphStore.SaveNode(node2); err != nil {
		t.Fatalf("save node2: %v", err)
	}

	edge := &KnowledgeEdge{
		ID:        "edge_1",
		SourceID:  "node_1",
		TargetID:  "node_2",
		RelType:   "works_at",
		Weight:    0.9,
		Context:   "在阿里巴巴工作",
		Evidence:  []string{"chunk_1"},
		CreatedAt: time.Now(),
	}

	if err := graphStore.SaveEdge(edge); err != nil {
		t.Fatalf("save edge: %v", err)
	}

	// 加载所有节点和边
	allNodes, err := graphStore.LoadAllNodes()
	if err != nil {
		t.Fatalf("load all nodes: %v", err)
	}

	if len(allNodes) != 2 {
		t.Errorf("expected 2 nodes, got %d", len(allNodes))
	}

	allEdges, err := graphStore.LoadAllEdges()
	if err != nil {
		t.Fatalf("load all edges: %v", err)
	}

	if len(allEdges) != 1 {
		t.Errorf("expected 1 edge, got %d", len(allEdges))
	}

	// 测试统计
	stats, err := graphStore.Stats()
	if err != nil {
		t.Fatalf("get stats: %v", err)
	}

	if stats.NodeCount != 2 {
		t.Errorf("expected 2 nodes in stats, got %d", stats.NodeCount)
	}

	if stats.EdgeCount != 1 {
		t.Errorf("expected 1 edge in stats, got %d", stats.EdgeCount)
	}
}

// TestKnowledgeGraphWithPersistence 测试知识图谱与持久化集成
func TestKnowledgeGraphWithPersistence(t *testing.T) {
	// 创建临时数据库
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_kg.db")

	// 创建带持久化的图谱
	store, err := NewSQLiteStore(128, dbPath)
	if err != nil {
		t.Fatalf("create sqlite store: %v", err)
	}
	defer store.Close()

	graphStore, err := NewGraphStore(store.DB())
	if err != nil {
		t.Fatalf("create graph store: %v", err)
	}

	kg, err := NewKnowledgeGraphWithStore(graphStore)
	if err != nil {
		t.Fatalf("create knowledge graph with store: %v", err)
	}

	// 添加节点（应该自动持久化）
	node := &KnowledgeNode{
		ID:          generateNodeID("person", "李四"),
		Type:        "person",
		Name:        "李四",
		Description: "产品经理",
		Importance:  0.7,
		CreatedAt:   time.Now(),
		AccessedAt:  time.Now(),
	}

	if err := kg.AddNode(node); err != nil {
		t.Fatalf("add node: %v", err)
	}

	// 直接从存储验证节点已持久化
	loadedNode, err := graphStore.LoadNode(node.ID)
	if err != nil {
		t.Fatalf("load node from store: %v", err)
	}

	if loadedNode == nil {
		t.Fatal("node not persisted")
	}

	if loadedNode.Name != "李四" {
		t.Errorf("expected name '李四', got '%s'", loadedNode.Name)
	}

	// 创建新的图谱实例，验证可以从存储加载
	kg2, err := NewKnowledgeGraphWithStore(graphStore)
	if err != nil {
		t.Fatalf("create second knowledge graph: %v", err)
	}

	if len(kg2.Nodes) != 1 {
		t.Errorf("expected 1 node after reload, got %d", len(kg2.Nodes))
	}

	reloadedNode, exists := kg2.GetNode(node.ID)
	if !exists {
		t.Fatal("node not found after reload")
	}

	if reloadedNode.Name != "李四" {
		t.Errorf("expected name '李四' after reload, got '%s'", reloadedNode.Name)
	}
}

// TestRAGManagerWithGraphPersistence 测试 RAG Manager 的图谱持久化
func TestRAGManagerWithGraphPersistence(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_rag_graph.db")

	// 创建 mock embedder
	mockEmbedder := newMockEmbedder(128)

	// 创建 mock LLM provider
	mockLLM := &mockLLMProvider{
		response: `{
			"entities": [
				{"name": "测试实体", "type": "concept", "description": "测试用实体", "aliases": []}
			],
			"relations": []
		}`,
	}

	config := DefaultRAGConfig()
	config.EnableGraph = true

	// 创建带 SQLite 和 Graph 的 RAG Manager
	ragManager, err := NewRAGManagerWithSQLiteAndGraph(mockEmbedder, config, dbPath, mockLLM)
	if err != nil {
		t.Fatalf("create rag manager: %v", err)
	}
	defer ragManager.CloseStore()

	// 验证图谱已启用
	if ragManager.graph == nil {
		t.Fatal("graph should be enabled")
	}

	// 索引文档并提取实体
	ctx := context.Background()
	doc, err := ragManager.IndexTextWithGraph(ctx, "test.txt", "测试文档", "这是一个测试文档，包含测试实体。")
	if err != nil {
		t.Fatalf("index text with graph: %v", err)
	}

	if doc == nil {
		t.Fatal("indexed document is nil")
	}

	// 验证图谱有节点
	stats := ragManager.graph.Stats()
	if stats.NodeCount == 0 {
		t.Error("expected at least 1 node in graph")
	}

	// 关闭 RAG Manager
	if err := ragManager.CloseStore(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	// 重新打开，验证图谱数据已持久化
	ragManager2, err := NewRAGManagerWithSQLiteAndGraph(mockEmbedder, config, dbPath, mockLLM)
	if err != nil {
		t.Fatalf("reopen rag manager: %v", err)
	}
	defer ragManager2.CloseStore()

	if ragManager2.graph == nil {
		t.Fatal("graph should be enabled after reload")
	}

	stats2 := ragManager2.graph.Stats()
	if stats2.NodeCount != stats.NodeCount {
		t.Errorf("expected %d nodes after reload, got %d", stats.NodeCount, stats2.NodeCount)
	}
}

// mockLLMProvider 用于测试
type mockLLMProvider struct {
	response string
}

func (m *mockLLMProvider) Complete(ctx context.Context, prompt string) (string, error) {
	return m.response, nil
}
