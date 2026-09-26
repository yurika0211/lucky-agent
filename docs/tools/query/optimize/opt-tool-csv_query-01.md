# opt-tool-csv_query-01

## 目标

优化 `csv_query` 的大文件处理、多条件过滤、列投影、CSV 方言和输出 metadata，让它继续保持“快速读取本地 CSV 小表”的定位，同时减少 `ReadAll` 内存风险和单列精确匹配能力不足的问题。

## 当前状态

相关实现：

- `internal/tool/builtin_query.go`
- `docs/tools/query/csv_query.md`

当前流程：

1. 读取 `path`。
2. 调用 `validatePath(path)`。
3. 读取 `limit`，默认 20，最大 100。
4. 读取 `column` 和 `equals`。
5. `csv.NewReader(f).ReadAll()` 一次性读取全部行。
6. 第一行作为 header。
7. 可选按单列 exact equals 过滤。
8. 返回 JSON 数组。

当前优势：

- 简单直观。
- 输出 JSON 方便后续处理。
- limit 有边界。
- 路径经过校验。

## 主要问题

### 1. 一次性 ReadAll 不适合大 CSV

即使只返回前 20 行，也会读取整个文件。

建议改为 streaming 读取，达到 limit 后停止；如果有过滤，则继续扫描但受 `max_scan_rows` 限制。

### 2. 只支持单列 equals

常见需求包括：

- 多条件过滤。
- contains / prefix / regex。
- 数值比较。
- 空值判断。

当前只能单列精确匹配。

### 3. `column` 不做投影

文档已说明只传 `column` 不投影，这容易误用。建议新增 `columns` 参数用于投影。

### 4. CSV 方言不可配置

当前使用默认 comma。缺少：

- delimiter
- comment
- lazy_quotes
- trim_leading_space

### 5. 输出缺少扫描统计

不知道扫描了多少行、匹配多少行、是否达到 scan limit。

## 优化原则

1. 默认保持小表快速查看。
2. 大文件必须 streaming。
3. 过滤能力增加但不变成完整 SQL 引擎。
4. 输出 metadata 说明是否截断或扫描受限。

## 推荐方案

### 1. 参数扩展

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `columns` | 否 | 无 | 只输出指定列。 |
| `filters` | 否 | 无 | 多条件过滤 JSON。 |
| `contains` | 否 | 无 | 和 `column` 配合，包含匹配。 |
| `regex` | 否 | 无 | 和 `column` 配合，正则匹配。 |
| `max_scan_rows` | 否 | `100000` | 最多扫描多少数据行。 |
| `delimiter` | 否 | `,` | 分隔符。 |
| `trim_space` | 否 | `false` | 是否 trim 字段。 |
| `include_meta` | 否 | `false` | 是否返回 metadata。 |

### 2. streaming 读取

用 `reader.Read()` 循环：

1. 读 header。
2. 逐行读。
3. 判断过滤。
4. append 输出。
5. 达到 limit 后停止；若需要统计总匹配，可继续扫描到 max_scan_rows。

### 3. 多条件 filters

示例：

```json
{
  "filters": [
    {"column": "role", "op": "eq", "value": "admin"},
    {"column": "email", "op": "contains", "value": "@example.com"}
  ]
}
```

支持 op：

- `eq`
- `neq`
- `contains`
- `prefix`
- `suffix`
- `regex`
- `empty`
- `not_empty`

### 4. 列投影

```json
{
  "columns": ["name", "email"]
}
```

输出只包含这些列。列不存在时报错。

### 5. metadata 输出

`include_meta=true`：

```json
{
  "rows": [...],
  "meta": {
    "scanned_rows": 120,
    "matched_rows": 20,
    "returned_rows": 20,
    "truncated": true
  }
}
```

## 分阶段实施

### 第一阶段：streaming 和 meta

- 去掉 `ReadAll`。
- 增加 `max_scan_rows`。
- 增加 `include_meta`。

### 第二阶段：投影和过滤

- 增加 `columns`。
- 增加 filters。
- 保持 `column` + `equals` 兼容。

### 第三阶段：CSV 方言

- delimiter。
- trim_space。
- lazy_quotes。

## 测试建议

- 空 CSV 报错。
- 大 CSV 不 ReadAll。
- limit 生效。
- max_scan_rows 生效。
- column + equals 保持兼容。
- columns 投影生效。
- filters 多条件生效。
- regex filter 编译错误可见。
- delimiter 支持 TSV。
- include_meta 返回 scanned/matched/truncated。

## 文档更新

同步更新 `docs/tools/query/csv_query.md` 参数表、过滤示例、投影示例和大文件行为。

## 风险与边界

- 多条件过滤不要扩展到复杂表达式嵌套。
- 统计总匹配可能需要扫描更多行，应受 max_scan_rows 控制。
- delimiter 只允许单 rune。

## 推荐结论

优先把 `ReadAll` 改成 streaming，并新增 `columns` 投影。这样能保留轻量工具定位，同时覆盖最常见的 CSV 查询需求。
