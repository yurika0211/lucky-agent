# autonomy Tools

LuckyAgent 的 autonomy 工具组用于管理后台任务队列、worker、heartbeat 和主动执行能力。它包含一个模型可见的统一入口 `autonomy`，以及多个隐藏的底层 `autonomy_*` 工具。

这些工具属于运行时调度和后台执行，不是普通文件或网络查询工具。

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
| `priority` | `add` 时的优先级：`low`、`normal`、`high`、`critical`。 |
| `tags` | `add` 时的标签。 |
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
