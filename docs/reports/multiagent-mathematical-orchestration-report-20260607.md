# LuckyHarness 多 Agent 数学化编排实验报告

日期：2026-06-07
对象：`cmd/lh-multiagent-bench` 与下一代多 Agent runtime planner
状态：方案型实验报告。本文不是新的 benchmark 跑分结果，而是基于当前 17 case benchmark、现有报告、arXiv 相邻研究和数学建模讨论，提出下一版严格实验设计。

## 0. 结论先行

当前 LuckyHarness 多 Agent benchmark 已经证明：多 Agent 有收益，但现有策略主要还是启发式。

现有 `dependency-aware` 在 final 结果里表现最好：

| Variant | Success | ModeAcc | CapRecall | RouteRisk | DepViol | CoordOH | Score | Clean |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | :---: |
| baseline | 0.7059 | 0.7059 | 0.3549 | 1.7081 | 0.1765 | 0.2303 | 0.6168 | false |
| capability-routed | 0.9412 | 0.9412 | 0.9154 | 0.1002 | 0.0000 | 0.1514 | 0.9008 | true |
| parallel-routed | 0.9412 | 0.9412 | 0.9461 | 0.1002 | 0.0000 | 0.1494 | 0.9077 | true |
| dependency-aware | 1.0000 | 1.0000 | 0.9881 | 0.0237 | 0.0000 | 0.1502 | 0.9369 | true |
| debate-review | 1.0000 | 1.0000 | 0.9881 | 0.0237 | 0.0000 | 0.1814 | 0.9366 | true |

但是这个结果不能直接说明 runtime 已经具备严格数学编排能力，因为当前 `dependency-aware` 在 synthetic suite 中直接使用 `GoldMode`，更像 oracle upper bound，而不是真实 planner。

本文给出的下一代方向是：

```text
Contextual Stochastic Shortest Path MDP
= 上下文随机最短路马尔可夫决策过程
```

其中：

- `Contextual`：任务上下文进入边概率模型，解决不同任务之间的泛化问题。
- `Stochastic`：边不是确定的，执行、调试、验收都有失败概率。
- `Shortest Path`：规划目标可以写成成功率、成本、风险、延迟的路径优化。
- `MDP`：Planner 每一步都要选择动作，而不是只被动预测下一步。

最终建议：

```text
不要用单一算法替代所有编排逻辑。

MDP 负责动作选择；
Dijkstra / A* 负责候选路径搜索；
Lyapunov Potential Field 负责稳定性和防循环；
Verifier 负责真实验收；
Online Learning 负责边参数更新。
```

一句话：LuckyHarness 下一步要从“启发式多 Agent benchmark”升级为“带概率、风险、稳定性证明的任务编排系统”。

## 1. 当前实验的数学缺口

### 1.1 当前 benchmark 已经有数学化指标

现有报告里已经定义了：

- `ModeAcc`：协作模式准确率。
- `SplitAcc`：是否拆分准确率。
- `SubtaskRecall`：子任务召回率。
- `CapRecall`：能力覆盖率。
- `RouteRisk`：路由风险。
- `CoordOH`：协调开销。
- `P(success)`：logistic 成功概率。
- `MultiAgentScore`：综合分数。

这些指标是有数学形式的。例如成功概率：

```text
P(success) = sigmoid(z)
```

其中：

```text
z =
  -0.55
+ 0.70 * SplitAcc
+ 0.92 * ModeAcc
+ 1.15 * SubtaskRecall
+ 0.80 * CapRecall
+ 0.35 * CapPrecision
+ 0.42 * RoleFit
+ 0.52 * AggregationQuality
+ 0.26 * ConsensusQuality
+ 0.22 * min(Speedup / 2, 1)
+ 0.28 * CriticRecall
+ 0.22 * BackgroundCorrect
- 0.78 * CoordOH
- 0.42 * RouteRisk
- 0.72 * DependencyViolations
- 0.95 * ForbiddenModeHit
```

这说明当前实验已经不是纯观察报告，而是有指标体系。

### 1.2 当前 benchmark 的策略仍是启发式

主要问题是：指标数学化，不等于编排数学化。

当前策略里：

- `baseline` 根据关键词判断是否拆分。
- `capability-routed` 根据能力重叠分配 agent。
- `parallel-routed` 更清楚地区分 parallel、pipeline、debate、autonomy。
- `dependency-aware` 和 `debate-review` 在 synthetic suite 中使用 `GoldMode`。

这意味着当前最优结果不能直接代表真实 runtime planner 的能力。

例子：

```text
Prompt:
先读代码，再改 benchmark，再跑测试，最后汇总结果。

GoldMode:
pipeline
```

在 benchmark 里，`dependency-aware` 可以直接返回 `pipeline`。
但真实 runtime 里，Planner 必须从自然语言、代码状态、历史 trace 和风险约束中推断出 `pipeline`，不能直接读取标准答案。

### 1.3 因此下一版实验目标要改变

当前实验问的是：

```text
如果策略知道或近似知道正确模式，指标会如何变化？
```

下一版实验应该问：

```text
在没有 GoldMode 的真实任务中，Planner 如何用概率模型、图算法和稳定性约束，选择是否召唤子代理、召唤谁、按什么顺序执行、何时停止？
```

这才是数学化编排问题。

## 2. 相关研究线索

本节列出和 LuckyHarness 下一代编排模型最相关的 arXiv 方向。它们不是完全等价的现成答案，但可以拼成理论路线。

### 2.1 LLM 多 Agent 编排

1. `Reinforcement Learning for LLM-based Multi-Agent Systems through Orchestration Traces`
   arXiv: https://arxiv.org/abs/2605.02801

   这篇论文把 LLM 多 Agent 的编排过程写成 `orchestration traces`，也就是由 `spawn`、`delegate`、`communicate`、`aggregate`、`stop` 等事件组成的时序图。
   对 LuckyHarness 的启发是：不要只记录最终成功或失败，而要记录每条编排边的执行证据。

2. `Verified Multi-Agent Orchestration: A Plan-Execute-Verify-Replan Framework`
   arXiv: https://arxiv.org/abs/2603.11445

   这篇论文使用 `Plan-Execute-Verify-Replan` 闭环，把复杂问题分解成 DAG（有向无环图：表达子任务依赖关系）。
   对 LuckyHarness 的启发是：Verifier 应该是图中的硬节点，而不是最终摘要里的软描述。

3. `MASFactory: A Graph-centric Framework for Orchestrating LLM-Based Multi-Agent Systems`
   arXiv: https://arxiv.org/abs/2603.06007

   这篇论文把多 Agent workflow 自然建模成 directed computation graph（有向计算图），节点执行 agent 或子 workflow，边表达依赖和消息传递。
   对 LuckyHarness 的启发是：`Subtask`、`Agent`、`Artifact`、`Verifier` 都应成为一等图结构。

4. `AdaptOrch: Task-Adaptive Multi-Agent Orchestration`
   arXiv: https://arxiv.org/abs/2602.16873

   这篇论文强调 `orchestration topology`，也就是 agent 如何串行、并行、汇总，本身会强烈影响系统表现。
   对 LuckyHarness 的启发是：下一版 benchmark 不应该只比较 agent 数量，而要比较拓扑。

### 2.2 验证、交接和规划

1. `Verification-Aware Planning for Multi-Agent Systems`
   arXiv: https://arxiv.org/abs/2510.17109

   重点是多 Agent 失败经常来自任务理解、输出格式和 handoff（交接）错位，而不只是推理错。
   对 LuckyHarness 的启发是：Verifier 要验收 handoff artifact 是否满足下游 agent 的输入契约。

2. `Learning to Hand Off: Provably Convergent Workflow Learning under Interface Constraints`
   arXiv: https://arxiv.org/abs/2605.19140

   这篇强调 `provably convergent`，也就是在接口约束下学习可收敛 workflow。
   对 LuckyHarness 的启发是：handoff 不能只靠自然语言，应有 interface schema 和收敛条件。

3. `TwoStep: Multi-agent Task Planning using Classical Planners and Large Language Models`
   arXiv: https://arxiv.org/abs/2403.17246

   使用 PDDL（Planning Domain Definition Language：经典符号规划语言）和 LLM 做多 Agent 任务规划。
   对 LuckyHarness 的启发是：依赖约束可以符号化，例如 `read -> edit -> test -> report`。

### 2.3 MDP 和随机最短路

1. `On Solving a Stochastic Shortest-Path Markov Decision Process as Probabilistic Inference`
   arXiv: https://arxiv.org/abs/2109.05866

   研究 SSP MDP（随机最短路马尔可夫决策过程：边转移有概率，目标是到达终止状态并最小化累计代价）。
   对 LuckyHarness 的启发是：Agent 编排天然就是“从任务起点到成功吸收态”的随机最短路问题。

2. `Risk-aware Stochastic Shortest Path`
   arXiv: https://arxiv.org/abs/2203.01640

   引入 CVaR（条件风险价值：关注最坏一部分结果的风险）来做风险敏感控制。
   对 LuckyHarness 的启发是：不能只优化平均成本，还要控制最坏情况下的失败风险和 token 爆炸。

3. `Stochastic Shortest Path Problem with Failure Probability`
   arXiv: https://arxiv.org/abs/2409.16672

   将失败概率直接放进随机最短路。
   对 LuckyHarness 的启发是：`Failure` 应该是吸收态，且 Planner 要显式约束失败概率。

4. `Stochastic Shortest Paths and Weight-Bounded Properties in Markov Decision Processes`
   arXiv: https://arxiv.org/abs/1804.11301

   研究带权 MDP 里的随机最短路和权重边界性质。
   对 LuckyHarness 的启发是：`CoordOH`、`RouteRisk`、`Cost`、`Latency` 都可以作为路径累计权重。

## 3. 形式化问题定义

### 3.1 任务上下文

每个用户请求先映射成上下文特征：

```text
x = phi(task, repo_state, session_state, user_constraints)
```

其中：

- `task`：自然语言任务。
- `repo_state`：当前代码仓库、测试、分支、脏文件状态。
- `session_state`：上下文窗口、历史记忆、工具状态。
- `user_constraints`：用户显式限制，例如“不要动原页面”“不要新建后台 worker”。

`phi` 是特征函数。它输出：

```text
x = [
  task_type,
  dependency_depth,
  subtask_independence,
  ambiguity,
  risk_level,
  test_observability,
  artifact_size,
  context_sufficiency,
  tool_availability,
  agent_capability_match,
  user_forbidden_modes
]
```

例子：

```text
任务：先读代码，再改 benchmark，再跑测试，最后汇总结果。

dependency_depth = high
subtask_independence = low
test_observability = high
risk_level = medium
user_forbidden_modes = []
```

反例：

```text
任务：解释多 agent 为什么不一定比单 agent 更好。

dependency_depth = low
subtask_independence = low
test_observability = low
risk_level = low
```

这个任务不应该因为出现 `agent` 这个词就开子代理。

### 3.2 状态集合

定义状态集合 `S`：

```text
S = {
  s_task_received,
  s_plan_drafted,
  s_subtasks_ready,
  s_executor_running,
  s_executor_done,
  s_debugger_running,
  s_debugger_passed,
  s_verifier_running,
  s_verified,
  s_replan_needed,
  s_success,
  s_failure
}
```

其中：

- `s_success` 是成功吸收态。
- `s_failure` 是失败吸收态。

吸收态（absorbing state：进入后不再离开）很重要，因为它让我们可以计算最终成功概率。

### 3.3 动作集合

定义动作集合 `A`：

```text
A = {
  a_answer_single,
  a_plan,
  a_spawn_backend_agent,
  a_spawn_frontend_agent,
  a_spawn_test_agent,
  a_spawn_docs_agent,
  a_spawn_research_agent,
  a_spawn_critic_agent,
  a_run_debugger,
  a_run_verifier,
  a_replan,
  a_stop
}
```

动作不是固定对应某个 agent，而是包含角色和约束。

例子：

```text
a_spawn_test_agent
```

表示把可验证的测试任务委派给具备 `test`、`benchmark`、`validation` 能力的子代理。

### 3.4 转移概率

核心转移概率是：

```text
P_x(s' | s, a)
```

意思是：在上下文 `x` 下，系统处于状态 `s`，执行动作 `a` 后，到达状态 `s'` 的概率。

例如：

```text
P_x(s_debugger_passed | s_executor_done, a_run_debugger)
```

表示：当前执行器已经完成产物，运行 Debugger 后通过的概率。

这个概率不能写死。它应该由边特征模型生成。

### 3.5 成本、风险、延迟

每个动作还有成本：

```text
C_x(s, a)
```

风险：

```text
R_x(s, a)
```

延迟：

```text
T_x(s, a)
```

例子：

```text
a_spawn_critic_agent
```

通常会增加 `C` 和 `T`，但降低 `R`。
所以它适合高风险任务，不适合简单问答。

## 4. 边概率模型：泛化性的核心

### 4.1 不学习具体边，学习边的生成规律

错误做法：

```text
Planner -> Executor 在任务 A 成功率 = 0.82
Planner -> Executor 在任务 B 成功率 = 0.61
```

这种做法没有泛化能力。新任务 C 到来时，系统不知道该用哪个概率。

正确做法：

```text
p_e(x) = f_theta(z_e)
```

其中：

- `p_e(x)`：上下文 `x` 下边 `e` 成功的概率。
- `z_e`：边特征。
- `f_theta`：从历史执行中学到的参数化模型。

### 4.2 logistic 边模型

最简单可解释模型是 logistic model（逻辑回归模型：把线性分数压到 0 到 1）。

```text
p_e(x) = sigma(theta^T z_e)
```

其中：

```text
sigma(u) = 1 / (1 + exp(-u))
```

边特征：

```text
z_e = psi(s, a, s', x)
```

可以包含：

```text
z_e = [
  capability_match,
  dependency_satisfied,
  context_sufficiency,
  historical_agent_success,
  test_coverage,
  artifact_complexity,
  ambiguity,
  risk_level,
  tool_availability,
  verifier_strictness
]
```

于是：

```text
P(DebuggerPass | ExecutorDone, RunDebugger, x)
= sigma(
    theta_1 * capability_match
  + theta_2 * test_coverage
  + theta_3 * dependency_satisfied
  + theta_4 * context_sufficiency
  - theta_5 * artifact_complexity
  - theta_6 * ambiguity
  - theta_7 * risk_level
)
```

例子：

```text
小 diff + 有测试 + backend-agent 能力匹配高
=> Debugger 通过概率高

大 diff + 没有测试 + 需求模糊
=> Debugger 通过概率低
```

### 4.3 用最大似然学习参数

历史数据：

```text
D = {(z_i, y_i)}
```

其中：

- `z_i`：第 `i` 次边转移的特征。
- `y_i`：是否成功。成功为 `1`，失败为 `0`。

负对数似然损失：

```text
L(theta)
= - sum_i [
    y_i log p_i
  + (1 - y_i) log(1 - p_i)
  ]
```

其中：

```text
p_i = sigma(theta^T z_i)
```

直觉：

- 模型预测 `0.9` 但实际失败，损失很大。
- 模型预测 `0.8` 且实际成功，损失较小。
- 模型预测 `0.5`，说明它知道自己不确定。

### 4.4 加入 logprobs 特征

如果底层 LLM API 暴露 `logprobs`，可以把它作为不确定性特征之一。

定义输出 token 平均对数概率：

```text
AvgLogProb = (1 / n) * sum_t log p(y_t | y_<t, prompt)
```

也可以定义 normalized uncertainty：

```text
U_lm = - AvgLogProb
```

`U_lm` 越高，表示模型生成该计划时越不确定。

例子：

```text
Planner 输出计划时，关键动作 token 的 top-2 概率很接近：

P("parallel") = 0.36
P("pipeline") = 0.33

这说明 mode selection 不稳定，应触发 Verifier 或先做 dry-run。
```

注意：logprobs 不能直接证明答案正确。它只表示模型在当前上下文下对 token 的生成偏好。

## 5. MDP：Planner 的动作选择模型

### 5.1 从马尔可夫链到 MDP

马尔可夫链（Markov Chain：下一状态只依赖当前状态）写作：

```text
P(s_{t+1} | s_t)
```

它只能描述“会发生什么”。

Planner 需要决定“做什么”，所以要使用 MDP（Markov Decision Process：状态转移受动作影响）：

```text
P(s_{t+1} | s_t, a_t, x)
```

其中：

- `s_t`：当前状态。
- `a_t`：当前动作。
- `x`：任务上下文。
- `s_{t+1}`：下一状态。

### 5.2 Bellman 方程

Planner 的目标是最小化期望总代价：

```text
J*(s, x)
= min_a [
    C_x(s, a)
  + lambda_r R_x(s, a)
  + lambda_t T_x(s, a)
  + sum_{s'} P_x(s' | s, a) J*(s', x)
]
```

这就是 Bellman equation（动态规划核心公式：当前最优 = 当前成本 + 未来最优期望）。

例子：

在状态 `s_plan_drafted`，Planner 有两个动作：

```text
a_answer_single
a_spawn_test_agent
```

如果任务可测试、风险高、代码 diff 大，则：

```text
J*(s, a_spawn_test_agent) < J*(s, a_answer_single)
```

系统应该开 test-agent。

反例：

如果任务只是概念解释，`a_spawn_test_agent` 会增加成本但不增加成功率，因此：

```text
J*(s, a_answer_single) < J*(s, a_spawn_test_agent)
```

系统不应该拆。

### 5.3 召唤子代理的效用差

定义动作效用：

```text
U(s, a, x)
= V_task * P_success(s, a, x)
- lambda_c E[cost | s, a, x]
- lambda_t E[time | s, a, x]
- lambda_r E[risk | s, a, x]
```

召唤子代理条件：

```text
U(s, a_spawn, x) - U(s, a_single, x) > delta_spawn
```

其中 `delta_spawn` 是启动门槛。

这个门槛很重要。它防止系统因为一点点收益就开很多 agent。

例子：

```text
P_success(single) = 0.91
P_success(spawn)  = 0.93
cost(single) = 1000
cost(spawn)  = 3500
```

如果任务价值不高，`spawn` 不划算。

另一个例子：

```text
P_success(single) = 0.55
P_success(spawn)  = 0.86
cost(single) = 3000
cost(spawn)  = 5200
```

如果任务风险高且必须成功，`spawn` 划算。

## 6. Dijkstra / A*：路径搜索层

### 6.1 为什么 Dijkstra 可以用于最大成功路径

如果路径成功概率为：

```text
P(path) = product_{e in path} p_e
```

最大化路径成功率：

```text
argmax_path product_e p_e
```

等价于最小化负对数：

```text
argmin_path sum_e -log(p_e)
```

定义边权：

```text
w_e = -log(p_e)
```

因为：

```text
0 < p_e <= 1
=> -log(p_e) >= 0
```

所以满足 Dijkstra（非负边权最短路算法）的条件。

### 6.2 加入成本、风险、延迟

实际边权不应只看成功概率：

```text
w_e =
  -log(p_e)
+ lambda_c cost_e
+ lambda_t time_e
+ lambda_r risk_e
+ lambda_d dep_violation_e
```

其中：

- `cost_e`：token 或金钱成本。
- `time_e`：延迟。
- `risk_e`：风险。
- `dep_violation_e`：依赖违例惩罚。

例子：

```text
路径 A:
成功率高，但要开 8 个 agent，成本高。

路径 B:
成功率略低，但只开 2 个 agent，且有测试和验收。
```

如果 `lambda_c` 很高，系统会选路径 B。
如果任务必须成功且预算充足，系统可能选路径 A。

### 6.3 Dijkstra 的局限

Dijkstra 适合普通路径：

```text
s0 -> s1 -> s2 -> success
```

但多 Agent 编排经常是 AND-OR graph（与或图：有些分支必须全部完成，有些分支可以择一）。

并行任务：

```text
backend AND frontend AND docs -> integrate
```

这不是一条普通路径，而是一个 AND join。

如果三个必要分支的成功概率分别是：

```text
p_backend = 0.90
p_frontend = 0.88
p_docs = 0.95
p_integrate = 0.92
```

那么整体成功率近似：

```text
P_parallel
= 0.90 * 0.88 * 0.95 * 0.92
= 0.6922
```

这个例子说明：多 Agent 分支越多，未必越好。每个必要分支都会乘进整体成功率。

### 6.4 对 LuckyHarness 的建议

路径搜索层建议分两类：

```text
普通串行候选:
  使用 Dijkstra / A*

DAG 并行候选:
  使用 DAG dynamic programming 或 AND-OR graph search
```

其中 A*（带启发函数的最短路搜索）可以使用：

```text
h(s) = 预计剩余工作势能
```

这和后面的 Lyapunov 函数可以共享部分特征。

## 7. Lyapunov Potential Field：稳定性和防循环

### 7.1 Lyapunov 函数不是 Dijkstra 的替代品

Lyapunov Potential Field（李雅普诺夫势能场：给状态定义一个会持续下降的能量函数）解决的是稳定性问题。

Dijkstra 解决：

```text
哪条路最短？
```

Lyapunov 解决：

```text
系统会不会在执行中发散、循环、越修越坏？
```

因此它们不是替代关系，而是互补关系。

### 7.2 定义任务势能

定义状态势能：

```text
V(s, x)
= alpha * remaining_work(s, x)
+ beta * uncertainty(s, x)
+ gamma * risk(s, x)
+ delta * dependency_violation(s, x)
+ eta * test_failure_count(s, x)
+ mu * cost_overrun(s, x)
+ nu * handoff_mismatch(s, x)
```

其中：

- `remaining_work`：剩余工作量。
- `uncertainty`：不确定性。
- `risk`：风险。
- `dependency_violation`：依赖违例。
- `test_failure_count`：测试失败数量。
- `cost_overrun`：成本超预算程度。
- `handoff_mismatch`：交接产物是否不符合下游要求。

### 7.3 稳定性条件

每一步要求：

```text
E[V(s_{t+1}, x) | s_t, a_t] - V(s_t, x) <= -epsilon
```

其中：

```text
epsilon > 0
```

含义：执行动作后，期望势能必须下降。

例子：

```text
Debugger 发现测试失败，返回 Executor 修复。
```

这个循环可以接受，但前提是：

```text
失败测试数下降
或者风险下降
或者不确定性下降
```

如果连续两轮测试失败数不变，势能没有下降，就应该触发：

```text
a_replan
```

而不是无限重试。

### 7.4 有限状态下的终止证明

假设：

1. 状态空间有限。
2. 势能下界为 `0`。
3. 每次非终止动作都满足：

```text
E[V_{t+1} - V_t] <= -epsilon
```

那么系统不能无限执行非终止动作。

直觉证明：

如果执行 `T` 步，期望势能至少下降：

```text
T * epsilon
```

但初始势能有限：

```text
V_0 < infinity
```

且势能不能小于 `0`。因此：

```text
T <= V_0 / epsilon
```

所以系统必须在有限步内进入：

```text
success / failure / replan-stop
```

这就是 Lyapunov 在 Agent 编排里的价值：它不是找最短路，而是防止系统空转。

## 8. Verifier：把“完成步骤”变成“真实成功”

### 8.1 Debugger 和 Verifier 的区别

Debugger（调试器：检查低级错误）负责：

```text
代码能不能编译？
测试能不能跑？
格式有没有错？
schema 是否匹配？
```

Verifier（验收者：验证真实目标是否达成）负责：

```text
产物是否满足用户目标？
真实场景是否可用？
是否违反用户约束？
是否值得交付？
```

例子：

```text
任务：把多 agent 实验报告写出来。
```

Debugger 可以检查：

```text
Markdown 文件存在；
表格格式正常；
链接格式正常。
```

Verifier 要检查：

```text
报告是否真的解释了数学模型；
是否覆盖 MDP / Dijkstra / Lyapunov；
是否没有把未跑实验写成已跑结果；
是否适合后续落地。
```

### 8.2 Verifier 是吸收态前的硬门

图结构：

```text
ExecutorDone -> DebuggerPassed -> VerifierPassed -> Success
```

不能直接：

```text
ExecutorDone -> Success
```

否则系统只能证明“做了”，不能证明“做对了”。

### 8.3 Verifier 也有转移概率

定义：

```text
P_x(s_success | s_verifier_running, a_verify)
```

这个概率受以下因素影响：

```text
artifact_quality
acceptance_criteria_clarity
test_coverage
real_world_check_available
user_constraint_satisfaction
```

如果验收标准不清晰，Verifier 通过概率应降低，Planner 应先澄清或生成验收标准。

## 9. 泛化性分析

### 9.1 泛化来自特征空间，而不是任务 ID

如果系统只记忆具体任务：

```text
任务 A 成功率 = 0.82
任务 B 成功率 = 0.61
```

它无法处理任务 C。

如果系统学习：

```text
p_e(x) = f_theta(z_e)
```

它可以处理新任务，因为新任务也能映射成 `z_e`。

### 9.2 Lipschitz 假设

泛化需要一个平滑性假设：

```text
|p*(z_1) - p*(z_2)| <= L ||z_1 - z_2||
```

这叫 Lipschitz continuity（利普希茨连续性：输入相似，输出不会剧烈跳变）。

含义：

两个任务在特征空间里越接近，它们的边成功率越接近。

例子：

```text
任务 1:
Go 后端小 diff，有测试，验收明确。

任务 2:
Go 后端小 diff，有测试，验收明确。
```

它们的 `Executor -> Debugger -> Verifier` 成功概率应该相近。

反例：

```text
任务 1:
Markdown 报告写作。

任务 2:
生产数据库迁移。
```

即使自然语言都包含“生成报告”，它们的风险结构完全不同，不能直接泛化。

### 9.3 误差随路径长度累积

边权：

```text
w_e = -log(p_e)
```

如果概率预测误差满足：

```text
|p_e - p_hat_e| <= epsilon
```

且：

```text
p_e >= p_min
```

则近似：

```text
|w_e - w_hat_e| <= epsilon / p_min
```

一条路径有 `k` 条边：

```text
|W(path) - W_hat(path)| <= k * epsilon / p_min
```

这说明：任务越复杂、路径越长，预测误差越容易累积。

因此复杂任务必须加入中途校正：

```text
Debugger
Verifier
Replan
```

不能只相信开局的一次规划。

### 9.4 分布内与分布外

定义分布内任务：

```text
z_new 接近历史训练分布
```

比如：

- 常见 Go 后端修复。
- 常见 benchmark 实验。
- 常见文档审查。
- 常见 UI 小改。

定义分布外任务：

```text
z_new 远离历史训练分布
```

比如：

- 新工具链。
- 新业务域。
- 高风险生产操作。
- 没有历史验收模式的复杂任务。

分布外时应触发：

```text
uncertainty_gate = true
```

策略：

```text
少拆分；
先澄清；
先 dry-run；
提前 Verifier；
限制自动执行；
提高 critic 权重。
```

## 10. 约束优化目标

最终 Planner 不应只最大化成功率，也不应只最小化成本。

建议目标：

```text
maximize_a U(s, a, x)
```

其中：

```text
U(s, a, x)
= V_task * P_success(s, a, x)
- lambda_c E[cost | s, a, x]
- lambda_t E[time | s, a, x]
- lambda_r E[risk | s, a, x]
- lambda_o E[coord_overhead | s, a, x]
```

约束：

```text
P_success >= tau_success
P_failure <= tau_failure
E[cost] <= B_cost
E[risk] <= B_risk
CoordOH <= B_coord
DependencyViolations = 0
ForbiddenMode = 0
E[V(s') - V(s)] <= -epsilon
```

这就是 constrained optimization（约束优化：在满足安全条件下优化目标）。

例子：

```text
用户要求只查看 autonomy 状态，不要新增后台 worker。
```

约束：

```text
ForbiddenMode(autonomy_queue) = 1
```

因此任何包含 `autonomy_queue` 的路径都应被直接剪枝，而不是进入加权比较。

## 11. 新 benchmark 设计

### 11.1 新增策略变体

建议新增以下 variants：

```text
math-mdp-v1
math-ssp-v1
math-lyapunov-v1
math-verifier-v1
math-full-v1
```

含义：

| Variant | 含义 |
| --- | --- |
| `math-mdp-v1` | 使用上下文 MDP 做动作选择 |
| `math-ssp-v1` | 使用随机最短路边权搜索候选路径 |
| `math-lyapunov-v1` | 加入势能下降约束，防止循环 |
| `math-verifier-v1` | 强化 Debugger / Verifier 硬门 |
| `math-full-v1` | MDP + SSP + Lyapunov + Verifier 完整模型 |

### 11.2 新增 trace 结构

每条 benchmark record 不只写 summary，还应写 orchestration trace：

```json
{
  "trace_id": "run-001",
  "task_id": "L3-002",
  "context_features": {
    "task_type": "release",
    "dependency_depth": 4,
    "subtask_independence": 0.15,
    "ambiguity": 0.20,
    "risk_level": 0.75,
    "test_observability": 0.90
  },
  "edges": [
    {
      "from": "plan_drafted",
      "action": "spawn_test_agent",
      "to": "executor_running",
      "p_hat": 0.82,
      "cost_hat": 900,
      "risk_hat": 0.30,
      "lyapunov_before": 4.8,
      "lyapunov_after_hat": 3.9
    }
  ],
  "verifier": {
    "required": true,
    "passed": true
  }
}
```

### 11.3 新指标

新增指标：

```text
EdgeNLL
CalibrationECE
ExpectedUtility
LyapunovDecreaseRate
ReplanRate
VerifierCatchRate
OODRate
PathRegret
```

解释：

| 指标 | 含义 |
| --- | --- |
| `EdgeNLL` | 边概率负对数似然，越低越好 |
| `CalibrationECE` | 概率校准误差，预测 0.8 的边是否真有约 80% 成功 |
| `ExpectedUtility` | 期望效用 |
| `LyapunovDecreaseRate` | 势能下降比例 |
| `ReplanRate` | 重规划比例 |
| `VerifierCatchRate` | Verifier 捕获问题的比例 |
| `OODRate` | 分布外任务比例 |
| `PathRegret` | 相对 oracle 路径的后悔值 |

### 11.4 校准指标

如果模型预测：

```text
p_hat = 0.8
```

那么大量样本中，实际成功率应接近 `0.8`。

ECE（Expected Calibration Error：期望校准误差）：

```text
ECE = sum_b (|B_b| / n) * |acc(B_b) - conf(B_b)|
```

其中：

- `B_b`：第 `b` 个概率分桶。
- `acc(B_b)`：该桶真实成功率。
- `conf(B_b)`：该桶平均预测概率。

例子：

```text
所有预测 0.7 到 0.8 的边中，平均预测是 0.75，但真实成功率是 0.60。
```

说明模型过度自信。

### 11.5 后悔值

定义 oracle 最优路径效用：

```text
U_oracle
```

当前策略路径效用：

```text
U_policy
```

后悔值：

```text
Regret = U_oracle - U_policy
```

`Regret` 越低，说明策略越接近理想决策。

在 synthetic suite 中 oracle 可以来自 `GoldMode`。
在 live replay 中 oracle 可以来自人工标注或事后成功路径。

## 12. 新 case 设计

当前 17 case 覆盖了：

```text
single: 4
parallel: 4
pipeline: 3
debate: 3
autonomy: 3
```

下一版建议扩展到至少 60 case：

| 场景 | 数量 | 新增重点 |
| --- | ---: | --- |
| `single` | 10 | 抗过度拆分 |
| `parallel` | 10 | 独立分支与 AND join |
| `pipeline` | 10 | 依赖深度与拓扑排序 |
| `debate` | 8 | critic gate 与高风险决策 |
| `autonomy` | 8 | 异步队列与禁止误触发 |
| `debug_loop` | 6 | Debugger 修复循环 |
| `verifier_reject` | 4 | Verifier 拒绝后重规划 |
| `ood` | 4 | 分布外检测 |

### 12.1 Debug loop case

示例：

```text
先实现一个 benchmark 指标，再跑测试。如果测试失败，定位原因并修复；最多重试两轮，最后报告是否通过。
```

Gold structure：

```text
plan -> edit -> test -> debug_if_failed -> verify -> report
```

要测：

```text
Lyapunov 是否下降；
是否无限重试；
是否正确触发 replan。
```

### 12.2 Verifier reject case

示例：

```text
生成多 agent 报告，但必须包含数学推导、反例、实验指标和落地路线。不要只写总结。
```

Gold structure：

```text
write -> verifier_check_requirements -> revise_if_missing -> success
```

要测：

```text
Verifier 是否能发现“只写了摘要，没写推导”的失败。
```

### 12.3 OOD case

示例：

```text
用一个之前没出现过的新工具链完成生产迁移，并保证可回滚。
```

要测：

```text
模型是否降低自动执行强度；
是否先澄清；
是否提前加入 verifier；
是否避免高风险自动操作。
```

## 13. 落地到 LuckyHarness 的模块设计

### 13.1 新增包建议

```text
internal/orchestrator/
  features.go
  mdp.go
  edge_model.go
  graph_search.go
  lyapunov.go
  verifier.go
  trace.go
```

职责：

| 文件 | 职责 |
| --- | --- |
| `features.go` | 提取任务、状态、agent、artifact 特征 |
| `mdp.go` | 定义状态、动作、Bellman 评估 |
| `edge_model.go` | 预测边概率、成本、风险 |
| `graph_search.go` | Dijkstra / A* / DAG 搜索 |
| `lyapunov.go` | 势能函数与下降约束 |
| `verifier.go` | 验收标准和验收结果 |
| `trace.go` | 记录 orchestration trace |

### 13.2 与现有 benchmark 对接

当前：

```text
runStrategy(cfg, task, agents) -> strategyResult
```

下一版：

```text
planner := NewMathematicalPlanner(edgeModel, graphSearch, lyapunov, verifier)
result := planner.Plan(ctx, task, agents)
```

输出：

```text
strategyResult + orchestrationTrace + mathDiagnostics
```

新增 diagnostics：

```text
{
  "expected_utility": 0.74,
  "path_weight": 1.82,
  "success_probability_hat": 0.86,
  "failure_probability_hat": 0.08,
  "lyapunov_start": 5.2,
  "lyapunov_end_hat": 2.1,
  "constraint_violations": []
}
```

### 13.3 runtime dry-run

真实 runtime 先不要直接执行多 Agent，而是 dry-run：

```text
用户请求 -> Planner 生成候选编排图 -> 写入 trace -> 仍由单 agent 执行
```

收集一段时间后比较：

```text
planner 预测应该 parallel 的任务，事后是否真的更适合 parallel？
planner 预测高风险的任务，是否真的更容易失败？
planner 预测 verifier 必要的任务，是否真的被 verifier 抓到问题？
```

只有当校准稳定后，再逐步启用真实多 Agent。

## 14. 实验路线图

### 阶段 1：离线数学模拟

目标：

```text
在 synthetic suite 中验证公式和指标。
```

任务：

- 扩展 case 到 60。
- 新增 `math-mdp-v1`、`math-ssp-v1`、`math-lyapunov-v1`。
- 输出 orchestration trace。
- 计算新指标。

验收：

```text
ModeAcc >= 0.90
ForbiddenModeCount = 0
DependencyViolationCount = 0
CoordOH <= 0.20
RouteRisk <= 0.20
LyapunovDecreaseRate >= 0.95
CalibrationECE <= 0.10
```

### 阶段 2：历史会话回放

目标：

```text
验证模型是否能泛化到真实 LuckyHarness 任务。
```

任务：

- 抽取历史 `lh` 会话。
- 人工或半自动标注：
  - 是否该拆。
  - 应该是什么 mode。
  - 子任务依赖。
  - 是否需要 verifier。
  - 实际是否成功。
- 训练边概率模型。
- 比较 heuristic 和 math planner。

验收：

```text
SplitAcc >= 0.85
ModeAcc >= 0.80
VerifierNeedAcc >= 0.85
OOD false negative <= 0.05
```

### 阶段 3：runtime dry-run

目标：

```text
不改变用户体验，只记录 planner 预测。
```

任务：

- 每次真实任务生成候选编排图。
- 写入 trace。
- 不自动开子代理。
- 事后比较预测与结果。

验收：

```text
CalibrationECE <= 0.10
HighRiskPrecision >= 0.80
SpawnDecisionPrecision >= 0.80
```

### 阶段 4：小流量启用

目标：

```text
只在低风险、高置信度任务中启用真实多 Agent。
```

启用条件：

```text
P_success_spawn - P_success_single >= delta_spawn
Risk <= B_risk
OOD = false
ForbiddenMode = 0
LyapunovDecreaseExpected = true
VerifierAvailable = true
```

## 15. 与当前 benchmark 的关系

当前 benchmark 的价值：

- 它证明 baseline 不够干净。
- 它证明 capability routing 有显著收益。
- 它证明 dependency guard 是关键。
- 它证明 debate-review 不能默认全开。

新数学模型不是推翻当前 benchmark，而是解释为什么这些现象成立。

### 15.1 为什么 capability routing 有收益

在边模型里：

```text
capability_match ↑
=> p_e ↑
=> -log(p_e) ↓
=> 路径权重下降
=> ExpectedUtility 上升
```

所以 capability-routed 成功率提升是合理的。

### 15.2 为什么 dependency-aware 更稳

在约束里：

```text
DependencyViolations = 0
```

不是软优化，而是硬约束。

因此 pipeline 任务不能为了 speedup 被并行化。

### 15.3 为什么 debate-review 不该默认开

critic 会：

```text
risk ↓
cost ↑
time ↑
coord_overhead ↑
```

只有当风险下降带来的效用超过成本增加时，才应该开。

数学条件：

```text
Delta U_critic > delta_critic
```

## 16. 需要避免的错误结论

### 16.1 不能说 Dijkstra 替代 Planner

Dijkstra 只能在已知图和边权后找路径。

Planner 还要：

- 构造图。
- 选择动作。
- 判断是否召唤子代理。
- 处理不确定性。
- 动态重规划。

所以 Dijkstra 是 Planner 的一个子模块。

### 16.2 不能说 Lyapunov 替代 Dijkstra

Lyapunov 保证稳定性，不保证全局最短路。

正确关系：

```text
Dijkstra / A*: 找候选路径
Lyapunov: 检查路径执行过程中是否收敛
```

### 16.3 不能说 logprobs 等于正确率

logprobs 只是 token 概率。

它可以帮助判断：

- Planner 是否犹豫。
- mode selection 是否不稳定。
- 输出是否低置信。

但不能证明：

- 代码一定正确。
- 报告一定满足需求。
- 子代理一定完成任务。

### 16.4 不能说更多 Agent 一定更好

并行 AND 图中：

```text
P_success = product_i p_i
```

必要分支越多，任何一个失败都会拖垮整体。
多 Agent 的收益来自独立性和能力匹配，不来自数量本身。

## 17. 最终建议

下一代 LuckyHarness 多 Agent 编排应采用：

```text
Contextual Stochastic Shortest Path MDP
+ Risk-aware Objective
+ Lyapunov Stability Guard
+ Verification Gate
+ Online Calibration
```

对应系统结构：

```text
User Request
  -> Feature Extractor
  -> MDP Planner
  -> Candidate Graph Builder
  -> Edge Probability Model
  -> Dijkstra / A* / DAG Search
  -> Lyapunov Guard
  -> Debugger Gate
  -> Verifier Gate
  -> Executor
  -> Trace Logger
  -> Online Learner
```

最终验收目标：

```text
ModeAcc >= 0.90
ForbiddenModeCount = 0
DependencyViolationCount = 0
CoordOH <= 0.20
RouteRisk <= 0.20
CalibrationECE <= 0.10
LyapunovDecreaseRate >= 0.95
VerifierCatchRate >= 0.80
OOD false negative <= 0.05
```

一句话收口：

LuckyHarness 的多 Agent 不应该只是“会开子代理”，而应该成为一个能计算、能证明、能校准、能收敛的编排系统。
真正的目标不是更多 agent，而是更好的决策边界：什么时候不拆，什么时候并行，什么时候串行，什么时候辩论，什么时候后台执行，什么时候停止。
