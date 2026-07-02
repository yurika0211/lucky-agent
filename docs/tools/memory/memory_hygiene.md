# memory_hygiene Tool

`memory_hygiene` 是 LuckyAgent 的内置记忆卫生工具，用来审计、隔离或删除 memory vault 中的脏记忆。默认 `audit` 是只读扫描；`quarantine` 会归档可疑记忆，使其不再参与召回；`delete` 会物理删除匹配条目。

这是可能修改持久记忆库的工具，因此被标记为需要批准。

## 工具定义

实现位置：

- `internal/tool/builtin_memory.go`
- `internal/tool/memory_service.go`
- `internal/memory/hygiene.go`

注册信息：

```go
Name:         "memory_hygiene"
Category:     CatBuiltin
Source:       "builtin"
Permission:   PermApprove
ParallelSafe: false
```

## 参数

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `action` | 否 | `audit` | 操作：`audit`、`quarantine` 或 `delete`。也接受部分别名。 |
| `min_severity` | 否 | `medium` | 最低严重级别：`low`、`medium`、`high`、`critical`。 |
| `include_inactive` | 否 | `false` | 是否包含 archived、superseded、expired 或 future-dated 记忆。 |
| `limit` | 否 | `50` | 最多返回或处理多少个 findings。 |

示例：

```json
{
  "action": "audit",
  "min_severity": "medium",
  "limit": 50
}
```

## 执行流程

`memory_hygiene` 的执行过程是：

1. 检查 memory store 是否已初始化。
2. 读取 `action`，转小写并去空白。
3. action 为空时使用 `audit`。
4. 读取 `limit`，当前直接转换为 int，没有额外上下限夹取。
5. 构造 `memory.HygieneOptions`。
6. 根据 action 调用对应 store 方法。
7. 将 `memory.HygieneReport` 用 `json.MarshalIndent` 输出。

如果 handler 未配置，默认错误是：

```text
memory_hygiene handler not configured
```

如果 store 未初始化，返回：

```text
memory store not initialized
```

## 脏记忆判断规则

`memory_hygiene` 当前不是用 LLM 做语义判断，而是在 `internal/memory/hygiene.go` 中用确定性规则扫描每条 memory entry。

默认只扫描 active 记忆。只有 `include_inactive=true` 时，才会把 archived、superseded、expired 或 future-dated 记忆也纳入扫描。

当前规则：

| reason | 严重级别 | 建议动作 | 判断方式 |
| --- | --- | --- | --- |
| `empty` | `high` | `delete` | `Content` 去空白后为空。 |
| `raw_conversation` | `high` | `quarantine` | 内容以 `User:`、`Assistant:` 或 `System:` 开头；或者 category 是 `conversation` / `session`，且内容包含原始 `User:` / `Assistant:` 对话片段。 |
| `secret_like` | `critical` | `quarantine` | 正则命中疑似敏感凭据，例如 `api_key`、`access_token`、`secret`、`password`、`passwd`、`Bearer <token>`、`sk-...` 或 Slack token。 |
| `prompt_injection` | `high` | `quarantine` | 正则命中提示注入或越狱文本，例如 `ignore previous instructions`、`system prompt`、`developer message`、`you are now`、`jailbreak`、`do not obey`、`泄露.*提示词`、`忽略.*指令`、`越狱`。 |
| `expired` | `medium` | `delete` | `ExpiresAt` 不为空，且已经小于或等于当前时间。 |
| `low_confidence_long_term` | `medium` | `quarantine` | 记忆是长期记忆 `TierLong`，但 `Confidence` 大于 0 且小于 0.35。 |
| `duplicate` | `medium` | `delete` | 同一 category 下，规范化后的 content 重复；工具会选择权重较低的一条作为 loser。 |
| `state_conflict` | `high` | `quarantine` | 多条 active 记忆有相同 `StateKey`，但 `StateValue` 不同；工具会选择置信度较低的一条作为 loser。 |
| `oversized` | `low` | `quarantine` | content 超过 4000 个 rune，且不是长期记忆。 |

重复判断会先对 content 做规范化：

1. 去除 wiki 语法影响。
2. 使用 `strings.Fields` 折叠空白。
3. 转小写。
4. 和 category 组成去重 key。

因此下面两条在同一 category 下会被视为重复：

```text
Run tests before deploy
run   tests before   deploy
```

状态冲突判断依赖 `StateKey` 和 `StateValue`：

```text
StateKey   = family.daughter.pollen
StateValue = active
```

如果另一条 active 记忆使用相同 `StateKey`，但 `StateValue=resolved`，就会产生 `state_conflict` finding。

## 严重级别和排序

严重级别排序是：

```text
critical > high > medium > low
```

`min_severity` 会过滤低于指定级别的 finding。例如：

```json
{
  "min_severity": "high"
}
```

只会保留 `high` 和 `critical`。

findings 会按 `Score` 从高到低排序。分数相同时，再按 entry ID 排序。

当前内置分数：

| reason | score |
| --- | --- |
| `secret_like` | `0.98` |
| `state_conflict` | `0.88` |
| `prompt_injection` | `0.86` |
| `empty` | `0.85` |
| `raw_conversation` | `0.82` |
| `duplicate` | `0.70` |
| `expired` | `0.65` |
| `low_confidence_long_term` | `0.62` |
| `oversized` | `0.40` |

## quarantine 和 delete 的具体效果

`audit` 只返回 findings，不修改 memory vault。

`quarantine` 会对匹配 entry 做这些修改：

1. 将 `Status` 设置为 `archived`。
2. 添加标签：`hygiene`、`dirty`、`hygiene-<reason>`。
3. 如果 `Confidence` 为空或大于 `0.25`，将其降到 `0.25`。
4. 调用 `persist` 保存变更。

归档后的记忆不会参与普通 recall，但源笔记仍保留，方便人工审查。

`delete` 会：

1. 删除对应 entry 文件。
2. 从内存索引中移除该 entry。
3. 调用 `persist` 保存变更。

因此建议默认使用：

```text
audit -> quarantine -> delete
```

除非用户明确要求物理删除，否则不要直接使用 `delete`。

## action

支持的 action：

| 输入 | 实际行为 |
| --- | --- |
| `audit` | 调用 `store.AuditHygiene`，只读扫描。 |
| `scan` | 等同 audit。 |
| `dry_run` | 等同 audit。 |
| `dry-run` | 等同 audit。 |
| `quarantine` | 调用 `store.QuarantineDirty`，隔离可疑记忆。 |
| `archive` | 等同 quarantine。 |
| `delete` | 调用 `store.DeleteDirty`，删除可疑记忆。 |
| `purge` | 等同 delete。 |

非法 action 返回：

```text
invalid memory_hygiene action "<action>" (use audit, quarantine, or delete)
```

## 输出格式

输出是 pretty JSON，对应 `memory.HygieneReport`。

示例结构：

```json
{
  "findings": [],
  "quarantined": 0,
  "deleted": 0
}
```

实际字段以 `internal/memory/hygiene.go` 中的 `HygieneReport` 为准。

## 适合使用的场景

优先使用 `memory_hygiene` 的场景：

- 怀疑 memory vault 中有脏记忆。
- 需要先审计旧的 raw conversation 片段。
- 需要隔离会污染召回的记忆。
- 需要按严重级别清理记忆库。

推荐顺序：

```text
audit -> quarantine -> delete
```

先审计，再隔离。只有用户明确要求物理删除时再使用 `delete`。

## 不适合使用的场景

不优先使用 `memory_hygiene` 的场景：

- 查找普通记忆，应使用 `recall`。
- 保存新记忆，应使用 `remember`。
- 清理 RAG 索引；这是 memory vault 工具，不是 RAG 工具。
- 用户没有明确要求清理或诊断记忆污染。

## 风险和注意事项

`memory_hygiene` 的主要注意点：

- 权限是 `PermApprove`。
- `audit` 只读，但 `quarantine` 和 `delete` 会改变 memory vault。
- 脏记忆判断是确定性规则扫描，不是完整语义审查。
- 规则可能产生误报，例如普通文本中出现 `password` 或 `system prompt` 也可能被标记。
- `quarantine` 后记忆不再被正常召回，但源笔记会保留用于审查。
- `delete` 是物理删除匹配条目，风险更高。
- `limit` 当前没有像 `boundedIntArg` 那样做 min/max 限制。

## 维护注意事项

如果后续修改 `memory_hygiene`，需要同步检查：

- action 别名是否变化。
- 默认 action 是否仍是 `audit`。
- 默认 `min_severity` 是否仍是 `medium`。
- 权限是否仍是 `PermApprove`。
- `limit` 是否新增上下限。
- `HygieneReport` 输出字段是否变化。
- quarantine 是否仍阻止后续 recall。
