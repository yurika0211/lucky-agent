# opt-tool-file_delete-01

## 目标

优化 `file_delete` 的删除前可见性、递归删除保护、恢复能力和测试覆盖，让它继续保持“明确删除本地路径、需要审批、路径受 sandbox 限制”的定位，同时降低误删文件、误删目录树和 symlink 边界不清的风险。

本方案聚焦：

- dry-run / delete plan
- 递归删除目录树的规模预览和限制
- 更安全的 trash/quarantine 删除模式
- symlink 和特殊路径行为
- `missing_ok` 幂等语义
- 与 `file_move overwrite=true` 和 guard 的边界

## 当前状态

相关实现：

- `internal/tool/builtin_fs.go`
- `internal/tool/builtin_test.go`
- `docs/tools/file_delete.md`

当前 `file_delete` 的流程：

1. 通过 `resolvePathArg(args, "path")` 解析目标路径。
2. 读取 `recursive`，默认 `false`。
3. 读取 `missing_ok`，默认 `false`。
4. 调用 `removePath(path, recursive)`。
5. 如果目标不存在且 `missing_ok=true`，返回 `Path already absent`。
6. 删除成功后返回 `Deleted <path>`。

当前 `removePath` 行为：

- 目标不存在：返回 `os.ErrNotExist`。
- 目标是目录且 `recursive=false`：调用 `os.Remove`，只能删除空目录。
- 目标是目录且 `recursive=true`：调用 `os.RemoveAll`，删除目录树。
- 目标是文件：调用 `os.Remove`。

当前优势：

- 默认不递归删除目录树。
- 支持 `missing_ok`，适合幂等清理。
- 路径解析和 sandbox 校验统一。
- 权限是 `PermApprove`。
- Agent 层 guard 会拦截用户明确禁止删除的场景。

## 主要问题

### 1. 递归删除前不可见

`recursive=true` 时直接调用：

```go
os.RemoveAll(path)
```

调用方无法在执行前看到：

- 将删除多少文件。
- 将删除多少目录。
- 总大小大概是多少。
- 是否包含隐藏文件。
- 是否包含 symlink。

这对破坏性操作来说可审计性不足。

### 2. 缺少 dry-run

当前如果只想确认“会删什么”，只能先用 `file_list`。但 `file_list` 不一定提供删除计划所需信息，例如总文件数、总大小、目标类型和递归风险。

建议 `file_delete` 自己支持：

```json
{
  "dry_run": true
}
```

不删除，只输出 delete plan。

### 3. 缺少目录规模限制

递归删除目录树没有数量或大小限制。即使 sandbox 限制了路径范围，仍可能删除大量生成产物、缓存、索引或用户工作区内容。

建议增加软限制：

- 最大文件数
- 最大目录数
- 最大总字节数

超过限制时拒绝，除非显式提供更强确认参数。

### 4. 没有 trash/quarantine 模式

当前删除是直接删除，恢复依赖文件系统或备份。对高风险操作，更稳妥的方式是先移动到 runtime trash/quarantine 目录：

```text
~/.luckyagent/trash/<timestamp>-<basename>
```

这样可以降低误删后的恢复成本。

### 5. symlink 行为没有明确

当前使用 `os.Stat`，会跟随 symlink。随后：

- 如果 symlink 指向目录，`info.IsDir()` 为 true。
- `os.RemoveAll(path)` 对 symlink 路径的实际行为需要明确测试和文档化。

删除工具必须清楚说明 symlink 行为，避免用户误以为会删除 link 本身或 link 指向内容。

建议使用 `os.Lstat` 做目标类型识别，并明确：

- 默认删除 symlink 本身。
- 不跟随 symlink 删除其指向内容。

这比 `os.Stat` 更符合删除工具的安全直觉。

### 6. 测试覆盖和 move/mkdir 混在一起

当前 `file_delete` 测试与 mkdir/move 混在 `TestFileMkdirMoveDeleteTools` 中。应拆出独立测试，覆盖破坏性边界。

## 优化原则

1. 保持默认 `recursive=false`。
2. 删除目录树前优先提供可见计划。
3. 对 symlink 使用更安全、可解释的策略。
4. 高风险递归删除增加显式确认或限制。
5. 不把 `file_delete` 当作 `file_move overwrite=true` 的替代入口。
6. 保持 `PermApprove` 和 `tool_execution_guard` 约束。

## 推荐方案

### 1. 增加 delete plan

新增内部结构：

```go
type fileDeletePlan struct {
	Path       string
	Exists     bool
	Kind       string
	Recursive  bool
	MissingOK  bool
	Files      int
	Dirs       int
	Bytes      int64
	Symlinks   int
	Truncated  bool
}
```

新增函数：

```go
func planFileDelete(path string, recursive bool, missingOK bool) (fileDeletePlan, error)
```

行为：

- 文件：统计 1 个文件和大小。
- 空目录且 `recursive=false`：允许删除。
- 非空目录且 `recursive=false`：计划阶段返回明确错误。
- 目录且 `recursive=true`：遍历目录树，统计规模。
- 不存在且 `missing_ok=true`：返回 `Exists=false` 的成功 plan。
- 不存在且 `missing_ok=false`：返回错误。

### 2. 增加 dry-run

新增参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `dry_run` | 否 | `false` | 只预览删除计划，不实际删除。 |

输出示例：

```text
Would delete directory tree /repo/docs/generated-old
Files: 42
Directories: 6
Bytes: 184320
Symlinks: 0
```

文件示例：

```text
Would delete file /tmp/output.txt
Bytes: 1284
```

dry-run 必须不删除任何路径。

### 3. 增加递归删除限制

新增常量或配置项：

```go
const (
	defaultDeleteMaxFiles = 1000
	defaultDeleteMaxBytes = 100 * 1024 * 1024
)
```

行为：

- 递归删除计划超过限制时拒绝。
- 错误提示建议用户先检查目录内容或显式提高限制。

可选参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `max_files` | 否 | `1000` | 本次允许删除的最大文件数。 |
| `max_bytes` | 否 | `104857600` | 本次允许删除的最大总字节数。 |

第一版也可以只内置限制，不暴露参数，避免模型随意提高限制。

### 4. 增加 require_empty 或 confirm_recursive

为了避免误删目录树，可以增加更明确的递归确认语义：

```json
{
  "recursive": true,
  "confirm_recursive": true
}
```

如果 `recursive=true` 但 `confirm_recursive` 未设置，则仍可保持旧行为以兼容；更安全的迁移方式是先只在文档推荐使用，后续版本再强制。

也可以新增：

```json
{
  "require_empty": true
}
```

语义：

- 只允许删除空目录。
- 即使 `recursive=true`，发现非空目录也拒绝。

### 5. 增加 trash/quarantine 模式

新增可选参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `trash` | 否 | `false` | 将目标移动到 LuckyAgent trash，而不是直接删除。 |

建议路径：

```text
~/.luckyagent/trash/YYYYMMDD-HHMMSS-<basename>
```

行为：

- 使用 `file_move` 类似的 rename 逻辑移动到 trash。
- 如果跨设备失败，可返回错误或显式支持 copy fallback。
- 返回 trash 路径，方便恢复。

输出：

```text
Moved to trash: /home/user/.luckyagent/trash/20260702-120301-old-report.md
```

第一版可只设计，不急于实现。实现时需要考虑 trash 清理策略。

### 6. 使用 Lstat 明确 symlink 行为

建议将删除类型判断从 `os.Stat` 改为 `os.Lstat`。

策略：

- symlink：删除 symlink 本身。
- symlink 指向目录：不递归删除目标目录。
- broken symlink：允许删除 symlink 本身。

这比跟随 symlink 更安全。

文档需要明确说明：

```text
file_delete 删除 symlink 本身，不跟随 symlink 删除其指向内容。
```

### 7. 审计输出增强

实际删除后可返回：

```text
Deleted directory tree /path/to/dir
Files deleted: 42
Directories deleted: 6
Bytes estimated: 184320
```

为了兼容，第一版可以只在 `verbose=true` 时输出。

## 分阶段实施

### Phase 1：测试拆分和 symlink 策略确认

改动范围：

- `internal/tool/builtin_fs.go`
- `internal/tool/builtin_test.go`
- `docs/tools/file_delete.md`

内容：

- 拆出 `file_delete` 独立测试。
- 明确并测试 symlink 行为。
- 对非空目录且 `recursive=false` 的错误提示更清晰。
- 保持现有参数兼容。

验收标准：

- `go test ./internal/tool` 通过。
- 删除文件、空目录、非空目录、missing_ok 行为稳定。
- symlink 删除行为有测试和文档。

### Phase 2：dry-run 和 delete plan

内容：

- 新增 `fileDeletePlan`。
- 新增 `dry_run` 参数。
- 递归目录 dry-run 统计文件数、目录数、字节数。

验收标准：

- dry-run 不删除目标。
- 文件 dry-run 输出类型和大小。
- 目录 dry-run 输出统计信息。
- missing_ok dry-run 行为明确。

### Phase 3：递归删除限制

内容：

- 增加默认递归删除规模限制。
- 超出限制时拒绝。
- 可选支持 `max_files` / `max_bytes`。

验收标准：

- 小目录递归删除成功。
- 超过文件数限制时拒绝且不删除。
- 超过大小限制时拒绝且不删除。

### Phase 4：trash 模式

内容：

- 新增 `trash` 参数。
- 将目标移动到 `~/.luckyagent/trash`。
- 返回 trash 路径。

验收标准：

- `trash=true` 不直接删除，目标出现在 trash。
- 源路径消失。
- trash 目录自动创建。
- sandbox 与 runtime home 规则一致。

### Phase 5：审计输出和文档更新

内容：

- 可选新增 `verbose` 参数。
- 实际删除输出统计信息。
- 更新文档风险说明和示例。

验收标准：

- 默认输出兼容。
- `verbose=true` 输出删除统计。
- 文档和实现一致。

## 测试建议

新增或补充测试：

### 基础行为

- 删除文件。
- 删除空目录。
- 非空目录 `recursive=false` 报错。
- 非空目录 `recursive=true` 删除成功。
- 目标不存在且 `missing_ok=false` 报错。
- 目标不存在且 `missing_ok=true` 成功。

### dry-run

- dry-run 文件不删除。
- dry-run 空目录不删除。
- dry-run 非空目录统计文件数、目录数、大小。
- dry-run missing_ok 对不存在路径返回 already absent 计划。

### 递归限制

- 文件数超过限制时拒绝。
- 字节数超过限制时拒绝。
- 拒绝后目录仍存在。

### symlink

- 删除 symlink 文件本身。
- symlink 指向目录时不删除目标目录。
- broken symlink 可删除。

### trash

- `trash=true` 移动到 trash。
- trash 目标重名时生成唯一名称。
- trash 失败时源路径仍保留。

### 路径和权限

- 相对路径基于 `_cwd`。
- `..` 路径被拒绝。
- sandbox 拒绝敏感路径。
- `file_delete` 仍是 `PermApprove`。
- 用户要求只读或不要删除时，guard 阻止 `file_delete`。

## 文档更新

完成实现后，同步更新：

- `docs/tools/file_delete.md`

如果加入新参数，参数表增加：

```text
dry_run | 否 | false | 只预览删除计划，不实际删除。
trash | 否 | false | 将目标移动到 LuckyAgent trash，而不是直接删除。
max_files | 否 | 1000 | 递归删除允许的最大文件数。
max_bytes | 否 | 104857600 | 递归删除允许的最大总字节数。
```

补充 symlink 行为：

```text
file_delete 删除 symlink 本身，不跟随 symlink 删除其指向内容。
```

## 风险与边界

1. dry-run 存在 TOCTOU 问题。
   预览和实际删除之间，目录内容可能变化。

2. 删除规模统计可能有成本。
   大目录遍历会增加延迟，需要设置遍历上限和超时策略。

3. trash 不是完整备份系统。
   它只能降低误删恢复成本，不保证长期保存，也不适合敏感数据。

4. symlink 行为必须固定。
   删除工具如果跟随 symlink，会增加误删外部路径风险。建议明确采用 Lstat 策略。

5. `file_move overwrite=true` 仍会内部删除目标。
   需要确保 move 的覆盖删除策略与 file_delete 的安全原则一致。

## 推荐结论

优先实现 Phase 1、Phase 2 和 Phase 3。

Phase 1 先把现有删除行为测试拆清楚，并固定 symlink 策略。Phase 2 的 dry-run 和 delete plan 是删除工具最关键的可见性增强。Phase 3 给递归删除加规模限制，能显著降低误删大目录的风险。Phase 4 的 trash 模式很有价值，但需要配套清理策略，建议作为第二批推进。
