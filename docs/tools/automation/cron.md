# cron Tools

LuckyAgent 的 cron 工具组用于管理定时任务。它包含一个统一入口 `cron`，以及多个细分工具：`cron_add`、`cron_list`、`cron_remove`、`cron_pause`、`cron_resume`、`cron_status`。

cron 任务可以按 shell 模式执行命令，也可以按 agent 模式执行 prompt。调度表达式支持自然语言和 5 字段 cron 表达式。

## 工具定义

实现位置：

- `internal/tool/cron_service.go`
- `internal/cron`

注册位置：

- `CronToolService.RegisterTools`

工具列表：

| 工具 | 权限 | 说明 |
| --- | --- | --- |
| `cron` | `PermApprove` | 统一入口，通过 `action` 分发到 list/status/add/remove/pause/resume。 |
| `cron_add` | `PermApprove` | 添加定时任务。 |
| `cron_list` | `PermAuto` | 列出所有定时任务。 |
| `cron_remove` | `PermApprove` | 删除定时任务。 |
| `cron_pause` | `PermApprove` | 暂停定时任务。 |
| `cron_resume` | `PermApprove` | 恢复暂停任务。 |
| `cron_status` | `PermAuto` | 查看 cron engine 状态和任务计数。 |

这些工具的 `Category` 都是 `CatDelegate`，`Source` 都是 `builtin`。

## cron 统一入口

`cron` 参数：

| 参数 | 必填 | 说明 |
| --- | --- | --- |
| `action` | 是 | `list`、`status`、`add`、`remove`、`pause`、`resume`。 |
| `id` | 否 | 任务 ID。 |
| `schedule` | 否 | 自然语言 schedule 或 5 字段 cron 表达式。 |
| `mode` | 否 | `shell` 或 `agent`，默认 `shell`。 |
| `command` | 否 | shell 命令或 agent prompt。 |
| `platform` | 否 | 通知平台，例如 telegram。 |
| `chat_id` | 否 | 通知目标 chat ID。 |
| `reply_to_message_id` | 否 | 回复目标消息 ID。 |
| `session_id` | 否 | agent 模式使用的会话 ID。 |

`cron` 会根据 `action` 调用对应 handler。

非法 action 返回：

```text
invalid cron action "<action>" (use list, status, add, remove, pause, resume)
```

## cron_add

`cron_add` 用于添加任务。

必填：

- `schedule`
- `command`

可选：

- `id`
- `mode`
- `platform`
- `chat_id`
- `reply_to_message_id`
- `session_id`

执行流程：

1. 检查 cron service、engine、buildTask 是否初始化。
2. 解析 schedule。
3. 读取 mode，默认 `shell`。
4. 校验 command 非空。
5. 如果 id 为空，根据 mode、command 和当前 Unix 时间生成 id。
6. 构造 metadata。
7. 调用 `buildTask(id, mode, command, meta)`。
8. 调用 `engine.AddJobWithMeta`。
9. 如果 engine 未运行，自动 `Start()`。
10. 如果配置了 save 函数，调用 save 持久化。
11. 返回 JSON。

成功输出：

```json
{
  "id": "shell-echo-hi-1780000000",
  "schedule": "0 9 * * *",
  "mode": "shell",
  "command": "echo hi",
  "running": true,
  "message": "Scheduled job shell-echo-hi-1780000000 added"
}
```

## schedule 解析

`parseCronSchedule` 先尝试：

```go
cronpkg.ParseNaturalLanguage(trimmed)
```

失败后再尝试：

```go
cronpkg.ParseCronExpr(trimmed)
```

支持的自然语言能力取决于 `internal/cron.ParseNaturalLanguage`。

如果 schedule 为空，返回：

```text
parse schedule: schedule is required
```

## mode 行为

`normalizeCronTaskMode` 只有两种结果：

| 输入 | 实际 mode |
| --- | --- |
| `agent` | `agent` |
| 其他 | `shell` |

这意味着拼错 mode 不会报错，而是回退到 shell。

## 自动生成 ID

如果没有提供 `id`，会调用 `buildCronJobID(mode, command)`。

生成规则：

- 使用 `mode + "-" + command`。
- 转小写。
- `_` 和空格转为 `-`。
- 只保留小写字母和数字，其他字符折叠为 `-`。
- 最长保留 48 字符。
- 最后追加当前 Unix 秒级时间戳。

如果无法生成有效 base，使用：

```text
cron-job-<unix>
```

## cron_list

`cron_list` 不需要参数。

返回 JSON：

```json
{
  "running": true,
  "total": 1,
  "jobs": [
    {
      "id": "job-id",
      "schedule": "0 9 * * *",
      "status": "running",
      "next_run": "...",
      "last_run": "...",
      "run_count": 0,
      "error_count": 0,
      "mode": "shell",
      "command": "echo hi",
      "schedule_text": "每天9点"
    }
  ]
}
```

`schedule_text` 优先来自 job metadata；为空时用 `cronpkg.DescribeSchedule(job.Schedule)`。

## cron_status

`cron_status` 不需要参数。

返回 JSON：

```json
{
  "running": true,
  "job_count": 3,
  "paused_jobs": 1,
  "active_jobs": 2,
  "failed_jobs": 0
}
```

其中 active_jobs 统计的是 cron job status 为 `StatusRunning` 的任务。

## remove / pause / resume

`cron_remove`、`cron_pause`、`cron_resume` 都需要：

| 参数 | 必填 | 说明 |
| --- | --- | --- |
| `id` | 是 | 任务 ID。 |

id 为空时返回：

```text
id is required
```

成功输出：

```json
{"id":"job-id","message":"removed"}
```

或：

```json
{"id":"job-id","message":"paused"}
```

或：

```json
{"id":"job-id","message":"resumed"}
```

这些操作成功后，如果 save 函数存在，会调用 save 持久化。

## 适合使用的场景

优先使用 cron 工具的场景：

- 用户要求定时运行某个 shell 命令。
- 用户要求定时触发 agent prompt。
- 管理已有定时任务。
- 暂停、恢复、删除任务。
- 查看 cron engine 是否运行。

示例：

```json
{
  "schedule": "每天9点",
  "mode": "agent",
  "command": "总结今天需要关注的任务"
}
```

## 不适合使用的场景

不优先使用 cron 工具的场景：

- 只需要立即执行一次命令，应使用 `terminal`。
- 只需要后台任务队列，不是周期性调度，应使用 autonomy 工具。
- 用户没有明确授权长期定时执行。
- 命令包含敏感操作且未确认影响范围。

## 风险和注意事项

cron 工具的主要注意点：

- 添加、删除、暂停、恢复都需要批准。
- `cron_add` 会在 engine 未运行时自动启动 engine。
- shell 模式可能执行任意命令，风险取决于 command。
- agent 模式可能在没有当前聊天上下文的情况下运行，除非传入 `session_id`。
- mode 拼错会回退 shell。
- 输出 JSON 当前不是 pretty JSON。

## 维护注意事项

如果后续修改 cron 工具，需要同步检查：

- 工具列表和权限是否变化。
- `cron` action 分发表是否变化。
- schedule 解析顺序是否仍是自然语言优先。
- mode 规范化是否变化。
- ID 生成规则是否变化。
- 输出 JSON 字段是否变化。
- save 持久化调用时机是否变化。
