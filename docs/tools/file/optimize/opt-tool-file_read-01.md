# opt-tool-file_read-01

## 目标

优化 `file_read` 的读取体验、资源占用和可维护性，让它继续保持“窄语义、自动批准、只读文件事实来源”的定位，同时更适合读取大文件、局部代码片段和后续可审计修改。

本方案聚焦：

- 大文件读取效率
- 输出续读提示
- 参数边界
- 二进制和非普通文本文件处理
- 与 `file_patch`、`document_read`、`terminal cat` 的职责边界

## 当前状态

相关实现：

- `internal/tool/builtin_fs.go`
- `internal/tool/builtin_test.go`
- `docs/tools/file_read.md`

当前 `file_read` 的流程是：

1. 通过 `resolvePathArg(args, "path")` 解析路径。
2. 调用 `validateSandbox` 做路径边界检查。
3. 使用 `os.ReadFile(path)` 一次性读取整个文件。
4. `strings.Split(string(data), "\n")` 拆成行。
5. 根据 `offset` 和 `limit` 截取输出范围。
6. 输出 `行号| 内容`。

当前默认参数：

- `offset`: 1
- `limit`: 2000

当前优势：

- 语义明确，只读本地文件。
- 不执行 shell。
- 自动批准。
- 输出自带行号，适合解释和后续 patch。
- 相对路径会结合 shell 上下文 `_cwd` 解析。

## 主要问题

### 1. 大文件会被全量读入内存

当前使用 `os.ReadFile`，即使只请求：

```json
{
  "path": "large.log",
  "offset": 100000,
  "limit": 80
}
```

工具也会先读取整个文件，再拆分所有行。这对大型日志、生成产物、索引文件不友好。

问题影响：

- 内存占用随文件大小增长。
- 对超大文件的响应变慢。
- 读取二进制或 minified 文件时可能产生大量不可用输出。

### 2. 没有续读提示

如果用户读取前 120 行，而文件还有后续内容，当前输出不会提示下一次应该从哪里继续。

对比 `document_read` 已经有类似提示：

```text
... truncated; use offset=<N> to continue
```

`file_read` 也应该提供一致体验。

### 3. `limit` 没有上限和下限归一

当前 `offset < 1` 会回退到 1，但 `limit` 没有明确处理：

- `limit <= 0` 时，可能导致空输出或非预期行为。
- 非常大的 `limit` 会放大输出和上下文压力。

建议将 `limit` 统一约束为：

- 默认：2000
- 最小：1
- 最大：5000 或 10000

具体最大值需要结合 agent 上下文裁剪策略确认。第一阶段建议 `5000`，更保守。

### 4. 缺少二进制文件检测

文档已经说明二进制文件不适合 `file_read`，但实现层仍会把字节转成字符串输出。

建议在读取前或读取头部样本时检测：

- NUL byte
- 过高比例的不可打印字符
- 常见二进制魔数

命中时返回明确错误：

```text
file appears to be binary; use document_read or a media-specific tool when appropriate
```

### 5. 普通文本和结构化查询边界还可以更清楚

`file_read` 适合读“原始文本事实”，但对于 JSON、YAML、CSV、SQLite，已有更窄工具：

- `json_query`
- `yaml_query`
- `csv_query`
- `db_schema`
- `sql_query`

优化后可以在文档和工具描述里更明确：

- 需要阅读上下文时用 `file_read`。
- 需要查询字段或表结构时用对应 query 工具。

## 优化原则

1. 保持 `file_read` 只读，不增加写入或 shell 行为。
2. 不引入复杂抽象，优先用 Go 标准库实现流式读取。
3. 输出格式保持兼容：继续使用 `行号| 内容`。
4. 默认参数尽量不破坏现有调用。
5. 对大文件和二进制文件给出清楚反馈，而不是输出大量无用文本。

## 推荐方案

### 1. 抽出参数解析函数

新增小函数：

```go
func fileReadRange(args map[string]any) (offset int, limit int)
```

行为：

- `offset` 默认 1，小于 1 回退 1。
- `limit` 默认 2000。
- `limit <= 0` 回退 2000。
- `limit > maxFileReadLines` 截到上限。

建议常量：

```go
const (
	defaultFileReadLimit = 2000
	maxFileReadLimit     = 5000
)
```

收益：

- 参数行为集中。
- 便于测试。
- 后续 `document_read` 如需统一参数策略，也可参考。

### 2. 将全量读取改为流式读取

新增内部函数：

```go
func readTextFileLines(path string, offset, limit int) (fileReadResult, error)
```

返回结构：

```go
type fileReadResult struct {
	Lines       []numberedLine
	NextOffset  int
	Truncated   bool
	TotalKnown  bool
	TotalLines  int
}

type numberedLine struct {
	Number int
	Text   string
}
```

实现方式：

- `os.Open(path)`
- `bufio.Reader` 或 `bufio.Scanner`
- 跳过 `offset - 1` 行。
- 读取 `limit` 行。
- 再尝试读取一行判断是否还有后续。

注意：

- `bufio.Scanner` 默认 token 限制是 64K，不适合很长行。
- 推荐使用 `bufio.Reader.ReadString('\n')` 或 `ReadBytes('\n')`，避免长行直接报错。

输出保持：

```text
120| line text
121| line text
```

如果还有后续：

```text
... truncated; use offset=122 to continue
```

### 3. 增加文件类型预检查

在打开文件后先做轻量检查：

1. `Stat()` 判断是否普通文件。
2. 读取前 4096 字节样本。
3. 检测二进制特征。
4. `Seek(0, io.SeekStart)` 后再正式按行读取。

非普通文件返回：

```text
path is not a regular file
```

二进制文件返回：

```text
file appears to be binary; use document_read or a media-specific tool when appropriate
```

这可以避免误读目录、设备文件、大型二进制产物。

### 4. 输出文件元信息摘要

可以考虑在输出末尾增加轻量摘要，但要谨慎保持兼容。

推荐第一阶段只加续读提示，不加头部元信息，避免破坏依赖 `行号| 内容` 的解析逻辑。

如果后续需要，可加可选参数：

```json
{
  "include_meta": true
}
```

返回：

```text
[file] path=... size=... offset=... limit=...
```

第一版不建议做。

### 5. 保持和 `file_patch` 的行号兼容

`file_read` 的一个核心价值是配合 `file_patch` 做可审计修改。因此优化后必须保持：

- 行号从 1 开始。
- 输出格式仍是 `%d| %s\n`。
- `offset` 对应真实文件行号。
- 空行也占一行。

换行边界需要测试：

- 文件末尾有换行。
- 文件末尾没有换行。
- 连续空行。
- Windows CRLF。

## 分阶段实施

### Phase 1：参数归一和续读提示

改动范围：

- `internal/tool/builtin_fs.go`
- `internal/tool/builtin_test.go`
- `docs/tools/file_read.md`

内容：

- 抽出 `fileReadRange`。
- 限制 `limit` 下限和上限。
- 当输出被 `limit` 截断时追加 `use offset=<N> to continue`。

验收标准：

- `go test ./internal/tool` 通过。
- 现有默认读取行为不变。
- `limit <= 0` 有确定行为。
- 读取超过 `limit` 的文件时出现续读提示。

### Phase 2：流式读取

改动范围：

- 新增 `readTextFileLines`。
- 替换 `os.ReadFile + strings.Split`。
- 增加大文件测试。

验收标准：

- 读取大文件时不需要全量加载。
- `offset` 很大时仍能正确定位并输出。
- 末尾无换行的最后一行不丢失。
- 输出格式与旧版兼容。

### Phase 3：二进制和普通文件检查

内容：

- `Stat()` 检查普通文件。
- 样本级二进制检测。
- 错误信息指向更合适工具。

验收标准：

- 目录路径返回清晰错误。
- 含 NUL byte 的文件被拒绝。
- 普通 UTF-8 文本正常读取。
- CRLF 文本正常读取。

### Phase 4：文档和工具描述微调

内容：

- 更新 `docs/tools/file_read.md`。
- 在工具描述中强调“普通文本文件”。
- 增加大文件续读示例。
- 增加二进制文件错误说明。

验收标准：

- 文档与实现一致。
- `terminal.md` 中关于 `file_read` 优先于 `cat` 的描述仍成立。

## 测试建议

新增或补充测试：

### 参数边界

- 默认 `offset=1, limit=2000`。
- `offset=0` 回退到 1。
- `limit=0` 回退到默认值。
- `limit` 超过最大值时被截断。

### 输出和续读

- 读取 5 行文件，`limit=2`，输出 1、2 行并提示 `offset=3`。
- 读取最后一段时不提示续读。
- `offset` 超过文件总行数时返回 `offset <N> exceeds file length <M>`。

### 换行边界

- 文件末尾有 `\n`。
- 文件末尾没有 `\n`。
- 连续空行。
- CRLF 文件。

### 文件类型

- 目录路径。
- 含 NUL byte 的二进制文件。
- 普通 Markdown 文件。
- 大型日志文件。

## 风险与边界

1. 流式读取无法在不扫描全文件的情况下快速知道总行数。
   如果 `offset` 超过文件长度，仍需要扫描到 EOF 才能返回准确行数。

2. 二进制检测只能是启发式。
   某些非 UTF-8 文本可能被误判，某些二进制也可能绕过样本检测。

3. 续读提示会改变输出末尾。
   如果有调用方严格依赖纯 `行号| 内容` 输出，需要确认影响。考虑到 `document_read` 已有类似提示，这个变化总体可接受。

4. `file_read` 不应变成格式解析工具。
   JSON/YAML/CSV/SQLite 的字段查询仍应交给更窄的 query 工具。

## 推荐结论

优先实现 Phase 1 和 Phase 2。

Phase 1 可以马上改善使用体验，成本低、风险小。Phase 2 解决当前最真实的性能和内存问题，让 `file_read` 更适合日志和大文件。Phase 3 再补上二进制检测和普通文件检查，提升错误反馈质量。
