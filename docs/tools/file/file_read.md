# file_read Tool

`file_read` 是 LuckyAgent 的内置本地文件读取工具，用来读取普通文本文件内容。它适合在代码、配置、文档草稿、日志片段、生成产物等本地文件是事实来源时使用。

相比通过 `terminal` 执行 `cat`、`sed` 或 `head`，`file_read` 的语义更窄：它只读取文件，不执行 shell 命令，不修改文件，也不会触发额外系统行为。因此它被标记为自动批准工具。

## 工具定义

实现位置：

- `internal/tool/builtin_fs.go`

注册信息：

```go
Name:       "file_read"
Category:   CatBuiltin
Source:     "builtin"
Permission: PermAuto
ShellAware: true
```

含义：

- `CatBuiltin`：这是核心内置工具，不依赖 skill 或 MCP。
- `PermAuto`：读取文件是只读操作，默认可以自动执行。
- `ShellAware`：agent 可以向工具注入当前工作目录 `_cwd`，相对路径会基于该目录解析。

## 参数

`file_read` 接收三个参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `path` | 是 | 无 | 要读取的本地文件路径。 |
| `offset` | 否 | `1` | 起始行号，按 1 开始计数。小于 1 时会回退到 1。 |
| `limit` | 否 | `2000` | 最多读取多少行。 |

示例参数：

```json
{
  "path": "docs/tools/terminal.md",
  "offset": 1,
  "limit": 120
}
```

## 执行流程

`file_read` 的执行过程是：

1. 读取必填参数 `path`，没有提供时返回 `path is required`。
2. 通过 `resolvePathArg(args, "path")` 解析路径。
3. 如果传入的是相对路径，并且 shell 上下文里有合法 `_cwd`，则相对 `_cwd` 解析。
4. 如果仍不是绝对路径，则相对当前进程工作目录解析。
5. 调用 `validateSandbox` 检查路径是否在允许范围内。
6. 使用 `os.ReadFile` 一次性读取整个文件。
7. 按 `\n` 拆分为行。
8. 根据 `offset` 和 `limit` 截取行范围。
9. 输出带行号的文本。

## 输出格式

`file_read` 每一行都带 1-based 行号，格式是：

```text
1| 第一行内容
2| 第二行内容
3| 第三行内容
```

这个格式适合后续引用具体行号，也适合配合 `file_patch` 做精确修改。

如果 `offset` 超过文件总行数，会返回错误：

```text
offset <N> exceeds file length <M>
```

## 路径解析

`file_read` 会走统一路径解析逻辑：

- 支持 `~` 和 `~/...` 展开到当前用户 home。
- 相对路径优先相对 `_cwd` 解析。
- `_cwd` 本身必须通过 sandbox 校验才会被采用。
- 路径清理后如果包含 `..`，会被拒绝。
- 最终路径必须通过 `validateSandbox`。

示例：

```json
{
  "path": "internal/tool/builtin_fs.go",
  "offset": 120,
  "limit": 80
}
```

如果当前 `_cwd` 是仓库根目录，这会解析到：

```text
/media/shiokou/DevRepo60/DevHub/Projects/2026-myapp/luckyagent/internal/tool/builtin_fs.go
```

注意：能否成功读取还取决于 sandbox 是否允许该路径。

## 访问限制

`file_read` 使用 `validateSandbox` 做路径限制。当前允许范围包括：

- `~/.luckyagent/`
- 系统临时目录，例如 `/tmp/`
- `/dev/null`
- 测试场景下，如果 home 目录名是 `.lh-home`，也允许该 home 目录

明确拒绝的敏感路径包括：

- `~/.nanobot`
- `~/.ssh`
- `~/.gnupg`
- `~/.aws`
- `~/.config/gcloud`
- Windows gcloud 配置目录
- `/etc/shadow`
- `/etc/ssh`

此外，路径中包含 `..` 会被视为路径穿越并拒绝。

这意味着 `file_read` 是带路径边界的只读工具，不是任意文件读取工具。

## 适合使用的场景

优先使用 `file_read` 的场景：

- 阅读源码文件。
- 查看配置文件。
- 检查 Markdown 文档。
- 读取生成的文本产物。
- 按行号查看文件局部内容。
- 为后续 `file_patch` 做上下文确认。

示例：

```json
{
  "path": "internal/tool/builtin_fs.go",
  "offset": 128,
  "limit": 60
}
```

## 不适合使用的场景

不优先使用 `file_read` 的场景：

- 列目录：使用 `file_list`。
- 修改文件：使用 `file_patch` 或 `file_write`。
- 删除文件：使用 `file_delete`。
- 移动文件：使用 `file_move`。
- 创建目录：使用 `file_mkdir`。
- 读取 PDF、DOCX、PPTX：使用 `document_read`。
- 查询 JSON 字段：使用 `json_query`。
- 查询 YAML 字段：使用 `yaml_query`。
- 查询 CSV 行：使用 `csv_query`。
- 查看 SQLite schema：使用 `db_schema`。
- 执行需要 shell 的检查：使用 `terminal`。

如果文件是二进制文件，`file_read` 仍会按字节读入并转成字符串，输出通常不可读。二进制、图片、音频、PDF、Office 文件应交给更合适的工具处理。

## 常见调用示例

读取文件开头：

```json
{
  "path": "README.md",
  "offset": 1,
  "limit": 80
}
```

读取某段源码：

```json
{
  "path": "internal/tool/builtin_fs.go",
  "offset": 128,
  "limit": 90
}
```

继续读取下一段：

```json
{
  "path": "internal/tool/builtin_fs.go",
  "offset": 218,
  "limit": 90
}
```

读取 LuckyAgent runtime home 下的文件：

```json
{
  "path": "~/.luckyagent/config.example.json",
  "offset": 1,
  "limit": 120
}
```

## 和 terminal 的关系

当目标只是读取文件内容时，优先使用 `file_read`，不要用：

```sh
cat file
```

也不要优先用：

```sh
sed -n '1,120p' file
```

`terminal` 适合运行命令、测试、构建、CLI 或系统检查；`file_read` 适合读取文件事实。两者职责不同。

使用 `file_read` 的好处：

- 不执行 shell。
- 权限是自动只读。
- 输出自带行号。
- 更容易和 `file_patch` 配合。
- 更容易被 agent 的上下文规划和审计逻辑理解。

## 维护注意事项

如果后续修改 `file_read`，需要同步检查：

- 参数说明是否仍与 `FileReadTool()` 一致。
- `offset` 和 `limit` 的默认值是否变化。
- 输出行号格式是否变化。
- 路径解析逻辑 `resolvePathArg` / `resolvePath` 是否变化。
- sandbox 允许路径和拒绝路径是否变化。
- 是否增加了文件大小限制或二进制文件检测。

