# LuckyHarness Memory Activation Benchmark Experiment

## 1. Experiment Goal

This experiment measures whether the evolved memory activation system improves
LuckyHarness memory recall without creating unsafe stale-memory hits or
unacceptable latency.

The experiment compares five variants:

- `baseline`: the previous or control implementation.
- `activation-v1`: the new explainable energy activation implementation.
- `activation-v2-a2`: the bounded-route activation implementation.
- `activation-v3-a3`: top-N graph-seed selection during activation scan.
- `activation-v4-a4`: bounded top-K final result selection for limited queries.

The main question is:

> Does graph-aware activation improve useful recall while preserving temporal
> safety and keeping query latency practical?

## 2. Hypotheses

### H1: Graph activation improves recall

For queries that directly mention only one node in a memory chain, graph
activation should recover related memories that lexical search alone misses.

Example chain:

```text
Outdoor Plan -> Daughter -> Pollen Allergy
```

Expected signal:

```text
graph_recall_lift > 0
avg_graph_on_recall_at_k > avg_graph_off_recall_at_k
```

### H2: Temporal safety is preserved

Expired, future-dated, superseded, conflict, or otherwise inactive memories
must not be returned as active evidence.

Expected signal:

```text
avg_stale_hit_rate == 0
forbid_hits == 0
clean == true
```

### H3: Route-level behavior remains usable

`Store.Route()` should still produce correct risk flags and required tools for
memory-gated user requests.

Expected signal:

```text
avg_risk_recall >= 0.65
avg_tool_recall >= 0.65
```

### H4: Latency remains practical

At `10k` synthetic memories, activation should remain in low-millisecond range
for benchmark queries on the current local machine.

Expected signal:

```text
p95_ms does not regress by more than 2x against baseline
```

## 3. Independent Variables

### Variant

The code branch or implementation being measured:

```text
baseline
activation-v1
activation-v2-a2
activation-v3-a3
activation-v4-a4
```

### Scenario

`lh-memory-bench` supports these scenarios:

```text
lexical
graph
temporal
scale
route
all
```

### Dataset Size

Recommended sizes:

```text
1k
10k
50k
```

The first official comparison should use `10k`; `50k` is a stress run.

### Graph Mode

The `graph` scenario automatically runs both:

```text
graph_off
graph_on
```

This isolates graph recall lift from the rest of the activation system.

## 4. Dependent Metrics

### Retrieval quality

- `recall_at_k`: share of expected memories returned.
- `precision_at_k`: share of returned memories that are expected.
- `mrr`: reciprocal rank of the first expected memory.
- `ndcg_at_k`: ranking quality with higher weight for earlier expected hits.
- `noise_at_k`: share of returned memories not in the expected set.

### Graph benefit

- `graph_recall_lift`
- `avg_graph_on_recall_at_k`
- `avg_graph_off_recall_at_k`

### Temporal safety

- `forbid_hit_count`
- `forbid_hits`
- `stale_hit_rate`
- `avg_stale_hit_rate`

### Runtime cost

- `duration_ns`
- `avg_duration_ns`
- `p50_duration_ns`
- `p95_duration_ns`

### Route behavior

- `risk_flags`
- `required_tools`
- `risk_recall`
- `tool_recall`

## 4.5 Formula Derivations

This section defines how activation scores and benchmark metrics are computed.

### Activation Score

For query `q` and memory entry `m`, direct activation is:

```text
DirectScore(q, m) = Match(q, m) * Weight(m) * TierMultiplier(m)
```

The match term is decomposed as:

```text
Match(q, m) =
  Lexical(q, m)
+ Category(q, m)
+ Tags(q, m)
+ Aliases(q, m)
+ Links(q, m)
```

The memory weight is:

```text
Weight(m) = Importance(m) * Recency(m) * AccessBoost(m)
```

Recency uses true half-life decay:

```text
Recency(m) = 0.5 ^ (AgeHours(m) / HalfLifeHours(m))
```

where:

```text
AgeHours(m) = now - CreatedAt(m)
```

The half-life depends on memory tier:

```text
Short-term  = 1 hour
Medium-term = 7 days
Long-term   = 365 days
```

Access boost uses logarithmic growth:

```text
AccessBoost(m) = 1 + min(log(1 + AccessCount(m)) * 0.12, 0.75)
```

Tier multiplier is:

```text
TierMultiplier(short)  = 0.8
TierMultiplier(medium) = 1.0
TierMultiplier(long)   = 1.2
```

Therefore:

```text
DirectScore(q, m)
= Match(q, m)
* Importance(m)
* 0.5 ^ (AgeHours(m) / HalfLifeHours(m))
* (1 + min(log(1 + AccessCount(m)) * 0.12, 0.75))
* TierMultiplier(m)
```

Graph propagation adds bounded activation from a directly activated source
memory `s` to a related target memory `t`:

```text
GraphBoost(s -> t) = Score(s) * EdgeCoefficient(s, t) * max(Weight(t), 0.05)
```

The per-entry graph boost is capped:

```text
TotalGraphBoost(t) <= MaxGraphBoost
```

The final score is:

```text
FinalScore(q, m) = DirectScore(q, m) + TotalGraphBoost(q, m)
```

### Recall@K

Let:

```text
G(q) = golden expected memory IDs for query q
R_k(q) = top-k retrieved memory IDs for query q
```

Then:

```text
Recall@K(q) = |R_k(q) intersection G(q)| / |G(q)|
```

Recall answers:

> Of all memories that should have been recalled, how many did the system find?

### Precision@K

```text
Precision@K(q) = |R_k(q) intersection G(q)| / |R_k(q)|
```

Precision answers:

> Of all returned memories, how many are truly expected memories?

### MRR

Let `rank_1(q)` be the rank position of the first relevant result. Rank starts
at `1`.

```text
MRR(q) = 1 / rank_1(q)
```

If no relevant result is returned:

```text
MRR(q) = 0
```

MRR rewards systems that put at least one correct memory near the top.

### DCG and NDCG@K

In this benchmark, relevance is binary:

```text
rel_i = 1, if result i is in G(q)
rel_i = 0, otherwise
```

Discounted cumulative gain is:

```text
DCG@K(q) = sum_{i=1..K} rel_i / log2(i + 1)
```

The ideal ranking places all relevant results first:

```text
IDCG@K(q) = sum_{i=1..min(K, |G(q)|)} 1 / log2(i + 1)
```

Normalized DCG is:

```text
NDCG@K(q) = DCG@K(q) / IDCG@K(q)
```

NDCG answers:

> Did the system rank the correct memories early, not merely somewhere in top-k?

### Noise@K

Noise is the share of retrieved memories that are not in the expected set:

```text
Noise@K(q) = (|R_k(q)| - |R_k(q) intersection G(q)|) / |R_k(q)|
```

This is the complement of precision in the binary-label synthetic setting:

```text
Noise@K(q) = 1 - Precision@K(q)
```

### Stale Hit Rate

Let:

```text
F(q) = forbidden memory IDs for query q
```

Forbidden memories include expired, future-dated, superseded, conflict, or
otherwise inactive memories.

```text
StaleHitRate(q) = |R_k(q) intersection F(q)| / |F(q)|
```

The strict target is:

```text
StaleHitRate(q) = 0
```

### Graph Recall Lift

The `graph` scenario runs the same query twice:

```text
Recall_graph_on(q)
Recall_graph_off(q)
```

The lift is:

```text
GraphRecallLift(q) = Recall_graph_on(q) - Recall_graph_off(q)
```

Across all graph queries:

```text
AvgGraphRecallLift
= average(Recall_graph_on) - average(Recall_graph_off)
```

A positive value means graph activation recovered memories that lexical
activation alone missed.

### Route Risk and Tool Recall

For route-level tests:

```text
ExpectedRisk(q) = golden risk flags
ActualRisk(q)   = route.RiskFlags
```

```text
RiskRecall(q) =
  |ActualRisk(q) intersection ExpectedRisk(q)| / |ExpectedRisk(q)|
```

Similarly:

```text
ExpectedTools(q) = golden required tools
ActualTools(q)   = route.RequiredTools
```

```text
ToolRecall(q) =
  |ActualTools(q) intersection ExpectedTools(q)| / |ExpectedTools(q)|
```

### Latency Summary

Each round records:

```text
DurationNS = finished_at - started_at
```

Per scenario:

```text
AvgDuration = average(DurationNS)
P50Duration = median(DurationNS)
P95Duration = 95th percentile(DurationNS)
```

## 5. Dataset

### Synthetic Dataset

The synthetic dataset is generated by:

```bash
/usr/local/go/bin/go run ./cmd/lh-memory-bench --dataset synthetic
```

It writes an isolated LuckyHarness memory vault in Markdown format and loads it
through `memory.NewStore`, so the benchmark measures the real memory parser,
graph index, activation logic, and route path.

Seeded core memories include:

```text
mem_bench_telegram
mem_bench_outdoor_plan
mem_bench_daughter
mem_bench_pollen_allergy
mem_bench_weather
mem_bench_air_quality
mem_bench_scale_anchor
mem_bench_old_allergy
mem_bench_expired_location
mem_bench_future_location
```

Noise memories are generated up to `--size` using fixed seed text, categories,
tags, aliases, tiers, and access counts.

### Real Dataset

Real memory can be measured with:

```bash
/usr/local/go/bin/go run ./cmd/lh-memory-bench \
  --dataset real \
  --memory-dir ~/.luckyharness/memory \
  --scenario scale \
  --query "女儿户外活动"
```

Real memory usually has no golden labels, so real-dataset runs are mainly for
latency and qualitative result inspection, not strict quality gates.

## 6. Official A/B Procedure

### Step 1: Run baseline

Checkout the baseline branch or commit, then run:

```bash
/usr/local/go/bin/go run ./cmd/lh-memory-bench \
  --variant baseline \
  --scenario all \
  --dataset synthetic \
  --size 10000 \
  --rounds 20 \
  --limit 6 \
  --out /tmp/lh-memory-bench/baseline.jsonl
```

### Step 2: Run activation-v1

Checkout the activation branch or commit, then run:

```bash
/usr/local/go/bin/go run ./cmd/lh-memory-bench \
  --variant activation-v1 \
  --scenario all \
  --dataset synthetic \
  --size 10000 \
  --rounds 20 \
  --limit 6 \
  --out /tmp/lh-memory-bench/activation-v1.jsonl
```

### Step 3: Run activation-v2-a2

Checkout the A2 activation branch or commit, then run:

```bash
/usr/local/go/bin/go run ./cmd/lh-memory-bench \
  --variant activation-v2-a2 \
  --scenario all \
  --dataset synthetic \
  --size 10000 \
  --rounds 20 \
  --limit 6 \
  --out /tmp/lh-memory-bench/activation-v2-a2.jsonl
```

### Step 4: Run activation-v3-a3

Checkout the A3 activation branch or commit, then run:

```bash
/usr/local/go/bin/go run ./cmd/lh-memory-bench \
  --variant activation-v3-a3 \
  --scenario all \
  --dataset synthetic \
  --size 10000 \
  --rounds 20 \
  --limit 6 \
  --out /tmp/lh-memory-bench/activation-v3-a3.jsonl
```

### Step 5: Run activation-v4-a4

Checkout the A4 activation branch or commit, then run:

```bash
/usr/local/go/bin/go run ./cmd/lh-memory-bench \
  --variant activation-v4-a4 \
  --scenario all \
  --dataset synthetic \
  --size 10000 \
  --rounds 20 \
  --limit 6 \
  --out /tmp/lh-memory-bench/activation-v4-a4.jsonl
```

### Step 6: Summarize

```bash
jq -s '
  map(select(.type=="summary")) |
  map({
    variant,
    scenario,
    records,
    avg_duration_ms: (.avg_duration_ns / 1000000),
    p50_ms: (.p50_duration_ns / 1000000),
    p95_ms: (.p95_duration_ns / 1000000),
    avg_recall_at_k,
    avg_precision_at_k,
    avg_mrr,
    avg_ndcg_at_k,
    avg_noise_at_k,
    avg_stale_hit_rate,
    graph_recall_lift,
    avg_risk_recall,
    avg_tool_recall,
    clean,
    quality_pass
  })
' /tmp/lh-memory-bench/baseline.jsonl \
  /tmp/lh-memory-bench/activation-v1.jsonl \
  /tmp/lh-memory-bench/activation-v2-a2.jsonl \
  /tmp/lh-memory-bench/activation-v3-a3.jsonl \
  /tmp/lh-memory-bench/activation-v4-a4.jsonl
```

### Step 7: Compute acceptance deltas

Use this interpretation table:

| Metric | Expected Direction | Acceptance |
| --- | --- | --- |
| `graph_recall_lift` | Up | `activation-v1 > baseline` |
| `avg_stale_hit_rate` | Same or down | `0` |
| `forbid_hits` | Same or down | `0` |
| `avg_noise_at_k` | Same or down | <= `0.60` |
| `avg_risk_recall` | Same or up | >= `0.65` |
| `avg_tool_recall` | Same or up | >= `0.65` |
| `p95_ms` | Same or moderate up | <= `2x baseline` |
| `quality_pass` | Must pass | `true` |
| `clean` | Must pass | `true` |

## 7. A2/A3/A4 Result Snapshot

This snapshot was produced on 2026-06-06 with:

```bash
/usr/local/go/bin/go run ./cmd/lh-memory-bench \
  --variant <variant> \
  --scenario all \
  --dataset synthetic \
  --size 10000 \
  --rounds 20 \
  --limit 6 \
  --out /tmp/lh-memory-bench/<variant>.jsonl
```

| Variant | Scenario | Avg ms | P95 ms | Recall@K | Precision@K | Noise@K | Stale Hit | Graph Lift | Quality |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| baseline | lexical | 609.56 | 680.66 | 1.000 | 1.000 | 0.000 | 0.000 | - | pass |
| baseline | graph | 603.70 | 678.38 | 0.833 | 0.633 | 0.367 | 0.000 | 0.333 | pass |
| baseline | temporal | 902.69 | 973.55 | 0.000 | 0.000 | 1.000 | 0.000 | - | fail |
| baseline | scale | 950.81 | 1016.40 | 1.000 | 0.000 | 1.000 | 0.000 | - | pass |
| baseline | route | 929.70 | 1038.00 | 0.000 | 0.000 | 1.000 | 0.000 | - | fail |
| baseline | all | 766.69 | 997.24 | 0.611 | 0.378 | 0.622 | 0.000 | 0.333 | pass |
| activation-v1 | lexical | 4.02 | 6.07 | 1.000 | 1.000 | 0.000 | 0.000 | - | pass |
| activation-v1 | graph | 5.80 | 8.64 | 0.833 | 0.550 | 0.450 | 0.000 | 0.333 | pass |
| activation-v1 | temporal | 482.01 | 516.54 | 1.000 | 0.500 | 0.500 | 0.000 | - | pass |
| activation-v1 | scale | 924.17 | 985.31 | 1.000 | 0.000 | 1.000 | 0.000 | - | pass |
| activation-v1 | route | 1095.96 | 1157.69 | 1.000 | 0.500 | 0.500 | 0.000 | - | pass |
| activation-v1 | all | 419.63 | 1129.64 | 0.944 | 0.517 | 0.483 | 0.000 | 0.333 | pass |
| activation-v2-a2 | lexical | 7.99 | 10.92 | 1.000 | 1.000 | 0.000 | 0.000 | - | pass |
| activation-v2-a2 | graph | 9.06 | 13.08 | 0.833 | 0.550 | 0.450 | 0.000 | 0.333 | pass |
| activation-v2-a2 | temporal | 26.21 | 38.14 | 1.000 | 0.500 | 0.500 | 0.000 | - | pass |
| activation-v2-a2 | scale | 22.16 | 29.99 | 1.000 | 0.000 | 1.000 | 0.000 | - | pass |
| activation-v2-a2 | route | 44.66 | 55.13 | 1.000 | 0.500 | 0.500 | 0.000 | - | pass |
| activation-v2-a2 | all | 19.86 | 44.06 | 0.944 | 0.517 | 0.483 | 0.000 | 0.333 | pass |
| activation-v3-a3 | lexical | 5.10 | 6.59 | 1.000 | 1.000 | 0.000 | 0.000 | - | pass |
| activation-v3-a3 | graph | 7.55 | 9.48 | 0.833 | 0.550 | 0.450 | 0.000 | 0.333 | pass |
| activation-v3-a3 | temporal | 22.22 | 25.06 | 1.000 | 0.500 | 0.500 | 0.000 | - | pass |
| activation-v3-a3 | scale | 19.35 | 28.19 | 1.000 | 0.000 | 1.000 | 0.000 | - | pass |
| activation-v3-a3 | route | 39.68 | 49.74 | 1.000 | 0.500 | 0.500 | 0.000 | - | pass |
| activation-v3-a3 | all | 16.91 | 39.94 | 0.944 | 0.517 | 0.483 | 0.000 | 0.333 | pass |
| activation-v4-a4 | lexical | 5.46 | 8.94 | 1.000 | 1.000 | 0.000 | 0.000 | - | pass |
| activation-v4-a4 | graph | 8.63 | 13.25 | 0.833 | 0.550 | 0.450 | 0.000 | 0.333 | pass |
| activation-v4-a4 | temporal | 18.84 | 21.66 | 1.000 | 0.500 | 0.500 | 0.000 | - | pass |
| activation-v4-a4 | scale | 14.77 | 18.29 | 1.000 | 0.000 | 1.000 | 0.000 | - | pass |
| activation-v4-a4 | route | 35.44 | 43.22 | 1.000 | 0.500 | 0.500 | 0.000 | - | pass |
| activation-v4-a4 | all | 15.29 | 36.26 | 0.944 | 0.517 | 0.483 | 0.000 | 0.333 | pass |

Key interpretation:

- A2 keeps the quality gains from `activation-v1`: aggregate recall stays at `0.944`, graph recall lift stays at `0.333`, and stale hit rate stays at `0`.
- A2 fixes the high-latency route and broad-query behavior from `activation-v1` by bounding graph spread seeds and making `Store.Route()` a read-only activation path.
- Aggregate average latency improves from `766.69 ms` in baseline to `19.86 ms` in A2, about `38.6x` faster on this synthetic 10k benchmark.
- A3 keeps the same quality as A2 and reduces aggregate average latency from `19.86 ms` to `16.91 ms` by selecting top graph seeds during the activation scan instead of sorting all direct matches.
- A4 keeps the same quality as A3 and reduces aggregate average latency from `16.91 ms` to `15.29 ms` by maintaining bounded top-K final results for limited activation queries.

## 8. Stress Runs

After the official `10k` run passes, run scale-only stress tests:

```bash
/usr/local/go/bin/go run ./cmd/lh-memory-bench \
  --variant activation-v1-50k \
  --scenario scale \
  --dataset synthetic \
  --size 50000 \
  --rounds 10 \
  --limit 6 \
  --out /tmp/lh-memory-bench/activation-v1-50k-scale.jsonl
```

Graph-only stress:

```bash
/usr/local/go/bin/go run ./cmd/lh-memory-bench \
  --variant activation-v1-50k \
  --scenario graph \
  --dataset synthetic \
  --size 50000 \
  --rounds 10 \
  --limit 6 \
  --out /tmp/lh-memory-bench/activation-v1-50k-graph.jsonl
```

A3/A4 50k scale snapshot:

```bash
/usr/local/go/bin/go run ./cmd/lh-memory-bench \
  --variant activation-v3-a3-50k \
  --scenario scale \
  --dataset synthetic \
  --size 50000 \
  --rounds 10 \
  --limit 6 \
  --out /tmp/lh-memory-bench/activation-v3-a3-50k-scale.jsonl
```

| Variant | Scenario | Size | Avg ms | P95 ms | Recall@K | Noise@K | Stale Hit | Quality |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| activation-v3-a3-50k | scale | 50000 | 98.19 | 121.81 | 1.000 | 1.000 | 0.000 | pass |
| activation-v4-a4-50k | scale | 50000 | 88.78 | 112.88 | 1.000 | 1.000 | 0.000 | pass |

## 9. Known Caveats

Since A2, `Store.Route()` intentionally uses `RouteActivationOptions()` instead
of the production `Search()` path. Route activation is read-only, does not
persist access stats, and limits graph spread to the strongest direct seeds.
Therefore, `route` measures bounded routing latency rather than writeback cost.

Since A3, graph seed selection is maintained as a bounded top-N list during the
direct-match scan. This avoids full direct-hit sorting before graph propagation.
The final activation results are still sorted by score before applying the
caller-facing result limit.

Since A4, final activation result selection is also bounded when `Limit > 0`.
The scan keeps only the top-K candidate results, then sorts that small result
set before returning. Unlimited `Search()` calls still return the complete
sorted result set.

## 10. Rejected Experiments

### A5: Bounded Direct Candidate Window

A5 tested a more aggressive bounded direct-candidate window: when `Limit > 0`,
the activation scan kept a widened top direct window, then only loaded those
direct candidates into the graph-spread score map.

The quality gates still passed, but latency regressed against A4:

| Variant | Scenario | Size | Avg ms | P95 ms | Recall@K | Stale Hit | Quality |
| --- | --- | ---: | ---: | ---: | ---: | ---: | --- |
| activation-v4-a4 | all | 10000 | 15.29 | 36.26 | 0.944 | 0.000 | pass |
| activation-v5-a5-window | all | 10000 | 17.07 | 36.79 | 0.944 | 0.000 | pass |
| activation-v4-a4-50k | scale | 50000 | 88.78 | 112.88 | 1.000 | 0.000 | pass |
| activation-v5-a5-window-50k | scale | 50000 | 98.27 | 111.48 | 1.000 | 0.000 | pass |

Decision: do not adopt A5. The extra bounded-window bookkeeping did not beat
A4 on the current benchmark. A4 remains the active implementation.

The `scale` scenario is primarily a latency scenario. It does not use strict
precision as a quality gate because broad top-k results are expected in large
synthetic corpora.

Synthetic quality metrics are stable and comparable across branches. Real memory
metrics are useful for manual inspection unless golden labels are added later.

## 11. Next Extensions

The next benchmark version can add:

- real labeled query sets
- pairwise JSONL comparison command
- CPU and heap profile flags
- graph depth sweeps
- vector/RAG hybrid comparison
- repeated real-memory snapshots
