# opt-tool-autonomy-01

## 目标

优化 `autonomy` 工具组的队列语义、worker 安全、自动启动行为、状态可观测性和 Tidal Proactive 联动边界，让它继续保持“后台任务队列和 worker 控制面”的定位，同时减少误触发后台执行、任务状态不透明、隐藏工具难排查和 proactive 不会入队的问题。

本方案覆盖：

- `autonomy`
- `autonomy_queue_add`
- `autonomy_queue_list`
- `autonomy_queue_update`
- `autonomy_worker_spawn`
- `autonomy_worker_list`
- `autonomy_heartbeat_trigger`
- `autonomy_status`

## 当前状态

相关实现：

- `internal/tool/autonomy_service.go`
- `internal/autonomy`
- `internal/proactive`
- `docs/tools/automation/autonomy.md`
- `docs/tools/automation/optimize/tidal_proactive_autonomy_bridge.md`

当前工具注册：

| 工具 | 权限 | HiddenFromModel | 说明 |
| --- | --- | --- | --- |
| `autonomy` | `PermApprove` | false | 模型可见统一入口。 |
| `autonomy_queue_add` | `PermAuto` | true | 添加后台任务。 |
| `autonomy_queue_list` | `PermAuto` | true | 列出队列任务。 |
| `autonomy_queue_update` | `PermAuto` | true | 更新任务状态。 |
| `autonomy_worker_spawn` | `PermApprove` | true | 为任务启动 worker。 |
| `autonomy_worker_list` | `PermAuto` | true | 列出 worker。 |
| `autonomy_heartbeat_trigger` | `PermAuto` | true | 触发 heartbeat。 |
| `autonomy_status` | `PermAuto` | true | 查看状态。 |

当前 `autonomy` 统一入口支持：

- status
- add / enqueue / queue_add
- list / queue / queue_list
- report / outputs / results
- update / queue_update
- complete / fail / block / unblock
- workers / worker_list
- spawn / worker_spawn / run
- heartbeat / trigger / heartbeat_trigger
- scale_up / scale_down / set_workers

部分 action 会调用 `ensureStarted()`：

- queue add
- worker spawn
- heartbeat trigger
- scale up
- set workers

当前优势：

- 模型侧只有一个主要入口，减少工具列表噪音。
- 底层队列、worker、heartbeat 能力已经拆分。
- worker 扩缩容有结构化 JSON 输出。
- action 别名丰富，调用体验宽松。
- 文档已明确它和 Tidal Proactive 不是同一套系统。

## 主要问题

### 1. `autonomy` 统一入口权限过粗

`autonomy` 是模型可见入口，整体权限是 `PermApprove`。这可以保护写操作，但会让只读动作也需要审批，例如：

```json
{"action": "status"}
```

而隐藏的 `autonomy_status` 实际是 `PermAuto`。

问题：

- 读状态体验变重。
- 模型可见入口和底层工具权限不一致。
- 用户难判断哪些 action 会产生副作用。

建议支持 action-level permission 或拆出模型可见只读工具。

### 2. 隐藏工具不利于调试

底层工具全部 `HiddenFromModel=true`，这能减少误用，但会让排查变困难。

例如用户想明确查看 queue list，模型只能走：

```json
{"action": "list"}
```

如果统一入口行为异常，无法直接选择底层工具验证。

建议保留隐藏，但文档和诊断模式中允许显示底层工具 contract。

### 3. `ensureStarted()` 会带来隐式启动

添加任务、触发 heartbeat、spawn worker、scale up、set workers 都可能自动启动 autonomy runtime。

这意味着：

- “添加任务”可能同时启动后台系统。
- “触发 heartbeat”可能先启动 worker 体系。
- “scale up”会改变长期运行状态。

建议让启动副作用变成显式参数或输出 warning。

### 4. queue add 缺少 dry-run 和幂等键

当前 queue add 会直接添加任务。缺少：

- `dry_run`
- `idempotency_key`
- duplicate detection
- queue limit preview
- estimated worker impact

风险：

- 模型重复调用导致重复后台任务。
- 用户无法预览将入队的 task。
- 外部 gateway 重试可能产生重复任务。

### 5. action 别名太宽松，错误可能被掩盖

丰富别名有利于使用，但也会带来维护问题：

- 新 action 名容易和旧别名冲突。
- 用户日志里 action 名不统一。
- metrics 难聚合。

建议输出中始终返回 canonical action。

### 6. worker spawn 和 scale 缺少策略边界

worker 操作会影响后台并发和资源占用。当前需要审批，但还需要更明确的策略：

- 最大 worker 数。
- 单任务最大 spawn 次数。
- idle worker 回收。
- critical 任务是否允许自动扩容。
- 是否允许在 dry-run 模式下 spawn。

### 7. status/report 输出需要更适合排障

用户发现 “Tidal Proactive 不会触发” 时，需要一眼看到：

- autonomy started 是否为 true。
- queue ready / in_progress / blocked / done 数量。
- worker idle / busy 数量。
- heartbeat last run。
- 最近失败任务。
- 是否连接 proactive bridge。

当前信息主要由底层工具输出决定，文档层还没有定义稳定排障视图。

### 8. Tidal Proactive 当前不会自动入队

当前 Tidal Proactive 和 autonomy 是并列系统：

```text
Agent
├── proactive runtime
└── autonomy kit
```

Tidal Proactive 不会调用 autonomy queue，`autonomy` 也不能触发 proactive sample/run once。

这不是 bug，但会造成用户预期落差。需要桥接能力时，应按已有桥接方案新增显式配置和工具动作。

## 优化原则

1. `autonomy` 是后台任务系统，不是普通一次性 terminal。
2. 读状态默认应轻量，写入和执行需要清楚审批。
3. 自动启动必须可见，最好可关闭。
4. 入队必须支持 dry-run 和幂等。
5. worker 扩缩容必须有上限和审计记录。
6. Proactive 到 autonomy 的桥接必须显式启用，默认关闭。
7. 输出要能支持 TUI、HTTP API、gateway 和日志统一排查。

## 推荐方案

### 1. 拆出模型可见只读工具

新增或取消隐藏：

| 工具 | 权限 | 说明 |
| --- | --- | --- |
| `autonomy_status` | `PermAuto` | 查看系统状态。 |
| `autonomy_queue_list` | `PermAuto` | 查看队列。 |
| `autonomy_worker_list` | `PermAuto` | 查看 worker。 |

保留 `autonomy` 统一入口，但文档建议：

- 读状态优先使用只读工具。
- 创建、更新、执行和扩缩容使用 `autonomy`。

如果不想改变 HiddenFromModel，可以在 `autonomy` 内实现 action-level auto permission。

### 2. 给写操作增加 dry-run

扩展参数：

| 参数 | 默认值 | 适用 action | 说明 |
| --- | --- | --- | --- |
| `dry_run` | `false` | add/spawn/scale/set_workers/heartbeat | 只返回计划，不执行。 |
| `start_if_needed` | `true` | add/spawn/scale/set_workers/heartbeat | 是否允许隐式启动 runtime。 |
| `idempotency_key` | 空 | add | 防止重复入队。 |
| `dedupe_window_seconds` | `600` | add | 重复任务检测窗口。 |

`dry_run=true` 输出示例：

```json
{
  "ok": true,
  "dry_run": true,
  "canonical_action": "add",
  "would_start_runtime": true,
  "task": {
    "title": "整理项目工具文档",
    "priority": "normal",
    "tags": ["docs", "tools"]
  },
  "warnings": []
}
```

### 3. 让 `ensureStarted()` 可审计

当前 `ensureStarted()` 是内部行为。建议输出中增加：

```json
{
  "runtime_started_before": false,
  "runtime_started_by_tool": true
}
```

并在 `start_if_needed=false` 时，如果 runtime 未启动，返回：

```text
autonomy runtime is not started; set start_if_needed=true or start autonomy explicitly
```

### 4. 统一 action canonicalization

新增：

```go
func canonicalAutonomyAction(action string) (string, error)
```

输出永远包含：

```json
{
  "action": "enqueue",
  "canonical_action": "add"
}
```

收益：

- 日志聚合更稳定。
- 文档可只解释 canonical action。
- 别名仍兼容用户输入。

### 5. 强化 queue task contract

建议 `add` 输出固定字段：

```json
{
  "ok": true,
  "action": "add",
  "task_id": "task-id",
  "title": "任务标题",
  "state": "ready",
  "priority": "normal",
  "tags": ["docs"],
  "deduped": false,
  "queue_ready": 3,
  "worker_count": 1
}
```

建议 `list` 输出固定字段：

```json
{
  "ok": true,
  "action": "list",
  "state_filter": "ready",
  "total": 2,
  "tasks": []
}
```

### 6. 增加 worker policy

建议配置：

```json
{
  "autonomy": {
    "max_workers": 4,
    "max_spawn_per_task": 1,
    "scale_requires_approval": true,
    "idle_worker_ttl_seconds": 900
  }
}
```

工具层在 spawn / scale / set_workers 前检查：

- requested count 是否超过上限。
- task 是否已经有 active worker。
- queue 是否为空。
- runtime 是否处于 dry-run 或 paused 状态。

### 7. 补齐 Tidal Proactive 桥接入口

按已有桥接方案新增显式 action：

```json
{"action": "proactive_bridge_status"}
{"action": "proactive_run_once"}
{"action": "proactive_enqueue_candidates", "dry_run": true}
```

默认行为：

- bridge disabled。
- dry-run enabled。
- 只允许低风险 proactive action 入队。
- 不执行 shell。
- 不主动发外部消息。

注意：这不是把 `autonomy` 和 Tidal Proactive 混成一个系统，而是增加一个受控桥接层。

### 8. 增加排障状态视图

新增：

```json
{"action": "diagnose"}
```

输出：

```json
{
  "ok": true,
  "autonomy": {
    "started": true,
    "queue_ready": 2,
    "queue_in_progress": 1,
    "queue_blocked": 0,
    "workers_total": 2,
    "workers_idle": 1,
    "workers_busy": 1
  },
  "heartbeat": {
    "enabled": true,
    "last_run": "2026-07-03T10:00:00+08:00",
    "last_error": ""
  },
  "proactive_bridge": {
    "enabled": false,
    "dry_run": true,
    "last_candidate_count": 0
  },
  "warnings": []
}
```

## 分阶段实施

### 第一阶段：可见性和 dry-run

- `autonomy` 输出 `canonical_action`。
- add/spawn/scale/set_workers 支持 `dry_run`。
- 输出 `runtime_started_by_tool`。
- 文档明确哪些 action 会调用 `ensureStarted()`。

### 第二阶段：队列可靠性

- add 支持 `idempotency_key`。
- 增加 duplicate detection。
- list/report 输出固定 schema。
- status 增加最近失败任务和 worker summary。

### 第三阶段：worker policy

- 增加 max worker 上限。
- spawn 检查单任务 active worker。
- scale 操作输出 before/after。
- idle worker TTL 可配置。

### 第四阶段：Proactive 桥接

- 增加 `proactive_bridge_status`。
- 增加 `proactive_run_once` 或单独 `proactive` tool。
- 增加 `proactive_enqueue_candidates dry_run`。
- 完成 `ProactiveAutonomyBridge` 审计记录。

## 测试建议

需要覆盖：

- 空 action 等价于 status。
- 每个 action alias 映射到正确 canonical action。
- 非法 action 返回清晰错误。
- `dry_run=true` 不启动 runtime、不入队、不 spawn、不 scale。
- `start_if_needed=false` 且 runtime 未启动时报错。
- `idempotency_key` 重复调用不重复入队。
- `count` 支持 int/int64/float64/string。
- `count<=0` 报错。
- worker 超过上限时报错。
- status 输出包含 queue/worker/heartbeat summary。
- proactive bridge disabled 时不入队。

建议测试包：

```bash
go test ./internal/tool ./internal/autonomy ./internal/agent
```

## 文档更新

同步更新：

- `docs/tools/automation/autonomy.md`
- `docs/tools/automation/optimize/tidal_proactive_autonomy_bridge.md`
- `config.example.json`
- `docs/proactive.md`
- 如果新增 proactive bridge action，需要补 API / CLI 文档。

## 风险与边界

- 不建议让 autonomy 自动执行任意 shell；shell 定时执行应优先走 cron。
- 不建议默认打开 proactive bridge；主动行为必须默认保守。
- 不建议把隐藏底层工具全部暴露给模型；只读工具可以考虑暴露，写工具继续集中到统一入口。
- `autonomy` 的目标是后台任务编排，不应该替代普通当前回合执行。

## 推荐结论

优先做 `dry_run`、`canonical_action`、`runtime_started_by_tool` 和 `idempotency_key`。这些改动能直接解决后台任务“不知道有没有触发、触发了什么、是否重复触发”的核心问题。Tidal Proactive 联动应放在第二条线推进，通过显式 bridge action 和默认关闭配置落地。
