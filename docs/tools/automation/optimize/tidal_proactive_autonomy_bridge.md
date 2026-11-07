# Tidal Proactive 与 Autonomy 联动方案

## 目标

把当前独立运行的 Tidal Proactive 和 Autonomy 后台任务系统连接起来，让 proactive 不只是记录 dry-run action，而是可以在满足安全条件时，把低风险、可执行的工作转成 autonomy queue task，由 worker 异步处理。

本方案聚焦：

- 解释为什么当前 Tidal Proactive 看起来“完全不会触发”。
- 明确 `internal/proactive` 和 `internal/autonomy` 的职责边界。
- 增加从 proactive decision 到 autonomy task 的桥接层。
- 保持默认安全：不开启、不执行 shell、不推送、不写危险文件。
- 提供可观测、可回滚、可测试的分阶段落地路径。

## 当前状态

相关实现：

- `internal/proactive`
- `internal/autonomy`
- `internal/agent/agent.go`
- `internal/tool/autonomy_service.go`
- `docs/proactive.md`
- `docs/tools/automation/autonomy.md`

当前 Tidal Proactive 运行链路：

```text
proactive.RuntimeService
  -> Runner.RunDryRun
  -> Sampler.Sample
  -> Estimator.Estimate
  -> FeedbackCalibrator.Calibrate
  -> Gate.Decide
  -> ActionExecutor.Execute
  -> Store.RecordActionExecutions
```

当前 Autonomy 运行链路：

```text
AutonomyKit
  -> TaskQueue
  -> WorkerPool
  -> HeartbeatEngine
  -> AgentExecutor
```

当前二者是并列关系：

```text
Agent
├── proactiveRuntime  -> Tidal Proactive loop
└── autonomyKit       -> queue / worker / heartbeat
```

## 为什么现在会感觉 Tidal Proactive 不触发

### 1. 默认关闭

默认配置：

```json
{
  "proactive": {
    "enabled": false,
    "dry_run": true
  }
}
```

`proactive.enabled=false` 时：

- 不初始化 proactive store。
- 不创建 `proactive.RuntimeService`。
- 不启动 background proactive loop。
- agent 不记录 proactive runtime events。

### 2. 启动后不立即执行

`proactive.RuntimeService.Start` 当前只启动 goroutine，然后等待 ticker：

```go
ticker := time.NewTicker(s.Interval)
...
case <-ticker.C:
    s.RunOnce(ctx)
```

默认 interval 是：

```json
{
  "proactive": {
    "action_interval_seconds": 300
  }
}
```

这意味着开启后最多要等 5 分钟才会第一次运行。没有“启动即 RunOnce”，所以体感像没触发。

### 3. 默认 dry-run

即使 proactive loop 运行了，默认：

```json
{
  "proactive": {
    "dry_run": true
  }
}
```

它会记录预测和 action execution，但不会真正执行非 dry-run 动作。

### 4. action executor 很保守

当前 proactive action 只允许 allowlist 内的低风险动作，例如：

- `preload_recent_project_context`
- `warm_memory_context`
- `preload_recent_session_summary`
- `prefer_lightweight_tasks`

并且当前边界明确：

- 不主动发消息。
- 不执行 shell。
- 不自动打开文件。
- 不采集敏感系统活动窗口。

### 5. 没有接入 Autonomy queue

当前 `internal/proactive` 的 action executor 不会调用：

```go
autonomyKit.AddTask(...)
```

也不会调用：

```go
autonomy_queue_add
```

所以 proactive 即使产生 decision，也不会形成一个可见的 autonomy task。

### 6. Autonomy tool 不能触发 Tidal Proactive

当前 `autonomy` tool 只分发到 `internal/autonomy.ToolDefinitions` 和 worker scale / queue / heartbeat wrapper。

它没有 action：

```text
proactive_sample
proactive_dry_run
proactive_act
proactive_feedback
```

因此从模型工具侧也无法直接驱动 Tidal Proactive。

## 设计原则

1. Tidal Proactive 负责“预测和决策”，Autonomy 负责“排队和执行”。
2. Proactive 不直接执行高风险动作，只能生成 task proposal。
3. 默认仍 dry-run，不自动改用户环境。
4. 能否入队必须经过 gate、policy、cooldown 和 allowlist。
5. 所有桥接行为都必须写入 proactive store 和 autonomy queue，方便审计。
6. 普通 `autonomy` 语义不破坏；新增桥接 action 或独立工具。

## 推荐架构

新增桥接层：

```text
Tidal Proactive
  sample -> estimate -> gate -> actions
                         |
                         v
              ProactiveAutonomyBridge
                         |
                         v
                  Autonomy Queue
                         |
                         v
                  WorkerPool / AgentExecutor
```

新增接口：

```go
type ProactiveTaskBridge interface {
    EnqueueProactiveTasks(ctx context.Context, decision proactive.Decision, executions []proactive.ActionExecution) ([]*autonomy.QueueTask, error)
}
```

默认实现：

```go
type AutonomyProactiveBridge struct {
    Kit     *autonomy.AutonomyKit
    Store   *proactive.Store
    Policy  ProactiveBridgePolicy
}
```

## 桥接策略

新增配置：

```json
{
  "proactive": {
    "autonomy_bridge_enabled": false,
    "enqueue_min_confidence": 0.75,
    "enqueue_dry_run": true,
    "enqueue_allowed_actions": [
      "preload_recent_project_context",
      "warm_memory_context",
      "preload_recent_session_summary",
      "prefer_lightweight_tasks"
    ],
    "enqueue_priority": "low",
    "enqueue_cooldown_seconds": 600,
    "run_once_on_start": false
  }
}
```

字段含义：

| 字段 | 默认值 | 说明 |
| --- | --- | --- |
| `autonomy_bridge_enabled` | `false` | 是否允许 proactive decision 入队 autonomy。 |
| `enqueue_min_confidence` | `0.75` | 入队最低 confidence。 |
| `enqueue_dry_run` | `true` | 只记录将入队的 task，不真正 AddTask。 |
| `enqueue_allowed_actions` | 默认低风险动作 | 允许转换成 autonomy task 的 proactive action。 |
| `enqueue_priority` | `low` | 入队任务优先级。 |
| `enqueue_cooldown_seconds` | `600` | 同类 proactive task 冷却时间。 |
| `run_once_on_start` | `false` | RuntimeService 启动后是否立即跑一次。 |

## 动作映射

将 proactive action 映射为 autonomy task：

| proactive action | autonomy task title | autonomy task description |
| --- | --- | --- |
| `preload_recent_project_context` | `Proactive: preload recent project context` | 读取最近项目上下文，生成轻量摘要，写入 proactive execution 记录。 |
| `warm_memory_context` | `Proactive: warm memory context` | 根据当前 estimate 的 predicted state 召回相关 memory，生成短上下文摘要。 |
| `preload_recent_session_summary` | `Proactive: preload recent session summary` | 读取最近 session 摘要，准备下一轮对话上下文。 |
| `prefer_lightweight_tasks` | `Proactive: prepare lightweight task list` | 根据最近 runtime events 提取可低成本处理的任务候选。 |

这些任务默认只做“预热”和“摘要”，不执行 shell，不写用户项目文件，不发消息。

## 运行流程

### 启动

agent 初始化时：

1. 初始化 `proactive.Store`。
2. 初始化 `proactive.RuntimeService`。
3. 初始化 `autonomy.AutonomyKit`。
4. 如果 `proactive.autonomy_bridge_enabled=true`，构造 bridge。
5. 把 bridge 注入 `RuntimeService` 或包一层 agent-level runner。

### 周期运行

```text
RuntimeService.RunOnce
  -> Runner.RunDryRun
  -> Executor.Execute
  -> Store.RecordActionExecutions
  -> Bridge.EnqueueProactiveTasks
  -> AutonomyKit.AddTaskWithError
```

### 手动触发

新增 autonomy action：

```json
{
  "action": "proactive_run_once"
}
```

或新增独立 tool：

```text
proactive_run_once
```

推荐新增独立 tool，避免 `autonomy` 统一入口过重。

## Tool 方案

### 新增 proactive_run_once

工具定义：

```go
Name: "proactive_run_once"
Category: CatDelegate
Permission: PermApprove
```

参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `enqueue` | 否 | `false` | 是否把通过 gate 的 action 入队 autonomy。 |
| `dry_run` | 否 | 配置默认值 | 是否只记录，不执行或入队。 |
| `min_confidence` | 否 | 配置默认值 | 覆盖入队最低 confidence。 |
| `format` | 否 | `json` | 输出格式。 |

输出：

```json
{
  "decision": {
    "predicted_state": "coding",
    "confidence": 0.82,
    "actions": []
  },
  "executions": [],
  "enqueued_tasks": [],
  "dry_run": true
}
```

### 新增 proactive_status

工具定义：

```go
Name: "proactive_status"
Permission: PermAuto
```

返回：

- enabled
- dry_run
- runtime_started
- interval
- store stats
- recent actions
- recent executions
- bridge enabled
- last enqueue result

### 可选：扩展 autonomy action

如果坚持走 `autonomy` 统一入口，可新增 action：

| action | 行为 |
| --- | --- |
| `proactive_status` | 返回 proactive status。 |
| `proactive_run_once` | 执行一次 proactive pipeline。 |
| `proactive_enqueue` | 执行一次 pipeline 并尝试入队。 |

但这会让 autonomy tool 同时承担 proactive facade，不如独立 tool 清晰。

## RuntimeService 修改建议

### 1. 支持启动即运行

新增字段：

```go
RunOnceOnStart bool
```

修改 `Start`：

```go
if s.RunOnceOnStart {
    go func() {
        _, _, _ = s.RunOnce(runCtx)
    }()
}
```

或在 loop 中先执行一次：

```go
if s.RunOnceOnStart {
    s.runAndRecord(ctx)
}
```

### 2. 抽出 runAndRecord

当前 loop 内错误记录逻辑可以抽成：

```go
func (s *RuntimeService) runAndRecord(ctx context.Context)
```

便于 Start-once、ticker、manual tool 复用。

### 3. 注入 bridge hook

新增：

```go
AfterRun func(ctx context.Context, decision Decision, executions []ActionExecution) error
```

`RunOnce` 最后调用：

```go
if s.AfterRun != nil {
    if err := s.AfterRun(ctx, decision, executions); err != nil {
        return decision, executions, err
    }
}
```

agent 初始化时把 `AutonomyProactiveBridge.EnqueueProactiveTasks` 注入进去。

## Autonomy Bridge 实现建议

伪代码：

```go
func (b *AutonomyProactiveBridge) EnqueueProactiveTasks(ctx context.Context, decision proactive.Decision, executions []proactive.ActionExecution) ([]*autonomy.QueueTask, error) {
    if !b.Policy.Enabled {
        return nil, nil
    }
    if decision.Estimate.Confidence < b.Policy.MinConfidence {
        return nil, nil
    }
    if b.Policy.DryRun {
        return nil, b.recordDryRun(decision, executions)
    }
    var tasks []*autonomy.QueueTask
    for _, action := range decision.Actions {
        if !b.Policy.Allowed[action.Name] {
            continue
        }
        if b.cooldownActive(action.Name) {
            continue
        }
        task, err := b.Kit.AddTaskWithError(
            "Proactive: "+action.Name,
            proactiveActionTaskDescription(decision, action),
            autonomy.PriorityLow,
            []string{"proactive", "tidal", action.Name},
        )
        if err != nil {
            return tasks, err
        }
        tasks = append(tasks, task)
    }
    return tasks, nil
}
```

## 为什么要接 Autonomy

Tidal Proactive 的强项：

- 判断什么时候可能需要做什么。
- 根据信号、反馈和 confidence 做 gate。
- 记录预测、动作、反馈和 kernel 学习。

Autonomy 的强项：

- 队列化任务。
- 控制 worker 并发。
- 后台执行 agent task。
- 查询任务状态。
- 失败、阻塞、重试和结果报告。

二者组合后：

```text
Proactive 负责提出“现在可以预热 X”
Autonomy 负责“把 X 排队并安全执行”
```

这比让 proactive executor 直接执行复杂动作更安全，也更可观测。

## 触发方式建议

### 被动触发

聊天和工具调用记录 runtime events：

```text
chat_turn
tool_call
tool_blocked
```

这些事件进入 proactive store，供 sampler 使用。

### 周期触发

`proactive.RuntimeService` 按 interval 运行。建议开发期配置：

```json
{
  "proactive": {
    "enabled": true,
    "dry_run": true,
    "action_interval_seconds": 30,
    "run_once_on_start": true
  }
}
```

生产默认仍保持 300 秒或更长。

### 手动触发

保留 CLI：

```bash
la proactive dry-run
la proactive act
la proactive act --apply
```

新增 tool 或 API：

```text
proactive_run_once
```

方便 agent 内部和 UI 调试。

## 分阶段实施

### 第一阶段：可观测和立即触发

- 给 `RuntimeService` 增加 `RunOnceOnStart`。
- 抽出 `runAndRecord`。
- 新增 `proactive_status` tool。
- 新增 `proactive_run_once` tool，默认 dry-run。
- 文档明确默认 300 秒 ticker 导致体感延迟。

验收：

- `proactive.enabled=true` 且 `run_once_on_start=true` 时，agent 启动后立即写入一次 estimate/action。
- `proactive_run_once` 能返回 decision、executions。
- `proactive_status` 能显示 runtime_started。

### 第二阶段：Bridge dry-run

- 新增 `AutonomyProactiveBridge`。
- 新增 `proactive.autonomy_bridge_enabled`。
- 新增 `proactive.enqueue_dry_run`。
- bridge 只记录“would_enqueue”，不调用 `AddTask`。
- 输出 would-enqueue task proposal。

验收：

- proactive decision 通过 gate 后，store 中出现 bridge dry-run 记录。
- autonomy queue 不增加任务。
- status 能看到 last bridge decision。

### 第三阶段：安全入队

- `enqueue_dry_run=false` 时，bridge 调用 `AutonomyKit.AddTaskWithError`。
- 默认只允许低风险 metadata/preload 类 action。
- 入队 task 带 tags：`proactive`、`tidal`、action name。
- 入队前检查 confidence、cooldown、max actions。

验收：

- 开启 bridge 后，proactive action 能进入 autonomy queue。
- `autonomy_queue_list` 能看到 proactive task。
- worker 执行结果可通过 autonomy report/status 查询。

### 第四阶段：反馈闭环

- autonomy task 完成后，把结果写回 proactive feedback 或 runtime event。
- 成功/失败影响 learned kernel。
- 如果 proactive 误判，降低同类 action confidence。

验收：

- completed task 产生 feedback event。
- `la proactive kernels` 能看到权重变化。
- 误触发率可在 benchmark 中观察。

## 测试建议

### RuntimeService

- `Start` 默认不立即 RunOnce。
- `RunOnceOnStart=true` 时立即 RunOnce。
- interval tick 仍正常执行。
- Stop 后 goroutine 退出。

### Bridge

- disabled bridge 不入队。
- dry-run bridge 不入队但记录 proposal。
- confidence 低于阈值不入队。
- action 不在 allowlist 不入队。
- cooldown 内不重复入队。
- allowlist action 入队成功。

### Agent 集成

- `proactive.enabled=false` 时不启动 proactive runtime。
- `proactive.enabled=true` 时启动 proactive runtime。
- bridge enabled 时能访问 autonomy kit。
- autonomy 未启动时，bridge 可选择只入队不启动 worker，或显式启动 autonomy。

建议策略：bridge 只入队，不自动启动 worker；是否启动 worker 由 `autonomy.enabled` 或用户调用 autonomy tool 决定。这样安全边界更清楚。

### CLI / Tool

- `proactive_status` 返回 runtime_started。
- `proactive_run_once` 返回 decision。
- `proactive_run_once enqueue=true dry_run=true` 返回 would_enqueue。
- `proactive_run_once enqueue=true dry_run=false` 在策略允许时新增 autonomy task。

## 风险与边界

- Proactive 误判可能制造无用后台任务，因此默认 bridge 关闭。
- 入队不等于执行；worker 是否启动需要明确配置。
- 如果 bridge 自动启动 autonomy，可能扩大副作用面，第一版不建议。
- Tidal Proactive 当前采样信号有限，不应承诺强预测能力。
- 默认 action 必须低风险，禁止 shell、文件写入、外部推送。
- 反馈闭环不要把单次失败过度学习成长期偏好。

## 推荐结论

当前 Tidal Proactive “完全不会触发”的主要原因不是逻辑不存在，而是默认关闭、默认 dry-run、首次运行等待 300 秒，并且没有接入 Autonomy queue。

推荐先做两个小改动：

1. `RuntimeService` 增加 `run_once_on_start` 和手动 `proactive_run_once` tool，让 proactive 是否工作可立即观察。
2. 新增 `AutonomyProactiveBridge` 的 dry-run 模式，把 proactive decision 转成 would-enqueue task proposal。

确认预测和 task proposal 质量后，再允许 `enqueue_dry_run=false` 把低风险 task 放入 autonomy queue。
