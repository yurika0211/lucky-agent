# opt-tool-memory_hygiene-01

## 目标

优化 `memory_hygiene` 的规则可解释性、操作安全、limit 边界、审计输出和误报处理，让它继续承担“审计、隔离或删除脏记忆”的职责，同时降低误删、误归档、结果不可追溯和规则难维护的问题。

本方案聚焦：

- limit 和 action 参数校验
- dry-run / apply 分离
- finding 结构增强
- 规则复用到 remember 写入前校验
- quarantine 可恢复性
- 删除前确认策略
- 测试矩阵补齐

## 当前状态

相关实现：

- `internal/tool/builtin_memory.go`
- `internal/tool/memory_service.go`
- `internal/memory/hygiene.go`
- `docs/tools/memory/memory_hygiene.md`

当前支持：

- `audit` / `scan` / `dry_run` / `dry-run`
- `quarantine` / `archive`
- `delete` / `purge`

当前内置规则包括：

- `empty`
- `raw_conversation`
- `secret_like`
- `prompt_injection`
- `expired`
- `low_confidence_long_term`
- `duplicate`
- `state_conflict`
- `oversized`

当前优势：

- 规则确定性强，不依赖 LLM。
- 默认 `audit` 只读。
- `quarantine` 会归档而不是删除。
- finding 包含 severity、reason、suggested_action、score 和 preview。

## 主要问题

### 1. `limit` 没有上下限

tool 层当前直接把 `limit` 转成 int。`limit <= 0` 在底层表示不限制，这可能导致一次返回或处理所有 findings。

建议：

- 默认 50。
- 最小 1。
- 最大 200 或 500。
- 需要全量扫描时使用显式 `limit=0` + `allow_unlimited=true`。

### 2. `delete` 风险高，但缺少二次确认参数

`delete` 会物理删除 entry 文件。虽然 tool 权限是 `PermApprove`，但从工具参数看不出用户是否明确确认了物理删除。

建议新增：

```json
{
  "action": "delete",
  "confirm_delete": true
}
```

没有确认时拒绝执行。

### 3. audit 和 dry-run 别名容易混淆

当前 `dry_run` 等同 `audit`。这没问题，但 `quarantine` / `delete` 没有 dry-run 计划模式。

建议支持：

- `action=audit`：只读扫描。
- `action=quarantine` + `dry_run=true`：返回将归档哪些 entry。
- `action=delete` + `dry_run=true`：返回将删除哪些 entry。

### 4. quarantine 缺少恢复路径

`quarantine` 会把 status 设为 archived，并加 hygiene 标签。但没有工具层恢复动作。

建议新增：

- `action=restore`
- `ids`
- `reason`

或者至少输出恢复说明。

### 5. finding 缺少匹配证据

当前 preview 只有截断内容，没有说明具体匹配到什么。排查误报时不够明确。

建议 finding 增加：

- `matched_rule`
- `matched_text`
- `field`
- `dedupe_key`
- `conflict_with`

注意 secret-like 的 `matched_text` 必须脱敏。

### 6. 规则和 remember 写入前校验没有复用

hygiene 负责事后清理，remember 负责入口。当前两者规则分散。

建议抽出：

```go
func AnalyzeMemoryEntry(e *Entry, opts HygieneOptions) []HygieneIssue
func AnalyzeMemoryContent(content string) []HygieneIssue
```

让 remember 写入前复用 raw conversation、secret-like、prompt injection、oversized 判断。

### 7. state_conflict 只选择 loser，不输出 winner

当前 `state_conflict` 会 quarantine 置信度较低的 entry，但 finding 中没有说明和哪条 entry 冲突。

建议增加：

- `conflict_with_id`
- `state_key`
- `current_value`
- `conflicting_value`

### 8. duplicate 只保留 loser，不输出 winner

重复判断会选择 lower value entry 作为 loser，但没有告诉用户保留的是哪条。

建议增加：

- `duplicate_of`
- `normalized_key`
- `loser_weight`
- `winner_weight`

## 优化原则

1. 默认 audit，危险动作必须显式。
2. quarantine 优先于 delete。
3. findings 要能解释“为什么被判脏”。
4. secret-like 证据必须脱敏。
5. hygiene 规则应成为 remember 写入前校验的基础。
6. 删除必须可预览、可限制、可确认。

## 推荐方案

### 1. 参数解析收敛

新增：

```go
type hygieneToolOptions struct {
	Action          string
	MinSeverity     string
	IncludeInactive bool
	Limit           int
	DryRun          bool
	ConfirmDelete   bool
	IDs             []string
	AllowUnlimited  bool
}
```

统一处理：

- action alias。
- severity allowlist。
- limit 上下限。
- delete 确认。

### 2. limit 边界

建议：

```go
const (
	defaultHygieneLimit = 50
	maxHygieneLimit     = 200
)
```

规则：

- 未传：50。
- `limit < 0`：报错。
- `limit == 0`：只有 `allow_unlimited=true` 时允许。
- `limit > max`：夹到 max 或报错，建议报错更清晰。

### 3. delete 二次确认

行为：

- `action=delete` 且 `confirm_delete != true`：拒绝。
- 输出提示先使用 `audit` 或 `quarantine`。

错误示例：

```text
delete requires confirm_delete=true; run audit first and prefer quarantine
```

### 4. dry-run 计划

`quarantine` 或 `delete` 加 `dry_run=true` 时返回：

```json
{
  "action": "quarantine",
  "dry_run": true,
  "would_quarantine": 3,
  "findings": [...]
}
```

不修改 store。

### 5. finding 结构增强

扩展：

```go
type HygieneIssue struct {
	ID              string
	Path            string
	Category        string
	Tier            string
	Severity        string
	Reason          string
	SuggestedAction string
	Score           float64
	Preview         string
	Evidence        string
	RelatedID       string
	StateKey        string
	StateValue      string
}
```

敏感证据只输出脱敏版本。

### 6. IDs 精确处理

新增 `ids` 参数：

```json
{
  "action": "quarantine",
  "ids": ["abc123", "def456"]
}
```

行为：

- audit 可只展示指定 ids 的 findings。
- quarantine/delete 只处理指定 ids。
- 避免按规则批量误处理。

### 7. restore 动作

新增：

```json
{
  "action": "restore",
  "ids": ["abc123"]
}
```

行为：

- 只恢复带 `hygiene` / `dirty` 标签的 archived entry。
- status 改回 active。
- 可保留 `hygiene-restored` 标签。

## 分阶段实施

### 第一阶段：安全边界

- limit 上下限。
- delete 需要 `confirm_delete=true`。
- 支持 `dry_run` 参数。
- severity allowlist。

### 第二阶段：可解释性

- finding 增加 evidence / related id。
- duplicate 输出 winner。
- state_conflict 输出 conflict pair。

### 第三阶段：恢复和规则复用

- 增加 `ids` 精确处理。
- 增加 `restore`。
- 抽出 hygiene analyzer 给 remember 复用。

## 测试建议

- 默认 action 是 audit。
- 非法 action 报错。
- 非法 severity 报错。
- limit 小于 0 报错。
- limit 超过最大值报错。
- `limit=0` 无 `allow_unlimited` 报错。
- delete 无 `confirm_delete` 报错。
- `dry_run=true` 不修改 store。
- quarantine 会设置 archived 和 hygiene 标签。
- restore 只恢复 hygiene archived entry。
- secret-like evidence 被脱敏。
- duplicate finding 包含 related id。
- state_conflict finding 包含 state_key 和 related id。

## 文档更新

同步更新：

- `docs/tools/memory/memory_hygiene.md`
- 参数表新增 `dry_run`、`confirm_delete`、`ids`、`allow_unlimited`
- delete 风险说明
- restore 行为
- finding evidence 说明

## 风险与边界

- finding 结构扩展会影响 JSON 消费方，但新增字段是兼容变化。
- restore 不能恢复已经物理 delete 的 entry。
- evidence 输出要避免泄漏 secret。
- `ids` 精确处理需要处理 entry 不存在和重复 id。

## 推荐结论

优先补 limit 边界、delete 确认和 dry-run。`memory_hygiene` 是清理工具，第一目标是避免误删和误归档。随后增强 finding 证据和 related id，让用户能判断规则是否误报。
