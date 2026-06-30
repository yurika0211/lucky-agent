package rag_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent/internal/embedder"
	"github.com/yurika0211/luckyagent/internal/rag"
)

// MockLLMProvider 模拟 LLM 提供者（用于测试）
type MockLLMProvider struct{}

func (m *MockLLMProvider) Complete(ctx context.Context, prompt string) (string, error) {
	// 模拟实体提取响应
	return `{
		"entities": [
			{"name": "张三", "type": "person", "description": "软件工程师", "aliases": ["Zhang San"]},
			{"name": "阿里巴巴", "type": "organization", "description": "中国科技公司", "aliases": ["Alibaba", "阿里"]},
			{"name": "杭州", "type": "location", "description": "浙江省省会城市", "aliases": ["Hangzhou"]}
		],
		"relations": [
			{"source": "张三", "target": "阿里巴巴", "type": "works_at", "context": "在阿里巴巴担任工程师"},
			{"source": "阿里巴巴", "target": "杭州", "type": "located_in", "context": "总部位于杭州"}
		]
	}`, nil
}

// Example_graphRAGBasicUsage 展示 Graph RAG 的基本用法
func Example_graphRAGBasicUsage() {
	// 1. 创建 embedder（这里用 mock）
	embedder := embedder.NewMockEmbedder(128)

	// 2. 创建 LLM Provider（用于实体提取）
	llmProvider := &MockLLMProvider{}

	// 3. 创建带 Graph 的 RAG Manager
	config := rag.DefaultRAGConfig()
	config.EnableGraph = true

	ragManager := rag.NewRAGManagerWithGraph(embedder, config, llmProvider)
	defer ragManager.CloseStore()

	// 4. 索引文档（同时构建知识图谱）
	ctx := context.Background()
	doc, err := ragManager.IndexFileWithGraph(ctx, "test_doc.md")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	fmt.Printf("Indexed document: %s\n", doc.ID)

	// 5. 使用 Graph RAG 检索
	result, err := ragManager.SearchWithGraph(ctx, "张三在哪个城市工作？")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	// 6. 查看结果
	fmt.Printf("\n=== Vector Search Results ===\n")
	for i, chunk := range result.ChunkResults {
		fmt.Printf("%d. Score: %.2f, Content: %s\n", i+1, chunk.Score, chunk.Content[:50])
	}

	fmt.Printf("\n=== Graph Activation Results ===\n")
	for i, node := range result.ActivatedNodes {
		fmt.Printf("%d. %s (%s) - Score: %.2f\n", i+1, node.Node.Name, node.Node.Type, node.Score)
		if len(node.Paths) > 0 {
			fmt.Printf("   Paths:\n")
			for _, path := range node.Paths {
				fmt.Printf("   - %s -[%s]-> %s\n", path.FromID[:8], path.RelType, path.ToID[:8])
			}
		}
	}

	fmt.Printf("\n=== Fused Context ===\n")
	fmt.Println(result.Context)

	// Output: Example output
}

// TestGraphRAGIntegration 集成测试
func TestGraphRAGIntegration(t *testing.T) {
	// 创建测试环境
	embedder := embedder.NewMockEmbedder(128)
	llmProvider := &MockLLMProvider{}

	config := rag.DefaultRAGConfig()
	config.EnableGraph = true

	ragManager := rag.NewRAGManagerWithGraph(embedder, config, llmProvider)
	defer ragManager.CloseStore()

	// 测试图谱是否启用
	if ragManager.Graph() == nil {
		t.Fatal("Graph should be enabled")
	}

	// 测试索引
	ctx := context.Background()
	_, err := ragManager.IndexText("test", "Test Doc", "张三在阿里巴巴工作，阿里巴巴总部在杭州。")
	if err != nil {
		t.Fatalf("IndexText failed: %v", err)
	}

	// 测试图谱统计
	stats := ragManager.Graph().Stats()
	t.Logf("Graph stats: %d nodes, %d edges", stats.NodeCount, stats.EdgeCount)

	// 测试 Graph RAG 检索
	result, err := ragManager.SearchWithGraph(ctx, "张三在哪里工作")
	if err != nil {
		t.Fatalf("SearchWithGraph failed: %v", err)
	}

	if len(result.ChunkResults) == 0 && len(result.ActivatedNodes) == 0 {
		t.Error("Expected some results from Graph RAG")
	}

	t.Logf("Vector results: %d, Graph nodes: %d", len(result.ChunkResults), len(result.ActivatedNodes))
}

// TestGraphActivation 测试激活扩散
func TestGraphActivation(t *testing.T) {
	// 创建知识图谱
	graph := rag.NewKnowledgeGraph()

	now := time.Now()

	// 添加节点
	zhangsan := &rag.KnowledgeNode{
		ID:          "node_1",
		Type:        "person",
		Name:        "张三",
		Description: "软件工程师",
		Importance:  0.8,
		CreatedAt:   now,
		AccessedAt:  now,
	}
	alibaba := &rag.KnowledgeNode{
		ID:          "node_2",
		Type:        "organization",
		Name:        "阿里巴巴",
		Description: "科技公司",
		Importance:  0.9,
		CreatedAt:   now,
		AccessedAt:  now,
	}
	hangzhou := &rag.KnowledgeNode{
		ID:          "node_3",
		Type:        "location",
		Name:        "杭州",
		Description: "城市",
		Importance:  0.7,
		CreatedAt:   now,
		AccessedAt:  now,
	}

	graph.AddNode(zhangsan)
	graph.AddNode(alibaba)
	graph.AddNode(hangzhou)

	// 添加边
	edge1 := &rag.KnowledgeEdge{
		ID:       "edge_1",
		SourceID: "node_1",
		TargetID: "node_2",
		RelType:  "works_at",
		Weight:   0.9,
		Context:  "在阿里巴巴工作",
	}
	edge2 := &rag.KnowledgeEdge{
		ID:       "edge_2",
		SourceID: "node_2",
		TargetID: "node_3",
		RelType:  "located_in",
		Weight:   0.8,
		Context:  "总部在杭州",
	}

	graph.AddEdge(edge1)
	graph.AddEdge(edge2)

	// 测试激活扩散
	opts := rag.DefaultGraphActivationOptions()
	opts.MaxDepth = 2
	opts.MaxSeeds = 5

	// 查询 "张三"，应该激活 阿里巴巴（1跳）和 杭州（2跳）
	results := graph.ActivateGraph("张三", opts)

	if len(results) == 0 {
		t.Fatal("Expected some activated nodes")
	}

	t.Logf("Activated %d nodes", len(results))
	for i, result := range results {
		t.Logf("%d. %s (%s) - Score: %.3f, Paths: %d",
			i+1, result.Node.Name, result.Node.Type, result.Score, len(result.Paths))
	}

	// 验证张三直接激活
	found := false
	for _, result := range results {
		if result.Node.Name == "张三" {
			found = true
			if result.DirectScore <= 0 {
				t.Error("张三 should have direct activation score")
			}
		}
	}
	if !found {
		t.Error("张三 should be activated")
	}

	// 验证阿里巴巴和杭州通过图扩散激活
	foundAlibaba := false
	foundHangzhou := false
	for _, result := range results {
		if result.Node.Name == "阿里巴巴" {
			foundAlibaba = true
			if result.Components.GraphBoost <= 0 {
				t.Error("阿里巴巴 should have graph boost")
			}
		}
		if result.Node.Name == "杭州" {
			foundHangzhou = true
			if result.Components.GraphBoost <= 0 {
				t.Error("杭州 should have graph boost")
			}
		}
	}

	if !foundAlibaba {
		t.Error("阿里巴巴 should be activated via graph spread")
	}
	if !foundHangzhou {
		t.Error("杭州 should be activated via graph spread (2-hop)")
	}
}
