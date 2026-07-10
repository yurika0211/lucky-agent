# LA Feishu Channel 接入计划

本文档描述 LuckyAgent 接入飞书机器人渠道的实施计划。目标是在现有
`lh msg-gateway start` 网关体系下新增 `feishu` 平台，让飞书私聊和群聊消息可以进入
LuckyAgent agent runtime，并把最终回答回发到飞书。

## 当前落地状态

Phase 1 文本通道已经落地：

- `internal/gateway/feishu` 实现 HTTP event callback、URL challenge、verification token 校验和 tenant access token 缓存。
- 支持 schema 2.0 的 `im.message.receive_v1` 文本事件。
- 支持私聊、群聊 `mention|all|none` 触发、chat/user allowlist 和 mention 文本移除。
- 支持飞书文本消息发送与 reply API，接入 LuckyAgent session、通用命令和 `/lucky` 收集能力。
- `lh msg-gateway start --platform feishu` 已接入配置解析、凭证校验、adapter 注册和启动。

Phase 1 尚不支持：

- 加密事件；配置非空 `encrypt_key` 时启动会明确失败。
- 图片、文件、语音等附件收发。
- 飞书富文本、卡片和流式消息更新。

最小启动配置：

```bash
lh config set msg_gateway.platform feishu
lh config set msg_gateway.feishu.app_id cli_xxx
lh config set msg_gateway.feishu.app_secret your-app-secret
lh config set msg_gateway.feishu.verification_token your-verification-token
lh msg-gateway start --platform feishu
```

默认本地回调为 `http://127.0.0.1:6710/feishu/events`。飞书控制台需要配置公网 HTTPS URL，并通过反向代理转发到该地址。

## 现状基线

当前仓库已经有统一的消息网关抽象：

- 网关接口：`internal/gateway/gateway.go`
- 通用消息结构：`internal/gateway/types.go`
- CLI 启动入口：`internal/cli/lhcmd/commands.go` 的 `runMsgGatewayStart`
- 配置结构：`internal/config/config.go` 的 `MsgGatewayConfig`
- 可参考实现：
  - `internal/gateway/telegram`：自带 richer handler、流式消息、附件和会话映射。
  - `internal/gateway/qqofficial`：通用命令和会话 handler。
  - `internal/gateway/napcat`：协议 adapter + 复用 `qqofficial.Handler`。
  - `internal/gateway/weixin`：HTTP 轮询 adapter + plain gateway handler。

推荐首版 Feishu 采用和 NapCat 类似的方案：新增飞书 adapter，把飞书事件转换为
`gateway.Message`，然后复用 `qqofficial.Handler`。这样可以先获得 `/help`、`/chat`、
`/lucky`、session、RAG、memory、cron 等既有能力，避免为首版重复维护一套 handler。

## 接入目标

首版必须支持：

- `lh msg-gateway start --platform feishu` 启动飞书网关。
- 飞书事件订阅回调接收消息事件。
- 私聊消息直接进入 agent。
- 群聊默认只在 @机器人或回复机器人消息时触发。
- 文本消息转成 `gateway.Message.Text`。
- `/help`、`/chat`、`/lucky on/off/status/cancel` 等通用命令可用。
- agent 最终回答通过飞书消息 API 回发。
- 每个飞书会话稳定绑定 LuckyAgent session。
- 支持 allowlist、verification token、tenant access token 自动刷新。

首版可以暂缓：

- 飞书卡片渲染和交互按钮。
- 真正的流式编辑输出。
- 图片、文件、语音等附件下载和上传。
- 多租户 app 管理。
- 飞书事件加密的强制启用。可以预留配置，优先支持明文事件和 challenge 校验。

## 飞书侧准备

需要在飞书开放平台创建自建应用：

1. 创建应用并记录 `app_id`、`app_secret`。
2. 启用机器人能力。
3. 在事件订阅里配置回调 URL，例如：

```text
https://<public-domain>/feishu/events
```

本地开发可用内网穿透把本机监听地址暴露给飞书。生产环境建议在反向代理或网关层终止
HTTPS，再转发到 LuckyAgent 的本地监听地址。

4. 订阅消息事件，至少包括：

```text
im.message.receive_v1
```

5. 配置权限并发布应用。首版至少需要：

```text
im:message
im:message:send_as_bot
im:message:send_multi_as_bot
```

实际权限名称以飞书开放平台当前控制台为准，提交代码前需要用官方文档或控制台复核。

## 配置设计

在 `internal/config/config.go` 增加：

```go
type MsgGatewayFeishu struct {
    AppID             string   `json:"app_id,omitempty"`
    AppSecret         string   `json:"app_secret,omitempty"`
    VerificationToken string   `json:"verification_token,omitempty"`
    EncryptKey        string   `json:"encrypt_key,omitempty"`
    ListenAddr        string   `json:"listen_addr,omitempty"`
    Path              string   `json:"path,omitempty"`
    APIBaseURL        string   `json:"api_base_url,omitempty"`
    AllowedChats      []string `json:"allowed_chats,omitempty"`
    AllowedUsers      []string `json:"allowed_users,omitempty"`
    RemoveAt          bool     `json:"remove_at,omitempty"`
    GroupTriggerMode  string   `json:"group_trigger_mode,omitempty"`
}
```

挂到：

```go
type MsgGatewayConfig struct {
    ...
    Feishu MsgGatewayFeishu `json:"feishu,omitempty"`
}
```

默认值建议：

```json
{
  "msg_gateway": {
    "platform": "feishu",
    "feishu": {
      "listen_addr": "127.0.0.1:6710",
      "path": "/feishu/events",
      "api_base_url": "https://open.feishu.cn",
      "remove_at": true,
      "group_trigger_mode": "mention"
    }
  }
}
```

需要补齐 `ConfigManager.Set/Get` 支持的 key：

- `msg_gateway.feishu.app_id`
- `msg_gateway.feishu.app_secret`
- `msg_gateway.feishu.verification_token`
- `msg_gateway.feishu.encrypt_key`
- `msg_gateway.feishu.listen_addr`
- `msg_gateway.feishu.path`
- `msg_gateway.feishu.api_base_url`
- `msg_gateway.feishu.allowed_chats`
- `msg_gateway.feishu.allowed_users`
- `msg_gateway.feishu.remove_at`
- `msg_gateway.feishu.group_trigger_mode`

## 代码结构

新增包：

```text
internal/gateway/feishu/
  adapter.go
  config.go
  event.go
  auth.go
  message.go
  adapter_test.go
  auth_test.go
  event_test.go
```

建议职责划分：

- `config.go`：默认配置、路径规范化、allowlist、群聊触发策略。
- `auth.go`：tenant access token 获取、缓存和过期刷新。
- `event.go`：飞书 challenge、事件 envelope、`im.message.receive_v1` 解析。
- `message.go`：飞书文本、mention、reply、chat/user id 归一化。
- `adapter.go`：实现 `gateway.Gateway`，启动 HTTP callback server，发送消息。

首版不建议新增第三方 SDK。飞书首版只需要少量 HTTP API，直接用标准库更容易控制结构、
测试和错误处理。后续如果要完整支持事件加密、卡片和复杂 media，再评估官方 SDK。

## Adapter 行为

`feishu.Adapter` 实现：

```go
func (a *Adapter) Name() string
func (a *Adapter) Start(ctx context.Context) error
func (a *Adapter) Stop() error
func (a *Adapter) Send(ctx context.Context, chatID string, message string) error
func (a *Adapter) SendWithReply(ctx context.Context, chatID string, replyToMsgID string, message string) error
func (a *Adapter) IsRunning() bool
func (a *Adapter) SetHandler(handler gateway.MessageHandler)
```

启动流程：

```text
lh msg-gateway start --platform feishu
  -> load config
  -> validate app_id/app_secret/verification_token
  -> agent.New
  -> feishu.NewAdapter
  -> qqofficial.NewHandlerWithOptions(... PlatformName: "feishu")
  -> handler.SetDataDir($HOME/.luckyagent/data/feishu)
  -> adapter.SetHandler(handler.HandleMessage)
  -> gateway manager Register
  -> gateway manager Start("feishu")
```

HTTP callback 行为：

- `GET` 可返回健康状态，便于本地检查。
- `POST` 处理飞书事件回调。
- 如果请求是 challenge 校验，返回飞书要求的 challenge JSON。
- 校验 `verification_token`。
- 如果配置了 `encrypt_key`，解密后再解析事件；首版如果暂不实现加密，需要在启动日志和文档中明确。
- 只处理 `im.message.receive_v1`，其他事件忽略并返回成功。

消息发送行为：

- `Send` 调用飞书发送消息 API，以 `chat_id` 为 `receive_id`。
- `SendWithReply` 优先调用飞书 reply API；失败时降级为普通发送，并在日志中记录。
- 文本内容需要 JSON escape，并按飞书单条消息长度限制切分。
- agent 输出的 Markdown 首版按纯文本发送；二期再转换为飞书富文本或卡片。

## 消息转换

飞书事件转 `gateway.Message` 的建议映射：

```text
event.message.message_id -> Message.ID
event.message.chat_id    -> Message.Chat.ID
event.message.chat_type  -> Message.Chat.Type
event.sender.sender_id.open_id/user_id -> Message.Sender.ID
event.message.create_time -> Message.Timestamp
event.message.content.text -> Message.Text
```

聊天类型映射：

- `p2p` -> `gateway.ChatPrivate`
- `group` -> `gateway.ChatGroup`
- 其他未知类型 -> `gateway.ChatGroup` 或忽略，首版建议保守忽略并记录日志。

命令识别：

- 文本以 `/` 开头时设置 `IsCommand=true`。
- `Command` 保留 `/help` 这种形式，复用现有 handler 的 `commandKey` 逻辑。
- `Args` 是命令后的剩余文本。

群聊触发策略：

- `group_trigger_mode=mention`：只有 @机器人或回复机器人消息时触发。
- `group_trigger_mode=all`：群内所有文本消息都触发。
- `group_trigger_mode=none`：群消息不触发。

如果 `remove_at=true`，adapter 在进入 agent 前移除文本开头或正文中的机器人 mention。

allowlist：

- `allowed_chats` 匹配飞书 `chat_id`。
- `allowed_users` 匹配 sender 的 `open_id` 或 `user_id`，实现时至少支持配置中出现的任一种。

## 会话与回复链

首版复用 `qqofficial.Handler` 的持久化会话机制：

```text
$LUCKYAGENT_HOME/data/feishu/chat_sessions.json
```

session key 使用飞书 `chat_id`。私聊和群聊都按 chat 维度绑定一个 LuckyAgent session。

agent 回答前调用：

```go
RecordRecentChatTarget("feishu", msg.Chat.ID, msg.ID)
```

这样 cron/autonomy 类后续主动消息可以知道最近的飞书目标。若飞书 reply API 可稳定返回和接收
message_id，二期可实现 `gateway.ReceiptGateway`，增强外部回复锚点恢复能力。

## CLI 接入点

需要改动：

- `internal/cli/lhcmd/root_cmd.go`
  - `--platform` help 文案加入 `feishu`。
  - 如需命令行覆盖，可增加 `--feishu-app-id`、`--feishu-app-secret`、`--feishu-listen`、`--feishu-path`。
- `internal/cli/lhcmd/commands.go`
  - `msgGatewayStartOptions` 增加 Feishu 字段。
  - `resolveMsgGatewayStartOptions` 从 config 读取 Feishu 配置。
  - `validateMsgGatewayStartOptions` 校验 app id / secret / verification token。
  - `runMsgGatewayStart` 增加 `case "feishu"`。
  - `runConfigGet` 增加 Feishu key。
- `internal/config/config.go`
  - 增加结构、默认值、`Set`、深拷贝逻辑。
- `config.example.json`
  - 增加 `msg_gateway.feishu` 示例。

启动示例：

```bash
lh config set msg_gateway.platform feishu
lh config set msg_gateway.feishu.app_id cli_xxx
lh config set msg_gateway.feishu.app_secret xxx
lh config set msg_gateway.feishu.verification_token xxx
lh config set msg_gateway.feishu.listen_addr 127.0.0.1:6710
lh config set msg_gateway.feishu.path /feishu/events
lh msg-gateway start --platform feishu
```

## 测试计划

Focused tests：

```bash
go test ./internal/gateway/feishu
go test ./internal/config
go test ./internal/cli/lhcmd
```

建议测试用例：

- config 默认值和 `lh config set/get`。
- `validateMsgGatewayStartOptions` 缺少 Feishu credential 时失败。
- callback challenge 请求返回正确 JSON。
- verification token 不匹配时拒绝。
- `im.message.receive_v1` 文本事件转换为 `gateway.Message`。
- p2p 消息默认触发。
- group 消息在 mention 模式下未 @ 不触发。
- group 消息 @机器人后移除 mention 并触发。
- `allowed_chats` 和 `allowed_users` 生效。
- tenant token 缓存未过期时复用，过期后刷新。
- `Send` 调用正确 endpoint、query 和 JSON body。
- `SendWithReply` 优先 reply endpoint，失败时降级普通发送。

手工验证：

1. 使用内网穿透暴露 `127.0.0.1:6710/feishu/events`。
2. 在飞书开放平台完成 challenge 校验。
3. 私聊机器人发送 `hello`，确认 LA 回答。
4. 群聊 @机器人发送问题，确认只在触发条件满足时响应。
5. 发送 `/help`、`/session`、`/new`、`/lucky on`，确认通用命令可用。

## 分阶段落地

### Phase 1：最小可用文本通道

- 增加 config、CLI、adapter skeleton。
- 实现 tenant token、challenge、明文事件接收。
- 实现文本事件转换和文本发送。
- 复用 `qqofficial.Handler`。
- 补齐 focused tests。

验收标准：

- `lh msg-gateway start --platform feishu` 可以启动。
- 飞书私聊和群聊 @机器人能触发 agent。
- 文本最终回答能回到飞书。

### Phase 2：生产可用性

- 支持事件加密 `encrypt_key`。
- 支持 reply API 和 message receipt。
- 完善错误日志、请求超时、发送限流和消息切分。
- 补齐 runtime state 文件，类似 Telegram 的 gateway state。
- 补充 docs/channels/feishu 的部署说明和故障排查。

验收标准：

- 加密事件可用。
- 长回答不会因单条限制发送失败。
- 主动任务能投递到最近飞书会话。

### Phase 3：富能力

- 图片、文件附件下载到 `gateway.Attachment`。
- 发送本地图片和文件。
- 将 Markdown 转为飞书富文本 post。
- 可选支持飞书交互卡片展示 tool trace、进度和操作按钮。
- 评估是否实现 `gateway.StreamGateway`，用消息更新或卡片更新模拟流式输出。

## 主要风险

- 飞书事件回调要求公网 HTTPS，本地开发依赖内网穿透或反向代理。
- 飞书开放平台权限名和事件字段会随版本变化，提交实现前必须按当前控制台复核。
- 群聊 @ 解析和机器人 open_id 获取要实测，否则容易误触发或不触发。
- 复用 `qqofficial.Handler` 可以快速落地，但首版输出是 plain text，不会有 Telegram 那种 richer trace 展示。
- 如果企业启用事件加密，而首版未实现 decrypt，则只能用于未加密事件订阅；生产前应完成 Phase 2。

## 建议首个 PR 范围

首个 PR 只做 Phase 1，避免把卡片、附件、流式输出和加密都混在一起：

- `internal/gateway/feishu` 新包。
- `internal/config` 增加 Feishu 配置。
- `internal/cli/lhcmd` 增加平台分发。
- `config.example.json` 增加示例。
- `docs/channels/feishu/la-feishu-channel-plan.md` 保持本计划。
- focused tests 覆盖 config、CLI 和 Feishu adapter。
