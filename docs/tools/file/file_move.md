# file_move Tool

`file_move` 是 LuckyAgent 的内置文件系统移动工具，用来移动或重命名本地文件、目录。它适合在需要整理产物、重命名文件、调整目录结构、把临时文件移动到目标位置时使用。

`file_move` 会改变磁盘状态，且在 `overwrite=true` 时会删除已有目标路径，因此它被标记为需要审批。

## 工具定义

实现位置：

- `internal/tool/builtin_fs.go`

注册信息：

```go
Name:       "file_move"
Category:   CatBuiltin
Source:     "builtin"
Permission: PermApprove
ShellAware: true
```

含义：

- `CatBuiltin`：这是核心内置工具，不依赖 skill 或 MCP。
- `PermApprove`：移动或重命名文件会改变磁盘状态，默认需要审批。
- `ShellAware`：agent 可以向工具注入当前工作目录 `_cwd`，相对路径会基于该目录解析。

## 参数

`file_move` 接收三个参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `src` | 是 | 无 | 已存在的源文件或源目录路径。 |
| `dst` | 是 | 无 | 移动后的目标路径。 |
| `overwrite` | 否 | `false` | 目标路径已存在时是否替换它。 |

示例参数：

```json
{
  "src": "docs/draft.md",
  "dst": "docs/archive/draft.md",
  "overwrite": false
}
```

## 执行流程

`file_move` 的执行过程是：

1. 读取必填参数 `src` 并解析路径。
2. 读取必填参数 `dst` 并解析路径。
3. 读取 `overwrite`，没有提供时默认为 `false`。
4. 使用 `os.Stat(src)` 检查源路径。
5. 如果源路径不存在，返回错误。
6. 如果源路径和目标路径相同，返回错误。
7. 自动创建目标路径的父目录。
8. 检查目标路径是否已存在。
9. 如果目标已存在且 `overwrite=false`，返回错误。
10. 如果目标已存在且 `overwrite=true`，先递归删除目标路径。
11. 使用 `os.Rename(src, dst)` 执行移动。
12. 根据源路径类型返回 `file` 或 `directory` 移动成功信息。

成功输出类似：

```text
Moved file from /path/a.md to /path/b.md
```

或：

```text
Moved directory from /path/a to /path/b
```

## overwrite 行为

默认 `overwrite=false`，如果目标路径已存在，会返回：

```text
destination already exists: /path/to/dst
```

当 `overwrite=true` 时，如果目标路径已经存在，工具会先调用：

```go
removePath(dst, true)
```

这意味着：

- 目标是文件：删除目标文件。
- 目标是目录：递归删除目标目录树。
- 删除成功后，再执行移动。

这个行为风险较高。只有在用户明确要求替换目标路径，或已经确认目标内容可以丢弃时，才应使用 `overwrite=true`。

## 目标父目录

`file_move` 会自动创建目标路径的父目录：

```go
os.MkdirAll(filepath.Dir(dst), 0o755)
```

因此把文件移动到一个尚不存在的目录下通常不需要先调用 `file_mkdir`。

示例：

```json
{
  "src": "docs/report.md",
  "dst": "docs/archive/2026/report.md",
  "overwrite": false
}
```

如果 `docs/archive/2026` 不存在，工具会自动创建它。

## 同路径检查

工具会检查：

```go
filepath.Clean(src) == filepath.Clean(dst)
```

如果清理后的源路径和目标路径相同，会返回：

```text
source and destination are the same path: /path/to/file
```

这可以避免无意义移动。

## 路径解析

`file_move` 和其他文件工具共用路径解析逻辑：

- 支持 `~` 和 `~/...` 展开到当前用户 home。
- 相对路径优先相对 `_cwd` 解析。
- `_cwd` 本身必须通过 sandbox 校验才会被采用。
- 路径清理后如果包含 `..`，会被拒绝。
- `src` 和 `dst` 都必须通过 `validateSandbox`。

示例：

```json
{
  "src": "tmp/output.md",
  "dst": "docs/generated/output.md",
  "overwrite": false
}
```

如果当前 `_cwd` 是仓库根目录，两个相对路径都会基于仓库根目录解析，前提是 sandbox 允许这些路径。

## 访问限制

`file_move` 使用 `validateSandbox` 做路径限制。当前允许范围包括：

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

优先使用 `file_move` 的场景：

- 重命名一个文件。
- 重命名一个目录。
- 把临时产物移动到最终目录。
- 整理生成文件的位置。
- 在确认目标可替换后，用 `overwrite=true` 替换旧产物。

示例：

```json
{
  "src": "/tmp/luckyagent-output.md",
  "dst": "docs/generated/output.md",
  "overwrite": false
}
```

## 不适合使用的场景

不优先使用 `file_move` 的场景：

- 创建目录：使用 `file_mkdir`。
- 写入新文件：使用 `file_write`。
- 修改已有文件：使用 `file_patch`。
- 删除文件或目录：使用 `file_delete`。
- 复制文件：当前没有专用 `file_copy`，不要误用 move。
- 读取文件：使用 `file_read`。
- 只想确保目标目录存在：使用 `file_mkdir`，或让 `file_write` 自动创建父目录。

## 常见调用示例

重命名文件：

```json
{
  "src": "docs/old-name.md",
  "dst": "docs/new-name.md",
  "overwrite": false
}
```

移动文件到归档目录：

```json
{
  "src": "docs/draft.md",
  "dst": "docs/archive/draft.md",
  "overwrite": false
}
```

重命名目录：

```json
{
  "src": "docs/generated-old",
  "dst": "docs/generated",
  "overwrite": false
}
```

替换已有目标：

```json
{
  "src": "/tmp/new-report.md",
  "dst": "docs/report.md",
  "overwrite": true
}
```

使用 `overwrite=true` 前应确认 `docs/report.md` 可以被替换。

## 和 file_delete 的关系

`file_move` 在 `overwrite=true` 且目标存在时，会内部调用递归删除逻辑移除目标。

如果用户只是要求删除文件或目录，应使用 `file_delete`。

如果用户要求“把 A 移到 B，并替换 B”，才使用 `file_move` 的 `overwrite=true`。

## 和 terminal 的关系

不要优先用 `terminal` 手写：

```sh
mv src dst
```

正常移动文件或目录应使用 `file_move`，因为它有统一的路径解析、权限标记、sandbox 校验、目标存在处理和清晰的返回结果。

`terminal` 只应在需要运行项目脚本、构建命令、测试命令或外部 CLI 时使用。

## 风险和注意事项

`file_move` 的主要风险有两个：

- 源路径移动后，原位置不再存在。
- `overwrite=true` 会删除已有目标路径，包括目录树。

调用前应确认：

- 用户确实要求移动或重命名。
- `src` 是正确的源路径。
- `dst` 是正确的目标路径。
- 目标路径存在时是否允许覆盖。
- 如果覆盖目录，目录里的内容是否可以丢弃。

不确定时，应先用 `file_list` 或 `file_read` 检查路径状态。

## 维护注意事项

如果后续修改 `file_move`，需要同步检查：

- 参数说明是否仍与 `FileMoveTool()` 一致。
- `overwrite` 默认值是否仍是 `false`。
- 是否仍自动创建目标父目录。
- 目标父目录权限是否仍是 `0755`。
- 覆盖目标时是否仍递归删除目标路径。
- 同路径判断是否仍基于 `filepath.Clean`。
- 移动实现是否仍使用 `os.Rename`。
- 路径解析和 sandbox 规则是否变化。

