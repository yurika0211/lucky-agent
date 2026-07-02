# log_grep Tool

`log_grep` 是 LuckyAgent 的内置日志搜索工具，用来在本地日志文件中按字符串或正则查找匹配行，并返回匹配行前后的上下文。它适合定位错误、堆栈、请求 ID 或某段日志爆发区间。

这是只读工具，不修改本地状态，因此被标记为自动批准。

## 工具定义

实现位置：

- `internal/tool/builtin_query.go`

注册信息：

```go
Name:         "log_grep"
Category:     CatBuiltin
Source:       "builtin"
Permission:   PermAuto
ParallelSafe: true
```

含义：

- `CatBuiltin`：这是核心内置工具，不依赖 skill 或 MCP。
- `PermAuto`：搜索日志是只读操作，默认可以自动执行。
- `ParallelSafe=true`：工具不修改共享状态，可以和其他只读工具并行。

## 参数

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `path` | 是 | 无 | 日志文件路径。 |
| `pattern` | 是 | 无 | 要搜索的字符串或正则表达式。 |
| `regex` | 否 | `false` | 是否把 `pattern` 当作正则表达式。 |
| `before` | 否 | `2` | 每个匹配行之前返回多少行，最小 0，最大 20。 |
| `after` | 否 | `2` | 每个匹配行之后返回多少行，最小 0，最大 20。 |
| `max_matches` | 否 | `20` | 最多返回多少个匹配块，最小 1，最大 100。 |

示例参数：

```json
{
  "path": "/tmp/app.log",
  "pattern": "ERROR",
  "before": 2,
  "after": 4
}
```

## 执行流程

`log_grep` 的执行过程是：

1. 读取必填参数 `path` 和 `pattern`。
2. 如果 `path` 不是字符串，返回 `path is required`。
3. 如果 `pattern` 为空，返回 `pattern is required`。
4. 调用 `validatePath(path)` 做路径校验。
5. 读取 `regex`、`before`、`after`、`max_matches`。
6. 如果 `regex=true`，调用 `regexp.Compile(pattern)` 编译正则。
7. 使用 `os.ReadFile(path)` 读取整个日志文件。
8. 将 CRLF 统一替换为 LF。
9. 逐行查找匹配。
10. 为每个匹配行计算上下文范围。
11. 输出带行号的上下文块。
12. 如果没有匹配，返回 `pattern not found in <path>`。

## 匹配模式

默认情况下，`pattern` 使用普通字符串包含匹配：

```go
strings.Contains(line, pattern)
```

启用正则时：

```json
{
  "regex": true,
  "pattern": "error|panic|fatal"
}
```

会使用：

```go
regexp.Compile(pattern)
```

正则编译失败时返回：

```text
compile regex: <error>
```

## 输出格式

输出按上下文块组织。匹配行用 `>` 标记，普通上下文行用两个空格标记。

格式：

```text
  1| one
> 2| error first
---
  4| three
> 5| error second
```

含义：

- `>`：这一行命中 pattern。
- `1|`：原文件中的 1-based 行号。
- `---`：不同匹配块之间的分隔线。

如果上下文范围重叠，工具会通过 `lastIncluded` 跳过已经输出过的行，避免重复。

## 参数边界

`before`、`after`、`max_matches` 都通过 `boundedIntArg` 限制。

| 参数 | 默认值 | 最小值 | 最大值 |
| --- | --- | --- | --- |
| `before` | `2` | `0` | `20` |
| `after` | `2` | `0` | `20` |
| `max_matches` | `20` | `1` | `100` |

## 适合使用的场景

优先使用 `log_grep` 的场景：

- 搜索错误关键字。
- 根据 request id 查上下文。
- 定位 panic、stack trace 或异常状态。
- 从日志中抽取少量匹配区间。
- 正则查找某类错误模式。

示例：

```json
{
  "path": "logs/server.log",
  "pattern": "request_id=abc123",
  "before": 5,
  "after": 10
}
```

## 不适合使用的场景

不优先使用 `log_grep` 的场景：

- 只想看日志末尾，应使用 `log_tail`。
- 需要高性能搜索超大日志，应使用 `terminal` 调用 `rg`、`grep` 或 `awk`。
- 需要实时搜索流式日志，应使用 `terminal`。
- 需要结构化查询 JSON/CSV/SQLite，应使用对应查询工具。

## 风险和注意事项

`log_grep` 的主要注意点：

- 会一次性读取整个文件。
- 普通匹配大小写敏感。
- 正则使用 Go 标准库 regexp 语法。
- 没有匹配时以 error 形式返回。
- 输出最多 100 个匹配块。
- 上下文行最多前后各 20 行。

## 维护注意事项

如果后续修改 `log_grep`，需要同步检查：

- 参数表是否仍与 `LogGrepTool()` 一致。
- 默认上下文是否仍是前后各 2 行。
- `max_matches` 默认值和最大值是否变化。
- 是否仍使用整文件读取。
- 输出行号格式是否变化。
- 没有匹配时的错误文案是否变化。
