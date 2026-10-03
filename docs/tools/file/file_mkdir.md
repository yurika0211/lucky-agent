# file_mkdir Tool

`file_mkdir` 是 LuckyAgent 的内置目录创建工具，用来在本地文件系统中创建目录。它适合在写文件、导出产物、生成项目结构之前，先准备目标目录。

虽然创建目录通常比写文件风险低，但它仍然会改变磁盘状态，因此被标记为需要审批。

## 工具定义

实现位置：

- `internal/tool/builtin_fs.go`

注册信息：

```go
Name:       "file_mkdir"
Category:   CatBuiltin
Source:     "builtin"
Permission: PermApprove
ShellAware: true
```

含义：

- `CatBuiltin`：这是核心内置工具，不依赖 skill 或 MCP。
- `PermApprove`：创建目录会改变磁盘状态，默认需要审批。
- `ShellAware`：agent 可以向工具注入当前工作目录 `_cwd`，相对路径会基于该目录解析。

## 参数

`file_mkdir` 接收两个参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `path` | 是 | 无 | 要创建的目录路径。 |
| `recursive` | 否 | `true` | 是否自动创建父目录。 |

示例参数：

```json
{
  "path": "docs/tools/archive",
  "recursive": true
}
```

## 执行流程

`file_mkdir` 的执行过程是：

1. 读取必填参数 `path`，没有提供时返回 `path is required`。
2. 通过 `resolvePathArg(args, "path")` 解析目录路径。
3. 读取 `recursive`，没有提供时默认为 `true`。
4. 使用 `os.Stat` 检查目标路径。
5. 如果路径已存在且是目录，直接返回成功信息。
6. 如果路径已存在但不是目录，返回错误。
7. 如果路径不存在，根据 `recursive` 选择创建方式。
8. 创建成功后返回目标目录路径。

成功输出类似：

```text
Created directory /path/to/dir
```

如果目录已经存在，输出类似：

```text
Directory already exists: /path/to/dir
```

## recursive 行为

`recursive=true` 时，工具使用：

```go
os.MkdirAll(path, 0o755)
```

这会创建目标目录以及缺失的所有父目录。

`recursive=false` 时，工具使用：

```go
os.Mkdir(path, 0o755)
```

这只创建目标目录本身。如果父目录不存在，会返回创建错误。

默认值是 `true`，适合大多数“准备输出目录”的场景。

## 已存在路径处理

`file_mkdir` 对已存在路径的处理是：

- 路径存在且是目录：成功返回 `Directory already exists`。
- 路径存在但不是目录：失败返回 `path exists and is not a directory`。
- 路径不存在：按 `recursive` 创建。
- `os.Stat` 出现非“不存在”错误：返回 `stat path` 错误。

这让工具可以安全地用于“确保目录存在”的场景。

## 路径解析

`file_mkdir` 和其他文件工具共用路径解析逻辑：

- 支持 `~` 和 `~/...` 展开到当前用户 home。
- 相对路径优先相对 `_cwd` 解析。
- `_cwd` 本身必须通过 sandbox 校验才会被采用。
- 路径清理后如果包含 `..`，会被拒绝。
- 最终路径必须通过 `validateSandbox`。

示例：

```json
{
  "path": "docs/generated/reports",
  "recursive": true
}
```

如果当前 `_cwd` 是仓库根目录，这会解析到仓库下的 `docs/generated/reports`，前提是 sandbox 允许该路径。

## 访问限制

`file_mkdir` 使用 `validateSandbox` 做路径限制。当前允许范围包括：

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

优先使用 `file_mkdir` 的场景：

- 在写文件之前创建输出目录。
- 准备导出报告、图片、音频或日志的目录。
- 创建新功能或示例的目录结构。
- 确保某个目录存在，而不关心它之前是否已经存在。

示例：

```json
{
  "path": "docs/tools/generated",
  "recursive": true
}
```

## 不适合使用的场景

不优先使用 `file_mkdir` 的场景：

- 写入文件内容：使用 `file_write`。
- 修改已有文件：使用 `file_patch`。
- 移动或重命名目录：使用 `file_move`。
- 删除目录：使用 `file_delete`。
- 列出目录内容：使用 `file_list`。
- 读取文件：使用 `file_read`。

如果目标只是写入一个文件，`file_write` 本身会自动创建父目录，不一定需要提前调用 `file_mkdir`。

## 常见调用示例

创建单个目录：

```json
{
  "path": "docs/generated",
  "recursive": false
}
```

创建多级目录：

```json
{
  "path": "docs/generated/reports/2026",
  "recursive": true
}
```

创建临时目录：

```json
{
  "path": "/tmp/luckyagent-work",
  "recursive": true
}
```

确保 runtime workspace 子目录存在：

```json
{
  "path": "~/.luckyagent/workspace/exports",
  "recursive": true
}
```

## 和 file_write 的关系

`file_write` 会自动创建目标文件的父目录：

```go
os.MkdirAll(filepath.Dir(path), 0o755)
```

所以如果只是为了写一个文件，通常可以直接用 `file_write`。如果任务需要先建立目录结构，或者目录本身就是目标产物，再使用 `file_mkdir`。

## 和 terminal 的关系

不要优先用 `terminal` 手写：

```sh
mkdir -p docs/generated
```

正常创建目录应使用 `file_mkdir`，因为它有统一的路径解析、权限标记、sandbox 校验和清晰的返回结果。

`terminal` 只应在需要运行项目脚本、构建命令、测试命令或外部 CLI 时使用。

## 风险和注意事项

`file_mkdir` 不会删除或覆盖已有文件，但它会创建真实目录。

调用前应确认：

- 用户确实要求创建目录或需要该目录作为后续产物位置。
- 目标路径正确。
- `recursive=false` 时父目录已经存在。
- 目标路径没有指向已有普通文件。

如果不确定路径状态，可以先用 `file_list` 或 `file_read` 检查。

## 维护注意事项

如果后续修改 `file_mkdir`，需要同步检查：

- 参数说明是否仍与 `FileMkdirTool()` 一致。
- `recursive` 默认值是否仍是 `true`。
- 是否仍使用 `os.MkdirAll` / `os.Mkdir`。
- 自动创建目录权限是否仍是 `0755`。
- 已存在目录是否仍返回成功。
- 已存在普通文件是否仍返回错误。
- 路径解析和 sandbox 规则是否变化。

