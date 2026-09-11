# Graph RAG 完整测试指南

## 测试方式对比

| 测试方式 | 适用场景 | 优点 | 缺点 |
|---------|---------|------|------|
| **单元测试** | 验证核心功能 | 快速、可重复 | 不涉及真实 LLM |
| **演示程序** | 快速体验 | 开箱即用 | 固定场景 |
| **交互测试** | 自定义查询 | 灵活 | 需要编译 |
| **集成测试** | 真实场景 | 最接近实际使用 | 需要 LLM API |

## 1. 快速测试（推荐）

### 运行单元测试
```bash
# 测试激活扩散算法
go test ./internal/rag -v -run TestGraphActivation

# 测试完整集成
go test ./internal/rag -v -run TestGraphRAG

# 运行所有 Graph 相关测试
go test ./internal/rag -v -run TestGraph
```

**预期输出：**
```
=== RUN   TestGraphActivation
    graph_example_test.go:199: Activated 3 nodes
    graph_example_test.go:201: 1. 张三 (person) - Score: 1.991, Paths: 1
    graph_example_test.go:201: 2. 阿里巴巴 (organization) - Score: 0.600, Paths: 1
    graph_example_test.go:201: 3. 杭州 (location) - Score: 0.202, Paths: 1
--- PASS: TestGraphActivation (0.00s)
```

## 2. 运行演示程序

### 基础演示
```bash
# 编译
go build -o bin/graph-rag-demo ./cmd/graph-rag-demo/

# 运行
./bin/graph-rag-demo
```

**查看要点：**
1. ✅ 知识图谱统计（节点和边数量）
2. ✅ 激活扩散结果
3. ✅ 关系路径追踪

### 交互测试工具
```bash
# 编译
go build -o bin/graph-rag-test ./cmd/graph-rag-test/

# 运行（选择模式）
./bin/graph-rag-test
# 输入 1: 自动测试
# 输入 2: 交互测试（可以输入自己的查询）
# 输入 3: 对比测试（传统 RAG vs Graph RAG）
```

## 3. 在实际代码中测试

### 创建测试脚本

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/yurika0211/luckyagent/internal/embedder"
	"github.com/yurika0211/luckyagent/internal/provider"
	"github.com/yurika0211/luckyagent/internal/rag"
)

// RealLLMProvider 使用你实际的 LLM Provider
type RealLLMProvider struct {
	provider *provider.OpenAIProvider
}

func (r *RealLLMProvider) Complete(ctx context.Context, prompt string) (string, error) {
	// 调用你实际的 LLM
	return r.provider.Complete(ctx, prompt, nil)
}

func main() {
	// 1. 创建真实的 embedder
	embedder := embedder.NewOpenAIEmbedder("your-api-key")

	// 2. 创建真实的 LLM Provider
	llmProvider := &RealLLMProvider{
		provider: provider.NewOpenAIProvider("your-api-key", "gpt-4o-mini"),
	}

	// 3. 创建 Graph RAG Manager
	config := rag.DefaultRAGConfig()
	config.EnableGraph = true

	ragManager := rag.NewRAGManagerWithGraph(embedder, config, llmProvider)
	defer ragManager.CloseStore()

	// 4. 索引你的真实文档
	ctx := context.Background()
	doc, err := ragManager.IndexFileWithGraph(ctx, "your_document.md")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Indexed: %s\n", doc.Title)

	// 5. 查看图谱
	if graph := ragManager.Graph(); graph != nil {
		stats := graph.Stats()
		fmt.Printf("Graph: %d nodes, %d edges\n", stats.NodeCount, stats.EdgeCount)
	}

	// 6. 测试查询
	result, err := ragManager.SearchWithGraph(ctx, "你的测试查询")
	if err != nil {
		log.Fatal(err)
	}

	// 7. 查看结果
	fmt.Println("\n=== 激活的节点 ===")
	for i, node := range result.ActivatedNodes {
		fmt.Printf("%d. %s (%s) - Score: %.3f\n",
			i+1, node.Node.Name, node.Node.Type, node.Score)

		// 查看激活路径
		for _, path := range node.Paths {
			if graph := ragManager.Graph(); graph != nil {
				fromNode, _ := graph.GetNode(path.FromID)
				if fromNode != nil {
					fmt.Printf("   %s -[%s]-> %s\n",
						fromNode.Name, path.RelType, node.Node.Name)
				}
			}
		}
	}

	// 8. 使用融合的上下文
	fmt.Println("\n=== 融合上下文 ===")
	fmt.Println(result.Context)
}
```

## 4. 验证关键功能

### 4.1 验证图谱构建

```bash
# 运行演示，检查输出
./bin/graph-rag-demo | grep -A 5 "Knowledge Graph Stats"
```

**预期：**
```
Knowledge Graph Stats:
  Nodes: 4      # 应该 > 0
  Edges: 3      # 应该 > 0
```

### 4.2 验证激活扩散

查询应该激活多个相关节点：

```bash
# 查询"张三"应该激活：
# 1. 张三（直接匹配）
# 2. 阿里巴巴（通过 works_at）
# 3. 杭州（通过 阿里巴巴 -> located_in，2跳）
```

**验证路径：**
```go
for _, node := range result.ActivatedNodes {
    if len(node.Paths) > 0 {
        fmt.Printf("✓ %s 通过 [%s] 关系激活\n", 
            node.Node.Name, node.Paths[0].RelType)
    }
}
```

### 4.3 验证多跳推理

**测试场景：**
- 文档A: "张三在阿里巴巴工作"
- 文档B: "阿里巴巴总部在杭州"
- 查询: "张三在哪个城市工作？"

**预期结果：**
- 传统 RAG: 可能找不到答案（文档A没有"杭州"）
- Graph RAG: 通过 2 跳推理找到答案（张三 → 阿里巴巴 → 杭州）

### 4.4 验证关系权重

```go
opts := rag.DefaultGraphActivationOptions()

// 增加某种关系的权重
opts.RelationWeights["works_at"] = 0.9

// 使用自定义选项
results := graph.ActivateGraph(query, opts)

// works_at 关系激活的节点应该有更高的分数
```

## 5. 性能测试

### 5.1 图谱构建性能

```go
import "time"

start := time.Now()
doc, _ := ragManager.IndexFileWithGraph(ctx, "large_doc.md")
duration := time.Since(start)

fmt.Printf("索引耗时: %v\n", duration)
// 预期：小文档 < 1s，大文档取决于 LLM 调用
```

### 5.2 激活扩散性能

```go
start := time.Now()
results := graph.ActivateGraph(query, opts)
duration := time.Since(start)

fmt.Printf("激活耗时: %v\n", duration)
// 预期：< 1ms for 1000 nodes
```

## 6. 调试技巧

### 6.1 查看激活分数组成

```go
for _, node := range result.ActivatedNodes {
    c := node.Components
    fmt.Printf("%s:\n", node.Node.Name)
    fmt.Printf("  Lexical: %.3f\n", c.Lexical)
    fmt.Printf("  Importance: %.3f\n", c.Importance)
    fmt.Printf("  Recency: %.3f\n", c.Recency)
    fmt.Printf("  GraphBoost: %.3f\n", c.GraphBoost)
    fmt.Printf("  Total: %.3f\n", node.Score)
}
```

### 6.2 导出图结构

```go
graph := ragManager.Graph()

// 导出所有节点
fmt.Println("=== 节点 ===")
for id, node := range graph.Nodes {
    fmt.Printf("%s: %s (%s)\n", id, node.Name, node.Type)
}

// 导出所有边
fmt.Println("\n=== 边 ===")
for id, edge := range graph.Edges {
    fmt.Printf("%s: %s -[%s]-> %s\n",
        id, edge.SourceID, edge.RelType, edge.TargetID)
}
```

### 6.3 追踪激活路径

```go
for _, node := range result.ActivatedNodes {
    fmt.Printf("\n节点: %s (分数: %.3f)\n", node.Node.Name, node.Score)
    fmt.Printf("直接分数: %.3f\n", node.DirectScore)
    fmt.Printf("图扩散: %.3f\n", node.Components.GraphBoost)

    if len(node.Paths) > 0 {
        fmt.Println("激活路径:")
        for i, path := range node.Paths {
            graph := ragManager.Graph()
            fromNode, _ := graph.GetNode(path.FromID)
            toNode, _ := graph.GetNode(path.ToID)
            fmt.Printf("  %d. %s -[%s:%.2f]-> %s\n",
                i+1, fromNode.Name, path.RelType, path.Weight, toNode.Name)
        }
    }
}
```

## 7. 常见问题排查

### Q: 图谱节点数为 0？

**可能原因：**
1. 没有使用 `IndexFileWithGraph` / `IndexTextWithGraph`
2. LLM Provider 未提供或返回空结果
3. LLM 响应格式不正确

**排查：**
```go
// 检查 LLM Provider
if ragManager.Graph() == nil {
    fmt.Println("图谱未启用")
}

// 手动测试实体提取
ctx := context.Background()
chunk := &rag.Chunk{ID: "test", Content: "张三在阿里巴巴工作"}
result, err := extractor.ExtractEntitiesAndRelations(ctx, chunk)
fmt.Printf("提取结果: %+v, 错误: %v\n", result, err)
```

### Q: 图激活没有结果？

**可能原因：**
1. 查询词和节点名称不匹配
2. 节点的 `CreatedAt` / `AccessedAt` 未设置
3. 激活选项配置不当

**排查：**
```go
// 检查节点是否存在
nodes := graph.FindNodeByName("张三")
fmt.Printf("找到 %d 个节点\n", len(nodes))

// 检查激活选项
opts := rag.DefaultGraphActivationOptions()
fmt.Printf("MaxDepth: %d, MaxSeeds: %d\n", opts.MaxDepth, opts.MaxSeeds)

// 尝试直接查找
graph := ragManager.Graph()
for _, node := range graph.Nodes {
    fmt.Printf("节点: %s\n", node.Name)
}
```

### Q: 激活路径不符合预期？

**排查：**
```go
// 检查边是否存在
for id, edge := range graph.Edges {
    fmt.Printf("边 %s: %s -[%s]-> %s (权重: %.2f)\n",
        id, edge.SourceID, edge.RelType, edge.TargetID, edge.Weight)
}

// 检查关系权重配置
opts := rag.DefaultGraphActivationOptions()
for relType, weight := range opts.RelationWeights {
    fmt.Printf("%s: %.2f\n", relType, weight)
}
```

## 8. 下一步

测试通过后，你可以：

1. **在真实数据上验证** - 使用你的实际文档
2. **调优参数** - 调整激活深度、关系权重
3. **对比效果** - 记录传统 RAG vs Graph RAG 的差异
4. **收集反馈** - 在实际场景中使用并记录改进点

---

**快速开始测试：**
```bash
# 1. 运行单元测试（最快）
go test ./internal/rag -v -run TestGraphActivation

# 2. 运行演示程序（可视化）
./bin/graph-rag-demo

# 3. 在真实场景中集成
# 参考上面的代码示例
```

有问题随时查看详细文档：
- `internal/rag/README_GRAPH_RAG.md`
- `docs/GRAPH_RAG_QUICKSTART.md`
