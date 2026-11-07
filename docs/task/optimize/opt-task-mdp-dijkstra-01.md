# opt-task-mdp-dijkstra-01

## 目标

把 `docs/multi-agent/multi-agent-analysis.md` 中的 Dijkstra（最短路径：在加权图里找总代价最低路径）和 MDP（马尔可夫决策过程：根据状态、动作、奖励学习策略）迁移到 LuckyAgent 的通用任务优化层。

目标不是替换现有 Agent Loop，而是在任务进入执行前增加一个轻量 `TaskOptimizer`：

```text
User Task
  -> TaskClassifier
  -> TaskGraphBuilder
  -> Dijkstra path selection
  -> MDP policy adjustment
  -> TaskExecutionPlan
  -> Agent Loop / delegate / collab / autonomy
  -> outcome feedback
  -> MDP update
```

一句话版：Dijkstra 负责“这次任务走哪条路径成本最低”，MDP 负责“历史上这种任务哪种路径更容易成功”。

例子：

- 用户说“修一个小 typo”：Dijkstra 应给 `single -> edit -> verify` 最低权重，MDP 如果历史也证明 single 成功率高，就继续压低 single 权重。
- 用户说“分别调研 A/B/C 三个方案并比较”：Dijkstra 会发现 `parallel_research -> aggregate` 比串行更省时间，MDP 根据历史结果决定是否需要 verifier。
- 用户说“先分析、再实现、再测试”：路径应偏向 `pipeline`，不是盲目 parallel。

## 优化后的核心判断

本方案应该按“先观测、再建议、再低风险接管、最后学习”的顺序落地。

不建议第一版就让 MDP 直接控制任务执行。原因是 MDP 依赖稳定反馈，如果 task outcome、用户验收、测试结果和工具成本没有记录完整，Q 值会被脏样本带偏。

更稳妥的目标是：

```text
Phase 1: trace-only，不改变行为
Phase 2: advisory，只给模型/用户解释推荐路径
Phase 3: 低风险参数接管，如 MaxIterations、DisabledTools
Phase 4: delegate/collab mode 接管
Phase 5: outcome 持久化
Phase 6: MDP 学习影响权重
```

关键边界：

- 用户硬约束优先于 planner。
- Dijkstra 初期只做启发式路径推荐。
- MDP 初期只能提供低权重 adjustment。
- 完整 trace 写 artifact，不直接塞进模型上下文。
- 简单任务默认不进入复杂 planner。
- classifier confidence 不足时必须降级为 trace/advisory，不能接管执行。
- planner hint 是软建议，只有明确映射到 `LoopConfig/DisabledTools/StopPolicy` 的字段才能成为硬配置。
- 用户新指令到达后，旧 plan 必须重新校验或废弃。
- MDP 学习必须按项目、任务类型和用户约束分 namespace，不能把一个场景的经验全局套用。

例子：

- 用户说“不要开子代理”，即使 MDP 历史认为 parallel 成功率高，`delegate_parallel` 也必须不可达。
- 用户问“MDP 是什么”，优化器最多推荐 `answer_direct -> final`，不应生成 inspect/search/delegate 路径。

## 二次优化后的核心修正

这版方案需要从“路径优化器”进一步收敛成“可降级的任务建议器”。

核心修正：

```text
classify with confidence
  -> build constrained graph
  -> recommend path
  -> map only safe fields to execution config
  -> observe actual path
  -> learn only verified outcome
```

### 1. 先做 confidence gate

`confidence gate`（置信度门控：分类不确定时不接管执行）必须放在 Dijkstra 前面。

规则：

- `confidence >= 0.8`：允许进入当前阶段可接管的能力。
- `0.5 <= confidence < 0.8`：只 advisory，不改硬配置。
- `confidence < 0.5`：只 trace，不给执行 hint。

例子：

- 用户说“看看这个地方有没有问题”，意图可能是只读分析，也可能是希望修复。分类置信度低时，只能推荐 `inspect_workspace -> final`，不能开启 `edit_files`。

### 2. Plan 必须可失效

`PlanInvalidation`（计划失效：旧计划在条件变化后不再可信）要成为正式机制。

触发条件：

- 用户追加新约束，例如“别改了”“不要联网”“直接给结论”。
- 工具返回关键事实与分类相反，例如本来以为是文档任务，实际发现需要改 Go 代码。
- budget 已接近耗尽。
- verifier 或 test 失败。

例子：

- 初始 plan 是 `inspect -> edit -> test`，用户中途说“只分析不要动文件”，则所有 mutation 节点立即不可达，旧 plan 只能保留为历史 trace。

### 3. Trace 要脱敏和分层

`trace redaction`（轨迹脱敏：移除敏感路径、密钥和长输出）必须在 artifact 写入前执行。

分层：

- `summary trace`：给模型和普通 `task_status` 使用，只包含 path、reason、少量指标。
- `debug trace`：给 dashboard/debug 使用，包含候选权重和实际偏离原因。
- `raw trace`：默认不持久化，除非显式开启 debug。

例子：

- planner 可以记录“用户设置了只读约束”，但不应把包含 token、cookie、`.env` 内容的命令输出写入 trace。

### 4. 学习必须隔离 namespace

MDP 不应维护一个全局 Q 表直接影响所有任务。

建议 namespace：

```text
planner/task_mdp/<project_hash>/<task_family>/<constraint_profile>.json
```

例子：

- 文档写作任务里学到“无需测试”，不能影响 Go 代码修复任务。
- 某用户偏好“少开子代理”，不能影响另一个用户明确要求并行检查的任务。

## 背景依据

`docs/multi-agent/multi-agent-analysis.md` 已经指出：

- `internal/collab/planner.go` 已有 Dijkstra + Markov + MDP planner。
- planner 当前主要选择 `single/parallel/pipeline/debate/autonomy_queue` 等协作模式。
- graph 结构仍偏浅，主要是 `start -> mode -> end`。
- `internal/collab/mdp.go` 已有 Q-learning（Q 表学习：记录状态-动作的长期收益）能力。
- MDP 状态包含 task shape、ambiguity、risk、agent bucket、has critic、has verifier、timeout bucket。
- MDP action 不只是 mode，还包含 aggregation、retry policy、require verifier、max steps、max concurrent。

本优化方案的核心是：把这些能力从“multi-agent mode 选择器”扩展为“通用 task path 选择器”。

## 当前问题

### 1. Task 收束主要是运行时保护

`docs/task/la-agent-task-convergence.md` 已经总结了当前收束机制：

- `MaxIterations`
- `Timeout`
- 重复工具调用检测
- 连续纯工具轮检测
- search synthesis
- memory gate
- tool execution guard
- intent tool gating

这些机制主要在执行中兜底。它们能防止失控，但不能提前选择更优路径。

例子：

- 当前可以防止模型连续搜索 10 轮。
- 但更好的方式是在任务开始前判断“最多搜索 2 次，然后必须综合”。

### 2. Multi-agent planner 和 task loop 没完全打通

`internal/collab` 有 planner trace，但默认前台 `delegate_task`、Agent Loop 和 task 收束还没有统一使用它。

影响：

- 复杂任务是否拆分，仍主要靠模型自己判断。
- planner 的 Q 值和路径权重不能影响普通任务。
- 任务完成后的真实 outcome 没有形成统一学习闭环。

### 3. 缺少任务级可解释 plan

当前 loop 日志能看 iteration、tool calls、tokens，但用户或 dashboard 很难看到：

- 为什么这个任务不拆分。
- 为什么选择 parallel 而不是 pipeline。
- 为什么需要 verifier。
- 为什么限制最多 2 个子任务。
- 为什么中途强制综合。

## 适用场景分级

TaskOptimizer 不应该覆盖所有任务。建议按任务复杂度分级。

| 等级 | 场景 | 是否启用 optimizer | 推荐行为 |
| --- | --- | --- | --- |
| L0 | 简单问答、概念解释 | 不启用或只做 `ToolChoice=none` | 直接回答。 |
| L1 | 简单本地检查 | 启用轻量规则 | 收紧 `MaxIterations/Timeout`，只开放只读工具。 |
| L2 | 单路径工程任务 | 启用 Dijkstra trace | 推荐 `inspect -> edit -> verify -> final`。 |
| L3 | 多源调研或多模块检查 | 启用 Dijkstra + delegate mode 建议 | 判断 single/parallel/pipeline。 |
| L4 | 高风险、多角色、长任务 | 启用 verifier、持久化 trace、可选 MDP | 更保守预算和验证策略。 |

适合优先接入的场景：

- 工程修复：读代码、改文件、跑 focused test。
- 多源调研：多个独立资料源后聚合。
- 多模块审查：多个目录或组件可并行检查。
- 高风险变更：权限、记忆、调度、任务系统。
- 后台任务：适合 autonomy queue，有明确 outcome。

不适合优先接入的场景：

- 一句话解释。
- 用户明确指定完整执行路径。
- 低频一次性任务。
- 没有可验证 outcome 的主观写作任务。

反例：

- “帮我解释 Dijkstra”不需要 task graph。
- “只读分析，不要动文件”不能被 MDP 学习结果改成 edit path。

## 设计原则

### 1. 用户硬约束先变成不可达边

Policy guard（策略守卫：把用户和系统约束转成执行边界）必须先于 Dijkstra 和 MDP。

硬约束包括：

- 不修改文件。
- 不删除。
- 不联网。
- 不开子代理。
- 不写 memory/RAG。
- 不调用外部有副作用 API。

这些约束应直接改变图结构，而不是只增加权重。

例子：

```text
用户：只读分析，不要改文件。

edit_files: unreachable
file_write/file_patch: disabled
terminal write command: blocked by toolExecutionGuard
```

这样即使 MDP 的 Q 值认为 edit 成功率高，也无法选择 edit 路径。

### 2. 分类低置信度时不接管

TaskOptimizer 的第一步不是建图，而是判断自己是否有资格影响执行。

如果 `TaskClassifier`（任务分类器：识别任务形状和风险）输出低置信度，优化器只能记录 trace，不能修改 `LoopConfig`、不能禁用工具、不能开启 delegate。

例子：

```text
用户：帮我处理一下这个问题。

分类结果：
  task_shape=unknown
  confidence=0.42

允许：
  记录 recommended_path 候选。

禁止：
  自动改 MaxIterations。
  自动禁用/启用工具。
  自动开子代理。
```

### 3. 先规划路径，再执行动作

不要让模型在无限开放的工具空间里边走边想。应该先给任务一个初始执行路径。

例子：

```text
Task: 修复一个 Go 单测失败

Plan:
  classify -> inspect_failure -> patch -> run_focused_test -> final
```

这不是强制写死，而是给 loop 一个收束框架。

注意：plan hint 不是系统指令。它只能作为“当前建议路径”，不能覆盖用户最新指令和运行时观测。

### 4. Dijkstra 负责局部最优路径

Dijkstra 适合解决“当前已知图上的最低成本路径”。

在 LA 中，图节点可以是任务阶段：

```text
start
  -> answer_direct
  -> inspect_workspace
  -> search_external
  -> edit_files
  -> run_tests
  -> delegate_parallel
  -> verify
  -> final
```

边权由成本、风险、耗时、工具预算、用户约束组成。

例子：

- 用户明确说“不要改文件”，所有通向 `edit_files` 的边权变成无穷大或不可达。
- 用户问概念解释，`answer_direct -> final` 权重最低。
- 用户要求“跑测试确认”，`run_tests -> final` 边权降低。

### 5. MDP 负责长期策略校正

Dijkstra 只看当前图和启发式权重。MDP 用历史结果调整权重。

例子：

- 某类“高风险代码修改”历史上没有 verifier 时失败率高，MDP 会提高 `require_verifier=true` 的 Q 值。
- 某类“简单文档生成”拆子代理反而慢，MDP 会降低 parallel action 的 Q 值。

### 6. Planner 只做决策，不直接执行

TaskOptimizer 输出计划和 trace，不执行工具。

执行仍由现有系统完成：

- Agent Loop
- `delegate_task`
- `internal/collab`
- autonomy queue
- skill runtime

好处：

- planner 可单测。
- executor 可替换。
- trace 不被执行过程改写。

### 7. Shadow mode 先于执行接管

Shadow mode（影子模式：只记录推荐，不改变实际行为）是第一阶段必须保留的上线方式。

例子：

```text
实际执行：现有 Agent Loop
影子输出：recommended_path=["inspect_workspace","edit_files","run_checks","final"]
对比指标：实际 path 是否更慢、是否多调用工具、是否更早收束
```

只有当 shadow trace 连续通过 benchmark 和真实任务抽样后，才能接管低风险参数。

### 8. 真实执行路径必须可回放

`actual_path`（真实路径：Agent Loop 实际经过的任务阶段）要和 recommended path 一起记录。

映射规则不需要一开始完美，但必须稳定：

| 工具/动作 | 映射节点 |
| --- | --- |
| 读文件、`rg`、`sed`、`ls` | `inspect_workspace` |
| `apply_patch`、文件写入 | `edit_files` |
| `go test`、lint、构建命令 | `run_checks` |
| `delegate_task` | `delegate_single` 或 `delegate_parallel` |
| web/opencli/RAG 查询 | `search_external` 或 `inspect_context` |

例子：

- planner 推荐 `inspect -> edit -> run_checks`，实际没有 `run_checks` 就 final，应记录 `path_deviation_reason=verification_skipped`。

## TaskOptimizer 结构

建议新增任务级优化器：

```go
type TaskOptimizer struct {
    markov *AdaptiveTaskMarkovModel
    mdp    *TaskMDPModel
    policy TaskPolicy
}
```

入口：

```go
func (o *TaskOptimizer) Optimize(req TaskOptimizeRequest) TaskOptimizeResult
func (o *TaskOptimizer) Observe(obs TaskExecutionObservation)
```

请求：

```go
type TaskOptimizeRequest struct {
    SessionID      string
    ProjectID      string
    Namespace      string
    UserInput      string
    ConversationTurn int
    Scope          TaskScope
    AvailableTools []string
    AgentProfiles  []AgentProfileRef
    Constraints    TaskConstraints
    Budget         TaskBudget
}
```

结果：

```go
type TaskOptimizeResult struct {
    Version       string
    State         TaskMDPState
    Action        TaskMDPAction
    Confidence    float64
    EffectiveMode string // trace_only, advisory, config_control, mode_control
    Path          []TaskPlanNode
    TotalWeight   float64
    Candidates    []TaskPathCandidate
    Trace         []TaskPlanEdge
    DecisionBasis string
}
```

## TaskGraph 设计

任务图可以先从固定模板开始，不需要一开始生成任意复杂 DAG。

基础节点：

| 节点 | 含义 |
| --- | --- |
| `start` | 任务开始。 |
| `answer_direct` | 直接回答，不调用工具。 |
| `inspect_context` | 检查 session/memory/RAG/context。 |
| `inspect_workspace` | 读文件、列目录、查代码。 |
| `search_external` | web/search/opencli 外部检索。 |
| `plan_steps` | 生成或更新任务计划。 |
| `edit_files` | 修改本地文件。 |
| `run_checks` | 测试、lint、命令验证。 |
| `delegate_single` | 单个子代理。 |
| `delegate_parallel` | 并行子代理。 |
| `pipeline` | 依赖顺序执行。 |
| `debate_review` | 多角色评审。 |
| `verify` | 独立验证。 |
| `final` | 输出最终回答。 |

简化图：

```text
start
  -> answer_direct -> final
  -> inspect_context -> final
  -> inspect_workspace -> final
  -> inspect_workspace -> edit_files -> run_checks -> final
  -> search_external -> final
  -> delegate_parallel -> verify -> final
  -> pipeline -> verify -> final
  -> debate_review -> final
```

## Dijkstra 权重设计

边权建议由以下部分组成：

```text
edge_weight =
  base_cost
  + latency_cost
  + token_cost
  + risk_cost
  + permission_cost
  + uncertainty_cost
  + coordination_cost
  - user_intent_bonus
  - mdp_adjustment
```

### base_cost

基础成本。

例子：

- `answer_direct -> final` 成本低。
- `delegate_parallel -> verify` 成本高，因为需要多个执行体和聚合。

### latency_cost

耗时成本。

例子：

- web search 比直接回答慢。
- 跑全量测试比跑 focused test 慢。

### token_cost

上下文和输出成本。

例子：

- 大文件阅读、网页抓取、并行子代理都会增加 token。

### risk_cost

副作用或失败风险。

例子：

- `edit_files` 风险高于 `inspect_workspace`。
- `terminal` 写操作风险高于只读命令。

### permission_cost

用户限制带来的成本。

例子：

- 用户说“只读”，通向 `edit_files` 的边不可达。
- 用户说“不要开子代理”，通向 `delegate_*` 的边不可达。

### uncertainty_cost

信息不足时的成本。

例子：

- 用户问“今天”的实时问题，直接回答的 uncertainty 高。
- 应降低 `search_external` 或 `current_time` 相关路径权重。

### coordination_cost

多 agent 协作成本。

例子：

- parallel 有聚合成本。
- debate 有多轮讨论成本。
- pipeline 有等待依赖成本。

### user_intent_bonus

用户明确要求带来的负权重。

例子：

- 用户说“并行检查 A/B/C”，`delegate_parallel` 降权。
- 用户说“先实现再测试”，`pipeline` 降权。

## MDP 状态设计

可以复用 `internal/collab/mdp.go` 的思想，但扩展到通用 task。

```go
type TaskMDPState struct {
    TaskShape      string
    Ambiguity      string
    Risk           string
    ToolNeed       string
    MutationNeed   string
    AgentNeed      string
    HasVerifier    bool
    UserConstraint string
    TimeoutBucket  string
}
```

字段解释：

| 字段 | 例子 |
| --- | --- |
| `TaskShape` | `explain`、`inspect`、`edit`、`research`、`multi_source`、`pipeline`。 |
| `Ambiguity` | 用户描述是否模糊。 |
| `Risk` | 是否会写文件、删文件、调用外部 API。 |
| `ToolNeed` | `none`、`local`、`web`、`memory`、`mixed`。 |
| `MutationNeed` | `none`、`file_write`、`external_side_effect`。 |
| `AgentNeed` | `none`、`single_delegate`、`parallel`、`debate`。 |
| `HasVerifier` | 是否有测试、lint、reviewer 或 verifier。 |
| `UserConstraint` | `read_only`、`no_delegate`、`no_network`、`normal`。 |
| `TimeoutBucket` | `short`、`normal`、`long`。 |

例子：

```text
用户：只读分析这个 bug 可能在哪里，不要改文件。

TaskMDPState:
  TaskShape=inspect
  Ambiguity=medium
  Risk=low
  ToolNeed=local
  MutationNeed=none
  AgentNeed=none
  UserConstraint=read_only
```

## MDP Action 设计

Action 不应该只是“选哪个 mode”，而是完整的执行策略。

```go
type TaskMDPAction struct {
    PathTemplate     string
    Mode             string
    RequireVerifier  bool
    MaxSteps         int
    MaxToolCalls     int
    MaxConcurrent    int
    RetryPolicy      string
    Aggregation      string
    ToolPolicy       string
}
```

例子：

```text
Action A:
  PathTemplate=direct_answer
  MaxSteps=1
  ToolPolicy=none

Action B:
  PathTemplate=inspect_then_answer
  MaxSteps=3
  ToolPolicy=read_only

Action C:
  PathTemplate=edit_then_focused_test
  RequireVerifier=true
  MaxSteps=5
  ToolPolicy=local_mutation

Action D:
  PathTemplate=parallel_research_then_merge
  MaxConcurrent=3
  Aggregation=summary_with_evidence
```

## Reward 设计

MDP 学习依赖 reward（奖励：执行后对这次策略好坏的数值反馈）。

建议 reward 由多项组成：

```text
reward =
  success_score
  + verification_bonus
  + user_acceptance_bonus
  - latency_penalty
  - token_penalty
  - tool_call_penalty
  - retry_penalty
  - coordination_penalty
  - violation_penalty
```

### success_score

按 outcome 给基础分：

| outcome | 分数 |
| --- | --- |
| `success` | `+1.0` |
| `partial` | `+0.3` |
| `blocked` | `-0.2` |
| `failed` | `-0.8` |
| `cancelled` | `-0.4` |

### verification_bonus

如果有真实验证成功，加分。

例子：

- `go test ./internal/agent` 通过：`+0.2`
- 只说“应该没问题”：不加分。

### user_acceptance_bonus

如果用户明确确认，加分。

例子：

- 用户回复“可以，就这样”：`+0.3`
- 用户要求返工：负分。

### violation_penalty

如果违反用户约束，重罚。

例子：

- 用户说“不要改文件”，却执行了写文件路径：`-1.0`
- 用户说“不要开子代理”，却 delegate：`-0.8`

## Dijkstra + MDP 组合方式

组合方式建议沿用 `internal/collab/planner.go` 的思路：

```text
DijkstraWeight = heuristicWeight - mdpQValueAdjustment
```

具体：

```go
func mdpWeightAdjustment(decision TaskMDPDecision, action TaskMDPAction) float64 {
    q := decision.QValues[action.Key()]
    samples := decision.Samples[action.Key()]
    confidence := min(1.0, float64(samples)/20.0)
    return -q * confidence
}
```

解释：

- Q 值高，说明历史收益高，降低路径权重。
- 样本少时 confidence 低，避免 MDP 过早主导。
- 样本足够后，学习结果逐渐影响规划。

例子：

- 初期没有历史，主要靠 heuristic。
- 跑了 50 次后，系统发现 `edit_then_focused_test` 对 Go bugfix 成功率高，就会优先选它。

## TaskExecutionPlan 输出

优化器输出的 plan 应该能直接指导 Agent Loop。

```go
type TaskExecutionPlan struct {
    Path             []string
    MaxIterations    int
    Timeout          time.Duration
    DisabledTools    []string
    RequiredTools    []string
    AllowDelegate    bool
    DelegateMode     string
    RequireVerifier  bool
    VerificationHint string
    StopPolicy       StopPolicy
    PlanID           string
    PlanHardness     string // hint, config, hard_constraint
    InvalidatedBy    string
}
```

`StopPolicy`：

```go
type StopPolicy struct {
    MaxToolCalls              int
    RepeatToolCallLimit       int
    ToolOnlyIterationLimit    int
    ForceSynthesisAfterEvidence int
    DuplicateFetchLimit       int
}
```

映射到现有系统：

- `MaxIterations` -> `LoopConfig.MaxIterations`
- `Timeout` -> `LoopConfig.Timeout`
- `DisabledTools` -> `LoopConfig.DisabledTools`
- `RepeatToolCallLimit` -> `LoopConfig.RepeatToolCallLimit`
- `ToolOnlyIterationLimit` -> `LoopConfig.ToolOnlyIterationLimit`
- `DuplicateFetchLimit` -> `LoopConfig.DuplicateFetchLimit`

## Trace 设计

每次优化都应产生 trace。

```json
{
  "version": "task-dijkstra-mdp-v1",
  "state": {
    "task_shape": "edit",
    "risk": "medium",
    "tool_need": "local",
    "user_constraint": "normal"
  },
  "chosen_path": ["start", "inspect_workspace", "edit_files", "run_checks", "final"],
  "actual_path": ["start", "inspect_workspace", "edit_files", "final"],
  "path_deviation_reason": "verification_skipped",
  "confidence": 0.86,
  "effective_mode": "advisory",
  "total_weight": 1.42,
  "mdp_action": {
    "path_template": "edit_then_focused_test",
    "require_verifier": true,
    "max_steps": 5
  },
  "candidates": [
    {
      "path": ["start", "answer_direct", "final"],
      "weight": 3.9,
      "risk": 0.7,
      "reason": "mutation task needs workspace inspection"
    }
  ]
}
```

用途：

- dashboard 展示。
- benchmark replay。
- debug 为什么过度拆分或没有拆分。
- 给 `task_status(include_trace=true)` 展示。

写入前必须做 trace redaction：

- 截断长输出。
- 移除疑似 token、cookie、secret、authorization header。
- 隐藏敏感绝对路径或只保留 repo 相对路径。
- 默认不保存 raw tool output。

## 接入现有任务系统

接入顺序必须从只读观测开始，不能直接进入执行控制。

推荐依赖链：

```text
StopReason
  -> ConvergenceTrace
  -> actual_path
  -> TaskOptimizer recommended_path
  -> path deviation
  -> advisory hint
  -> low-risk config control
  -> outcome feedback
  -> MDP namespace learning
```

例子：

- 如果还没有 `StopReason`，就无法判断任务是正常完成、保护性停止还是验证失败。
- 如果还没有 `actual_path`，就无法知道 Dijkstra 推荐路径和真实执行路径差多少。
- 如果还没有 outcome，MDP 只能学到模型自评，不能学到真实质量。

### 入口 1: Agent Loop

在 `RunLoopWithSessionInput` 构造 `LoopConfig` 后执行：

```text
TaskOptimizer.Optimize
  -> 调整 LoopConfig
  -> 调整 DisabledTools
  -> 注入 plan hint
```

第一阶段只做观测，不改变行为：

- 生成 trace。
- 记录 recommended plan。
- 不修改实际 loop config。

第二阶段开始启用低风险配置：

- 简单任务降低 max iterations。
- 只读任务禁用写工具。
- 搜索任务设置 force synthesis 阈值。

### 入口 2: delegate_task

给 `delegate_task` 增加可选参数：

```json
{
  "mode": "auto",
  "optimize": true,
  "include_trace": true
}
```

当 `mode=auto`：

- 简单任务保持 single。
- 独立子任务可 parallel。
- 有依赖词“先/再/最后”时偏 pipeline。
- 高风险或争议任务可 debate/review。

### 入口 3: HTTP collab

`internal/collab` 已有 planner。短期可以把 task optimizer 的 state/action/trace 对齐到现有 `PlanResult`，避免重复发明一套格式。

### 入口 4: autonomy queue

后台任务适合使用更保守策略：

- 更高 verifier 权重。
- 更低并发。
- 更强持久化要求。
- outcome 必须写回 MDP。

## MVP 切片

第一版不要完整实现 Dijkstra + MDP。推荐切成四个小闭环。

### Slice 1: 收束观测

目标：让现有 Agent Loop 能解释“为什么停”。

范围：

- 增加 `StopReason`。
- 增加 `ConvergenceTrace`。
- 粗粒度记录 `actual_path`。
- 不改变任何执行行为。

验收：

- 直接回答、单工具后回答、重复工具停止、搜索综合、memory gate blocked 都有明确 stop reason。
- `actual_path` 至少能区分 `answer_direct`、`inspect_workspace`、`search_external`、`edit_files`、`run_checks`、`final`。

### Slice 2: Trace-only optimizer

目标：生成推荐路径，但不影响执行。

范围：

- 新增 `internal/taskopt` 或同等包。
- 实现 task shape classifier。
- 构建固定 TaskGraph。
- 输出 recommended path、candidate weights、confidence。
- trace 写 artifact 或 debug，不进入普通上下文。

验收：

- 低置信度任务只 trace。
- 用户硬约束会删除不可达边。
- recommended path 和 actual path 能并排查看。

### Slice 3: Advisory hint

目标：让 planner 帮助主 agent 更快收束，但不改硬配置。

范围：

- 给 context debug 或 task status 增加 decision summary。
- 只注入短 plan hint。
- 用户新指令触发 plan invalidation。

验收：

- hint 不包含完整权重表。
- 旧 plan 失效后不会继续影响后续轮次。
- 工具调用数量不高于 baseline。

### Slice 4: Low-risk config control

目标：只接管不会改变任务语义的配置。

范围：

- 简单任务降低 `MaxIterations/Timeout`。
- 只读任务增加 `DisabledTools`。
- 搜索任务调整 force synthesis 阈值。
- 禁止 delegate 时隐藏 delegate 工具。

验收：

- `UserConstraintViolationRate == 0`。
- `ClassifierLowConfidenceControlRate == 0`。
- `ToolWasteRate` 不高于 baseline。

MDP 学习必须等 Slice 1 到 Slice 4 都稳定后再启用。

## 分阶段实施

### Phase 0: 基线冻结

目标：在优化器介入前，先拿到现有任务系统的基线。

任务：

1. 固化当前 `RunLoop`、`delegate_task`、`task_status` 的关键测试。
2. 记录简单任务、工程修复、搜索调研、delegate 任务的 baseline 指标。
3. 定义 stop reason、tool waste、over delegation 的统计口径。

验收：

- 能回答“优化前平均几轮完成、调用多少工具、失败原因是什么”。
- benchmark 和真实 replay 有可对比记录。

### Phase 1: Trace-only Dijkstra

目标：先看 planner 会怎么选，不改变实际执行。

任务：

1. 定义 `TaskOptimizeRequest/Result`。
2. 实现 task shape classifier。
3. 构建固定 TaskGraph。
4. 输出 Dijkstra candidate trace。
5. 把 trace 写入 task metadata 或 session debug。
6. 明确 `planner_effective=false`，表示没有接管执行。
7. 增加 classifier confidence 和 namespace 字段。
8. 增加 trace redaction。

验收：

- 普通任务能看到 recommended path。
- trace 能解释为什么选择 single/parallel/pipeline。
- 不影响现有测试。
- trace 与实际执行的差异可统计。
- 低置信度任务只能 trace，不产生执行 hint。
- trace 不包含敏感环境变量、cookie、token 或长原始输出。

### Phase 2: Advisory Mode

目标：让 planner 给模型或 UI 提示，但仍不改变硬配置。

任务：

1. 在 context debug 或 task status 中展示 decision summary。
2. 对复杂任务生成短 plan hint，例如“建议先检查本地状态，再修改，再跑 focused test”。
3. plan hint 不包含完整候选权重，避免污染模型上下文。
4. 用户明确路径优先于 planner hint。
5. 当用户新指令改变约束时，旧 plan 标记为 invalidated。

验收：

- 模型能更快收束，但工具可见性和预算仍由现有逻辑控制。
- 用户或 dashboard 能看到 planner 推荐但不会被强制执行。
- 旧 plan 失效后不会继续注入到后续上下文。

### Phase 3: 低风险配置接管

目标：只接管不会改变任务语义的参数。

允许接管：

- 简单任务调整 `MaxIterations/Timeout`。
- 只读任务增加 `DisabledTools`。
- 搜索任务降低 synthesis 触发阈值。
- 用户禁止 delegate 时禁用 delegate 工具。

禁止接管：

- 自动开启写文件能力。
- 自动开启 delegate。
- 自动联网。
- 自动跳过用户要求的验证。

验收：

- 解释类任务不暴露工具。
- 只读任务不能写文件。
- 搜索任务证据足够后自动综合。
- 任何用户硬约束都不能被 planner 覆盖。

### Phase 4: delegate/collab mode 接入

目标：让前台委派能使用 Dijkstra + MDP mode planner。

任务：

1. `delegate_task(mode=auto)` 调用 optimizer，但默认只启用 Dijkstra。
2. 输出 planner decision summary。
3. `task_status(include_trace=true)` 展示路径和候选权重。
4. 子代理默认禁止递归 delegate。
5. `mode=auto` 必须受 parent task budget 限制。

验收：

- A/B/C 独立检查选择 parallel。
- 有依赖顺序选择 pipeline。
- 高风险决策选择 debate/review。
- 用户说“不拆”时保持 single。
- simple task 不能被拆成 delegate。

### Phase 5: Outcome 持久化

目标：先记录高质量反馈，不急着学习。

任务：

1. 标准化 outcome。
2. 记录 duration、tool calls、tokens、verification result。
3. 记录 user acceptance 或 follow-up correction。
4. 记录 constraint violation。
5. 保存 task trace artifact。

验收：

- 每个 completed/failed/blocked/cancelled task 都有 outcome。
- 能区分“模型自称完成”和“验证完成”。
- 重启后仍能 replay task trace。

### Phase 6: MDP 学习闭环

目标：让策略从真实 outcome 中学习，但只在样本足够时影响权重。

任务：

1. 根据持久化 outcome 更新 Q 表。
2. 定期保存 `$LUCKYAGENT_HOME/planner/task_mdp.json`。
3. 给每个 action 计算 `samples` 和 `confidence`。
4. `confidence < threshold` 时只做 trace，不做权重 adjustment。
5. benchmark replay 检查策略退化。
6. 按 project/task_family/constraint_profile 保存 namespace Q 表。

验收：

- planner trace 能展示 Q value、samples、confidence。
- 重启后 Q table 不丢。
- replay 指标不低于 baseline。
- MDP adjustment 不会恢复 policy guard 标记为 unreachable 的路径。
- 不同项目、任务类型和用户硬约束的学习结果不会互相污染。

## 上线门槛

每个阶段进入下一阶段前必须满足：

| 阶段 | 门槛 |
| --- | --- |
| Phase 1 -> 2 | trace 生成稳定，不影响现有输出和测试。 |
| Phase 2 -> 3 | advisory hint 不增加无效工具调用。 |
| Phase 3 -> 4 | 只读/禁用 delegate/禁用联网等硬约束 100% 生效。 |
| Phase 4 -> 5 | delegate mode auto 的 over-delegation rate 低于阈值。 |
| Phase 5 -> 6 | outcome、verification、duration、tool cost 有稳定记录。 |

建议阈值：

- `UserConstraintViolationRate == 0`
- `OverDelegationRate <= 5%`
- `ToolWasteRate` 不高于 baseline
- `ConvergenceTurns` 不高于 baseline
- 高风险任务 `UnderVerificationRate <= 5%`
- `ClassifierLowConfidenceControlRate == 0`
- `TraceSecretLeakRate == 0`
- `PlanInvalidationMissRate == 0`
- `CrossNamespacePolicyLeakRate == 0`

## 验证方式

### 单元测试

建议新增：

```bash
go test ./internal/taskopt
go test ./internal/collab
go test ./internal/agent -run 'TaskOptimizer|ToolIntentGating|Loop'
go test ./internal/tool -run 'Delegate|TaskStatus'
```

### Benchmark

复用 multi-agent benchmark：

```bash
go run ./cmd/la-multiagent-bench -variant baseline -scenario all -rounds 1
go run ./cmd/la-multiagent-bench -variant runtime-mdp-v1 -scenario all -rounds 1
go run ./cmd/la-multiagent-bench -variant math-full-v1 -scenario heavy -rounds 1
```

新增 task optimizer 指标：

- `PathRegret`：实际路径与最优路径差距。
- `OverDelegationRate`：不该拆却拆的比例。
- `UnderVerificationRate`：应验证却未验证比例。
- `ToolWasteRate`：无效工具调用比例。
- `ConvergenceTurns`：完成任务所需轮数。
- `UserConstraintViolationRate`：用户限制被违反比例。

## 风险与保护

### 1. MDP 早期样本少

风险：少量错误样本让策略偏掉。

保护：

- 样本少时只作为轻微 adjustment。
- 设置 confidence。
- 保留 heuristic 下限。
- `confidence < threshold` 时不改变 Dijkstra 权重。

### 2. 过度拆分

风险：planner 为了“高级”选择 parallel/debate，反而慢。

保护：

- foreground 默认最多 3 个子任务。
- simple task 禁止 delegate。
- delegate 有 coordination cost。
- 用户未要求多 agent 时 single 先验更强。
- `mode=auto` 第一版只允许 L3/L4 任务进入 delegate candidate。

### 3. 用户约束被 planner 覆盖

风险：Dijkstra/MDP 选择了违反用户约束的路径。

保护：

- 用户硬约束先转成不可达边。
- MDP adjustment 不能恢复不可达边。
- `toolExecutionGuard` 作为执行时最后防线。
- trace 中必须记录哪些边因为用户约束被删除。

### 4. Trace 太大

风险：planner trace 塞进模型上下文或 tool response 造成噪音。

保护：

- tool response 只返回 decision summary。
- 完整 trace 写 artifact。
- `include_trace=true` 时再展开。

### 5. Planner 和真实执行脱节

风险：planner 推荐 `inspect -> edit -> test`，但 Agent Loop 实际走了 search 或 delegate，trace 变成装饰。

保护：

- Phase 1/2 允许脱节，但必须记录 `actual_path`。
- Phase 3 后，接管字段必须明确映射到 `LoopConfig`、`DisabledTools`、`StopPolicy`。
- 如果实际执行偏离推荐路径，记录 `path_deviation_reason`。

### 6. 低质量 outcome 污染学习

风险：模型自称完成但没有测试，MDP 把它当 success。

保护：

- outcome 分成 `model_claimed_success` 和 `verified_success`。
- reward 对 verified success 加分，对未验证 success 保守给分。
- 用户返工、测试失败、工具错误都要回写 observation。

### 7. TaskClassifier 误判

风险：分类器把只读分析误判成修改任务，或把复杂工程任务误判成简单问答。

保护：

- 输出 `confidence`。
- 低置信度只 trace/advisory。
- 高风险能力接管必须同时满足 `confidence` 和用户意图证据。
- trace 中记录分类依据，方便回放。

### 8. 权重校准错误

风险：Dijkstra 会稳定选择“权重最低”的路径，但权重本身可能错。

例子：

- `run_checks` 权重过高会导致系统倾向跳过测试。
- `delegate_parallel` 权重过低会导致过度拆分。

保护：

- 每条边权拆分展示 base、latency、risk、coordination、user bonus。
- benchmark 中加入权重敏感性测试。
- 关键边权变化需要 replay 验证。

### 9. Plan 绑架主 Agent

风险：plan hint 进入上下文后，主 Agent 把它当硬规则，忽略新证据。

保护：

- plan hint 文案明确是建议，不是系统约束。
- 只有 `PlanHardness=hard_constraint` 且来自 PolicyGuard 的字段才能作为硬限制。
- 用户新指令、verifier 失败、budget 变化会触发 plan invalidation。

### 10. Trace 泄露或污染上下文

风险：trace artifact 包含敏感路径、密钥片段、长命令输出，或把大量 debug 信息塞回模型上下文。

保护：

- trace 写入前执行 redaction。
- 默认只返回 summary trace。
- `include_trace=true` 也只展开 debug trace，不展开 raw output。
- raw trace 需要显式 debug flag。

### 11. Namespace 污染

风险：一个项目或任务类型学到的策略影响另一个项目。

保护：

- MDP Q table 按 project/task_family/constraint_profile 分 namespace。
- namespace 样本不足时回退 heuristic。
- 不允许跨 namespace 直接复用高风险 action 的 Q 值。

### 12. Actual path 映射不准

风险：真实工具调用无法正确映射回计划节点，导致 replay 和 MDP 学习失真。

保护：

- 第一版只要求粗粒度稳定映射。
- 无法识别的工具调用记为 `unknown_tool_action`，不参与高权重学习。
- `path_deviation_reason` 必须记录 unknown、user_override、tool_error、budget_limit、verifier_failed 等原因。

## 降级策略

任何阶段发现指标退化，应能快速降级：

```text
mdp_enabled=false
task_optimizer_mode=trace_only
delegate_auto_mode=false
planner_hints=false
task_optimizer_config_control=false
task_optimizer_mode_control=false
task_optimizer_namespace_learning=false
task_optimizer_raw_trace=false
```

降级后保留 trace 记录，但不改变执行。

典型触发：

- 用户约束违规。
- over delegation 增加。
- 平均完成轮数上升。
- 工具调用浪费率上升。
- MDP Q table 出现异常偏置。

## 推荐落地点

短期新增包：

```text
internal/taskopt/
  classifier.go
  graph.go
  dijkstra.go
  mdp.go
  optimizer.go
  trace.go
```

或者先复用 `internal/collab`：

```text
internal/collab/task_optimizer.go
```

更推荐第一种，因为 task optimizer 不只服务 multi-agent，也服务普通 Agent Loop、tool gating 和 autonomy。

## 推荐提交顺序

为了降低回滚成本，建议按下面粒度提交：

1. `docs/task: describe convergence and optimizer boundaries`
2. `task: add stop reason and convergence trace`
3. `taskopt: add trace-only classifier and graph`
4. `taskopt: record recommended and actual paths`
5. `agent: expose advisory task plan hints`
6. `agent: enable low-risk task optimizer config controls`
7. `taskopt: persist outcomes for planner replay`
8. `taskopt: add namespaced MDP learning`

每个提交都应该能独立解释风险和回滚方式。

例子：

- 如果 advisory hint 导致模型多调用工具，只回滚第 5 个提交，不影响 stop reason。
- 如果 MDP 学习污染策略，只关闭第 8 个提交或设置 `mdp_enabled=false`，不影响 Dijkstra trace。

## 最小闭环

最小可交付版本：

1. 识别 task shape。
2. 构造固定 TaskGraph。
3. 用 Dijkstra 输出 recommended path。
4. 不改变执行，只保存 trace。
5. 在 `task_status` 或 context debug 中展示推荐。

第二步再启用行为接管：

1. 简单任务收紧 loop。
2. 只读任务禁用写工具。
3. 搜索任务强制综合。

第三步才接入 MDP 学习。

## 总结

Dijkstra 和 MDP 在 task 优化中的分工应保持清晰：

- Dijkstra 管“当前任务按什么路径执行成本最低”。
- MDP 管“历史上这种状态选什么动作收益最高”。
- Policy guard 管“哪些路径绝对不能走”。
- Agent Loop 管“按计划执行并在观察后收束”。

这样 LuckyAgent 的任务系统会从“执行中防失控”升级为“执行前选路径、执行中控风险、执行后学习”的闭环。
