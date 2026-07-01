# Graph RAG Benchmark

`la-graph-rag-bench` measures Graph RAG retrieval behavior with a deterministic
synthetic corpus. It compares vector-only retrieval with graph-enhanced
retrieval across direct, bridge, multihop, and distractor scenarios.

The benchmark does not call a real LLM. It uses a rule-based extraction provider
and the mock embedder so runs are repeatable and suitable for regression checks.

## Run

```bash
go run ./cmd/la-graph-rag-bench \
  --variant graph-v1 \
  --scenario all \
  --rounds 5 \
  --noise-docs 200 \
  --out /tmp/lh-graph-rag-bench/graph-v1.jsonl
```

## Scenarios

- `direct`: a query should activate the directly mentioned entity and its close
  graph neighborhood.
- `bridge`: a query starts from a person and should traverse employer/location
  relations.
- `multihop`: queries should recover entities connected by multiple relations.
- `distractor`: relevant entities must stay in the top results despite
  unrelated archive documents.
- `all`: runs all scenarios.

## Important Metrics

- `graph_node_recall`: expected graph entities found in top K activated nodes.
- `graph_rel_recall`: expected relation types found in activation paths.
- `graph_source_recall`: expected document sources found in vector chunks.
- `graph_latency_overhead_pct`: graph search latency overhead versus
  vector-only retrieval.
- `quality_pass`: graph node recall and node noise meet configured thresholds.

## Compare Summaries

```bash
jq -s '
  map(select(.type=="summary")) |
  map({
    variant,
    scenario,
    records,
    graph_node_recall: .avg_graph_node_recall,
    graph_rel_recall: .avg_graph_rel_recall,
    graph_overhead: .graph_latency_overhead_pct,
    clean,
    quality_pass
  })
' /tmp/lh-graph-rag-bench/*.jsonl
```
