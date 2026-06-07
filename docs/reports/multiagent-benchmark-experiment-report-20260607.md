# LuckyHarness 多 Agent Benchmark 实验报告

日期：2026-06-07
对象：`cmd/lh-multiagent-bench`
结论先行：多 Agent 值得做，但默认优化方向不应该是“开更多 agent”，而应该是“先判断该不该拆，再判断怎么拆，再判断交给谁，最后判断是否需要 critic/review”。本轮实验显示，`dependency-aware` 是当前最适合进入运行时设计的默认策略；`debate-review` 质量相近，但协调开销更高，应该只在高风险决策场景按需开启。

## 1. 实验目的

本实验研究 LuckyHarness 的多 Agent 路由器，也就是一个“任务调度脑”：用户给出一个请求后，系统到底应该：

- 保持 `single`：单 agent 直接完成。
- 使用 `parallel`：多个互相独立的子任务并行执行，最后聚合。
- 使用 `pipeline`：有先后依赖的子任务按顺序执行。
- 使用 `debate`：多个 agent 给出不同方案，由 critic 或投票收敛。
- 使用 `autonomy_queue`：放入后台 worker 异步执行。

注意，这不是证明“多 Agent 一定比单 Agent 好”的实验。真正要回答的问题是：什么时候多 Agent 反而会制造错误、成本和协调噪声。

例子：
用户说“解释多 agent 为什么不一定更好”，这应该是 `single`。如果系统因为看见 “agent” 这个词就拆多个子代理，反而会过度调度。
用户说“先读代码，再改 benchmark，再跑测试，最后汇总结果”，这应该是 `pipeline`。如果系统把它当成 `parallel`，就可能在没有读代码之前改文件，或者在没有改完之前跑测试。

## 2. 实验对象

本轮 benchmark 固定使用 17 个合成任务，覆盖 5 类场景：

| 场景 | 任务数 | 目标 |
| --- | ---: | --- |
| `single` | 4 | 测系统是否能忍住不拆 |
| `parallel` | 4 | 测独立子任务是否能并行拆分并正确聚合 |
| `pipeline` | 3 | 测有依赖任务是否能保持顺序 |
| `debate` | 3 | 测架构/风险/公式权重类问题是否能用 critic 收敛 |
| `autonomy` | 3 | 测后台队列是否只在明确异步意图下启用 |

候选策略共 5 组：

| 策略 | 含义 |
| --- | --- |
| `baseline` | 关键词触发式路由。看到 agent、worker、benchmark、autonomy 等词就容易拆分或入队 |
| `capability-routed` | 加入能力匹配，把子任务分给更合适的 agent |
| `parallel-routed` | 更明确区分 parallel、pipeline、debate、autonomy |
| `dependency-aware` | 加入依赖识别，按任务依赖选择正确模式 |
| `debate-review` | 在 dependency-aware 基础上增强 critic/review 聚合 |

## 3. 实验命令

测试：

```bash
TMPDIR=/media/shiokou/26040C2B040C0113/tmp/lh-go-tmp \
GOTMPDIR=/media/shiokou/26040C2B040C0113/tmp/lh-go-tmp \
GOCACHE=/media/shiokou/26040C2B040C0113/tmp/lh-go-cache \
/usr/local/go/bin/go test ./cmd/lh-multiagent-bench
```

运行 baseline：

```bash
TMPDIR=/media/shiokou/26040C2B040C0113/tmp/lh-go-tmp \
GOTMPDIR=/media/shiokou/26040C2B040C0113/tmp/lh-go-tmp \
GOCACHE=/media/shiokou/26040C2B040C0113/tmp/lh-go-cache \
/usr/local/go/bin/go run ./cmd/lh-multiagent-bench \
  -variant baseline \
  -scenario all \
  -rounds 1 \
  -out docs/reports/multiagent-bench-final-baseline-20260607.jsonl
```

运行对照组：

```bash
/usr/local/go/bin/go run ./cmd/lh-multiagent-bench -variant capability-routed -scenario all -rounds 1 -out docs/reports/multiagent-bench-final-capability-routed-20260607.jsonl
/usr/local/go/bin/go run ./cmd/lh-multiagent-bench -variant parallel-routed -scenario all -rounds 1 -out docs/reports/multiagent-bench-final-parallel-routed-20260607.jsonl
/usr/local/go/bin/go run ./cmd/lh-multiagent-bench -variant dependency-aware -scenario all -rounds 1 -out docs/reports/multiagent-bench-final-dependency-aware-20260607.jsonl
/usr/local/go/bin/go run ./cmd/lh-multiagent-bench -variant debate-review -scenario all -rounds 1 -out docs/reports/multiagent-bench-final-debate-review-20260607.jsonl
```

汇总对比：

```bash
/usr/local/go/bin/go run ./cmd/lh-multiagent-bench \
  -compare docs/reports/multiagent-bench-final-baseline-20260607.jsonl,docs/reports/multiagent-bench-final-capability-routed-20260607.jsonl,docs/reports/multiagent-bench-final-parallel-routed-20260607.jsonl,docs/reports/multiagent-bench-final-dependency-aware-20260607.jsonl,docs/reports/multiagent-bench-final-debate-review-20260607.jsonl
```

说明：之前如果使用 `go test -exec='env TMPDIR=/tmp'` 会失败，因为本机 `/tmp` 空间不足；改为使用大盘上的 `TMPDIR/GOTMPDIR/GOCACHE` 后，`./cmd/lh-multiagent-bench` 测试通过。

## 4. 指标定义与公式

### 4.1 模式准确率

`ModeAcc` 衡量系统有没有选对协作模式。

令：

- `M` 表示预测模式。
- `M*` 表示标准答案模式。

则：

```text
ModeAcc = 1[M = M*]
```

这里的 `1[条件]` 是指示函数：条件成立时取 1，否则取 0。

例子：
标准答案是 `pipeline`，系统预测 `parallel`，则 `ModeAcc = 0`。这类错很危险，因为它会把有依赖顺序的任务错误并行化。

### 4.2 拆分准确率

`SplitAcc` 只关心“是否应该拆”，不关心具体拆成哪种模式。

```text
SplitAcc = 1[(M != single) = (M* != single)]
```

例子：
标准答案是 `debate`，系统预测 `parallel`，这时 `SplitAcc = 1`，因为它至少知道要拆；但 `ModeAcc = 0`，因为拆法错了。

### 4.3 子任务召回率

`SubtaskRecall` 衡量该做的子任务有没有覆盖全。

令：

- `S_pred` 表示实际执行的子任务集合。
- `S_gold` 表示标准答案中的子任务集合。

```text
SubtaskRecall = |S_pred ∩ S_gold| / |S_gold|
```

例子：
发布收口任务需要 `status -> baseline -> report -> commit` 四步。系统只做了 `status`，则：

```text
SubtaskRecall = 1 / 4 = 0.25
```

这说明它不是“没有出错”，而是漏掉了 75% 的关键流程。

### 4.4 子任务精确率

`SubtaskPrecision` 衡量执行的子任务里有多少是必要的。

```text
SubtaskPrecision = |S_pred ∩ S_gold| / |S_pred|
```

例子：
用户只要求“查看 autonomy 状态，不要新增后台 worker”。如果系统额外创建后台 worker，那么召回可能仍然高，但精确率会下降，并且会触发 forbidden mode 风险。

### 4.5 能力召回率

`CapRecall` 衡量被分配的 agent 是否覆盖了任务所需能力。

令：

- `C_required` 表示任务需要的能力集合。
- `C_agents` 表示被分配 agent 的能力集合。

```text
CapRecall = |C_agents ∩ C_required| / |C_required|
```

例子：
任务需要 `go、benchmark、test、docs`。如果系统只用了 generalist 和 repo-agent，覆盖了 `go`，但没有覆盖 `benchmark、test、docs`，能力召回就会很低。

### 4.6 能力精确率

`CapPrecision` 衡量选出来的 agent 能力是否集中在当前任务上。

```text
CapPrecision = |C_agents ∩ C_required| / |C_agents|
```

能力召回和能力精确率是一对取舍：
召回高说明该覆盖的能力基本覆盖了；精确高说明没有引入太多无关能力。多 Agent 系统不能只追求“什么能力都有”，否则上下文会膨胀，协调成本也会上升。

### 4.7 依赖违例

`DependencyViolations` 衡量有依赖顺序的子任务是否被错误执行。

如果某个子任务 `s_i` 依赖 `s_j`，记作：

```text
s_j -> s_i
```

则合法执行顺序必须满足：

```text
position(s_j) < position(s_i)
```

如果系统用 `parallel` 执行这类依赖链，或者在 `pipeline` 里把依赖放反，就会产生依赖违例。

例子：
`read -> edit -> test -> report` 中，`test` 依赖 `edit`。如果还没 patch benchmark 就运行 test，测试结果没有意义。

### 4.8 路由风险

`RouteRisk` 是一个惩罚项，衡量错误路由带来的风险。

```text
RouteRisk =
  2.5 * 1[forbidden_mode]
+ 1.3 * 1[M != M*]
+ 0.8 * 1[M* = single and M != single]
+ Σ_i risk_i * (1 - assignment_score_i) * (1 + agent_risk_bias_i)
+ 1.15 * DependencyViolations
+ debate/background penalties
```

含义拆开看：

- `forbidden_mode`：触发禁止模式，比如用户明确说不要后台 worker，系统却进了 `autonomy_queue`。
- `M != M*`：协作模式错了。
- `M* = single and M != single`：本来不该拆，系统强行拆。
- `risk_i`：第 `i` 个子任务本身风险。
- `assignment_score_i`：子任务和 agent 能力匹配程度。
- `agent_risk_bias_i`：该 agent 的风险偏置。
- `DependencyViolations`：依赖违例次数。

例子：
把“只查看 autonomy 状态”误路由到 `autonomy_queue`，会同时触发 forbidden mode 和 single 被错误拆分，因此风险会显著上升。

### 4.9 协调开销

`CoordOH` 表示多 Agent 调度本身花掉了多少成本。它混合 token 开销和延迟开销：

```text
CoordOH =
  0.55 * CoordinatorTokens / TotalTokens
+ 0.45 * CoordinatorLatency / CriticalPathLatency
```

解释：

- `CoordinatorTokens / TotalTokens`：协调器消耗的 token 占比。
- `CoordinatorLatency / CriticalPathLatency`：协调器延迟占关键路径延迟的比例。
- `0.55` 和 `0.45` 是权重，表示 token 成本略重于延迟成本。

例子：
如果一个任务本身只需要 500 token，但调度多个 agent 又花了 800 token，那多 Agent 就不划算。

### 4.10 并行收益

`Speedup` 衡量多 Agent 是否真的更快：

```text
Speedup = SingleAgentLatency / CriticalPathLatency
```

`ParallelEfficiency` 衡量并行是否高效：

```text
ParallelEfficiency = Speedup / NumExecutedSubtasks
```

例子：
如果 4 个 agent 并行后只快了 1.2 倍，那么效率并不高；如果 3 个独立检查任务并行后快了 3 倍左右，才是真正有效的并行。

### 4.11 成功概率

实验使用一个 logistic 模型把多个指标合成成功概率：

```text
P(success) = sigmoid(z)
```

其中：

```text
sigmoid(z) = 1 / (1 + exp(-z))
```

线性项为：

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

为什么用 `sigmoid`：
因为成功概率需要被压到 `[0, 1]`。线性加权可能大于 1 或小于 0，而 `sigmoid` 可以把任意实数映射成概率。

例子：
如果一个策略模式准确、能力覆盖高、没有依赖违例，它的 `z` 会变大，`P(success)` 接近 1。
如果一个策略触发 forbidden mode、依赖违例多、协调开销高，它的 `z` 会变小，`P(success)` 接近 0。

### 4.12 综合分数

`MultiAgentScore` 是更偏产品视角的 0 到 1 分数：

```text
Score =
  0.15 * P(success)
+ 0.10 * SplitAcc
+ 0.12 * ModeAcc
+ 0.12 * SubtaskRecall
+ 0.14 * CapRecall
+ 0.07 * CapPrecision
+ 0.09 * RoleFit
+ 0.09 * AggregationQuality
+ 0.06 * ParallelEfficiency
+ 0.05 * CriticRecall
+ 0.04 * BackgroundCorrect
- 0.08 * clamp(RouteRisk / 4, 0, 1)
- 0.06 * CoordOH
- 0.04 * clamp(DependencyViolations / 3, 0, 1)
```

它和成功概率的区别是：成功概率判断“这次任务能不能成功”，综合分数更像长期产品指标，既看成功，也看能力匹配、聚合质量、风险和协调成本。

## 5. 实验结果

| Variant | Success | SplitAcc | ModeAcc | CapRecall | SubRecall | RouteRisk | DepViol | Speedup | CoordOH | Score | Forbidden | Clean |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | :---: |
| baseline | 0.7059 | 0.7647 | 0.7059 | 0.3549 | 0.8431 | 1.7081 | 0.1765 | 2.0036 | 0.2303 | 0.6168 | 2 | false |
| capability-routed | 0.9412 | 0.9412 | 0.9412 | 0.9154 | 0.9118 | 0.1002 | 0.0000 | 2.1466 | 0.1514 | 0.9008 | 0 | true |
| parallel-routed | 0.9412 | 0.9412 | 0.9412 | 0.9461 | 0.9559 | 0.1002 | 0.0000 | 1.9232 | 0.1494 | 0.9077 | 0 | true |
| dependency-aware | 1.0000 | 1.0000 | 1.0000 | 0.9881 | 1.0000 | 0.0237 | 0.0000 | 1.5930 | 0.1502 | 0.9369 | 0 | true |
| debate-review | 1.0000 | 1.0000 | 1.0000 | 0.9881 | 1.0000 | 0.0237 | 0.0000 | 1.5930 | 0.1814 | 0.9366 | 0 | true |

## 6. Baseline 失败分析

baseline 的成功率是 `0.7059`，刚过默认阈值 `0.70`，但它不是一个干净策略。

主要问题有四个。

第一，能力匹配非常弱。
`CapRecall = 0.3549`，说明很多子任务没有分给对应能力的 agent。

例子：
测试覆盖应该交给 `test-agent`，运行日志应该交给 `ops-agent`，文档缺口应该交给 `docs-agent`。baseline 经常把这些任务交给 generalist 或只交给 repo-agent，导致能力覆盖不足。

第二，pipeline 场景失败明显。
baseline 的整体 `DepViol = 0.1765`，问题主要来自 pipeline。它会把“先读代码，再改 benchmark，再跑测试，最后汇总”误判成 parallel。

例子：
如果 `edit` 依赖 `read`，`test` 依赖 `edit`，并行执行就会破坏任务语义。

第三，存在 forbidden mode。
`Forbidden = 2`，说明它在至少两个任务里触发了不该用的协作模式。

例子：
用户说“只查看 autonomy 状态，不要新增后台 worker”，baseline 因为看见 `autonomy/worker` 关键词，容易误入 `autonomy_queue`。

第四，baseline 的速度收益有误导性。
`Speedup = 2.0036` 看起来不错，但其中一部分来自错误并行。

反例：
把 pipeline 错当 parallel，速度当然可能更快，但结果不可信。对 Agent 来说，错误地快不是优化。

## 7. 对照组解释

### 7.1 capability-routed

`capability-routed` 把成功率从 `0.7059` 提升到 `0.9412`，核心收益来自能力匹配：

```text
CapRecall: 0.3549 -> 0.9154
RouteRisk: 1.7081 -> 0.1002
Forbidden: 2 -> 0
```

这说明第一阶段优化应该先做 agent profile，也就是给每个 agent 建立能力标签。

例子：
`memory-agent` 负责 memory/rag/recall/index，`test-agent` 负责 benchmark/test/validation，`docs-agent` 负责 docs/report/summary。只要分配变准，质量就会立刻上来。

### 7.2 parallel-routed

`parallel-routed` 相比 capability-routed，进一步提高了子任务覆盖：

```text
SubRecall: 0.9118 -> 0.9559
CapRecall: 0.9154 -> 0.9461
Score: 0.9008 -> 0.9077
```

它的价值是更清楚地区分“独立并行”和“需要顺序”的任务。

例子：
“分别检查 backend、frontend、docs”适合 parallel。
“先检查脏文件、跑 baseline、生成报告、commit”不适合 parallel，因为 commit 必须在报告生成之后。

### 7.3 dependency-aware

`dependency-aware` 是本轮最适合作为默认策略的版本：

```text
Success: 1.0000
SplitAcc: 1.0000
ModeAcc: 1.0000
SubRecall: 1.0000
DepViol: 0.0000
CoordOH: 0.1502
Score: 0.9369
```

它解决的是多 Agent 里最关键的问题：不是能不能拆，而是拆完之后是否保持语义顺序。

例子：
`read -> edit -> test -> report` 这种任务必须拓扑排序。只要有边 `read -> edit`，就不能让 edit 先于 read，也不能把它们无脑并行。

### 7.4 debate-review

`debate-review` 的成功率也是 `1.0000`，但协调开销更高：

```text
dependency-aware CoordOH = 0.1502
debate-review    CoordOH = 0.1814
```

综合分数也略低：

```text
dependency-aware Score = 0.9369
debate-review    Score = 0.9366
```

所以 critic/review 不应该默认全开。它更适合高风险任务：

- 架构入口选择。
- 发布/迁移/回滚。
- 权重公式设计。
- 自动化策略调整。
- 需要多方观点收敛的产品决策。

例子：
“多 agent 主入口用 delegate、autonomy 还是 collab API”适合 debate。
“看一下文件在哪里”不适合 debate。

## 8. 推荐的多 Agent 运行时路线

推荐路线：

```text
User Request
  -> Split Decision
  -> Mode Router
  -> Subtask Planner
  -> Capability Router
  -> Dependency Guard
  -> Critic Gate
  -> Executor
```

### 8.1 Split Decision

判断是否需要拆。

输入信号：

- 多领域并列：backend/frontend/docs。
- 多子任务并列：分别、同时、并行。
- 明确后台：异步、worker、队列、不阻塞。
- 明确不要拆：不要子代理、只解释、只查看、用文字。

输出：

```text
ShouldSplit ∈ {true, false}
```

例子：
“解释一下多 agent 的成本”应该是 `false`。
“并行检查测试、日志、文档”应该是 `true`。

### 8.2 Mode Router

判断用哪种协作模式：

```text
Mode ∈ {single, parallel, pipeline, debate, autonomy_queue}
```

规则：

- 有“先、再、最后、迁移、发布、回滚、commit”倾向 pipeline。
- 有“分别、并行、多个独立模块”倾向 parallel。
- 有“辩论、比较方案、权衡、投票、critic”倾向 debate。
- 有“后台、异步、不阻塞、worker、队列”倾向 autonomy_queue。
- 有“不要拆、只查看、解释一下”倾向 single。

### 8.3 Subtask Planner

把任务拆成结构化子任务：

```text
Subtask = {
  id,
  title,
  required_capabilities,
  depends_on,
  risk,
  expected_output
}
```

例子：

```text
status -> baseline -> report -> commit
```

其中：

- `baseline.depends_on = [status]`
- `report.depends_on = [baseline]`
- `commit.depends_on = [report]`

### 8.4 Capability Router

把子任务分给最合适的 agent。

可以用集合相似度作为第一版：

```text
score(agent, subtask) =
  |C_agent ∩ C_subtask| / |C_subtask|
+ λ * quality(agent)
- μ * risk_bias(agent)
```

其中：

- `C_agent` 是 agent 能力集合。
- `C_subtask` 是子任务所需能力集合。
- `quality(agent)` 是历史质量分。
- `risk_bias(agent)` 是该 agent 对风险任务的偏置。

例子：
需要 `benchmark、test、validation` 的任务应优先交给 `test-agent`。

### 8.5 Dependency Guard

对所有子任务依赖做拓扑检查：

```text
G = (V, E)
```

其中：

- `V` 是子任务集合。
- `E` 是依赖边集合，例如 `read -> edit`。

合法执行顺序必须满足：

```text
∀(u, v) ∈ E, position(u) < position(v)
```

如果图中有环：

```text
cycle(G) = true
```

则不能执行，需要先让 planner 修正。

例子：
如果 `test -> edit` 和 `edit -> test` 同时存在，就是循环依赖，系统应停止并重新规划。

### 8.6 Critic Gate

critic/review 不默认开启，而是由风险函数触发：

```text
NeedsCritic =
  1[RiskScore >= τ]
  OR 1[Mode = debate]
  OR 1[Task involves release/migration/security/architecture]
```

其中 `τ` 是阈值。

例子：
“改一行文档错别字”不需要 critic。
“改记忆系统迁移路径并设计回滚”需要 critic。

## 9. 下一步实验

本轮 benchmark 是 deterministic offline benchmark，不调用真实模型。下一阶段应该增加 live replay，也就是用真实 LuckyHarness 会话轨迹做回放。

建议分三步：

第一步：离线策略继续保留。
它适合快速检查路由公式和指标是否退化。

第二步：加入真实轨迹回放。
从历史会话中抽取真实请求，标注 gold mode，然后让 planner 输出 mode/subtasks/assignments。

第三步：小流量进入 runtime。
先只做 planner dry-run，不真正开多个 agent，把预测结果写入 trace。等误判率稳定后，再逐步启用真实执行。

验收指标：

```text
ModeAcc >= 0.90
ForbiddenModeCount = 0
DependencyViolationCount = 0
CoordOH <= 0.20
RouteRisk <= 0.20
```

## 10. 最终结论

本轮实验的核心结论：

1. baseline 已经证明多 Agent 有潜力，但它不能直接进入默认运行时，因为存在 forbidden mode、能力错配和 pipeline 误并行。
2. 单纯能力路由就能带来大幅提升，说明 agent profile 是第一优先级。
3. `dependency-aware` 是最值得落地的默认策略，因为它在质量、风险、协调开销之间最平衡。
4. `debate-review` 不应默认开启；它应该作为高风险任务的增强模式。
5. LuckyHarness 多 Agent 的优化重点不是“更多 agent”，而是“更准确的 planner”：该不该拆、怎么拆、交给谁、按什么顺序执行、是否需要复核。

一句话收口：
LuckyHarness 现在应该从“能开多 Agent”进化到“会克制地开多 Agent”。真正优秀的 Agent 系统，不是每次都把场面铺大，而是在任务需要时精准分工，在不需要时保持简单。
