# LA Agent Loop

本文说明 LuckyAgent 当前代码库里的 Agent Loop 执行逻辑，重点覆盖入口、上下文构建、Reason/Act/Observe 循环、工具执行保护、memory gate、收敛条件、会话持久化和流式路径。

## 结论

LuckyAgent 的 agent loop 主入口在 `internal/agent/loop.go`：

- `RunLoop`
- `RunLoopWithSession`
- `RunLoopWithSessionInput`

普通聊天入口 `ChatWithSessionInput` 会优先调用 `RunLoopWithSessionInput`，失败后才回退到简单聊天。流式入口 `ChatWithSessionStreamInputWithLoopConfig` 走 `streamNative` 或 `streamSimulated`，但核心收敛策略与非流式 loop 基本一致。

核心流程可以概括为：

```text
用户输入
  -> Normalize
  -> 模型路由
  -> LoopConfig 校验和裁剪
  -> 意图级工具 gating
  -> 构建上下文
  -> 构建 model-visible tools
  -> 迭代执行 Reason / Act / Observe
  -> memory gate 补检
  -> finalize
  -> 写入 session / final answer 文档 / 可选 RAG 索引
```

Agent Loop 不是单纯的“发一次 prompt 给模型”。它是一个带状态的执行循环，负责把模型回复、工具调用、工具结果、上下文压缩、会话消息和收敛保护串起来。

## 核心入口

### 非流式入口

`RunLoopWithSessionInput(ctx, sess, turnInput, loopCfg)` 是当前主入口。

执行开始时会做这些事：

1. `turnInput.Normalize()`，得到结构化用户输入和 `RoutingText`。
2. `maybeRouteModel(routingText)`，根据输入切换活动模型。
3. `sanitizeLoopConfig(&loopCfg)`，给 loop 配置补默认值并施加硬上限。
4. `applyIntentToolGating(&loopCfg, routingText)`，按用户意图隐藏不相关或不应使用的工具。
5. `StartAutonomy(ctx)`，在需要时懒启动 autonomy。
6. 建立 telemetry span 和日志字段。
7. 初始化 `LoopResult` 和本轮 `loopRuntimeState`。
8. 构建上下文消息和 function calling 工具 schema。

普通会话聊天入口在 `internal/agent/agent.go`：

```text
ChatWithSessionInput
  -> chatWithSessionInput
  -> RunLoopWithSessionInput
```

`chatWithSessionInput` 会应用配置里的 agent loop 参数，并把 `AutoApprove` 设为 `true`，适配 Telegram 等场景里的自动工具调用。

### 流式入口

流式入口在 `internal/agent/agent.go`：

```text
ChatWithSessionStreamInputWithLoopConfig
  -> streamNative
  或 streamSimulated
```

流式路径同样会：

- sanitize loop config；
- apply intent tool gating；
- build context messages；
- build loop call options；
- 初始化 `streamConvergenceState`；
- 建立 memory gate；
- 建立 tool execution guard。

差异是：

- `streamNative` 使用 provider 原生 chunk 流，增量拼接 content、reasoning 和 tool call deltas。
- `streamSimulated` 先拿到完整模型回复，再按块模拟输出。
- 一旦进入工具调用阶段，流式路径也会执行工具、回写 tool message、裁剪上下文，再进入下一轮。

## LoopConfig

默认配置来自 `DefaultLoopConfig()`：

| 字段 | 默认值 | 说明 |
| --- | --- | --- |
| `MaxIterations` | `10` | 单轮最多迭代次数。 |
| `Timeout` | `60s` | 每次模型调用的超时时间。 |
| `AutoApprove` | `false` | 工具调用是否自动批准。 |
| `RepeatToolCallLimit` | `3` | 同一工具调用签名重复多少次后认为陷入循环。 |
| `ToolOnlyIterationLimit` | `3` | 连续只调工具、不产出文本的最大轮数。 |
| `DuplicateFetchLimit` | `1` | 同一 URL 抓取重复次数上限。 |

`sanitizeLoopConfig` 会补默认值，并设置硬上限：

- `MaxIterations` 最大 `300`。
- `Timeout` 最大 `10m`。
- disabled tools 会被规范化。

配置层的目标是防止无限循环、过长阻塞和重复抓取。

## 上下文构建

上下文构建在 `internal/agent/context_planner.go`，入口是：

```text
buildContextMessagesForInput
  -> contextPlanner.BuildInput
```

默认构建选项：

| 选项 | 默认值 |
| --- | --- |
| `IncludeRAG` | `true` |
| `IncludeHistory` | `true` |
| `HistoryRecent` | `6` |
| `HistoryMiddle` | `12` |

上下文预算按可用窗口分配：

| 类别 | 比例 |
| --- | --- |
| System | 15% |
| Memory | 10% |
| RAG | 20% |
| History | 25% |
| ToolResult | 30% |

`BuildInput` 的组装顺序是：

1. 运行 context memory hygiene hook。
2. 如果 provider 不支持图片 content parts，则剥离结构化图片内容。
3. 如果不是结构化多模态输入，尝试使用 context cache。
4. 构造 system prompt。
5. 对不支持 function calling 的 provider，额外写入工具目录文本。
6. 加入 skill route system hint。
7. 加入 memory messages。
8. 加入 RAG message。
9. 加入意图感知的 session history。
10. 加入附件解析证据。
11. 临时追加用户文本做窗口裁剪。
12. 移除临时用户文本，换回原始结构化 user message。

因此，最终模型看到的不是裸用户输入，而是：

```text
system prompt
+ skill hint
+ memory
+ RAG evidence
+ session history
+ attachment evidence
+ current user message
```

## 工具可见性

工具 schema 构建在 `internal/agent/loop_execution.go`：

```text
buildLoopCallOptions
  -> function.NewManager(a.tools)
  -> BuildTools
  -> filterFunctionTools
  -> normalizeToolChoiceForTools
```

这里会根据 `loopCfg.DisabledTools` 过滤 model-visible tools。

如果工具都被过滤掉：

```text
ToolChoice = "none"
```

如果配置里强制指定了某个工具，但该工具已经不可见，则：

- 有其他工具时降级为 `auto`；
- 没有工具时降级为 `none`。

模型调用时：

- provider 支持 function calling 且有可见工具：调用 `ChatWithOptions`。
- 否则：调用普通 `Chat`。

当 loop 进入强制搜索综合阶段时，`prepareLoopCallOptions` 会清空工具并把 `ToolChoice` 设为 `none`，要求模型基于已有证据产出最终回答。

## 意图级工具 Gating

意图级工具 gating 在 `internal/agent/tool_intent_gating.go`。

它由环境变量控制：

```text
LH_TOOL_INTENT_GATING=on
```

未开启时不会生效。

开启后，`applyIntentToolGating` 会根据用户输入识别任务意图，只保留相关工具，其余工具写入 `loopCfg.DisabledTools`。

当前识别的意图类型包括：

- 本地文件/代码查看；
- 文件编辑；
- Web 搜索/抓取/opencli；
- 时间；
- 计算；
- memory recall / hygiene / remember；
- RAG index；
- skill read/run；
- 图片、语音等 media；
- 数据库 schema / SQL query；
- cron、autonomy、heartbeat、delegate 等后台任务。

如果用户说“不用工具”或只是解释概念，且没有强工具意图，工具集合会被收缩为空。

需要注意：这是模型可见工具层面的收缩，不是最终安全边界。即使某个工具仍然可见，后面还有 tool execution guard 和 hook 层。

## 主循环状态

单次 loop 的临时状态放在 `loopRuntimeState`：

| 字段 | 作用 |
| --- | --- |
| `toolCallRepeatCount` | 按工具名和参数签名统计重复调用。 |
| `toolCallLastResult` | 缓存最新工具结果，用于重复循环提前停止时展示。 |
| `toolURLRepeatCount` | 按 URL 统计重复抓取。 |
| `toolURLLastResult` | 缓存同一 URL 的抓取结果。 |
| `toolExecutionGuard` | 用户约束驱动的工具执行保护。 |
| `consecutiveToolOnlyIters` | 连续只调用工具、不产出文本的轮数。 |
| `emptyResponseRetries` | 空回复恢复次数。 |
| `lengthRecoveryCount` | length 截断续写次数。 |
| `successfulSearchEvidence` | 有效搜索证据计数。 |
| `detailedSearchEvidence` | 详细搜索证据计数。 |
| `forceSearchSynthesis` | 是否强制进入搜索综合。 |
| `continuedResponse` | length 续写累计正文。 |
| `continuedReasoning` | length 续写累计 reasoning。 |

这些状态只属于当前一次 agent loop，不写入长期 memory。

## Reason / Act / Observe

主循环在 `RunLoopWithSessionInput` 的 `for i := 0; i < MaxIterations; i++` 中执行。

### Reason

每轮先进入 `StateReason`：

```text
chatLoopIteration(loopCtx, messages, callOpts, forceSearchSynthesis)
```

这里会用 `loopCfg.Timeout` 创建单轮超时上下文。模型返回后，loop 会统计 token，并处理文本协议里的 tool calls。

### Act

如果模型返回 `ToolCalls`，进入 `processToolCallBatch`：

1. 状态改为 `StateAct`。
2. 重置空回复和 length 恢复计数。
3. 统计连续 tool-only 轮数。
4. 为每个工具调用生成签名并统计重复次数。
5. 检查是否陷入重复工具调用循环。
6. 把 assistant tool-call message 追加到当前上下文和 session。
7. 执行工具。
8. 把工具结果写入 `LoopResult.ToolCalls`。

工具调用签名由工具名和规范化参数组成。URL 类工具还会提取 URL 目标，用于重复抓取去重。

### Observe

工具执行完成后进入 `StateObserve`：

1. 将每个工具结果包装成 tool message。
2. 对工具结果做上下文压缩。
3. 将结果追加到当前 messages。
4. 将结果追加到 session。
5. 更新工具结果缓存。
6. 执行 `fitContextWindow` 裁剪上下文。
7. 必要时追加搜索综合 prompt。
8. 回到下一轮 Reason。

## 工具执行

工具执行入口在 `executeToolCallsOrderedGuarded`。

执行顺序是：

```text
toolExecutionGuard
  -> hooks.RunPre
  -> executeToolCallsOrdered
  -> 按原始 tool call 顺序合并结果
```

### 执行保护

`toolExecutionGuard` 在 `internal/agent/tool_execution_guard.go`。

它根据用户当前输入里的限制词生成保护状态，例如：

- 只读；
- 只查看；
- 不要修改文件；
- 不要删除；
- 不要 push；
- 只总结网页；
- 不要写入记忆；
- 不要建立 RAG 索引；
- 只看数据库 schema；
- 不要新增 cron；
- 不要手动触发 heartbeat；
- 不要新建委派任务。

保护层会阻断明显违反约束的工具调用。

示例：

| 用户约束 | 可能被阻断的工具 |
| --- | --- |
| 不要修改文件 | `file_write`、`file_patch`、`file_mkdir`、`file_move`、写入型 `terminal` 命令 |
| 不要删除 | `file_delete`、删除型 `terminal` 命令、`cron_remove` |
| 不要 push | 包含 `git push` 的 `terminal` 命令 |
| 只总结网页 | `http_request` 的副作用调用 |
| 不要写入记忆 | `remember`、`rag_index` |
| 只看 schema | `sql_query` |

当前 terminal 命令判断是轻量字符串/字段匹配：

- 删除类：`rm`、`rmdir`、`unlink`、`del`、`rm -rf`、`rm -r`。
- 写入类：`>`、`sed -i`、`perl -pi`、`tee`、`touch`、`mv`、`cp`、`chmod`、`chown`、`git add`、`git commit`。

它不是 shell 级解析器，也不是系统级沙箱。它的定位是尊重当前用户约束，减少明显违背请求的工具调用。

### Hook 层

如果 hooks 启用，`executeToolCallsOrderedGuarded` 会在执行前调用：

```text
hooks.RunPre(toolName, arguments, source, sessionID)
```

hook 可以：

- 放行；
- 阻断；
- 改写工具参数。

被 guard 或 hook 阻断的工具不会实际执行，但会生成一条 tool result，告诉模型该调用被拦截。

### 并发与顺序

底层执行在 `executeToolCallsOrdered`：

- 如果 `allowMixedParallel=true`，并发安全工具并行执行，非并发安全工具串行执行。
- 如果 `allowMixedParallel=false`，只有全部工具都并发安全时才整体并行；否则全部串行。
- 不管执行方式如何，最终结果会按原始 tool call 顺序排序。

非流式主 loop 调用工具时 `allowMixedParallel=false`。

memory gate 和流式工具路径使用 `allowMixedParallel=true`。

## 重复抓取与结果压缩

`executeToolMaybeDedup` 会针对 `web_fetch` 和 `opencli` 做 URL 级去重。

URL 归一化逻辑：

- 只处理 `web_fetch` 和 `opencli`。
- 从参数里提取 `url`。
- `opencli` 会额外从 `args` 里的 `--url` 提取。
- 去掉 fragment。
- host 转小写。

当同一 URL 的重复次数超过 `DuplicateFetchLimit` 后，loop 会复用上一次结果，避免反复抓同一页面。

工具结果进入上下文前会通过 `buildContextToolResult` 压缩：

| 工具 | 上下文保留上限 |
| --- | --- |
| `web_search` | 2400 字符 |
| `web_fetch` / `opencli` | 6000 字符 |
| `file_list` | 1200 字符 |
| 默认 | 8000 字符 |

除 `file_list` 外，结果前面会尽量加入 `[Tool Summary]`，帮助模型优先看到关键信息。过长结果会在中间截断，并带截断提示。

## 搜索综合收敛

当模型持续调用搜索类工具但不产出最终回答时，loop 会触发搜索综合。

有效搜索证据来自：

- `web_search`
- `web_fetch`
- `opencli`

空结果、错误、无结果、全部搜索源失败都不算有效证据。

触发综合的条件在 `shouldForceSearchSynthesis`：

- 有至少 `3` 条有效搜索证据并出现工具-only 倾向；
- 或达到搜索证据阈值并连续多轮 tool-only。

触发后，loop 会追加 `searchSynthesisPrompt`，并设置：

```text
forceSearchSynthesis = true
```

下一轮模型调用会禁用工具，只允许模型基于已有证据综合回答。

## Memory Gate

Memory gate 在 `internal/agent/memory_gate.go`。

它解决的问题是：有些问题必须先补充外部事实或时间信息，不能直接依赖记忆回答。

构建流程：

```text
buildMemoryToolGate(query, turnScope, disabledTools)
  -> memory.RouteWithOptions(query, scope filter)
  -> route.ToolRequirements
  -> 检查工具是否存在、启用、未被 disabled 且 PolicySafe
```

如果 memory route 要求某些工具，memory gate 会记录：

- required tools；
- unavailable tools；
- attempted tools；
- failed checks。

当模型准备直接给最终答案，但 required tools 还没执行时，`executeMemoryGateForLoop` 会自动插入工具调用。

工具名和参数不再由 gate 特判。每个 `RouteToolRequirement` 直接携带零个或多个结构化 `calls.arguments`；零个 calls 表示执行一次 `{}`。策略模板在 memory router 中展开后，gate 只负责通用执行。

只有工具注册时声明 `PolicySafe=true` 才能被 durable-memory policy 自动执行。当前写操作、shell、委派和 runtime 控制工具默认都不能被策略自动调用。

工具执行后，memory gate 会追加 synthesis prompt，要求模型基于已尝试的检查回答，并说明哪些内容无法核验。

如果达到最大迭代仍没有完成 memory gate 综合，loop 会返回 incomplete message，并带错误：

```text
memory gate did not produce final synthesis
```

## 直接回复路径

如果模型没有返回工具调用，loop 走 `processDirectResponse`。

它处理三种情况：

### 空回复

如果模型回复为空：

- 最多重试 `maxEmptyResponseRetries=2` 次。
- 每次追加 `emptyResponseRecoveryPrompt`。
- 超过后返回空回复 fallback 文案，或返回已累计的 continuation。

### length 截断

如果 `FinishReason == "length"`：

- 将本轮内容追加到 `continuedResponse`。
- 将 reasoning 追加到 `continuedReasoning`。
- 最多续写 `maxLengthContinuationRetries=3` 次。
- 超过后返回已累计内容并追加截断提示。

### 正常文本

如果已有 continuation，则拼接后 finalize。

否则直接 finalize 当前回复。

## 结束条件

当前 loop 的主要结束条件：

| 条件 | 行为 |
| --- | --- |
| 模型给出无工具最终文本 | finalize。 |
| length 续写完成 | finalize 拼接结果。 |
| 空回复恢复耗尽 | finalize fallback。 |
| 重复工具调用循环 | 提前停止并返回最新工具输出摘要。 |
| 搜索证据足够且持续 tool-only | 强制进入搜索综合。 |
| memory gate 未满足 | 自动执行 required tools 后继续。 |
| 达到 `MaxIterations` 且有 continuation | finalize continuation + 截断提示。 |
| 达到 `MaxIterations` 且 memory gate 未综合 | 返回 incomplete message + error。 |
| 达到 `MaxIterations` 且无可用部分结果 | 返回 max iterations error。 |

## Finalize 与持久化

`finalize` 在 `RunLoopWithSessionInput` 内部定义。

它会：

1. 清理响应里的工具协议残留。
2. 根据工具调用追加自然引用。
3. 设置 `LoopResult.Response`。
4. 设置状态为 `StateDone`。
5. 向 session 追加最终 assistant message。
6. 在允许时保存 final answer 文档。
7. 在开启 `autoIndexFinalAnswersEnabled` 时把最终回答写入 RAG。
8. 保存 session。

默认不会把 final answer 自动索引进 RAG。代码注释里明确说明：RAG 索引源材料应该和模型生成结论分开。

如果 `loopCfg.Ephemeral=true`：

- 不保存 final answer 文档；
- 不自动索引最终答案。

## Chat 层额外行为

`chatWithSessionInput` 在 loop 成功返回后还会做聊天层处理：

- `saveConversationMemoryFromTurn(input, response)`，自动记忆当前对话轮次。
- 每 10 轮执行 memory decay 和 expire。
- 每 20 轮执行 auto summarize。
- 每 50 轮清理过期 mid-term summaries。
- 记录 metrics。
- 记录 proactive chat event。

这些不是 `RunLoopWithSessionInput` 本身的职责，而是 chat 层包在 loop 外面的长期记忆和指标逻辑。

## 流式路径的关键差异

流式路径与非流式路径共享这些策略：

- intent tool gating；
- memory gate；
- tool execution guard；
- hook pre-check；
- 重复工具调用检测；
- URL 去重；
- 搜索综合；
- 空回复恢复；
- length 续写恢复；
- session 持久化。

但流式路径多了事件输出：

- `ChatEventThinking`
- `ChatEventContent`
- `ChatEventToolCall`
- `ChatEventToolResult`
- `ChatEventError`

`streamNative` 会在 chunk 中增量拼接 tool call deltas。为了避免把文本形式的 tool call 协议误发给用户，它会在可能出现文本 tool call 时暂缓输出。

当流式模型返回工具调用后，代码通常转入 `streamSimulated` 继续下一轮，因为工具结果之后需要完整响应做稳定收敛。

## 当前边界

当前 agent loop 的安全与收敛边界可以这样理解：

```text
工具可见性：intent gating + disabled tools
工具执行保护：toolExecutionGuard + hooks
循环收敛：repeat call limit + tool-only limit + search synthesis + max iterations
上下文控制：context planner budgets + fitContextWindow + tool result compaction
事实补检：memory gate
持久化：session + final answer document + optional RAG indexing
```

其中 tool execution guard 是请求级保护，不是系统级沙箱；intent gating 是减少模型可见工具，不是权限系统；memory gate 是回答前的事实补检，不是长期 memory 写入机制。

如果后续要优化 agent loop，优先关注这些方向：

1. 把 intent gating 从关键词规则升级为可解释的 intent policy。
2. 把 terminal guard 从轻量字符串匹配升级为 shell AST 或受限命令计划。
3. 将搜索综合触发条件配置化，便于不同模型调参。
4. 将 memory gate 的 required tool 执行结果做结构化状态输出，方便 UI 展示。
5. 把 stream native 和 simulated 的收敛逻辑进一步抽成共享状态机，减少重复实现。
