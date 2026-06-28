# Memory Activation Benchmark

`lh-memory-bench` measures LuckyAgent memory retrieval quality and latency.
It follows the same experiment shape as `lh-cache-bench`: each run writes JSONL
round records plus per-scenario summary records, so different branches or
activation variants can be compared with the same command.

## Build

```bash
go run ./cmd/lh-memory-bench --help
```

The default dataset is synthetic and isolated in a temporary memory vault. The
synthetic vault uses LuckyAgent Markdown memory notes with YAML frontmatter,
wikilinks, aliases, tags, and temporal-state fields.

## Recommended A/B Run

Run the same command on the baseline branch and on the changed branch:

```bash
go run ./cmd/lh-memory-bench \
  --variant baseline \
  --scenario all \
  --dataset synthetic \
  --size 10000 \
  --rounds 10 \
  --limit 6 \
  --out /tmp/lh-memory-bench/baseline.jsonl
```

```bash
go run ./cmd/lh-memory-bench \
  --variant activation-v1 \
  --scenario all \
  --dataset synthetic \
  --size 10000 \
  --rounds 10 \
  --limit 6 \
  --out /tmp/lh-memory-bench/activation-v1.jsonl
```

Compare summaries:

```bash
jq -s '
  map(select(.type=="summary")) |
  map({
    variant,
    scenario,
    records,
    avg_duration_ms: (.avg_duration_ns / 1000000),
    p95_ms: (.p95_duration_ns / 1000000),
    avg_recall_at_k,
    avg_precision_at_k,
    avg_noise_at_k,
    avg_stale_hit_rate,
    graph_recall_lift,
    clean,
    quality_pass
  })
' /tmp/lh-memory-bench/*.jsonl
```

## Scenarios

- `lexical`: direct content/category/tag/alias recall.
- `graph`: runs each query twice, once with graph propagation disabled and once
  enabled. The summary reports graph recall lift and graph latency overhead.
- `temporal`: verifies expired, future, superseded, or otherwise inactive
  memories are not returned as active hits.
- `scale`: measures activation latency against the selected dataset size.
- `route`: measures `Store.Route` end-to-end routing signals such as risk flags
  and required tools.
- `all`: runs all scenarios in order.

## Datasets

Synthetic dataset:

```bash
go run ./cmd/lh-memory-bench --dataset synthetic --size 1000 --scenario graph
```

Real memory vault:

```bash
go run ./cmd/lh-memory-bench \
  --dataset real \
  --memory-dir ~/.luckyagent/memory \
  --scenario scale \
  --query "女儿户外活动"
```

Real datasets usually do not include golden labels, so quality metrics are only
meaningful for synthetic runs unless a query override intentionally reuses a
synthetic golden case.

## Output

Each `round` record contains:

- latency: `duration_ns`, `duration_ms`
- retrieval results: `result_ids`, `result_count`
- quality: `recall_at_k`, `precision_at_k`, `mrr`, `ndcg_at_k`, `noise_at_k`
- temporal safety: `forbid_hit_count`, `stale_hit_rate`
- route signals for route scenario: `risk_flags`, `required_tools`
- run status: `clean`, `quality_pass`

Each `summary` record aggregates one scenario. A clean synthetic run means:

```text
errors == 0
forbid_hit_count <= --max-stale-hits
avg recall on quality candidate records >= --min-recall
avg noise on quality candidate records <= --max-noise
route risk/tool recall, when present, also meets --min-recall
```

Defaults are intentionally lenient for early experiments:

```text
--min-recall 0.65
--max-noise 0.60
--max-stale-hits 0
```
