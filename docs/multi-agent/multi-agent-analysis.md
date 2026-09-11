# LuckyAgent Multi-Agent Analysis

## 结论

LuckyAgent 当前已经具备三层 multi-agent 能力，但它们还不是同一个统一调度系统：

1. `internal/tool/delegate.go` 提供模型可见的子代理工具，面向对话内的异步委派。
2. `internal/collab` 提供 HTTP API 侧的多 Agent 协作运行时，支持注册 Agent、任务拆分、pipeline、parallel、debate 和 planner trace。
3. `cmd/la-multiagent-bench` 提供离线 benchmark，用于验证拆分策略、协作模式、风险、校准和数学规划器。

当前最成熟、最可直接使用的是 `delegate_task` 工具链。`internal/collab` 的规划器已经有 Dijkstra、Markov、MDP Q-learning 和 verifier 钩子，但运行时接入仍偏轻量，主要通过 `/api/v1/agents/*` 暴露，尚未和模型可见的 `delegate_task`、autonomy worker、session trace 完全打通。

## 代码入口

### 模型可见委派工具

相关文件：

- `internal/tool/delegate.go`
- `internal/tool/delegate_enhanced.go`
- `internal/tool/services.go`
- `internal/agent/agent.go`
- `docs/tools/delegate/delegate.md`

默认注册工具：

- `delegate_task`
- `task_status`
- `list_tasks`
- `delegate_cancel`

扩展工具定义：

- `delegate_parallel`
- `delegate_to_skill`
- `delegate_to_mcp`

当前默认路径中，`delegate_task` 会创建任务记录和 workspace，然后启动 goroutine。完整 Agent runtime 初始化时，`internal/agent/agent.go` 会给 `DelegateManager` 注入 executor：

```go
supportRT.delegateMgr.SetAgentExecutor(func(ctx context.Context, description, contextStr string) (string, error) {
    sess := memoryRT.sessions.NewWithTitle("delegate-task")
    loopCfg := DefaultLoopConfig()
    loopCfg.AutoApprove = false
    loopCfg.MaxIterations = 5
    result, err := a.RunLoopWithSession(ctx, sess, prompt, loopCfg)
    return result.Response, err
})
```

这意味着子代理不是假返回，而是真正进入 Agent Loop。它的安全边界是：

- 子代理使用独立 session。
- 子代理 cwd 可以被限制到 delegate workspace。
- `AutoApprove=false`，危险工具仍需要批准。
- `MaxIterations=5`，避免无限循环。
- `delegate_cancel` 通过 context cancel 请求终止运行中任务。

### HTTP 多 Agent 协作层

相关文件：

- `internal/collab/registry.go`
- `internal/collab/delegate.go`
- `internal/collab/planner.go`
- `internal/collab/mdp.go`
- `internal/collab/aggregator.go`
- `internal/server/collab_handlers.go`
- `internal/server/server.go`

HTTP API 路由：

- `GET /api/v1/agents`
- `POST /api/v1/agents/register`
- `DELETE /api/v1/agents/deregister`
- `POST /api/v1/agents/delegate`
- `GET /api/v1/agents/task`
- `GET /api/v1/agents/tasks`
- `POST /api/v1/agents/cancel`

`internal/server/server.go` 初始化了独立的 `collab.Registry` 和 `collab.DelegateManager`：

```go
collabRegistry := collab.NewRegistry()
delegateManager := collab.NewDelegateManager(collabRegistry, collab.TaskHandlerFunc(func(ctx context.Context, task *collab.SubTask) (string, error) {
    return a.Chat(ctx, task.Input)
}))
```

这套 manager 和 `internal/tool.DelegateManager` 是不同实现。前者服务 HTTP multi-agent API，后者服务模型工具调用。两者状态不共享，任务 ID 体系也不同：

- tool delegate: `task-1`
- collab delegate: `collab-1`

### 离线 benchmark

相关文件：

- `cmd/la-multiagent-bench`
- `cmd/la-multiagent-bench/README.md`

benchmark 不调用真实模型，而是用固定 synthetic suite 和 replay case 验证调度策略。主要模式包括：

- `single`
- `parallel`
- `pipeline`
- `debate`
- `autonomy_queue`

主要策略包括：

- `baseline`
- `capability-routed`
- `parallel-routed`
- `dependency-aware`
- `debate-review`
- `runtime-mdp-v1`
- `math-full-v1`

它更像 planner 实验平台，不是线上执行路径本身。

## 当前架构视图

```text
User / Gateway / HTTP
        |
        v
Agent Loop
        |
        +-- model-visible tools
        |       |
        |       +-- delegate_task / task_status / list_tasks / delegate_cancel
        |               |
        |               +-- internal/tool.DelegateManager
        |                       |
        |                       +-- child Agent Loop session
        |
        +-- HTTP server
                |
                +-- /api/v1/agents/*
                        |
                        +-- internal/collab.Registry
                        +-- internal/collab.DelegateManager
                                |
                                +-- Planner: Dijkstra + Markov + MDP
                                +-- Modes: pipeline / parallel / debate
                                +-- Handler: Agent.Chat(task.Input)
```

这个结构的优点是实现边界清晰，工具委派和 HTTP 协作可以独立演进。缺点是能力分散：模型无法直接使用 `internal/collab` 的 planner trace，HTTP 协作层也不能直接查询 `delegate_task` 状态。

## 执行流分析

### delegate_task 执行流

```text
delegate_task
  -> 校验 description
  -> 解析 context / timeout
  -> 检查 MaxConcurrent
  -> 创建 task id
  -> 准备 workspace
  -> 写入内存 task map
  -> goroutine executeTask
        -> context.WithTimeout
        -> 调用 agentExecutor
        -> RunLoopWithSession
        -> 写入 completed / failed / cancelled
  -> 返回 task_id
```

适合：

- 用户明确要求“开子代理”、“委派”、“后台跑”。
- 任务可异步完成，主 agent 可以先返回 task id。
- 子任务有明确 workspace，允许分离上下文。

不适合：

- 只读查询。
- 简单单文件修改。
- 需要严格事务一致性的操作。
- 需要多个角色投票或依赖式协作的复杂任务。

### collab Delegate 执行流

```text
POST /api/v1/agents/delegate
  -> 校验 agent_ids
  -> 查询 registry 中的 AgentProfile
  -> mode=auto 时调用 Planner.Plan
  -> 创建 CollabTask 和 SubTask
  -> 根据 mode 启动 goroutine
        -> pipeline: 前一步输出作为后一步输入
        -> parallel: 并发执行子任务
        -> debate: 多轮立场 + 投票
  -> observePlannedOutcome
  -> Planner.ObserveExecution
```

`mode=auto` 会触发 planner。planner 输出会写入 `task.Metadata`：

- `planner`
- `planned_mode`
- `planner_path`
- `planner_weight`
- `mdp_version`
- `mdp_state`
- `mdp_q_values`
- `mdp_action`
- `planner_trace`

这让协作决策具备可审计性，是后续接入 dashboard 和 trace replay 的基础。

## Planner 模型分析

### Dijkstra + Markov

`internal/collab/planner.go` 把每种可执行 mode 建成从 `start -> mode -> end` 的加权路径。权重由三部分组成：

- Markov transition estimate: success / failure / blocked
- heuristic cost / risk
- MDP adjustment

Planner 选择最小权重路径，并返回完整候选分数。

优点：

- 输出可解释。
- 能在少量历史样本下用 prior 稳定启动。
- 可以把失败、阻塞、风险和成本同时纳入路由。

限制：

- 当前 graph 结构仍偏浅，主要是在 mode 之间选择，不是真正多阶段任务图规划。
- planner 不直接生成子任务拆解，只选择协作模式。
- agent 能力匹配主要依赖注册时的 capability 标签和 benchmark 侧模拟。

### MDP Q-learning

`internal/collab/mdp.go` 把任务抽象成稳定 state：

- task shape
- ambiguity
- risk
- agent bucket
- has critic
- has verifier
- timeout bucket

action 不只是 mode，还包含：

- aggregation
- retry policy
- require verifier
- max steps
- max concurrent

执行完成后，`ObserveExecution` 根据 outcome 和 duration 更新 Q table。

优点：

- 可以从真实执行结果学习。
- action 比单纯 `parallel/pipeline/debate` 更细。
- snapshot 支持持久化和恢复。

限制：

- 当前 server 初始化没有自动加载/保存 MDP snapshot。
- 反馈主要来自任务最终状态，缺少中间事件质量信号。
- reward 仍依赖启发式，缺少人工验收和实际用户满意度反馈。

## 当前能力成熟度

| 能力 | 当前状态 | 说明 |
| --- | --- | --- |
| 子代理异步执行 | 可用 | `delegate_task` 已接入真实 Agent Loop。 |
| 子代理取消 | 可用 | `delegate_cancel` 取消 pending/running 任务，依赖 executor 尊重 context。 |
| 任务状态查询 | 可用 | `task_status` / `list_tasks` 有摘要、过滤、排序和截断。 |
| 多 Agent 注册中心 | 可用 | `/api/v1/agents/register` 可注册 AgentProfile。 |
| pipeline / parallel / debate | 可用但轻量 | 已有执行模型，但 handler 默认只是 `Agent.Chat(task.Input)`。 |
| planner trace | 可用 | `mode=auto` 会写入 planner metadata。 |
| MDP 在线学习 | 部分可用 | 内存中可学习，持久化需要显式 Save/Load。 |
| 统一任务存储 | 缺失 | tool delegate 和 collab task 都以内存为主。 |
| 统一事件流 | 缺失 | 缺少 task created/started/progress/completed 事件总线。 |
| dashboard 展示 | 不完整 | HTTP 有接口，但缺少统一 trace UI 和状态聚合。 |

## 关键问题

### 1. 两套 delegate manager 没有统一

`internal/tool.DelegateManager` 和 `internal/collab.DelegateManager` 解决的是相邻问题，但目前没有共享接口、存储、事件和任务 ID。

影响：

- 用户通过模型工具创建的任务，HTTP `/api/v1/agents/tasks` 看不到。
- HTTP collab 任务，模型侧 `task_status` 看不到。
- dashboard 和 gateway 很难统一展示所有后台协作任务。

建议：

- 抽象统一 `TaskStore` 和 `TaskEventBus`。
- tool delegate 和 collab delegate 都写入同一种 task record。
- 对外保留不同入口，但状态查询走统一索引。

### 2. collab AgentProfile 不是运行中 Agent 实体

`Registry` 注册的是 profile：

```go
type AgentProfile struct {
    ID string
    Capabilities []string
    Status AgentStatus
}
```

它没有绑定独立模型、工具集、cwd、memory scope 或 provider。server 默认 handler 最终仍然调用同一个 `Agent.Chat`。

影响：

- 多 Agent 更像多角色调度，而不是多个隔离 runtime。
- capability 标签只影响规划，不保证执行时真的使用不同能力。
- 安全边界依赖主 Agent，而不是每个 agent 的 sandbox。

建议：

- 给 AgentProfile 增加可选 runtime binding。
- 支持每个 agent 的 tool allowlist、cwd、memory namespace、model/provider。
- 让 handler 根据 `SubTask.AgentID` 选择不同 execution profile。

### 3. 缺少任务持久化

当前两套任务状态都主要存在内存里。进程重启后：

- `delegate_task` 任务丢失。
- `collab` 任务丢失。
- planner 学习样本如果不保存 snapshot 也会丢失。

建议：

- 使用 `$LUCKYAGENT_HOME/tasks/<task-id>/` 做统一本地任务目录。
- 写入 `task.json`、`events.jsonl`、`result.md`、`planner_trace.json`。
- MDP snapshot 定期写入 `$LUCKYAGENT_HOME/planner/mdp.json`。

### 4. 结果聚合偏简单

`Aggregator` 当前支持 concat、best、vote、merge、summary。默认 `AggBest` 使用长度评分，这适合测试，但不足以支撑真实质量判断。

建议：

- 聚合前要求每个子任务输出结构化字段：claim、evidence、risk、files_changed、tests_run。
- vote 模式使用 agent id 和论点 ID，而不是前 100 字符分组。
- debate 模式增加 critic/verifier 独立角色，避免参赛者自评。

### 5. 缺少收敛和成本保护

multi-agent 最大风险不是“不会拆”，而是过度拆分、循环讨论、成本失控和上下文污染。

建议设置硬约束：

- foreground task 默认最多 3 个子任务。
- debate 默认最多 2 轮。
- 子代理默认禁用再开子代理，除非显式允许。
- 每个 parent task 维护 token/time/tool-call budget。
- planner 输出必须记录为什么没有选择 single。

## 推荐目标架构

```text
Task API / Model Tools / Autonomy
        |
        v
Unified Task Orchestrator
        |
        +-- TaskStore
        +-- EventBus
        +-- Planner
        +-- PolicyGuard
        +-- RuntimeResolver
                |
                +-- local Agent Loop
                +-- skill runtime
                +-- MCP tool runtime
                +-- external agent endpoint
```

核心原则：

- 入口可以多个，任务记录必须统一。
- planner 只负责决策，不直接执行。
- executor 只负责执行，不改写 planner trace。
- policy guard 统一管权限、预算、工具 allowlist 和循环限制。
- 所有状态变化都写事件。

## 分阶段路线图

### Phase 1: 统一观测

目标：不大改执行模型，先让状态可看、可审计。

任务：

1. 定义统一 `TaskRecord` 和 `TaskEvent`。
2. tool delegate 写入 task events。
3. collab delegate 写入 task events。
4. `/api/v1/agents/tasks` 和 `list_tasks` 至少能暴露统一字段。
5. `planner_trace` 单独保存，不只塞进 metadata 字符串。

验收：

- 同一个 dashboard 能看到 tool delegate 和 collab task。
- 每个任务能查看 created、started、completed、failed、cancelled。
- planner 决策可追溯。

### Phase 2: 统一持久化

目标：重启后能查询最终状态和结果。

任务：

1. 本地文件 store 或 SQLite store。
2. 任务结果 artifact 化。
3. MDP snapshot 自动保存/加载。
4. 增加保留策略和清理命令。

验收：

- 重启后能查询 completed / failed / cancelled 任务。
- 大结果不塞进 tool response。
- benchmark replay 可以读取历史 task trace。

### Phase 3: Runtime profile 隔离

目标：让“多个 Agent”不只是多个名字。

任务：

1. 扩展 `AgentProfile`：
   - model/provider
   - tool allowlist
   - cwd/workspace
   - memory namespace
   - max iterations
   - approval policy
2. handler 根据 `AgentID` 构建子 runtime。
3. 子代理默认禁止递归开新子代理。

验收：

- 不同 agent 可以使用不同工具集。
- 不同 agent 的 cwd 和 memory scope 不互相污染。
- 高风险 agent 必须人工批准写操作。

### Phase 4: Planner 接入前台工具

目标：让模型工具也能使用 `internal/collab` 的 mode planner。

任务：

1. 给 `delegate_task` 增加可选 `mode=auto|single|parallel|pipeline|debate`。
2. 对复杂任务调用 planner，简单任务保持单子代理。
3. 输出 planner decision summary。
4. 增加 `task_status(include_trace=true)`。

验收：

- 用户要求“分别检查 A/B/C”时能自动选 parallel。
- 用户要求“先调研再实现再测试”时能自动选 pipeline。
- 用户要求“安全和性能辩论”时能自动选 debate。
- 用户要求“不要拆子代理”时必须保持 single。

### Phase 5: 线上学习闭环

目标：让 MDP 真正从运行结果中学习。

任务：

1. 标准化 outcome：success、partial、failed、blocked、cancelled。
2. 记录 verifier result 和用户验收反馈。
3. 定期保存 MDP snapshot。
4. 用 benchmark replay 检查线上策略是否退化。

验收：

- planner 能解释 Q value 和样本数。
- replay 指标不低于 baseline。
- 高风险任务的误拆分率下降。

## 验证方式

### 单元测试

重点包：

```bash
go test ./internal/collab
go test ./internal/server -run 'Agents|Collab'
go test ./internal/tool -run 'Delegate|TaskStatus|ListTasks'
go test ./internal/agent -run 'ToolIntentGating|ToolExecutionGuard|Delegate'
```

### benchmark

离线策略验证：

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
- `LyapunovDecreaseRate`
- `PathRegret`

### 集成验收

建议保留以下真实场景：

1. 只读任务：查看 task 列表，不允许新建子代理。
2. 简单修复：单 agent 完成，不拆分。
3. 并行调研：三个独立资料源，parallel。
4. 依赖开发：调研、实现、测试，pipeline。
5. 高风险决策：安全、性能、产品 debate，critic 汇总。
6. 后台监控：进入 autonomy queue，而不是 foreground 多 agent。
7. 取消任务：pending/running 都能取消，状态可查询。

## 文档结论

LuckyAgent 的 multi-agent 方向已经有足够的基础组件，但下一步不应该先增加更多协作模式，而应该先统一状态、事件、持久化和执行边界。当前最重要的工程目标是：

1. 统一 tool delegate 和 HTTP collab 的任务观测面。
2. 让 AgentProfile 绑定真实执行 profile。
3. 把 planner trace 从 metadata 字符串升级为一等 artifact。
4. 用 benchmark 和 replay 约束 planner 改动，防止过度拆分。

完成这些之后，multi-agent 才能从“可演示的协作能力”升级为“可恢复、可审计、可学习、可控成本的生产级编排系统”。
