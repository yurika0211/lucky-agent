# opt-tool-rag_search-01

## 目标

优化 `rag_search` 的并发安全、top_k 边界、查询参数、结果结构化和来源引用，让它继续保持“只读搜索已索引知识库”的定位，同时降低共享 retriever config 临时修改带来的竞态、返回过多片段、结果不可追溯和与 `recall` 混用的问题。

本方案聚焦：

- top_k 上下限
- 避免修改共享 manager config
- context timeout
- 结果 metadata 和 JSON 输出
- source / chunk id 引用
- score 阈值和 rerank 参数
- 测试矩阵补齐

## 当前状态

相关实现：

- `internal/tool/builtin_memory.go`
- `internal/tool/rag_service.go`
- `internal/rag/retriever.go`
- `internal/rag/rag.go`
- `docs/tools/memory/rag_search.md`

当前 `rag_search` handler 流程：

1. 检查 RAG manager 是否初始化。
2. 读取 `query`。
3. query 为空时报错。
4. 读取 `top_k`，默认 5。
5. 保存当前 retriever config。
6. 临时把 manager retriever config 的 `TopK` 改成本次 top_k。
7. 调用 `manager.Search(context.Background(), query)`。
8. defer 恢复原 retriever config。
9. 格式化结果文本。

当前优势：

- 是只读检索工具，权限为 `PermAuto`。
- 输出包含 score、title/source 和内容片段。
- 默认 top_k 是 5。
- 明确和 `recall` 分工不同。
- 代码库已经存在 Graph RAG 检索 API，但当前 `rag_search` tool 没有触发这些 API。

## 主要问题

### 1. `ParallelSafe=true` 但会临时修改共享 retriever config

当前 handler 执行：

```go
prev := s.manager.RetrieverConfig()
cfg := prev
cfg.TopK = topK
s.manager.UpdateRetrieverConfig(cfg)
defer s.manager.UpdateRetrieverConfig(prev)
```

如果多个 `rag_search` 并发执行，可能互相影响 TopK。工具注册却是 `ParallelSafe=true`，存在语义不一致。

建议不要修改共享 config，而是新增 per-call search options。

### 2. top_k 没有最大值

当前只要求大于 0。过大的 top_k 会：

- 增加检索和排序成本。
- 输出过长。
- 占用上下文。

建议默认 5，最大 20 或 50。

### 3. query 没有长度限制

超长 query 可能影响 embedding 成本、检索质量和日志输出。

建议限制 query 长度，例如 2000 rune。

### 4. 使用 `context.Background()`，没有 timeout

检索可能卡在 embedding provider、SQLite 或 vector store。tool 层应有 timeout。

建议默认 30 秒，并支持配置或参数覆盖。

### 5. 输出缺少 chunk id 和 source path

当前 title 优先 `DocTitle`，否则 `DocSource`，但输出不包含：

- document id
- chunk id
- source path
- chunk index
- score 以外的 rerank 信号

这会影响引用和后续精读。

### 6. 没有 `format=json`

文本输出适合读，但 agent/API 更适合结构化字段。

建议增加 `format=json`。

### 7. 没有 score threshold

无关结果可能仍被返回。建议支持：

- `min_score`
- `threshold`

低于阈值的结果过滤掉。

### 8. 与 recall 的边界只在文档里说明

如果 query 是“用户偏好、身份、长期事实”，RAG 搜索可能返回旧文档噪声。tool 本身无需路由，但输出可以提醒 source 是 indexed knowledge base。

### 9. Graph RAG 逻辑存在，但 `rag_search` 不能触发

当前代码库里已经有 Graph RAG 检索入口：

```go
SearchWithGraph(ctx, query)
```

它会返回：

```go
type GraphRAGSearchResult struct {
	ChunkResults   []RetrievalResult
	ActivatedNodes []NodeActivationScore
	Context        string
}
```

但 `rag_search` handler 调用的是普通向量 RAG 路径：

```go
manager.Search(context.Background(), query)
```

结果是：

- 使用 `rag_search` 只会返回普通 chunk retrieval 结果。
- 不会执行 graph activation。
- 不会返回 `ActivatedNodes`。
- 不会返回 graph-enhanced context。
- 即使 Graph RAG demo/test 能运行，普通 tool 链路也无法触发。

建议明确拆分：

- `rag_search`：继续作为普通向量 RAG 搜索。
- `graph_rag_search`：新增独立 graph-aware search tool。

或者在 `rag_search` 增加显式参数：

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `use_graph` | `false` | 是否调用 `SearchWithGraph`。 |
| `include_graph_nodes` | `true` | 是否返回 graph activated nodes。 |
| `include_graph_context` | `true` | 是否返回 graph-enhanced context。 |

第一阶段更建议新增 `graph_rag_search`，避免改变现有 `rag_search` 的输出格式和性能预期。

## 优化原则

1. `rag_search` 保持只读和自动批准。
2. 不修改共享 manager config。
3. 默认输出短、可引用、可追踪。
4. JSON 输出用于 agent 后续处理。
5. 用户偏好和 durable memory 仍走 `recall`。
6. Graph RAG 检索需要显式触发，并在输出中清楚区分 chunk results 和 graph nodes。

## 推荐方案

### 1. 新增 per-call SearchOptions

在 RAG manager / retriever 层新增：

```go
type SearchOptions struct {
	TopK      int
	MinScore  float64
	Timeout   time.Duration
	Rerank    bool
}

func (m *RAGManager) SearchWithOptions(ctx context.Context, query string, opts SearchOptions) ([]RetrievalResult, error)
```

handler 不再调用 `UpdateRetrieverConfig`。

### 2. top_k 边界

建议：

```go
const (
	defaultRAGSearchTopK = 5
	maxRAGSearchTopK     = 20
)
```

行为：

- 未传：5。
- `top_k <= 0`：回退 5 或报错，建议回退保持兼容。
- `top_k > max`：报错。

### 3. query 长度限制

建议：

```go
const maxRAGSearchQueryRunes = 2000
```

超长时报错：

```text
query exceeds 2000 rune limit
```

### 4. timeout

handler 使用：

```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()
```

可选参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `timeout_seconds` | 否 | `30` | 本次搜索超时。 |

限制范围，例如 1 到 120 秒。

### 5. JSON 输出

新增参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `format` | 否 | `text` | 输出 `text` 或 `json`。 |
| `min_score` | 否 | 无 | 最小 score 阈值。 |

JSON 示例：

```json
{
  "query": "context planner compression",
  "top_k": 5,
  "count": 2,
  "results": [
    {
      "score": 0.91,
      "doc_id": "doc123",
      "chunk_id": "chunk456",
      "title": "context_planner.md",
      "source": "docs/context_planner.md",
      "content": "..."
    }
  ]
}
```

### 6. 文本输出补充引用

保持短文本，但补充 source：

```text
1. [0.91] context_planner.md (docs/context_planner.md#chunk456) — ...
```

### 7. 空结果诊断

无结果时可提示：

- 是否索引为空。
- 建议先运行 `rag_index`。
- 当前 top_k / min_score。

不要自动索引文件。

### 8. 新增 Graph RAG 搜索入口

建议新增独立工具：

```go
GraphRAGSearchTool
```

工具名：

```text
graph_rag_search
```

参数建议：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `query` | 是 | 无 | 检索查询。 |
| `top_k` | 否 | `5` | 向量 chunk 返回数量。 |
| `include_graph_nodes` | 否 | `true` | 是否返回 graph activated nodes。 |
| `include_context` | 否 | `true` | 是否返回 graph-enhanced context。 |
| `format` | 否 | `text` | 输出 text 或 json。 |

实现上调用：

```go
result, err := manager.SearchWithGraph(ctx, query)
```

JSON 输出应区分：

```json
{
  "query": "张三在哪里工作",
  "chunk_results": [],
  "activated_nodes": [],
  "context": "..."
}
```

如果 graph 未启用，应返回明确错误：

```text
graph rag is not enabled
```

不要静默退化成普通 `rag_search`，否则用户无法判断图检索是否真的生效。

## 分阶段实施

### 第一阶段：并发安全

- 新增 `SearchWithOptions`。
- handler 不再修改共享 retriever config。
- top_k 增加最大值。
- 增加 query 长度限制。

### 第二阶段：输出增强

- 增加 `format=json`。
- 输出 doc/chunk/source。
- 增加 `min_score`。

### 第三阶段：诊断和 rerank

- 增加 timeout。
- 空索引诊断。
- 可选 rerank 参数。

### 第四阶段：Graph RAG 接入

- 新增 `graph_rag_search`，或给 `rag_search` 增加 `use_graph`。
- 初始化支持 graph 的 RAG manager。
- handler 调用 `SearchWithGraph`。
- 输出 chunk results、activated nodes 和 graph context。
- graph 未启用时给出明确错误。
- 补充 Graph RAG 和普通 RAG 的对照测试。

## 测试建议

- query 为空时报错。
- query 超长时报错。
- top_k 默认是 5。
- top_k 超过最大值时报错。
- 并发搜索不会互相覆盖 top_k。
- handler 不调用 `UpdateRetrieverConfig`。
- timeout context 会传入 manager。
- `format=json` 返回 doc_id/chunk_id/source。
- `min_score` 过滤低分结果。
- 无结果输出包含 query。
- 普通 `rag_search` 不调用 `SearchWithGraph`。
- `graph_rag_search` 或 `use_graph=true` 会调用 `SearchWithGraph`。
- graph 未启用时返回明确错误。
- Graph RAG JSON 输出包含 `chunk_results` 和 `activated_nodes`。
- Graph RAG 文本输出明确区分 vector results 和 graph results。

## 文档更新

同步更新：

- `docs/tools/memory/rag_search.md`
- 参数表新增 `format`、`min_score`、`timeout_seconds`
- top_k 最大值
- 并发安全说明
- JSON 输出示例
- recall / rag_search 边界
- Graph RAG 边界说明
- 如果新增工具，同步新增 `docs/tools/memory/graph_rag_search.md`

## 风险与边界

- RAG manager 增加 per-call options 需要保持现有 `Search` API 兼容。
- doc_id/chunk_id 是否可用取决于当前 RAG store 结构。
- min_score 默认不应过高，否则召回率下降。
- timeout 太短会影响远程 embedding provider。
- Graph RAG 输出结构和普通 RAG 不同，不能直接塞进现有纯文本格式。
- Graph RAG 依赖 graph index 已经建立，否则 activated nodes 可能为空。

## 推荐结论

优先修复共享 retriever config 临时修改问题。当前 `rag_search` 标记为并行安全，但实现会改共享状态，这是最需要先处理的点。随后补 top_k 边界、JSON 输出和 source/chunk 引用。

Graph RAG 应作为单独阶段处理。当前项目已经有 Graph RAG 逻辑，但普通 `rag_search` 不能触发；推荐新增 `graph_rag_search`，而不是让现有 `rag_search` 默认切换到 graph-aware 输出。
