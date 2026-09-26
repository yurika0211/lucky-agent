# Graph RAG 快速开始

## 30 秒快速试用

```bash
# 1. 编译并运行演示
cd /media/shiokou/DevRepo60/DevHub/Projects/2026-myapp/luckyharness
go run ./cmd/graph-rag-demo/

# 2. 查看输出
# 你会看到：
# - 知识图谱统计（节点和边数量）
# - 激活扩散结果
# - 关系路径追踪
```

## 在你的代码中使用

### 最简单的例子

```go
package main

import (
    "context"
    "fmt"
    
    "github.com/yurika0211/luckyagent/internal/embedder"
    "github.com/yurika0211/luckyagent/internal/rag"
)

// 实现你的 LLM Provider
type MyLLMProvider struct {
    // 你的 LLM 客户端
}

func (p *MyLLMProvider) Complete(ctx context.Context, prompt string) (string, error) {
    // 调用你的 LLM API，返回 JSON 格式的实体和关系
    // 具体格式见 internal/rag/extractor.go
}

func main() {
    // 创建 RAG Manager（带图谱）
    config := rag.DefaultRAGConfig()
    config.EnableGraph = true
    
    ragManager := rag.NewRAGManagerWithGraph(
        embedder.NewOpenAIEmbedder("your-key"),
        config,
        &MyLLMProvider{},
    )
    
    // 索引文档（自动提取知识图谱）
    ctx := context.Background()
    ragManager.IndexFileWithGraph(ctx, "knowledge.md")
    
    // 使用 Graph RAG 检索
    result, _ := ragManager.SearchWithGraph(ctx, "你的问题")
    
    fmt.Println(result.Context) // 融合的上下文
}
```

## 与现有代码集成

如果你已经有 RAG 系统，只需：

```go
// 1. 在你现有的 RAG Manager 上启用图谱
ragManager.EnableGraph(yourLLMProvider)

// 2. 使用 Graph RAG 检索（其他代码不变）
result, _ := ragManager.SearchWithGraph(ctx, query)
```

## 核心概念

### 双路检索
```
查询 "张三在哪个城市工作？"
  ├─→ 向量检索: 找到包含"张三"的文档
  └─→ 图激活: 张三 → 阿里巴巴(works_at) → 杭州(located_in)
       ↓
    融合结果: "张三在杭州工作"
```

### 激活扩散
- **直接激活**: 匹配查询的实体获得初始分数
- **扩散激活**: 通过关系传播到相关实体
- **路径追踪**: 记录激活路径，可解释

## 配置调优

```go
// 自定义激活选项
opts := rag.DefaultGraphActivationOptions()
opts.MaxDepth = 3              // 增加遍历深度
opts.MaxGraphBoost = 0.8       // 增加图扩散权重
opts.RelationWeights["works_at"] = 0.9  // 调整关系权重

// 使用自定义选项
graph := ragManager.Graph()
results := graph.ActivateGraph(query, opts)
```

## 查看激活路径

```go
for _, node := range result.ActivatedNodes {
    fmt.Printf("节点: %s (分数: %.2f)\n", node.Node.Name, node.Score)
    for _, path := range node.Paths {
        fmt.Printf("  路径: %s -[%s]-> %s\n", 
            path.FromID, path.RelType, node.Node.Name)
    }
}
```

## 常见问题

**Q: 为什么图谱是空的？**
A: 确保使用 `IndexFileWithGraph` 或 `IndexTextWithGraph`，并提供 LLM Provider。

**Q: 如何降低实体提取成本？**
A: 使用更小的模型（如 GPT-4o-mini），或只对重要文档提取实体。

**Q: 图谱会持久化吗？**
A: 当前版本在内存中，Phase 2 会添加 SQLite 持久化。

## 下一步

- 阅读详细文档: `internal/rag/README_GRAPH_RAG.md`
- 查看设计文档: `/tmp/graph_rag_design.md`
- 运行测试: `go test ./internal/rag -v -run TestGraph`

## 对比效果

| 场景 | 传统 RAG | Graph RAG |
|------|---------|-----------|
| 语义相似 | ✅ 优秀 | ✅ 优秀 |
| 多跳推理 | ❌ 不支持 | ✅ 支持 |
| 关系查询 | ❌ 困难 | ✅ 简单 |
| 可解释性 | ⚠️ 一般 | ✅ 优秀 |

---

**开始试用！** 🚀
