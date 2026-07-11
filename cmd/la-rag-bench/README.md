# RAG Retrieval Benchmark

`la-rag-bench` is an offline deterministic comparison for LuckyAgent retrieval.
It compares dense-only retrieval, normal hybrid retrieval, and hybrid lexical
fallback during a simulated embedding outage.

The corpus contains semantic questions, opaque identifiers, Chinese queries,
and configurable irrelevant documents. Query latency excludes indexing time.

```bash
go run ./cmd/la-rag-bench \
  --rounds 30 \
	--inner 10 \
  --noise-docs 500 \
  --top-k 3 \
  --out tmp/interview/rag-benchmark/results.jsonl
```

The controlled embedder intentionally models opaque IDs as out-of-vocabulary.
This makes the exact-match slice reproducible without depending on an external
embedding API. Results prove retrieval behavior in this implementation; they
do not predict production answer accuracy or remote embedding latency.

To reduce Windows timer quantization, dense-only samples use ten times the
configured inner iteration count. Every JSONL query record includes its actual
`inner_iterations` value.
