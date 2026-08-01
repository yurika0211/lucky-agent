# opt-prompt-compact-01

## 目标

优化 LuckyAgent 的 manual compact 第一版，让它从“能写入 compact boundary 并裁剪旧历史”升级为“可恢复、可观测、可自动触发、可安全回滚”的上下文生命周期能力。

本方案承接 `docs/prompts/claude-code-compact-method.md`，聚焦代码落地后的下一阶段优化，不重复分析 Claude Code 原理。

## 当前状态

已具备的能力：

- `internal/session` 支持 `system/name=compact_boundary` marker。
- `Agent.CompactSession` 可以用无工具模型调用生成 compact summary。
- `contextPlanner` 会读取最近 boundary，只保留 boundary 后的原始历史。
- REPL 支持 `/session compact`。
- 已有基础测试覆盖 boundary split 和 context planner 裁剪。

当前定位：

- 这是 manual compact。
- compact summary 是旧历史的替代摘要，不是 durable memory。
- 最新用户消息和 boundary 后消息优先级高于 compact summary。

## 主要问题

### 1. Summary 质量不可控

当前 summary prompt 虽然规定了固定标题，但缺少结构化校验。

风险：

- 模型可能输出泛泛总结。
- 可能漏掉关键文件、命令、测试结果。
- 可能把推测内容写成事实。
- 空标题或标题缺失时仍会写入 boundary。

### 2. 缺少 post-compact restore

当前只有 summary，没有恢复附件。

compact 后模型可能缺少：

- 当前计划状态。
- 最近失败测试的关键错误。
- 最近读过或修改过的文件列表。
- 已启用 skill 或工具上下文。
- 当前 shell cwd/env。

### 3. Trace 不够完整

`CompactMetadata` 已有 token 和 dropped message 字段，但还没有统一暴露。

需要能回答：

- 为什么触发 compact。
- compact 前后 token 估算是多少。
- 哪些消息被 boundary 替代。
- summary 是否来自 LLM 还是 fallback。
- 是否恢复了附件。

### 4. CLI 入口仍偏窄

目前只有 REPL `/session compact`。

缺口：

- 非交互 `lh chat "/session compact"` 只会创建新会话，不能 compact 已有会话。
- HTTP server 没有 compact API。
- dashboard/TUI 看不到 compact trace。

### 5. 没有失败保护和回滚

当前失败时不会写 boundary，这是正确的。

但仍缺少：

- summary 质量过低时拒绝写入。
- compact 后上下文 token 没有下降时提醒。
- 手动撤销最近 boundary。
- compact 前 session 快照。

### 6. Auto compact 触发条件未实现

长会话仍需要用户手动执行 `/session compact`。

自动触发要避免：

- 内部 compact 调用递归触发 compact。
- 当前任务正在等待工具调用时压缩。
- 连续 compact 失败造成循环。
- 最新用户 turn 被错误压缩。

## 优化原则

- compact 是上下文维护，不应产生工具副作用。
- boundary 前原文历史必须只通过 summary 或 durable memory 进入上下文。
- summary 永远低于最新用户消息、当前工具结果和工作区事实。
- compact 失败不得破坏 session。
- 第一优先级是可解释和可回滚，其次才是自动化。

## 推荐方案

### Phase 1：Summary 质量门控

新增 summary validation：

```go
type CompactSummaryValidation struct {
    Valid       bool
    Missing    []string
    Reason     string
    CharCount  int
    HasFileRef bool
    HasNextStep bool
}
```

最小规则：

- summary 非空。
- 至少包含 `Current user goal` 或 `当前用户目标`。
- 至少包含 `Pending work` 或 `未完成事项`。
- 长度不少于 120 字符，且不超过配置上限。
- 如果 compact 范围内出现 tool/error/test/path，summary 至少保留一个对应线索。

失败行为：

- 不写入 `compact_boundary`。
- 返回明确错误。
- session 不变。

验收标准：

- 空 summary 不写 boundary。
- 泛泛 summary 不写 boundary。
- 合格 summary 写 boundary。
- 测试能断言 session message count 未被失败 compact 改变。

### Phase 2：Post-Compact Restore 附件

新增内部结构：

```go
type PostCompactAttachment struct {
    Kind     string
    Source   string
    Content  string
    Priority int
    Tokens   int
}
```

第一版恢复项：

- 最近 5 个工具结果摘要。
- 最近 10 个出现过的文件路径。
- 当前 shell cwd/env。
- 最近一条用户消息和其后的 assistant/tool 消息。

实现位置：

- `internal/agent/compact.go`：抽取附件。
- `contextPlanner.compactBoundaryContext`：在 summary 后注入附件块。
- `CompactMetadata` 只保存 metadata，不保存大附件正文；附件可由 boundary 后消息重建，或保存短文本。

输出格式建议：

```text
[Post-Compact Restore]
- shell cwd: ...
- recent files: internal/agent/context_planner.go, ...
- recent tool evidence: go test ./internal/agent failed: ...
```

验收标准：

- compact 后上下文含 summary。
- compact 后上下文含 restore block。
- restore block 不包含大段原始工具输出。
- 最新用户 turn 仍保留在上下文尾部。

### Phase 3：Context Trace

扩展 trace 数据：

```go
type CompactTrace struct {
    BoundaryID          string
    Trigger             string
    CreatedAt           time.Time
    PreTokenEstimate    int
    PostTokenEstimate   int
    DroppedMessages     int
    SummaryTokens       int
    RestoredAttachments int
    SummarySource       string // llm, fallback
}
```

建议先放在 session metadata 中，并在 `/api/v1/context` 输出最近 boundary trace。

验收标准：

- `agent.context_debug=true` 时日志包含 compact trace。
- `/api/v1/context` 能看到最近 boundary 的 dropped messages 和 token delta。
- REPL `/session compact` 打印 trace。

### Phase 4：Fallback Summary

当 LLM summary 失败时，不直接 compact，但可以提供 fallback 预览。

两种模式：

- 默认：LLM 失败，不写 boundary。
- `force_local=true`：使用本地摘要并写 boundary。

本地摘要来源：

- `summarizeConversationRange`。
- `summarizeToolResult`。
- 最近文件路径扫描。

验收标准：

- provider 超时不会写 boundary。
- `force_local` 可以写入明确标注 `SummarySource=local` 的 boundary。

### Phase 5：HTTP 和 CLI Session Compact

新增非交互入口：

```bash
lh session compact <session-id>
lh session compact <session-id> --dry-run
lh session compact <session-id> --force-local
```

HTTP API：

```text
POST /api/v1/sessions/{id}/compact
GET  /api/v1/sessions/{id}/compact/latest
```

响应字段：

- `boundary_id`
- `summary`
- `pre_token_estimate`
- `post_token_estimate`
- `dropped_messages`
- `restored_attachments`
- `dry_run`

验收标准：

- 可以 compact 指定历史 session。
- dry-run 不写 session。
- API 失败不会改变 session。

### Phase 6：Auto Compact

配置建议：

```json
{
  "context": {
    "auto_compact": true,
    "auto_compact_threshold": 0.82,
    "auto_compact_min_messages": 24,
    "auto_compact_cooldown_turns": 8,
    "auto_compact_reserved_summary_tokens": 1200
  }
}
```

触发条件：

- 当前估算 token 超过阈值。
- 距离上次 compact 已超过 cooldown。
- 当前不是 compact 内部调用。
- 当前轮没有未完成工具调用。
- 最近连续 compact 失败次数低于阈值。

验收标准：

- 长会话自动 compact。
- 短会话不会 compact。
- compact 内部调用不会递归 compact。
- 连续失败后熔断。

### Phase 7：Undo Compact

支持撤销最近 boundary：

```bash
lh session compact undo <session-id>
```

语义：

- 删除最后一个 `compact_boundary` marker。
- 不删除原始历史消息。
- 如果 boundary 后已有新消息，默认拒绝，除非 `--keep-after`。

验收标准：

- 无新消息时可以撤销。
- 有新消息时默认拒绝。
- 撤销后 context planner 能重新看到旧 raw history。

## 测试建议

### Session 层

- 多个 boundary 时只识别最近一个。
- metadata JSON 解析失败时 fallback 为纯文本 summary。
- compact 失败不改变 message count。
- undo 删除最近 boundary。

### Agent 层

- LLM 成功生成 summary 后写 boundary。
- LLM 失败时不写 boundary。
- summary validation 失败时不写 boundary。
- post-compact restore 能提取工具结果和文件路径。

### Context Planner

- boundary 前 raw history 不进入上下文。
- summary 进入上下文。
- restore block 进入上下文。
- boundary 后最新 user/assistant/tool 消息进入上下文。
- latest user turn 不会被 intent filter 丢掉。

### CLI/API

- `/session compact` 输出 boundary 和 token delta。
- `lh session compact <id> --dry-run` 不写文件。
- `POST /api/v1/sessions/{id}/compact` 返回 trace。

## 风险与边界

- compact summary 不能替代 memory；不要把会话临时状态自动写入 durable memory。
- restore block 要短，不能把大工具输出重新塞回上下文。
- auto compact 必须有熔断，否则 provider 故障时会重复尝试。
- undo compact 只处理 boundary marker，不应修改用户原始消息。
- partial compact 暂不建议做，等 full manual compact 稳定后再加。

## 推荐结论

下一阶段优先顺序：

1. Summary validation。
2. Post-compact restore。
3. Context trace/API 暴露。
4. dry-run 和指定 session compact。
5. auto compact。
6. undo 和 partial compact。

这样能先把 compact 从“可用”提升到“可信”，再逐步自动化。
