# Graph RAG 实现指南

基于 LuckyAgent 记忆系统的激活扩散机制实现的 Graph RAG。

## 概述

Graph RAG 在传统向量 RAG 的基础上，增加了知识图谱和激活扩散机制，能够：
- 通过关系链进行多跳推理
- 找到语义上不直接相似但逻辑上相关的信息
- 提供可解释的激活路径

## 核心概念

### 1. 双路检索架构

```
用户查询
  ├─→ [路径1] 向量检索 → 相似文档块
  └─→ [路径2] 图激活扩散 → 相关实体 + 关系链
       ↓
    融合上下文 → LLM 生成答案
```

### 2. 激活扩散机制

从记忆系统移植的核心算法：
- **直接激活**：匹配查询的节点获得初始分数
- **传播激活**：通过关系边向外扩散激活能量
- **权重衰减**：考虑重要性、时效性、访问频率

### 3. 知识图谱结构

- **节点（Entity）**：人物、组织、地点、概念、事件
- **边（Relation）**：works_at, located_in, part_of, related_to
- **索引**：类型索引、名称索引、标签索引、反向链接

## 快速开始

### 基本用法

```go
package main

import (
    "context"
    "fmt"
    
    "github.com/yurika0211/luckyagent/internal/embedder"
    "github.com/yurika0211/luckyagent/internal/rag"
)

func main() {
    // 1. 创建 embedder
    embedder := embedder.NewOpenAIEmbedder("your-api-key")
    
    // 2. 创建 LLM Provider（用于实体提取）
    llmProvider := &YourLLMProvider{}
    
    // 3. 创建带 Graph 的 RAG Manager
    config := rag.DefaultRAGConfig()
    config.EnableGraph = true
    
    ragManager := rag.NewRAGManagerWithGraph(embedder, config, llmProvider)
    defer ragManager.CloseStore()
    
    // 4. 索引文档（同时构建知识图谱）
    ctx := context.Background()
    doc, err := ragManager.IndexFileWithGraph(ctx, "docs/company.md")
    if err != nil {
        panic(err)
    }
    
    fmt.Printf("Indexed: %s\n", doc.Title)
    
    // 5. Graph RAG 检索
    result, err := ragManager.SearchWithGraph(ctx, "张三在哪个城市工作？")
    if err != nil {
        panic(err)
    }
    
    // 6. 使用融合的上下文
    fmt.Println("=== Fused Context ===")
    fmt.Println(result.Context)
    
    // 7. 查看激活路径
    for _, node := range result.ActivatedNodes {
        fmt.Printf("Activated: %s (%s) - Score: %.2f\n", 
            node.Node.Name, node.Node.Type, node.Score)
        for _, path := range node.Paths {
            fmt.Printf("  Path: %s -[%s]-> %s\n", 
                path.FromID, path.RelType, path.ToID)
        }
    }
}
```

### 使用 SQLite 持久化

```go
// 创建带 SQLite 和 Graph 的 RAG
config := rag.DefaultRAGConfig()
config.EnableGraph = true

ragManager, err := rag.NewRAGManagerWithSQLiteAndGraph(
    embedder, 
    config, 
    "~/.luckyagent/rag/graph.db",
    llmProvider,
)
if err != nil {
    panic(err)
}
defer ragManager.CloseStore()
```

### 只使用图激活（不提取实体）

如果你已经有了知识图谱，可以直接使用激活功能：

```go
// 创建普通 RAG Manager
ragManager := rag.NewRAGManager(embedder, config)

// 手动启用图谱（不提供 LLM，不自动提取实体）
ragManager.EnableGraph(nil)

// 手动添加节点和边
graph := ragManager.Graph()
graph.AddNode(&rag.KnowledgeNode{
    ID:   "node_1",
    Type: "person",
    Name: "张三",
    // ...
})

// 使用图激活
result, _ := ragManager.SearchWithGraph(ctx, "张三")
```

## 配置选项

### GraphActivationOptions

```go
opts := rag.GraphActivationOptions{
    MaxDepth:      2,        // 最大遍历深度（1-3跳）
    MaxGraphBoost: 0.6,      // 图扩散的最大加成
    MaxSeeds:      10,       // 初始激活种子数
    RelationWeights: map[string]float64{
        "works_at":    0.7,  // 关系类型权重
        "located_in":  0.6,
        "part_of":     0.5,
        "related_to":  0.3,
    },
    IncludeChunks: true,     // 是否包含关联的文档块
    UpdateAccess:  true,     // 是否更新访问统计
}

// 使用自定义选项激活
results := graph.ActivateGraph("query", opts)
```

## 实体提取

### 实现 LLMProvider 接口

```go
type YourLLMProvider struct {
    // your fields
}

func (p *YourLLMProvider) Complete(ctx context.Context, prompt string) (string, error) {
    // 调用你的 LLM API
    // 返回 JSON 格式的实体和关系
}
```

### 提取格式

LLM 应该返回如下 JSON 格式：

```json
{
  "entities": [
    {
      "name": "张三",
      "type": "person",
      "description": "软件工程师",
      "aliases": ["Zhang San", "小张"]
    },
    {
      "name": "阿里巴巴",
      "type": "organization",
      "description": "中国科技公司",
      "aliases": ["Alibaba", "阿里"]
    }
  ],
  "relations": [
    {
      "source": "张三",
      "target": "阿里巴巴",
      "type": "works_at",
      "context": "在阿里巴巴担任软件工程师"
    }
  ]
}
```

## 架构说明

### 文件结构

```
internal/rag/
├── graph.go                  # 知识图谱数据结构
├── graph_activation.go       # 激活扩散算法
├── extractor.go             # 实体关系提取器
├── rag.go                   # RAG Manager（已扩展）
├── graph_example_test.go    # 使用示例和测试
└── README_GRAPH_RAG.md      # 本文档
```

### 核心组件

1. **KnowledgeGraph** - 知识图谱
   - 节点和边的存储
   - 多种索引（类型、名称、标签、反向链接）
   - 线程安全

2. **Graph Activation** - 激活扩散
   - BFS 广度优先遍历
   - 权重计算（重要性 × 时效性 × 访问频率）
   - 激活路径记录

3. **EntityExtractor** - 实体提取
   - 使用 LLM 提取实体和关系
   - 标准化实体类型和关系类型
   - 转换为图节点和边

4. **RAGManager** - 集成层
   - 双路检索（向量 + 图）
   - 上下文融合
   - 统一接口

## 性能考虑

### 提取成本

实体提取需要调用 LLM，会增加索引时间和成本：
- 每个 chunk 调用一次 LLM
- 建议使用较小的模型（如 GPT-4o-mini）
- 可以批量提取降低成本

### 图遍历性能

激活扩散使用 BFS，时间复杂度：
- O(V + E)，V = 节点数，E = 边数
- 通过 MaxDepth 和 MaxSeeds 控制复杂度
- 对于 < 1 万节点的图，性能良好

### 内存使用

- 图数据全部在内存中
- 每个节点约 200-500 字节
- 1 万节点约 2-5MB 内存

## 对比传统 RAG

### 示例场景

**文档内容：**
- 文档A: "张三在阿里巴巴工作"
- 文档B: "阿里巴巴总部在杭州"

**问题：** "张三在哪个城市工作？"

### 传统向量 RAG

1. 向量检索 → 找到文档A（包含"张三"）
2. 文档A 中没有"城市"或"杭州"
3. ❌ 无法回答

### Graph RAG

1. 向量检索 → 找到文档A
2. 图激活扩散：
   - 直接激活：张三（匹配查询）
   - 1跳激活：阿里巴巴（通过 works_at 关系）
   - 2跳激活：杭州（通过 located_in 关系）
3. 融合上下文包含：张三 → 阿里巴巴 → 杭州
4. ✅ 正确回答："杭州"

## 最佳实践

### 1. 何时使用 Graph RAG

适合场景：
- 需要多跳推理（"A的B的C是什么"）
- 关系密集的领域（组织架构、知识图谱）
- 需要可解释的检索路径

不适合场景：
- 纯文本相似度匹配就够了
- 实体和关系不明确
- 对成本非常敏感

### 2. 实体提取优化

- 使用更小更快的模型（GPT-4o-mini）
- 批量提取多个 chunk
- 缓存已提取的实体
- 只对重要文档提取实体

### 3. 关系权重调优

根据你的领域调整关系权重：

```go
opts.RelationWeights = map[string]float64{
    "direct_relation":   0.9,  // 直接关系权重高
    "indirect_relation": 0.4,  // 间接关系权重低
}
```

### 4. 激活深度控制

- **MaxDepth=1**：只考虑直接相关的实体（快）
- **MaxDepth=2**：考虑二度关系（平衡）
- **MaxDepth=3**：考虑三度关系（慢，可能噪音多）

## 调试技巧

### 查看激活路径

```go
for _, node := range result.ActivatedNodes {
    fmt.Printf("Node: %s (Score: %.2f)\n", node.Node.Name, node.Score)
    fmt.Printf("  Direct: %.2f, GraphBoost: %.2f\n", 
        node.DirectScore, node.Components.GraphBoost)
    for _, path := range node.Paths {
        fmt.Printf("  Path: %s -[%s:%.2f]-> %s\n",
            path.FromID, path.RelType, path.Weight, path.ToID)
    }
}
```

### 导出图结构

```go
graph := ragManager.Graph()
stats := graph.Stats()
fmt.Printf("Graph: %d nodes, %d edges\n", stats.NodeCount, stats.EdgeCount)

// 遍历所有节点
for id, node := range graph.Nodes {
    fmt.Printf("%s: %s (%s)\n", id, node.Name, node.Type)
}

// 遍历所有边
for id, edge := range graph.Edges {
    fmt.Printf("%s: %s -[%s]-> %s\n", 
        id, edge.SourceID, edge.RelType, edge.TargetID)
}
```

## 常见问题

### Q: 为什么激活扩散找不到相关节点？

A: 可能原因：
1. 实体提取失败 - 检查 LLM 返回的 JSON
2. 关系权重太低 - 调高 RelationWeights
3. MaxDepth 太小 - 增加遍历深度
4. 节点名称不匹配 - 添加 aliases

### Q: 图激活的分数如何解释？

A: 分数组成：
- DirectScore：直接匹配查询的分数
- GraphBoost：从其他节点传播来的分数
- Total = DirectScore + GraphBoost（上限 MaxGraphBoost）

### Q: 如何持久化知识图谱？

A: 当前版本图谱在内存中，重启后需要重新提取。后续版本会添加 SQLite 持久化：

```sql
-- 将在 future version 中添加
CREATE TABLE knowledge_nodes (...);
CREATE TABLE knowledge_edges (...);
```

## 后续优化

- [ ] 图谱持久化到 SQLite
- [ ] 批量实体提取
- [ ] 实体消歧和合并
- [ ] 图可视化支持
- [ ] 社区检测（分层摘要）
- [ ] 关系强化学习

## 相关文档

- 设计文档：`/tmp/graph_rag_design.md`
- 记忆系统：`internal/memory/activation.go`
- 测试示例：`internal/rag/graph_example_test.go`
