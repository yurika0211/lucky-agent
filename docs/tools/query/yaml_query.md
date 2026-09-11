# yaml_query Tool

`yaml_query` 是 LuckyAgent 的内置 YAML 文件查询工具，用来读取本地 YAML 文件，并用点路径语法提取嵌套字段。它适合查看配置文件、Kubernetes manifest、CI 配置和其他 YAML 文档。

这是只读工具，不修改本地状态，因此被标记为自动批准。

## 工具定义

实现位置：

- `internal/tool/builtin_query.go`
- `internal/tool/builtin_helpers.go`

注册信息：

```go
Name:         "yaml_query"
Category:     CatBuiltin
Source:       "builtin"
Permission:   PermAuto
ParallelSafe: true
```

## 参数

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `path` | 是 | 无 | YAML 文件路径。 |
| `query` | 否 | 空字符串 | 点路径查询，例如 `metadata.name` 或 `items[0].id`。为空时格式化输出整个文档。 |
| `document` | 否 | `0` | 多文档 YAML 的 0-based 文档下标。 |
| `all_documents` | 否 | `false` | 是否对所有文档执行同一个 query。 |
| `max_file_bytes` | 否 | `20971520` | 最大读取文件大小，默认 20 MiB，最大 100 MiB。 |
| `max_output_chars` | 否 | `12000` | 最大输出字符数，超出时返回截断 metadata。 |
| `summary` | 否 | `false` | `query` 为空时返回文档摘要。 |

示例：

```json
{
  "path": "deploy.yaml",
  "query": "spec.template.metadata.name"
}
```

## 执行流程

`yaml_query` 的执行过程是：

1. 读取必填参数 `path`。
2. 如果 `path` 不是字符串，返回 `path is required`。
3. 调用 `validatePath(path)` 做路径校验。
4. 使用 `os.Stat` 检查文件大小，超过 `max_file_bytes` 时拒绝读取。
5. 使用 `os.ReadFile(path)` 读取文件。
6. 使用 `yaml.Decoder` 解析一个或多个 YAML document。
7. 调用 `normalizeYAMLValue` 把 YAML map 规范化为 `map[string]any`。
8. 默认选择 `document=0`；如果 `all_documents=true`，对所有 document 执行同一个 query。
9. 如果 `query` 为空且 `summary=true`，返回文档摘要。
10. 如果 `query` 为空，返回选中文档的 pretty JSON。
11. 如果 `query` 非空，使用 `walkStructuredPath` 提取值。
12. 使用 pretty JSON 输出结果；超过 `max_output_chars` 时返回截断 metadata。

## YAML 规范化

YAML 解析后可能出现：

```go
map[any]any
```

工具会递归规范化：

- `map[string]any`：递归处理 value。
- `map[any]any`：key 通过 `fmt.Sprint(k)` 转成字符串。
- `[]any`：递归处理每个元素。
- 其他值：原样保留。

这让 YAML 和 JSON 能共用同一套点路径查询逻辑。

## 查询语法

查询语法和 `json_query` 相同：

- 使用 `.` 分割层级。
- 每段可以是对象 key。
- 每段可以带一个数组下标，例如 `items[0]`。
- 支持连续数组下标，例如 `matrix[0][1]`。
- 支持 bracket key，例如 `metadata.labels["app.kubernetes.io/name"]`。
- 支持数组 wildcard，例如 `items[*].metadata.name`。
- 支持 `length` 读取数组、对象或字符串长度。

示例：

```text
service.name
metadata.labels.app
items[0].metadata.name
metadata.labels["app.kubernetes.io/name"]
items[*].metadata.name
```

当前不支持：

- 过滤表达式。
- YAML anchor/alias 的特殊语义查询。
- yq 或 jq 语法。

## 输出格式

输出不是 YAML，而是 pretty JSON：

```go
json.MarshalIndent(v, "", "  ")
```

例如 YAML：

```yaml
service:
  name: api
```

查询：

```json
{
  "query": "service.name"
}
```

返回：

```json
"api"
```

多文档 YAML 默认查询第 0 个文档。查询第二个文档：

```json
{
  "path": "deploy.yaml",
  "document": 1,
  "query": "metadata.name"
}
```

查询所有文档：

```json
{
  "path": "deploy.yaml",
  "all_documents": true,
  "query": "kind"
}
```

`summary=true` 且 `query` 为空时，会返回每个 document 的类型和常见 Kubernetes `kind` / `metadata.name`：

```json
{
  "type": "yaml_documents",
  "size_bytes": 12345,
  "documents": [
    {"index": 0, "type": "object", "kind": "Service", "name": "api"},
    {"index": 1, "type": "object", "kind": "Deployment", "name": "api"}
  ]
}
```

## 错误行为

常见错误包括：

```text
read yaml file: <error>
parse yaml: <error>
path "service" expected object
path key "service" not found at "<root>"
path index 0 expected array at "items"
path index 0 out of range at "items"
document index 3 out of range; yaml file has 2 documents
yaml file is 30000000 bytes, above max_file_bytes 20971520
```

## 适合使用的场景

优先使用 `yaml_query` 的场景：

- 查看 YAML 配置里的某个字段。
- 检查 Kubernetes manifest。
- 快速读取 GitHub Actions、Docker Compose 等配置。
- 把 YAML 文档格式化成 JSON 风格输出。

示例：

```json
{
  "path": ".github/workflows/test.yml",
  "query": "jobs.test.runs-on"
}
```

## 不适合使用的场景

不优先使用 `yaml_query` 的场景：

- 需要保留 YAML 注释和原始格式。
- 需要复杂筛选、重写或 merge，应使用 `terminal` 调用 `yq`。
- 需要查询 JSON，应使用 `json_query`。
- YAML 文件超过 `max_file_bytes`，需要 `yq`、流式处理或专门工具。

## 维护注意事项

如果后续修改 `yaml_query`，需要同步检查：

- 参数名是否仍是 `path` 和 `query`。
- 输出是否仍是 pretty JSON 而不是 YAML。
- `normalizeYAMLValue` 行为是否变化。
- 点路径语法是否变化。
- 多文档解析、文件大小和输出大小边界是否仍生效。
- 路径校验是否仍调用 `validatePath`。
