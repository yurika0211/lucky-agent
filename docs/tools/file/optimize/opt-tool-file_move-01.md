# opt-tool-file_move-01

## 目标

优化 `file_move` 的覆盖安全、跨设备移动能力、执行前可解释性和测试覆盖，让它继续保持“移动或重命名本地路径、需要审批、路径受 sandbox 限制”的定位，同时降低误删目标、移动目录到自身内部、跨文件系统失败等风险。

本方案聚焦：

- `overwrite=true` 的删除风险控制
- dry-run / move plan
- 跨设备移动 fallback
- 目录移动边界检查
- symlink 和同路径判断
- 测试矩阵补齐

## 当前状态

相关实现：

- `internal/tool/builtin_fs.go`
- `internal/tool/builtin_test.go`
- `docs/tools/file_move.md`

当前 `file_move` 的流程：

1. 通过 `resolvePathArg(args, "src")` 解析源路径。
2. 通过 `resolvePathArg(args, "dst")` 解析目标路径。
3. 读取 `overwrite`，默认 `false`。
4. `os.Stat(src)` 检查源路径。
5. 用 `sameFilePath(src, dst)` 判断同路径。
6. 自动创建目标父目录。
7. 如果目标存在且 `overwrite=false`，返回错误。
8. 如果目标存在且 `overwrite=true`，调用 `removePath(dst, true)` 递归删除目标。
9. 使用 `os.Rename(src, dst)` 移动。
10. 根据源类型返回 `Moved file` 或 `Moved directory`。

当前优势：

- 语义比 `terminal mv` 窄。
- 统一路径解析和 sandbox 校验。
- 默认不覆盖目标。
- 自动创建目标父目录。
- 需要审批，符合真实磁盘变更行为。

## 主要问题

### 1. `overwrite=true` 风险偏高

当前 `overwrite=true` 会先递归删除目标路径：

```go
removePath(dst, true)
```

这意味着如果目标是目录，会删除整个目录树。虽然文档已经提示风险，但工具参数本身没有表达：

- 目标预计是什么类型。
- 是否允许删除目录树。
- 是否要求目标 hash 或内容状态匹配。
- 是否只是替换一个文件。

对 Agent 来说，这个行为需要更强的计划和保护。

### 2. 没有 dry-run

移动操作会改变两个位置：

- 源路径消失。
- 目标路径出现或被替换。

当前无法只预览：

- 源路径类型。
- 目标是否存在。
- 是否会创建父目录。
- 是否会删除目标。
- 是否会跨设备失败。

建议增加 `dry_run`，先返回 move plan。

### 3. 跨设备移动会失败

`os.Rename` 在不同挂载点或文件系统之间可能返回 `EXDEV`。

常见场景：

- 从 `/tmp` 移动到项目目录。
- 从外部挂载盘移动到 runtime home。
- 容器或 bind mount 环境。

当前会直接返回 `move path: invalid cross-device link` 一类错误。可以考虑可选 fallback：

- 文件：copy + fsync + remove source。
- 目录：递归 copy + remove source。

但 fallback 风险更高，需要显式参数控制。

### 4. 目录不能移动到自身内部

如果源是目录，目标在源目录内部，例如：

```text
src=/repo/docs
dst=/repo/docs/archive/docs
```

这类操作应该提前拒绝。当前实现只检查清理后路径是否完全相同，不能覆盖“目标在源内部”的情况。

### 5. 同路径判断不处理真实路径和 symlink

当前：

```go
filepath.Clean(src) == filepath.Clean(dst)
```

这能处理简单同路径，但不处理：

- symlink 指向同一文件。
- 大小写不敏感文件系统。
- 相对路径经过不同表示后指向同一 inode。

第一阶段不一定要做完整 inode 比较，但需要文档化当前边界，并考虑在源和目标都存在时使用 `os.SameFile`。

### 6. 测试和 mkdir/delete 混在一起

当前 `file_move` 的基础测试在 `TestFileMkdirMoveDeleteTools` 中。建议拆成独立测试，覆盖移动语义和高风险边界。

## 优化原则

1. 保持默认 `overwrite=false`。
2. 保持 `file_move` 不做复制，除非用户显式启用跨设备 fallback。
3. 覆盖目标时优先保护用户数据。
4. dry-run 必须不创建目录、不移动、不删除。
5. 对目录移动做更严格边界检查。
6. 保持 `PermApprove` 和 guard 约束。

## 推荐方案

### 1. 增加 move plan

新增内部结构：

```go
type fileMovePlan struct {
	Src               string
	Dst               string
	SrcKind           string
	DstExists         bool
	DstKind           string
	Overwrite         bool
	WouldDeleteDst    bool
	ParentDirsToCreate []string
	CrossDeviceFallback bool
}
```

新增函数：

```go
func planFileMove(src, dst string, overwrite bool) (fileMovePlan, error)
```

收益：

- Handler 更薄。
- dry-run 和实际执行共用判断。
- 测试可以直接覆盖计划逻辑。

### 2. 增加 dry-run

新增可选参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `dry_run` | 否 | `false` | 只预览移动计划，不创建目录、不移动、不删除。 |

输出示例：

```text
Would move file from /tmp/out.md to /repo/docs/out.md
Destination: absent
Parent directories to create: 1
- /repo/docs
```

覆盖目标时：

```text
Would move file from /tmp/out.md to /repo/docs/out.md
Destination: exists file, would replace because overwrite=true
```

dry-run 仍必须走路径解析和 sandbox 校验。

### 3. 增加覆盖目标类型保护

新增可选参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `expected_dst_kind` | 否 | 无 | 覆盖前要求目标类型匹配：`file` 或 `directory`。 |

行为：

- `overwrite=true` 且目标存在时，如果目标类型和 `expected_dst_kind` 不一致，拒绝移动。
- 如果没有提供，保留旧行为，但在 dry-run 中明确提示会删除目标类型。

示例错误：

```text
destination kind mismatch: expected file, got directory
```

这可以避免本来想替换文件，却误删目录树。

### 4. 拒绝目录移入自身内部

新增检查：

```go
func destinationInsideSource(src, dst string) bool
```

仅当源是目录时启用。

语义：

- `dst == src`：同路径错误。
- `dst` 在 `src` 内部：返回错误。

错误示例：

```text
cannot move directory into itself: /repo/docs -> /repo/docs/archive/docs
```

实现注意：

- 使用 cleaned absolute path。
- 可用 `filepath.Rel(src, dst)` 判断是否不以 `..` 开头。
- 注意 `Rel` 返回 `.` 表示同路径。

### 5. 改善同路径判断

保留 `filepath.Clean` 判断，同时在源和目标都存在时增加：

```go
os.SameFile(srcInfo, dstInfo)
```

这样可以识别不同路径表示但同一文件的情况。

第一阶段不用强行解析所有 symlink 细节，但文档中需要说明：

- 当前 `os.Stat` 会跟随 symlink。
- 移动 symlink 路径时，行为取决于 Go/OS 的 rename 语义和路径本身。

### 6. 跨设备 fallback 显式化

新增可选参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `allow_copy_fallback` | 否 | `false` | `os.Rename` 跨设备失败时，允许 copy + delete 源路径。 |

默认保持 `false`，避免意外把 move 变成复制删除。

fallback 行为：

- 文件：复制到目标临时文件，fsync，rename，删除源。
- 目录：递归复制目录树，再删除源。
- 复制失败：不删除源。
- 删除源失败：返回部分成功错误，明确目标已复制但源未删除。

建议第一版只支持文件 fallback，目录 fallback 复杂度更高，可后续再做。

### 7. 审计输出增强

实际移动后返回更多上下文：

```text
Moved file from /tmp/out.md to /repo/docs/out.md
Created parent directories: 1
Replaced destination: false
```

为了兼容，第一版可只在 `verbose=true` 时输出。

## 分阶段实施

### Phase 1：测试拆分和边界检查

改动范围：

- `internal/tool/builtin_fs.go`
- `internal/tool/builtin_test.go`
- `docs/tools/file_move.md`

内容：

- 拆出 `file_move` 独立测试。
- 增加目录移入自身内部检查。
- 增加目标是同一文件时的 `os.SameFile` 检查。
- 改善错误提示。

验收标准：

- `go test ./internal/tool` 通过。
- 同路径移动仍被拒绝。
- 目录移动到自身内部被拒绝。
- 普通文件移动行为不变。

### Phase 2：dry-run 和 move plan

内容：

- 新增 `fileMovePlan`。
- 新增 `dry_run` 参数。
- dry-run 输出源类型、目标状态、父目录计划、覆盖风险。

验收标准：

- dry-run 不创建父目录。
- dry-run 不移动源。
- dry-run 不删除目标。
- 目标存在时清楚说明是否会覆盖。

### Phase 3：覆盖保护

内容：

- 新增 `expected_dst_kind`。
- `overwrite=true` 时可校验目标类型。
- overwrite 删除目标前统一走 move plan。

验收标准：

- 预期 file 但目标是 directory 时拒绝。
- 预期 directory 但目标是 file 时拒绝。
- 未提供时兼容旧行为。
- 覆盖文件和覆盖目录都有测试。

### Phase 4：跨设备 fallback

内容：

- 新增 `allow_copy_fallback`。
- 先实现文件 fallback。
- 目录 fallback 后续评估。

验收标准：

- 默认跨设备失败仍返回错误。
- 开启 fallback 后文件可 copy + delete。
- copy 失败不删除源。
- delete 源失败时返回清晰的部分成功错误。

### Phase 5：审计输出和文档更新

内容：

- 可选新增 `verbose` 参数。
- 输出创建父目录数量、是否替换目标、是否使用 fallback。
- 更新文档中的风险说明。

验收标准：

- 默认输出兼容。
- `verbose=true` 输出额外审计信息。
- 文档与实际参数一致。

## 测试建议

新增或补充测试：

### 基础行为

- 移动文件。
- 移动目录。
- 自动创建目标父目录。
- 源路径不存在时报错。
- 目标已存在且 `overwrite=false` 报错。
- 目标已存在且 `overwrite=true` 替换文件。
- 目标已存在且 `overwrite=true` 替换目录。

### dry-run

- dry-run 不移动文件。
- dry-run 不创建父目录。
- dry-run 目标存在时显示 would replace。
- dry-run 源不存在时仍报错。

### 路径边界

- src 和 dst 清理后相同时报错。
- src 和 dst 指向同一现有文件时报错。
- 目录移动到自身内部时报错。
- 相对路径基于 `_cwd`。
- `..` 路径被拒绝。
- sandbox 拒绝路径时报错。

### 覆盖保护

- `expected_dst_kind=file` 但目标是目录时报错。
- `expected_dst_kind=directory` 但目标是文件时报错。
- 非法 expected kind 报错。

### 跨设备 fallback

- `allow_copy_fallback=false` 时 EXDEV 返回错误。
- `allow_copy_fallback=true` 时文件 fallback 成功。
- copy 失败不删除源。
- delete 源失败报告部分成功。

### 权限和 guard

- `file_move` 仍是 `PermApprove`。
- 用户要求只读或不要修改文件时，`tool_execution_guard` 阻止 `file_move`。
- 用户要求不要删除时，`overwrite=true` 的 move 应被 guard 或工具保护策略拦截。

## 文档更新

完成实现后，同步更新：

- `docs/tools/file_move.md`

如果加入新参数，参数表增加：

```text
dry_run | 否 | false | 只预览移动计划，不创建目录、不移动、不删除。
expected_dst_kind | 否 | 无 | overwrite=true 时要求目标类型匹配 file 或 directory。
allow_copy_fallback | 否 | false | 跨设备 rename 失败时允许 copy + delete 源路径。
```

并补充：

```text
overwrite=true 可能删除已有目标，尤其当目标是目录时会删除目录树。谨慎场景下建议先 dry_run，并在覆盖时指定 expected_dst_kind。
```

## 风险与边界

1. dry-run 存在 TOCTOU 问题。
   预览和实际移动之间，源或目标状态可能变化。

2. copy fallback 会改变 move 的故障模型。
   rename 是单步操作，而 copy + delete 可能产生部分成功状态，因此必须显式启用。

3. 目录 fallback 复杂度高。
   目录树复制涉及权限、symlink、特殊文件、部分失败回滚，第一版不建议默认支持。

4. symlink 行为跨平台复杂。
   第一版建议文档化当前 `os.Stat` 和 `os.Rename` 语义，只在源和目标都存在时用 `os.SameFile` 增强同路径判断。

5. overwrite 删除目标不能替代 `file_delete`。
   它只应作为“移动并替换目标”的一部分，而不是普通删除入口。

## 推荐结论

优先实现 Phase 1、Phase 2 和 Phase 3。

Phase 1 能立刻堵住目录移动到自身内部和同文件判断不足的问题。Phase 2 的 dry-run 让 Agent 可以先解释移动影响。Phase 3 给 `overwrite=true` 增加目标类型保护，是降低误删目录树风险的关键。Phase 4 的跨设备 fallback 很有用，但要谨慎显式开启，适合作为第二批推进。
