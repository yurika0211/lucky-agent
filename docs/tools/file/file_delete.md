# file_delete Tool

`file_delete` 是 LuckyAgent 的内置文件系统删除工具，用来删除本地文件或目录。它适合在用户明确要求清理产物、移除临时文件、删除废弃目录时使用。

删除是不可逆的磁盘操作，尤其是 `recursive=true` 会删除整个目录树。因此 `file_delete` 被标记为需要审批。

## 工具定义

实现位置：

- `internal/tool/builtin_fs.go`

注册信息：

```go
Name:       "file_delete"
Category:   CatBuiltin
Source:     "builtin"
Permission: PermApprove
ShellAware: true
```

含义：

- `CatBuiltin`：这是核心内置工具，不依赖 skill 或 MCP。
- `PermApprove`：删除文件或目录会改变磁盘状态，默认需要审批。
- `ShellAware`：agent 可以向工具注入当前工作目录 `_cwd`，相对路径会基于该目录解析。

## 参数

`file_delete` 接收三个参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `path` | 是 | 无 | 要删除的文件或目录路径。 |
| `recursive` | 否 | `false` | 是否递归删除目录树。 |
| `missing_ok` | 否 | `false` | 目标不存在时是否仍视为成功。 |

示例参数：

```json
{
  "path": "docs/generated/old-report.md",
  "recursive": false,
  "missing_ok": false
}
```

## 执行流程

`file_delete` 的执行过程是：

1. 读取必填参数 `path`，没有提供时返回 `path is required`。
2. 通过 `resolvePathArg(args, "path")` 解析目标路径。
3. 读取 `recursive`，没有提供时默认为 `false`。
4. 读取 `missing_ok`，没有提供时默认为 `false`。
5. 调用 `removePath(path, recursive)` 删除目标。
6. 如果目标不存在且 `missing_ok=true`，返回成功信息。
7. 如果删除失败，返回对应错误。
8. 删除成功后返回目标路径。

成功输出类似：

```text
Deleted /path/to/file
```

如果目标不存在且 `missing_ok=true`，输出类似：

```text
Path already absent: /path/to/file
```

## recursive 行为

当目标是普通文件时，`recursive` 不影响行为，工具会调用：

```go
os.Remove(path)
```

当目标是目录时：

- `recursive=false`：调用 `os.Remove(path)`，只能删除空目录。
- `recursive=true`：调用 `os.RemoveAll(path)`，删除整个目录树。

默认值是 `false`。这是重要的安全边界，避免默认删除非空目录。

## missing_ok 行为

默认 `missing_ok=false`。如果目标路径不存在，会返回错误。

当 `missing_ok=true` 时，如果目标路径不存在，工具会返回成功信息：

```text
Path already absent: /path/to/file
```

这个参数适合“确保某个临时文件不存在”的幂等清理场景。

## 路径解析

`file_delete` 和其他文件工具共用路径解析逻辑：

- 支持 `~` 和 `~/...` 展开到当前用户 home。
- 相对路径优先相对 `_cwd` 解析。
- `_cwd` 本身必须通过 sandbox 校验才会被采用。
- 路径清理后如果包含 `..`，会被拒绝。
- 最终路径必须通过 `validateSandbox`。

示例：

```json
{
  "path": "docs/generated/old",
  "recursive": true,
  "missing_ok": false
}
```

如果当前 `_cwd` 是仓库根目录，这会解析到仓库下的 `docs/generated/old`，前提是 sandbox 允许该路径。

## 访问限制

`file_delete` 使用 `validateSandbox` 做路径限制。当前允许范围包括：

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

## Agent 层执行保护

agent 的 `tool_execution_guard` 会根据用户当前请求中的限制拦截删除行为。

如果用户说了：

- `不要删`
- `不要删除`
- `不要直接删`
- `不要移除`
- `只读`
- `只查看`

那么 `file_delete` 调用会被阻止。

这层保护用于尊重用户当前意图，但不替代审批。删除前仍应确认用户明确要求了删除。

## 适合使用的场景

优先使用 `file_delete` 的场景：

- 删除明确指定的临时文件。
- 删除确认不再需要的生成产物。
- 清理空目录。
- 在用户明确授权后递归删除某个生成目录。
- 幂等清理：目标不存在也算成功时使用 `missing_ok=true`。

示例：

```json
{
  "path": "/tmp/luckyagent-output.txt",
  "recursive": false,
  "missing_ok": true
}
```

## 不适合使用的场景

不优先使用 `file_delete` 的场景：

- 用户只是要求“整理”或“移动”：使用 `file_move`。
- 用户只是要求“覆盖旧文件”：使用 `file_write` 或 `file_move` 的明确覆盖语义。
- 需要清空文件内容但保留文件：使用 `file_write` 写入空内容或目标内容。
- 目标路径不确定：先用 `file_list` 或 `file_read` 检查。
- 用户明确说不要删除或只读查看。

## 常见调用示例

删除单个文件：

```json
{
  "path": "docs/generated/old-report.md",
  "recursive": false,
  "missing_ok": false
}
```

删除空目录：

```json
{
  "path": "docs/generated/empty-dir",
  "recursive": false,
  "missing_ok": false
}
```

递归删除目录树：

```json
{
  "path": "docs/generated/old-build",
  "recursive": true,
  "missing_ok": false
}
```

幂等删除临时文件：

```json
{
  "path": "/tmp/luckyagent-output.txt",
  "recursive": false,
  "missing_ok": true
}
```

## 和 file_move 的关系

如果目标是“删除”，使用 `file_delete`。

如果目标是“移动或重命名”，使用 `file_move`。

注意：`file_move` 在 `overwrite=true` 且目标存在时，会内部删除目标路径。那是“替换目标”的一部分，不应替代明确删除操作。

## 和 terminal 的关系

不要优先用 `terminal` 手写：

```sh
rm file
```

也不要优先用：

```sh
rm -rf dir
```

正常删除文件或目录应使用 `file_delete`，因为它有统一的路径解析、权限标记、sandbox 校验、`recursive` / `missing_ok` 参数和清晰的返回结果。

`terminal` 只应在需要运行项目脚本、构建命令、测试命令或外部 CLI 时使用。

## 风险和注意事项

`file_delete` 是破坏性工具。

调用前应确认：

- 用户明确要求删除。
- 目标路径完全正确。
- 删除文件还是目录已经确认。
- 删除目录树时，`recursive=true` 是用户明确允许的。
- 不存在路径是否应视为成功，已正确设置 `missing_ok`。
- 没有违反用户的“只读”“不要删除”等限制。

不确定时，先用 `file_list`、`file_read` 或 `db_schema` 等只读工具确认目标内容。

## 维护注意事项

如果后续修改 `file_delete`，需要同步检查：

- 参数说明是否仍与 `FileDeleteTool()` 一致。
- `recursive` 默认值是否仍是 `false`。
- `missing_ok` 默认值是否仍是 `false`。
- 目录删除是否仍使用 `os.Remove` / `os.RemoveAll`。
- 文件删除是否仍使用 `os.Remove`。
- 不存在路径的返回语义是否变化。
- 路径解析和 sandbox 规则是否变化。
- agent 层 `tool_execution_guard` 对删除意图的拦截规则是否变化。

