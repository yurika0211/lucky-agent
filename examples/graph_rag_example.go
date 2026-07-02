package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/yurika0211/luckyagent/internal/embedder"
	"github.com/yurika0211/luckyagent/internal/rag"
)

// 简单的 LLM Provider 示例
type SimpleLLM struct{}

func (s *SimpleLLM) Complete(ctx context.Context, prompt string) (string, error) {
	// 这里应该调用真实的 LLM API
	// 现在返回示例数据
	return `{
		"entities": [
			{"name": "测试实体", "type": "concept", "description": "测试", "aliases": []}
		],
		"relations": []
	}`, nil
}

func main() {
	// 1. 初始化
	fmt.Println("初始化 Graph RAG 系统...")

	homeDir, _ := os.UserHomeDir()
	dbPath := filepath.Join(homeDir, ".luckyagent", "rag", "my_graph.db")

	config := rag.DefaultRAGConfig()
	config.EnableGraph = true

	// 2. 创建带持久化的 RAG Manager
	ragManager, err := rag.NewRAGManagerWithSQLiteAndGraph(
		embedder.NewMockEmbedder(128),  // 使用 mock embedder，实际使用时换成真实的
		config,
		dbPath,
		&SimpleLLM{},
	)
	if err != nil {
		fmt.Printf("错误: %v\n", err)
		return
	}
	defer ragManager.CloseStore()

	// 3. 索引文档
	ctx := context.Background()
	fmt.Println("\n索引文档...")

	doc, err := ragManager.IndexTextWithGraph(
		ctx,
		"example.txt",
		"示例文档",
		"这是一个测试文档，用于演示 Graph RAG 的持久化功能。",
	)
	if err != nil {
		fmt.Printf("索引失败: %v\n", err)
		return
	}

	fmt.Printf("✓ 已索引: %s\n", doc.Title)

	// 4. 查看图谱信息
	stats := ragManager.Graph().Stats()
	fmt.Printf("\n图谱统计:\n")
	fmt.Printf("  节点: %d\n", stats.NodeCount)
	fmt.Printf("  边: %d\n", stats.EdgeCount)

	// 5. 查询
	fmt.Println("\n测试查询...")
	result, err := ragManager.SearchWithGraph(ctx, "测试查询")
	if err != nil {
		fmt.Printf("查询失败: %v\n", err)
		return
	}

	fmt.Printf("找到 %d 个激活节点\n", len(result.ActivatedNodes))

	fmt.Println("\n✓ 完成！数据已保存到:", dbPath)
	fmt.Println("✓ 重新运行此程序将自动加载现有图谱")
}
