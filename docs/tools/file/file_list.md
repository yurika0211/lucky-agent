# file_list Tool

`file_list` 是 LuckyAgent 的内置目录查看工具，用来列出本地目录中的文件和子目录。它适合在读取、修改、移动或删除文件之前，先确认目录结构和候选路径。

`file_list` 是只读工具，不修改文件系统，因此被标记为自动批准。

## 工具定义

实现位置：

- `internal/tool/builtin_fs.go`

注册信息：

```go
Name:       "file_list"
Category:   CatBuiltin
Source:     "builtin"
Permission: PermAuto
ShellAware: true
```

含义：

- `CatBuiltin`：这是核心内置工具，不依赖 skill 或 MCP。
- `PermAuto`：列目录是只读操作，默认可以自动执行。
- `ShellAware`：agent 可以向工具注入当前工作目录 `_cwd`，相对路径会基于该目录解析。

## 参数

`file_list` 的公开参数是：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `path` | 是 | 无 | 要查看的目录路径。 |
| `recursive` | 否 | `false` | 是否递归列出嵌套文件和目录。 |

handler 还支持一个内部参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `max_entries` | 否 | `200` | 最多返回多少个条目。当前工具 schema 没显式暴露它，但实现会读取这个参数。 |

示例参数：

```json
{
  "path": "docs/tools",
  "recursive": false
}
```

## 执行流程

`file_list` 的执行过程是：

1. 读取必填参数 `path`，没有提供时返回 `path is required`。
2. 通过 `resolvePathArg(args, "path")` 解析目录路径。
3. 读取 `recursive`，没有提供时默认为 `false`。
4. 读取 `max_entries`，没有提供或小于等于 0 时默认为 `200`。
5. 如果 `recursive=false`，使用 `os.ReadDir(path)` 列出当前目录直接子项。
6. 如果 `recursive=true`，使用 `filepath.Walk(path)` 遍历目录树。
7. 输出文件和目录条目。
8. 如果达到 `max_entries`，追加截断提示。

## 输出格式

目录输出格式：

```text
  📁 subdir/
```

文件输出格式：

```text
  📄 file.md (1234 bytes)
```

递归模式下，路径会显示相对被列出的根目录的相对路径：

```text
  📁 ./
  📁 docs/
  📄 docs/readme.md (1200 bytes)
```

非递归模式下，只显示直接子项名称：

```text
  📁 tools/
  📄 README.md (2048 bytes)
```

如果结果被截断，会追加：

```text
  ... truncated after 200 entries
```

## recursive 行为

默认 `recursive=false`，只列出目标目录的一层内容。

当 `recursive=true` 时，工具会遍历整个目录树：

```go
filepath.Walk(path, ...)
```

递归模式会把根目录自身也作为一个条目输出，通常显示为：

```text
  📁 ./
```

递归模式适合查看小型目录结构。大型仓库或产物目录应谨慎使用，并结合 `max_entries` 控制输出规模。

## max_entries 行为

实现里默认最多返回 200 个条目。

如果传入 `max_entries<=0`，会回退到 200。

达到上限时：

- 非递归模式停止继续输出。
- 递归模式通过内部 sentinel error 停止 walk。
- 返回内容末尾追加截断提示。

虽然当前工具参数 schema 没显式声明 `max_entries`，但 handler 支持它。后续如果希望模型稳定使用这个参数，应把它补到 `FileListTool()` 的 `Parameters` 中。

## 路径解析

`file_list` 和其他文件工具共用路径解析逻辑：

- 支持 `~` 和 `~/...` 展开到当前用户 home。
- 相对路径优先相对 `_cwd` 解析。
- `_cwd` 本身必须通过 sandbox 校验才会被采用。
- 路径清理后如果包含 `..`，会被拒绝。
- 最终路径必须通过 `validateSandbox`。

示例：

```json
{
  "path": "docs/tools",
  "recursive": false
}
```

如果当前 `_cwd` 是仓库根目录，这会解析到仓库下的 `docs/tools`，前提是 sandbox 允许该路径。

## 访问限制

`file_list` 使用 `validateSandbox` 做路径限制。当前允许范围包括：

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

## 适合使用的场景

优先使用 `file_list` 的场景：

- 查看目录结构。
- 查找候选文件名。
- 在读取文件前确认路径。
- 在删除、移动、覆盖前确认目标存在。
- 查看生成产物目录中有哪些文件。
- 递归查看小型目录树。

示例：

```json
{
  "path": "docs/tools",
  "recursive": false
}
```

## 不适合使用的场景

不优先使用 `file_list` 的场景：

- 搜索文件内容：使用 `terminal` 跑 `rg`，或使用更专门的搜索工具。
- 读取文件内容：使用 `file_read`。
- 读取 PDF/DOCX/PPTX：使用 `document_read`。
- 修改文件：使用 `file_patch` 或 `file_write`。
- 删除文件：使用 `file_delete`。
- 移动文件：使用 `file_move`。
- 遍历非常大的目录树且没有限制输出。

## 常见调用示例

列出当前目录一层：

```json
{
  "path": ".",
  "recursive": false
}
```

列出文档工具目录：

```json
{
  "path": "docs/tools",
  "recursive": false
}
```

递归列出小目录：

```json
{
  "path": "docs/tools",
  "recursive": true
}
```

递归列出并限制条目数：

```json
{
  "path": "docs",
  "recursive": true,
  "max_entries": 50
}
```

查看临时目录：

```json
{
  "path": "/tmp",
  "recursive": false
}
```

## 和 file_read 的关系

`file_list` 用来看有哪些文件。

`file_read` 用来读取某个文件的内容。

常见流程是：

1. 用 `file_list` 确认目录结构。
2. 用 `file_read` 读取目标文件。
3. 如需修改，再用 `file_patch` 或 `file_write`。

## 和 terminal 的关系

不要优先用 `terminal` 手写：

```sh
ls -la
```

也不要优先用：

```sh
find . -maxdepth 2 -type f
```

正常列目录应使用 `file_list`，因为它有统一的路径解析、权限标记、sandbox 校验、输出限制和清晰的返回格式。

`terminal` 更适合内容搜索、运行项目命令、测试、构建或需要 shell 管道的场景。

## 风险和注意事项

`file_list` 是只读工具，风险主要来自输出规模和路径选择。

调用前应确认：

- 目标路径是目录。
- 是否真的需要递归。
- 递归时是否需要设置 `max_entries`。
- 目标目录不会包含大量无关产物导致上下文污染。

如果要找文件内容，不要用递归列目录替代内容搜索。

## 维护注意事项

如果后续修改 `file_list`，需要同步检查：

- 参数说明是否仍与 `FileListTool()` 一致。
- 是否把 `max_entries` 正式加入工具 schema。
- `recursive` 默认值是否仍是 `false`。
- `max_entries` 默认值是否仍是 `200`。
- 输出图标和格式是否变化。
- 递归模式是否仍使用 `filepath.Walk`。
- 截断提示是否变化。
- 路径解析和 sandbox 规则是否变化。

