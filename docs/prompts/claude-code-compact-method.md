# Claude Code Compact 方法分析与 LuckyAgent 落地建议

## 结论

Claude Code 的 `compact`（上下文压缩：把旧会话压成摘要并重建可继续工作的上下文）不是简单的“总结聊天记录”。它更像一次上下文换代：

```text
旧 transcript
  -> 生成 summary
  -> 写入 compact boundary
  -> 后续只读取 boundary 后的消息
  -> 恢复当前任务仍需要的运行时状态
```

LuckyAgent 最值得借鉴的是 `compact boundary`（压缩边界：标记旧历史已经被摘要代表）和 `post-compact restore`（压缩后状态恢复：把文件、计划、技能、工具状态补回上下文），而不是单纯增加一个摘要函数。

## 信息来源

已验证事实：

- 本机 `claude` 指向 `/home/shiokou/.local/share/claude/versions/2.1.198`。
- npm 包 `@anthropic-ai/claude-code` 位于 `/home/shiokou/.nvm/versions/node/v22.22.2/lib/node_modules/@anthropic-ai/claude-code`。
- 当前版本以二进制形式分发，没有可直接阅读的 TypeScript 源码。
- 二进制字符串中可以看到 compact 相关符号：
  - `compact_boundary`
  - `microcompact_boundary`
  - `PreCompact`
  - `PostCompact`
  - `CLAUDE_AUTOCOMPACT_PCT_OVERRIDE`
  - `compact_partial`
  - `compact_full`
  - `compact_progress`
  - `Failed to generate conversation summary`
  - `Tool use is not allowed during compaction`
  - `compaction agent should only produce text summary`

参考材料：

- `tmp/claude code/context-orchestration.md`
- `tmp/claude code/hooks-knowledge.md`

因此，本文把直接可验证的字符串称为“事实”，把函数链路还原称为“推断”。推断基于本地分析文档、二进制符号和 Claude Code 的运行行为。

## 一句话版

Claude Code 的 compact 是“摘要旧世界，划一道边界，再恢复新世界继续工作需要的状态”。

例子：

- 对话前 100 轮里读了很多文件、跑了很多命令。
- compact 后，模型不再看到这 100 轮原文。
- 模型看到的是一段工作摘要、一个 `compact_boundary` 之后的新消息，以及被恢复的文件/技能/计划等附件。

反例：

- 如果只是把 100 轮对话总结成一段文字，但下一轮仍然把 100 轮原文也发给模型，token 没省下来。
- 如果只保留摘要，不恢复当前任务状态，agent 会忘记刚才读过哪些文件、启用了哪个 skill、计划走到哪一步。

## Compact 的核心链路

根据本地文档和二进制符号，完整 compact 流程大致是：

```text
触发 compact
  -> 执行 PreCompact hook
  -> 构造 summaryRequest
  -> fork compaction agent
  -> 禁止 compaction agent 使用工具
  -> 处理 response_length / prompt_too_long retry
  -> 生成 summaryMessages
  -> 写入 compact_boundary
  -> 保留 messagesToKeep
  -> 恢复 attachments / 文件 / skill / plan / agent 状态
  -> 执行 PostCompact hook
  -> 后续每轮只取 compact_boundary 之后的消息
```

其中最重要的是三段：

1. `summaryRequest`：要求模型把旧上下文压成可继续工作的摘要。
2. `compact_boundary`：告诉后续上下文选择器，边界前的原文历史已经被 summary 代表。
3. `post-compact restore`：把摘要无法可靠表达的运行时状态补回来。

## 1. Compact Boundary

`compact_boundary` 是整个机制的核心。

它的作用不是展示给用户，而是给上下文选择器用。后续构造模型输入时，会找到最近一个 boundary，只取它之后的消息。

简化模型：

```text
messages = [
  user: "旧问题 1",
  assistant: "旧回答 1",
  tool: "旧工具结果",
  system: "compact_boundary",
  user/system: "summary of previous conversation",
  user: "compact 后的新问题"
]

messagesForQuery = getMessagesAfterCompactBoundary(messages)
```

这样可以避免两类问题：

- 重复：summary 已经代表旧历史，旧原文不应再次进入模型。
- 污染：旧用户意图不应压过最新用户消息。

LuckyAgent 对应建议：

- 在 `internal/session` 中引入显式 compact marker。
- 在 `internal/agent/context_planner.go` 的历史选择阶段先裁剪到最近 marker 后。
- marker 前的内容只通过 summary 或 durable memory 进入，不再作为原始 session history 进入。

## 2. Compaction Agent

Claude Code 的压缩摘要不是主 agent 顺手做的，而是 fork 出一个 compaction agent（压缩代理：专门负责生成摘要的内部模型调用）。

二进制中能看到两个约束：

```text
Tool use is not allowed during compaction
compaction agent should only produce text summary
```

这说明 compact 阶段不允许继续调用工具。原因很直接：compact 是维护上下文，不应该产生新的文件修改、命令执行或外部副作用。

LuckyAgent 对应建议：

- `CompactConversation` 应使用受限模型调用。
- 禁用所有 tools。
- 输出必须是纯文本 summary。
- 如果 summary 为空或 API 失败，不写入 compact boundary。

## 3. PreCompact 与 PostCompact Hooks

二进制中能看到：

```text
Compaction blocked by PreCompact hook
PreCompact
PostCompact
The conversation summary produced by compaction
```

这说明 Claude Code 的 compact 前后都有 hook（钩子：流程中的可扩展回调）。

`PreCompact` 可以阻止 compact。比如某个插件认为当前状态不能丢，或者需要先保存外部任务状态，就可以 block。

`PostCompact` 可以拿到压缩摘要。比如插件可以记录摘要、补充上下文、更新 UI 或恢复 runtime state。

LuckyAgent 对应建议：

- 如果暂时不做通用 hook，也至少预留内部事件：
  - `BeforeCompact`
  - `AfterCompact`
- `BeforeCompact` 用于收集状态。
- `AfterCompact` 用于恢复状态和记录 trace。

## 4. Auto Compact

`auto compact`（自动压缩：上下文接近阈值时主动压缩）不会等到模型窗口完全满。

本地文档中提到它会预留 summary 输出空间，逻辑类似：

```text
effectiveWindow = modelContextWindow - reservedSummaryTokens
autoThreshold = effectiveWindow - AUTOCOMPACT_BUFFER_TOKENS
```

可见常量和配置包括：

```text
CLAUDE_AUTOCOMPACT_PCT_OVERRIDE
DISABLE_AUTO_COMPACT
DISABLE_COMPACT
```

这说明 Claude Code 把 auto compact 当成一个可配置能力，而不是不可关闭的隐式行为。

LuckyAgent 对应建议：

- 第一阶段先做 manual compact。
- 第二阶段再做 auto compact。
- auto compact 触发条件至少包括：
  - 当前 token 估算超过阈值。
  - 当前不是 compact/session-memory 这类内部调用，避免递归压缩。
  - 最近连续失败次数没有超过熔断阈值。

## 5. Microcompact

`microcompact`（微压缩：只压缩局部大块内容）和 full compact 不是一回事。

二进制中能看到 `microcompact_boundary`。本地文档说明它主要处理工具结果级别的膨胀，例如：

- `Read`
- shell/bash 输出
- `Grep`
- `Glob`
- `WebSearch`
- `WebFetch`
- `Edit`
- `Write`

例子：

```text
旧工具结果：
  Read file A: 30000 tokens
  Read file B: 28000 tokens
  Bash output: 20000 tokens

microcompact 后：
  Read file A: [old tool result cleared; file was read at turn N]
  Read file B: [old tool result cleared; file was read at turn N]
  Bash output: [exit=0; first lines... last lines... full output omitted]
```

它保留“工具曾经发生过”的结构，但清理大块内容。

LuckyAgent 当前已有 `compactToolResultForContext` 和 `summarizeToolResult`，可以进一步升级为：

- 对每个工具结果记录原始大小。
- 超阈值时替换为结构化摘要。
- 必要时保存完整输出 artifact 引用。
- 在 context trace 中标明结果被压缩。

## 6. Partial Compact

二进制中能看到：

```text
compact_partial
message_selector
Nothing to summarize before the selected message.
Nothing to summarize after the selected message.
messagesKept
```

这说明 Claude Code 支持 partial compact（局部压缩：只压缩选中范围之前或之后的消息）。

用途：

- 保留当前关键调试段。
- 只压缩更早的背景讨论。
- 或者只清理某段无关探索。

LuckyAgent 初期不必实现 partial compact。等 manual compact 稳定后，可以支持：

```text
lh session compact --before <message_id>
lh session compact --after <message_id>
lh session compact --range <start_id>..<end_id>
```

## 7. Post-Compact Restore

这是 Claude Code compact 最值得借鉴的部分。

compact 后，summary 可以保留事实和决策，但它很难完整保留运行时状态。Claude Code 会补回一批附件和状态：

- 最近读过的文件。
- invoked skills。
- plan mode。
- async agent 状态。
- deferred tools。
- agent listing。
- MCP instructions。
- hook results。

例子：

```text
summary:
  用户要求修复 memory recall 的 stale result 问题，已定位到 internal/agent/memory_gate.go。

restore attachments:
  - 当前计划：先补测试，再修 gate，再跑 go test ./internal/agent
  - 已读文件：memory_gate.go 摘要
  - 已启用 skill：refactor-go-systems 摘要
  - 最近工具结果：失败测试的关键错误
```

如果没有 restore，模型只知道“曾经定位到 memory_gate.go”，但不知道当前计划和已读上下文，很容易重复探索。

LuckyAgent 对应恢复项：

- 当前任务计划。
- 最新用户消息。
- 最近工具结果摘要。
- 已读文件摘要。
- 已启用 skill。
- 活跃 subagent/delegate 状态。
- 当前 AGENTS.md/项目规则。
- memory/RAG 检索源摘要。

## LuckyAgent 最小实现方案

建议先做一个可测试的 manual compact，不要一开始复制完整 Claude Code。

### Phase 1: Session Compact Marker

目标：让 session 能表达“这里发生过 compact”。

建议结构：

```go
type CompactMetadata struct {
    ID              string
    Trigger         string // manual, auto, partial
    Summary         string
    CreatedAt       time.Time
    PreTokenEstimate int
    PostTokenEstimate int
}
```

session message 中增加一种 marker：

```text
role=system
name=compact_boundary
content=<summary or metadata ref>
```

### Phase 2: Context Planner Respect Boundary

目标：`contextPlanner` 构造历史时只取最近 compact boundary 之后的原文消息。

逻辑：

```go
func messagesAfterCompactBoundary(messages []provider.Message) []provider.Message {
    last := -1
    for i, msg := range messages {
        if msg.Role == "system" && msg.Name == "compact_boundary" {
            last = i
        }
    }
    if last < 0 {
        return messages
    }
    return messages[last+1:]
}
```

注意：summary 本身要以单独上下文块注入，不能被裁掉。

### Phase 3: Compact Summary Call

目标：增加一个不带工具的压缩调用。

压缩 prompt 应要求保留：

- 当前用户目标。
- 已完成事项。
- 未完成事项。
- 关键文件/函数/命令。
- 关键错误和测试结果。
- 用户明确偏好和约束。
- 不确定事实。

压缩 prompt 应禁止：

- 编造未发生的命令。
- 把 transient debug 当 durable memory。
- 输出泛泛建议。
- 调用工具。

### Phase 4: Post-Compact Restore

目标：压缩后恢复继续工作需要的附件。

建议最小集合：

```go
type PostCompactAttachment struct {
    Kind     string // plan, file_state, skill, tool_result, subagent
    Source   string
    Content  string
    Priority int
    Tokens   int
}
```

初期只恢复：

- plan/todo。
- 最近 3 到 5 个关键工具结果摘要。
- 最近读过或改过的文件路径。
- 当前已启用 skill 的 summary。

### Phase 5: Context Trace

目标：每次 compact 后可以解释发生了什么。

trace 应包含：

- compact trigger。
- compact 前 token 估算。
- compact 后 token 估算。
- summary 长度。
- dropped message count。
- restored attachment count。
- 是否触发 hook 或失败重试。

这可以直接接入现有 `/api/v1/context` 或 `agent.context_debug`。

## 不建议直接照搬的部分

不要第一版就做完整 auto compact、partial compact、context collapse、cached microcompact。原因是这些能力依赖稳定的基础边界和可观测性。

更稳妥的顺序是：

```text
manual compact
  -> boundary 生效
  -> post-compact restore 生效
  -> trace 可解释
  -> microcompact 工具结果
  -> auto compact
  -> partial compact
```

## 对 LA 代码位置的映射

主要落点：

- `internal/session`：保存 compact boundary 和 summary metadata。
- `internal/agent/context_planner.go`：读取 boundary，构造 post-compact context。
- `internal/agent/loop.go`：加入 manual compact 调用入口，禁止工具调用。
- `internal/agent/system_prompt.go`：明确 compact summary 的权威级别，避免覆盖当前用户消息。
- `internal/tool`：升级工具结果 microcompact。
- `internal/server`：把 compact trace 暴露到 `/api/v1/context`。

已有基础：

- `compactToolResultForContext` 已经能压缩工具输出。
- `summarizeConversationRangeWithLLM` 已经有 LLM 摘要能力。
- `midTerm` 和 `shortTerm` 已经能保存摘要类上下文。
- `contextPlanner` 已经有预算和 history selection。

缺口：

- 没有显式 compact boundary。
- summary 与原始历史之间缺少“替代关系”。
- compact 后缺少系统化 runtime state restore。
- context debug 还不能完整解释每块上下文为何被保留或丢弃。

## 推荐的第一版验收标准

第一版完成后，应满足：

1. 手动 compact 后，旧原文历史不再进入模型上下文。
2. compact summary 会进入下一轮上下文。
3. 最新用户消息优先级高于 compact summary。
4. 当前计划、最近文件、关键工具结果能被恢复。
5. compact 失败不会破坏 session。
6. context debug 能看到 compact 前后 token 和 dropped messages。

最小测试用例：

```text
构造 30 条历史消息
插入 compact boundary + summary
再追加 2 条新消息
调用 contextPlanner.BuildInput
断言：
  - boundary 前原文未出现
  - summary 出现
  - boundary 后新消息出现
  - 当前 user message 在末尾
```

## 总结

Claude Code compact 的关键不是“总结得多好”，而是它把压缩做成了一个可恢复、可追踪、可继续执行的上下文生命周期。

LuckyAgent 如果要借鉴，应优先实现：

```text
compact boundary
summary-only compaction call
post-compact restore
context trace
```

这四个能力形成闭环后，再做 auto compact 和 partial compact 才有稳定基础。
