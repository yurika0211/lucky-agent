package main

import (
	"context"
	"fmt"
	"os"

	"github.com/yurika0211/luckyagent/internal/embedder"
	"github.com/yurika0211/luckyagent/internal/rag"
)

// SimpleLLMProvider 简单的 LLM 提供者（用于演示）
// 实际使用时应该替换为真实的 LLM 调用
type SimpleLLMProvider struct{}

func (s *SimpleLLMProvider) Complete(ctx context.Context, prompt string) (string, error) {
	// 这里应该调用真实的 LLM API
	// 现在返回一个示例响应
	return `{
		"entities": [
			{"name": "LuckyAgent", "type": "concept", "description": "Go语言编写的AI Agent框架", "aliases": ["LA", "幸运Agent"]},
			{"name": "Graph RAG", "type": "concept", "description": "知识图谱增强的检索增强生成", "aliases": ["图RAG"]},
			{"name": "记忆系统", "type": "concept", "description": "激活扩散机制的记忆管理", "aliases": ["Memory System"]},
			{"name": "SQLite", "type": "concept", "description": "轻量级数据库", "aliases": []}
		],
		"relations": [
			{"source": "LuckyAgent", "target": "Graph RAG", "type": "part_of", "context": "LuckyAgent包含Graph RAG功能"},
			{"source": "Graph RAG", "target": "记忆系统", "type": "related_to", "context": "Graph RAG借鉴了记忆系统的激活扩散机制"},
			{"source": "LuckyAgent", "target": "SQLite", "type": "related_to", "context": "LuckyAgent使用SQLite存储向量"}
		]
	}`, nil
}

func main() {
	fmt.Println("=== Graph RAG Demo ===\n")

	// 1. 创建 embedder（使用 mock）
	fmt.Println("1. Creating embedder...")
	mockEmbedder := embedder.NewMockEmbedder(128)

	// 2. 创建 LLM Provider
	fmt.Println("2. Creating LLM provider...")
	llmProvider := &SimpleLLMProvider{}

	// 3. 创建 Graph RAG Manager
	fmt.Println("3. Creating Graph RAG Manager...")
	config := rag.DefaultRAGConfig()
	config.EnableGraph = true

	ragManager := rag.NewRAGManagerWithGraph(mockEmbedder, config, llmProvider)
	defer ragManager.CloseStore()

	// 4. 索引示例文档
	fmt.Println("4. Indexing documents...")
	ctx := context.Background()

	docs := []struct {
		source  string
		title   string
		content string
	}{
		{
			"doc1.md",
			"LuckyAgent 介绍",
			"LuckyAgent 是用 Go 语言编写的 AI Agent 框架。它提供了完整的记忆系统、工具调用和多平台消息网关。",
		},
		{
			"doc2.md",
			"Graph RAG 功能",
			"LuckyAgent 现在支持 Graph RAG，这是一个知识图谱增强的检索系统。它使用激活扩散机制进行多跳推理。",
		},
		{
			"doc3.md",
			"记忆系统",
			"记忆系统实现了激活扩散机制，可以通过关系链激活相关记忆。Graph RAG 的激活算法就是从记忆系统移植过来的。",
		},
		{
			"doc4.md",
			"存储方案",
			"LuckyAgent 使用 SQLite 作为向量存储后端，轻量级且无需额外服务。知识图谱也将存储在 SQLite 中。",
		},
	}

	for _, doc := range docs {
		_, err := ragManager.IndexTextWithGraph(ctx, doc.source, doc.title, doc.content)
		if err != nil {
			fmt.Printf("Error indexing %s: %v\n", doc.source, err)
			continue
		}
		fmt.Printf("  ✓ Indexed: %s\n", doc.title)
	}

	// 5. 显示图谱统计
	fmt.Println("\n5. Knowledge Graph Stats:")
	if graph := ragManager.Graph(); graph != nil {
		stats := graph.Stats()
		fmt.Printf("  Nodes: %d\n", stats.NodeCount)
		fmt.Printf("  Edges: %d\n", stats.EdgeCount)
	}

	// 6. 测试查询
	fmt.Println("\n6. Testing queries...\n")

	queries := []string{
		"什么是 LuckyAgent",
		"Graph RAG 使用了什么机制",
		"记忆系统和 Graph RAG 有什么关系",
	}

	for i, query := range queries {
		fmt.Printf("--- Query %d: %s ---\n", i+1, query)

		// 传统向量检索
		fmt.Println("\n[Traditional Vector RAG]")
		vectorResults, err := ragManager.Search(ctx, query)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			continue
		}
		for j, result := range vectorResults {
			if j >= 3 {
				break
			}
			fmt.Printf("  %d. Score: %.3f | %s\n", j+1, result.Score, result.DocTitle)
		}

		// Graph RAG 检索
		fmt.Println("\n[Graph RAG]")
		graphResults, err := ragManager.SearchWithGraph(ctx, query)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			continue
		}

		// 显示向量检索结果
		fmt.Println("  Vector Results:")
		for j, result := range graphResults.ChunkResults {
			if j >= 3 {
				break
			}
			fmt.Printf("    %d. Score: %.3f | %s\n", j+1, result.Score, result.DocTitle)
		}

		// 显示图激活结果
		fmt.Println("\n  Graph Activation:")
		for j, node := range graphResults.ActivatedNodes {
			if j >= 5 {
				break
			}
			fmt.Printf("    %d. %s (%s) - Score: %.3f\n",
				j+1, node.Node.Name, node.Node.Type, node.Score)

			// 显示激活路径
			if len(node.Paths) > 0 && j < 3 {
				fmt.Println("       Paths:")
				for k, path := range node.Paths {
					if k >= 2 {
						break
					}
					if graph := ragManager.Graph(); graph != nil {
						fromNode, _ := graph.GetNode(path.FromID)
						if fromNode != nil {
							fmt.Printf("       - %s -[%s]-> %s\n",
								fromNode.Name, path.RelType, node.Node.Name)
						}
					}
				}
			}
		}

		fmt.Println()
	}

	// 7. 显示融合上下文示例
	fmt.Println("\n7. Example: Fused Context\n")
	result, err := ragManager.SearchWithGraph(ctx, "Graph RAG 的原理")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(result.Context)

	fmt.Println("\n=== Demo Complete ===")
}
