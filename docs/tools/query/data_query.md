# data Tool：JSON 和 YAML

JSON 和 YAML 查询现在是 `data` 的 `action=query`。`format=json` 读取一个 JSON 文档，`format=yaml` 读取 YAML，并可以按文档下标或对全部文档查询。两种格式都用同一套点路径语法提取嵌套字段。

这是只读工具，不修改本地状态，因此被标记为自动批准。

## 工具定义

实现位置：

- `internal/tool/builtin_query.go`
- `internal/tool/builtin_helpers.go`

注册信息：

```go
Name:         "data"
action:       "query"
Category:     CatBuiltin
Source:       "builtin"
Permission:   PermAuto
ParallelSafe: true
```

## 参数

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `format` | 是 | 无 | `json`、`yaml` 或 `yml`。 |
| `path` | 是 | 无 | JSON 或 YAML 文件路径。 |
| `query` | 否 | 空字符串 | 点路径查询。为空时格式化输出选中的文档。 |
| `document` | 否 | `0` | `format=yaml` 时的 0-based 文档下标。 |
| `all_documents` | 否 | `false` | `format=yaml` 时对所有文档执行同一个 query。 |
| `max_file_bytes` | 否 | `20971520` | 最大读取文件大小，默认 20 MiB，最大 100 MiB。 |
| `max_output_chars` | 否 | `12000` | 最大输出字符数，超出时返回截断 metadata。 |
| `summary` | 否 | `false` | `query` 为空时只返回文档摘要。 |

JSON：

```json
{
  "format": "json",
  "path": "package.json",
  "query": "scripts.build"
}
```

YAML：

```json
{
  "format": "yaml",
  "path": "deploy.yaml",
  "query": "spec.template.metadata.name",
  "document": 0
}
```

## 执行流程

`format` 缺失或不是 `json`、`yaml`、`yml` 时返回 `invalid data_query format`。

两种格式都先校验 `path`，再按 `max_file_bytes` 限制读取文件。JSON 用 `json.Unmarshal` 解析。YAML 解析为一个或多个文档，默认查询第 0 个文档；`all_documents=true` 时对每个文档执行同一个 query 并返回数组。

查询语法由 `parseStructuredPath` 实现：

- 使用 `.` 分割层级。
- 每段可以是对象 key，也可以带数组下标，例如 `items[0]`。
- 支持连续数组下标，例如 `matrix[0][1]`。
- 支持 bracket key，例如 `metadata["app.kubernetes.io/name"]`。
- 支持数组 wildcard，例如 `items[*].id`。
- 支持 `length` 读取数组、对象或字符串长度。

输出是 pretty JSON，不是 YAML。超过 `max_output_chars` 时返回截断 metadata。

## 不适合使用的场景

- 复杂筛选、管道或重写 JSON，使用 `terminal` 调用 `jq`。
- 保留 YAML 注释和原始格式，或做 merge，使用 `terminal` 调用 `yq`。
- 查询 CSV，使用 `csv_query`。
- 查询 SQLite，使用 `sql_query` 或 `db_schema`。
- 文件超过 `max_file_bytes`，需要流式处理或专门数据工具。
