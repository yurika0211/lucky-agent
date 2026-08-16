# log_tail Tool

`log_tail` 是 LuckyAgent 的内置日志读取工具，用来读取本地日志文件末尾的若干行。它适合调试最近发生的错误、服务启动失败、运行时异常或任务执行尾部状态。

这是只读工具，不修改本地状态，因此被标记为自动批准。

## 工具定义

实现位置：

- `internal/tool/builtin_query.go`

注册信息：

```go
Name:         "log_tail"
Category:     CatBuiltin
Source:       "builtin"
Permission:   PermAuto
ParallelSafe: true
```

含义：

- `CatBuiltin`：这是核心内置工具，不依赖 skill 或 MCP。
- `PermAuto`：读取日志是只读操作，默认可以自动执行。
- `ParallelSafe=true`：工具不修改共享状态，可以和其他只读工具并行。

## 参数

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `path` | 是 | 无 | 日志文件路径。 |
| `lines` | 否 | `100` | 返回末尾多少行，最小 1，最大 500。 |
| `max_bytes` | 否 | `65536` | 最多从文件尾部读取多少字节，最大 1048576。 |
| `with_line_numbers` | 否 | `false` | 是否给返回行加原文件 1-based 行号。 |
| `include_meta` | 否 | `false` | 是否返回 JSON metadata。 |

示例参数：

```json
{
  "path": "/tmp/app.log",
  "lines": 80
}
```

## 执行流程

`log_tail` 的执行过程是：

1. 读取必填参数 `path`。
2. 如果 `path` 不是字符串，返回 `path is required`。
3. 调用 `validatePath(path)` 做路径校验。
4. 读取 `lines` 参数，并通过 `boundedIntArg(args, "lines", 100, 1, 500)` 限制范围。
5. 读取 `max_bytes`、`with_line_numbers` 和 `include_meta`。
6. 打开文件并从文件尾部按块反向读取，直到覆盖请求行数或达到 `max_bytes`。
7. 对尾部片段做二进制 NUL 检测。
8. 将 CRLF 统一替换为 LF。
9. 按 `\n` 分割行并返回最后 `lines` 行。
10. 如果 `with_line_numbers=true`，额外扫描文件统计总行数后加行号。

## 输出格式

输出是纯文本日志片段，不额外添加行号、文件名或标题。

例如日志文件末尾是：

```text
error second
four
```

如果启用 `with_line_numbers=true`，输出会加原文件行号：

```text
5| error second
6| four
```

如果启用 `include_meta=true`，输出是 JSON：

```json
{
  "lines": ["error second", "four"],
  "meta": {
    "path": "/tmp/app.log",
    "file_size": 12345,
    "max_bytes": 65536,
    "returned_lines": 2,
    "truncated": false
  }
}
```

调用：

```json
{
  "path": "/tmp/app.log",
  "lines": 2
}
```

返回：

```text
error second
four
```

## lines 行为

`lines` 通过 `boundedIntArg` 限制：

| 输入 | 实际行为 |
| --- | --- |
| 未提供 | 使用 `100` |
| 小于 1 | 使用边界逻辑限制到有效范围 |
| 大于 500 | 最多返回 `500` 行 |

当前实现从文件尾部反向按块读取，不会为了返回尾部行而读取整个文件。

## 适合使用的场景

优先使用 `log_tail` 的场景：

- 查看服务最近的错误。
- 检查启动日志尾部。
- 读取任务执行最后几行。
- 快速判断日志是否仍在写入。
- 不需要复杂过滤，只想看尾部上下文。

示例：

```json
{
  "path": "logs/luckyagent.log",
  "lines": 120
}
```

## 不适合使用的场景

不优先使用 `log_tail` 的场景：

- 需要搜索关键字，应使用 `log_grep`。
- 需要实时跟踪日志，应使用 `terminal` 执行 `tail -f`。
- 需要解析结构化 JSON 日志，应使用 `json_query` 或专门脚本。

## 和 file_read 的关系

`file_read` 适合读取一般文本文件，并支持 offset/limit 类分页能力。

`log_tail` 专注日志尾部，默认读取最后 100 行，更适合排查最近发生的问题。

## 风险和注意事项

`log_tail` 的主要注意点：

- 默认最多读取尾部 65536 字节，超出时返回尾部可用内容并在 metadata 中标记截断。
- 只做尾部行截取，不识别日志级别。
- 默认不返回行号，`with_line_numbers=true` 会额外扫描文件统计总行数。
- 二进制文件会被拒绝。
- `path` 会经过通用路径校验。
- 最大返回 500 行。

## 维护注意事项

如果后续修改 `log_tail`，需要同步检查：

- 参数名是否仍是 `path` 和 `lines`。
- `lines` 默认值是否仍是 100。
- 最大行数是否仍是 500。
- 是否仍使用尾部块读取，避免整文件加载。
- CRLF 到 LF 的规范化行为是否变化。
- 默认输出是否仍不带行号。
