package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/yurika0211/luckyagent/internal/embedder"
	"github.com/yurika0211/luckyagent/internal/rag"
)

// TestLLMProvider 用于测试的简单 LLM Provider
type TestLLMProvider struct{}

func (t *TestLLMProvider) Complete(ctx context.Context, prompt string) (string, error) {
	// 简单解析 prompt 中的文本，提取实体
	// 这是一个极简版本，实际使用时应该调用真实的 LLM

	// 检测常见实体模式
	text := strings.ToLower(prompt)

	if strings.Contains(text, "张三") || strings.Contains(text, "阿里巴巴") || strings.Contains(text, "杭州") {
		return `{
			"entities": [
				{"name": "张三", "type": "person", "description": "软件工程师", "aliases": []},
				{"name": "阿里巴巴", "type": "organization", "description": "科技公司", "aliases": ["Alibaba"]},
				{"name": "杭州", "type": "location", "description": "浙江省城市", "aliases": ["Hangzhou"]}
			],
			"relations": [
				{"source": "张三", "target": "阿里巴巴", "type": "works_at", "context": "在阿里巴巴工作"},
				{"source": "阿里巴巴", "target": "杭州", "type": "located_in", "context": "总部在杭州"}
			]
		}`, nil
	}

	return `{"entities": [], "relations": []}`, nil
}

func main() {
	fmt.Println("=== Graph RAG 交互式测试工具 ===\n")

	// 1. 创建 RAG Manager
	fmt.Println("初始化 Graph RAG...")
	embedder := embedder.NewMockEmbedder(128)
	llmProvider := &TestLLMProvider{}

	config := rag.DefaultRAGConfig()
	config.EnableGraph = true

	ragManager := rag.NewRAGManagerWithGraph(embedder, config, llmProvider)
	defer ragManager.CloseStore()

	fmt.Println("✓ 初始化完成\n")

	// 2. 预加载一些测试数据
	ctx := context.Background()
	testDocs := []struct {
		title   string
		content string
	}{
		{
			"人物简介",
			"张三是一名软件工程师，目前在阿里巴巴工作。他精通 Go 语言和分布式系统。",
		},
		{
			"公司信息",
			"阿里巴巴是一家领先的科技公司，总部位于杭州。公司专注于电子商务和云计算。",
		},
		{
			"城市介绍",
			"杭州是浙江省的省会城市，以美丽的西湖和发达的互联网产业而闻名。",
		},
	}

	fmt.Println("索引测试文档...")
	for i, doc := range testDocs {
		source := fmt.Sprintf("test_doc_%d.txt", i+1)
		_, err := ragManager.IndexTextWithGraph(ctx, source, doc.title, doc.content)
		if err != nil {
			fmt.Printf("✗ 索引失败: %v\n", err)
		} else {
			fmt.Printf("✓ 已索引: %s\n", doc.title)
		}
	}

	// 显示图谱统计
	if graph := ragManager.Graph(); graph != nil {
		stats := graph.Stats()
		fmt.Printf("\n知识图谱统计:\n")
		fmt.Printf("  节点: %d\n", stats.NodeCount)
		fmt.Printf("  边: %d\n", stats.EdgeCount)
	}

	fmt.Println("\n" + strings.Repeat("-", 60))
	fmt.Println("测试模式选择:")
	fmt.Println("  1. 自动测试 - 运行预定义的测试查询")
	fmt.Println("  2. 交互测试 - 输入你自己的查询")
	fmt.Println("  3. 对比测试 - 同时运行传统 RAG 和 Graph RAG")
	fmt.Println(strings.Repeat("-", 60))

	reader := bufio.NewReader(os.Stdin)
	fmt.Print("\n选择模式 (1/2/3): ")
	modeInput, _ := reader.ReadString('\n')
	mode := strings.TrimSpace(modeInput)

	switch mode {
	case "1":
		runAutoTests(ctx, ragManager)
	case "2":
		runInteractiveTests(ctx, ragManager, reader)
	case "3":
		runComparisonTests(ctx, ragManager)
	default:
		fmt.Println("无效选择，运行自动测试...")
		runAutoTests(ctx, ragManager)
	}
}

func runAutoTests(ctx context.Context, ragManager *rag.RAGManager) {
	fmt.Println("\n=== 自动测试模式 ===\n")

	queries := []string{
		"张三是谁",
		"张三在哪个公司工作",
		"张三在哪个城市工作",  // 需要2跳推理
		"阿里巴巴在哪里",
		"杭州有什么公司",
	}

	for i, query := range queries {
		fmt.Printf("\n[查询 %d] %s\n", i+1, query)
		fmt.Println(strings.Repeat("-", 50))

		result, err := ragManager.SearchWithGraph(ctx, query)
		if err != nil {
			fmt.Printf("✗ 错误: %v\n", err)
			continue
		}

		// 显示向量检索结果
		fmt.Printf("向量检索结果: %d 个\n", len(result.ChunkResults))
		for j, chunk := range result.ChunkResults {
			if j >= 3 {
				break
			}
			fmt.Printf("  %d. [%.2f] %s\n", j+1, chunk.Score, chunk.DocSource)
		}

		// 显示图激活结果
		fmt.Printf("\n图激活结果: %d 个节点\n", len(result.ActivatedNodes))
		for j, node := range result.ActivatedNodes {
			if j >= 5 {
				break
			}
			fmt.Printf("  %d. %s (%s) - 分数: %.3f\n",
				j+1, node.Node.Name, node.Node.Type, node.Score)

			// 显示激活路径（仅前3个节点）
			if len(node.Paths) > 0 && j < 3 {
				for k, path := range node.Paths {
					if k >= 2 {
						break
					}
					if graph := ragManager.Graph(); graph != nil {
						fromNode, _ := graph.GetNode(path.FromID)
						if fromNode != nil {
							fmt.Printf("     路径: %s -[%s]-> %s\n",
								fromNode.Name, path.RelType, node.Node.Name)
						}
					}
				}
			}
		}
	}

	fmt.Println("\n✓ 自动测试完成")
}

func runInteractiveTests(ctx context.Context, ragManager *rag.RAGManager, reader *bufio.Reader) {
	fmt.Println("\n=== 交互测试模式 ===")
	fmt.Println("输入你的查询（输入 'quit' 退出）\n")

	for {
		fmt.Print("查询> ")
		input, _ := reader.ReadString('\n')
		query := strings.TrimSpace(input)

		if query == "quit" || query == "exit" {
			break
		}

		if query == "" {
			continue
		}

		result, err := ragManager.SearchWithGraph(ctx, query)
		if err != nil {
			fmt.Printf("✗ 错误: %v\n\n", err)
			continue
		}

		// 显示结果
		fmt.Println("\n--- 检索结果 ---")

		if len(result.ChunkResults) > 0 {
			fmt.Println("\n向量检索:")
			for i, chunk := range result.ChunkResults {
				if i >= 3 {
					break
				}
				content := chunk.Content
				if len(content) > 100 {
					content = content[:100] + "..."
				}
				fmt.Printf("  [%.2f] %s\n", chunk.Score, content)
			}
		}

		if len(result.ActivatedNodes) > 0 {
			fmt.Println("\n图激活:")
			for i, node := range result.ActivatedNodes {
				if i >= 5 {
					break
				}
				fmt.Printf("  %s (%s) - %.3f\n",
					node.Node.Name, node.Node.Type, node.Score)
			}
		}

		fmt.Println()
	}

	fmt.Println("退出交互模式")
}

func runComparisonTests(ctx context.Context, ragManager *rag.RAGManager) {
	fmt.Println("\n=== 对比测试模式 ===\n")

	queries := []string{
		"张三在哪个城市工作",  // 多跳推理场景
		"阿里巴巴的总部",
	}

	for i, query := range queries {
		fmt.Printf("\n[查询 %d] %s\n", i+1, query)
		fmt.Println(strings.Repeat("=", 60))

		// 传统向量 RAG
		fmt.Println("\n【传统向量 RAG】")
		vectorResult, _ := ragManager.Search(ctx, query)
		fmt.Printf("结果数: %d\n", len(vectorResult))
		for j, chunk := range vectorResult {
			if j >= 3 {
				break
			}
			fmt.Printf("  %d. [%.2f] %s: %s\n",
				j+1, chunk.Score, chunk.DocTitle, chunk.Content[:50]+"...")
		}

		// Graph RAG
		fmt.Println("\n【Graph RAG】")
		graphResult, _ := ragManager.SearchWithGraph(ctx, query)
		fmt.Printf("向量结果: %d, 图激活节点: %d\n",
			len(graphResult.ChunkResults), len(graphResult.ActivatedNodes))

		if len(graphResult.ActivatedNodes) > 0 {
			fmt.Println("\n激活的实体:")
			for j, node := range graphResult.ActivatedNodes {
				if j >= 5 {
					break
				}
				fmt.Printf("  %d. %s (%s) - %.3f",
					j+1, node.Node.Name, node.Node.Type, node.Score)

				if len(node.Paths) > 0 {
					fmt.Printf("  [通过: %s]", node.Paths[0].RelType)
				}
				fmt.Println()
			}
		}

		fmt.Println("\n分析:")
		if len(graphResult.ActivatedNodes) > len(vectorResult) {
			fmt.Println("  ✓ Graph RAG 找到了更多相关信息")
		}
		if len(graphResult.ActivatedNodes) > 0 && graphResult.ActivatedNodes[0].Components.GraphBoost > 0 {
			fmt.Println("  ✓ 图扩散成功激活了间接相关的实体")
		}
	}

	fmt.Println("\n✓ 对比测试完成")
}
