# opt-tool-rag_index-01

## 目标

优化 `rag_index` 的路径安全、索引范围控制、文件过滤、增量索引和诊断输出，让它继续保持“把本地文件或目录写入 RAG 知识库”的定位，同时降低误索引敏感文件、目录过大、重复索引、路径边界不清和索引结果不可审计的问题。

本方案聚焦：

- path 校验和 sandbox 策略
- include / exclude 过滤
- 递归深度和文件数量限制
- 文件类型和大小限制
- dry-run / index plan
- 增量索引和 hash 去重
- 输出结构化 metadata

## 当前状态

相关实现：

- `internal/tool/builtin_memory.go`
- `internal/tool/rag_service.go`
- `internal/rag/indexer.go`
- `internal/rag/rag.go`
- `docs/tools/memory/rag_index.md`

当前 `rag_index` handler 流程：

1. 检查 RAG manager 是否初始化。
2. 读取 `path`。
3. trim path。
4. path 为空时报错。
5. `os.Stat(path)` 检查存在性。
6. 目录调用 `manager.IndexDirectory(path)`。
7. 文件调用 `manager.IndexFile(path)`。
8. 返回中文成功消息。

当前优势：

- 工具权限是 `PermApprove`。
- 文件和目录分支清晰。
- 输出包含索引文档数或 chunks 数。
- RAG 和 durable memory 边界在文档中已说明。
- 代码库已经存在 Graph RAG 相关 API，但当前 `rag_index` tool 没有触发这些 API。

## 主要问题

### 1. handler 层不做路径校验

当前直接使用 `os.Stat(path)`。这意味着可索引范围取决于进程权限。

风险：

- 误索引 home 下敏感文件。
- 误索引密钥、数据库、日志。
- 用户以为受 workspace 限制，实际没有。

建议复用统一路径策略，至少增加显式 allowlist 或确认提示。

### 2. 目录索引范围不可控

当前 handler 只有 `path`，没有：

- recursive 开关。
- max_files。
- max_bytes。
- include / exclude glob。
- ignore hidden。

如果 manager/indexer 内部行为变化，tool 文档和用户预期会漂移。

### 3. 缺少 dry-run

索引会写入 RAG 存储。调用前应该能查看：

- 将索引多少文件。
- 总字节数。
- 文件类型分布。
- 被跳过的文件。
- 是否可能包含敏感文件。

### 4. 文件类型限制不在 tool 层可见

文档说适合 Markdown、TXT、说明书等，但参数没有显式格式控制。建议 tool 层输出计划，明确哪些文件会进入索引。

### 5. 缺少敏感文件拦截

常见不应索引：

- `.env`
- `id_rsa`
- `*.pem`
- `*.key`
- `node_modules`
- `.git`
- database 文件
- binary / media 文件

建议默认排除。

### 6. 重复索引和增量策略不可见

当前输出只说 indexed 多少 documents，不说明：

- 新增多少。
- 更新多少。
- 跳过多少。
- 删除旧 chunk 还是追加 chunk。
- 是否按 content hash 去重。

### 7. 输出不是结构化 JSON

当前只返回一句文本，不利于后续链路记录索引结果。

建议支持 `format=json`。

### 8. Graph RAG 逻辑存在，但 `rag_index` 不能触发

当前代码库里已经有 Graph RAG 索引入口：

```go
IndexFileWithGraph(ctx, path)
IndexTextWithGraph(ctx, source, title, content)
```

但 `rag_index` handler 调用的是普通向量 RAG 路径：

```go
manager.IndexDirectory(path)
manager.IndexFile(path)
```

结果是：

- 使用 `rag_index` 只会写入普通 RAG 文档和 chunks。
- 不会自动提取实体和关系。
- 不会创建 Graph RAG 的 nodes / edges。
- 即使 `internal/rag` 里有 Graph RAG 实现，普通 tool 链路也无法触发。

这会造成认知偏差：用户看到项目里有 Graph RAG 文档和 demo，可能误以为 `rag_index` 已经会建立图谱。

建议明确拆分：

- `rag_index`：继续作为普通向量 RAG 索引。
- `graph_rag_index`：新增独立 graph-aware tool。

或者在 `rag_index` 增加显式参数：

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `use_graph` | `false` | 是否调用 Graph RAG 索引路径。 |
| `graph_mode` | `off` | `off`、`index_only`、`index_and_search` 等模式。 |

第一阶段更建议新增 `graph_rag_index`，避免改变现有 `rag_index` 的语义和成本模型。

## 优化原则

1. `rag_index` 继续需要审批。
2. 默认不要索引敏感路径和隐藏目录。
3. 目录索引必须可预览、可限制。
4. 索引计划应比实际索引更容易审查。
5. RAG 文档索引不用于保存用户偏好，避免和 `remember` 混用。
6. Graph RAG 需要显式触发，不应让普通 `rag_index` 静默产生额外 LLM 提取成本。

## 推荐方案

### 1. 增加参数

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `path` | 是 | 无 | 要索引的文件或目录。 |
| `recursive` | 否 | `false` | 是否递归索引目录。 |
| `max_files` | 否 | `200` | 最大索引文件数。 |
| `max_file_bytes` | 否 | `5 MiB` | 单文件最大大小。 |
| `max_total_bytes` | 否 | `100 MiB` | 本次索引最大总大小。 |
| `include` | 否 | 默认文本扩展名 | include glob 列表。 |
| `exclude` | 否 | 默认敏感路径 | exclude glob 列表。 |
| `dry_run` | 否 | `false` | 只输出索引计划，不写入。 |
| `format` | 否 | `text` | 输出 `text` 或 `json`。 |

### 2. 路径校验策略

新增：

```go
func validateRAGIndexPath(path string) error
```

策略：

- 路径必须存在。
- 默认允许当前工作区和明确用户提供的绝对路径。
- 禁止明显敏感路径，例如 `/etc`, `/proc`, `/sys`, runtime secret 目录。
- 对 workspace 外路径输出额外 warning 或要求 `allow_external=true`。

如果工具框架支持动态审批，workspace 外路径应升级审批说明。

### 3. dry-run index plan

新增：

```go
type RAGIndexPlan struct {
	Root          string
	Recursive     bool
	Files         []RAGIndexFilePlan
	Skipped       []RAGIndexSkip
	TotalFiles    int
	TotalBytes    int64
	Warnings      []string
}
```

dry-run 输出示例：

```json
{
  "dry_run": true,
  "root": "docs",
  "total_files": 12,
  "total_bytes": 345678,
  "skipped": [
    {"path": ".env", "reason": "sensitive_name"}
  ]
}
```

### 4. 默认过滤规则

默认 include：

- `.md`
- `.txt`
- `.rst`
- `.adoc`
- `.json`
- `.yaml`
- `.yml`
- `.csv`

默认 exclude：

- `.git/**`
- `node_modules/**`
- `vendor/**`
- `dist/**`
- `build/**`
- `.env*`
- `*.pem`
- `*.key`
- `id_rsa*`
- `*.sqlite`
- `*.db`
- 二进制文件

### 5. 增量索引

建议保存 source path + mtime + size + content hash。

行为：

- 内容 hash 未变化：skip。
- hash 变化：更新旧 document/chunks。
- source 不存在：可选 cleanup。

输出：

- `created`
- `updated`
- `unchanged`
- `skipped`

### 6. 结构化输出

JSON 输出示例：

```json
{
  "indexed": true,
  "documents": 12,
  "chunks": 128,
  "created": 2,
  "updated": 1,
  "unchanged": 9,
  "skipped": 3,
  "store": "sqlite"
}
```

文本输出保持兼容。

### 7. 新增 Graph RAG 索引入口

建议新增独立工具：

```go
GraphRAGIndexTool
```

工具名：

```text
graph_rag_index
```

参数建议：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `path` | 是 | 无 | 要索引的文件或目录。 |
| `recursive` | 否 | `false` | 是否递归目录。 |
| `dry_run` | 否 | `false` | 只输出计划，不写入。 |
| `extract_entities` | 否 | `true` | 是否提取实体。 |
| `extract_relations` | 否 | `true` | 是否提取关系。 |
| `format` | 否 | `text` | 输出 text 或 json。 |

实现上，文件路径调用：

```go
manager.IndexFileWithGraph(ctx, path)
```

目录路径需要新增目录版本，或在 tool handler 中遍历文件并逐个调用 `IndexFileWithGraph`。

如果选择复用 `rag_index`，则必须显式传：

```json
{
  "path": "docs",
  "use_graph": true
}
```

并在审批说明里提示：

- 会调用 LLM entity/relation extractor。
- 会额外写入 KnowledgeGraph。
- 可能产生费用和更长耗时。

## 分阶段实施

### 第一阶段：安全过滤

- 增加 max_files / max_file_bytes / max_total_bytes。
- 增加默认 exclude。
- 拒绝敏感文件名。
- 增加 dry-run。

### 第二阶段：路径和输出

- 增加路径校验策略。
- 增加 `format=json`。
- 输出 skipped reason。

### 第三阶段：增量索引

- 保存 source hash。
- unchanged 跳过。
- changed 更新旧 chunks。
- 增加 cleanup stale source 能力。

### 第四阶段：Graph RAG 接入

- 新增 `graph_rag_index`，或给 `rag_index` 增加 `use_graph`。
- 初始化支持 graph 的 RAG manager。
- 为 Graph RAG 准备 LLM provider / extractor。
- 文件索引调用 `IndexFileWithGraph`。
- 文本索引调用 `IndexTextWithGraph`。
- 输出 graph node / edge 统计。
- 明确普通 RAG index 和 Graph RAG index 的存储边界。

## 测试建议

- path 为空时报错。
- path 不存在时报错。
- 敏感文件 `.env` 默认跳过。
- 二进制文件默认跳过。
- max_files 生效。
- max_file_bytes 生效。
- dry-run 不调用 IndexFile / IndexDirectory。
- workspace 外路径需要 `allow_external=true`。
- `format=json` 返回 documents/chunks/skipped。
- 重复索引未变文件返回 unchanged。
- 普通 `rag_index` 不调用 `IndexFileWithGraph`。
- `graph_rag_index` 或 `use_graph=true` 会调用 `IndexFileWithGraph`。
- Graph RAG 未配置 LLM provider 时返回明确错误，而不是静默退化。
- Graph RAG 输出包含 nodes/edges 统计。

## 文档更新

同步更新：

- `docs/tools/memory/rag_index.md`
- 参数表新增 dry-run、include/exclude、limit 相关参数
- 默认排除规则
- workspace 外路径说明
- 增量索引说明
- Graph RAG 边界说明
- 如果新增工具，同步新增 `docs/tools/memory/graph_rag_index.md`

## 风险与边界

- 默认排除可能跳过用户确实想索引的文件，需要支持显式 include。
- hash 增量索引需要兼容现有 RAG store schema。
- workspace 外路径策略要兼顾 CLI 用户的真实需求。
- JSON/YAML 可能包含 secret，默认索引前仍应做敏感扫描。
- Graph RAG 索引需要 LLM provider，成本和延迟高于普通向量索引。
- Graph RAG 的 graph store 生命周期需要和 RAG SQLite 生命周期对齐。

## 推荐结论

优先做 dry-run、默认敏感排除和大小/数量限制。`rag_index` 是大范围写入索引的入口，先让用户看清楚“将索引什么”，再考虑增量索引和 schema 扩展。

Graph RAG 应作为单独阶段处理。当前项目已经有 Graph RAG 逻辑，但普通 `rag_index` 不能触发；推荐新增 `graph_rag_index`，而不是让现有 `rag_index` 默认变成 graph-aware。
