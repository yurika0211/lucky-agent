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
4. 使用 `os.ReadFile(path)` 读取文件。
5. 使用 `yaml.Unmarshal` 解析 YAML。
6. 调用 `normalizeYAMLValue` 把 YAML map 规范化为 `map[string]any`。
7. 如果 `query` 为空，返回整个文档的 pretty JSON。
8. 如果 `query` 非空，使用 `walkStructuredPath` 提取值。
9. 使用 `prettyStructuredValue` 输出结果。

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

示例：

```text
service.name
metadata.labels.app
items[0].metadata.name
```

当前不支持：

- 带点号的 key 转义。
- 通配符。
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

## 错误行为

常见错误包括：

```text
read yaml file: <error>
parse yaml: <error>
path "service" expected object
path key "service" not found
path index 0 expected array
path index 0 out of range
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
- YAML 文件非常大且不适合一次性加载。

## 维护注意事项

如果后续修改 `yaml_query`，需要同步检查：

- 参数名是否仍是 `path` 和 `query`。
- 输出是否仍是 pretty JSON 而不是 YAML。
- `normalizeYAMLValue` 行为是否变化。
- 点路径语法是否变化。
- 是否仍一次性读取整个文件。
- 路径校验是否仍调用 `validatePath`。
