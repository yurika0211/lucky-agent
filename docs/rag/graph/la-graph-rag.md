# LA Graph RAG

本文说明 LuckyAgent 当前代码库里的 Graph RAG 实现状态、核心数据结构、索引流程、检索流程、SQLite 持久化和 tool 层边界。

## 结论

LuckyAgent 已经具备 Graph RAG 的核心实现：

- `KnowledgeGraph`：知识图谱节点和边。
- `GraphActivationOptions`：图激活扩散参数。
- `EntityExtractor`：通过 LLM 从 chunk 中提取实体和关系。
- `IndexFileWithGraph` / `IndexTextWithGraph`：普通向量索引 + 图谱提取。
- `SearchWithGraph`：向量检索 + 图激活 + 融合上下文。
- `GraphStore`：把 graph nodes / edges 持久化到 SQLite。

但当前内置 tool 层还没有直接接入 Graph RAG：

- `rag_index` 调用的是 `IndexFile` / `IndexDirectory`，不会触发图谱提取。
- `rag_search` 调用的是 `Search`，不会触发 graph activation。
- Graph RAG 目前主要存在于 `internal/rag` API、测试和 demo 路径。

因此，当前边界应理解为：

```text
普通 RAG tool：rag_index / rag_search
Graph RAG API：IndexFileWithGraph / IndexTextWithGraph / SearchWithGraph
```

如果要让用户从 agent tool 直接使用 Graph RAG，建议新增独立工具：

```text
graph_rag_index
graph_rag_search
```

不要让现有 `rag_index` / `rag_search` 静默切换到 Graph RAG，因为 Graph RAG 会额外调用 LLM 做实体提取，成本、延迟和输出结构都不同。

## 核心文件

当前主要实现位置：

- `internal/rag/graph.go`
- `internal/rag/graph_activation.go`
- `internal/rag/extractor.go`
- `internal/rag/graph_store.go`
- `internal/rag/rag.go`
- `internal/rag/sqlite_store.go`
- `internal/rag/graph_persist_test.go`
- `cmd/graph-rag-persist-demo/`

已有说明文档：

- `internal/rag/README_GRAPH_RAG.md`
- `internal/rag/GRAPH_PERSISTENCE_IMPLEMENTATION.md`
- `internal/rag/GRAPH_PERSISTENCE_QUICKSTART.md`
- `GRAPH_RAG_STARTUP_GUIDE.md`
- `docs/GRAPH_RAG_QUICKSTART.md`
- `docs/GRAPH_RAG_PHASE1_COMPLETE.md`

## 架构

Graph RAG 是双路检索：

```text
用户查询
  ├─ 向量检索
  │   └─ 返回相关 chunks
  │
  └─ 图激活扩散
      ├─ 直接命中实体节点
      ├─ 沿关系边多跳扩散
      └─ 返回 activated nodes + activation paths

向量结果 + 图结果
  └─ buildGraphEnhancedContext
      └─ 提供给 LLM 的融合上下文
```

普通 RAG 解决“语义相似片段检索”。

Graph RAG 额外解决：

- 查询词和答案片段不直接相似，但中间实体有关联。
- 需要沿实体关系找信息。
- 需要解释为什么某个实体被召回。
- 需要从多个文档 chunk 之间建立关系链。

## 数据结构

### KnowledgeNode

`KnowledgeNode` 表示实体或概念节点。

关键字段：

| 字段 | 说明 |
| --- | --- |
| `ID` | 节点唯一 ID。 |
| `Type` | 节点类型，例如 `person`、`organization`、`location`、`concept`、`event`。 |
| `Name` | 实体名称。 |
| `Aliases` | 别名。 |
| `Description` | 简短描述。 |
| `Importance` | 重要性，0 到 1。 |
| `AccessCount` | 访问次数。 |
| `CreatedAt` | 创建时间。 |
| `AccessedAt` | 最后访问时间。 |
| `SourceChunks` | 支撑该节点的 chunk IDs。 |
| `EmbeddingID` | 可选向量 ID。 |
| `Tags` | 标签。 |

节点权重：

```go
Weight = Importance * recencyFactor * accessBoost
```

当前 recency 半衰期约 30 天。访问次数会带来有限 boost。

### KnowledgeEdge

`KnowledgeEdge` 表示实体之间的关系。

关键字段：

| 字段 | 说明 |
| --- | --- |
| `ID` | 边唯一 ID。 |
| `SourceID` | 源节点 ID。 |
| `TargetID` | 目标节点 ID。 |
| `RelType` | 关系类型。 |
| `Weight` | 关系强度，0 到 1。 |
| `Context` | 关系上下文说明。 |
| `Evidence` | 支撑该关系的 chunk IDs。 |
| `CreatedAt` | 创建时间。 |

当前默认关系类型：

- `works_at`
- `located_in`
- `part_of`
- `related_to`
- `mention`

### KnowledgeGraph

`KnowledgeGraph` 保存节点、边和索引。

主要索引：

| 索引 | 说明 |
| --- | --- |
| `Nodes` | `nodeID -> node`。 |
| `Edges` | `edgeID -> edge`。 |
| `Forward` | `nodeID -> outgoing edge IDs`。 |
| `Backward` | `nodeID -> incoming edge IDs`。 |
| `TypeIndex` | `type -> node IDs`。 |
| `NameIndex` | normalized name / alias -> node IDs。 |
| `TagIndex` | tag -> node IDs。 |
| `ChunkNodes` | chunkID -> related node IDs。 |

`KnowledgeGraph` 内部有锁，支持并发读写。

## 实体和关系提取

实体提取由 `EntityExtractor` 完成。

接口：

```go
type LLMProvider interface {
	Complete(ctx context.Context, prompt string) (string, error)
}
```

提取流程：

1. 输入一个 RAG chunk。
2. 如果 chunk 内容超过 2000 字符，先截断。
3. 构造实体/关系提取 prompt。
4. 调用 LLM provider。
5. 解析 JSON 响应。
6. 转成 `KnowledgeNode` 和 `KnowledgeEdge`。

LLM 期望输出：

```json
{
  "entities": [
    {
      "name": "张三",
      "type": "person",
      "description": "软件工程师",
      "aliases": ["Zhang San"]
    }
  ],
  "relations": [
    {
      "source": "张三",
      "target": "LuckyAgent",
      "type": "related_to",
      "context": "张三参与 LuckyAgent 项目"
    }
  ]
}
```

实体类型会规范化：

| 输入 | 规范类型 |
| --- | --- |
| `person`、`人物` | `person` |
| `organization`、`org`、`company`、`公司` | `organization` |
| `location`、`city`、`地点` | `location` |
| `concept`、`technology`、`技术` | `concept` |
| `event`、`事件` | `event` |
| 其他 | `concept` |

关系类型会规范化：

| 输入 | 规范类型 |
| --- | --- |
| `works_at`、`employed_by`、`就职于` | `works_at` |
| `located_in`、`位于` | `located_in` |
| `part_of`、`属于` | `part_of` |
| `related_to`、`相关` | `related_to` |
| `mention`、`mentions`、`提到` | `mention` |

## 索引流程

Graph RAG 索引入口在 `RAGManager`：

```go
IndexFileWithGraph(ctx, path)
IndexTextWithGraph(ctx, source, title, content)
```

流程：

1. 先执行普通向量索引。
2. 得到 document 和 chunks。
3. 如果 `m.graph != nil && m.graphExtractor != nil`：
   - 遍历 chunks。
   - 对每个 chunk 调用 `ExtractEntitiesAndRelations`。
   - 转换为 graph nodes / edges。
   - 写入 `KnowledgeGraph`。
4. 如果某个 chunk 的实体提取失败，当前实现会跳过该 chunk，不阻塞整个索引。

关键边界：

- 没有启用 graph 时，只执行普通向量索引。
- 没有 LLM provider / extractor 时，不会提取实体关系。
- 当前提取是顺序处理，不是批量并发。

## 检索流程

Graph RAG 检索入口：

```go
SearchWithGraph(ctx, query)
```

返回：

```go
type GraphRAGSearchResult struct {
	ChunkResults   []RetrievalResult
	ActivatedNodes []NodeActivationScore
	Context        string
}
```

流程：

1. 调用普通 retriever，得到 `ChunkResults`。
2. 如果 graph 已启用：
   - 使用 `DefaultGraphActivationOptions()`。
   - 调用 `graph.ActivateGraph(query, graphOpts)`。
   - 得到 activated nodes。
3. 调用 `buildGraphEnhancedContext` 融合向量结果和图结果。

融合上下文分两部分：

```text
## Relevant Document Chunks

向量检索得到的 chunks。

## Activated Knowledge Entities

图激活得到的实体、分数、描述和部分 activation paths。
```

当前上下文限制：

- 最多输出 5 个 chunk。
- chunk 内容截到约 200 字符。
- 最多输出 8 个 activated nodes。
- 前 5 个节点展示 activation paths。
- 每个节点最多展示 3 条 path。

## 图激活扩散

默认参数：

```go
GraphActivationOptions{
	MaxDepth:      2,
	MaxGraphBoost: 0.6,
	MaxSeeds:      10,
	RelationWeights: map[string]float64{
		"works_at":    0.7,
		"located_in":  0.6,
		"part_of":     0.5,
		"related_to":  0.3,
		"mention":     0.2,
	},
	IncludeChunks: true,
	UpdateAccess:  true,
}
```

激活分两步：

1. 直接激活：
   - 节点 name、alias、type、tag 与 query 匹配时获得直接分数。
   - 分数乘以节点权重。
   - top seeds 作为扩散起点。

2. 图扩散：
   - BFS 遍历出边和入边。
   - 出边按关系权重传播。
   - 入边按关系权重的一半传播。
   - 每个目标节点的 graph boost 受 `MaxGraphBoost` 限制。
   - 记录 `ActivationPath`。

传播公式大致为：

```text
boost = sourceScore * relationWeight * edge.Weight * targetNode.Weight(now)
```

节点最终分数由直接分数和 graph boost 累加。

## SQLite 持久化

`GraphStore` 使用 SQLite 保存知识图谱。

创建方式：

```go
graphStore, err := rag.NewGraphStore(sqliteStore.DB())
graph, err := rag.NewKnowledgeGraphWithStore(graphStore)
```

`NewKnowledgeGraphWithStore` 会从 SQLite 加载已有节点和边，并重建内存索引。

持久化表：

### knowledge_nodes

| 字段 | 说明 |
| --- | --- |
| `id` | 主键。 |
| `type` | 节点类型。 |
| `name` | 节点名称。 |
| `aliases` | JSON 数组。 |
| `description` | 描述。 |
| `importance` | 重要性。 |
| `access_count` | 访问次数。 |
| `created_at` | 创建时间。 |
| `accessed_at` | 最后访问时间。 |
| `source_chunks` | JSON 数组。 |
| `embedding_id` | 可选向量 ID。 |
| `tags` | JSON 数组。 |

索引：

- `idx_nodes_type`
- `idx_nodes_name`

### knowledge_edges

| 字段 | 说明 |
| --- | --- |
| `id` | 主键。 |
| `source_id` | 源节点。 |
| `target_id` | 目标节点。 |
| `rel_type` | 关系类型。 |
| `weight` | 边权重。 |
| `context` | 关系上下文。 |
| `evidence` | JSON 数组，通常是 chunk IDs。 |
| `created_at` | 创建时间。 |

索引：

- `idx_edges_source`
- `idx_edges_target`
- `idx_edges_type`

## 使用方式

### 内存 Graph RAG

```go
config := rag.DefaultRAGConfig()
config.EnableGraph = true

manager := rag.NewRAGManagerWithGraph(embedder, config, llmProvider)
defer manager.CloseStore()

doc, err := manager.IndexFileWithGraph(ctx, "docs/company.md")
result, err := manager.SearchWithGraph(ctx, "张三在哪个城市工作？")
```

### SQLite 持久化 Graph RAG

```go
config := rag.DefaultRAGConfig()
config.EnableGraph = true

manager, err := rag.NewRAGManagerWithSQLiteAndGraph(
	embedder,
	config,
	"~/.luckyagent/rag/graph.db",
	llmProvider,
)
defer manager.CloseStore()

doc, err := manager.IndexTextWithGraph(ctx, "doc.md", "标题", "内容")
result, err := manager.SearchWithGraph(ctx, "查询内容")
```

## 和当前 tool 的关系

### rag_index

当前 `rag_index` tool 调用：

```go
manager.IndexDirectory(path)
manager.IndexFile(path)
```

不会调用：

```go
manager.IndexFileWithGraph(ctx, path)
manager.IndexTextWithGraph(ctx, source, title, content)
```

因此 `rag_index` 不会创建 graph nodes / edges。

### rag_search

当前 `rag_search` tool 调用：

```go
manager.Search(context.Background(), query)
```

不会调用：

```go
manager.SearchWithGraph(ctx, query)
```

因此 `rag_search` 不会返回 activated nodes，也不会输出 graph-enhanced context。

## 建议的 tool 接入方式

建议新增两个工具，而不是改变现有工具默认语义。

### graph_rag_index

定位：

```text
显式执行 Graph RAG 索引：普通向量索引 + LLM 实体关系提取 + KnowledgeGraph 写入。
```

建议参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `path` | 是 | 无 | 文件或目录路径。 |
| `recursive` | 否 | `false` | 是否递归目录。 |
| `dry_run` | 否 | `false` | 只输出计划，不调用 LLM。 |
| `max_files` | 否 | `50` | 最大文件数。 |
| `max_chunks` | 否 | `200` | 最大 chunk 数。 |
| `format` | 否 | `text` | 输出格式。 |

输出应包含：

- indexed documents
- chunks
- extracted entities
- extracted relations
- graph nodes
- graph edges
- skipped files
- extraction errors

### graph_rag_search

定位：

```text
显式执行 Graph RAG 检索：向量检索 + 图激活 + 融合上下文。
```

建议参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `query` | 是 | 无 | 检索问题。 |
| `top_k` | 否 | `5` | 向量 chunk 数量。 |
| `graph_depth` | 否 | `2` | 图扩散深度。 |
| `max_nodes` | 否 | `8` | 最大 activated nodes。 |
| `include_paths` | 否 | `true` | 是否输出 activation paths。 |
| `format` | 否 | `text` | 输出格式。 |

输出应明确分区：

```json
{
  "query": "...",
  "chunk_results": [],
  "activated_nodes": [],
  "context": "...",
  "graph_stats": {
    "nodes": 123,
    "edges": 456
  }
}
```

## 当前限制

### 1. Graph RAG 不在 tool 层默认启用

用户通过 `rag_index` / `rag_search` 不会触发 Graph RAG。

### 2. 索引依赖 LLM provider

实体提取需要 `LLMProvider`。如果没有 provider，Graph RAG 只能使用手工构建的 graph，不能自动从 chunk 中提取实体和关系。

### 3. 提取失败被静默跳过

`IndexFileWithGraph` 中，单个 chunk 提取失败时当前会 `continue`，不会中断索引。需要在 tool 层输出 extraction error 统计，否则用户可能以为图谱完整建立。

### 4. 图谱质量依赖 LLM 输出

实体归一化、关系类型和别名质量会影响图激活效果。

### 5. SearchWithGraph 使用默认 graph options

当前 `SearchWithGraph` 内部直接使用 `DefaultGraphActivationOptions()`，调用方不能传入自定义 `MaxDepth`、relation weights 或 max nodes。若 tool 层需要调参，应新增：

```go
SearchWithGraphOptions(ctx, query, opts)
```

### 6. GraphStore 与普通 RAG store 生命周期耦合

`GraphStore` 复用 SQLiteStore 的底层 DB。关闭、迁移、清理和备份策略需要和普通 RAG SQLite 保持一致。

## 推荐下一步

1. 明确配置入口：
   - `rag.enable_graph`
   - `rag.graph_db_path`
   - `rag.graph_extraction_model`

2. 增加 Graph RAG tool：
   - `graph_rag_index`
   - `graph_rag_search`

3. 给 RAGManager 增加 option API：

```go
IndexFileWithGraphOptions(ctx, path, opts)
SearchWithGraphOptions(ctx, query, opts)
```

4. 输出 Graph RAG 统计：
   - node count
   - edge count
   - relation type distribution
   - extraction errors
   - activated paths

5. 补测试：
   - 普通 `rag_index` 不创建 graph。
   - `graph_rag_index` 创建 graph nodes / edges。
   - 普通 `rag_search` 不调用 graph activation。
   - `graph_rag_search` 返回 chunk results + activated nodes。
   - SQLite graph store 重启后能恢复 nodes / edges。

## 验证命令

可运行现有测试：

```bash
go test ./internal/rag -run 'TestGraph|Test.*Graph.*Persist'
```

可运行持久化 demo：

```bash
go run ./cmd/graph-rag-persist-demo/main.go
```

可查看 SQLite 中的图表：

```bash
sqlite3 ~/.luckyagent/rag/graph.db
.tables
SELECT id, type, name FROM knowledge_nodes LIMIT 10;
SELECT source_id, rel_type, target_id FROM knowledge_edges LIMIT 10;
```

## 维护注意事项

如果后续修改 Graph RAG，需要同步检查：

- `internal/rag/graph.go` 的节点和边字段。
- `internal/rag/graph_activation.go` 的默认扩散参数。
- `internal/rag/extractor.go` 的实体/关系 schema。
- `internal/rag/graph_store.go` 的 SQLite schema。
- `internal/rag/rag.go` 的 Graph RAG API。
- `docs/tools/memory/rag_index.md` 和 `docs/tools/memory/rag_search.md` 中的 tool 边界说明。
- `docs/tools/memory/optimize/opt-tool-rag_index-01.md` 和 `opt-tool-rag_search-01.md` 中的 Graph RAG 接入方案。
