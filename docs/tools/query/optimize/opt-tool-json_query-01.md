# opt-tool-json_query-01

## 目标

优化 `json_query` 的大文件保护、查询语法、输出控制和错误诊断，让它继续保持“只读查询本地 JSON 文件”的定位，同时降低整文件加载风险、点路径语法不足、输出过长和路径错误难排查的问题。

## 当前状态

相关实现：

- `internal/tool/builtin_query.go`
- `docs/tools/query/json_query.md`

当前流程：

1. 读取 `path`。
2. 调用 `validatePath(path)`。
3. `os.ReadFile(path)` 整文件读取。
4. `json.Unmarshal` 解析到 `any`。
5. `query` 为空时 pretty print 整个文档。
6. `query` 非空时使用 `walkStructuredPath`。
7. 使用 `prettyStructuredValue` 输出。

当前优势：

- 只读，自动批准合理。
- 路径经过 `validatePath`。
- 点路径语法简单直观。
- 输出是 pretty JSON。

## 主要问题

### 1. 没有文件大小限制

当前直接 `os.ReadFile`，大 JSON 会占用大量内存。

建议设置默认上限，例如 20 MiB。超出后提示使用 `jq`、流式工具或更专门的数据工具。

### 2. query 为空时可能输出整个大文档

即使文件可读，整个 pretty JSON 可能远超上下文。

建议增加：

- `max_output_chars`
- `summary=true`
- 默认截断并标注 `truncated`

### 3. 点路径语法不支持转义

当前 `strings.Split(query, ".")`，无法查询带点号的 key：

```json
{"a.b": 1}
```

建议支持 bracket key：

```text
["a.b"]
metadata["app.kubernetes.io/name"]
```

### 4. 数组能力有限

当前只支持每段一个下标，例如 `items[0]`。不支持：

- `items[0][1]`
- wildcard
- slice
- filter

建议不要完整实现 JSONPath，但可补常用能力：

- 连续下标
- `[*]`
- `length`

### 5. 错误缺少当前位置

错误如 `path key "name" not found`，没有说明前面已经走到哪里。

建议输出：

```text
path key "name" not found at "items[0]"
```

## 优化原则

1. 保持轻量点路径工具，不变成完整 jq。
2. 大文件和大输出必须有边界。
3. 默认输出兼容，新增能力向后兼容。
4. 错误信息应帮助修正 query。

## 推荐方案

### 1. 参数扩展

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `max_file_bytes` | 否 | `20 MiB` | 最大读取文件大小。 |
| `max_output_chars` | 否 | `12000` | 最大输出字符数。 |
| `format` | 否 | `json` | 输出格式，预留 `text`。 |
| `summary` | 否 | `false` | query 为空时只输出顶层摘要。 |

### 2. 文件大小检查

读取前先 `os.Stat`：

```go
if info.Size() > maxFileBytes { ... }
```

### 3. 查询语法升级

新增 parser 支持：

```text
user.name
items[0].id
items[0][1]
metadata["app.kubernetes.io/name"]
items[*].name
items.length
```

实现上继续自研小 parser，避免引入完整 JSONPath 依赖。

### 4. 输出截断 metadata

文本兼容模式可输出：

```json
{
  "value": ...,
  "_meta": {
    "truncated": true,
    "max_output_chars": 12000
  }
}
```

或者默认仍返回值，只在截断后追加提示。建议 JSON 模式提供稳定 `_meta`。

### 5. summary 模式

query 为空且 `summary=true` 时返回：

```json
{
  "type": "object",
  "keys": ["name", "version", "scripts"],
  "size_bytes": 12345
}
```

## 分阶段实施

### 第一阶段：边界保护

- 文件大小限制。
- 输出大小限制。
- query 长度限制。

### 第二阶段：语法增强

- bracket key。
- 连续数组下标。
- wildcard。
- length。

### 第三阶段：诊断增强

- 错误包含当前位置。
- summary 模式。
- JSON meta 输出。

## 测试建议

- path 为空时报错。
- 超大文件被拒绝。
- query 为空输出全文或 summary。
- 带点 key 可通过 bracket 查询。
- 连续数组下标可用。
- wildcard 返回数组。
- key 不存在时报错包含当前位置。
- 输出超过限制时标注 truncated。

## 文档更新

同步更新 `docs/tools/query/json_query.md` 的参数表、查询语法、错误示例和大文件限制。

## 风险与边界

- 查询语法增强不要破坏现有点路径。
- wildcard 可能输出大量结果，需要和 max_output_chars 联动。
- 不建议实现完整 jq；复杂转换仍交给 terminal。

## 推荐结论

优先补文件大小和输出截断，再补 bracket key。这样能解决最实际的大文件风险和带点 key 无法查询的问题。
