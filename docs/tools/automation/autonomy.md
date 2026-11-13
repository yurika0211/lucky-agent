# autonomy Tools

LuckyAgent 的 autonomy 工具组用于管理后台任务队列、worker、heartbeat 和主动执行能力。它包含一个模型可见的统一入口 `autonomy`，以及多个隐藏的底层 `autonomy_*` 工具。

这些工具属于运行时调度和后台执行，不是普通文件或网络查询工具。

## 运行时配置

后台 `autonomy` worker 的配置位于 `config.json` 的 `autonomy` 段：

```bash
lh cfg s autonomy.enabled true
lh cfg s autonomy.worker.max_iterations 100
lh cfg s autonomy.worker.timeout_seconds 180
lh cfg s autonomy.worker.auto_approve false
lh cfg s autonomy.pool.max_workers 4
lh cfg s autonomy.pool.auto_scale true
lh cfg s autonomy.heartbeat.interval_seconds 600
lh cfg s autonomy.heartbeat.active_start 8
lh cfg s autonomy.heartbeat.active_end 22
```

可配置的范围包括：

- `autonomy.worker.*`：worker 的 Agent Loop 迭代次数、超时、自动批准、重复调用限制、纯工具轮次限制、重复 URL 限制和隐藏工具。
- `autonomy.queue_buffer`：任务队列缓冲区大小。
- `autonomy.pool.*`：最大 worker 数、结果缓冲区、是否自动扩容和最小 worker 数。
- `autonomy.heartbeat.*`：`passive`/`proactive` 模式、轮询间隔、活动时间窗口和每次最多处理的任务数。
- `autonomy.recovery.*`：重启恢复、错误重试、跨预算续跑及全任务预算。

这些配置在 Agent 创建时装配到 `AutonomyKit`，修改后需要重启 Agent 进程。

## 长任务执行与恢复

后台任务现在按有界片段执行。`autonomy.worker.timeout_seconds` 默认 300 秒，`max_iterations` 默认 300，均限制一个片段。片段预算用尽会保存检查点，自动重新调度同一个任务；不会从原始提示重新开始。

| 配置 | 默认值 | 含义 |
| --- | --- | --- |
| `autonomy.recovery.resume_on_start` | `true` | 启动时恢复已入队任务；这与 `autonomy.enabled` 控制的普通自动启动分开。设为 false 可在启动后先检查队列。 |
| `autonomy.recovery.max_retries` | `3` | 执行错误最多自动重试 3 次；0 禁止自动重试。预算续跑不计为失败重试。 |
| `autonomy.recovery.retry_initial_seconds` | `5` | 首次失败重试等待时间。 |
| `autonomy.recovery.retry_max_seconds` | `300` | 指数退避上限。 |
| `autonomy.recovery.max_slices` | `48` | 含失败尝试在内的执行片段上限。 |
| `autonomy.recovery.max_total_seconds` | `14400` | 从首次执行开始的总墙钟期限，包含退避及停机时间。 |

任务拥有独立的 session，worker 不在不同任务之间共享聊天历史。检查点和工具执行日志写入 `~/.luckyagent/runtime/autonomy_queue.json`。写入失败时停止推进；检查点采用同步写入和原子替换。一个队列文件应由一个运行时进程持有，不支持多个独立进程同时写入。

每次模型调用前控制上下文大小；必要时生成包含已完成工作、路径、决策和待办的进度摘要。完整对话和工具日志保留在检查点中。候选答案还要经过独立的无工具验收请求：只有完成原始任务及验收条件，且有证据支持，才会变为 `done`。验收可要求继续工作或标记 `blocked`。模型验收仍是语义判断，重要业务条件应写成可由测试、查询或产物检查证明的验收条件。

重启后，已完成工具调用复用持久化结果；未开始的调用继续执行。**已经开始但没有可靠结果的工具调用不会自动重复**，任务进入 `blocked`。这覆盖崩溃和可识别的工具取消/超时。外部系统和本地检查点无法共同提交事务，因此系统不承诺任意外部操作 exactly-once。第三方工具还须配合取消信号；不响应取消的工具可能延迟停止。

### 入队和检查

```json
{
  "action": "add",
  "title": "完成项目改动",
  "description": "完成指定改动，执行相关测试并给出结果",
  "acceptance_criteria": ["指定功能已实现", "相关测试通过，并记录实际命令和结果"],
  "idempotency_key": "project-change-001"
}
```

`action=list` 展示 `session_id`、`attempts`、`retries`、`continuations`、`next_run_at`、`checkpoint_at`、`verified`、`verification` 和 `unresolved_operations`。`action=report` 返回最终结果与验收说明。正常续跑和等待重试不会发送“任务完成”通知。

### 核对不确定的外部操作

先用 `action=list,state=blocked` 查看 `unresolved_operations`。人工核对目标系统后，直接将以下 JSON 作为当前用户消息发送（或通过受信任的内部工具接口调用）。模型调用的核对字段必须与当前用户消息完全一致；后台 worker 不能自行核对自己的未决操作：

```json
{"action":"resolve","task_id":"tq-1","operation_id":"step-2-1","resolution":"completed","result":"已核对：消息发送成功，目标系统 message_id=123"}
```

如果已经确认可以安全重做：

```json
{"action":"resolve","task_id":"tq-1","operation_id":"step-2-1","resolution":"retry"}
```

核对完所有未决操作，再调用：

```json
{"action":"unblock","task_id":"tq-1"}
```

`unblock` 保留进度，显式授予一组新的有界执行预算。不允许直接解除仍有未决操作的任务。人工 `complete` 也要求当前用户消息包含完全匹配的 JSON（action、task_id、result），且不能覆盖正在运行或存在未决操作的任务。详见 [设计方案](../../long-task-reliability.md)。

## 工具定义

实现位置：

- `internal/tool/autonomy_service.go`
- `internal/autonomy`

注册位置：

- `AutonomyToolService.RegisterTools`

## 工具列表

| 工具 | 权限 | HiddenFromModel | 说明 |
| --- | --- | --- | --- |
| `autonomy` | `PermApprove` | false | 高层统一入口。 |
| `autonomy_queue_add` | `PermAuto` | true | 添加后台任务。 |
| `autonomy_queue_list` | `PermAuto` | true | 列出队列任务。 |
| `autonomy_queue_update` | `PermAuto` | true | 更新任务状态。 |
| `autonomy_worker_spawn` | `PermApprove` | true | 为指定任务启动 worker。 |
| `autonomy_worker_list` | `PermAuto` | true | 列出 worker 状态。 |
| `autonomy_heartbeat_trigger` | `PermAuto` | true | 手动触发 heartbeat cycle。 |
| `autonomy_status` | `PermAuto` | true | 查看 autonomy 系统状态。 |

这些工具的 `Category` 都是 `CatDelegate`，`Source` 都是 `builtin`。

## autonomy 统一入口

`autonomy` 参数：

| 参数 | 用途 |
| --- | --- |
| `action` | 操作名。 |
| `title` | `add` 时的任务标题。 |
| `description` | `add` 时的任务详情。 |
| `acceptance_criteria` | `add` 时的字符串数组，指定可验证的完成条件。 |
| `operation_id` / `resolution` | `resolve` 时核对未决工具操作；resolution 为 completed 或 retry。 |
| `priority` | `add` 时的优先级：`low`、`normal`、`high`、`critical`。 |
| `tags` | `add` 时的标签。 |
| `dry_run` | `add`、`spawn`、`heartbeat`、`scale_up`、`scale_down`、`set_workers` 时只预览，不改变 runtime。 |
| `start_if_needed` | 写类动作是否允许隐式启动 autonomy runtime，默认 `true`。 |
| `idempotency_key` | `add` 时的幂等键，用于防止重复入队。 |
| `state` | `list` 时过滤状态：`ready`、`in_progress`、`blocked`、`done`。 |
| `task_id` | update/complete/fail/block/unblock/spawn 目标任务 ID。 |
| `count` | scale_up/scale_down/set_workers 的 worker 数。 |
| `limit` | report 输出数量限制。 |
| `result` | complete 的结果文本。 |
| `error` | fail 的错误文本。 |
| `reason` | block 的阻塞原因。 |
| `retry` | fail 时是否重试。 |

## action 分发

`HandleAutonomy` 支持以下 action 和别名：

| action | 分发到 |
| --- | --- |
| 空字符串、`status` | `HandleStatus` |
| `add`、`enqueue`、`queue_add` | `HandleQueueAdd` |
| `list`、`queue`、`queue_list` | `HandleQueueList` |
| `report`、`outputs`、`results` | `HandleReport` |
| `update`、`queue_update` | `HandleQueueUpdate` |
| `complete`、`fail`、`block`、`unblock` | `HandleQueueUpdate` |
| `resolve` | 核对未决工具操作；保留任务 blocked 状态，等待显式 unblock。 |
| `workers`、`worker_list` | `HandleWorkerList` |
| `spawn`、`worker_spawn`、`run` | `HandleWorkerSpawn` |
| `heartbeat`、`trigger`、`heartbeat_trigger` | `HandleHeartbeatTrigger` |
| `scale_up`、`scaleup`、`workers_add` | `HandleScaleUp` |
| `scale_down`、`scaledown`、`workers_remove` | `HandleScaleDown` |
| `set_workers`、`workers_set` | `HandleSetWorkers` |

非法 action 返回：

```text
invalid autonomy action "<action>" (use status, add, list, report, update, complete, fail, block, unblock, workers, spawn, heartbeat, scale_up, scale_down, set_workers)
```

统一入口输出会额外包含：

```json
{
  "input_action": "enqueue",
  "canonical_action": "add"
}
```

底层 handler 输出仍保留自身 `action` 字段；`canonical_action` 用于日志、TUI 和网关侧稳定聚合。

## ensureStarted 行为

部分操作会调用 `ensureStarted()`：

- queue add
- worker spawn
- heartbeat trigger
- scale up
- set workers

逻辑：

1. 如果 service 或 tools 为空，返回 `autonomy service not initialized`。
2. 如果没有 `ensureStart` 函数，直接通过。
3. 如果 kit 已 started，直接通过。
4. 否则调用 `ensureStart()`。

这意味着部分动作会自动启动 runtime autonomy kit。

写类动作支持：

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `dry_run` | `false` | 返回计划，不入队、不 spawn、不触发 heartbeat、不扩缩容。 |
| `start_if_needed` | `true` | runtime 未启动时是否允许工具隐式启动。 |

当工具可能启动 runtime 时，输出包含：

```json
{
  "runtime_started_before": false,
  "runtime_started_by_tool": true
}
```

如果传入 `start_if_needed=false` 且 runtime 未启动，写类动作返回：

```text
autonomy runtime is not started; set start_if_needed=true or start autonomy explicitly
```

`dry_run=true` 时不会调用 `ensureStart`，输出中的 `would_start_runtime` 表示如果真实执行是否会启动 runtime。

## add dry-run 和幂等

`add` 支持 `dry_run`：

```json
{
  "action": "add",
  "title": "整理项目工具文档",
  "dry_run": true
}
```

返回示例：

```json
{
  "ok": true,
  "action": "add",
  "canonical_action": "add",
  "dry_run": true,
  "would_start_runtime": true,
  "runtime_started_before": false,
  "runtime_started_by_tool": false,
  "task": {
    "title": "整理项目工具文档",
    "priority": "normal",
    "tags": []
  },
  "queue_ready": 0,
  "warnings": ["dry_run=true; task was not queued"]
}
```

`add` 也支持 `idempotency_key`。如果已有任务 metadata 中存在相同 key，工具直接返回已有 task：

```json
{
  "ok": true,
  "action": "add",
  "canonical_action": "add",
  "task_id": "tq-1",
  "deduped": true,
  "idempotency_key": "gateway-retry-1",
  "runtime_started_by_tool": false
}
```

幂等命中时不会重复入队，也不会为了重复请求启动 runtime。

## worker 数量参数

`scale_up`、`scale_down`、`set_workers` 使用 `parsePositiveCountArg` 解析 `count`。

支持类型：

- `int`
- `int64`
- `float64`
- string 形式的整数

规则：

- 未提供时使用 fallback，当前是 1。
- 空字符串使用 fallback。
- 小于等于 0 返回 `<name> must be positive`。
- 非数字返回 parse/type 错误。

## scale 输出

`scale_up` 和 `scale_down` 输出 JSON：

```json
{
  "action": "scale_up",
  "requested": 1,
  "removed": 0,
  "worker_count": 2,
  "idle_workers": 1,
  "busy_workers": 1,
  "queue_ready": 3,
  "queue_blocked": 0
}
```

`scale_down` 的 `removed` 表示实际移除 worker 数。

`set_workers` 输出：

```json
{
  "action": "set_workers",
  "requested": 3,
  "worker_count": 3,
  "pool_stats": {},
  "queue_ready": 0,
  "queue_blocked": 0
}
```

## 底层隐藏工具

`autonomy_queue_add`、`autonomy_queue_list`、`autonomy_queue_update`、`autonomy_worker_spawn`、`autonomy_worker_list`、`autonomy_heartbeat_trigger`、`autonomy_status` 都注册为 `HiddenFromModel=true`。

含义：

- 它们可供内部调用或精细控制。
- 模型面向用户时主要应使用 `autonomy` 统一入口。
- 文档仍需要记录它们，因为 registry 中实际存在。

## 和 Tidal Proactive 的关系

`autonomy` tool 和当前 Tidal Proactive 不是同一个系统。

`autonomy` 对应的是：

- `internal/autonomy`
- `AutonomyKit`
- task queue
- worker pool
- autonomy heartbeat
- `autonomy.*` 配置

它的职责是管理后台任务队列和 worker，让任务可以排队、执行、阻塞、完成、失败或扩缩 worker。

Tidal Proactive 对应的是：

- `internal/proactive`
- `proactive.RuntimeService`
- sampler / estimator / gate / executor
- proactive SQLite store
- `proactive.*` 配置
- `la proactive ...` CLI
- `/api/v1/proactive/status`

它的职责是基于运行时事件和采样信号做状态估计，然后在 gate 通过时执行 allowlist 内的低风险 proactive action。默认 `proactive.enabled=false`，即使启用也默认 `dry_run=true`。

两者当前在运行时装配上是并列关系：

```text
Agent
├── AutonomyKit        -> autonomy tools / queue / workers
└── ProactiveService   -> Tidal Proactive sampler / estimator / gate
```

当前 `autonomy` tool 不会调用 `internal/proactive`，也不能直接触发 Tidal Proactive 的 sample、dry-run、act、feedback、events 或 kernels。

反过来，Tidal Proactive 当前也不是通过 `autonomy` queue 来运行。它在 `proactive.enabled=true` 时由 agent 启动自己的 background runtime loop，按 `proactive.action_interval_seconds` 运行：

```text
sample -> estimate -> gate -> executor -> persist
```

需要注意命名上的重叠：`internal/autonomy/heartbeat.go` 里有 `HeartbeatProactive` 模式，这是 autonomy heartbeat 的“主动拉取队列工作”模式，不等于 `internal/proactive` 的 Tidal Proactive。

因此更准确的边界是：

- 后台任务队列和 worker：用 `autonomy`。
- Tidal state estimator / proactive gate / safe action executor：用 `proactive` CLI/API。
- 当前没有 `proactive` tool，也没有 `autonomy` action 直接代理 Tidal Proactive。

## 适合使用的场景

优先使用 autonomy 工具的场景：

- 用户要求“后台处理”“稍后继续”“主动跟进”。
- 需要把任务放入队列等待 worker 执行。
- 需要查看后台任务状态。
- 需要手动触发 heartbeat 调度。
- 需要调整 worker 数量。

示例：

```json
{
  "action": "add",
  "title": "整理项目工具文档",
  "description": "继续补全 docs/tools 下剩余工具说明",
  "priority": "normal",
  "tags": ["docs", "tools"]
}
```

## 不适合使用的场景

不优先使用 autonomy 工具的场景：

- 当前回合能直接完成的普通任务。
- 周期性定时任务，应使用 cron 工具。
- 单次 shell 命令，应使用 `terminal`。
- 用户没有授权后台执行或主动行为。
- 需要查看或触发 Tidal Proactive，应使用 `la proactive ...` 或 proactive API，而不是 `autonomy` tool。

## 风险和注意事项

autonomy 工具的主要注意点：

- 高层 `autonomy` 是 `PermApprove`。
- 部分隐藏工具是 `PermAuto`，但隐藏于模型。
- 某些 action 会启动 autonomy runtime。
- worker 执行可能产生后续工具调用或外部副作用，取决于任务内容。
- 输出主要由 `internal/autonomy.ToolDefinitions` 决定，本文只覆盖 service wrapper 层可见行为。

## 维护注意事项

如果后续修改 autonomy 工具，需要同步检查：

- action 分发表是否变化。
- 隐藏底层工具列表是否变化。
- 权限和 `HiddenFromModel` 是否变化。
- 哪些操作会调用 `ensureStarted` 是否变化。
- worker count 解析规则是否变化。
- scale 输出 JSON 字段是否变化。
- `internal/autonomy.ToolDefinitions` 输出格式是否变化。
- 是否新增 `autonomy` 和 `internal/proactive` 的桥接 action。
