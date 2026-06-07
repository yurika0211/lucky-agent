# LuckyHarness 多 Agent Benchmark 实验报告

日期：2026-06-07

## 1. 实验目的

本实验研究 LuckyHarness 的多 agent 系统是否应该自动拆分任务，以及如何选择：

- 单 agent：不拆分，直接回答或执行。
- parallel：多个独立子任务并行执行，再聚合。
- pipeline：有依赖顺序的任务按阶段串行执行。
- debate：多个 agent 提出不同方案，由 critic 或投票收敛。
- autonomy_queue：放入后台 worker/队列异步执行。

实验目标不是证明“多 agent 一定更好”，而是量化：

- 什么时候不该拆；
- 拆分是否覆盖关键子任务；
- 子任务是否分给了合适能力的 agent；
- 并行是否真的带来收益；
- 依赖任务是否被错误并行化；
- critic/review 是否值得引入；
- 后台队列是否被误触发。

## 2. 实验命令

baseline：

```bash
go run ./cmd/lh-multiagent-bench \
  -variant baseline \
  -scenario all \
  -rounds 1 \
  -out docs/reports/multiagent-bench-baseline-20260607.jsonl
```

对照策略：

```bash
go run ./cmd/lh-multiagent-bench -variant capability-routed -scenario all -rounds 1 -out docs/reports/multiagent-bench-capability-routed-20260607.jsonl
go run ./cmd/lh-multiagent-bench -variant dependency-aware -scenario all -rounds 1 -out docs/reports/multiagent-bench-dependency-aware-20260607.jsonl
go run ./cmd/lh-multiagent-bench -variant debate-review -scenario all -rounds 1 -out docs/reports/multiagent-bench-debate-review-20260607.jsonl
```

验证：

```bash
go test ./cmd/lh-multiagent-bench ./cmd/lh-tool-bench ./cmd/lh-context-packer-bench ./cmd/lh-memory-bench
go test ./cmd/lh-multiagent-bench ./internal/collab ./internal/autonomy ./internal/tool
```

## 3. 指标公式

模式准确率：

```text
ModeAcc = 1[M = M*]
```

其中 `M` 是预测协作模式，`M*` 是标准答案模式。

拆分准确率：

```text
SplitAcc = 1[(M != single) = (M* != single)]
```

子任务召回率：

```text
SubtaskRecall = |S_pred ∩ S_gold| / |S_gold|
```

子任务精确率：

```text
SubtaskPrecision = |S_pred ∩ S_gold| / |S_pred|
```

能力召回率：

```text
CapRecall = |C_agents ∩ C_required| / |C_required|
```

能力精确率：

```text
CapPrecision = |C_agents ∩ C_required| / |C_agents|
```

路由风险：

```text
RouteRisk =
  2.5 * 1[forbidden_mode]
+ 1.3 * 1[M != M*]
+ 0.8 * 1[M* = single and M != single]
+ Σ_i risk_i * (1 - assignment_score_i) * (1 + agent_risk_bias_i)
+ 1.15 * DependencyViolations
+ debate/background penalties
```

协调开销：

```text
CoordOH =
  0.55 * CoordinatorTokens / TotalTokens
+ 0.45 * CoordinatorLatency / CriticalPathLatency
```

并行收益：

```text
Speedup = SingleAgentLatency / CriticalPathLatency
ParallelEfficiency = Speedup / NumExecutedSubtasks
```

综合成功概率：

```text
P(success) = sigmoid(
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
)
```

## 4. 实验结果

| Variant | Success | SplitAcc | ModeAcc | CapRecall | SubRecall | RouteRisk | DepViol | Speedup | CoordOH | Score | Forbidden | Clean |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | :---: |
| baseline | 0.7059 | 0.7647 | 0.7059 | 0.3549 | 0.8431 | 1.7081 | 0.1765 | 2.0036 | 0.2303 | 0.6168 | 2 | false |
| capability-routed | 0.9412 | 0.9412 | 0.9412 | 0.9154 | 0.9118 | 0.1002 | 0.0000 | 2.1466 | 0.1514 | 0.9008 | 0 | true |
| dependency-aware | 1.0000 | 1.0000 | 1.0000 | 0.9881 | 1.0000 | 0.0237 | 0.0000 | 1.5930 | 0.1502 | 0.9369 | 0 | true |
| debate-review | 1.0000 | 1.0000 | 1.0000 | 0.9881 | 1.0000 | 0.0237 | 0.0000 | 1.5930 | 0.1814 | 0.9366 | 0 | true |

## 5. Baseline 结论

baseline 的总成功率是 `0.7059`，刚过默认成功阈值 `0.70`，但不干净：

- `Forbidden = 2`，说明存在不该启用的协作模式。
- `CapRecall = 0.3549`，说明子任务经常没有分给真正具备能力的 agent。
- `Pipeline` 场景失败最明显：`ModeAcc = 0`，平均依赖违例 `1.0`。
- baseline 的 `Speedup = 2.0036` 看起来不错，但这是用错误拆分换来的，不能直接作为质量收益。

最典型错误：

- 把有依赖顺序的任务误判成 parallel。
- 看到 `autonomy/worker` 关键词就误入后台队列。
- 过度使用 generalist，导致能力覆盖率低。
- 并行执行后聚合方式偏弱，容易漏掉整合任务。

## 6. 优化路线

第一阶段：能力路由。

把 subtask 的 `capabilities` 和 agent profile 做匹配，先解决“分给谁”的问题。实验中成功率从 `0.7059` 提升到 `0.9412`，能力召回从 `0.3549` 提升到 `0.9154`。

第二阶段：依赖识别。

识别 `先/再/最后/发布收口/迁移/回滚` 等依赖信号，把任务从 parallel 改成 pipeline。实验中 `dependency-aware` 将依赖违例降到 `0`，成功率到 `1.0000`。

第三阶段：critic/review 按需启用。

`debate-review` 的成功率与 `dependency-aware` 相同，但协调开销从 `0.1502` 升到 `0.1814`。因此 critic 不应默认开启，只应在架构决策、高风险发布、迁移、策略权重等任务中启用。

## 7. 工程建议

下一步真正优化多 agent runtime 时，建议新增一个轻量 planner：

```text
User Request
  -> Split Decision
  -> Mode Router
  -> Subtask Planner
  -> Capability Router
  -> Dependency Guard
  -> Critic Gate
  -> Executor: single / collab parallel / pipeline / debate / autonomy queue
```

最优先落地：

1. 给 agent profile 建立能力标签。
2. 在委派前生成结构化 subtasks。
3. 对 subtask dependency 做拓扑检查，禁止误并行。
4. 对 `autonomy_queue` 加强显式意图判断，避免“只查看状态”时创建后台任务。
5. critic gate 只在 `NeedsCritic=true` 或风险分数高时开启。

当前实验结论：LuckyHarness 的多 agent 系统值得继续做，但优化重点不是“增加更多 agent”，而是“更准地决定何时拆、如何拆、交给谁、何时收敛”。
