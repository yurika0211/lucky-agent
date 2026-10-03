# 长任务可靠执行方案

## 目标和边界

直接对话的前台长任务和显式入队的后台任务共用检查点、工具操作日志和完成验收。每次执行是一个有轮数预算的片段；片段结束后保留任务身份、模型上下文和工具执行记录，再从检查点继续。

前台任务始终属于当前请求，保持原生或模拟流式输出与工具进度。取消、断线或调用方超时会停止执行；重启不会自动启动这些任务。需要无人在线继续执行的工作才使用 `autonomy` 后台队列。工具需配合 Go context 才能及时响应取消；不支持取消的旧工具可能到自身超时才返回。

## 前台默认行为和恢复

`agent.foreground.enabled=true` 默认开启。CLI、TUI、HTTP 和消息网关通过常规 Agent 配置进入同一条前台执行路径；心跳、cron、委派子任务和 autonomy 保持各自的执行预算。

- `agent.max_iterations=10` 是每段循环步数，包含模型请求、工具批次和验收阶段；到达上限会在原请求内续跑。
- `agent.timeout_seconds=60` 是单次模型请求期限。总任务另受 `agent.foreground.max_slices=48` 和 `max_total_seconds=3600` 约束。
- `agent.foreground.max_retries=3` 是整次预算内的额外失败重试次数；从 1 秒指数退避到最多 30 秒，0 可禁用。底层 provider 重试仍受模型单次期限和任务总期限约束。
- 工具任务的候选答案必须通过完成验收。普通无工具回答不增加第二次模型请求。预算耗尽、取消和验收失败会报告未完成，不再回退成普通聊天回答。
- 同一会话只允许一个前台任务执行，避免两个请求同时改写会话和恢复点；不同会话可并行。

记录保存到 `~/.luckyagent/runtime/foreground/<session-ID的SHA256>.json`，与 `runtime/autonomy_queue.json` 隔离。运行时 home 自定义时使用对应目录。原始输入、会话范围、工作目录、环境、上下文摘要和已确认工具结果都会恢复；新消息创建新任务，不会把新要求混入旧检查点。下次访问时压缩已完成记录，仅保留最近 10 条完成摘要，完整对话仍在 session；未完成任务的检查点不会被清理。

调用方更短的期限仍然优先：Telegram 使用 `msg_gateway.telegram.chat_timeout_seconds`（默认 600 秒），QQ Official 当前入口也是 10 分钟；HTTP 客户端、反向代理或网关的主动取消也会提前停止请求。需要更长在线运行时，应一并调整该入口期限。

在**原会话**发送下列普通文本即可操作，无需模型解释或生成工具调用：

- `查看前台任务`：查看最近 10 条记录及待核对操作。
- `继续前台任务`：恢复最近的未完成任务；也可用 `继续前台任务 tq-1` 指定记录。指定已完成记录时直接返回已保存答案，不会重新执行。显式恢复未完成任务会授予新的有界预算。CLI 一次性聊天会新建会话，恢复时需在 REPL/TUI 中先切换回原会话，或通过 HTTP 使用原 session_id。
- 操作结果不明时先核对实际系统状态，再发送明确决定。例如已完成：`处理前台操作 {"task_id":"tq-1","operation_id":"step-1-1","resolution":"completed","result":"已检查目标文件，内容和预期一致"}`。
- 已确认可以重做：`处理前台操作 {"task_id":"tq-1","operation_id":"step-1-1","resolution":"retry"}`。随后再发送继续指令。处理指令仅记录决定，不会自动运行工具。

流式 `content` 是过程文本，成功终态仅由 `done`（HTTP SSE 的 `complete`）表示；验收前不会发送成功终态。进度、工具结果和失败继续使用已有事件协议。

系统不承诺任意外部 API 的 exactly-once：外部操作和本地文件无法进行同一事务。工具执行前先持久化意图，完成后持久化结果。如果崩溃发生在两者之间，任务必须阻塞并核对操作结果，不能盲目重放。

## 状态和调度

- `ready`：等待调度或退避时间；`in_progress`：已持久化领取；`blocked`：预算耗尽、验收阻塞或操作结果不确定；`done`：已验收完成。
- 每次领取增加执行代号；检查点和结果写入必须匹配当前代号，过期 worker 无权覆盖新状态。
- 后台每个任务拥有自己的 session；worker 可以复用，session 不在不同任务间复用。前台沿用用户选择的会话。
- 重启恢复 `in_progress` 为 `ready`，保留检查点和工具日志。
- 轮数/片段时间用尽属于续跑；执行错误采用有上限的指数退避。全任务另有执行片段数和累计墙钟期限上限。
- 调度器、heartbeat、手动 spawn 共用领取和 worker 预留逻辑，防止同一个 worker 被重复分配。

## 检查点和副作用

检查点保存模型消息、当前工具批次、已用 token、候选答案和验收阶段。每次模型返回、每个工具调用前后以及阶段切换时写入。写入使用临时文件、同步和原子替换；持久化失败时停止后续操作。

恢复规则：有结果的工具调用复用记录；尚未开始的调用继续；已经开始但没有结果的调用转为 `blocked`。操作者核对后，提供已完成结果或明确允许重试，再解除阻塞。工具错误也被记录，不能被当成成功证据。

## 完成验收和上下文

候选答案不直接标记完成。独立的无工具验收请求根据原始任务、显式验收条件、工具记录和候选答案，返回 complete / continue / blocked。complete 必须给出证据；continue 将不足之处送回执行循环；blocked 保留原因。验收本身也是可恢复阶段。模型验收是语义判断，不能替代测试或外部系统的业务校验。

每次模型调用前重新控制上下文大小，保留原始目标、恢复指令和最近完整工具轮次；完整历史和工具证据仍留在持久化检查点中。普通聊天的工具循环保护只在重复且没有新证据时触发，不把持续推进视为卡死。

## 验证

覆盖：重启接续、不重复执行已有结果、未决副作用阻塞及人工核对、检查点写入失败、退避及上限、跨预算续跑、任务会话隔离、验收不通过不完成、并发调度互斥、停止取消、SSE 超过普通写超时。前台额外覆盖同步/原生流式/模拟流式共用执行、同会话互斥、总期限、慢消费者背压、网关包装后的原文指令与发送者隔离、完成答案重取、完成记录压缩。配置示例和 GitHub Pages 字段说明随实现同步。

实现后的验证命令：

```bash
go test ./internal/autonomy ./internal/agent ./internal/config ./internal/tool ./internal/session ./internal/server -count=1
go test ./internal/cli/lhcmd ./internal/gateway/telegram ./internal/gateway/qqofficial ./internal/gateway/napcat ./internal/gateway/feishu -count=1
go test -race ./internal/autonomy ./internal/agent ./internal/server ./internal/session ./internal/config -run 'Test(Foreground|Recovery|Durable|ProgressingTool|LongChat|AutonomyRecovery)' -count=1
go test ./internal/tool ./internal/agent -run 'Test(Autonomy|ToolExecutionGuard|Durable|ProgressingTool)' -count=1
go build -o /tmp/luckyagent-foreground-check ./cmd/la
```

上述检查已通过。模型完成验收不提供业务正确性的数学保证；队列文件由单个运行时进程持有，不支持多个进程同时写同一队列。进程恢复时可恢复已确认的工具结果；中断中的外部副作用仍须由操作者核对。`resolve` 和人工 `complete` 的模型调用必须匹配当前用户消息中的明确 JSON 指令，不能由模型自行编造操作结果来解除阻塞。
