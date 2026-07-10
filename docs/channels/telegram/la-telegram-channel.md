# LA Telegram Channel

本文说明 LuckyAgent 当前 Telegram 渠道的实现状态、启动方式、配置项、消息链路、命令系统、会话绑定、附件处理、流式输出和运行状态文件。

## 结论

Telegram 渠道由 `internal/gateway/telegram` 实现，是 LuckyAgent `msg-gateway` 体系下的一个平台 adapter。

核心组件：

| 组件 | 文件 | 作用 |
| --- | --- | --- |
| `Adapter` | `internal/gateway/telegram/adapter.go` | 对接 Telegram Bot API，负责轮询、收发消息、附件下载、消息分片、流式编辑。 |
| `Handler` | `internal/gateway/telegram/handler.go` | 处理命令、会话、Lucky 收集、多模态输入、agent 调用和进度展示。 |
| `Config` | `internal/gateway/telegram/config.go` | Telegram adapter 级配置。 |
| commands | `internal/gateway/telegram/commands.go` | Bot 命令列表和帮助文案。 |
| runtime state | `internal/gateway/runtime_state.go` | 写入 Telegram 网关跨进程运行状态快照。 |

启动命令：

```bash
lh msg-gateway start --platform telegram
```

也可以传入 token：

```bash
lh msg-gateway start --platform telegram --token <telegram-bot-token>
```

## 配置项

配置位于 `config.json` 的 `msg_gateway.telegram`。

示例来自 `config.example.json`：

```json
{
  "msg_gateway": {
    "platform": "telegram",
    "start_all": false,
    "api_addr": "127.0.0.1:9090",
    "telegram": {
      "token": "",
      "proxy": "",
      "chat_timeout_seconds": 600,
      "progress_as_messages": true,
      "progress_as_natural_language": false,
      "progress_summary_with_llm": false,
      "show_tool_details_in_result": false
    }
  }
}
```

字段说明：

| 字段 | 默认值 | 说明 |
| --- | --- | --- |
| `token` | 空 | Telegram Bot Token。启动 Telegram 渠道必须提供。 |
| `proxy` | 空 | Telegram API 代理，支持 `http`、`https`、`socks5`、`socks5h`。 |
| `chat_timeout_seconds` | `600` | 单次 Telegram 对话流的总超时时间。 |
| `progress_as_messages` | `true` | 是否把中间思考/工具步骤作为独立消息展示。 |
| `progress_as_natural_language` | `false` | 是否把中间步骤转成自然语言进度消息。 |
| `progress_summary_with_llm` | `false` | 是否让 LLM 为每轮未完成进度生成总结。 |
| `show_tool_details_in_result` | `false` | 最终回答前是否附加工具步骤摘要。 |

可用 `lh config set` 写入：

```bash
lh config set msg_gateway.platform telegram
lh config set msg_gateway.telegram.token <telegram-bot-token>
lh config set msg_gateway.telegram.proxy socks5://127.0.0.1:7890
lh config set msg_gateway.telegram.progress_as_messages true
lh config set msg_gateway.telegram.progress_as_natural_language false
```

兼容别名：

```bash
lh config set msg_gateway.telegram.show_tool_chain true
```

这个别名实际写入的是 `show_tool_details_in_result`。

## Adapter 配置

`telegram.Config` 是 adapter 内部配置：

| 字段 | 默认值 | 说明 |
| --- | --- | --- |
| `Token` | 空 | Telegram Bot Token。 |
| `Proxy` | 空 | Telegram API 代理。 |
| `AllowedChats` | 空 | 聊天白名单，空表示允许所有 chat。当前 CLI 启动路径没有从 `msg_gateway.telegram` 暴露该字段。 |
| `AdminIDs` | 空 | 管理员用户 ID 列表。当前主要是 adapter 能力。 |
| `MaxMessageLen` | `4000` | 单条消息切分阈值，硬限制不超过 Telegram 4096。 |
| `RateLimit` | `1` | 每个 chat 每秒消息速率。 |
| `PollTimeout` | `30` | long polling 超时时间。 |
| `AttachmentDownloadLimit` | `1 GiB` | 入站附件本地缓存大小上限。 |
| `AttachmentDownloadTimeout` | `30s` | 附件下载超时时间。 |

当前 `lh msg-gateway start --platform telegram` 创建 adapter 时只传：

```go
telegram.Config{
    Token: opts.Token,
    Proxy: cfg.MsgGateway.Telegram.Proxy,
}
```

因此 `AllowedChats`、`AdminIDs`、`MaxMessageLen` 等使用 adapter 默认值，除非后续启动路径补配置映射。

## 启动流程

CLI 入口在 `internal/cli/lhcmd/commands.go`：

```text
runMsgGatewayStart
  -> config.NewManager / Load
  -> resolveMsgGatewayStartOptions
  -> agent.New
  -> a.MsgGateway()
  -> telegram.NewAdapter
  -> telegram.NewHandler
  -> handler.SetDataDir($HOME/.luckyagent/data/telegram)
  -> adapter.SetHandler(handler.HandleMessage)
  -> gateway manager Register
  -> gateway manager Start("telegram")
```

启动后会打印：

```text
Telegram 网关已启动
```

Telegram adapter 的 `Start` 会：

1. 校验 token。
2. 根据 `proxy` 创建 HTTP client。
3. 创建 Telegram Bot API client。
4. 读取 bot username。
5. 注册 bot commands。
6. 启动 long polling goroutine。

## 运行状态文件

Telegram 启动时会写入跨进程状态：

```text
$LUCKYAGENT_HOME/runtime/telegram_gateway_state.json
```

对应结构：

| 字段 | 说明 |
| --- | --- |
| `platform` | 固定为 `telegram`。 |
| `pid` | 当前网关进程 PID。 |
| `registered` | 是否已注册到 gateway manager。 |
| `connected` | 当前 gateway 是否运行中。 |
| `messages_sent` | 已发送消息数。 |
| `messages_received` | 已接收消息数。 |
| `errors` | 错误计数。 |
| `updated_at` | 更新时间。 |

启动后每 2 秒同步一次状态。

## 消息接收链路

入站消息从 Telegram update 转换成 LuckyAgent 的 `gateway.Message`：

```text
Telegram Update
  -> Adapter.processUpdate
  -> Adapter.convertMessage
  -> Handler.HandleMessage
  -> Handler.dispatchChatAsync / handleCommand
  -> Agent ChatWithSessionStreamInput 或 ChatWithSessionInput
```

`Adapter.processUpdate` 的规则：

- 只处理 `update.Message`。
- 先检查 `AllowedChats`。
- 私聊消息直接进入 handler。
- 群聊、超群、频道消息只有在满足下面条件时才响应：
  - 消息里 mention 了 bot；
  - 或者回复的是 bot 自己发出的消息。
- 群聊触发后会移除文本里的 `@botusername`。
- 群聊消息会设置：
  - `IsGroupTrigger=true`
  - `TriggerType=mention` 或 `reply`

`convertMessage` 会提取：

- chat id / type / title / username；
- sender id / username / first name / last name；
- text 或 caption；
- command 和 command args；
- reply message；
- attachments。

如果用户只发附件没有文本，会构造描述文本，例如：

```text
[用户发送了一张图片]
[用户发送了一段语音]
[用户发送了文件: xxx.pdf]
```

## 会话绑定

Telegram handler 内部维护：

```text
chatID -> sessionID
```

持久化文件：

```text
$LUCKYAGENT_HOME/data/telegram/chat_sessions.json
```

相关行为：

- 每个 chat 默认绑定一个 LuckyAgent session。
- 第一次收到 chat 消息时，如果没有 session，会创建新 session。
- `/new` 会为当前 chat 创建新 session。
- `/reset` 会重置当前 chat session。
- `/session` 查看或切换当前 session。
- `/sessions` 列出最近 session。
- `/resume <title|id>` 恢复已有 session。
- `/rename <title>` 重命名当前 session。

如果用户回复某条由外部任务或 agent 发出的 Telegram 消息，handler 会尝试通过 `ResolveExternalReplyAnchor("telegram", chatID, messageID)` 找回对应 session，并把当前 chat 重新绑定到该 session。

群聊里会在输入前加发送者显示名：

```text
[Alice]: 用户原始消息
```

这样 agent 能知道群聊中是谁在发言。

## 命令系统

命令列表在 `internal/gateway/telegram/commands.go`，启动时会注册到 Telegram Bot API。

基础命令：

| 命令 | 说明 |
| --- | --- |
| `/start` | 欢迎信息。 |
| `/help` | 帮助。 |
| `/chat <message>` | 显式发送一条消息给 agent。 |
| `/lucky [on|off|status|cancel]` | 多段消息收集。 |

系统命令：

| 命令 | 说明 |
| --- | --- |
| `/review` | 查看 workspace 状态。 |
| `/init` | 查看初始化状态。 |
| `/config [list|get]` | 查看配置。 |
| `/version` | 查看 runtime 版本。 |
| `/model [name]` | 查看或切换模型。 |
| `/models` | 列出可用模型。 |
| `/soul` | 查看 SOUL 信息。 |
| `/tools` | 列出工具。 |
| `/skills` | 列出 skills。 |
| `/mcp <name> <url> [api_key]` | 连接 MCP server。 |
| `/approve <tool>` | 自动批准工具。 |
| `/deny <tool>` | 拒绝工具。 |
| `/cron ...` | 管理定时任务。 |
| `/watch ...` | 管理文件 watch。 |
| `/dashboard [status]` | 查看 dashboard 状态。 |
| `/msg_gateway [status]` | 查看消息网关状态。 |
| `/rag ...` | 管理 RAG。 |
| `/context` | 查看上下文窗口状态。 |
| `/fc ...` | 查看 function calling 信息。 |
| `/embedder ...` | 管理 embedding 模型。 |
| `/metrics` | 查看指标。 |
| `/health` | 健康检查。 |

学习命令：

| 命令 | 说明 |
| --- | --- |
| `/learn` | 学习模式命令入口。 |
| `/learn_start <course>` | 开始或恢复课程。 |
| `/learn_current` | 查看当前学习模块。 |
| `/learn_lab` | 查看当前实验。 |
| `/learn_submit <evidence>` | 提交实验凭证。 |
| `/learn_progress` | 查看学习进度。 |

记忆和会话命令：

| 命令 | 说明 |
| --- | --- |
| `/remember <content>` | 写入中期记忆。 |
| `/remember_long <content>` | 写入长期记忆。 |
| `/recall <query>` | 搜索记忆。 |
| `/memstats` | 查看记忆统计。 |
| `/memdecay` | 衰减低权重记忆。 |
| `/promote <memory_id>` | 提升记忆层级。 |
| `/profile [list|switch]` | 管理 profile。 |
| `/reset` | 重置会话。 |
| `/history` | 查看对话历史。 |
| `/session [title|id]` | 查看或切换 session。 |
| `/sessions` | 列出最近 session。 |
| `/resume <title|id>` | 恢复已有 session。 |
| `/rename <title>` | 重命名当前 session。 |
| `/new` | 新建 session。 |
| `/stop` | 停止当前任务。 |
| `/status` | 查看 bot 状态。 |
| `/restart` | 重启 bot gateway。 |

命令名支持部分兼容归一化：

| 输入 | 归一化后 |
| --- | --- |
| `msg-gateway` | `msg_gateway` |
| `remember-long` | `remember_long` |
| `learn-start` | `learn_start` |
| `learn-current` | `learn_current` |
| `learn-lab` | `learn_lab` |
| `learn-submit` | `learn_submit` |
| `learn-progress` | `learn_progress` |

## Lucky 多段消息收集

`/lucky` 用于把多条 Telegram 消息收集成一次 agent 输入。

流程：

```text
/lucky on
  -> 后续普通消息不立即交给 agent
  -> collector 按 chat key 收集文本、附件和分段
/lucky status
  -> 查看当前已收集段数和附件数
/lucky off
  -> 合并成 UserTurnInput
  -> 统一 dispatch 给 agent
/lucky cancel
  -> 丢弃当前收集内容
```

collector key：

```text
telegram|chat:<chatID>
```

`/lucky off` 之后，handler 会把合并后的 `RoutingText` 和附件重新构造成一次正常消息，再进入 `dispatchChatAsync`。

## Agent 调用路径

普通消息和 `/chat` 最终进入 `handleChat`。

主要流程：

```text
handleChat
  -> RecordRecentChatTarget("telegram", chatID, messageID)
  -> beginChatTask
  -> ReactToMessage 👍
  -> getSessionID(chatID)
  -> 群聊加发送者名
  -> 注入 telegramMediaDeliveryGuidance
  -> 简单本地检查任务走 sync
  -> 其他任务优先走 stream
```

流式优先级：

1. 如果 `progress_as_messages=true` 且 `progress_as_natural_language=true`，走 `handleChatNarrativeStream`。
2. 否则先尝试 `Adapter.SendStream`，成功则走 `handleChatStream`。
3. 如果流式不可用，回退到 `handleChatSync`。

agent 调用接口：

| 模式 | 调用 |
| --- | --- |
| 流式 | `ChatWithSessionStreamInput(ctx, sessionID, input)` |
| 同步 | `ChatWithSessionInput(ctx, sessionID, input)` |

如果返回 `session not found`，handler 会重置当前 chat session 后重试。

## 进度输出模式

Telegram 支持三种主要展示模式。

### 默认流式编辑

配置：

```json
{
  "progress_as_messages": true,
  "progress_as_natural_language": false
}
```

行为：

- 创建一条可编辑的流式消息。
- `ChatEventContent` 会追加到同一条消息。
- thinking / tool call 可能作为独立进度卡片或编辑状态展示。
- 最终结果替换/完成流式消息。

### 独立自然语言进度

配置：

```json
{
  "progress_as_messages": true,
  "progress_as_natural_language": true
}
```

行为：

- 不使用同一条占位消息承载最终结论。
- 中间步骤作为独立消息发送。
- 最终结论作为最后一条新消息发送，避免结论被编辑回顶部旧消息。

### LLM 进度总结

配置：

```json
{
  "progress_as_messages": true,
  "progress_as_natural_language": true,
  "progress_summary_with_llm": true
}
```

行为：

- 每轮未完成时收集工具调用和工具结果观察。
- 调用 agent 的 `ProgressFeedback` 生成一条自然语言进度反馈。
- 用于长任务的阶段性说明。

## ChatEvent 处理

Telegram handler 消费 agent stream 事件：

| Event | Telegram 行为 |
| --- | --- |
| `ChatEventThinking` | 展示 thinking 或阶段进度。 |
| `ChatEventToolCall` | 展示工具调用进度，记录 trace step。 |
| `ChatEventToolResult` | 更新工具 trace，必要时生成人类可读工具结果摘要。 |
| `ChatEventContent` | 累积最终正文；非 narrative 模式下追加到流式消息。 |
| `ChatEventDone` | 发送最终结果，必要时发送工具 trace card / agent trace card。 |
| `ChatEventError` | 展示超时、取消或错误。 |

超时和取消文案：

```text
⏱ 请求超时
🛑 当前任务已停止
```

## 附件处理

Telegram adapter 会提取这些附件：

| Telegram 类型 | LuckyAgent 类型 |
| --- | --- |
| photo | image |
| voice | audio |
| audio | audio |
| video | video |
| animation / GIF | video |
| video note | video |
| static sticker | image |
| animated sticker | document |
| document | document |

附件处理流程：

```text
Telegram file_id
  -> bot.GetFileDirectURL
  -> 下载到本地缓存
  -> gateway.Attachment.FilePath
  -> agent.MultimodalUserTurnInput
```

默认缓存目录：

```text
$HOME/.luckyagent/workspace/downloads/telegram/attachments
```

如果无法取得 home，则 fallback 到：

```text
/tmp/luckyagent/workspace/downloads/telegram/attachments
```

默认下载限制是 `1 GiB`，默认下载超时 `30s`。

## 出站消息

Telegram adapter 支持：

- 普通文本发送；
- reply 发送；
- HTML rich text；
- 图片发送；
- document 发送；
- typing loop；
- message reaction；
- stream sender 实时编辑。

文本发送会先走 `formatTelegramRichText` 并使用 Telegram HTML parse mode。如果 rich text 发送失败，会回退到 plain text。

长消息会按 `MaxMessageLen` 切分，默认 4000，且不超过 Telegram 4096 字符限制。代码块会尽量修复 fence，避免切分后格式破坏。

出站媒体响应会通过 `resolveOutboundMediaResponse` 解析。如果最终回答包含可发送媒体，handler 会先完成文本占位，再发送媒体文件。

## 群聊行为

群聊默认不会响应所有消息。

触发条件：

- mention bot；
- 回复 bot 消息。

触发后：

- 去掉 `@botusername`；
- 标记 `IsGroupTrigger=true`；
- 设置触发类型；
- 将发送者名字写入 agent 输入；
- 使用当前 group chat 的 session。

这避免机器人在群里监听所有普通聊天内容。

## 停止任务

handler 内部维护每个 chat 的当前任务：

```text
chatID -> chatTask
```

`/stop` 会停止当前 chat 的任务。正在执行的流式事件循环会收到 context canceled，并向 Telegram 返回：

```text
🛑 当前任务已停止
```

## 当前边界

当前 Telegram 渠道的边界：

- Telegram 使用 long polling，不是 webhook。
- CLI 启动路径只把 `token` 和 `proxy` 映射到 adapter。
- `AllowedChats` 和 `AdminIDs` 在 adapter 配置中存在，但当前 `msg_gateway.telegram` 配置没有完整暴露。
- 附件会尽量下载到本地，但下载失败不会阻断消息进入 handler；附件可能只有 `FileURL` 没有 `FilePath`。
- 群聊只响应 mention 或 reply，不会全量监听。
- `tool` 进度展示依赖 agent stream event；同步路径只返回最终结果。
- runtime state 是状态快照，不是进程守护或高可用机制。

## 相关文件

- `internal/gateway/telegram/adapter.go`
- `internal/gateway/telegram/handler.go`
- `internal/gateway/telegram/commands.go`
- `internal/gateway/telegram/config.go`
- `internal/gateway/telegram/formatting.go`
- `internal/gateway/telegram/tool_nl.go`
- `internal/gateway/telegram/outbound_media.go`
- `internal/gateway/runtime_state.go`
- `internal/cli/lhcmd/commands.go`
- `internal/config/config.go`
- `config.example.json`
