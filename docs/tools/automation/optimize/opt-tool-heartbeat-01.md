# opt-tool-heartbeat-01

## 目标

优化 `heartbeat` 工具组的触发安全、输出 schema、运行状态可观测性和调试能力，让它继续保持“手动触发 HEARTBEAT.md evaluation 和查看 runtime status”的定位，同时降低 `PermAuto` 触发副作用不透明、handler 输出不稳定和排查信息不足的问题。

本方案覆盖：

- `heartbeat_trigger`
- `heartbeat_status`

## 当前状态

相关实现：

- `internal/tool/heartbeat_service.go`
- `docs/tools/automation/heartbeat.md`

当前工具注册：

| 工具 | 权限 | 说明 |
| --- | --- | --- |
| `heartbeat_trigger` | `PermAuto` | 手动触发 HEARTBEAT.md evaluation，并执行 active periodic tasks。 |
| `heartbeat_status` | `PermAuto` | 返回 HEARTBEAT.md runtime status 和最新 routed external chat target。 |

当前工具层设计：

```go
type HeartbeatToolService struct {
    trigger func(args map[string]any) (string, error)
    status  func(args map[string]any) (string, error)
}
```

工具层只做：

1. 注册工具。
2. 检查 handler 是否存在。
3. 调用注入 handler。
4. 原样返回 handler 输出。

handler 未配置时返回：

```text
heartbeat trigger handler not configured
heartbeat status handler not configured
```

当前优势：

- 实现很薄，职责清楚。
- handler 注入让 runtime 侧可以自由实现。
- 两个工具都不需要参数，调用简单。
- status 和 trigger 分离。

## 主要问题

### 1. `heartbeat_trigger` 是自动权限但可能有副作用

文档已经说明 `heartbeat_trigger` 可能执行 active periodic tasks。当前权限是 `PermAuto`。

风险：

- 模型为了排查状态自动触发 heartbeat。
- heartbeat handler 内部执行周期任务。
- 用户以为只是检查，实际可能触发后台行为。

建议至少增加 `dry_run`，并考虑把真实 trigger 改为 `PermApprove`。

### 2. trigger 没有 dry-run

当前参数为空：

```json
{}
```

无法表达：

- 只评估 HEARTBEAT.md，不执行任务。
- 只列出会执行哪些 periodic tasks。
- 限制最多执行多少任务。
- 限制执行超时。

### 3. 输出没有固定 schema

工具层原样返回 handler 输出，因此调用方不能稳定解析：

- triggered 是否成功。
- 执行了几个任务。
- 跳过了几个任务。
- last routed target 是什么。
- 是否有 warning。
- 是否有 error detail。

建议工具层定义最小 schema，handler 输出作为 payload 或 detail。

### 4. status 缺少标准排障字段

当用户说“heartbeat 不触发”或“proactive 不触发”时，至少需要看到：

- heartbeat runtime 是否启用。
- handler 是否配置。
- last trigger 时间。
- last success 时间。
- last error。
- active periodic task 数。
- routed external chat target。
- autonomy / proactive 的关联状态。

当前这些字段不由工具层保证。

### 5. 缺少参数化过滤

实际排查时可能只想看某类任务：

- active tasks
- inactive tasks
- due tasks
- failed tasks
- external chat route

当前 `heartbeat_status` 没有参数，只能依赖 handler 自行输出。

### 6. handler 错误上下文不足

handler 未配置的错误清楚，但 handler 内部错误如果只返回普通 error，工具层没有统一包装：

- action
- timestamp
- partial result
- retryable
- suggestions

## 优化原则

1. status 是只读工具，应保持 `PermAuto`。
2. trigger 可能有副作用，必须支持 dry-run；真实执行建议审批。
3. 工具层至少定义最小输出 schema。
4. handler 仍然可以注入，但返回值最好结构化。
5. heartbeat 不应替代 cron/autonomy，只负责评估和调度触发。
6. 排障输出要能解释为什么“看起来没有触发”。

## 推荐方案

### 1. 给 `heartbeat_trigger` 增加参数

建议参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `dry_run` | 否 | `true` | 只评估，不执行 active tasks。 |
| `execute` | 否 | `false` | 是否真实执行 due tasks。 |
| `max_tasks` | 否 | `10` | 本次最多执行任务数。 |
| `timeout_seconds` | 否 | `60` | 本次触发超时。 |
| `include_inactive` | 否 | `false` | dry-run 时是否包含 inactive tasks。 |
| `task_id` | 否 | 空 | 只触发指定 task。 |
| `format` | 否 | `json` | 输出格式。 |

推荐默认：

- `dry_run=true`
- `execute=false`

真实执行需要：

```json
{
  "dry_run": false,
  "execute": true
}
```

### 2. 调整权限策略

推荐两种方案：

#### 方案 A：保留一个工具，动态权限

`heartbeat_trigger` 仍注册为 `PermAuto`，但：

- `dry_run=true` 自动执行。
- `execute=true` 时由 handler 或 tool runtime 升级审批。

如果当前工具框架不支持动态权限，则不建议选这个方案。

#### 方案 B：拆成两个工具

新增：

| 工具 | 权限 | 说明 |
| --- | --- | --- |
| `heartbeat_check` | `PermAuto` | 只评估，不执行。 |
| `heartbeat_trigger` | `PermApprove` | 真实触发并执行 due tasks。 |

这是更清晰的方案。

### 3. 定义 trigger 输出 schema

建议输出：

```json
{
  "ok": true,
  "action": "trigger",
  "dry_run": true,
  "executed": false,
  "started_at": "2026-07-03T10:00:00+08:00",
  "finished_at": "2026-07-03T10:00:01+08:00",
  "due_tasks": 2,
  "executed_tasks": 0,
  "skipped_tasks": 2,
  "tasks": [
    {
      "id": "task-id",
      "title": "task title",
      "due": true,
      "would_execute": true,
      "executed": false,
      "reason": "dry_run"
    }
  ],
  "warnings": []
}
```

handler 原始输出可以放入：

```json
{
  "raw_output": "..."
}
```

但不建议只返回 raw string。

### 4. 定义 status 输出 schema

建议输出：

```json
{
  "ok": true,
  "action": "status",
  "enabled": true,
  "handler_configured": true,
  "last_trigger_at": "2026-07-03T10:00:00+08:00",
  "last_success_at": "2026-07-03T10:00:01+08:00",
  "last_error": "",
  "active_tasks": 3,
  "inactive_tasks": 1,
  "due_tasks": 0,
  "routed_external_chat_target": {
    "platform": "telegram",
    "chat_id": "123"
  },
  "warnings": []
}
```

### 5. 给 status 增加过滤参数

建议参数：

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `include_tasks` | `false` | 是否返回 task 列表。 |
| `include_inactive` | `false` | 是否包含 inactive tasks。 |
| `include_routes` | `true` | 是否包含 external chat route。 |
| `include_errors` | `true` | 是否包含最近错误。 |
| `limit` | `20` | 返回任务数量上限。 |

### 6. handler 接口结构化

可以保留现有 string handler，新增结构化 handler：

```go
type HeartbeatTriggerResult struct {
    OK            bool
    DryRun        bool
    Executed      bool
    DueTasks      int
    ExecutedTasks int
    SkippedTasks  int
    Tasks         []HeartbeatTaskResult
    Warnings      []string
}
```

迁移方式：

1. 先在 handler 内部生成 JSON string。
2. 再把 `HeartbeatToolService` handler 类型替换为结构化结果。
3. 最后保留兼容 wrapper。

## 分阶段实施

### 第一阶段：文档和默认安全

- 文档明确 `heartbeat_trigger` 可能有副作用。
- 增加 `dry_run` 参数。
- 默认 `dry_run=true`。
- 输出中增加 `dry_run`、`executed`、`warnings`。

### 第二阶段：结构化输出

- `heartbeat_status` 返回固定 schema。
- `heartbeat_trigger` 返回固定 schema。
- handler 未配置错误包装为 JSON。
- 增加 `include_tasks`、`limit` 等 status 参数。

### 第三阶段：权限拆分

- 新增 `heartbeat_check`。
- 将真实 `heartbeat_trigger` 改为 `PermApprove`。
- 更新模型工具描述，避免模型用 trigger 做普通状态检查。

### 第四阶段：联动排障

- status 输出 autonomy heartbeat 状态。
- status 输出 proactive runtime 简要状态。
- trigger dry-run 输出会影响哪些 autonomy/proactive 路径。

## 测试建议

需要覆盖：

- trigger handler 未配置时报错。
- status handler 未配置时报错。
- `dry_run=true` 不执行 active tasks。
- `execute=true` 才执行 due tasks。
- `max_tasks` 限制生效。
- `timeout_seconds` 超时返回可诊断错误。
- status 输出 handler_configured。
- status include_tasks=false 时不返回大列表。
- status include_tasks=true 时限制 limit。
- handler 返回错误时输出 action、ok=false、error。

建议测试包：

```bash
go test ./internal/tool ./internal/agent ./internal/autonomy
```

## 文档更新

同步更新：

- `docs/tools/automation/heartbeat.md`
- `docs/tools/automation/autonomy.md`
- 如果新增 `heartbeat_check`，需要更新工具总览。
- 如果权限从 `PermAuto` 改为 `PermApprove`，需要更新工具 registry 说明和审批文案。

## 风险与边界

- 不要让 heartbeat 成为第二套 cron；周期性计划仍应由 cron 或 autonomy 管理。
- 不要让 status 输出过多任务细节，默认应保持紧凑。
- 如果保留 `PermAuto` trigger，必须保证默认 dry-run 不产生副作用。
- 如果 handler 输出仍是任意 string，调用方就不能可靠判断触发结果。

## 推荐结论

优先把 `heartbeat_trigger` 改成默认 dry-run，并定义 trigger/status 的最小 JSON schema。下一步再考虑拆出 `heartbeat_check` 和把真实 trigger 调整为审批工具。这样可以最快解决“heartbeat 到底有没有触发、触发了什么、有没有执行任务”的排障问题。
