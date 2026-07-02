# delegate Tools

LuckyAgent 的 delegate 工具组用于把任务委派给子代理、查询任务状态、列出委派任务，以及在扩展路径中委派给 skill 或 MCP。它们属于 `CatDelegate`，用于异步或并行任务执行，不是普通查询工具。

## 工具定义

实现位置：

- `internal/tool/delegate.go`
- `internal/tool/delegate_enhanced.go`
- `internal/tool/services.go`

默认核心注册：

- `delegate_task`
- `task_status`
- `list_tasks`

代码中还定义：

- `delegate_parallel`
- `delegate_to_skill`
- `delegate_to_mcp`

## 工具列表

| 工具 | 权限 | 说明 |
| --- | --- | --- |
| `delegate_task` | `PermApprove` | 委派单个任务给子代理异步执行。 |
| `task_status` | `PermAuto` | 查询指定委派任务状态。 |
| `list_tasks` | `PermAuto` | 列出所有委派任务。 |
| `delegate_parallel` | `PermApprove` | 并行委派多个任务并汇总结果。 |
| `delegate_to_skill` | `PermApprove` | 委派任务给指定 skill。 |
| `delegate_to_mcp` | `PermApprove` | 委派任务给指定 MCP server/tool。 |

## delegate_task

参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `description` | 是 | 无 | 要委派的任务描述。 |
| `context` | 否 | 空字符串 | 额外上下文或子代理指令。 |
| `timeout` | 否 | `120` | 超时时间，单位秒。 |

执行流程：

1. 校验 `description`。
2. 读取可选 `context` 和 `timeout`。
3. 检查当前 running 任务数是否达到 `DelegateConfig.MaxConcurrent`。
4. 生成 `task-<n>` ID。
5. 调用 `prepareDelegateExecutionContext` 准备 workspace 和增强上下文。
6. 将任务记录到 manager 内存 map。
7. 启动 goroutine 异步执行。
8. 返回 task id、running 状态和 workspace。

成功输出：

```json
{
  "task_id": "task-1",
  "status": "running",
  "workspace": "...",
  "message": "Task 'task-1' delegated. Use task_status to check progress."
}
```

如果没有配置 `agentExecutor`，任务会降级为占位完成：

```text
Sub-agent task completed (no executor): <description>
```

## task_status

参数：

| 参数 | 必填 | 说明 |
| --- | --- | --- |
| `task_id` | 是 | 要查询的任务 ID。 |

输出：

```json
{
  "task_id": "task-1",
  "description": "...",
  "workspace": "...",
  "status": "completed",
  "result": "...",
  "error": "",
  "started_at": "2026-07-03T00:00:00+08:00",
  "completed_at": "2026-07-03T00:01:00+08:00"
}
```

任务不存在时返回：

```text
task not found: <task_id>
```

## list_tasks

参数：

```json
{}
```

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
  "count": 1
}
```

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
- 没有 `agentExecutor` 时会返回占位结果，不代表真的完成了复杂任务。
- `task_status` 的 `completed_at` 对未完成任务可能是零值时间格式。
- 输出 JSON 当前不是 pretty JSON。

## 维护注意事项

如果后续修改 delegate 工具，需要同步检查：

- 默认注册工具是否变化。
- `delegate_parallel`、`delegate_to_skill`、`delegate_to_mcp` 的注册路径是否变化。
- 任务 ID 格式是否变化。
- 并发限制逻辑是否变化。
- fallback executor 行为是否变化。
- 输出 JSON 字段是否变化。
