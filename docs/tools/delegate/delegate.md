# delegate Tools

子代理现在用一个 `subagent` 工具。`action=delegate` 创建任务并返回 `task_id`，`status` 查一个任务，`wait` 等待一组任务结束，`list` 列出任务，`cancel` 取消任务。创建和取消需要批准，查询和等待自动放行。

## 工具定义

实现位置：

- `internal/tool/delegate.go`
- `internal/tool/delegate_enhanced.go`
- `internal/tool/services.go`

默认核心注册：

- `delegate_task`
- `task_status`
- `list_tasks`
- `delegate_cancel`

代码中还定义：

- `delegate_parallel`
- `delegate_to_skill`
- `delegate_to_mcp`

## 运行时配置

前台 `delegate_task` 的全局限制位于 `config.json` 的 `delegate` 段。单次工具参数可以进一步收紧限制，但不能超过全局上限：

| 配置项 | 默认值 | 说明 |
| --- | ---: | --- |
| `delegate.max_concurrent` | `3` | 原子预留的 pending/running 委派名额，以及所有委派/HTTP 协作执行器共享的并发上限。 |
| `delegate.timeout_seconds` | `120` | 单个委派任务的默认外层超时。 |
| `delegate.min_timeout_seconds` | `5` | 单次 `timeout` 的最小值。 |
| `delegate.max_timeout_seconds` | `1800` | 单次 `timeout` 的最大值。 |
| `delegate.max_result_bytes_inline` | `4000` | `task_status` 内联结果上限。 |
| `delegate.max_children` | `3` | 每次协作允许的最大子任务数。 |
| `delegate.child.max_iterations` | `5` | 子 Agent Loop 最大迭代次数。 |
| `delegate.child.timeout_seconds` | `60` | 子 Agent Loop 单轮超时。 |
| `delegate.child.auto_approve` | `false` | 子 Agent 是否自动批准工具调用，建议保持关闭。 |
| `delegate.child.repeat_tool_call_limit` | `3` | 子 Agent 重复工具调用上限。 |
| `delegate.child.tool_only_iteration_limit` | `3` | 子 Agent 连续纯工具轮次上限。 |
| `delegate.child.duplicate_fetch_limit` | `1` | 子 Agent 同一 URL 抓取上限。 |
| `delegate.child.disabled_tools` | `[]` | 额外对 child 隐藏的工具。递归委派还受单独开关控制。 |
| `delegate.child.allow_recursive_delegate` | `false` | 全局允许递归委派；单次请求还必须传入 `allow_recursive_delegate=true`。 |

示例：

```bash
lh cfg s delegate.max_concurrent 5
lh cfg s delegate.child.max_iterations 10
lh cfg s delegate.child.disabled_tools terminal,file_delete
```

修改 `delegate.*` 后需要重启 Agent 进程；这些参数不会被当前配置热加载器重新装配到已有的 `DelegateManager`。

## 工具列表

| 工具 | 权限 | 说明 |
| --- | --- | --- |
| `delegate_task` | `PermApprove` | 委派单个任务给子代理异步执行。 |
| `task_status` | `PermAuto` | 查询指定委派任务状态。 |
| `list_tasks` | `PermAuto` | 列出所有委派任务。 |
| `delegate_cancel` | `PermApprove` | 取消 pending/running 委派任务。 |
| `delegate_parallel` | `PermApprove` | 并行委派多个任务并汇总结果。 |
| `delegate_to_skill` | `PermApprove` | 委派任务给指定 skill。 |
| `delegate_to_mcp` | `PermApprove` | 委派任务给指定 MCP server/tool。 |

## delegate_task

参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `description` | 是 | 无 | 要委派的任务描述。 |
| `context` | 否 | 空字符串 | 额外上下文或子代理指令。 |
| `timeout` | 否 | `120` | 超时时间，单位秒；当前会限制在 5-1800 秒之间。 |
| `mode` | 否 | `single` | `single`、`auto`、`parallel`、`pipeline`、`debate` 或 `autonomy_queue`。 |
| `max_children` | 否 | 全局配置 | 进一步收紧本次协作的子任务数量。 |

执行流程：

1. 校验 `description`。
2. 读取可选 `context` 和 `timeout`。
3. 在同一把锁内检查并预留 pending/running 名额，同一会话请求重试复用已有任务。
4. 生成带时间戳的任务 ID，避免重启后复用会话。
5. 调用 `prepareDelegateExecutionContext` 准备 workspace 和增强上下文。
6. 将任务记录到 manager 内存 map。
7. 启动 goroutine，按实际模式执行单个子代理、协作流程或后台队列。
8. 返回 task id、running 状态、workspace 和实际 timeout。

成功输出：

```json
{
  "task_id": "task-1",
  "status": "running",
  "workspace": "...",
  "timeout_seconds": 120,
  "message": "Task 'task-1' delegated. Use task_status to check progress."
}
```

没有执行器时任务失败，不再生成占位成功结果。协作模式为各子任务创建独立会话与职责；流水线保留原始请求并传递上一步输出；并行保留各项结果；辩论只接受有效 Agent ID 的多数票，无法形成多数时报告失败。

`autonomy_queue` 实际写入后台持久队列，返回的任务记录会关联 `autonomy_task_id` 和后台执行会话。它沿用子代理限制，等待后台验收完成；取消委派也会阻止关联队列任务继续执行。前台任务文件与后台队列仍分开保存。

普通委派和协作任务在启动时回载历史。中断的直接执行标为失败，保留已有结果和子会话供检查，不自动重放有副作用的操作。后台队列仍使用自身的 checkpoint/恢复规则。

## task_status

参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `task_id` | 是 | 无 | 要查询的任务 ID。 |
| `include_result` | 否 | `true` | 是否返回内联结果文本。 |

状态响应还包含可观测性字段：`tool_calls`（子 Agent 已执行的工具调用次数）、`elapsed_ms`（从启动到完成/当前的耗时）和 `last_tool`（最近一次工具名）。这些字段也会写入统一任务存储，供 Telegram Agent Trace 和 Dashboard 使用。

输出：

```json
{
  "task_id": "task-1",
  "description": "...",
  "workspace": "...",
  "status": "completed",
  "result_summary": "...",
  "result": "...",
  "result_bytes": 123,
      "result_truncated": false,
      "tool_calls": 3,
      "elapsed_ms": 1200,
      "last_tool": "file_read",
      "started_at": "2026-07-03T00:00:00+08:00",
  "completed_at": "2026-07-03T00:01:00+08:00"
}
```

结果会按 `DelegateConfig.MaxResultBytesInline` 截断，默认内联上限为 4000 字节。未完成任务的 `completed_at` 返回空字符串。`include_result=false` 时仍返回 `result_summary`、`result_bytes` 和 `result_truncated`，但不返回 `result` 字段。

任务不存在时返回：

```text
task not found: <task_id>
```

## list_tasks

参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `status` | 否 | 空 | 按状态过滤：`pending`、`running`、`completed`、`failed`、`cancelled`。 |
| `limit` | 否 | `20` | 返回数量上限，最大 `100`。 |
| `order` | 否 | `desc` | 按 `started_at` 排序：`desc` 或 `asc`。 |
| `include_result` | 否 | `false` | 是否包含每个任务的结果摘要字段。 |

输出：

```json
{
  "tasks": [
    {
      "task_id": "task-1",
      "description": "...",
      "workspace": "...",
      "status": "running",
      "started_at": "2026-07-03T00:00:00+08:00"
    }
  ],
  "count": 1,
  "total": 1,
  "by_status": {
    "pending": 0,
    "running": 1,
    "completed": 0,
    "failed": 0,
    "cancelled": 0
  }
}
```

`list_tasks` 会锁内复制任务快照，锁外过滤、排序和格式化，避免长期持有 manager 锁。默认按 `started_at` 倒序，同一时间下按 task id 保持稳定顺序。

## delegate_cancel

参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `task_id` | 是 | 无 | 要取消的委派任务 ID。 |
| `reason` | 否 | `cancelled by user` | 取消原因，会写入任务 `error` 字段。 |

成功输出：

```json
{
  "task_id": "task-1",
  "status": "cancelled",
  "message": "Task 'task-1' cancelled."
}
```

行为：

- `pending` / `running`：标记为 `cancelled`，记录 `completed_at`，并调用该任务的 cancel func。
- `completed` / `failed` / `cancelled`：返回不可取消错误。
- unknown task：返回 `task not found: <task_id>`。

## delegate_parallel

参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `tasks` | 是 | 无 | 任务描述数组。 |
| `context` | 否 | 空字符串 | 共享上下文。 |
| `timeout` | 否 | `120` | 每个任务的超时时间。 |

执行特点：

- 每个任务会创建 `parallel-task-<n>`。
- 并发数由 `DelegateConfig.MaxConcurrent` 控制；如果小于等于 0，默认 3。
- 如果配置了 `agentExecutor`，通过 agent loop 执行。
- 如果没有 executor，返回占位结果。
- 最终返回 JSON 汇总。

输出字段：

```json
{
  "success_count": 2,
  "failed_count": 0,
  "duration_sec": 1.23,
  "summary": "Parallel Delegation Summary:\n...",
  "results": ["..."]
}
```

注意：`delegate_parallel` 有工具定义，但是否注册取决于具体服务装配路径。

## delegate_to_skill

参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `skill_name` | 是 | 无 | 目标 skill 名称。 |
| `description` | 是 | 无 | 要委派给 skill 的任务描述。 |
| `priority` | 否 | `normal` | `low`、`normal`、`high`、`critical`。 |

成功输出：

```json
{
  "task_id": "...",
  "status": "running",
  "skill_name": "research",
  "priority": "normal"
}
```

## delegate_to_mcp

参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `server_name` | 是 | 无 | MCP server 名称。 |
| `tool_name` | 是 | 无 | MCP server 上的工具名。 |
| `arguments` | 否 | 空对象 | 传给 MCP tool 的参数。 |
| `priority` | 否 | `normal` | `low`、`normal`、`high`、`critical`。 |

成功输出：

```json
{
  "task_id": "...",
  "status": "running",
  "server_name": "server",
  "tool_name": "tool",
  "priority": "normal"
}
```

## 适合使用的场景

优先使用 delegate 工具的场景：

- 任务可以独立执行，不需要主循环一直等待细节。
- 需要并行拆解多个子任务。
- 需要查看异步任务状态。
- 需要把任务路由到特定 skill 或 MCP。

## 不适合使用的场景

不优先使用 delegate 工具的场景：

- 当前回合能直接完成的简单任务。
- 用户要求立即同步返回完整结果。
- 任务涉及高风险写操作但没有明确授权。
- 只是定时执行，应使用 cron。
- 只是后台队列管理，应使用 autonomy。

## 风险和注意事项

delegate 工具的主要注意点：

- 委派类写入/执行入口多为 `PermApprove`。
- task 数据存在 `DelegateManager` 的内存 map 中。
- `delegate_task` 是异步执行，需要用 `task_status` 查询结果。
- `delegate_cancel` 依赖子代理 executor 尊重 context；如果外部调用不响应 context，取消状态会先记录，但底层工作可能仍需等 executor 返回。
- 没有 `agentExecutor` 时会返回占位结果，不代表真的完成了复杂任务。
- `task_status` 对未完成任务返回空 `completed_at`，不再暴露零值时间。
- 当前任务状态仍未持久化，进程重启后内存任务会丢失。

## 维护注意事项

如果后续修改 delegate 工具，需要同步检查：

- 默认注册工具是否变化。
- `delegate_cancel` 的取消状态转换是否变化。
- `delegate_parallel`、`delegate_to_skill`、`delegate_to_mcp` 的注册路径是否变化。
- 任务 ID 格式是否变化。
- 并发限制逻辑是否变化。
- timeout 边界和结果截断上限是否变化。
- fallback executor 行为是否变化。
- 输出 JSON 字段是否变化。
