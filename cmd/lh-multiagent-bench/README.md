# lh-multiagent-bench

`lh-multiagent-bench` is an offline benchmark for LuckyHarness multi-agent
planning. It measures whether a task should stay in one agent, be split into
parallel subtasks, run as a dependency-aware pipeline, use debate/review, or be
queued into the autonomy worker system.

The benchmark is intentionally deterministic. It does not call a live model.
Instead, it simulates strategy variants over a fixed task set so that routing
and coordination changes can be compared before touching the runtime path.

## Strategy Variants

- `baseline`: naive single-or-parallel routing with keyword-triggered
  over-splitting.
- `capability-routed`: routes subtasks to agents by capability overlap.
- `parallel-routed`: distinguishes parallel/pipeline/debate/background intent.
- `dependency-aware`: preserves dependency order and uses the gold mode in this
  synthetic suite.
- `debate-review`: dependency-aware plus stronger critic/review aggregation.

## Scenarios

- `single`: tasks that should not be split.
- `parallel`: independent subtasks with a merge step.
- `pipeline`: ordered subtasks where later work depends on earlier output.
- `debate`: tasks that need competing proposals and a critic/vote.
- `autonomy`: background worker and queue scenarios, plus an autonomy trap.

## Core Metrics

Let `M` be the predicted collaboration mode and `M*` the gold mode.

```text
ModeAcc = 1[M = M*]
SplitAcc = 1[(M != single) = (M* != single)]
```

Subtask recall and precision measure whether the planner executed the required
subtasks:

```text
SubtaskRecall    = |S_pred ∩ S_gold| / |S_gold|
SubtaskPrecision = |S_pred ∩ S_gold| / |S_pred|
```

Capability recall and precision measure whether assigned agents cover the
capabilities needed by the task:

```text
CapRecall    = |C_agents ∩ C_required| / |C_required|
CapPrecision = |C_agents ∩ C_required| / |C_agents|
```

Route risk penalizes forbidden collaboration modes, wrong modes, poor
capability assignment, dependency violations, and missing critic review:

```text
RouteRisk =
  2.5 * 1[forbidden_mode]
+ 1.3 * 1[M != M*]
+ 0.8 * 1[M* = single and M != single]
+ Σ_subtask risk_i * (1 - assignment_score_i) * (1 + agent_risk_bias_i)
+ 1.15 * DependencyViolations
+ debate_or_background_penalties
```

Coordination overhead normalizes planner/aggregator cost by total cost:

```text
CoordOH =
  0.55 * CoordinatorTokens / TotalTokens
+ 0.45 * CoordinatorLatency / CriticalPathLatency
```

Parallel speedup is computed against a simulated single-agent latency:

```text
Speedup = SingleAgentLatency / CriticalPathLatency
ParallelEfficiency = Speedup / NumExecutedSubtasks
```

The benchmark reports a logistic success probability:

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

`MultiAgentScore` is a bounded 0-1 score combining the same factors with a
slightly more product-facing weighting.

## Usage

```bash
go run ./cmd/lh-multiagent-bench \
  -variant baseline \
  -scenario all \
  -rounds 1 \
  -out docs/reports/multiagent-bench-baseline.jsonl
```

Compare result files:

```bash
go run ./cmd/lh-multiagent-bench \
  -compare docs/reports/multiagent-bench-baseline.jsonl,docs/reports/multiagent-bench-dependency-aware.jsonl
```

## Experiment Plan

1. Run `baseline` to quantify current naive multi-agent failure modes.
2. Run `capability-routed` to isolate capability matching gains.
3. Run `parallel-routed` to test mode selection improvements.
4. Run `dependency-aware` to measure dependency safety.
5. Run `debate-review` to test critic/review gains on high-risk decisions.

The expected improvement path is:

```text
baseline
  -> capability-routed
  -> parallel-routed
  -> dependency-aware
  -> debate-review
```

The implementation target after this benchmark is a planner that decides:

- whether to split;
- which collaboration mode to use;
- which worker/agent profile should receive each subtask;
- whether a critic pass is required;
- whether the task belongs in the autonomy queue instead of the foreground loop.
