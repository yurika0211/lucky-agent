# Telegram 会话与 Trace 机制

本文件记录 Telegram channel 当前实现中的会话绑定、任务队列、Tool Trace 和 Reasoning Trace 机制。取证来源主要是：

- `internal/gateway/telegram/handler.go`
- `internal/gateway/telegram/tool_nl.go`
- `internal/agent/agent.go`
- `internal/agent/loop_execution.go`
- `internal/session/session.go`
- `internal/config/config.go`

## 结论

Telegram 的会话连续性不是由 Telegram 消息本身承载，而是由 handler 内部的 `chatID -> sessionID` 映射承载。真正的会话内容仍由 `internal/session.Manager` 维护。

Tool Trace 来自 agent loop 发出的 `ChatEventToolCall` 和 `ChatEventToolResult`。Telegram handler 将这些事件拼成 `telegramToolTraceStep`，最后渲染成 Tool Trace 或 Agent Trace 卡片。

Reasoning Trace 不是 provider 原始 `ReasoningContent` 的外泄。原始 `ReasoningContent` 会作为 provider message 的结构化字段保存到 session 中；Telegram 用户看到的 Reasoning Trace 是基于进度事件或 LLM 生成的进度摘要渲染出来的用户可读卡片。

## 会话绑定

核心状态在 `telegram.Handler`：

- `sessions map[string]string`：`chatID -> sessionID`。
- `tasks map[string]*chatTask`：当前 chat 正在执行的任务。
- `queues map[string]*chatQueue`：当前 chat 等待执行的消息队列。
- `dataDir string`：启用持久化时用于保存 Telegram chat 与 session 的绑定。

### 创建与读取

`getSessionID(chatID)` 是默认入口：

1. 先读 `h.sessions[chatID]`。
2. 如果已有绑定，直接返回 session ID。
3. 如果没有绑定，通过 `session.Manager.New()` 创建新 session。
4. 写入 `h.sessions[chatID]`。
5. 调用 `saveChatSessions()` 持久化。

`resetSession(chatID)` 总是创建一个新 session，并覆盖当前 chat 的绑定。`/reset` 和 session 缺失后的恢复路径都会用到它。

### 持久化

`SetDataDir(dir)` 会设置 Telegram handler 的 data dir，并调用 `loadChatSessions()`。

持久化文件路径：

```text
<dataDir>/chat_sessions.json
```

文件内容是 `chatID -> sessionID` 映射。加载时会校验 session ID 是否仍能在 `session.Manager` 中找到；找不到的绑定不会恢复。也就是说，`chat_sessions.json` 只是 Telegram 侧的绑定索引，不是会话内容本身。

### 切换与管理命令

当前实现中会话相关命令的语义：

- `/session`：无参数时显示当前 chat 绑定的 session；带参数时切换到指定 session。
- `/sessions`：列出最近 session，标记当前 session，提示使用 `/resume <title>` 或 `/resume <id>`。
- `/resume <session_title_or_id>`：按 ID、标题或前缀查找并切换 session；歧义时返回候选列表。
- `/rename <title>`：重命名当前 session。
- `/new`：创建新 session 并覆盖当前 chat 的绑定。
- `/reset`：创建新 session 并覆盖当前 chat 的绑定。
- `/history`：读取当前绑定 session 的消息历史。
- `/stop`：取消当前 chat 正在运行的任务，不会创建或切换 session。

### 外部回复锚点

Telegram handler 还支持 reply 方式的 session 回绑：

1. 用户回复一条由外部流程发出的 Telegram 消息。
2. handler 调用 `ResolveExternalReplyAnchor("telegram", chatID, messageID)`。
3. 如果能解析到 session ID，并且 session 仍存在，调用 `bindSessionID(chatID, sessionID)`。
4. 之后该 Telegram chat 会继续使用解析出的 session。

这个机制用于让“外部发出的消息”重新接回正确的 LuckyAgent session。

## 任务队列与取消

Telegram 的会话连续性和任务并发控制是两套状态。

入口会通过 `dispatchChatAsync()` 给 `UserTurnInput` 增加 `TurnScope`，然后按 chat 入队。`enqueueChatRequest()` 为每个 chat 维护一个 `chatQueue`，`runChatQueue()` 串行处理该 chat 中的请求。

每次真正执行 chat 时，`handleChat()` 会：

1. 调用 `beginChatTask(chatID, ctx)` 创建可取消的 task context。
2. 调用 `getSessionID(chatID)` 确定本轮使用的 session。
3. 根据任务类型选择同步、流式或自然语言进度模式。
4. defer `finishChatTask(chatID, task)` 清理当前任务。

`/stop` 调用 `cancelChatTask(chatID)`，只取消当前 chat 的当前任务。它不会清空队列，也不会重置 session。

## ChatEvent 流

Telegram trace 的数据来自 agent 对上层 UI 暴露的 `ChatEvent`：

```go
type ChatEvent struct {
    Type    ChatEventType
    Content string
    Name    string
    Args    string
    Result  string
    Err     error
}
```

事件类型包括：

- `ChatEventThinking`
- `ChatEventToolCall`
- `ChatEventToolResult`
- `ChatEventContent`
- `ChatEventDone`
- `ChatEventError`

流式路径：

```text
Telegram message
  -> dispatchChatAsync(inputWithMessageScope)
  -> runChatQueue
  -> handleChat
  -> openChatEventStream
  -> ChatWithSessionStreamInput
  -> agent.ChatEvent stream
  -> handleChatNarrativeStream 或 handleChatStream
```

`openChatEventStream()` 如果遇到 `session not found`，会调用 `resetSession(chatID)` 后重试一次。这保证 Telegram chat 不会永久卡在一个失效 session ID 上。

## Tool Trace

Tool Trace 的内部数据结构在 `tool_nl.go`：

```go
type telegramToolTraceStep struct {
    Name    string
    Args    string
    Result  string
    Success bool
}
```

构建规则：

1. 收到 `ChatEventToolCall` 时追加一个 step，记录 `Name` 和 `Args`。
2. 收到 `ChatEventToolResult` 时，从后往前找同名且尚未有 result 的 step。
3. 写入 `Result`。
4. 如果 result 以 `error:` 开头，`Success=false`，否则 `Success=true`。

agent 侧事件来源：

- `emitChatToolCallEvents()` 把工具名和截断到 100 字符以内的参数发成 `ChatEventToolCall`。
- `emitChatToolResultEvent()` 把工具名和 short result 发成 `ChatEventToolResult`。

### 可见性分层

`telegramToolTraceVisibility(name)` 决定每个工具如何展示：

- `hidden`：大多数 `skill_*` 内部工具。
- `compact`：`skill_read`、`remember`、`recall`、`rag_search`、`rag_index`、`cron*`、`skill_*_run`。
- `agent`：`delegate_*`、`autonomy_*`、`heartbeat_*`。
- `visible`：默认工具。

`renderTelegramToolTraceCard()` 会跳过 `hidden` 和 `agent`，最多展示 6 个工具步骤，渲染为 `Tool Trace` 卡片。

`renderTelegramAgentTraceCard()` 只展示 `agent` 类步骤，最多展示 6 个 agent step，渲染为 `Agent Trace` 卡片。

当前 Tool Trace 卡片主要展示步骤名和成功/失败状态，不展开完整参数和结果。更详细的工具自然语言说明由 `humanizeToolCall()`、`humanizeToolResult()` 和 `show_tool_details_in_result` 相关逻辑控制。

### 发送时机

`handleChatNarrativeStream()` 会在 `ChatEventDone` 或流结束但已有最终内容时发送 Tool Trace 和 Agent Trace 卡片。

`handleChatStream()` 也会累积 `toolTraceSteps`，但卡片发送受 `narrativeMode` 条件保护。按当前路由，`progress_as_messages=true` 且 `progress_as_natural_language=true` 时会提前进入 `handleChatNarrativeStream()`，所以默认流式编辑模式通常只通过 `sender.SetToolCall()`、`sender.SetThinking()` 展示即时进度，不一定发送最终 Tool Trace 卡片。

## Reasoning Trace

Reasoning Trace 的用户可见卡片由这些函数渲染：

- `renderTelegramThinkingCard(content)`
- `renderTelegramSummaryCard(summary)`
- `renderTelegramProgressHistoryCard(parts)`

它们统一渲染为标题为 `Reasoning Trace` 的 Telegram HTML expandable blockquote。

需要强调：这些卡片不是 provider 原始隐藏 reasoning。

### 原始 ReasoningContent 的去向

agent 在 native stream 中会累积 provider chunk 的 `ReasoningContent`，在 direct response 路径中会读取 `resp.ReasoningContent`。这些内容会写入 provider message，并通过 `session.AddProviderMessage()` 保存到 session。

`session.AddProviderMessage()` 保留 `ReasoningContent`、`ToolCalls`、`ToolCallID`、`Name` 等结构化字段。相关测试也覆盖了 reasoning 和 tool call 字段的往返保留。

但是 `ChatEvent` 结构没有 `ReasoningContent` 字段，Telegram handler 也没有把原始 `ReasoningContent` 直接发送给用户。

### 进度事件过滤

agent 会发出类似 `Thinking... (round 1)`、`Thinking... (round N)` 的内部轮次提示。Telegram 侧的 `isInternalThinkingProgress()` 会过滤这些内容：

- 空内容。
- 可解析出 round number 的 `Thinking... (round N)`。
- `thinking`、`thinking..` 等占位文本。

因此，默认情况下这些内部轮次提示不会形成用户可见的 Reasoning Trace 卡片。

### LLM 进度摘要

当启用自然语言进度和 LLM 摘要时，handler 会收集每轮观察：

- tool call 的自然语言描述。
- 已发送的 progress history。
- 当前 round 信息。

在进入下一轮或结束时调用 `ProgressFeedback()` 生成用户可读的进度摘要，并通过 `renderTelegramSummaryCard()` 或相关格式化函数发送。这类 Reasoning Trace 是“当前进展摘要”，不是模型隐藏思维链。

## 配置影响

相关配置键在 `msg_gateway.telegram.*` 下：

- `chat_timeout_seconds`：每轮 Telegram chat 流式任务的超时时间。
- `progress_as_messages`：中间进度是否作为独立 Telegram 消息发送。
- `progress_as_natural_language`：中间步骤是否转换为自然语言进度；与 `progress_as_messages` 同时启用时进入 narrative stream。
- `progress_summary_with_llm`：每轮未完成时是否调用 LLM 生成进度摘要；实际主要在 narrative stream 中生效。
- `show_tool_details_in_result`：是否在最终回答前追加自然语言工具摘要。

配置组合对 trace 的影响：

| 配置组合 | 行为 |
| --- | --- |
| `progress_as_messages=false` | 主要通过流式消息编辑展示 thinking/tool 状态。 |
| `progress_as_messages=true`, `progress_as_natural_language=false` | 可发送简单进度消息，但默认最终 Tool Trace 卡片不一定发送。 |
| `progress_as_messages=true`, `progress_as_natural_language=true` | 进入 narrative stream，中间进度独立发送，结束时发送 Tool Trace/Agent Trace 卡片。 |
| 再启用 `progress_summary_with_llm=true` | round 边界生成用户可读的 Reasoning Trace 摘要。 |

## 风险点与改进建议

- `chat_sessions.json` 只保存绑定，不保存 session 内容；如果 session 文件被删，加载时会跳过该绑定，下一条消息会创建新 session。可以考虑记录跳过数量，便于排查。
- Tool result 目前按工具名回填到最近一个未完成 step，没有 tool call ID。重复同名工具调用大体可用，但遇到乱序或更复杂并发时不够强。
- Tool Trace 卡片目前偏摘要，只展示步骤名和状态。调试场景可能需要一个受配置控制的 sanitized args/result 展开模式。
- 默认流式编辑模式会累积 `toolTraceSteps`，但最终卡片发送受 narrative 条件限制。若产品预期是所有模式都发送 Tool Trace，需要调整发送条件。
- `Reasoning Trace` 这个名称容易被理解成隐藏思维链。当前实现实际是 progress trace 或 progress summary。若面向普通用户，建议 UI 文案进一步澄清。
- 建议补测试覆盖：chat session 持久化恢复、失效 session 自动重建、external reply anchor 回绑、tool trace 可见性分层、ReasoningContent 不直接出现在 Telegram trace 中。
