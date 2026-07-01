# la-swe-bench

`la-swe-bench` runs LuckyAgent against SWE-bench instances and writes
SWE-bench evaluator-compatible prediction JSONL.

The runner expects local repository caches. For a SWE-bench repo value such as
`django/django`, place a local clone at either:

```text
<repos-dir>/django/django
<repos-dir>/django__django
```

## Dry Run

```bash
go run ./cmd/la-swe-bench \
  -dataset /path/to/swebench_lite.jsonl \
  -dry-run \
  -limit 2
```

Dry run validates dataset loading and output shape without calling an LLM or
preparing git worktrees.

## Generate Predictions

```bash
go run ./cmd/la-swe-bench \
  -dataset /path/to/swebench_lite.jsonl \
  -repos-dir ~/.cache/swebench/repos \
  -work-dir ~/.luckyagent/bench/swebench \
  -variant baseline \
  -model-name luckyagent/baseline \
  -limit 10 \
  -auto-approve \
  -predictions docs/reports/swebench-lite-predictions.jsonl \
  -out docs/reports/swebench-lite-results.jsonl
```

The generated predictions file contains one object per instance:

```json
{"instance_id":"...","model_name_or_path":"luckyagent/baseline","model_patch":"diff --git ..."}
```

Use the official SWE-bench harness to evaluate `model_patch`; this command only
generates patches and LuckyAgent run traces.

Non-dry-run execution requires `-auto-approve` because the benchmark runner is
non-interactive and repair attempts need terminal and file-edit tools. Use it
only with disposable worktrees or containers.
