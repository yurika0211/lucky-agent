# Context Packer V1 Typed Memory Results

日期：2026-06-07

## 1. 本次继续做了什么

在 baseline benchmark 之后，本次实现了 V1 Typed Memory Packer。

核心变化：

- Working Memory 从单段文本升级为 typed sections。
- `memory.Route(query)` 的输出被拆成事实、工具、约束、搜索建议、时间警告、证据引用。
- 户外场景下，route 会补充 location 记忆，用于生成 location hint。
- benchmark 继续使用同一套 `cmd/lh-context-packer-bench` 数据集。

新增/修改的主要代码：

```text
internal/agent/context_planner.go
internal/agent/context_packer_snapshot.go
internal/agent/context_packer_test.go
internal/memory/memory.go
cmd/lh-context-packer-bench/
```

## 2. V1 Typed Memory Packer 格式

现在 Working Memory 结构大致是：

```text
[Working Memory — Mandatory Memory Gate]

[Required Tools]
- current_time
- web_search

[Answer Constraints]
- ...

[Suggested web_search queries]
- ...

[Temporal Warnings]
- Temporal resolution:
- Superseded refs: ...

[Evidence Refs]
Memory refs:
- ...

[Must Use Facts]
- ...

[Memory Router]
- ...
```

这个顺序是经过 benchmark 调整后的结果：先保留工具、约束、时间和证据，再放事实和 router 细节。

## 3. 最终采用的 V1f 结果

运行命令：

```bash
/usr/local/go/bin/go run ./cmd/lh-context-packer-bench \
  -variant typed-memory-v1f \
  -scenario all \
  -rounds 3 \
  -out docs/reports/context-packer-typed-memory-v1f-20260607.jsonl \
  -golden-trace
```

总体结果：

```text
records: 12
avg_prompt_tokens: 1559.25
avg_quality: 0.9000
avg_CMR: 1.0000
avg_CR: 0.8750
avg_TWR: 1.0000
avg_ToolRecall: 1.0000
avg_ERR: 1.0000
avg_ContextNoise: 0.7500
p95_duration_ms: 2.61
latency_pass: true
clean: false
```

## 4. Baseline vs V1f

| Metric | Baseline | V1f | Delta |
| --- | ---: | ---: | ---: |
| Avg Quality | 0.7958 | 0.9000 | +0.1042 |
| CMR | 0.7917 | 1.0000 | +0.2083 |
| CR | 0.6667 | 0.8750 | +0.2083 |
| TWR | 1.0000 | 1.0000 | +0.0000 |
| ToolRecall | 1.0000 | 1.0000 | +0.0000 |
| ERR | 1.0000 | 1.0000 | +0.0000 |
| ContextNoise | 0.7500 | 0.7500 | +0.0000 |
| Avg Prompt Tokens | 1273.75 | 1559.25 | +285.50 |
| P95 Duration | 2.57ms | 2.61ms | +0.04ms |

结论：V1f 明显提升质量，代价是平均 prompt 多约 `285.5` tokens。延迟几乎不变。

## 5. 分场景结果

| Scenario | Quality | CMR | CR | TWR | ToolRecall | ERR | Noise | Tokens | P95 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| memory_constraint | 0.933 | 1.000 | 1.000 | 1.000 | 1.000 | 1.000 | 0.667 | 1600 | 2.65ms |
| tool_gate | 0.933 | 1.000 | 1.000 | 1.000 | 1.000 | 1.000 | 0.667 | 1694 | 1.47ms |
| temporal_conflict | 0.833 | 1.000 | 0.500 | 1.000 | 1.000 | 1.000 | 0.667 | 1669 | 2.36ms |
| long_history | 0.900 | 1.000 | 1.000 | 1.000 | 1.000 | 1.000 | 1.000 | 1274 | 2.28ms |

## 6. 关键发现

### 6.1 V1 解决了关键记忆丢失

Baseline 的 `CMR = 0.7917`，V1f 到了 `1.0000`。

这说明 typed memory block 对事实保留有效，尤其是：

```text
daughter has active pollen allergy
quiet parks
targeted Go tests
```

### 6.2 工具门控变稳定

`ToolRecall = 1.0` 继续保持。

更重要的是，工具约束现在位于独立 section：

```text
[Required Tools]
- current_time
- web_search
```

这比把工具要求混在普通段落里更利于模型遵守。

### 6.3 Temporal warning 保住了

`TWR = 1.0`。

V1f 显式保留：

```text
[Temporal Warnings]
- Temporal resolution:
- Superseded refs: ...
```

这比只在 router 末尾出现更稳。

### 6.4 仍未解决长历史噪声

`ContextNoise = 0.75` 没变。

原因是 V1 只优化 memory packing，不优化 history packing。

这说明下一步应该做 V3 Intent-aware History Packer，而不是继续在 V1 上纠结。

### 6.5 Temporal conflict 还有一个约束缺口

`temporal_conflict` 的 `CR = 0.5`。

仍缺：

```text
do not apply older allergy risk unless new evidence contradicts it
```

这来自 route 的 inactive/resolved 约束，当前 benchmark 数据里 active/superseded 状态仍让它不是每次自然进入 route constraint。

下一步可以在 temporal packer 中把 superseded/resolved 语义转换为独立约束，而不是只作为 warning/ref。

## 7. 是否接受 V1f

建议接受 V1f 作为当前 Context Packer 的 V1 实现。

理由：

- 质量从 `0.7958` 提升到 `0.9000`。
- CMR 从 `0.7917` 提升到 `1.0000`。
- CR 从 `0.6667` 提升到 `0.8750`。
- TWR、ToolRecall、ERR 均保持 `1.0000`。
- P95 仍只有 `2.61ms`，远低于 `10ms` 门槛。

未通过 clean 的主要原因是 history noise，不属于 V1 的目标范围。

## 8. 下一步

推荐进入 V3 Intent-aware History Packer。

目标：

```text
ContextNoise: 0.7500 -> <= 0.2500
long_history Noise: 1.0000 -> <= 0.2500
Quality: keep >= 0.9000
P95: keep <= 10ms
```

也可以先做一个小的 V2 temporal constraint patch，把 temporal_conflict 的 `CR` 从 `0.5` 拉到 `1.0`，但收益比 history noise 小。
