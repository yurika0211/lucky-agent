# csv_query Tool

`csv_query` 是 LuckyAgent 的内置 CSV 查询工具，用来流式读取本地 CSV 文件，并按列过滤、投影输出列。它适合快速查看表格、导出数据和测试样本，也能在有限扫描行数内处理较大的 CSV。

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
| `column` | 否 | 无 | 兼容参数，和 `equals` / `contains` / `regex` 配合过滤。 |
| `equals` | 否 | 无 | 精确匹配值，和 `column` 配合使用。 |
| `contains` | 否 | 无 | 包含匹配值，和 `column` 配合使用。 |
| `regex` | 否 | 无 | 正则匹配值，和 `column` 配合使用。 |
| `columns` | 否 | 无 | 只输出指定列，支持数组、JSON 数组字符串或逗号字符串。 |
| `filters` | 否 | 无 | 多条件过滤数组，条件之间是 AND。 |
| `limit` | 否 | `20` | 最多返回多少行，最小 1，最大 100。 |
| `max_scan_rows` | 否 | `100000` | 最多扫描多少数据行，最大 1000000。 |
| `delimiter` | 否 | `,` | 单字符分隔符，TSV 可传 `\t`。 |
| `comment` | 否 | 无 | 可选单字符注释前缀。 |
| `trim_space` | 否 | `false` | 是否 trim 字段首尾空白，并启用 CSV reader 的 leading space 处理。 |
| `lazy_quotes` | 否 | `false` | 是否允许非标准 lazy quote。 |
| `include_meta` | 否 | `false` | 是否返回扫描、匹配、截断 metadata。 |

示例：

```json
{
  "path": "users.csv",
  "column": "role",
  "equals": "admin",
  "columns": ["name", "email"],
  "limit": 50
}
```

## 执行流程

`csv_query` 的执行过程是：

1. 读取必填参数 `path`。
2. 如果 `path` 不是字符串，返回 `path is required`。
3. 调用 `validatePath(path)` 做路径校验。
4. 读取 `limit` 和 `max_scan_rows` 并限制边界。
5. 读取 `column`、`equals`、`contains`、`regex`、`columns`、`filters` 和 CSV 方言参数。
6. 使用 `os.Open(path)` 打开文件。
7. 使用 `csv.NewReader(f)` 流式读取，不调用 `ReadAll`。
8. 第一行作为 header，并建立列名索引。
9. 校验投影列和过滤列是否存在。
10. 逐行读取并应用过滤条件。
11. 每行输出为 `map[string]string`，缺失字段输出空字符串。
12. 默认达到 `limit` 后停止；`include_meta=true` 时会继续扫描到 EOF 或 `max_scan_rows` 以统计匹配行。
13. 使用 pretty JSON 输出结果数组，或带 `rows` / `meta` 的对象。

## 过滤行为

兼容过滤参数：

- `column` + `equals`：精确匹配。
- `column` + `contains`：包含匹配。
- `column` + `regex`：正则匹配。

如果只传 `column`，但没有 `equals`、`contains` 或 `regex`，不会过滤，也不会投影列；仍然返回整行。

多条件过滤使用 `filters`：

```json
{
  "filters": [
    {"column": "role", "op": "eq", "value": "admin"},
    {"column": "email", "op": "suffix", "value": "@example.com"},
    {"column": "score", "op": "gte", "value": "90"}
  ]
}
```

支持的 `op`：

- `eq`
- `neq`
- `contains`
- `prefix`
- `suffix`
- `regex`
- `empty`
- `not_empty`
- `gt`
- `gte`
- `lt`
- `lte`

列名匹配是精确匹配：

```go
if h == column
```

不会忽略大小写。`trim_space=true` 时会 trim header 和字段首尾空白。

如果指定列不存在，返回：

```text
column "<name>" not found
```

列投影：

```json
{
  "columns": ["name", "email"]
}
```

输出只包含这些列。列不存在时报错。

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

`include_meta=true` 时输出：

```json
{
  "rows": [
    {"name": "Ada", "email": "ada@example.com"}
  ],
  "meta": {
    "scanned_rows": 120,
    "matched_rows": 20,
    "returned_rows": 20,
    "max_scan_rows": 100000,
    "scan_limited": false,
    "truncated": false
  }
}
```

## 错误行为

常见错误包括：

```text
open csv file: <error>
read csv: <error>
read csv row 42: <error>
csv is empty
column "role" not found
unsupported filter op "between"
compile regex for column "email": <error>
```

## 适合使用的场景

优先使用 `csv_query` 的场景：

- 快速查看 CSV 前几十行。
- 按单个列或多条件查记录。
- 只输出需要的列。
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

- 需要排序、聚合、join 或复杂表达式。
- 需要扫描超过 `max_scan_rows` 的超大 CSV。
- 需要处理 gzip、Excel 或自动编码识别。
- 需要统计分析，应使用 `terminal` 中的脚本或专门数据工具。

## 维护注意事项

如果后续修改 `csv_query`，需要同步检查：

- 参数名是否仍与 `CSVQueryTool()` 一致。
- `limit` 默认值和最大值是否变化。
- `max_scan_rows` 默认值和最大值是否变化。
- 是否仍使用第一行作为 header。
- 只传 `column` 时是否仍不投影。
- 默认输出是否仍是 pretty JSON 数组。
- `include_meta=true` 时 metadata 字段是否仍稳定。
- 是否仍流式读取 CSV，避免 `ReadAll`。
