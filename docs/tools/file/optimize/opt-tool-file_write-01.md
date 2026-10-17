# opt-tool-file_write-01

## 目标

优化 `file_write` 的覆盖保护、写入可靠性和审计反馈，让它继续保持“完整文件写入、需要审批、路径受 sandbox 限制”的定位，同时降低误覆盖用户文件、部分写入和上下文误用的风险。

本方案聚焦：

- 覆盖已有文件的显式语义
- 原子写入和失败恢复
- 写入前计划与 dry-run
- 文件大小和内容类型边界
- 与 `file_patch`、`file_mkdir`、`terminal` 的职责边界
- 测试覆盖补齐

## 当前状态

相关实现：

- `internal/tool/builtin_fs.go`
- `internal/tool/builtin_test.go`
- `docs/tools/file_write.md`

当前 `file_write` 的流程：

1. 通过 `resolvePathArg(args, "path")` 解析目标路径。
2. 读取必填参数 `content`。
3. 使用 `os.MkdirAll(filepath.Dir(path), 0o755)` 自动创建父目录。
4. 使用 `os.WriteFile(path, []byte(content), 0o644)` 写入文件。
5. 返回 `Written <N> bytes to <path>`。

当前语义：

- 目标不存在：创建文件。
- 目标存在：完整覆盖。
- 父目录不存在：自动创建。
- 文件权限：`0644`。
- 父目录权限：`0755`。
- 权限级别：`PermApprove`。

当前优势：

- 比 `terminal` 中的 `cat > file` 或 `echo > file` 更可控。
- 路径解析和 sandbox 校验统一。
- 适合创建新文档、生成产物、完整重写文件。

## 主要问题

### 1. 覆盖已有文件没有二次语义保护

当前只要调用 `file_write`，目标文件存在时就会直接覆盖。虽然工具需要审批，但工具参数本身没有表达：

- 这是新建文件。
- 这是允许覆盖。
- 这是只在文件不存在时创建。
- 这是基于某个已读版本的覆盖。

这会带来风险：Agent 误把小修改当成完整重写，或覆盖用户刚刚改过的文件。

### 2. 写入不是原子替换

`os.WriteFile` 会直接打开目标文件写入。异常中断、磁盘错误、进程退出时，理论上可能留下部分内容或破坏旧文件。

对配置文件、文档产物、代码文件来说，更理想的方式是：

1. 写入同目录临时文件。
2. `fsync` 临时文件。
3. `rename` 到目标路径。
4. 必要时 `fsync` 父目录。

这样能降低部分写入风险。

### 3. 缺少 dry-run 或写入计划

调用前无法只预览：

- 目标是否存在。
- 将创建哪些父目录。
- 将写入多少字节。
- 是否会覆盖。
- 新旧内容是否相同。

这让 Agent 在谨慎模式下只能先 `file_read` / `file_list`，再决定是否写入。可增加 `dry_run` 支持。

### 4. 缺少 expected hash / compare-and-swap

如果 Agent 先读取文件，再生成完整内容，读和写之间用户可能修改文件。

建议支持：

```json
{
  "expected_sha256": "<hash>"
}
```

如果目标当前内容 hash 不匹配，则拒绝覆盖。这能防止基于旧上下文覆盖新内容。

### 5. 缺少内容大小限制

当前可以写入任意大小字符串。大内容会带来：

- 内存压力。
- 审批界面难以审查。
- 意外写入巨大文件。

建议加入默认最大写入大小，例如 10MB 或 25MB，并允许后续配置化。

### 6. 现有测试覆盖偏基础

当前测试主要覆盖写入后能被读取，以及权限是 `PermApprove`。缺少：

- 覆盖已有文件。
- 父目录自动创建。
- 目标是目录。
- sandbox 拒绝。
- content 缺失。
- 原子写失败路径。
- expected hash 不匹配。

## 优化原则

1. 保持 `file_write` 是完整文件写入工具，不做局部 patch。
2. 保持默认行为兼容，但逐步引入更明确的覆盖控制。
3. 写入必须继续受 `PermApprove` 和 `tool_execution_guard` 约束。
4. 用标准库实现原子写，不引入重依赖。
5. 优先保护用户已有文件，再考虑便利性。

## 推荐方案

### 1. 增加写入模式

新增可选参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `mode` | 否 | `overwrite` | 写入模式：`overwrite`、`create_new`、`overwrite_if_exists`。 |

建议语义：

- `overwrite`：兼容当前行为，存在则覆盖，不存在则创建。
- `create_new`：只允许创建新文件，目标存在时报错。
- `overwrite_if_exists`：只允许覆盖已有文件，目标不存在时报错。

如果想更保守，可以把默认值长期迁移到 `create_new`，但这会破坏兼容。第一阶段不建议改默认。

### 2. 增加 dry-run

新增：

```json
{
  "dry_run": true
}
```

dry-run 行为：

- 解析并校验路径。
- 检查目标是否存在。
- 检查父目录是否存在，以及会创建哪些父目录。
- 计算 `content` 字节数和 sha256。
- 不创建目录，不写文件。

输出示例：

```text
Would write 1284 bytes to /path/to/file.md
Target: exists, would overwrite
Parent directories to create: 0
SHA256: <hash>
```

这对“先确认再写”很有用。

### 3. 增加 expected_sha256

新增可选参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `expected_sha256` | 否 | 无 | 如果目标存在，要求当前文件 hash 匹配后才覆盖。 |

行为：

- 目标存在且 hash 不匹配：拒绝写入。
- 目标存在且 hash 匹配：允许继续。
- 目标不存在且提供了 `expected_sha256`：返回错误，除非后续约定空文件 hash 语义。

错误示例：

```text
target file changed since it was read: expected sha256 <A>, got <B>
```

收益：

- 防止读写之间的竞态覆盖。
- 支持 Agent 在 `file_read` 后进行安全完整重写。

### 4. 原子写入

新增内部函数：

```go
func atomicWriteFile(path string, data []byte, perm fs.FileMode) error
```

建议流程：

1. 在目标同目录创建临时文件。
2. 写入内容。
3. `Close` 前或后同步文件内容。
4. `os.Rename(temp, path)`。
5. 失败时删除临时文件。

注意：

- Windows 上 rename 覆盖语义不同，需要测试。
- 如果目标是目录，应该提前返回明确错误。
- 临时文件必须在同目录，保证 rename 尽量原子。

第一阶段可只在 Unix 主路径做标准实现，Windows 行为用测试约束。

### 5. 写入计划结构化

新增内部结构：

```go
type fileWritePlan struct {
	Path              string
	Mode              string
	TargetExists      bool
	TargetIsDir       bool
	Bytes             int
	ContentSHA256      string
	ParentDirsToCreate []string
	WouldOverwrite    bool
}
```

Handler 可以先生成 plan，再执行。

收益：

- dry-run 和实际写入共用判断。
- 输出更一致。
- 测试可以直接覆盖计划逻辑。

### 6. 内容大小限制

新增常量：

```go
const maxFileWriteBytes = 10 * 1024 * 1024
```

行为：

- `len([]byte(content)) > maxFileWriteBytes` 时拒绝。
- 错误提示写明当前大小和最大值。

如果未来需要写大文件，应由专门的 artifact 或 streaming 工具处理，而不是通过单个 tool call 携带巨大字符串。

### 7. 同内容写入优化

如果目标存在且内容完全相同，可以返回：

```text
File already up to date: /path/to/file.md
```

默认可以不重写文件，避免修改 mtime。

注意：这会改变当前“总是写入”的行为。建议作为 Phase 3，或受 `skip_if_unchanged=true` 参数控制。

## 分阶段实施

### Phase 1：测试拆分和错误提示

改动范围：

- `internal/tool/builtin_fs.go`
- `internal/tool/builtin_test.go`
- `docs/tools/file_write.md`

内容：

- 拆出 `file_write` 独立测试。
- 补充 content 缺失、目标是目录、父目录自动创建、sandbox 拒绝测试。
- 改善目标是目录时的错误提示。

验收标准：

- `go test ./internal/tool` 通过。
- 当前默认覆盖行为不变。
- 常见错误更可读。

### Phase 2：dry-run 和写入计划

内容：

- 新增 `fileWritePlan`。
- 新增 `dry_run` 参数。
- dry-run 不创建父目录、不写文件。
- 输出目标状态、字节数、hash、将创建的父目录。

验收标准：

- dry-run 对新文件、已有文件、缺失父目录都有准确输出。
- dry-run 不改变磁盘状态。
- 实际写入路径继续兼容旧行为。

### Phase 3：覆盖控制和 expected hash

内容：

- 新增 `mode` 参数。
- 新增 `expected_sha256` 参数。
- 目标 hash 不匹配时拒绝覆盖。

验收标准：

- `create_new` 遇到已有文件时报错。
- `overwrite_if_exists` 遇到不存在文件时报错。
- `expected_sha256` 匹配时可覆盖。
- `expected_sha256` 不匹配时不写入。

### Phase 4：原子写入和大小限制

内容：

- 新增 `atomicWriteFile`。
- 新增 `maxFileWriteBytes`。
- 写入失败时清理临时文件。

验收标准：

- 正常写入结果一致。
- 写入失败不留下目标半成品。
- 超过大小限制时拒绝。
- Windows 和 Unix 行为都有测试或平台条件处理。

### Phase 5：同内容跳过写入

内容：

- 新增 `skip_if_unchanged`，默认可先为 `false`。
- 当内容一致时返回 already up to date。

验收标准：

- 开启后相同内容不改 mtime。
- 不影响默认写入兼容性。

## 测试建议

新增或补充测试：

### 基础行为

- 创建新文件。
- 覆盖已有文件。
- 自动创建父目录。
- 目标路径是目录时报错。
- `content` 缺失时报错。
- 相对路径基于 `_cwd`。
- sandbox 拒绝路径时报错。

### dry-run

- dry-run 新文件不落盘。
- dry-run 已有文件显示 would overwrite。
- dry-run 缺失父目录显示将创建的目录。
- dry-run 输出 content sha256。

### mode

- `overwrite` 保持兼容。
- `create_new` 遇到已有文件失败。
- `create_new` 遇到不存在文件成功。
- `overwrite_if_exists` 遇到不存在文件失败。
- 非法 mode 返回错误。

### expected_sha256

- hash 匹配时覆盖成功。
- hash 不匹配时拒绝且原文件不变。
- 目标不存在但提供 hash 时行为明确。

### 原子写入

- 正常写入内容正确。
- 临时文件清理。
- rename 失败时返回错误。
- 并发或重复写入不产生损坏文件。

### 大小限制

- 小于限制写入成功。
- 超过限制返回清晰错误。

### 权限和 guard

- `file_write` 仍是 `PermApprove`。
- 用户要求只读或不要修改文件时，`tool_execution_guard` 阻止 `file_write`。

## 文档更新

完成实现后，同步更新：

- `docs/tools/file_write.md`

如果加入 `dry_run`、`mode`、`expected_sha256`，参数表增加：

```text
mode | 否 | overwrite | 写入模式：overwrite、create_new、overwrite_if_exists。
dry_run | 否 | false | 只预览写入计划，不创建目录或写文件。
expected_sha256 | 否 | 无 | 覆盖前要求目标当前内容 hash 匹配。
```

并补充：

```text
对已有文件做完整重写时，推荐先读取文件并在写入时传入 expected_sha256，避免覆盖读写之间发生的新修改。
```

## 风险与边界

1. `dry_run` 存在 TOCTOU 问题。
   预览和实际写入之间，目标文件可能变化。因此 dry-run 只能作为计划，不是保证。

2. `expected_sha256` 不能替代审批。
   它只能防止基于旧内容覆盖，不能判断写入内容是否正确。

3. 原子写入跨平台细节不同。
   Windows 上覆盖 rename 行为需要单独验证，必要时采用平台特定实现。

4. 大文件不适合通过 `file_write` 传输。
   如果需要生成大型二进制或流式产物，应由专门工具负责。

5. 不建议加入 append 模式作为默认能力。
   追加日志、列表、结构化文件时更容易产生重复或格式破坏。需要追加时应有更专门的语义工具，或明确构造完整文件内容。

## 推荐结论

优先实现 Phase 1、Phase 2 和 Phase 3。

Phase 1 能先补齐测试和错误提示。Phase 2 的 dry-run 让 Agent 可以在写入前解释将发生的磁盘变化。Phase 3 的 `mode` 和 `expected_sha256` 是最关键的防误覆盖能力。Phase 4 的原子写入适合随后补上，提升可靠性。Phase 5 可作为锦上添花，等兼容性风险确认后再推进。
