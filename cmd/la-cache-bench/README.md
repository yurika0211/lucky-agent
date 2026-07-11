# Prompt Cache Benchmark

`lh-cache-bench` measures both planner-side prompt-prefix stability and actual
provider prompt-cache behavior. It is intended for A/B testing system prompt,
session checkpoint, context ordering, and history packing changes.

## Build

```bash
go run ./cmd/la-cache-bench --help
```

The tool uses the normal LuckyAgent config and writes upstream captures through
`LH_UPSTREAM_CAPTURE_DIR`.

By default the benchmark copies the active config into a temporary
LuckyAgent home. This avoids existing sessions, cron jobs, autonomy queues,
and other local runtime state from polluting captures. Pass
`--isolated-home=false` only when intentionally measuring the real local home.

## Recommended A/B Run

Run the same command on the baseline branch and on the fixed branch:

```bash
go run ./cmd/la-cache-bench \
  --variant baseline \
  --scenario same-session \
  --rounds 20 \
  --delay 65s \
  --model deepseek-v4-flash \
  --pure \
  --out /tmp/lh-cache-bench/baseline.jsonl \
  --capture-dir /tmp/lh-cache-bench/baseline-capture
```

```bash
go run ./cmd/la-cache-bench \
  --variant fixed \
  --scenario same-session \
  --rounds 20 \
  --delay 65s \
  --model deepseek-v4-flash \
  --pure \
  --out /tmp/lh-cache-bench/fixed.jsonl \
  --capture-dir /tmp/lh-cache-bench/fixed-capture
```

Use `--delay 65s` when testing the current timestamp metadata, because the
current prompt uses minute-level time formatting. Same-minute calls may still
hit cache.

For checkpoint and append-only session history, use the same benchmark code in
baseline and candidate worktrees and keep calls inside the provider cache TTL:

```bash
go run ./cmd/la-cache-bench \
  --variant baseline \
  --scenario checkpoint-session \
  --rounds 16 \
  --delay 2s \
  --pure \
  --out /tmp/lh-cache-bench/checkpoint-baseline.jsonl \
  --capture-dir /tmp/lh-cache-bench/checkpoint-baseline-capture
```

```bash
go run ./cmd/la-cache-bench \
  --variant checkpoint-v1 \
  --scenario checkpoint-session \
  --rounds 16 \
  --delay 2s \
  --pure \
  --out /tmp/lh-cache-bench/checkpoint-v1.jsonl \
  --capture-dir /tmp/lh-cache-bench/checkpoint-v1-capture
```

Run each variant at least three times, alternating variant order. Use the same
provider, model, API base, region, delay, and prompt override. Do not compare a
cold baseline run with a warm candidate run.

## Scenarios

- `single`: creates a fresh session each round and asks a fixed short question.
- `same-session`: reuses one session across rounds with a fixed no-tool prompt.
- `checkpoint-session`: seeds archived history, adds a stable compact boundary,
  retains four recent turns, and then grows the raw session tail each round.
- `tool`: reuses one session and asks the model to run `pwd` via the shell tool.
- `all`: runs all scenarios in order.

For a pure prompt-cache test that hides tools from the provider request, prefer:

```bash
--pure
```

`--pure` expands to:

```text
--no-tools --max-iterations 1 --auto-approve=false
```

If you need to tune those switches manually, use:

```bash
--no-tools
```

Benchmark sessions are removed from the normal LuckyAgent session store by
default. Add `--keep-sessions` if you want to inspect the generated session
history afterward.

## Output

The JSONL file contains one `round` record per benchmark round and one `summary`
record per scenario. Key fields:

- `prompt_tokens`
- `cached_prompt_tokens`
- `cached_ratio`
- `uncached_prompt_tokens`
- `provider_calls`
- `missing_usage_calls`
- `capture_errors`
- `tool_rounds`
- `tool_calls`
- `tool_names`
- `system_prompt_hash`
- `system_prompt_bytes`
- `system_prompt_tokens`
- `system_prompt_stable`
- `prompt_hash`
- `prompt_estimated_tokens`
- `stable_prefix_messages`
- `stable_prefix_tokens`
- `stable_prefix_ratio`
- `prefix_comparable`
- `empty_responses`
- `clean`
- `duration_ms`

`stable_prefix_tokens` is a conservative planner metric. It counts estimated
tokens in complete, byte-identical leading messages shared with the previous
round. It stops at the first changed message. The first round is cold and is
excluded from summary prefix averages.

Provider tokenization and cache block rounding can make actual
`cached_prompt_tokens` differ from this estimate. In `--pure` mode the estimate
is still useful because model-visible tool schemas are absent from the request.

Compare variants with:

```bash
jq -s '
  map(select(.type=="summary")) |
  map({
    variant,
    scenario,
    rounds,
    avg_prompt_tokens,
    avg_cached_prompt_tokens,
    cached_ratio,
    avg_stable_prefix_tokens,
    avg_stable_prefix_ratio,
    min_stable_prefix_tokens,
    prefix_comparable_rounds,
    avg_uncached_prompt_tokens,
    errors,
    capture_errors,
    missing_usage_calls,
    tool_rounds,
    tool_calls,
    system_prompt_stable,
    clean
  })
' /tmp/lh-cache-bench/*.jsonl
```

## Interpretation

The main success metric is:

```text
cached_ratio = cached_prompt_tokens / prompt_tokens
```

The provider-independent leading indicator is:

```text
stable_prefix_ratio = stable_prefix_tokens / estimated_prompt_tokens
```

Useful secondary metrics:

```text
extra_cached_tokens = fixed.cached_prompt_tokens - baseline.cached_prompt_tokens
uncached_reduction = baseline.uncached_prompt_tokens - fixed.uncached_prompt_tokens
stable_prefix_lift = fixed.avg_stable_prefix_ratio - baseline.avg_stable_prefix_ratio
```

For this Context Planner change, accept the candidate only when all three move
in the expected direction across repeated runs:

1. `avg_stable_prefix_ratio` increases. This proves the planner emits a more
   reusable prefix independently of provider cache state.
2. `cached_ratio` increases and `avg_uncached_prompt_tokens` decreases. This
   proves the provider realizes the theoretical improvement.
3. Error count, empty responses, missing usage, prompt size, and latency do not
   regress materially. Treat `clean=false` as an invalid measurement.

If removing dynamic system prompt metadata is effective, `same-session` and
`tool` should show the largest lift, especially with `--delay 65s`.

Treat `clean=false` as a warning that the run is not a pure A/B cache signal.
Common causes are model-visible tool calls, capture errors, missing usage
records, per-round errors, or changing system prompt hashes.
