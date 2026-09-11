# opt-tool-cron-01

## 目标

优化 `cron` 工具组的调度预览、参数校验、执行安全、输出结构和可观测性，让它继续保持“用户批准后创建或修改定时任务”的定位，同时降低误建任务、误执行 shell、调度时间不透明和后续排查困难的问题。

本方案覆盖：

- `cron`
- `cron_add`
- `cron_list`
- `cron_remove`
- `cron_pause`
- `cron_resume`
- `cron_status`

## 当前状态

相关实现：

- `internal/tool/cron_service.go`
- `internal/cron`
- `docs/tools/automation/cron.md`

当前工具注册：

| 工具 | 权限 | 说明 |
| --- | --- | --- |
| `cron` | `PermApprove` | 统一入口，通过 `action` 分发。 |
| `cron_add` | `PermApprove` | 添加定时任务。 |
| `cron_list` | `PermAuto` | 列出任务。 |
| `cron_remove` | `PermApprove` | 删除任务。 |
| `cron_pause` | `PermApprove` | 暂停任务。 |
| `cron_resume` | `PermApprove` | 恢复任务。 |
| `cron_status` | `PermAuto` | 查看 engine 状态。 |

当前 `cron_add` 流程：

1. 检查 service、engine、buildTask 是否初始化。
2. 读取并解析 `schedule`。
3. 读取 `mode`，默认 `shell`。
4. 读取 `command`，为空时报错。
5. 如果没有传 `id`，自动生成 job id。
6. 构造 metadata。
7. 调用 `buildTask(id, mode, command, meta)`。
8. 调用 `engine.AddJobWithMeta`。
9. 如果 engine 未运行，自动 `Start()`。
10. 调用 save 函数持久化。
11. 返回 compact JSON。

当前优势：

- 创建、删除、暂停、恢复都需要审批。
- 读操作可以自动执行。
- 同时支持 shell 模式和 agent 模式。
- schedule 支持自然语言和 5 字段 cron 表达式。
- `cron_list` 会返回 next run、last run、run count、error count 等运行信息。

## 主要问题

### 1. 添加任务前没有 dry-run / validate 模式

当前 `cron_add` 会直接创建并持久化任务。用户无法在创建前确认：

- 自然语言 schedule 被解析成了什么 cron 表达式。
- 下一次运行时间是什么。
- timezone 按什么规则解释。
- 将创建 shell 任务还是 agent 任务。
- 任务 ID 是否会和已有任务冲突。

建议增加 `dry_run` 或独立 `cron_validate`，让模型和用户先看到调度计划。

### 2. `mode` 拼错会静默回退到 shell

当前 `normalizeCronTaskMode` 的行为是：

| 输入 | 实际 mode |
| --- | --- |
| `agent` | `agent` |
| 其他 | `shell` |

这意味着：

```json
{"mode": "agetn"}
```

会变成 shell 模式，而不是报错。

风险：

- 用户本来想创建 agent prompt，结果变成 shell command。
- 模型生成参数拼写错误时，行为更危险。

建议改为严格枚举：只允许 `shell` 和 `agent`。

### 3. 自然语言 schedule 的解析结果不可见

`parseCronSchedule` 先尝试自然语言解析，失败后再尝试 cron 表达式解析。这个顺序合理，但当前添加成功后只返回最终 schedule，缺少：

- `schedule_text`
- `parsed_by`
- `timezone`
- `next_runs`
- parse warning

用户很难判断“每天早上”这类输入是否按预期解析。

### 4. shell command 缺少风险摘要

`cron_add` 是审批工具，但审批前最好能清楚展示 shell command 的风险类型。

高风险信号包括：

- 删除或覆盖文件：`rm`, `mv`, `>`, `truncate`
- 网络上传：`curl`, `wget`, `scp`, `rsync`
- 权限修改：`chmod`, `chown`, `sudo`
- 后台驻留：`nohup`, `&`, `systemctl`
- 访问密钥路径：`.env`, `id_rsa`, `*.pem`

不是要完全禁止这些命令，而是要在 approval summary 中显式提示。

### 5. 自动启动 engine 的副作用不够明显

添加任务时，如果 engine 未运行，当前会自动 `Start()`。

这对体验有利，但副作用是：

- “添加任务”同时也是“启动定时执行系统”。
- 用户可能只想保存任务，不想立即启动 engine。

建议增加参数：

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `start_engine` | `true` | 添加任务后是否启动 engine。 |
| `enabled` | `true` | 新任务是否立即启用。 |

### 6. 统一入口 `cron` 的权限粒度偏粗

`cron` 统一入口整体是 `PermApprove`。这意味着通过 `cron action=list/status` 做读操作也需要审批，而单独的 `cron_list` / `cron_status` 是 `PermAuto`。

建议：

- 保留 `cron` 统一入口。
- 在工具调度层支持 action-level permission。
- 或文档中明确：模型需要读状态时优先用细分工具。

### 7. 输出结构还可以更稳定

当前成功输出是 compact JSON，错误是普通文本 error。建议形成统一 schema，方便 API、TUI、网关和日志侧消费。

建议字段：

```json
{
  "ok": true,
  "action": "add",
  "id": "job-id",
  "schedule": "0 9 * * *",
  "schedule_text": "每天9点",
  "mode": "shell",
  "command": "echo hi",
  "running": true,
  "next_run": "2026-07-04T09:00:00+08:00",
  "warnings": []
}
```

### 8. notification metadata 命名需要统一

当前 metadata 同时涉及：

- `platform`
- `chat_id`
- `reply_to_message_id`
- `session_id`

需要统一内部 key 和外部参数名，避免出现 `chatID`、`chat_id` 混用导致网关回调无法定位目标。

## 优化原则

1. 创建、删除、暂停、恢复继续需要审批。
2. 读状态尽量保持自动执行。
3. 添加任务前必须能预览下一次运行时间。
4. shell 模式必须比 agent 模式更谨慎。
5. 自然语言 schedule 的解析结果必须可审计。
6. 不静默修正高风险参数，参数错误应明确报错。
7. 默认兼容现有输出，但新增结构化字段。

## 推荐方案

### 1. 增加 `cron_validate`

新增工具：

```text
cron_validate
```

权限：

```text
PermAuto
```

参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `schedule` | 是 | 无 | 自然语言 schedule 或 cron 表达式。 |
| `mode` | 否 | `shell` | `shell` 或 `agent`。 |
| `command` | 否 | 空 | 用于生成风险摘要和 ID 预览。 |
| `timezone` | 否 | local | 解析和预览时区。 |
| `next_runs` | 否 | `3` | 返回几个未来运行时间。 |

输出示例：

```json
{
  "ok": true,
  "valid": true,
  "schedule": "0 9 * * *",
  "schedule_text": "每天9点",
  "parsed_by": "natural_language",
  "timezone": "Asia/Shanghai",
  "next_runs": [
    "2026-07-04T09:00:00+08:00",
    "2026-07-05T09:00:00+08:00",
    "2026-07-06T09:00:00+08:00"
  ],
  "mode": "shell",
  "warnings": []
}
```

### 2. 给 `cron_add` 增加 dry-run

扩展参数：

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `dry_run` | `false` | 只返回创建计划，不写入 engine。 |
| `timezone` | local | schedule 解析和 next run 预览时区。 |
| `start_engine` | `true` | 添加后是否启动 engine。 |
| `enabled` | `true` | 新任务是否立即启用。 |
| `format` | `text` | `text` 或 `json`。 |

`dry_run=true` 时不调用：

- `engine.AddJobWithMeta`
- `engine.Start`
- `save`

### 3. 严格校验 `mode`

替换当前回退逻辑：

```go
func parseCronTaskMode(mode string) (string, error) {
    switch strings.ToLower(strings.TrimSpace(mode)) {
    case "", "shell":
        return "shell", nil
    case "agent":
        return "agent", nil
    default:
        return "", fmt.Errorf("invalid cron mode %q (use shell or agent)", mode)
    }
}
```

这样 mode typo 会直接失败。

### 4. 增加 command risk summary

新增：

```go
type CronCommandRisk struct {
    Level   string   `json:"level"`
    Reasons []string `json:"reasons"`
}
```

规则：

- `agent` 模式默认 `low`。
- `shell` 模式根据命令 token 做轻量扫描。
- 不解析复杂 shell AST，第一阶段只做启发式提示。
- 高风险不自动拒绝，但写入 approval detail 和返回 warnings。

### 5. 输出统一结构

新增通用响应：

```go
type CronToolResult struct {
    OK       bool     `json:"ok"`
    Action   string   `json:"action"`
    ID       string   `json:"id,omitempty"`
    Message  string   `json:"message,omitempty"`
    Warnings []string `json:"warnings,omitempty"`
}
```

读操作继续可以返回当前 jobs/status 字段，但增加 `ok` 和 `action`。

### 6. 完善 list/status 可观测性

`cron_list` 建议增加：

- `timezone`
- `now`
- `enabled`
- `next_run_human`
- `last_error`
- `metadata`
- `source`

`cron_status` 建议增加：

- `started_at`
- `last_tick`
- `store_path`
- `persistence_enabled`
- `timezone`

### 7. 增加更新任务能力

当前修改任务需要 remove + add。建议新增：

```text
cron_update
```

或给统一入口增加：

```json
{"action": "update"}
```

支持只修改：

- schedule
- command
- mode
- metadata
- enabled

并返回 before/after diff。

## 分阶段实施

### 第一阶段：低风险体验优化

- 新增 `cron_validate`。
- `cron_add` 支持 `dry_run`。
- `cron_add` 返回 `next_run` 和 warnings。
- 文档明确自然语言解析和 timezone。
- 给 `mode` typo 增加错误提示。

### 第二阶段：安全和可观测性

- 增加 shell command risk summary。
- 统一 JSON response schema。
- `cron_list` 输出 metadata 和 last_error。
- approval 文案包含 schedule、next run、mode、command risk。

### 第三阶段：管理能力补齐

- 新增 `cron_update`。
- 支持 `enabled=false` 创建暂停任务。
- 支持 `start_engine=false` 只保存不启动。
- 增加重复任务检测和 idempotency key。

## 测试建议

需要覆盖：

- 自然语言 schedule dry-run。
- 5 字段 cron 表达式 dry-run。
- 空 schedule 报错。
- 非法 mode 报错，不再回退 shell。
- `dry_run=true` 不写入 engine、不 save、不 start。
- `start_engine=false` 时不自动启动 engine。
- shell command risk warning。
- `cron_list` 对空 metadata 的兼容。
- `cron` 统一入口 action 分发。
- `cron_remove` / `pause` / `resume` 缺少 id 的错误。

建议测试包：

```bash
go test ./internal/tool ./internal/cron
```

## 文档更新

同步更新：

- `docs/tools/automation/cron.md`
- 工具 registry 描述。
- `config.example.json` 中和 cron 相关的持久化或 timezone 字段。
- 如果新增 `cron_validate` / `cron_update`，需要补工具总览。

## 风险与边界

- 不建议让 `cron` 直接成为任意后台执行入口；长期任务仍应走 autonomy。
- 不建议在第一阶段引入复杂 shell AST 解析，启发式风险提示足够。
- 不应默认禁止所有 mutation command，否则会破坏定时任务工具定位。
- timezone 行为必须和 `internal/cron` 的实际实现保持一致，不能只在文档层声明。

## 推荐结论

优先做 `cron_validate`、`cron_add dry_run`、严格 `mode` 校验和 next run 预览。这四项能最快降低误创建和误执行风险，也能明显改善用户判断“这个定时任务到底会不会按预期运行”的能力。
