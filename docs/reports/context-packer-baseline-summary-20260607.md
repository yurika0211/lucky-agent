# Context Packer Baseline Benchmark Summary

日期：2026-06-07

## 1. 本次做了什么

本次补了一个独立 benchmark：

```bash
/usr/local/go/bin/go run ./cmd/lh-context-packer-bench \
  -variant baseline \
  -scenario all \
  -rounds 3 \
  -out docs/reports/context-packer-baseline-20260607.jsonl \
  -golden-trace
```

它不调用 LLM，只测当前 `internal/agent/context_planner.go` 真实生成的上下文消息、token 分布、关键上下文保留率和延迟。

新增测量入口：

```text
agent.BuildContextPackerSnapshot(...)
```

用途是复用现有 context planner，输出：

- messages
- total_tokens
- bucket_tokens
- bucket_counts

## 2. 场景

本次 baseline 覆盖 4 类场景：

| Scenario | 目标 |
| --- | --- |
| memory_constraint | 关键记忆和约束是否保留 |
| tool_gate | 工具门控约束是否保留 |
| temporal_conflict | 时间冲突和 superseded 记忆是否保留 |
| long_history | 长历史中是否保留关键上下文并降低噪声 |

## 3. 总体结果

总体 summary：

```text
records: 12
avg_prompt_tokens: 1273.75
avg_quality: 0.7958
avg_CMR: 0.7917
avg_CR: 0.6667
avg_TWR: 1.0000
avg_ToolRecall: 1.0000
avg_ERR: 1.0000
avg_ContextNoise: 0.7500
p95_duration_ms: 2.57
quality_pass: false
latency_pass: true
clean: false
```

结论：当前 baseline 延迟很好，但质量没有过门槛。主要问题不是慢，而是打包策略还不够会挑上下文。

## 4. 分场景结果

| Scenario | Avg Tokens | Quality | CMR | CR | TWR | ToolRecall | Noise | P95 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| memory_constraint | 1233 | 0.733 | 0.667 | 0.500 | 1.000 | 1.000 | 0.667 | 2.43ms |
| tool_gate | 1327 | 0.867 | 1.000 | 0.667 | 1.000 | 1.000 | 0.667 | 2.64ms |
| temporal_conflict | 1302 | 0.683 | 0.500 | 0.500 | 1.000 | 1.000 | 0.667 | 1.76ms |
| long_history | 1233 | 0.900 | 1.000 | 1.000 | 1.000 | 1.000 | 1.000 | 1.50ms |

## 5. 主要发现

### 5.1 延迟已经足够低

P95 只有 `2.57ms`，低于实验门槛 `10ms`。

这说明第一阶段优化不应该先盯性能，而应该先盯质量。

### 5.2 关键约束保留不足

总体 `CR = 0.667`。

典型丢失项：

```text
Use location hint: Shanghai
Check air quality
do not apply older allergy risk unless new evidence contradicts it
```

这说明当前 planner 虽然会调用 `memory.Route(query)`，但 route 输出没有被拆成更强的 typed context。

### 5.3 关键记忆保留不足

总体 `CMR = 0.792`。

典型丢失项：

```text
quiet parks
Old note: pollen allergy was resolved
```

这说明当前 `prioritizeMemoryForContext` 更偏向少量高权重条目，但对于“冲突记忆”和“偏好补充事实”的保留不够稳定。

### 5.4 时间冲突场景最弱

`temporal_conflict` 的质量只有 `0.683`。

问题是当前上下文能保留 `Temporal resolution` 这类提示，但没有把 superseded 旧事实本身和对应约束稳定放进 prompt。

这会让模型知道“有时间解析”，但不一定看到完整冲突证据。

### 5.5 长历史噪声很高

`long_history` 的质量高，是因为关键验收指标在最近尾部被保留了。

但 `ContextNoise = 1.0`，说明无关历史仍然进入了上下文。

这支持 V3：需要 intent-aware history packer，而不是只靠 recent/middle history。

## 6. 是否通过验收

当前 baseline：

```text
Quality >= 0.75: pass at aggregate level, but scenario unstable
CMR >= 0.95: fail
CR >= 0.95: fail
TWR >= 0.90: pass
ContextNoise <= 0.25: fail
P95PackerMS <= 10: pass
```

总体 `clean=false`。

## 7. 下一步优化顺序

推荐先做 V1，而不是直接做完整 V4。

### Step 1: Typed Memory Packer

把 `memory.Route(query)` 输出拆成：

```text
[Must Use Facts]
[Required Tools]
[Answer Constraints]
[Temporal Warnings]
[Evidence Refs]
```

目标：

```text
CR: 0.667 -> >= 0.95
CMR: 0.792 -> >= 0.95
TWR: keep >= 0.90
P95: keep <= 10ms
```

### Step 2: Temporal Conflict Packing

对 superseded/conflict/expired/future 记忆做显式保留。

目标：

```text
temporal_conflict Quality: 0.683 -> >= 0.85
```

### Step 3: Intent-aware History Packer

对历史消息按当前 query 做相关性筛选，再保留最近尾部。

目标：

```text
ContextNoise: 0.750 -> <= 0.25
long_history Noise: 1.000 -> <= 0.25
```

### Step 4: Utility Score Packer

引入效用函数：

```text
U = relevance + priority + freshness + evidence + gate + type - cost - noise
```

目标是在不降低质量的前提下降低 token：

```text
avg_prompt_tokens: 1274 -> lower or unchanged
Quality: >= baseline
```

## 8. 结论

baseline 已经建立。

当前 Context Packer 的基础工程状态不错：快、稳定、可测。

但它还不是优秀 packer，因为它没有显式区分事实、约束、工具门控、时间冲突和噪声历史。下一步最值得做的是 V1 Typed Memory Packer，然后复跑同一个 benchmark 对比。
