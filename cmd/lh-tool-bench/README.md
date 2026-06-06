# lh-tool-bench

`lh-tool-bench` measures LuckyHarness tool strategy quality without calling an
LLM or executing real tools. It uses golden synthetic tasks plus deterministic
strategy simulation so tool-routing changes can be compared cheaply and
reproducibly.

The benchmark answers:

- Should this task use tools?
- Were the required tools and operations selected?
- Were redundant or forbidden operations called?
- How much route risk, result noise, and token cost did the strategy create?
- Did the strategy pass the task quality proxy?

## Run

```bash
go run ./cmd/lh-tool-bench \
  -variant baseline \
  -scenario all \
  -rounds 1 \
  -out docs/reports/tool-bench-baseline-20260607.jsonl
```

Compare existing runs:

```bash
go run ./cmd/lh-tool-bench \
  -compare docs/reports/tool-bench-baseline-20260607.jsonl,docs/reports/tool-bench-intent-gated-20260607.jsonl,docs/reports/tool-bench-risk-aware-20260607.jsonl,docs/reports/tool-bench-packed-results-20260607.jsonl
```

Supported variants:

```text
baseline
static-slim
intent-gated
risk-aware
packed-results
```

Supported scenarios:

```text
no_tool
read_only
single_tool
multi_tool
risk
trap
all
```

## Metrics

Core fields:

- `need_tool_prob`: estimated `P(NeedTool | x)`.
- `tool_recall`: share of required tools covered.
- `tool_precision`: share of called tools that were required.
- `operation_recall`: share of required operations covered.
- `operation_precision`: share of called operations that were required.
- `redundant_rate`: redundant calls divided by total operation calls.
- `route_risk`: realized risk from misused operations.
- `expected_route_risk`: probabilistic expected route risk from visible tools.
- `tool_alignment`: cosine alignment between task intent terms and called tool-operation tags.
- `info_efficiency`: information gain divided by token/risk cost.
- `tool_result_noise`: irrelevant result tokens divided by result tokens.
- `tool_tune_score`: weighted aggregate strategy score.

## Baseline Snapshot

Baseline run on 2026-06-07:

```text
records: 24
success_rate: 0.5833
tool_need_acc: 0.8750
operation_recall: 0.9375
operation_precision: 0.6042
redundant_rate: 0.2639
route_risk: 2.3958
expected_route_risk: 1.5243
tool_result_noise: 0.2943
tool_alignment: 0.5196
info_efficiency: 0.5568
tool_tune_score: 0.4763
forbidden_call_count: 6
clean: false
```

Weakest scenarios:

```text
trap: success_rate=0.00, route_risk=7.25, result_noise=0.47
risk: success_rate=0.50, route_risk=4.50, redundant_rate=0.21
no_tool: tool_need_acc=0.25, redundant_rate=0.375
```

This is expected for the baseline: it intentionally exposes all tools and uses a
loose heuristic policy, so the benchmark has a measurable signal for future
intent-gated, risk-aware, and packed-result variants.

## Variant Comparison

Calibrated run on 2026-06-07:

| Variant | Success | NeedAcc | OpRecall | OpPrecision | Redundant | RouteRisk | Noise | Score | Forbidden | Clean |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | :---: |
| baseline | 0.5833 | 0.8750 | 0.9375 | 0.6042 | 0.2639 | 2.3958 | 0.2943 | 0.4763 | 6 | false |
| intent-gated | 1.0000 | 1.0000 | 0.9375 | 1.0000 | 0.0000 | 0.0000 | 0.2259 | 0.6830 | 0 | true |
| risk-aware | 1.0000 | 1.0000 | 0.9375 | 1.0000 | 0.0000 | 0.0000 | 0.2259 | 0.6830 | 0 | true |
| packed-results | 1.0000 | 1.0000 | 0.9375 | 1.0000 | 0.0000 | 0.0000 | 0.0491 | 0.6940 | 0 | true |

The main signal is:

```text
intent-gated removes unnecessary and forbidden calls;
risk-aware preserves that clean route;
packed-results keeps the clean route and sharply reduces result noise.
```
