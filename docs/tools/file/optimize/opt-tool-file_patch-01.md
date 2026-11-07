# opt-tool-file_patch-01

## 目标

优化 `file_patch` 的误匹配防护、补丁可预览性、写入可靠性和诊断能力，让它继续保持“局部修改已有文件、需要审批、路径受 sandbox 限制”的定位，同时降低改错位置、覆盖并发修改、破坏换行格式或误改二进制文件的风险。

本方案聚焦：

- dry-run / patch plan
- expected hash 防并发覆盖
- 原子写入
- diff hunk 诊断增强
- 二进制和大文件保护
- 换行和权限保留
- 测试矩阵补齐

## 当前状态

相关实现：

- `internal/tool/builtin_fs.go`
- `internal/tool/builtin_test.go`
- `docs/tools/file_patch.md`

当前 `file_patch` 支持两种模式：

1. 精确字符串替换：`match` + `replace`。
2. 行级 diff hunk：`diff`。

当前流程：

1. 通过 `resolvePathArg(args, "path")` 解析路径。
2. 读取目标文件 `os.ReadFile(path)`。
3. 如果 `diff` 非空，进入 diff 模式。
4. 否则使用 `match` / `replace` 精确替换。
5. 使用 `os.WriteFile(path, []byte(patched), 0o644)` 写回文件。

当前优势：

- 比 `file_write` 更适合小范围修改。
- 精确替换支持 `occurrence` 和 `replace_all`。
- diff 模式支持多个 hunk。
- diff 模式保留目标文件是否以换行结尾。
- 不依赖系统 `patch` 命令。

## 主要问题

### 1. 没有 dry-run

当前补丁一旦匹配成功就直接写回。调用方无法只预览：

- 会替换第几个匹配。
- 总共有多少匹配。
- diff hunk 会匹配到哪一段。
- 修改前后行数变化。
- 是否会改变文件末尾换行。

对于局部编辑工具，dry-run 是非常有价值的审计能力。

### 2. 没有 expected hash

典型流程是：

1. `file_read` 读取目标段落。
2. Agent 构造 `file_patch`。
3. 写回。

如果读和写之间文件被用户或其他工具修改，当前 patch 可能仍然匹配某段旧文本，或者在新上下文下改错位置。

建议支持：

```json
{
  "expected_sha256": "<hash>"
}
```

写入前检查目标当前内容 hash，防止基于旧上下文修改。

### 3. 写回不是原子替换

当前使用 `os.WriteFile` 直接写回目标。异常中断或写入失败时可能留下部分内容。

`file_patch` 修改的是已有文件，比 `file_write` 更常用于代码和配置，因此应优先采用原子写入。

### 4. 文件权限被固定为 `0644`

当前写回：

```go
os.WriteFile(path, []byte(patched), 0o644)
```

如果原文件有可执行位或更严格权限，patch 后可能丢失权限语义。应读取原文件 mode，并尽量保留。

### 5. diff 诊断不足

当 hunk 匹配失败时，只返回：

```text
diff hunk <N> did not match target file
```

对 Agent 修复补丁不够友好。可以提供：

- hunk 的 before 行数。
- 最接近匹配位置。
- 第一处不匹配的行。
- 建议先读取目标上下文。

不需要实现复杂 fuzzy patch，但错误信息应更可操作。

### 6. 二进制和大文件保护不足

当前会把任何文件读成字符串并修改。风险：

- 误改二进制文件。
- 对大文件做 `strings.Count` 和完整替换，内存压力大。
- minified 文件误匹配范围大。

建议增加：

- 最大文件大小限制。
- 二进制检测。
- 对超大文本提示使用更专门工具。

### 7. diff 语义没有严格校验“至少有变更”

当前 hunk 只要求 before/after 至少有内容。纯上下文 hunk 可能被视为成功，但没有实际修改。

建议区分：

- 有 context。
- 有 deletion。
- 有 addition。
- 实际是否改变内容。

如果 patch 后内容不变，应返回明确错误或 no-op 结果。

## 优化原则

1. 保持 `file_patch` 只修改已有文本文件，不创建新文件。
2. 默认保持现有 `match` 和 `diff` 行为兼容。
3. 优先防止误改和并发覆盖。
4. 写回时尽量保留文件权限和换行风格。
5. 不引入完整 GNU patch 兼容层，保持实现可控。

## 推荐方案

### 1. 增加 patch plan

新增内部结构：

```go
type filePatchPlan struct {
	Path          string
	Mode          string
	OriginalBytes int
	PatchedBytes  int
	OriginalHash  string
	PatchedHash   string
	Replacements  int
	Hunks         int
	Changed       bool
}
```

对 exact replace 增加：

```go
type exactReplacePlan struct {
	MatchCount    int
	Occurrence    int
	ReplaceAll    bool
	TargetOffsets []int
}
```

对 diff 增加：

```go
type diffPatchPlan struct {
	Hunks []diffHunkPlan
}

type diffHunkPlan struct {
	Index      int
	StartLine  int
	BeforeLines int
	AfterLines  int
}
```

收益：

- dry-run 和实际写入共用逻辑。
- 测试可以断言匹配位置和替换数量。
- 输出更可审计。

### 2. 增加 dry-run

新增参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `dry_run` | 否 | `false` | 只预览补丁计划，不写回文件。 |

exact replace 输出示例：

```text
Would patch /path/to/file.go
Mode: exact
Matches: 3
Would replace occurrence: 2
Bytes: 1200 -> 1214
```

diff 输出示例：

```text
Would patch /path/to/file.go
Mode: diff
Hunks: 2
- hunk 1 matches at line 18
- hunk 2 matches at line 44
Bytes: 1200 -> 1270
```

dry-run 必须不写文件。

### 3. 增加 expected_sha256

新增参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `expected_sha256` | 否 | 无 | 写入前要求目标当前内容 hash 匹配。 |

行为：

- hash 匹配：继续 patch。
- hash 不匹配：拒绝 patch。

错误示例：

```text
target file changed since it was read: expected sha256 <A>, got <B>
```

建议 `file_read` 或未来读取工具可以输出 hash，方便配合使用。

### 4. 原子写入并保留权限

新增或复用 `file_write` 方案中的：

```go
func atomicWriteFile(path string, data []byte, perm fs.FileMode) error
```

写入前：

```go
info, err := os.Stat(path)
mode := info.Mode().Perm()
```

写回时使用原 mode，而不是固定 `0644`。

收益：

- 减少部分写入风险。
- 不破坏可执行脚本或更严格权限。

### 5. 增加文本文件保护

建议新增：

```go
const maxFilePatchBytes = 5 * 1024 * 1024
```

行为：

- 超过大小限制拒绝。
- 检测 NUL byte 或高比例不可打印字符，拒绝二进制文件。
- 目标是目录时报清晰错误。

错误示例：

```text
file appears to be binary; file_patch only supports text files
```

### 6. diff 诊断增强

当 hunk 匹配不到时，返回更多信息：

```text
diff hunk 2 did not match target file; expected 4 before-lines. Closest candidate starts at line 38 with first mismatch at hunk line 3.
```

实现可以先做轻量版：

- 查找第一行相同的候选位置。
- 计算连续匹配数量。
- 返回候选行号和匹配进度。

不建议做 fuzzy apply，避免意外改错位置。

### 7. no-op 检测

patch 后如果内容完全不变：

```go
if patched == content { ... }
```

建议返回：

```text
patch produced no changes
```

对 exact replace 来说一般不会发生，除非 `match == replace`。

对 diff 来说纯上下文 hunk 可能发生，应明确拒绝或返回 no-op。

### 8. 行号范围辅助

可以新增可选参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `expected_start_line` | 否 | 无 | 要求 exact match 或 diff hunk 出现在指定行附近。 |
| `line_tolerance` | 否 | `0` | 允许的行号偏移。 |

第一版不建议立即实现，但可以作为后续能力。它能进一步降低重复文本误匹配。

## 分阶段实施

### Phase 1：测试拆分、no-op 和权限保留

改动范围：

- `internal/tool/builtin_fs.go`
- `internal/tool/builtin_test.go`
- `docs/tools/file_patch.md`

内容：

- 拆出 exact replace、diff、错误路径测试。
- 增加 `match == replace` no-op 测试。
- 写回时保留原文件权限。
- 目标是目录时报更清晰错误。

验收标准：

- `go test ./internal/tool` 通过。
- 现有 patch 行为兼容。
- no-op 不会静默写回。
- 可执行文件 patch 后仍保留可执行位。

### Phase 2：dry-run 和 patch plan

内容：

- 新增 `filePatchPlan`。
- 新增 `dry_run` 参数。
- exact 和 diff 都能输出计划。

验收标准：

- dry-run 不修改文件。
- exact dry-run 输出匹配数量和目标 occurrence。
- diff dry-run 输出 hunk 数和匹配行号。
- 实际 patch 和 dry-run 使用同一匹配逻辑。

### Phase 3：expected hash 和原子写入

内容：

- 新增 `expected_sha256`。
- 引入原子写入。
- hash 不匹配时拒绝写入。

验收标准：

- hash 匹配时 patch 成功。
- hash 不匹配时文件不变。
- 写入失败不留下半成品。

### Phase 4：二进制和大文件保护

内容：

- 新增最大文件大小限制。
- 增加二进制检测。
- 文档说明 `file_patch` 只适合文本文件。

验收标准：

- 二进制文件被拒绝。
- 超大文件被拒绝。
- 普通 UTF-8 / CRLF 文本正常 patch。

### Phase 5：diff 诊断增强

内容：

- hunk 匹配失败时返回最近候选信息。
- 可选增加 hunk plan 的行号输出。

验收标准：

- 匹配失败错误包含 hunk 编号和可操作诊断。
- 不引入 fuzzy apply。
- 成功路径输出保持兼容。

### Phase 6：行号约束

内容：

- 可选新增 `expected_start_line` 和 `line_tolerance`。
- exact replace 和 diff hunk 均可校验匹配位置。

验收标准：

- 匹配位置不在范围内时拒绝 patch。
- 未提供行号参数时兼容旧行为。

## 测试建议

新增或补充测试：

### exact replace

- 替换第一个匹配。
- 替换第 N 个匹配。
- `replace_all=true` 替换所有匹配。
- `occurrence<=0` 回退为 1。
- `occurrence` 超过匹配数时报错。
- `match` 为空时报错。
- `replace` 缺失时报错。
- `match == replace` 返回 no-op。

### diff mode

- 单 hunk 成功。
- 多 hunk 成功。
- unified diff header 被忽略。
- 空 diff 报错。
- 非法行前缀报错。
- hunk 匹配不到时报诊断。
- 纯上下文 hunk 返回 no-op 或错误。
- 保留末尾换行。
- 原文件无末尾换行时不新增换行。

### dry-run

- exact dry-run 不修改文件。
- diff dry-run 不修改文件。
- dry-run 输出匹配数量、hunk 数、字节变化。

### expected hash

- hash 匹配 patch 成功。
- hash 不匹配 patch 拒绝。
- 拒绝后文件不变。

### 文件属性

- patch 后保留原权限。
- 目标是目录时报错。
- 二进制文件被拒绝。
- 超大文件被拒绝。
- CRLF 文本处理策略固定。

### 权限和 guard

- `file_patch` 仍是 `PermApprove`。
- 用户要求只读或不要修改文件时，guard 阻止 `file_patch`。

## 文档更新

完成实现后，同步更新：

- `docs/tools/file_patch.md`

如果加入新参数，参数表增加：

```text
dry_run | 否 | false | 只预览补丁计划，不写回文件。
expected_sha256 | 否 | 无 | 写入前要求目标当前内容 hash 匹配。
expected_start_line | 否 | 无 | 要求匹配位置接近指定行。
line_tolerance | 否 | 0 | 允许匹配行号偏移。
```

补充建议：

```text
对重要文件做局部修改时，推荐先 file_read 确认上下文，并在 file_patch 中传入 expected_sha256，避免基于旧内容写回。
```

## 风险与边界

1. dry-run 存在 TOCTOU 问题。
   预览和实际 patch 之间文件可能变化，因此关键文件仍应使用 `expected_sha256`。

2. 不建议实现 fuzzy patch。
   fuzzy apply 可能在上下文漂移时改错位置。当前工具应保持严格匹配。

3. CRLF 处理需要明确。
   当前 diff 模式会规范化为 `\n`，可能改变 Windows 风格换行。优化时应决定是否保留原换行风格。

4. 原子写入跨平台细节不同。
   Windows rename 覆盖行为需要单独验证或平台特定实现。

5. 大文件 patch 不应通过字符串全量处理无限放大。
   对大型日志或生成产物，应优先使用专门工具或限制范围。

## 推荐结论

优先实现 Phase 1、Phase 2 和 Phase 3。

Phase 1 先固定 no-op、权限保留和测试结构。Phase 2 的 dry-run 是 `file_patch` 最关键的可审计增强。Phase 3 的 `expected_sha256` 和原子写入能显著降低并发覆盖和部分写入风险。Phase 4、Phase 5 作为第二批推进，分别解决文件类型保护和 diff 诊断质量。
