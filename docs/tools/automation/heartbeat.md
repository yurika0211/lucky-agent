# heartbeat Tools

LuckyAgent 的 heartbeat 工具组用于手动触发 HEARTBEAT.md 评估，以及查看 heartbeat 运行状态。它包含两个工具：`heartbeat_trigger` 和 `heartbeat_status`。

这组工具依赖外部注入的 handler。工具层负责注册、参数校验、安全默认值和最小 JSON 输出 schema；真实 heartbeat 执行仍由注入 handler 完成。

## 工具定义

实现位置：

- `internal/tool/heartbeat_service.go`

注册位置：

- `HeartbeatToolService.RegisterTools`

工具列表：

| 工具 | 权限 | 说明 |
| --- | --- | --- |
| `heartbeat_trigger` | `PermAuto` | 预览或手动触发 HEARTBEAT.md evaluation。默认 dry-run，不执行 active periodic tasks。 |
| `heartbeat_status` | `PermAuto` | 返回 HEARTBEAT.md runtime status 和最新 routed external chat target。 |

两个工具的 `Category` 都是 `CatDelegate`，`Source` 都是 `builtin`。

## heartbeat_trigger

参数：

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `dry_run` | `true` | 只预览，不调用 trigger handler。 |
| `execute` | `false` | 是否真实执行；只有 `dry_run=false` 且 `execute=true` 才调用 handler。 |
| `max_tasks` | `10` | 传给 handler 的最大任务数提示，并写入 dry-run 输出。 |
| `timeout_seconds` | `60` | 传给 handler 的超时提示，并写入 dry-run 输出。 |
| `include_inactive` | `false` | dry-run/handler 可使用的 inactive task 提示。 |
| `task_id` | 空 | 只处理指定任务的提示。 |
| `format` | `json` | 稳定输出格式。 |

执行流程：

1. 检查 service 和 trigger handler 是否存在。
2. 如果 handler 不存在，返回错误。
3. 解析 `dry_run` 和 `execute`。
4. 如果 `dry_run=true` 或 `execute=false`，直接返回 dry-run JSON，不调用 handler。
5. 如果 `dry_run=false` 且 `execute=true`，调用注入的 trigger handler。
6. 将 handler 输出包装进稳定 JSON，原始输出放入 `raw_output`；如果 handler 输出是 JSON object，同时合并到顶层并放入 `payload`。

handler 未配置时返回：

```text
heartbeat trigger handler not configured
```

默认 dry-run 输出示例：

```json
{
  "ok": true,
  "action": "trigger",
  "dry_run": true,
  "executed": false,
  "handler_configured": true,
  "started_at": "2026-07-03T10:00:00+08:00",
  "finished_at": "2026-07-03T10:00:00+08:00",
  "due_tasks": 0,
  "executed_tasks": 0,
  "skipped_tasks": 0,
  "max_tasks": 10,
  "timeout_seconds": 60,
  "warnings": ["dry_run=true or execute=false; heartbeat handler was not called"]
}
```

真实触发必须显式传：

```json
{
  "dry_run": false,
  "execute": true
}
```

真实触发输出示例：

```json
{
  "ok": true,
  "action": "trigger",
  "dry_run": false,
  "executed": true,
  "handler_configured": true,
  "raw_output": "{\"executed_tasks\":2}",
  "payload": {"executed_tasks": 2},
  "executed_tasks": 2
}
```

## heartbeat_status

参数：

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `include_tasks` | `false` | 请求 handler 返回任务列表。 |
| `include_inactive` | `false` | 请求 handler 包含 inactive tasks。 |
| `include_routes` | `true` | 请求 handler 包含 external chat route。 |
| `include_errors` | `true` | 请求 handler 包含最近错误。 |
| `limit` | `20` | 请求 handler 返回的任务数量上限。 |

执行流程：

1. 检查 service 和 status handler 是否存在。
2. 如果 handler 不存在，返回错误。
3. 调用注入的 status handler。
4. 将 handler 输出包装进稳定 JSON，原始输出放入 `raw_output`；如果 handler 输出是 JSON object，同时合并到顶层并放入 `payload`。

handler 未配置时返回：

```text
heartbeat status handler not configured
```

成功输出示例：

```json
{
  "ok": true,
  "action": "status",
  "handler_configured": true,
  "raw_output": "{\"enabled\":true}",
  "payload": {"enabled": true},
  "enabled": true
}
```

## 输出格式

工具层定义最小输出 schema，并保留 handler 原始输出。

注入函数仍是：

```go
trigger func(args map[string]any) (string, error)
status  func(args map[string]any) (string, error)
```

因此调用方可以依赖 `ok`、`action`、`handler_configured`、`dry_run`、`executed`、`raw_output`、`payload` 等工具层字段；更细的 task/runtime 字段仍取决于 handler。

## 适合使用的场景

优先使用 heartbeat 工具的场景：

- 手动触发一次 HEARTBEAT.md 检查。
- 调试周期任务是否会被 heartbeat 路由。
- 查看 heartbeat runtime 状态。
- 查看最新 routed external chat target。

示例：

```json
{"dry_run": true}
```

## 不适合使用的场景

不优先使用 heartbeat 工具的场景：

- 添加周期任务，应使用 cron 或 autonomy 相关工具。
- 立即执行 shell 命令，应使用 `terminal`。
- 查看普通任务队列，应使用 autonomy 状态工具。
- 未启用 heartbeat runtime 时，handler 可能未配置。

## 风险和注意事项

heartbeat 工具的主要注意点：

- 两个工具都是 `PermAuto`。
- `heartbeat_trigger` 默认不执行 active periodic tasks。
- 真实执行需要 `dry_run=false` 且 `execute=true`。
- `heartbeat_trigger` 仍是 `PermAuto`，因此 handler 必须继续保持自己的安全边界。
- 工具层定义最小输出结构，但具体任务字段取决于注入 handler。

## 维护注意事项

如果后续修改 heartbeat 工具，需要同步检查：

- 工具名是否仍是 `heartbeat_trigger` 和 `heartbeat_status`。
- 权限是否仍是 `PermAuto`。
- 是否新增参数。
- handler 未配置时的错误文案是否变化。
- 输出是否开始在工具层定义固定 schema。
