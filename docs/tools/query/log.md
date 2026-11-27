# data Tool：日志

日志读取现在是 `data` 的 `action=log`。`mode=tail` 读取文件末尾，`mode=grep` 搜索匹配行并返回上下文。它适合排查最近的运行时错误、堆栈、请求 ID 或任务执行尾部状态。

这是只读工具，不修改本地状态，因此被标记为自动批准。

## 工具定义

实现位置：

- `internal/tool/builtin_query.go`

注册信息：

```go
Name:         "data"
action:       "log"
Category:     CatBuiltin
Source:       "builtin"
Permission:   PermAuto
ParallelSafe: true
```

## 参数

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `action` | 是 | 无 | 固定为 `log`。 |
| `mode` | 是 | 无 | `tail` 读尾部，`grep` 搜索。 |
| `path` | 是 | 无 | 日志文件路径。 |
| `lines` | 否 | `100` | `action=tail` 返回末尾多少行，最小 1，最大 500。 |
| `max_bytes` | 否 | `65536` | `action=tail` 最多从文件尾部读取多少字节，最大 1048576。 |
| `with_line_numbers` | 否 | `false` | `action=tail` 是否给返回行加原文件 1-based 行号。 |
| `pattern` | `grep` 时必填 | 无 | 要搜索的字符串或正则表达式。 |
| `regex` | 否 | `false` | `action=grep` 是否把 `pattern` 当作正则。 |
| `before` | 否 | `2` | `action=grep` 每个匹配行之前返回多少行，最大 20。 |
| `after` | 否 | `2` | `action=grep` 每个匹配行之后返回多少行，最大 20。 |
| `max_matches` | 否 | `20` | `action=grep` 最多返回多少个匹配，最大 100。 |
| `ignore_case` | 否 | `false` | `action=grep` 是否忽略大小写。 |
| `max_scan_lines` | 否 | `1000000` | `action=grep` 最多扫描多少行，最大 10000000。 |
| `max_output_bytes` | 否 | `65536` | `action=grep` 最大输出字节，最大 1048576。 |
| `include_meta` | 否 | `false` | 是否返回 JSON metadata。 |

读尾部：

```json
{
  "action": "tail",
  "path": "logs/luckyagent.log",
  "lines": 120
}
```

搜索：

```json
{
  "action": "grep",
  "path": "logs/server.log",
  "pattern": "request_id=abc123",
  "before": 5,
  "after": 10
}
```

## 执行流程

`mode` 只接受 `tail` 和 `grep`。缺失或其他值返回 `invalid log_file action`。

`tail` 从文件尾部反向按块读取，不会为了返回尾部行而读取整个文件。二进制文件会被拒绝。`with_line_numbers=true` 会额外扫描文件统计总行数。

`grep` 流式扫描文件。正则使用 Go 标准库 regexp 语法。没有匹配时返回正常的 no-match 文本，不返回 error。匹配行以 `> 行号|` 标记，上下文行以空格前缀标记。

## 不适合使用的场景

- 实时跟踪日志，使用 `terminal` 执行 `tail -f`。
- 多文件或复杂批量分析，使用 `terminal` 调用 `rg`、`grep` 或 `awk`。
- 解析结构化 JSON 日志，使用 `data_query`。
