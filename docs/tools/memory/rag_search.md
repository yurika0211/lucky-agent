# rag_search Tool

`rag_search` 是 LuckyAgent 的内置 RAG 检索工具，用来搜索本地已索引知识库中的文档片段。它适合查找之前索引过的文件、笔记、资料或归档 final answer。

这是只读检索工具，因此被标记为自动批准。

## 工具定义

实现位置：

- `internal/tool/builtin_memory.go`
- `internal/tool/rag_service.go`

注册信息：

```go
Name:         "rag_search"
Category:     CatBuiltin
Source:       "builtin"
Permission:   PermAuto
ParallelSafe: true
```

## 参数

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `query` | 是 | 无 | 语义检索查询。 |
| `top_k` | 否 | `5` | 最多返回多少个知识片段。只有大于 0 时才覆盖默认值。 |

示例：

```json
{
  "query": "alpha beta gamma",
  "top_k": 3
}
```

## 执行流程

`rag_search` 的执行过程是：

1. 检查 RAG manager 是否已初始化。
2. 读取必填参数 `query`。
3. 如果 query 为空，返回 `query is required`。
4. 读取 `top_k`，默认为 5。
5. 临时保存当前 retriever config。
6. 把 retriever config 的 `TopK` 改成本次调用的 top_k。
7. 调用 `manager.Search(context.Background(), query)`。
8. defer 恢复原 retriever config。
9. 如果没有结果，返回中文无结果消息。
10. 如果有结果，格式化 score、title/source 和内容片段。

如果 handler 未配置，默认错误是：

```text
rag_search handler not configured
```

如果 manager 未初始化，返回：

```text
rag manager not initialized
```

## top_k 行为

`top_k` 支持 `float64` 和 `int`。

只有大于 0 时才会生效：

```go
if int(v) > 0 {
    topK = int(v)
}
```

当前 handler 没有对 `top_k` 设置最大值，具体返回数量还会受到 RAG manager 和检索器配置影响。

注意：handler 会临时修改 manager 的 retriever config，再通过 defer 恢复。工具本身被标记为 `ParallelSafe=true`，但共享 manager 配置的临时修改是一个需要留意的实现细节。

## 输出格式

无结果：

```text
没有找到关于「alpha」的 RAG 结果
```

有结果：

```text
找到 2 条关于「alpha」的知识片段：
1. [0.91] Demo — alpha beta gamma
2. [0.75] notes.md — related content
```

字段来源：

- 分数：`r.Score`，格式为两位小数。
- 标题：优先 `r.DocTitle`，为空时用 `r.DocSource`，仍为空时用 `(unknown source)`。
- 内容：`r.Content` 去空白后截断到 160 字符。

## 适合使用的场景

优先使用 `rag_search` 的场景：

- 答案可能在已索引文档中。
- 查找长期资料、项目文档、归档总结。
- 需要语义搜索而不是文件名搜索。
- 想从 indexed knowledge base 中找证据片段。

示例：

```json
{
  "query": "LuckyAgent context planner compression behavior",
  "top_k": 5
}
```

## 不适合使用的场景

不优先使用 `rag_search` 的场景：

- 查用户偏好、身份或长期个人事实，应使用 `recall`。
- 查当前项目代码，应优先使用 `rg`、`file_read` 或 `terminal`。
- 文档还没有被索引，应先使用 `rag_index`。
- 需要精确读取某个文件当前内容，应使用文件工具。

## 和 recall 的关系

`rag_search` 查的是 RAG indexed knowledge base。

`recall` 查的是 Markdown memory vault。

两者不能混用：

- durable memory fact：`recall`
- indexed document evidence：`rag_search`

## 和 Graph RAG 的关系

当前 `rag_search` tool 和 Graph RAG 没有直接走通。

`rag_search` handler 调用的是普通 RAG manager 方法：

```go
manager.Search(context.Background(), query)
```

也就是：

```go
func (m *RAGManager) Search(ctx context.Context, query string) ([]RetrievalResult, error) {
    return m.retriever.Search(ctx, query)
}
```

Graph RAG 相关检索入口在 `internal/rag/rag.go` 里是单独的方法：

```go
SearchWithGraph(ctx, query)
```

`SearchWithGraph` 会返回：

```go
type GraphRAGSearchResult struct {
    ChunkResults   []RetrievalResult
    ActivatedNodes []NodeActivationScore
    Context        string
}
```

其中 `ChunkResults` 是普通向量检索结果，`ActivatedNodes` 是 graph activation 结果。

当前 `rag_search` tool 没有调用 `SearchWithGraph`，也没有输出 graph activated nodes 或 graph-enhanced context。因此它现在应被理解为普通向量 RAG 检索工具，不是 Graph RAG 查询工具。

更准确的边界是：

- `rag_search`：普通向量 RAG search。
- Graph RAG：目前是 `RAGManager.SearchWithGraph` 这类独立 API/演示/测试路径。
- 如果要让 tool 支持 Graph RAG，需要新增参数或新工具，并在 handler 中调用 `SearchWithGraph`。

## 维护注意事项

如果后续修改 `rag_search`，需要同步检查：

- 参数名是否仍是 `query` 和 `top_k`。
- 默认 `top_k` 是否仍是 5。
- 是否仍临时修改 retriever config。
- 是否改为或新增调用 `SearchWithGraph`。
- 输出分数和内容截断格式是否变化。
- 无结果文案是否变化。
- 权限是否仍是 `PermAuto`。
