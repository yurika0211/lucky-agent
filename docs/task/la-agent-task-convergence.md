# LuckyAgent Agent 任务收束机制

## 结论

LuckyAgent 的任务收束机制不是单一的“达到最大轮数就停”。它由三层共同完成：

```text
Prompt 约束
  -> Loop 运行时收束
  -> 工具/记忆/上下文守卫
```

一句话版：模型被提示“完成就停”，主循环负责“没有工具调用就结束、有工具调用就观察后再决策”，守卫负责“发现重复、超时、空输出、证据足够或必要检查未完成时，强制纠偏或停止”。

例子：

- 用户问“这个目录里有哪些文件”，LA 会通过 simple task tuning 把循环压短，通常 1 到 3 轮内完成。
- 用户让搜索资料，连续拿到足够搜索证据后，LA 会强制进入 synthesis（综合回答：基于已有证据输出结论），而不是继续搜索。
- 模型反复调用同一个工具，LA 会提前停止并返回最近工具输出，避免卡到超时。

## 核心代码位置

主要实现：

- `internal/agent/system_prompt.go`
- `internal/agent/loop.go`
- `internal/agent/loop_execution.go`
- `internal/agent/memory_gate.go`
- `internal/agent/tool_execution_guard.go`
- `internal/agent/tool_intent_gating.go`
- `internal/agent/agent.go`

关键结构：

- `LoopConfig`：单次 agent loop 的预算和边界。
- `loopRuntimeState`：非流式 loop 的临时收束状态。
- `streamConvergenceState`：流式 loop 的临时收束状态。
- `memoryToolGate`：最终回答前的必要记忆/实时信息检查门。
- `toolExecutionGuard`：根据用户约束阻断不允许的工具副作用。

## 1. Prompt 层收束

Prompt 层是第一道软约束。

`system_prompt.go` 的核心行为要求包括：

```text
Your goal is to help the user reach correct, task-complete outcomes with the smallest necessary set of actions.
Stop once the task is complete and further work would not materially improve the result.
Do not repeat the same tool call unless the previous result was incomplete, stale, or contradicted.
If one tool result is already enough to answer the user, stop.
```

作用：

- 告诉模型不要为了“看起来在干活”而继续调用工具。
- 告诉模型每次工具结果回来后要重新判断是否已经足够。
- 告诉模型不要重复相同工具调用。

例子：

- `file_read` 已经读到了目标配置项，模型应直接解释结果。
- 不应该继续 `file_list`、`terminal pwd`、`rg` 只是为了多拿证据。

局限：

- Prompt 是软约束，模型可能不遵守。
- 所以后面还有 loop 层和 guard 层做硬收束。

## 2. LoopConfig 预算边界

`LoopConfig` 定义一次任务最多能跑多久、跑几轮、重复多少次。

默认值：

```go
LoopConfig{
    MaxIterations:          10,
    Timeout:                60 * time.Second,
    AutoApprove:            false,
    RepeatToolCallLimit:    3,
    ToolOnlyIterationLimit: 3,
    DuplicateFetchLimit:    1,
}
```

含义：

| 字段 | 作用 |
| --- | --- |
| `MaxIterations` | 最多模型循环轮数。 |
| `Timeout` | 每轮模型调用超时时间。 |
| `RepeatToolCallLimit` | 相同工具签名重复上限。 |
| `ToolOnlyIterationLimit` | 连续“只调用工具、不输出内容”的轮次上限。 |
| `DuplicateFetchLimit` | 同一 URL 或目标重复抓取上限。 |
| `DisabledTools` | 本轮从模型可见工具中隐藏的工具。 |
| `Ephemeral` | 临时执行，不写 final answer 等外部持久化上下文。 |

`sanitizeLoopConfig` 还会做硬边界：

- `MaxIterations <= 0` 时回退到 `10`。
- `MaxIterations` 最大不超过 `300`。
- `Timeout <= 0` 时回退到 `60s`。
- `Timeout` 最大不超过 `10m`。
- 重复工具、纯工具轮、重复抓取限制为空时回退默认值。

例子：

- 配置把 `max_iterations` 设成 `10000`，运行时也会被压到 `300`。
- 工具卡住时，单轮会被 `Timeout` 截断，不会无限等模型。

## 3. 主循环状态机

非流式主入口是 `RunLoopWithSessionInput`。

状态可以理解为：

```text
Reason
  -> Act
  -> Observe
  -> Done
```

实际链路：

```text
构造上下文 messages
  -> 构造可见工具 callOpts
  -> for i < MaxIterations
      -> 调模型
      -> 如果有 tool_calls：执行工具，写回 tool result，继续下一轮
      -> 如果无 tool_calls：处理直接回复，finalize
  -> 超过最大轮数：返回 max iterations reached
```

收束点：

- 模型无工具调用且有正常内容：直接 `finalize`。
- 模型有工具调用：执行工具后继续下一轮，让模型基于观察结果决定是否结束。
- 达到最大轮数：返回错误和不完整提示。

例子：

```text
User: 这个文件里配置项 X 是什么？
Round 1: 模型调用 file_read
Round 2: 模型根据 tool result 回答
Done
```

反例：

```text
Round 1: file_read
Round 2: file_read 同一文件同一参数
Round 3: file_read 同一文件同一参数
```

这种情况会被重复工具调用机制拦住。

## 4. 直接回复收束

`processDirectResponse` 处理模型没有返回工具调用的情况。

它有三种收束/恢复路径：

### 4.1 正常完成

如果 `resp.Content` 非空，且 `FinishReason` 不是 `length`，直接视为最终回答。

```text
model answer
  -> finalize
  -> StateDone
```

### 4.2 空回复恢复

如果模型返回空内容，会最多重试 `maxEmptyResponseRetries = 2` 次。

恢复提示：

```text
Your last response was empty. Please provide a direct, complete answer to my previous request. Avoid tool calls unless required.
```

超过重试次数后：

- 如果已有续写累计内容，返回累计内容。
- 否则返回 `I couldn't produce a complete answer this round. Please retry.`

### 4.3 length 截断续写

如果 `FinishReason == "length"`，说明模型输出被截断。

LA 会累计已有输出，并最多续写 `maxLengthContinuationRetries = 3` 次。

恢复提示：

```text
Continue exactly from where you stopped. Do not repeat previous content.
```

超过续写次数后，会返回已有内容并加：

```text
[Output may be truncated after multiple continuation attempts.]
```

作用：

- 防止长回答因为一次截断就丢失。
- 也防止模型无限续写。

## 5. 工具调用后收束

`processToolCallBatch` 处理模型返回的一批工具调用。

流程：

```text
tool_calls
  -> 记录 assistant(tool_calls)
  -> 执行工具
  -> 压缩工具结果
  -> 记录 tool messages
  -> fitContextWindow
  -> 必要时追加 synthesis prompt
  -> 进入下一轮
```

收束相关动作：

- 工具结果会通过 `compactToolResultForContext` 压缩，避免上下文膨胀。
- URL 类目标会通过 `DuplicateFetchLimit` 去重。
- 搜索证据足够时会强制进入综合回答。
- 重复工具循环会提前终止。

## 6. 重复工具调用检测

重复检测基于 `toolCallSignature`。

签名格式：

```text
tool_name + "|" + canonical_json_arguments
```

其中 `canonicalToolArguments` 会把 JSON 参数规范化，避免同样参数因为字段顺序不同而被误判为不同调用。

触发条件：

```text
allRepeated && assistantContent == ""
```

或者：

```text
consecutiveToolOnlyIters >= ToolOnlyIterationLimit
```

触发后：

- 如果已经有搜索证据，优先强制进入搜索综合。
- 否则返回重复工具循环中止消息，带最近工具输出摘要。

返回格式类似：

```text
Detected repeated tool-call loop and stopped early to avoid timeout.
Latest tool outputs:
- file_read: ...
```

例子：

- 模型连续三轮调用同一个 `web_fetch(url=A)`，且没有输出任何解释。
- LA 不再继续执行，而是要求综合或直接中止。

## 7. 连续纯工具轮收束

`consecutiveToolOnlyIters` 记录模型连续多少轮“只调用工具，没有 assistant 文本”。

为什么需要它：

- 有些循环不是完全相同工具，但模型一直只查不答。
- 例如 `web_search -> web_fetch -> web_search -> web_fetch`，每次参数不同，但任务已经有足够证据。

触发后：

- 如果 `successfulSearchEvidence > 0`，先进入 `forceSearchSynthesis`。
- 如果没有足够证据，停止并返回最近工具输出。

例子：

```text
Round 1: web_search，无正文
Round 2: web_fetch，无正文
Round 3: web_search，无正文
```

达到 `ToolOnlyIterationLimit` 后，LA 会打断继续搜索倾向。

## 8. 搜索综合强制收束

搜索类工具结果由 `isUsefulSearchEvidence` 判断是否有效。

有效工具包括：

- `web_search`
- `web_fetch`
- `opencli`

无效结果包括：

- 空输出。
- `Error:` 开头。
- `no results found`。
- `all search sources failed`。

触发规则：

```go
successfulSearchEvidenceCount >= 3 && consecutiveToolOnlyIters >= 1
```

或者：

```go
successfulSearchEvidenceCount >= 2 && consecutiveToolOnlyIters >= 2
```

触发后会设置 `forceSearchSynthesis = true`，并追加提示：

```text
You now have enough search evidence from previous tool results.
Synthesize a direct, source-aware answer now.
Do not call any more tools unless a critical factual gap remains unresolved.
```

同时 `prepareLoopCallOptions` 会在 `forceSearchSynthesis` 时隐藏所有工具：

```go
opts.Tools = nil
opts.ToolChoice = "none"
```

这是一种硬收束：模型下一轮不能再调用工具，只能回答。

## 9. 重复抓取去重

`executeToolMaybeDedup` 防止重复抓同一个目标。

适用目标：

- `web_fetch`
- `opencli` 中带 URL 的调用。

逻辑：

```text
如果同一 normalized URL 抓取次数超过 DuplicateFetchLimit
  -> 如果有上次结果，返回缓存内容
  -> 否则提示复用 earlier fetched content
```

例子：

```text
web_fetch https://example.com/a#section1
web_fetch https://example.com/a#section2
```

归一化时会去掉 fragment，并把 host 小写，因此这两次可能视为同一目标，避免重复消耗。

## 10. Memory Gate 最终回答门

`memoryToolGate` 是最终回答前的强制检查机制。

它解决的问题：

- 用户问题需要实时或记忆相关证据。
- 模型可能想直接回答。
- 但 LA 判断必须先调用某些工具，比如 `current_time`、`web_search`。

流程：

```text
buildMemoryToolGate
  -> memory.Route(query) 判断 required tools
  -> 如果 final 前还有 unmet required tools
      -> 自动执行 required tools
      -> 追加 synthesisPrompt
      -> 继续一轮模型综合
```

如果最终仍未完成必要检查，会返回：

```text
Memory gate blocked the final answer because required tools were not completed: ...
```

例子：

- 用户问“今天上海天气适合跑步吗”，如果路由要求当前时间/外部搜索，LA 不应只根据记忆回答。
- gate 会先补必要工具，再让模型综合。

## 11. 工具执行守卫

`toolExecutionGuard` 根据用户显式约束阻断工具副作用。

它不决定任务完成，但能防止任务偏离用户边界。

典型规则：

- 用户说“只读”，阻断 `file_write`、`file_patch`、`terminal` 中的写操作。
- 用户说“不要删”，阻断 `file_delete` 或 shell 删除命令。
- 用户说“不要 push”，阻断 git push。
- 用户说“不要写入记忆”，阻断 `remember`。
- 用户说“只查看任务列表”，阻断新增/修改 delegate/autonomy/cron。

被阻断的工具不会执行，而是把阻断原因作为工具结果返回给模型：

```text
Blocked by tool execution guard: the user requested a read-only/no-file-modification task.
The file_patch tool call was not executed.
```

这样模型下一轮可以基于“工具被阻断”收束回答，而不是继续尝试同类副作用。

## 12. 意图工具门控

`tool_intent_gating.go` 是模型可见工具层面的预收束。

当 `LH_TOOL_INTENT_GATING` 启用时，LA 会根据用户意图隐藏无关工具。

例子：

- 用户只是问概念解释，工具列表可以为空，`ToolChoice = none`。
- 用户要求读文件，只开放 `file_read`、`file_list` 和必要的 `terminal`。
- 用户只查看 delegate 任务，不开放 `delegate_task`。

作用：

- 减少模型乱用工具的机会。
- 让简单任务更快结束。
- 降低误触发写操作的风险。

## 13. 简单本地检查任务调参

`IsSimpleLocalInspectionTask` 会识别简单本地检查任务。

匹配示例：

- `list files`
- `check workspace files`
- `查看目录`
- `确认路径`
- `是否可以发送这个文件`

命中后，`applySimpleTaskLoopTuning` 会收紧参数：

```text
MaxIterations: 默认最多 3
Timeout: 默认最多 25s
RepeatToolCallLimit: 默认最多 2
ToolOnlyIterationLimit: 默认最多 2
```

这类任务本来就应该短平快。

例子：

- 用户问“这个目录下有什么文件”，最多列目录和读少量文件，不应该跑 10 轮。

## 14. 流式路径的收束

流式对话使用 `streamConvergenceState`。

它复用了非流式的核心思想：

- 重复工具签名检测。
- 连续纯工具轮检测。
- 搜索综合强制。
- duplicate URL 去重。
- memory gate。
- length 续写恢复。
- final answer 引用工具结果。

区别：

- 流式路径要先收集 `ToolCallDeltas`。
- 如果检测到重复循环，会通过 `finalizeStreamWithState` 直接输出停止消息。
- 如果触发 memory gate，会执行 required tools 后递归进入下一轮。

## 15. Finalize 收尾动作

任务完成后，`finalize` 做统一收尾：

```text
SanitizeToolProtocolOutput
  -> appendNaturalCitations
  -> result.State = StateDone
  -> session 添加 assistant final message
  -> 可选 index final answer
  -> 可选 save final answer document
  -> session.Save
```

注意：

- Final answers 默认不写入 RAG，除非 `autoIndexFinalAnswersEnabled()`。
- `Ephemeral` 任务不会写 final answer artifact 等外部持久化上下文。

这保证“完成”不仅是输出文本，还包括会话保存和必要的最终记录。

## 当前机制的收束链条

完整收束链可以概括为：

```text
用户输入
  -> simple task tuning 收紧预算
  -> intent gating 隐藏无关工具
  -> tool execution guard 记录用户禁止事项
  -> context planner 构造消息
  -> 模型一轮推理
      -> 无工具：空输出/截断恢复后 finalize
      -> 有工具：执行、压缩、去重、记录
  -> 重复工具/纯工具轮检测
  -> 搜索证据足够时 force synthesis
  -> memory gate 补必要检查
  -> finalize 或 max iterations
```

## 与 TaskOptimizer 的边界

TaskOptimizer（任务优化器：执行前推荐路径、预算和验证策略）不应该替代当前收束链条。

两者分工：

| 层 | 负责什么 | 不负责什么 |
| --- | --- | --- |
| 当前收束机制 | 执行中防循环、防越界、强制综合、最终保存。 | 不负责从多个候选路径里选最优策略。 |
| TaskOptimizer | 执行前推荐 path、budget、tool policy、verifier。 | 不直接执行工具，不覆盖用户硬约束。 |
| Outcome recorder | 执行后记录 stop reason、actual path、验证结果。 | 不在样本不足时让 MDP 主导执行。 |

例子：

- 用户问“解释 Dijkstra”：TaskOptimizer 最多推荐 `answer_direct -> final`，真正停止仍由“无工具调用后 finalize”完成。
- 用户要求“修复测试”：TaskOptimizer 可以推荐 `inspect -> edit -> run_checks -> final`，但重复工具检测、timeout、tool guard 仍由当前 loop 负责。
- 用户说“只读”：TaskOptimizer 应把 `edit_files` 标为不可达，tool execution guard 仍作为执行时最后防线。

推荐接入方式：

```text
TaskOptimizer trace
  -> LoopConfig hint
  -> Agent Loop execution
  -> convergence trace
  -> actual_path + StopReason
  -> outcome feedback
```

这意味着第一版只观察，不接管；第二版只接管低风险配置，例如更短的 `MaxIterations` 或只读任务隐藏写工具。

## 结构化观测建议

当前文档描述了很多收束点，但它们还需要统一成机器可读事件。

建议增加三类字段：

```go
type ConvergenceTrace struct {
    StopReason          StopReason
    ActualPath          []string
    ToolCallCount       int
    RepeatedToolCount   int
    ToolOnlyIterations  int
    SearchEvidenceCount int
    ForceSynthesis      bool
    MemoryGateRequired  []string
    GuardBlocks         []string
}
```

用途：

- `StopReason` 解释为什么停。
- `ActualPath` 给 TaskOptimizer 做 recommended path 对比。
- `GuardBlocks` 记录用户约束是否被正确执行。
- `ForceSynthesis` 判断搜索任务是否及时收束。

例子：

```json
{
  "stop_reason": "search_synthesis",
  "actual_path": ["search_external", "search_external", "final"],
  "tool_call_count": 2,
  "search_evidence_count": 2,
  "force_synthesis": true
}
```

这类 trace 不应该默认塞进模型上下文；它适合进入 debug、task status、benchmark replay 和 MDP observation。

## 风险与缺口

### 1. 收束原因还不够结构化

现在 `LoopResult` 只有 `State`，没有明确的 `StopReason`。

建议增加：

```go
type StopReason string

const (
    StopFinalAnswer       StopReason = "final_answer"
    StopMaxIterations     StopReason = "max_iterations"
    StopRepeatedToolLoop  StopReason = "repeated_tool_loop"
    StopSearchSynthesis   StopReason = "search_synthesis"
    StopMemoryGateBlocked StopReason = "memory_gate_blocked"
    StopTimeout           StopReason = "timeout"
)
```

好处：

- 日志更清楚。
- API 调用方能区分成功完成和保护性停止。
- benchmark 可以统计哪类任务最常收束失败。

### 2. 工具循环停止后缺少二次综合

当前重复工具循环可能直接返回“检测到循环”的消息。

更理想的行为：

```text
重复工具循环
  -> 如果有任何有效工具结果
      -> 禁用工具
      -> 让模型基于已有结果输出 final
  -> 如果没有有效结果
      -> 返回保护性停止消息
```

这样用户更容易得到可用答案。

### 3. Memory gate 与 max iterations 的协调还可加强

当前如果 memory gate 补查后没有剩余轮次，可能无法产出最终综合。

建议：

- memory gate 触发时预留至少 1 轮 synthesis。
- 如果已到最后一轮，直接禁用工具并要求综合。

### 4. 收束 trace 可以进入 context debug

建议在 context debug 或 loop debug 中记录：

- 当前 iteration。
- 工具调用签名计数。
- consecutive tool-only iterations。
- successful search evidence count。
- force synthesis 是否触发。
- memory gate required/attempted/unavailable。
- stop reason。

## 推荐验收测试

### 1. 无工具直接完成

输入概念解释类问题。

期望：

- `ToolCalls == 0`
- `State == StateDone`
- 1 轮完成。

### 2. 单工具后完成

输入“读取某文件并总结”。

期望：

- 第一轮 `file_read`
- 第二轮 final answer
- 不继续重复读同一文件。

### 3. 重复工具循环停止

模拟 provider 连续返回相同 tool call。

期望：

- 达到 `RepeatToolCallLimit` 后停止。
- 返回包含 `Detected repeated tool-call loop` 的结果。

### 4. 搜索证据足够后强制综合

模拟连续有效 `web_search` / `web_fetch` 结果。

期望：

- `forceSearchSynthesis == true`
- 下一轮 `ToolChoice == none`
- 输出最终回答。

### 5. Memory gate 阻止未经检查的最终回答

构造 memory route 要求 `current_time` 或 `web_search`。

期望：

- 模型直接回答前，gate 自动补工具。
- 综合 prompt 被追加。
- 如果工具不可用，最终回答明确说明未能检查。

## 总结

LuckyAgent 现在已经有一套比较完整的任务收束基础：

- Prompt 约束让模型倾向于少工具、完成即停。
- LoopConfig 给出硬预算。
- 重复工具检测防止循环。
- 连续纯工具轮检测防止只查不答。
- 搜索综合强制让模型在证据足够时停止搜索。
- Memory gate 防止遗漏必要实时/记忆检查。
- Tool execution guard 防止任务越界。

下一步最值得补的是结构化 `StopReason` 和收束 trace。这样 LA 不仅能停下来，还能解释“为什么停在这里”。
