# LuckyAgent 上下文拼接策略

## 结论

LuckyAgent 的上下文拼接不是把所有内容简单塞进 prompt，而是分成两层：

1. `system prompt`（系统提示词：身份、工具规则、技能规则、项目手册、AGENTS.md、平台提示）先由 `buildSystemPromptWithOptions` 组装。
2. `context planner`（上下文规划器：把 memory、RAG、历史消息、附件、当前用户输入按预算拼成消息序列）再由 `contextPlanner.BuildInput` 组装。

最终进入模型的是一个 `[]provider.Message`，大致顺序是：

```text
system prompt
skill route hint
memory messages
RAG message
selected session history
attachment messages
current user message
```

如果超出窗口，会通过 `contextx.ContextWindow.Fit` 按优先级裁剪。

## 关键实现位置

核心代码：

- `internal/agent/context_planner.go`
- `internal/agent/system_prompt.go`
- `internal/agent/loop.go`
- `internal/agent/agent.go`
- `internal/contextx/window.go`
- `internal/agent/context_packer_snapshot.go`

入口链路：

```text
RunLoopWithSessionInput
  -> defaultContextBuildOptions
  -> buildContextMessagesForInput
  -> contextPlanner.BuildInput
  -> buildSystemPromptWithOptions
  -> buildMemoryMessages
  -> buildRAGMessage
  -> buildHistoryMessages
  -> buildAttachmentMessages
  -> fitContextWindow
```

## 一句话版

LA（LuckyAgent）先构造“行为规则”，再补“可用证据”，最后放“用户当前问题”。

例子：

- 用户问“这个项目里的 accounts.db 是干什么的”
- system prompt 会告诉模型它是工具型 agent、要查真实文件
- memory/RAG 可能提供项目长期事实
- history 会保留最近用户说“拿走，这里不需要这个”
- 当前 user message 放最后，确保最新请求优先

## 默认上下文选项

默认选项来自 `defaultContextBuildOptions`：

| 字段 | 默认值 | 说明 |
| --- | --- | --- |
| `IncludeRAG` | `true` | 默认拼接 RAG 检索结果。 |
| `IncludeHistory` | `true` | 默认拼接 session history。 |
| `HistoryRecent` | `6` | 默认保留最近 6 条历史消息作为候选。 |
| `HistoryMiddle` | `12` | 最近消息之前最多取 12 条做摘要候选。 |
| `DisabledTools` | 空 | 本轮禁用工具列表，会影响 system prompt 和 tool catalog。 |

这些选项不是用户配置文件里的全部 context 配置，而是 agent loop 每轮构造上下文时使用的 planner 选项。

## Token 预算

`newContextPlanner` 会先读取 `contextx.WindowConfig`：

```text
available = MaxTokens - ReservedTokens
```

然后按比例切分预算：

| 类别 | 比例 | 最小值 | 用途 |
| --- | --- | --- | --- |
| System | 15% | 256 | system prompt、工具目录等。 |
| Memory | 10% | 128 | core memory、working memory、短期摘要、中期摘要。 |
| RAG | 20% | 256 | RAG 检索知识。 |
| History | 25% | 256 | 会话历史和历史摘要。 |
| ToolResult | 30% | 256 | 后续工具结果进入上下文时的预算参考。 |

默认 `contextx.DefaultWindowConfig` 是：

| 字段 | 默认值 |
| --- | --- |
| `MaxTokens` | `4096` |
| `ReservedTokens` | `1024` |
| `Strategy` | `TrimLowPriority` |
| `SlidingWindowSize` | `10` |
| `MaxConversationTurns` | `50` |
| `MemoryBudget` | `800` |
| `SummarizeThreshold` | `0.8` |

配置层还有：

| 配置键 | 默认值 | 说明 |
| --- | --- | --- |
| `context.max_history_turns` | `50` | 最大历史轮数配置。 |
| `context.max_context_tokens` | `8000` | 配置层的上下文 token 上限。 |
| `context.compression_threshold` | `0.8` | 历史摘要触发阈值。 |
| `agent.context_debug` | `false` | 是否输出 context planner 调试日志。 |

## 第一层：system prompt 拼接

`buildSystemPromptWithOptions` 负责构造系统提示词。

拼接顺序如下：

```text
core prompt
tool policy
tool inventory
skill policy
available skills
memory / RAG policy
supplementary context intro
LuckyAgent manual
context file
metadata
platform hint
```

### 1. core prompt

来源：

- `soul.SystemPrompt()`
- `~/.luckyagent/memory/prompts/core.md`
- 如果外部文件不存在，则使用默认 core prompt

作用：

- 定义 LuckyAgent 是工具型 agent，不是纯聊天包装。
- 要求优先查真实状态、使用工具验证、不要假装执行工具。

例子：

用户问“这个数据库文件是什么”，core prompt 会推动模型先查文件、schema 或路径，而不是直接猜。

### 2. tool policy 和 tool inventory

只有存在可见工具时才拼接。

`tool policy` 来源：

- `~/.luckyagent/memory/prompts/tool_policy.md`
- fallback 默认工具使用策略

`tool inventory` 来自当前 model-visible tools，最多写入 20 个工具名和描述。

注意：

- 如果本轮有 `DisabledTools`，这些工具不会进入 tool inventory。
- function-calling provider 通常通过 API 传工具 schema；非 function-calling provider 会额外拼 `[Available Tools]` 文本目录。

例子：

如果 `web_search` 被禁用，system prompt 里不会把它当作可用工具提示给模型。

### 3. skill policy 和 skills block

只有同时满足两个条件才拼接：

- 已加载 skill。
- 可见工具里包含 `skill_read`。

拼接内容：

- skill routing policy
- available skills summary

例子：

用户要求“写一个讲解方案”，如果 skill 列表里有相关写作 skill，system prompt 会提示模型按 skill 工作流处理。

### 4. memory / RAG policy

只有相关工具可见时才拼接：

- `remember`
- `recall`
- `rag_search`
- `rag_index`

这个 block 会明确：

- memory 是 durable user facts、偏好、项目约束。
- RAG 是 indexed documents、长文档和语义检索材料。
- LuckyAgent memory source of truth 是 `${HOME}/.luckyagent/memory` 下的 Markdown vault。
- RAG SQLite storage 不是 memory source of truth。

例子：

问“我之前让你记住了什么偏好”，应该优先使用 memory/recall，而不是查普通 RAG。

### 5. supplementary context intro

当 manual 或 context file 存在时，会先插入一段说明：

```text
Supplementary context policy
```

作用是告诉模型：后面的手册和项目上下文是补充指导，不能覆盖核心安全和任务收敛规则。

### 6. LuckyAgent manual

来源查找顺序：

1. 环境变量 `LUCKYAGENT_MANUAL_FILE`
2. 环境变量 `LUCKYHARNESS_MANUAL_FILE`
3. 当前 cwd 下：
   - `memory/prompts/AGENTS.md`
   - `description/AGENTS.md`
   - `description/agents.md`
   - `LUCKYAGENT_AGENT_MANUAL.md`
   - `description/LUCKYAGENT_AGENT_MANUAL.md`
   - `LUCKYHARNESS_AGENT_MANUAL.md`
   - `description/LUCKYHARNESS_AGENT_MANUAL.md`
4. runtime home 下：
   - `memory/prompts/AGENTS.md`
   - `description/AGENTS.md`
   - `description/agents.md`
   - `description/LUCKYAGENT_AGENT_MANUAL.md`
   - `description/LUCKYHARNESS_AGENT_MANUAL.md`

读取后会：

- 做 prompt-injection 基础过滤。
- 超过 20000 字符时保留头部约 70% 和尾部约 20%。
- 包装成 `LuckyAgent manual (<filename>): ...`。

### 7. context file

context file 指项目级 `AGENTS.md` 或 `agents.md`。

查找顺序：

1. 环境变量 `LUCKYAGENT_AGENTS_FILE`
2. 环境变量 `LUCKYHARNESS_AGENTS_FILE`
3. 从当前 cwd 向父目录查找最近的：
   - `AGENTS.md`
   - `agents.md`

读取后会：

- 做 prompt-injection 基础过滤。
- 用 `CompactMarkdownForPrompt` 压缩到约 20000 字符。
- 包装成 `Context file (<filename>): ...`。

例子：

当前仓库根目录的 `AGENTS.md` 会进入 context file block，告诉模型项目是 Go agent runtime、改代码前要 `git status --short`、优先用 `rg` 等。

### 8. metadata 和 platform hint

metadata 包含：

- `Model`
- `Provider`

platform hint 根据 `msg_gateway.platform` 选择：

- `telegram`
- `qqofficial`
- `napcat` / `onebot`
- `cli`

例子：

在 QQ 平台时，platform hint 会要求最终回答使用纯聊天文本，避免 Markdown。

## 第二层：context planner 拼接

`contextPlanner.BuildInput` 负责把本轮上下文拼成消息序列。

### 总顺序

实际顺序是：

```text
1. system prompt
2. skill route system hint
3. memory messages
4. RAG message
5. session history messages
6. attachment messages
7. current user message
```

最终当前用户消息一定追加到最后。

### 1. 输入标准化

首先调用：

```go
input = input.Normalize()
```

得到：

- `RoutingText`：用于路由、memory、RAG、history intent 的文本。
- `Message`：最终要放进模型的当前用户消息。
- `Attachments`：附件。
- `Scope`：用于 memory scope 过滤。

如果 provider 不支持 image content parts，会把结构化图片 parts 去掉。

### 2. context cache

如果当前输入没有结构化 content parts，则允许缓存。

cache key 会包含：

- system prompt
- 最近 memory
- RAG document count
- session id/title/message count/last message signature
- user input

命中缓存时直接返回缓存 messages。

例子：

同一个 session 内重复问同一个普通文本问题，如果 memory/RAG/session 没变，可能复用上下文拼接结果。

### 3. skill route system hint

在 system prompt 后，会根据当前 routing text 生成技能路由提示。

作用：

- 如果任务明显匹配某个 skill，提示模型应使用对应工作流。
- 如果工具被禁用，路由提示也会考虑 `DisabledTools`。

### 4. memory messages

memory messages 由四块组成：

```text
[Core Memory]
[Working Memory — Retrieved Evidence]
[Session History — Mid-term]
[Recent Context]
```

#### Core Memory

来源：

- memory store 中 `TierLong`。

过滤：

- 根据当前 `TurnScope` 做 scope 可见性过滤。

选择规则：

- 最多 3 条。
- 如果 query 为空，或内容包含 query，或当前还没选任何条目，则加入。

优先级：

- 后续裁剪时归类为 `memory_long`
- priority 是 `High`

例子：

长期记忆里有“用户偏好中文回答”，这类稳定规则适合进入 Core Memory。

#### Working Memory

来源：

- `memory.Route(query)` 的结果。

处理：

- 过滤 raw conversation short memory。
- 按 scope 过滤。
- 生成 evidence refs。
- 清空部分 temporal/superseded/conflict refs。
- 按 tier 排序，长期记忆优先。
- 写入 activation feedback。

提示语明确要求：

- retrieved memory 是 prior evidence，不是当前任务本身。
- 如果和最新用户消息、明确 session history 冲突，优先最新用户消息和 session history。
- 如果 memory 说需要实时检查，要用工具验证或说明无法检查。

例子：

memory 里说“用户之前在做 A 项目”，但当前用户明确给了 B 项目路径，则以当前路径为准。

#### Mid-term Summary

来源：

- `midTerm.SearchSummaries(query, 2)`

输出头：

```text
[Session History — Mid-term]
```

语义：

- 压缩摘要。
- 非权威。
- 和原始近期消息、工具输出、workspace state 冲突时，以后者为准。

#### Recent Context

来源：

- `shortTerm.Summary()`

输出头：

```text
[Recent Context]
```

语义：

- 最近上下文摘要。
- 裁剪时优先级较低。

### 5. RAG message

只有满足以下条件才拼接：

- `IncludeRAG=true`
- `ragManager != nil`
- query 非空
- RAG document count > 0
- `SearchWithContext(ctx, query)` 有结果

输出内容来自 RAG manager 的 `SearchWithContext`，再按 RAG 预算裁剪。

优先级：

- 如果内容以 `## Retrieved Knowledge` 或 `[Retrieved Knowledge` 开头，后续裁剪时归类为 `rag`
- priority 是 `High`

例子：

项目文档已经被 `rag_index` 建索引，用户问某个长期设计背景时，会走 RAG 取回相关片段。

## session history 策略

`buildHistoryMessages` 不会无脑拿全部历史，而是 intent-aware（按当前意图筛选）。

流程：

1. 读取 `sess.GetMessages()`。
2. 根据当前 query、session title 和历史内容提取 intent terms。
3. 取最近 `HistoryRecent` 条作为 recent 区间，默认 6。
4. recent 之前最多取 `HistoryMiddle` 条作为 middle 区间，默认 12。
5. 更早历史生成 `[Conversation Themes]`。
6. middle 区间生成 `[Conversation Summary]`。
7. recent 区间按意图筛选后原样加入。

### 历史意图词

会从当前问题、session title 和部分历史消息中提取：

- 英文/数字 token。
- 中文 2 到 4 字片段。
- 一些硬编码 alias。

当前硬编码 alias 包括：

- 女儿/孩子/daughter/child -> `daughter`, `child`, `family`
- 过敏/花粉/pollen/allergy -> `pollen`, `allergy`
- 户外/出门/公园/outdoor/park -> `outdoor`, `park`

这些 alias 是为了让家庭、过敏、户外这类跨语言话题更容易召回相关历史。

### recent history 选择

recent 区间不是全部保留，而是优先保留：

- 和 intent terms 相关的消息。
- 最后两条 tail candidate。
- 最新 user message 之后的消息。
- tool 消息。
- 包含关键调试词的消息，例如 `context packer`、`benchmark`、`constraint`、`latest user direction` 等。

同时会跳过明确标记无关的消息，例如包含：

- `no current task relevance`
- `unrelated note`
- `unrelated project state`

### 历史摘要

如果历史范围足够长且 token 超过阈值，会尝试调用 LLM 生成摘要。

摘要 prompt 要求保留：

- User topics
- Assistant progress
- Tool evidence
- Open questions

如果 LLM 摘要失败，则使用本地摘要 fallback。

生成的压缩摘要会保存到 mid-term memory。

例子：

一个长会话里前面讨论过 Graph RAG，后面用户问“这个和 GRAPH RAG 有关系吗”，历史策略会更倾向保留 Graph RAG 相关消息，而不是完全保留最近所有闲聊。

## 附件拼接策略

附件在 memory、RAG、history 之后，当前 user message 之前拼接。

附件 block 会提示：

```text
Use only the attachments listed in this block for the current user request.
Do not substitute files from chat history, memory, or the workspace when an attachment is missing or unreadable.
```

含义：

- 用户本轮上传的附件是当前请求的直接证据。
- 如果附件缺失或不可读，不能用历史里的同名文件或 workspace 猜一个替代。

例子：

用户上传 `report.pdf` 并要求总结。如果附件解析失败，不能擅自去项目目录找另一个 `report.pdf` 当作输入。

## 当前用户消息

当前用户消息永远在最后追加。

实现上会先构造一个 provisional messages：

```text
已有上下文 + 附件 + 临时 user routing text
```

然后先跑一次 `fitContextWindow`，确认上下文能放下，再移除临时 user，最后追加真实的 `input.Message`。

这样做的目的：

- 裁剪时把当前用户问题纳入预算考虑。
- 最终保留结构化 user message，例如 content parts。

## 最终裁剪策略

最终会调用：

```go
fitContextWindow(messages)
```

如果存在 `ContentParts`，目前会跳过裁剪，避免破坏多模态消息。

否则会转换成 `contextx.Message`，分配 priority 和 category。

### 优先级映射

| 消息 | Category | Priority |
| --- | --- | --- |
| 普通 system | `system` | `Critical` |
| `[Core Memory]` | `memory_long` | `High` |
| `[Working Memory...]` | `memory_medium` | `Normal` |
| `[Recent Context]` | `memory_short` | `Low` |
| `[Session History...]` | `memory_mid` | `Normal` |
| `[Conversation Summary]` | `conversation_summary` | `Low` |
| `[Conversation Themes]` | `conversation_summary` | `Low` |
| RAG retrieved knowledge | `rag` | `High` |
| tool result | `tool_result` | `Normal` |
| 普通 user/assistant | role 名 | `Normal` |

默认裁剪策略是：

```text
TrimLowPriority
```

也就是按优先级从高到低填充：

```text
Critical -> High -> Normal -> Low
```

例子：

如果上下文超长，`[Recent Context]` 和 `[Conversation Summary]` 这类低优先级内容更容易被裁掉；system prompt、Core Memory、RAG 重要证据更容易保留。

## 工具结果进入上下文

模型调用工具后，工具结果会作为 `tool` message 追加到当前 messages。

追加前会调用：

```go
buildContextToolResult
  -> compactToolResultForContext
  -> truncateToolResultForContext
```

不同工具结果有不同长度限制：

| 工具 | 上下文限制 |
| --- | --- |
| `web_search` | 约 2400 字符 |
| `web_fetch` | 约 6000 字符 |
| `opencli` | 约 6000 字符 |
| `file_list` | 约 1200 字符 |
| 其他工具 | 约 8000 字符 |

长结果会保留头部和尾部，中间插入：

```text
... (middle omitted for context: N chars; do not rely on omitted details without another tool check) ...
```

例子：

`web_fetch` 抓到一篇很长网页时，模型只能看到压缩后的头尾内容。如果需要中间细节，应该再次调用工具定向读取。

## memory、RAG、history 的优先级关系

LA 的语义优先级是：

```text
当前用户消息
> 本轮附件
> 工具实时输出 / workspace state
> 明确 session history
> memory retrieved evidence
> RAG retrieved knowledge
> 压缩摘要
```

注意这不是纯 token priority，而是回答时的证据优先级。

例子：

- memory 说“用户使用旧项目路径”
- 当前用户给了新路径 `/media/.../luckyagent`
- 回答和操作必须以当前用户的新路径为准

## 上下文缓存策略

context planner 会在没有结构化 content parts 时缓存拼接结果。

cache key 包含：

- system prompt
- 最近 8 条 memory
- RAG document count
- session id
- session title
- session message count
- session last message signature
- user input

这意味着：

- memory 变化会影响 cache。
- RAG 文档数量变化会影响 cache。
- session 新增消息会影响 cache。
- 同一问题在上下文未变化时可复用。

## 调试和观测

### context debug 日志

配置：

```bash
lh config set agent.context_debug true
```

开启后，context planner 会输出：

- cache hit / cache store
- total tokens
- message count
- system/memory/rag/history/tool_result bucket tokens
- 各 bucket message count

### context packer snapshot

`BuildContextPackerSnapshot` 可以在不调用模型的情况下构造上下文快照。

返回：

```json
{
  "messages": [],
  "total_tokens": 0,
  "bucket_tokens": {},
  "bucket_counts": {}
}
```

用途：

- benchmark
- 调试某条用户输入到底拼了哪些上下文
- 检查 memory/RAG/history 是否过量

## 当前策略的特点

### 优点

1. 最新用户消息在最后，避免旧记忆覆盖当前任务。
2. memory 明确标注为 prior evidence，不是当前任务。
3. history 是 intent-aware，不是简单最近 N 条。
4. RAG 只有在有文档和有检索结果时才进入。
5. 工具结果会压缩，避免一次大输出挤爆上下文。
6. AGENTS.md 和 runtime manual 都有注入风险基础过滤。

### 局限

1. system prompt 先按预算裁剪，长 AGENTS.md 可能被二次截断。
2. RAG 结果当前只走 `SearchWithContext`，不是 Graph RAG 多跳查询。
3. history alias 里有少量硬编码家庭/过敏/户外词，泛化能力有限。
4. `ContentParts` 存在时会跳过最终窗口裁剪，长多模态上下文可能需要额外控制。
5. tool result 压缩是字符级，不是结构化摘要。
6. prompt injection 过滤是基础正则，不是完整安全沙箱。

## 推荐优化方向

### 1. 输出更完整的 context inspection API

现在已有 `BuildContextPackerSnapshot`，建议进一步暴露：

- 每条 message 的 source。
- 每条 message 的 priority。
- 每条 message 的 estimated tokens。
- 是否来自 cache。
- 是否被裁剪。

### 2. 让 config 和 planner 预算完全对齐

配置里有 `context.max_context_tokens`，`context.max_history_turns`，planner 又从 `contextx.WindowConfig` 读取窗口配置。

建议整理成一个明确链路：

```text
config.Context -> contextx.WindowConfig -> contextPlanner budget
```

避免文档和实现理解分叉。

### 3. 强化 history intent terms

当前 alias 偏特定场景。建议：

- 把 alias 移到配置或规则文件。
- 根据项目名、文件名、工具名自动扩展 terms。
- 给“最新用户明确改方向”的消息更高权重。

### 4. RAG 和 Graph RAG 分层

当前 RAG context 是普通 `SearchWithContext`。

建议未来拆分：

- Vector RAG context
- Graph RAG context
- Hybrid context

并在 message header 中明确来源，避免用户以为普通 RAG 已经做了多跳图查询。

### 5. 工具结果结构化压缩

建议不同工具使用不同摘要器：

- `file_read` 保留行号和命中段。
- `web_search` 保留 title/url/snippet。
- `terminal` 保留 exit code、关键 stdout/stderr。
- `rag_search` 保留 source/chunk score。

这样比纯字符截断更稳定。

## 推荐结论

当前 LA 的上下文拼接策略可以概括为：

```text
规则先行，证据分层，历史筛选，当前请求最后，超窗按优先级裁剪。
```

维护时最重要的原则是：不要让旧 memory、RAG 或历史摘要覆盖最新用户请求；任何从 memory/RAG/history 进入的内容都应该被视为证据层，而不是当前任务本身。
