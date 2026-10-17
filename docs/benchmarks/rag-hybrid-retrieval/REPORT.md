# LuckyAgent RAG 优化 Benchmark 报告

## 结论

在固定的离线对照集上，本次 Hybrid 检索同时保留了 Dense 的语义召回，
并补齐了 Dense 无法表示的精确标识符召回：

- 语义组：Dense-only 与 Hybrid 的 Recall@1、MRR 都是 `1.000`，无质量回退。
- 精确标识符组：Recall@1 从 `0.000` 提升到 `1.000`，提升 `100` 个百分点。
- 全集：Recall@1 从 `0.600` 提升到 `1.000`，提升 `40` 个百分点；空结果率从 `40%` 降到 `0%`。
- 模拟 Embedding 故障时：检索错误率仍为 `0%`，精确标识符 Recall@1 保持 `1.000`。
- 代价：510 篇文档下，Hybrid P50 为 `2.50ms`，Dense-only P50 为 `35.68us`。当前词法实现是全量扫描，约增加 `2.46ms`，后续规模化需要倒排索引。

这组结果证明了本次实现的三个目标：精确词召回生效、正常语义召回未被破坏、
Embedding 故障时词法降级可用。它不等价于线上最终答案准确率 A/B 测试。

## 实验设计

- 语料：10 篇有效文档，加 500 篇无关噪声文档，共 510 篇。
- 查询：10 个独立查询，包括 6 个中英文语义查询和 4 个 OOV 精确标识符查询。
- 精确样本：`ERR_LEASE_409`、`cfg_rag_dense_weight`、`trace_id_7F3A`、`ZX-8842`。
- 对照模式：`dense-only`、`hybrid`、`hybrid-embedding-outage`。
- 参数：`top_k=3`、30 轮；质量指标为 Recall@1、MRR、空结果率、错误率。
- 延迟：不包含建索引时间；每个 Hybrid 样本批内执行 10 次，Dense-only 执行 100 次，以降低 Windows 计时量化误差。
- Embedder：确定性的受控语义 Embedder，显式把下划线标识符和错误码视为 OOV，不调用外部 API。

30 轮重复用于测量延迟分布；质量集仍然只有 10 个独立查询，不能把重复轮次视为新增质量样本。

## 结果

| 模式 | 分组 | Recall@1 | MRR | 空结果率 | 错误率 | P50 | P95 |
|---|---:|---:|---:|---:|---:|---:|---:|
| Dense-only | 全部 | 0.600 | 0.600 | 40% | 0% | 35.68us | 50.49us |
| Dense-only | 语义 | 1.000 | 1.000 | 0% | 0% | 38.55us | 59.11us |
| Dense-only | 精确 | 0.000 | 0.000 | 100% | 0% | 26.13us | 45.66us |
| Hybrid | 全部 | 1.000 | 1.000 | 0% | 0% | 2.50ms | 3.70ms |
| Hybrid | 语义 | 1.000 | 1.000 | 0% | 0% | 2.62ms | 4.04ms |
| Hybrid | 精确 | 1.000 | 1.000 | 0% | 0% | 2.28ms | 3.45ms |
| Hybrid + Embedding 故障 | 全部 | 0.800 | 0.800 | 0% | 0% | 3.47ms | 5.31ms |
| Hybrid + Embedding 故障 | 语义 | 0.667 | 0.667 | 0% | 0% | 3.66ms | 5.36ms |
| Hybrid + Embedding 故障 | 精确 | 1.000 | 1.000 | 0% | 0% | 3.19ms | 5.10ms |

故障模式下语义组下降到 `0.667` 是预期边界：没有 Query Embedding 后只剩词法信号，
跨语言或同义表达无法完整恢复。但旧实现会直接报错，本次实现至少返回可用的词法证据，
且四个精确标识符全部命中。

## 性能判断

Hybrid 相对 Dense-only 的离线核心检索 P50 约为 `70x`，但绝对增加约 `2.46ms`。
这个倍率不能外推到线上请求，因为本测试的本地 Embedder 几乎没有延迟；真实远程
Embedding 请求通常会显著稀释该倍率。

当前 [hybrid.go](../../../internal/rag/hybrid.go) 每次查询都会遍历并分词全部 Chunk，
因此延迟会随知识库规模近似线性增长。500 篇噪声下几毫秒可接受，但万级以上文档应引入：

1. 持久化倒排表和文档频率，避免每次查询重新分词。
2. 增量更新词法索引，与 SQLite 文档事务同步提交。
3. 继续保留 Dense 候选池，并用 RRF 或校准后的分数融合。
4. 在 1k、10k、100k Chunk 上建立 P50/P95、内存和索引写放大基线。

## 复现

```powershell
go run ./cmd/la-rag-bench `
  --rounds 30 `
  --inner 10 `
  --noise-docs 500 `
  --top-k 3 `
  --out tmp/interview/rag-benchmark/results.jsonl
```

运行环境：

- Go：`go1.25.0 windows/amd64`
- CPU：`12th Gen Intel(R) Core(TM) i7-12700H`
- 运行结果：`clean=true`
- JSONL：901 行
- SHA-256：`5BA6FD4E503D61311B8A94462E5A99F03A06C7E0DDC5A203BD25D26EE37E999C`

原始逐查询记录和最终 summary 位于 `results.jsonl`，benchmark 实现位于
`cmd/la-rag-bench`。
