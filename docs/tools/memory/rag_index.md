# rag_index Tool

`rag_index` 是 LuckyAgent 的内置 RAG 索引工具，用来把本地文件或目录加入本地知识库，使其后续可以通过 `rag_search` 做语义检索。

这是会读取本地文件并写入 RAG 索引存储的工具，因此被标记为需要批准。

## 工具定义

实现位置：

- `internal/tool/builtin_memory.go`
- `internal/tool/rag_service.go`

注册信息：

```go
Name:         "rag_index"
Category:     CatBuiltin
Source:       "builtin"
Permission:   PermApprove
ParallelSafe: false
```

## 参数

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `path` | 是 | 无 | 要加入本地知识库的文件或目录路径。 |

示例：

```json
{
  "path": "docs/"
}
```

## 执行流程

`rag_index` 的执行过程是：

1. 检查 RAG manager 是否已初始化。
2. 读取必填参数 `path`。
3. 对 `path` 去掉首尾空白。
4. 如果 path 为空，返回 `path is required`。
5. 使用 `os.Stat(path)` 检查路径是否存在。
6. 如果是目录，调用 `manager.IndexDirectory(path)`。
7. 如果是文件，调用 `manager.IndexFile(path)`。
8. 返回中文成功消息。

如果 handler 未配置，默认错误是：

```text
rag_index handler not configured
```

如果 manager 未初始化，返回：

```text
rag manager not initialized
```

路径不存在时返回：

```text
path not found: <error>
```

## 输出格式

索引目录：

```text
✅ Indexed 12 documents from docs/
```

索引文件：

```text
✅ Indexed Demo (3 chunks)
```

其中：

- 目录输出里的数字是 `IndexDirectory` 返回的 document 数量。
- 文件输出里的 title 和 chunks 来自 `IndexFile` 返回的 document。

## 路径行为

当前 `rag_index` handler 直接使用传入 path：

```go
os.Stat(path)
```

不会在 handler 层调用 `validatePath`，也不会像文件工具那样做 workspace 限制。实际可索引范围取决于进程权限、RAG manager 和调用环境。

因此使用时应只索引明确需要进入知识库的文件或目录。

## 适合使用的场景

优先使用 `rag_index` 的场景：

- 用户明确要求把文件或目录加入知识库。
- 需要之后用 `rag_search` 语义检索这些内容。
- 索引项目文档、长 Markdown、说明书或知识资料。
- 把 final answer artifact 归档到可检索知识库。

示例：

```json
{
  "path": "/media/shiokou/DevRepo60/DevHub/Projects/2026-myapp/luckyagent/docs"
}
```

## 不适合使用的场景

不优先使用 `rag_index` 的场景：

- 只是读取当前文件内容，应使用 `file_read`。
- 只是搜索项目源码，应使用 `rg` 或 `terminal`。
- 保存用户偏好或长期规则，应使用 `remember`。
- 需要索引敏感文件、密钥、数据库或临时数据。
- 路径非常大且没有筛选，可能造成索引成本和噪声。

## 和 rag_search 的关系

`rag_index` 写入索引，`rag_search` 读取索引。

典型流程：

```text
rag_index -> rag_search
```

如果内容没有进入索引，`rag_search` 不一定能找到。

## 和 Graph RAG 的关系

当前 `rag_index` tool 和 Graph RAG 没有直接走通。

`rag_index` handler 调用的是普通 RAG manager 方法：

```go
manager.IndexDirectory(path)
manager.IndexFile(path)
```

也就是：

```go
func (m *RAGManager) IndexFile(path string) (*Document, error) {
    return m.indexer.IndexFile(path)
}
```

Graph RAG 相关索引入口在 `internal/rag/rag.go` 里是单独的方法：

```go
IndexFileWithGraph(ctx, path)
IndexTextWithGraph(ctx, source, title, content)
```

这些方法会先做普通向量索引，再在 `m.graph != nil && m.graphExtractor != nil` 时提取实体和关系并写入 `KnowledgeGraph`。

当前 `rag_index` tool 没有调用 `IndexFileWithGraph` 或 `IndexTextWithGraph`。因此即使代码库里存在 Graph RAG 能力，使用 `rag_index` 并不会自动创建 graph nodes/edges。

更准确的边界是：

- `rag_index`：普通向量 RAG 索引工具。
- Graph RAG：目前是 RAG manager 的独立 API/演示/测试路径。
- 如果要让 tool 支持 Graph RAG，需要新增参数或新工具，并在 handler 中调用 `IndexFileWithGraph` 等 graph-aware API。

## 和 remember 的关系

`rag_index` 不等于保存 durable memory。

- 文档资料：`rag_index`
- 用户事实、偏好、规则：`remember`

不要为了记住用户偏好而索引一个聊天记录文件；应直接写入 memory vault。

## 风险和注意事项

`rag_index` 的主要注意点：

- 权限是 `PermApprove`。
- 会写入 RAG 索引存储。
- handler 层不调用 `validatePath`。
- 索引目录可能处理大量文件。
- 索引内容可能包含敏感信息，调用前应确认路径。
- 索引质量取决于 RAG manager 的 chunking、embedding 和存储配置。

## 维护注意事项

如果后续修改 `rag_index`，需要同步检查：

- 参数名是否仍是 `path`。
- 权限是否仍是 `PermApprove`。
- 是否新增路径校验。
- 文件和目录分支是否仍分别调用 `IndexFile`、`IndexDirectory`。
- 是否改为或新增调用 `IndexFileWithGraph`、`IndexTextWithGraph`。
- 输出文案是否变化。
- 是否新增索引过滤、递归深度或格式限制。
