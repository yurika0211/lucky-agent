# lh-multiagent-bench

`lh-multiagent-bench` is an offline benchmark for LuckyAgent multi-agent
planning. It measures whether a task should stay in one agent, be split into
parallel subtasks, run as a dependency-aware pipeline, use debate/review, or be
queued into the autonomy worker system.

The benchmark is intentionally deterministic. It does not call a live model.
Instead, it simulates strategy variants over a fixed 60-case synthetic suite so
that routing and coordination changes can be compared before touching the
runtime path.

## Strategy Variants

- `baseline`: naive single-or-parallel routing with keyword-triggered
  over-splitting.
- `capability-routed`: routes subtasks to agents by capability overlap.
- `parallel-routed`: distinguishes parallel/pipeline/debate/background intent.
- `dependency-aware`: preserves dependency order and uses the gold mode in this
  synthetic suite.
- `debate-review`: dependency-aware plus stronger critic/review aggregation.
- `math-mdp-v1`: deterministic offline Contextual MDP planner that scores
  candidate modes by expected utility.
- `math-ssp-v1`: stochastic-shortest-path style planner that chooses the lowest
  path weight using `-log(p)` plus cost and risk terms.
- `math-lyapunov-v1`: MDP planner with a Lyapunov decrease guard to reject
  non-convergent candidates.
- `math-verifier-v1`: MDP planner that treats debugger/verifier availability as
  an explicit constraint.
- `math-full-v1`: combined MDP + stochastic shortest path diagnostics +
  Lyapunov guard + verifier gate.

## Scenarios

- `single`: tasks that should not be split.
- `parallel`: independent subtasks with a merge step.
- `pipeline`: ordered subtasks where later work depends on earlier output.
- `debate`: tasks that need competing proposals and a critic/vote.
- `autonomy`: background worker and queue scenarios, plus an autonomy trap.
- `heavy`: super-heavy end-to-end orchestration cases, including Hermes Agent
  reproduction, Hermes parity audit, release recovery, architecture debate, and
  async replay.

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

Math planner variants also emit a `diagnostics` object per record:

- `trace_id`: stable deterministic identifier for the planner trace.
- `context_features`: extracted task features such as dependency depth, risk,
  test observability, and whether the task is super-heavy.
- `candidate_scores`: expected utility, path weight, estimated success, risk,
  Lyapunov endpoint, and rejection reasons for each candidate mode.
- `trace`: deterministic orchestration trace edges with edge probabilities,
  `-log(p)` weights, estimated cost/risk, and Lyapunov before/after values.
- `expected_utility`, `path_weight`, `estimated_success`,
  `edge_nll`, `calibration_ece`, `lyapunov_decrease`,
  `lyapunov_decrease_rate`, `replan_recommended`, `verifier_expected_catch`,
  `out_of_distribution`, and `path_regret` for summary comparison.

The aggregate summary includes the corresponding report-level metrics:

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

## Usage

```bash
go run ./cmd/lh-multiagent-bench \
  -variant baseline \
  -scenario all \
  -rounds 1 \
  -out docs/reports/multiagent-bench-baseline.jsonl
```

Run only the super-heavy orchestration cases:

```bash
go run ./cmd/lh-multiagent-bench \
  -variant baseline \
  -scenario heavy \
  -rounds 1 \
  -out docs/reports/multiagent-bench-heavy-baseline.jsonl
```

Run the full mathematical planner on heavy cases:

```bash
go run ./cmd/lh-multiagent-bench \
  -variant math-full-v1 \
  -scenario heavy \
  -rounds 1 \
  -out docs/reports/multiagent-bench-heavy-math-full.jsonl
```

Compare result files:

```bash
go run ./cmd/lh-multiagent-bench \
  -compare docs/reports/multiagent-bench-baseline.jsonl,docs/reports/multiagent-bench-dependency-aware.jsonl
```

Run historical session replay cases:

```bash
go run ./cmd/lh-multiagent-bench \
  -replay ~/.luckyagent/sessions \
  -replay-label-out docs/reports/multiagent-replay-labels.jsonl
```

Review and edit the generated labels, then run the replay:

```bash
go run ./cmd/lh-multiagent-bench \
  -variant math-full-v1 \
  -replay docs/reports/multiagent-replay-labels.jsonl \
  -replay-only \
  -scenario replay \
  -rounds 1 \
  -out docs/reports/multiagent-replay-math-full.jsonl
```

Replay inputs can be JSON, JSONL, LuckyAgent session Markdown files, or a
directory containing those files. A JSON replay case may provide `prompt` or
`messages`, plus optional labels such as `gold_mode`, `should_split`,
`needs_verifier`, `needs_critic`, `allows_background`, `forbidden_modes`, and
`required_capabilities`. When labels are missing, the loader performs a
deterministic semi-automatic label pass from prompt cues.

`-replay-label-out` writes one `replay_label` JSON object per historical case
with inferred labels, confidence, and evidence. The label file is intentionally
also valid replay input, so it can be hand-edited and passed back through
`-replay`.

## Experiment Plan

1. Run `baseline` to quantify current naive multi-agent failure modes.
2. Run `capability-routed` to isolate capability matching gains.
3. Run `parallel-routed` to test mode selection improvements.
4. Run `dependency-aware` to measure dependency safety.
5. Run `debate-review` to test critic/review gains on high-risk decisions.
6. Run `heavy` scenarios separately to make sure the planner handles
   super-heavy work such as reproducing a Hermes-like agent, where the correct
   plan requires ordered research, implementation, debugger/verifier gates,
   security review, GUI/CLI acceptance, and scoped reporting.
7. Run `math-full-v1` against `heavy` and `all` to validate the first offline
   version of the mathematical orchestration model before touching runtime.
8. Check the math columns in `-compare` output, especially `ECE`, `LyapRate`,
   `Replan`, and `Regret`, before treating a planner as calibrated.
9. Run `-replay` with historical LuckyAgent sessions to measure replay
   generalization. The first offline proxy metrics are `VerifierNeedAccuracy`
   and `OODFalseNegativeRate`; replace the semi-automatic labels with human
   labels before treating them as final research numbers.

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
