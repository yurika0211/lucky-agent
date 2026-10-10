# opt-multi-agent-01

## 目标

优化 LuckyAgent multi-agent（多代理：把任务拆给多个执行体协作完成）系统，让它从“多套可用能力并存”升级为“统一任务状态、统一事件、可恢复、可审计、可控成本、可学习”的生产级编排层。

本方案基于 `docs/multi-agent/multi-agent-analysis.md` 的结论：

- `internal/tool/delegate.go` 已经是模型可见的可用子代理工具链。
- `internal/collab` 已有 HTTP 侧 multi-agent runtime、Dijkstra（最短路径：在加权图里找最低成本路径）、Markov（状态转移概率估计：用历史成功/失败/阻塞估算模式质量）、MDP Q-learning（马尔可夫决策过程：从执行结果学习状态-动作收益）和 planner trace。
- `cmd/la-multiagent-bench` 已经能离线验证策略。

核心优化方向不是先加更多模式，而是先统一底座：

```text
Task entrypoints
  -> Unified Task Orchestrator
  -> TaskStore
  -> TaskEventBus
  -> PolicyGuard
  -> Planner
  -> RuntimeResolver
  -> Executor
  -> ResultAggregator
  -> Outcome feedback
```

一句话版：先把所有多代理任务变成同一种可追踪 task，再让 planner 安全地选择 single/parallel/pipeline/debate/autonomy。

## 优化后的核心判断

当前最重要的问题不是“不会多代理”，而是“多代理状态不统一”。

如果直接把 Dijkstra/MDP planner 接到前台工具，会有几个风险：

- `delegate_task` 和 `/api/v1/agents/delegate` 的任务 ID、状态和结果不共享。
- planner trace 只在 HTTP collab metadata 里，模型工具看不到统一解释。
- 任务重启后丢失，MDP 学习样本也可能丢。
- AgentProfile 只是 profile，不是真正隔离的 runtime。
- 多 agent 容易过度拆分、循环讨论、成本失控。

因此推荐顺序：

```text
Phase 1: 统一观测
Phase 2: 统一持久化
Phase 3: Runtime profile 隔离
Phase 4: Planner trace 一等 artifact
Phase 5: 前台 delegate_task(mode=auto)
Phase 6: MDP 学习闭环
```

关键原则：

- 入口可以多个，任务记录必须统一。
- Planner 只做决策，不直接执行。
- Executor 只执行 planner 结果，不修改 planner trace。
- PolicyGuard（策略守卫：约束权限、预算、工具和递归）必须先于 planner。
- 用户硬约束优先于历史学习。
- MDP 初期只做 trace，不直接改变执行。

## 当前架构问题

### 1. 两套 DelegateManager 未统一

当前有两套相邻但不共享状态的 manager：

- `internal/tool.DelegateManager`：服务模型可见工具 `delegate_task/task_status/list_tasks/delegate_cancel`。
- `internal/collab.DelegateManager`：服务 HTTP `/api/v1/agents/*`。

问题：

- task ID 体系不同，例如 `task-1` 和 `collab-1`。
- 状态查询入口不同。
- 事件、结果、取消、trace 不共享。
- dashboard 无法自然聚合两类任务。

例子：

- 模型调用 `delegate_task` 创建了任务，HTTP `/api/v1/agents/tasks` 不一定能看到。
- HTTP 创建的 collab task，模型 `task_status` 也不一定能查。

### 2. AgentProfile 不是运行时隔离体

`internal/collab.Registry` 注册的是 `AgentProfile`：

```go
type AgentProfile struct {
    ID           string
    Capabilities []string
    Status       AgentStatus
}
```

它目前更像调度标签，不代表一个独立 runtime。

缺口：

- 没有绑定 provider/model。
- 没有独立 tool allowlist。
- 没有独立 cwd/workspace。
- 没有独立 memory namespace。
- 没有独立 approval policy。

影响：

- “多个 agent”可能只是多个名字。
- capability 影响 planner，但不保证实际执行隔离。
- 高风险 agent 无法强制更严审批。

### 3. Planner trace 不是一等产物

`internal/collab` 的 `mode=auto` 会把 planner 信息写入 task metadata：

- `planner`
- `planned_mode`
- `planner_path`
- `planner_weight`
- `mdp_state`
- `mdp_q_values`
- `planner_trace`

但这还不够：

- metadata 字符串不适合大 trace。
- tool delegate 没有同等 trace。
- replay 和 dashboard 缺少统一 artifact。

### 4. 缺少任务持久化

当前任务多以内存状态为主。

风险：

- 进程重启后任务丢失。
- 用户拿到 task id 后无法查历史。
- 长任务结果没有审计。
- MDP snapshot 如果不自动保存，学习结果丢失。

### 5. 缺少多代理成本与收束保护

multi-agent 的主要风险不是能力不足，而是过度执行。

典型失控：

- 简单任务被拆成多个子代理。
- debate 轮数过多。
- 子代理继续创建子代理。
- parallel 子任务输出大量上下文，聚合失败。
- 每个 agent 都重复搜索同一资料。

## 推荐目标架构

```text
Model Tools / HTTP API / Autonomy / Gateway
        |
        v
Unified Task Orchestrator
        |
        +-- TaskStore
        +-- TaskEventBus
        +-- PolicyGuard
        +-- Planner
        +-- RuntimeResolver
        +-- ResultAggregator
        +-- OutcomeRecorder
                |
                +-- local Agent Loop
                +-- delegate runtime
                +-- collab runtime
                +-- skill runtime
                +-- MCP/runtime endpoint
```

### Unified Task Orchestrator

`Unified Task Orchestrator`（统一任务编排器：所有任务入口共享的调度门面）负责：

- 创建统一 task record。
- 写事件。
- 调 planner。
- 选择 runtime。
- 执行或排队。
- 聚合结果。
- 写 outcome。

它不应该替代 Agent Loop，而是站在 Agent Loop 外层管理任务生命周期。

### TaskStore

`TaskStore`（任务存储：保存任务状态、事件和结果）负责统一持久化。

建议路径：

```text
$LUCKYAGENT_HOME/tasks/<task-id>/
  task.json
  events.jsonl
  result.md
  planner_trace.json
  children.json
  artifacts/
```

`task.json` 最小字段：

```go
type TaskRecord struct {
    ID          string
    ParentID    string
    Source      string // tool, http, autonomy, gateway
    Mode        string // single, parallel, pipeline, debate, autonomy_queue
    Status      string // pending, running, completed, failed, blocked, cancelled
    Description string
    Input       string
    CreatedAt   time.Time
    StartedAt   time.Time
    CompletedAt time.Time
    Runtime     TaskRuntimeRef
    Budget      TaskBudget
    Outcome     TaskOutcome
}
```

### TaskEventBus

`TaskEventBus`（任务事件总线：记录任务生命周期变化）负责所有状态变化：

```text
task.created
task.planned
task.started
task.child_created
task.progress
task.tool_used
task.completed
task.failed
task.cancelled
task.outcome_recorded
```

例子：

```json
{"type":"task.planned","task_id":"task-42","mode":"parallel","planner":"dijkstra-markov-mdp-v1"}
```

好处：

- dashboard 可以实时展示。
- benchmark 可以 replay。
- MDP 可以从事件中学习。
- 排查失败时不用只看最终字符串。

### MainAgentObserver

`MainAgentObserver`（主 Agent 观测器：把后台任务事件压缩成主 agent 可决策信号）负责让主 agent 在多代理任务执行中保持控制权。

当前已有的观测主要是两类：

- `delegate_task/task_status/list_tasks` 可以让主 agent 查询子任务状态和结果。
- `internal/collab` 在 workflow 结束后通过 `observePlannedOutcome` 把最终 outcome 回写 planner/MDP。

缺口是：主 agent 看到的不是连续任务态势，而是零散状态查询和最终反馈。它还不能稳定回答这些问题：

- 当前任务是否还值得等。
- 哪个子任务阻塞了整体收束。
- 子代理输出是否足够支撑最终回答。
- 是否需要补一个 verifier、test-runner 或 critic。
- 是否应该取消、降级为 single、重新拆分或直接汇报 partial result。

建议在 `TaskEventBus` 之上增加主 agent 专用观测面：

```text
TaskEventBus
  -> ObservationReducer
  -> MainAgentObservation
  -> Agent Loop context packer / task_status / dashboard
```

`ObservationReducer`（观测归约器：把大量事件压缩为可读摘要）不把完整事件流塞回模型上下文，而是生成稳定小对象：

```go
type MainAgentObservation struct {
    TaskID          string
    Status          string
    Mode            string
    Progress        float64
    RunningChildren int
    CompletedChildren int
    FailedChildren  int
    Blockers        []string
    FreshEvidence   []string
    FilesChanged    []string
    TestsRun        []string
    VerifierStatus  string
    Cost            TaskCostSnapshot
    RecommendedNext string // wait, poll_later, verify, aggregate, cancel, ask_user, finalize
}
```

主 agent 使用它做四类决策：

| 决策 | 观测条件 | 推荐动作 |
| --- | --- | --- |
| 继续等待 | 有 running 子任务，未超时，仍有新事件。 | `poll_later` 或保持后台任务。 |
| 聚合结果 | 关键子任务 completed，失败项不阻断目标。 | 调 `task_status(include_children=true)`，进入 final aggregation。 |
| 追加验证 | 有代码/配置变更，但没有 test/verifier 信号。 | 新建 verifier/test-runner 子任务。 |
| 收束或降级 | 超预算、重复失败、无新证据、用户目标已满足。 | cancel 剩余子任务，返回 partial/final result。 |

关键规则：

- 主 agent 不直接读取完整 `events.jsonl`，默认只读 `MainAgentObservation` 摘要。
- 只有调试、dashboard 或 `include_events=true` 时才展开事件流。
- 每次最终回答前，主 agent 应检查当前 parent task 是否还有 running children。
- 如果仍有 running children，必须选择：等待、取消、说明 partial、或把 task id 交给用户继续查。
- 用户最新指令优先于 planner 观测；例如用户说“别等了”，主 agent 应取消或降级收束。

这层观测的价值不是给 dashboard 多一张表，而是让主 agent 能把多代理执行纳入自己的收束机制：

```text
plan
  -> delegate
  -> observe
  -> decide(wait/verify/aggregate/cancel/finalize)
  -> final answer or next loop
```

没有这层，multi-agent 容易变成“开出去就失联”的后台任务；有了这层，主 agent 才能在干活后判断是否真的完成、是否需要补证据、是否该停止。

### PolicyGuard

`PolicyGuard` 负责把用户、系统和 runtime 约束变成硬边界。

约束包括：

- 最大子任务数。
- 最大并发数。
- 最大 debate 轮数。
- 是否允许子代理再开子代理。
- 工具 allowlist/denylist。
- cwd/workspace。
- approval policy。
- token/time/tool budget。

例子：

- 用户说“不要开子代理”，所有 `parallel/pipeline/debate` 边不可达。
- 用户说“只读”，所有写工具从 runtime profile 中移除。
- foreground task 默认最多 3 个子任务。

### RuntimeResolver

`RuntimeResolver`（运行时解析器：根据 agent profile 找到实际执行环境）把 `AgentProfile` 从标签升级为可执行 profile。

建议结构：

```go
type AgentRuntimeProfile struct {
    AgentID         string
    Provider        string
    Model           string
    ToolAllowlist   []string
    CWD             string
    MemoryNamespace string
    MaxIterations   int
    Timeout         time.Duration
    ApprovalPolicy  string
    AllowDelegate   bool
}
```

例子：

- `security-reviewer` 只能读文件和跑只读命令。
- `test-runner` 可以跑 `go test`，但不能改文件。
- `implementer` 可以改文件，但需要 verifier 检查。

## Planner 优化

### Dijkstra + Markov

当前 `internal/collab/planner.go` 是浅图：

```text
start -> mode -> end
```

建议第一阶段保留浅图，只把 trace 统一化。第二阶段再扩展成多阶段图。

浅图适合：

- 选择 single/parallel/pipeline/debate/autonomy。
- 给出可解释候选权重。
- 和现有 `PlanResult` 保持兼容。

多阶段图适合：

```text
start -> split -> assign -> execute -> aggregate -> verify -> final
```

不要一开始就做多阶段图，否则执行映射和调试成本会明显上升。

### MDP Q-learning

MDP 不应在早期直接控制执行。

推荐使用顺序：

```text
trace-only q_values
  -> confidence 足够后做小幅 adjustment
  -> benchmark 不退化后影响 planner 权重
```

MDP state 继续使用：

- task shape
- ambiguity
- risk
- agent bucket
- has critic
- has verifier
- timeout bucket

但 reward 需要更完整：

```text
reward =
  success_score
  + verifier_bonus
  + user_acceptance_bonus
  - latency_penalty
  - token_penalty
  - coordination_penalty
  - constraint_violation_penalty
```

例子：

- 没有测试的“模型自称完成”不能等同 verified success。
- 用户后续要求返工，应回写负反馈。

## 模型工具优化

### delegate_task

建议新增参数：

```json
{
  "mode": "single|auto|parallel|pipeline|debate|autonomy_queue",
  "max_children": 3,
  "include_trace": false,
  "allow_recursive_delegate": false
}
```

规则：

- 默认 `mode=single`，保持现有行为。
- `mode=auto` 只对复杂任务启用 planner。
- simple task 禁止 auto 拆分。
- 用户显式“不拆”时强制 single。
- 子代理默认不能再开子代理。

返回内容：

```json
{
  "task_id": "task-42",
  "mode": "parallel",
  "planner_summary": "Selected parallel because the request contains three independent modules.",
  "status": "running"
}
```

完整 trace 写 `planner_trace.json`，不要直接塞进 tool result。

### task_status

建议支持：

```json
{
  "task_id": "task-42",
  "include_events": true,
  "include_trace": true,
  "include_children": true
}
```

返回重点：

- 当前状态。
- 子任务状态。
- 最近事件。
- planner decision summary。
- result artifact 路径或摘要。

### list_tasks

建议统一展示 tool delegate 和 collab task：

```json
{
  "status": "running|completed|failed|cancelled",
  "source": "tool|http|autonomy|gateway",
  "parent_id": "...",
  "limit": 20
}
```

## HTTP API 优化

保留 `/api/v1/agents/*`，但底层改用统一 task store。

建议新增或调整：

```text
GET  /api/v1/tasks
GET  /api/v1/tasks/{id}
GET  /api/v1/tasks/{id}/events
GET  /api/v1/tasks/{id}/trace
POST /api/v1/tasks/{id}/cancel
POST /api/v1/tasks/{id}/feedback
```

`/api/v1/agents/tasks` 可以继续存在，但内部读取同一 `TaskStore`。

## 结果聚合优化

当前 aggregator 支持 concat、best、vote、merge、summary。建议让子任务输出结构化结果：

```go
type SubTaskResult struct {
    Summary      string
    Claims       []Claim
    Evidence     []EvidenceRef
    Risks        []string
    FilesChanged []string
    TestsRun     []string
    Blockers     []string
}
```

聚合策略：

- `summary`：适合调研。
- `merge`：适合多模块检查。
- `vote`：适合争议判断。
- `best`：只适合测试或明确打分场景，不能只按长度。
- `verifier`：高风险任务必须独立验证。

例子：

- 三个子代理都说“安全”，但没有 evidence，不应投票通过。
- 一个 verifier 跑出失败测试，应优先覆盖 implementer 的自评。

## 成本与收束保护

默认策略：

| 场景 | 默认限制 |
| --- | --- |
| foreground task | 最多 3 个子任务。 |
| debate | 最多 2 轮。 |
| child agent | 默认禁止再开 delegate。 |
| high-risk mutation | 必须 verifier 或 focused test。 |
| external search | 每个子任务限制搜索次数。 |
| aggregation | 大结果写 artifact，tool response 只给摘要。 |

必须记录：

- token estimate。
- tool call count。
- elapsed time。
- child count。
- retry count。
- verifier result。

## 分阶段实施

### Phase 1: 统一观测

目标：不改变执行路径，先统一任务视图，并让主 agent 能基于观测做收束判断。

任务：

1. 定义 `TaskRecord`、`TaskEvent`、`TaskStore` 接口。
2. `internal/tool.DelegateManager` 写 task events。
3. `internal/collab.DelegateManager` 写 task events。
4. `task_status/list_tasks` 能读取统一字段。
5. planner trace 写 artifact，不只塞 metadata。
6. 增加 `ObservationReducer`，把事件流压缩成 `MainAgentObservation`。
7. 主 agent 最终回答前检查 parent task 是否还有 running children。

验收：

- dashboard 或 CLI 能同时看到 tool delegate 和 collab task。
- 每个任务至少有 created、started、completed/failed/cancelled。
- 不影响现有 delegate 行为。
- 主 agent 能区分 wait、verify、aggregate、cancel、finalize 五类下一步动作。
- 有 running children 时，主 agent 不应静默声称所有工作已经完成。

### Phase 2: 统一持久化

目标：重启后仍能查历史任务和结果。

任务：

1. 本地文件 store 或 SQLite store。
2. 写入 `task.json/events.jsonl/result.md/planner_trace.json`。
3. 大结果 artifact 化。
4. MDP snapshot 自动保存/加载。
5. 增加 retention/cleanup。

验收：

- 重启后能查询 completed/failed/cancelled 任务。
- task id 不因进程重启失效。
- benchmark replay 能读取历史 trace。

### Phase 3: Runtime profile 隔离

目标：让多个 agent 具备真实执行边界。

任务：

1. 扩展 `AgentProfile`，增加 runtime binding。
2. 支持 tool allowlist、cwd、memory namespace、model/provider。
3. handler 根据 `SubTask.AgentID` 选择 runtime profile。
4. child agent 默认 `AllowDelegate=false`。

验收：

- 不同 agent 可以使用不同工具集。
- 不同 agent 的 cwd 和 memory 不互相污染。
- 高风险 agent 写操作需要审批。

### Phase 4: Planner 接入前台工具

目标：让 `delegate_task(mode=auto)` 使用 planner。

任务：

1. 新增 `mode` 参数。
2. simple task 保持 single。
3. 复杂任务调用 Dijkstra planner。
4. 输出短 planner summary。
5. 完整 trace 写 artifact。

验收：

- 独立 A/B/C 检查选择 parallel。
- 有依赖顺序选择 pipeline。
- 高风险评审选择 debate/review。
- 用户禁止拆分时保持 single。

### Phase 5: Outcome 与反馈闭环

目标：先记录高质量 outcome，再启用 MDP 学习。

任务：

1. 标准化 outcome：success、partial、failed、blocked、cancelled。
2. 记录 verifier result。
3. 记录用户反馈。
4. 记录 token/time/tool cost。
5. 写入 MDP observation。
6. 把 `MainAgentObservation.RecommendedNext` 和最终选择写入 outcome，用于评估主 agent 是否过早收束或过度等待。

验收：

- 能区分 claimed success 和 verified success。
- 用户返工会生成负反馈。
- outcome 可用于 benchmark replay。
- 能统计 `PrematureFinalizeRate`、`OverWaitRate`、`MissingVerifierRate`。

### Phase 6: MDP 线上学习

目标：在样本足够后让 MDP 影响 planner 权重。

任务：

1. MDP trace-only 运行一段时间。
2. 每个 action 记录 samples/confidence。
3. confidence 低时不调整权重。
4. replay 不退化后启用小幅 adjustment。
5. 定期保存 `$LUCKYAGENT_HOME/planner/mdp.json`。

验收：

- planner 能解释 Q value 和样本数。
- 高风险任务误拆分率下降。
- replay 指标不低于 baseline。

## 上线门槛

| 阶段 | 必须满足 |
| --- | --- |
| Phase 1 -> 2 | 统一任务视图不影响现有工具响应。 |
| Phase 2 -> 3 | 重启后任务可查询，结果 artifact 可读。 |
| Phase 3 -> 4 | runtime profile 的工具/cwd/memory 隔离测试通过。 |
| Phase 4 -> 5 | over-delegation rate 低于阈值。 |
| Phase 5 -> 6 | outcome 和 verifier 信号稳定。 |

建议阈值：

- `UserConstraintViolationRate == 0`
- `OverDelegationRate <= 5%`
- `PrematureFinalizeRate <= 2%`
- `MissingVerifierRate <= 5%`
- `CancelledTaskLeakRate == 0`
- `TaskStatusNotFoundAfterRestart == 0`
- `PlannerTraceMissingRate == 0`

## 风险与保护

### 1. 过度拆分

风险：简单任务被 parallel/debate。

保护：

- simple task 不进入 auto planner。
- foreground 默认最多 3 个子任务。
- 用户未要求多 agent 时 single 先验更强。
- planner 必须记录为什么没有选择 single。

### 2. 子代理递归失控

风险：子代理继续开子代理，形成任务树爆炸。

保护：

- child agent 默认 `AllowDelegate=false`。
- parent task 维护 child budget。
- 超预算直接 blocked。

### 3. 状态双写不一致

风险：tool delegate 和 collab 同时写不同状态。

保护：

- 统一 `TaskStore` 为唯一状态源。
- manager 内存 map 只做热缓存。
- 所有状态变化先写 event，再更新 snapshot。

### 4. MDP 学坏

风险：错误 outcome 污染策略。

保护：

- MDP 初期 trace-only。
- reward 区分 claimed success 和 verified success。
- confidence 不足时不影响权重。
- 保留 heuristic fallback。

### 5. Trace 污染上下文

风险：完整 planner trace 进入模型上下文，增加噪音。

保护：

- tool response 只返回 decision summary。
- 完整 trace 写 artifact。
- `include_trace=true` 时按需返回。

## 降级策略

必须能快速关闭高阶能力：

```text
multi_agent_unified_store=false
delegate_auto_mode=false
planner_effective=false
mdp_enabled=false
runtime_profile_isolation=false
```

降级时：

- 保留现有 `delegate_task` 单任务路径。
- 保留 `task_status/list_tasks` 基本功能。
- planner trace 可继续 shadow 记录。
- MDP 不影响执行。

## 验证方式

### 单元测试

```bash
go test ./internal/tool -run 'Delegate|TaskStatus|ListTasks'
go test ./internal/collab
go test ./internal/server -run 'Agents|Tasks|Collab'
go test ./internal/agent -run 'Delegate|ToolIntentGating|ToolExecutionGuard'
```

### Benchmark

```bash
go run ./cmd/la-multiagent-bench -variant baseline -scenario all -rounds 1
go run ./cmd/la-multiagent-bench -variant runtime-mdp-v1 -scenario all -rounds 1
go run ./cmd/la-multiagent-bench -variant math-full-v1 -scenario heavy -rounds 1
```

重点观察：

- `ModeAcc`
- `SplitAcc`
- `RouteRisk`
- `CoordOH`
- `CalibrationECE`
- `PathRegret`
- `OverDelegationRate`

### 集成验收

建议保留这些真实场景：

1. 只读查询 task 列表，不创建子代理。
2. 简单修复保持 single。
3. A/B/C 独立调研选择 parallel。
4. 调研、实现、测试选择 pipeline。
5. 高风险安全评审选择 debate + verifier。
6. 长期后台监控进入 autonomy queue。
7. pending/running 任务可取消，重启后仍能查询最终状态。

## 推荐落地点

短期建议新增：

```text
internal/task/
  store.go
  event.go
  artifact.go
  orchestrator.go
  policy.go
```

然后让两套 manager 接入：

```text
internal/tool/delegate.go
internal/collab/delegate.go
```

planner 可暂时留在 `internal/collab`，等 task store 稳定后再抽象成共享 planner 包。

## 最小闭环

第一版最小闭环：

1. 定义统一 `TaskRecord`。
2. `delegate_task` 写 `task.created/task.started/task.completed`。
3. collab delegate 写同样事件。
4. `list_tasks` 能读统一 task index。
5. planner trace 写文件 artifact。

这一步完成后，multi-agent 的状态才算真正可观测。

## 总结

LuckyAgent multi-agent 优化的主线应该是：

```text
统一状态
  -> 统一事件
  -> 统一持久化
  -> runtime profile 隔离
  -> planner 前台接入
  -> outcome 学习闭环
```

先把任务变成可恢复、可审计、可解释的对象，再让 Dijkstra/MDP 逐步影响调度。这样能避免过度拆分和成本失控，也能让多代理从演示能力变成可靠的工程能力。
