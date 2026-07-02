package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/yurika0211/luckyagent/internal/embedder"
	"github.com/yurika0211/luckyagent/internal/rag"
)

// SimpleLLMProvider 简单的 LLM 提供者（用于演示）
type SimpleLLMProvider struct{}

func (s *SimpleLLMProvider) Complete(ctx context.Context, prompt string) (string, error) {
	return `{
		"entities": [
			{"name": "LuckyAgent", "type": "concept", "description": "Go语言编写的AI Agent框架", "aliases": ["LA"]},
			{"name": "Graph RAG", "type": "concept", "description": "知识图谱增强的检索增强生成", "aliases": ["图RAG"]},
			{"name": "SQLite", "type": "technology", "description": "轻量级数据库", "aliases": []}
		],
		"relations": [
			{"source": "LuckyAgent", "target": "Graph RAG", "type": "part_of", "context": "LuckyAgent包含Graph RAG功能"},
			{"source": "Graph RAG", "target": "SQLite", "type": "uses", "context": "Graph RAG使用SQLite存储"}
		]
	}`, nil
}

func main() {
	fmt.Println("=== Graph RAG 持久化演示 ===")
	fmt.Println()

	// 数据库路径
	homeDir, _ := os.UserHomeDir()
	dbPath := filepath.Join(homeDir, ".luckyagent", "rag", "demo_graph.db")

	// 确保目录存在
	os.MkdirAll(filepath.Dir(dbPath), 0755)

	// 1. 创建 embedder（使用 mock）
	fmt.Println("1. 创建 embedder...")
	mockEmbedder := embedder.NewMockEmbedder(128)

	// 2. 创建 LLM Provider
	fmt.Println("2. 创建 LLM provider...")
	llmProvider := &SimpleLLMProvider{}

	// 3. 创建带持久化的 Graph RAG Manager
	fmt.Println("3. 创建带持久化的 Graph RAG Manager...")
	fmt.Printf("   数据库路径: %s\n", dbPath)
	config := rag.DefaultRAGConfig()
	config.EnableGraph = true

	ragManager, err := rag.NewRAGManagerWithSQLiteAndGraph(mockEmbedder, config, dbPath, llmProvider)
	if err != nil {
		fmt.Printf("❌ 错误: %v\n", err)
		os.Exit(1)
	}
	defer ragManager.CloseStore()

	// 检查是否有现有数据
	existingStats := ragManager.Graph().Stats()
	if existingStats.NodeCount > 0 {
		fmt.Printf("\n✓ 从数据库加载了现有图谱: %d 个节点, %d 条边\n", existingStats.NodeCount, existingStats.EdgeCount)
	} else {
		fmt.Println("\n✓ 数据库为空，将创建新图谱")
	}

	// 4. 索引示例文档
	fmt.Println("\n4. 索引文档并提取实体...")
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
			"Graph RAG 持久化",
			"LuckyAgent 的 Graph RAG 现在支持 SQLite 持久化。知识图谱会自动保存到数据库，重启后无需重新提取。",
		},
		{
			"doc3.md",
			"存储方案",
			"LuckyAgent 使用 SQLite 作为向量存储和图谱存储后端，轻量级且无需额外服务。",
		},
	}

	for _, doc := range docs {
		_, err := ragManager.IndexTextWithGraph(ctx, doc.source, doc.title, doc.content)
		if err != nil {
			fmt.Printf("  ❌ 索引失败 %s: %v\n", doc.source, err)
			continue
		}
		fmt.Printf("  ✓ 已索引: %s\n", doc.title)
	}

	// 5. 显示图谱统计
	fmt.Println("\n5. 知识图谱统计:")
	if graph := ragManager.Graph(); graph != nil {
		stats := graph.Stats()
		fmt.Printf("  节点数: %d\n", stats.NodeCount)
		fmt.Printf("  边数: %d\n", stats.EdgeCount)
		fmt.Println("\n  节点列表:")
		for id, node := range graph.Nodes {
			fmt.Printf("    - %s: %s (%s)\n", id, node.Name, node.Type)
		}
	}

	// 6. 测试查询
	fmt.Println("\n6. 测试 Graph RAG 检索...")
	query := "LuckyAgent 使用什么数据库"
	fmt.Printf("\n查询: %s\n", query)

	result, err := ragManager.SearchWithGraph(ctx, query)
	if err != nil {
		fmt.Printf("❌ 查询失败: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("\n激活的节点:")
	for i, node := range result.ActivatedNodes {
		if i >= 5 {
			break
		}
		fmt.Printf("  %d. %s (%s) - Score: %.3f\n",
			i+1, node.Node.Name, node.Node.Type, node.Score)
	}

	// 7. 演示持久化
	fmt.Println("\n7. 持久化演示:")
	fmt.Println("  ✓ 所有数据已自动保存到 SQLite")
	fmt.Printf("  ✓ 重新运行此程序将从 %s 加载数据\n", dbPath)
	fmt.Println("  ✓ 提示: 删除该文件将清空图谱数据")

	fmt.Println("\n=== 演示完成 ===")
}
