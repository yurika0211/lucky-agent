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
  -out docs/reports/tool-bench-expanded-v3-baseline-20260607.jsonl
```

Compare existing runs:

```bash
go run ./cmd/lh-tool-bench \
  -compare docs/reports/tool-bench-expanded-v3-baseline-20260607.jsonl,docs/reports/tool-bench-expanded-v3-risk-aware-20260607.jsonl,docs/reports/tool-bench-expanded-v3-packed-results-20260607.jsonl
```

Supported variants:

```text
baseline
static-slim
intent-gated
guarded-intent
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

Expanded baseline run on 2026-06-07. The dataset contains 60 tasks spanning
concept-only, read-only inspection, single-tool, multi-tool, risk-sensitive, and
prompt-injection scenarios. It covers file/repo, web, memory/RAG, media,
database, cron, autonomy, heartbeat, and delegate tool domains.

```text
records: 60
success_rate: 0.6500
tool_need_acc: 0.8833
operation_recall: 0.9417
operation_precision: 0.5478
redundant_rate: 0.2603
route_risk: 2.5083
expected_route_risk: 3.1097
tool_result_noise: 0.2866
tool_alignment: 0.5645
info_efficiency: 0.3684
tool_tune_score: 0.4331
forbidden_call_count: 19
clean: false
```

Weakest scenarios:

```text
trap: success_rate=0.20, route_risk=5.35, result_noise=0.39
risk: success_rate=0.30, route_risk=5.35, redundant_rate=0.43
no_tool: tool_need_acc=0.12, redundant_rate=0.19
```

This is expected for the baseline: it intentionally exposes all tools and uses a
loose heuristic policy, so the benchmark has a measurable signal for future
intent-gated, risk-aware, and packed-result variants.

## Variant Comparison

Expanded calibrated run on 2026-06-07:

| Variant | Success | NeedAcc | OpRecall | OpPrecision | Redundant | RouteRisk | Noise | Score | Forbidden | Clean |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | :---: |
| baseline | 0.6500 | 0.8833 | 0.9417 | 0.5478 | 0.2603 | 2.5083 | 0.2866 | 0.4331 | 19 | false |
| guarded-intent | 1.0000 | 1.0000 | 0.9917 | 0.9625 | 0.0042 | 0.0583 | 0.2367 | 0.6938 | 0 | false |
| risk-aware | 1.0000 | 1.0000 | 0.9917 | 0.9625 | 0.0042 | 0.0583 | 0.2367 | 0.6938 | 0 | false |
| packed-results | 1.0000 | 1.0000 | 0.9917 | 0.9625 | 0.0042 | 0.0583 | 0.0517 | 0.7048 | 0 | false |

The main signal is:

```text
guarded-intent/risk-aware remove forbidden calls and most redundant operations across the wider tool surface;
packed-results keeps the same route quality and sharply reduces result noise.
```
