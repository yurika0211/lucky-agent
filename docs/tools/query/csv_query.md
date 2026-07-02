# csv_query Tool

`csv_query` 是 LuckyAgent 的内置 CSV 查询工具，用来读取本地 CSV 文件，并按单个列名做可选的精确匹配过滤。它适合快速查看小型表格、导出数据和测试样本。

这是只读工具，不修改本地状态，因此被标记为自动批准。

## 工具定义

实现位置：

- `internal/tool/builtin_query.go`
- `internal/tool/builtin_helpers.go`

注册信息：

```go
Name:         "csv_query"
Category:     CatBuiltin
Source:       "builtin"
Permission:   PermAuto
ParallelSafe: true
```

## 参数

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `path` | 是 | 无 | CSV 文件路径。 |
| `column` | 否 | 无 | 可选列名，用来过滤。 |
| `equals` | 否 | 无 | 可选精确匹配值，和 `column` 配合使用。 |
| `limit` | 否 | `20` | 最多返回多少行，最小 1，最大 100。 |

示例：

```json
{
  "path": "users.csv",
  "column": "role",
  "equals": "admin",
  "limit": 50
}
```

## 执行流程

`csv_query` 的执行过程是：

1. 读取必填参数 `path`。
2. 如果 `path` 不是字符串，返回 `path is required`。
3. 调用 `validatePath(path)` 做路径校验。
4. 读取 `limit`，通过 `boundedIntArg(args, "limit", 20, 1, 100)` 限制范围。
5. 读取 `column` 和 `equals`。
6. 使用 `os.Open(path)` 打开文件。
7. 使用 `csv.NewReader(f).ReadAll()` 一次性读取全部行。
8. 第一行作为 header。
9. 如果传入 `column`，查找对应列下标。
10. 遍历数据行。
11. 如果同时提供 `column` 和非空 `equals`，只保留该列等于 `equals` 的行。
12. 每行输出为 `map[string]string`。
13. 达到 `limit` 后停止。
14. 使用 pretty JSON 输出结果数组。

## 过滤行为

只有同时满足以下条件时才会过滤：

- `column` 非空。
- `equals` 去掉空白后非空。

如果只传 `column`，但 `equals` 为空，不会过滤，也不会投影列；仍然返回整行。

列名匹配是精确匹配：

```go
if h == column
```

不会忽略大小写，也不会 trim header。

如果指定列不存在，返回：

```text
column "<name>" not found
```

## 输出格式

输出是 JSON 数组，每一行是对象。

CSV：

```csv
name,role
Ada,admin
Bob,user
```

调用：

```json
{
  "column": "role",
  "equals": "admin"
}
```

返回：

```json
[
  {
    "name": "Ada",
    "role": "admin"
  }
]
```

如果某行字段数量少于 header，缺失字段会输出为空字符串。

## 错误行为

常见错误包括：

```text
open csv file: <error>
read csv: <error>
csv is empty
column "role" not found
```

## 适合使用的场景

优先使用 `csv_query` 的场景：

- 快速查看 CSV 前几十行。
- 按单个列精确匹配查记录。
- 检查导出数据是否包含某类行。
- 把 CSV 行转换成 JSON 方便阅读。

示例：

```json
{
  "path": "reports/users.csv",
  "column": "status",
  "equals": "active",
  "limit": 20
}
```

## 不适合使用的场景

不优先使用 `csv_query` 的场景：

- 需要多条件过滤、排序、聚合。
- 需要只投影某一列；当前 `column` 不做投影。
- 需要处理超大 CSV；当前会一次性 `ReadAll`。
- 需要容错复杂 CSV 方言。
- 需要统计分析，应使用 `terminal` 中的脚本或专门数据工具。

## 维护注意事项

如果后续修改 `csv_query`，需要同步检查：

- 参数名是否仍是 `path`、`column`、`equals`、`limit`。
- `limit` 默认值和最大值是否变化。
- 是否仍使用第一行作为 header。
- 只传 `column` 时是否仍不投影。
- 输出是否仍是 pretty JSON 数组。
- 是否仍一次性读取全部 CSV。
