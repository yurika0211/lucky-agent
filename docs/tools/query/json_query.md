# json_query Tool

`json_query` 是 LuckyAgent 的内置 JSON 文件查询工具，用来读取本地 JSON 文件，并用点路径语法提取嵌套字段。它适合从配置、API 响应样本或结构化日志文件中快速取值。

这是只读工具，不修改本地状态，因此被标记为自动批准。

## 工具定义

实现位置：

- `internal/tool/builtin_query.go`
- `internal/tool/builtin_helpers.go`

注册信息：

```go
Name:         "json_query"
Category:     CatBuiltin
Source:       "builtin"
Permission:   PermAuto
ParallelSafe: true
```

## 参数

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `path` | 是 | 无 | JSON 文件路径。 |
| `query` | 否 | 空字符串 | 点路径查询，例如 `user.name` 或 `items[0].id`。为空时格式化输出整个文档。 |
| `max_file_bytes` | 否 | `20971520` | 最大读取文件大小，默认 20 MiB，最大 100 MiB。 |
| `max_output_chars` | 否 | `12000` | 最大输出字符数，超出时返回截断 metadata。 |
| `summary` | 否 | `false` | `query` 为空时只返回顶层摘要。 |

示例：

```json
{
  "path": "sample.json",
  "query": "items[1].id"
}
```

## 执行流程

`json_query` 的执行过程是：

1. 读取必填参数 `path`。
2. 如果 `path` 不是字符串，返回 `path is required`。
3. 调用 `validatePath(path)` 做路径校验。
4. 使用 `os.Stat` 检查文件大小，超过 `max_file_bytes` 时拒绝读取。
5. 使用 `os.ReadFile(path)` 读取文件。
6. 使用 `json.Unmarshal` 解析 JSON。
7. 读取可选参数 `query`。
8. 如果 `query` 为空且 `summary=true`，返回顶层摘要。
9. 如果 `query` 为空，返回整个文档的 pretty JSON。
10. 如果 `query` 非空，使用 `walkStructuredPath` 提取值。
11. 使用 pretty JSON 输出结果；超过 `max_output_chars` 时返回截断 metadata。

## 查询语法

查询语法由 `parseStructuredPath` 实现：

- 使用 `.` 分割层级。
- 每段可以是对象 key。
- 每段可以带数组下标，例如 `items[0]`。
- 支持连续数组下标，例如 `matrix[0][1]`。
- 支持 bracket key，例如 `metadata["app.kubernetes.io/name"]` 或 `["a.b"]`。
- 支持数组 wildcard，例如 `items[*].id`。
- 支持 `length` 读取数组、对象或字符串长度。

示例：

```text
user.name
items[0].id
data.users[2].email
metadata["app.kubernetes.io/name"]
items[*].id
items.length
```

当前不支持：

- 过滤表达式。
- JSONPath 或 jq 语法。

## 输出格式

输出总是使用：

```go
json.MarshalIndent(v, "", "  ")
```

因此不同值的输出示例：

对象：

```json
{
  "name": "Ada"
}
```

数字：

```text
2
```

字符串：

```json
"Ada"
```

数组：

```json
[
  {
    "id": 1
  }
]
```

如果输出超过 `max_output_chars`，返回：

```json
{
  "value": "{\n  \"large\": ...",
  "_meta": {
    "truncated": true,
    "max_output_chars": 12000,
    "original_chars": 30000
  }
}
```

`summary=true` 且 `query` 为空时返回：

```json
{
  "type": "object",
  "size_bytes": 12345,
  "keys": ["items", "user"],
  "key_count": 2
}
```

## 错误行为

常见错误包括：

```text
read json file: <error>
parse json: <error>
path "name" expected object
path key "name" not found at "items[0]"
path index 2 expected array at "items"
path index 2 out of range at "items"
json file is 30000000 bytes, above max_file_bytes 20971520
```

这些错误来自文件读取、JSON 解析或路径遍历。

## 适合使用的场景

优先使用 `json_query` 的场景：

- 读取 JSON 配置中的某个字段。
- 从 API 响应样本里提取值。
- 查看本地 JSON 文件的格式化内容。
- 快速验证数组元素或嵌套字段。

示例：

```json
{
  "path": "package.json",
  "query": "scripts.build"
}
```

## 不适合使用的场景

不优先使用 `json_query` 的场景：

- 需要复杂筛选、管道和转换，应使用 `terminal` 调用 `jq`。
- 需要查询 YAML，应使用 `yaml_query`。
- 需要查询 CSV，应使用 `csv_query`。
- 需要查询 SQLite，应使用 `sql_query`。
- JSON 文件超过 `max_file_bytes`，需要 `jq`、流式处理或专门数据工具。

## 维护注意事项

如果后续修改 `json_query`，需要同步检查：

- 参数名是否仍是 `path` 和 `query`。
- 输出是否仍使用 `json.MarshalIndent`。
- 点路径语法是否变化。
- 文件大小和输出大小边界是否仍生效。
- 路径校验是否仍调用 `validatePath`。
