# opt-tool-log_grep-01

## 目标

优化 `log_grep` 的大文件搜索、正则安全、匹配模式、输出截断和无结果行为，让它继续保持“快速定位日志匹配上下文”的定位，同时避免整文件读取和输出过长。

## 当前状态

相关实现：

- `internal/tool/builtin_query.go`
- `docs/tools/query/log_grep.md`

当前流程：

1. 读取 `path` 和 `pattern`。
2. 调用 `validatePath(path)`。
3. 读取 `regex`、`before`、`after`、`max_matches`。
4. regex=true 时编译 Go regexp。
5. `os.ReadFile(path)` 整文件读取。
6. Split 成所有行。
7. 逐行匹配。
8. 输出带行号上下文块。
9. 无匹配时返回 error。

当前优势：

- 支持普通字符串和 regexp。
- before/after/max_matches 有边界。
- 输出带行号。
- 上下文重叠会去重。

## 主要问题

### 1. 整文件读取不适合大日志

搜索日志应 streaming scan，当前会读完整文件。

### 2. 无匹配作为 error 不利于自动流程

无匹配不是工具失败，很多情况下是正常结果。

建议返回结构化 no_match，而不是 error。文本模式可返回：

```text
No matches for "ERROR" in app.log
```

### 3. 普通匹配大小写敏感且不可配置

常见日志查询需要 ignore_case。

### 4. 输出缺少最大字节限制

max_matches 限制匹配块数量，但长行仍可能输出过大。

### 5. 正则没有超时问题但仍需复杂度边界

Go regexp 是 RE2，不会灾难性回溯，但超长 pattern 和超大行仍可能影响性能。

## 优化原则

1. grep 应 streaming，不整文件加载。
2. 无匹配应是正常结果。
3. 输出必须受 matches 和 bytes 双重限制。
4. 默认字符串匹配保持兼容，新增 ignore_case。

## 推荐方案

### 1. 参数扩展

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `ignore_case` | 否 | `false` | 是否忽略大小写。 |
| `max_scan_lines` | 否 | `1000000` | 最多扫描多少行。 |
| `max_output_bytes` | 否 | `65536` | 最大输出字节数。 |
| `include_meta` | 否 | `false` | 是否返回扫描统计。 |
| `from_end` | 否 | `false` | 是否优先从文件尾部查。 |

### 2. streaming scan

使用 `bufio.Scanner` 或 `bufio.Reader`：

- 自定义 buffer，支持长行。
- ring buffer 保存 before context。
- match 后继续输出 after context。
- 达到 max_matches 或 max_output_bytes 停止。

### 3. no match 非 error

返回：

```json
{
  "matches": [],
  "meta": {
    "matched": false,
    "scanned_lines": 1234
  }
}
```

文本模式返回简短 no match 文案。

### 4. ignore_case

普通匹配：

```go
strings.Contains(strings.ToLower(line), strings.ToLower(pattern))
```

regex 模式可以自动加 `(?i)` 或用 regexp 处理。

### 5. 输出 metadata

`include_meta=true`：

```json
{
  "blocks": [...],
  "meta": {
    "scanned_lines": 5000,
    "matches": 20,
    "max_matches_reached": true,
    "output_truncated": false
  }
}
```

## 分阶段实施

### 第一阶段：streaming 和 no-match

- 改成 streaming。
- 无匹配返回正常结果。
- 增加 max_output_bytes。

### 第二阶段：匹配能力

- ignore_case。
- from_end。
- include_meta。

### 第三阶段：结构化输出

- `format=json`。
- blocks/meta 稳定结构。

## 测试建议

- 普通匹配保持兼容。
- regex 匹配保持兼容。
- regex 编译失败报错。
- 无匹配不返回 error。
- ignore_case 生效。
- before/after 上下文正确。
- 重叠上下文不重复。
- max_output_bytes 截断。
- 大文件 streaming。

## 文档更新

同步更新 `docs/tools/query/log_grep.md` 的无匹配行为、ignore_case、streaming 和 metadata。

## 风险与边界

- 无匹配从 error 改为正常返回可能影响旧测试。
- Scanner 默认 token 限制要显式调大。
- from_end 实现复杂，可放后续。

## 推荐结论

优先改 streaming 和无匹配行为。日志搜索中“没找到”是有效信息，不应该被当作工具执行失败。
