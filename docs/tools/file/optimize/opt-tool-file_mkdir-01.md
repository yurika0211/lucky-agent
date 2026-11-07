# opt-tool-file_mkdir-01

## 目标

优化 `file_mkdir` 的执行反馈、参数边界、安全语义和测试覆盖，让它继续保持“专门创建目录、需要审批、路径受 sandbox 限制”的定位，同时更适合 Agent 做可审计的文件系统准备工作。

本方案聚焦：

- 创建结果更结构化
- 递归创建行为更可解释
- 参数和路径意图更清晰
- 与 `file_write`、`file_move`、`terminal mkdir` 的职责边界
- 测试覆盖补齐

## 当前状态

相关实现：

- `internal/tool/builtin_fs.go`
- `internal/tool/builtin_test.go`
- `docs/tools/file_mkdir.md`

当前 `file_mkdir` 的流程：

1. 通过 `resolvePathArg(args, "path")` 解析路径。
2. 读取 `recursive`，默认 `true`。
3. `os.Stat(path)` 检查目标路径。
4. 如果已存在且是目录，返回 `Directory already exists`。
5. 如果已存在但不是目录，返回错误。
6. 如果不存在，根据 `recursive` 调用：
   - `os.MkdirAll(path, 0o755)`
   - `os.Mkdir(path, 0o755)`
7. 成功后返回 `Created directory <path>`。

当前优势：

- 语义比 `terminal mkdir -p` 窄。
- 统一路径解析和 sandbox 校验。
- 已存在目录视为成功，适合“确保目录存在”。
- 权限是 `PermApprove`，符合真实写磁盘行为。

## 主要问题

### 1. 递归创建的实际变更不可见

`recursive=true` 时，`os.MkdirAll` 可能创建多级目录，但当前只返回目标目录：

```text
Created directory /path/to/a/b/c
```

调用方无法知道：

- 是只创建了最后一级。
- 还是创建了整条父目录链。
- 还是并发场景下别的进程已经创建了目录。

这对审计和向用户解释“我创建了哪些东西”不够友好。

### 2. 缺少 dry-run 或 plan 模式

目录创建虽然风险低于写文件，但仍是磁盘变更。某些任务中，Agent 可能只需要确认将要创建哪些目录。

可以考虑增加：

```json
{
  "dry_run": true
}
```

返回将创建的目录清单，不实际执行。

### 3. `recursive=false` 的错误提示可以更清楚

当父目录不存在时，`os.Mkdir` 会返回系统错误。当前包装为：

```text
create directory: no such file or directory
```

建议明确提示：

```text
parent directory does not exist; set recursive=true or create the parent first
```

这样更利于 Agent 自我修正。

### 4. 路径目标类型校验可以更细

当前已处理：

- 已存在目录：成功
- 已存在普通文件：错误

但还可以明确处理：

- symlink 指向目录
- symlink 指向文件
- broken symlink

Go 的 `os.Stat` 会跟随 symlink，当前行为可能是可接受的，但文档没有说明。建议明确策略：

- 默认跟随 symlink。
- 如果 symlink 最终是目录，视为目录存在。
- 如果 symlink 最终不是目录，返回错误。
- broken symlink 返回 stat 错误。

如需更严格行为，后续再加 `follow_symlink`，第一版不建议增加。

### 5. 测试覆盖和现有测试耦合

当前 `file_mkdir` 测试在 `TestFileMkdirMoveDeleteTools` 中和 move/delete 混在一起。

建议拆成独立测试：

- 默认 recursive 创建多级目录。
- `recursive=false` 父目录不存在时报错。
- 已存在目录幂等成功。
- 已存在普通文件报错。
- sandbox 拒绝路径报错。

这样后续修改 mkdir 行为时更容易定位失败。

## 优化原则

1. 保持 `file_mkdir` 只负责目录创建，不写文件、不删除、不移动。
2. 保持默认 `recursive=true`，兼容现有“确保目录存在”的常见用法。
3. 保持 `PermApprove`，因为创建目录会改变磁盘状态。
4. 优先改善反馈和测试，不引入复杂权限模型。
5. 不默认暴露底层系统差异，除非错误处理需要。

## 推荐方案

### 1. 抽出 mkdir 参数解析

新增：

```go
type fileMkdirOptions struct {
	Path      string
	Recursive bool
	DryRun    bool
}

func parseFileMkdirOptions(args map[string]any) (fileMkdirOptions, error)
```

第一阶段可以只解析现有参数：

- `path`
- `recursive`

如果实现 `dry_run`，再加入该参数。

收益：

- Handler 更薄。
- 参数默认值集中。
- 方便测试 bool 参数解析。

### 2. 增加创建计划计算

新增内部函数：

```go
func planMkdir(path string, recursive bool) (mkdirPlan, error)
```

结构示例：

```go
type mkdirPlan struct {
	Target        string
	AlreadyExists bool
	ToCreate      []string
	Recursive     bool
}
```

行为：

- 目标已存在且是目录：`AlreadyExists=true`，`ToCreate=[]`。
- 目标已存在但不是目录：错误。
- `recursive=false`：只检查父目录是否存在，`ToCreate=[target]`。
- `recursive=true`：从最近存在的父目录往下计算缺失目录列表。

示例输出可保守保持兼容：

```text
Created directory /path/to/a/b/c
```

但可以追加审计信息：

```text
Created directory /path/to/a/b/c
Created 3 directories:
- /path/to/a
- /path/to/a/b
- /path/to/a/b/c
```

如果担心破坏调用方，第一版可以只在 `dry_run` 中展示清单，实际执行先保持原输出。

### 3. 增加 dry-run 模式

新增可选参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `dry_run` | 否 | `false` | 只返回将创建的目录，不实际创建。 |

示例：

```json
{
  "path": "docs/generated/reports",
  "recursive": true,
  "dry_run": true
}
```

输出：

```text
Would create directory /path/to/docs/generated/reports
Would create 2 directories:
- /path/to/docs/generated
- /path/to/docs/generated/reports
```

注意：`dry_run` 仍然应该走 `resolvePathArg` 和 `validateSandbox`，确保计划路径本身合法。

### 4. 改善错误提示

对常见错误做更具体提示：

- 父目录不存在且 `recursive=false`
- 目标是普通文件
- 目标路径权限不足
- sandbox 拒绝

示例：

```text
parent directory does not exist: /path/to/parent; set recursive=true or create it first
```

```text
path exists and is not a directory: /path/to/file
```

不要吞掉底层错误，可以用 `%w` 保留包装，方便测试和调试。

### 5. 保持幂等和并发友好

并发场景下可能出现：

1. `os.Stat` 发现不存在。
2. 另一个进程创建了目录。
3. 当前调用 `os.MkdirAll` 或 `os.Mkdir`。

`os.MkdirAll` 通常能自然处理。`recursive=false` 的 `os.Mkdir` 可能返回已存在错误。

建议创建后如果遇到 `os.ErrExist`，再 `os.Stat` 一次：

- 如果最终是目录，返回 `Directory already exists` 或 `Directory created by another process`。
- 如果不是目录，返回冲突错误。

这样工具更幂等。

## 分阶段实施

### Phase 1：测试拆分和错误提示

改动范围：

- `internal/tool/builtin_fs.go`
- `internal/tool/builtin_test.go`
- `docs/tools/file_mkdir.md`

内容：

- 拆出 `file_mkdir` 独立测试。
- 对 `recursive=false` 父目录不存在给出明确错误。
- 保持现有参数和输出基本不变。

验收标准：

- `go test ./internal/tool` 通过。
- 已存在目录仍成功。
- 已存在普通文件仍报错。
- `recursive=false` 父目录不存在时错误可读。

### Phase 2：创建计划和 dry-run

内容：

- 新增 `planMkdir`。
- 新增 `dry_run` 参数。
- dry-run 返回将创建的目录清单。

验收标准：

- `dry_run=true` 不创建任何目录。
- `dry_run=false` 行为与旧版兼容。
- `recursive=true` 能列出多级缺失目录。
- `recursive=false` 只计划目标目录。

### Phase 3：审计输出增强

内容：

- 实际创建后可选返回创建数量和目录清单。
- 如果担心输出兼容，可新增 `verbose` 参数控制。

建议参数：

```json
{
  "verbose": true
}
```

验收标准：

- 默认输出仍简洁。
- `verbose=true` 输出 created count 和目录列表。
- 已存在目录时输出不会误称创建成功。

### Phase 4：symlink 行为文档化

内容：

- 明确 `os.Stat` 会跟随 symlink。
- 文档中说明 symlink 指向目录时视为目录存在。
- 如有需要再考虑 `follow_symlink=false`，第一版不建议做。

验收标准：

- 文档与实现一致。
- symlink 相关测试在支持的平台上稳定。

## 测试建议

新增或补充测试：

### 基础行为

- 默认 `recursive=true` 创建多级目录。
- `recursive=false` 创建单级目录。
- `recursive=false` 父目录不存在时报错。
- 已存在目录返回成功。
- 已存在普通文件返回错误。

### dry-run

- `dry_run=true` 不创建目录。
- `dry_run=true recursive=true` 返回多级计划。
- `dry_run=true recursive=false` 只返回目标目录计划。
- dry-run 仍受 sandbox 限制。

### 路径解析

- 相对路径基于 `_cwd`。
- `~` 展开到 sandbox home。
- 包含 `..` 的路径被拒绝。
- 敏感目录被拒绝。

### 并发和幂等

- 连续调用两次同一路径，第二次返回 already exists。
- `os.ErrExist` 后目标是目录时不失败。

### 权限

- `file_mkdir` 仍是 `PermApprove`。
- read-only guard 下 `file_mkdir` 会被阻止。

## 文档更新

完成实现后，同步更新：

- `docs/tools/file_mkdir.md`

如果加入 `dry_run`，参数表增加：

```text
dry_run | 否 | false | 只预览将创建的目录，不实际创建。
```

并补充：

```text
recursive=true 时，工具可以创建多级父目录。dry_run=true 可用于查看将创建哪些目录。
```

## 风险与边界

1. `dry_run` 存在 TOCTOU 问题。
   预览和实际执行之间，文件系统状态可能变化。因此 dry-run 只能作为计划，不是保证。

2. 递归创建清单可能受并发影响。
   计划中列出的目录在实际创建前可能已被其他进程创建。

3. symlink 行为跨平台细节复杂。
   第一版建议只文档化当前 `os.Stat` 行为，不急于增加控制参数。

4. 不建议把 `file_mkdir` 做成项目脚手架工具。
   它只创建目录。多文件、多模板、项目初始化应由更高层工具或脚本负责。

## 推荐结论

优先实现 Phase 1 和 Phase 2。

Phase 1 成本低，能马上提升错误可读性和测试稳定性。Phase 2 的 `dry_run` 和创建计划能让 Agent 在需要谨慎操作时先解释将要改变哪些路径。Phase 3 和 Phase 4 可以等实际需要更强审计输出或 symlink 控制时再做。
