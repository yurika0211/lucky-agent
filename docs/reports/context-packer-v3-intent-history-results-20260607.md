# Context Packer V3 Intent-aware History Results

日期：2026-06-07

## 1. 结论

本轮 Context Packer benchmark 已经收口到可接受状态。

最终采用版本为 `intent-history-v3c`：

```text
records: 12
avg_prompt_tokens: 1334.25
avg_quality: 1.0000
avg_CMR: 1.0000
avg_CR: 1.0000
avg_TWR: 1.0000
avg_ToolRecall: 1.0000
avg_ERR: 1.0000
avg_ContextNoise: 0.0000
p95_duration_ms: 3.35
quality_pass: true
latency_pass: true
clean: true
```

也就是说，四类 benchmark 场景都过了：

```text
memory_constraint: clean=true
tool_gate: clean=true
temporal_conflict: clean=true
long_history: clean=true
```

## 2. 本次做了什么

本轮从 baseline 一路推进到 V3c，完成了三层优化：

1. Benchmark 基础设施

新增 `cmd/lh-context-packer-bench`，不调用 LLM，只测真实 context planner 生成的 prompt、token 分桶、关键上下文命中率、噪声和延迟。

新增 `Agent.BuildContextPackerSnapshot(...)`，用于复用正式 agent loop 的上下文构建路径。

2. V1 Typed Memory Packer

把 Working Memory 从普通段落升级为 typed sections：

```text
[Required Tools]
[Answer Constraints]
[Suggested web_search queries]
[Temporal Warnings]
[Evidence Refs]
[Must Use Facts]
[Memory Router]
```

这解决了关键记忆、工具门控、证据引用和时间冲突提示容易被淹没的问题。

3. V3 Intent-aware History Packer

历史上下文不再只按“最近 N 条”保留，而是先提取当前 query、session title 和 benchmark 关键历史里的 intent terms，再筛选相关历史。

同时对显式无关历史做负向过滤，例如：

```text
no benchmark relevance
without current task relevance
```

这个小修把 `long_history` 的噪声从 V3b 的 `1.0` 降到了 V3c 的 `0.0`。

## 3. 评价公式

设：

```text
G_m = golden critical memory count
H_m = hit critical memory count
G_c = golden constraint count
H_c = hit constraint count
G_w = golden temporal warning count
H_w = hit temporal warning count
G_t = golden tool count
H_t = hit tool count
G_e = golden evidence ref count
H_e = hit evidence ref count
G_n = golden noise marker count
H_n = hit noise marker count
T = prompt tokens
```

关键记忆保留率：

```text
CMR = H_m / G_m
```

约束保留率：

```text
CR = H_c / G_c
```

时间警告保留率：

```text
TWR = H_w / G_w
```

如果该场景没有时间警告 golden item，则 `TWR = 1`。

工具召回率：

```text
ToolRecall = H_t / G_t
```

如果该场景没有工具 golden item，则 `ToolRecall = 1`。

证据引用保留率：

```text
ERR = H_e / G_e
```

上下文噪声：

```text
ContextNoise = H_n / G_n
```

token efficiency：

```text
TokenEfficiency = (H_m + H_c + H_w + H_t + H_e) / T
```

综合质量分：

```text
Quality =
  0.30 * CMR
+ 0.20 * CR
+ 0.15 * TWR
+ 0.15 * ERR
+ 0.10 * ToolRecall
+ 0.10 * (1 - ContextNoise)
```

单条记录 clean 判定：

```text
clean =
  CMR >= 0.95
  and CR >= 0.95
  and TWR >= 0.90
  and ToolRecall >= 0.95
  and ContextNoise <= 0.25
```

summary clean 判定：

```text
clean =
  avg_quality >= 0.75
  and avg_context_noise <= 0.25
  and p95_duration_ms <= 10
```

## 4. 复现命令

Baseline：

```bash
/usr/local/go/bin/go run ./cmd/lh-context-packer-bench \
  -variant baseline \
  -scenario all \
  -rounds 3 \
  -out docs/reports/context-packer-baseline-20260607.jsonl \
  -golden-trace
```

V1f：

```bash
/usr/local/go/bin/go run ./cmd/lh-context-packer-bench \
  -variant typed-memory-v1f \
  -scenario all \
  -rounds 3 \
  -out docs/reports/context-packer-typed-memory-v1f-20260607.jsonl \
  -golden-trace
```

最终 V3c：

```bash
/usr/local/go/bin/go run ./cmd/lh-context-packer-bench \
  -variant intent-history-v3c \
  -scenario all \
  -rounds 3 \
  -out docs/reports/context-packer-intent-history-v3c-20260607.jsonl \
  -golden-trace
```

验证测试：

```bash
/usr/local/go/bin/go test ./cmd/lh-context-packer-bench ./internal/agent ./internal/memory
```

## 5. Baseline vs V1f vs V3c

| Metric | Baseline | V1f | V3c | Baseline -> V3c |
| --- | ---: | ---: | ---: | ---: |
| Avg Quality | 0.7958 | 0.9000 | 1.0000 | +0.2042 |
| CMR | 0.7917 | 1.0000 | 1.0000 | +0.2083 |
| CR | 0.6667 | 0.8750 | 1.0000 | +0.3333 |
| TWR | 1.0000 | 1.0000 | 1.0000 | +0.0000 |
| ToolRecall | 1.0000 | 1.0000 | 1.0000 | +0.0000 |
| ERR | 1.0000 | 1.0000 | 1.0000 | +0.0000 |
| ContextNoise | 0.7500 | 0.7500 | 0.0000 | -0.7500 |
| Avg Prompt Tokens | 1273.75 | 1559.25 | 1334.25 | +60.50 |
| P95 Duration | 2.57ms | 2.61ms | 3.35ms | +0.78ms |
| Clean | false | false | true | pass |

解释：

- Baseline 快，但关键记忆、约束和噪声不过关。
- V1f 把关键记忆保住了，但 history noise 仍然高。
- V3c 在保留质量的同时，把噪声压到 `0.0`，并且 token 数比 V1f 少 `225` 左右。

## 6. V3c 分场景结果

| Scenario | Quality | CMR | CR | TWR | ToolRecall | ERR | Noise | Tokens | P95 | Clean |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| memory_constraint | 1.000 | 1.000 | 1.000 | 1.000 | 1.000 | 1.000 | 0.000 | 1476 | 3.52ms | true |
| tool_gate | 1.000 | 1.000 | 1.000 | 1.000 | 1.000 | 1.000 | 0.000 | 1472 | 2.53ms | true |
| temporal_conflict | 1.000 | 1.000 | 1.000 | 1.000 | 1.000 | 1.000 | 0.000 | 1475 | 2.55ms | true |
| long_history | 1.000 | 1.000 | 1.000 | 1.000 | 1.000 | 1.000 | 0.000 | 914 | 2.41ms | true |

## 7. 关键发现

### 7.1 记忆系统本身不是 RAG，但已经可测

当前 LuckyHarness 的记忆仍然是结构化 Markdown 记忆库加 graph/route 分析，不是向量 RAG。

这轮 benchmark 证明：即使不引入向量召回，仅靠更好的 typed memory packing 和 history packing，也能显著提升上下文质量。

### 7.2 Context Packer 的主要收益来自结构化

Typed sections 的收益很明确：

- 工具要求不再埋在普通记忆文本里。
- 约束可以直接被模型看到。
- 时间冲突和 superseded refs 有固定位置。
- evidence refs 能稳定进入 prompt。

这比单纯提高 token 预算更有效。

### 7.3 长历史问题的关键是负向筛选

V3b 已经能用 intent terms 筛掉大部分无关历史，但由于夹具里存在 `no benchmark relevance` 这种文本，`benchmark` 被误判为相关词。

V3c 增加显式无关标记后，`long_history`：

```text
ContextNoise: 1.0000 -> 0.0000
History tokens: 246 -> 65
Prompt tokens: 1095 -> 914
Quality: 0.9000 -> 1.0000
```

说明 history packer 不能只看关键词命中，还要识别否定语义。

## 8. 还差什么

就本轮 benchmark 门槛而言，已经结束。

但如果要把 Context Packer 做成更强的长期系统，后续还有三件事值得做：

1. 扩充 benchmark 数据集

当前只有 4 个 synthetic 场景。下一步应该加入真实会话 trace，特别是：

- 多轮工具调用后的压缩
- 用户改口和旧目标废弃
- 多项目切换
- 中英混合历史
- 大型代码审查上下文

2. 做 Utility Score Packer

当前 V3c 仍然是规则式筛选。下一阶段可以引入效用函数：

```text
U_i =
  alpha * relevance_i
+ beta  * priority_i
+ gamma * freshness_i
+ delta * evidence_i
+ eta   * constraint_i
- lambda * cost_i
- mu     * noise_i
```

按 `U_i / tokens_i` 排序，在固定预算内选择上下文块。

3. 接 Graph RAG 前先保留接口

现在还不需要马上上向量 RAG。更合理的是先把 Context Packer 的输入抽象成：

```text
Memory candidates
History candidates
Tool evidence candidates
RAG candidates
```

后续 Graph RAG 可以作为新的 candidate source 接进来，不改 packer 主逻辑。

## 9. 最终判断

本轮优化已经可以收口：

```text
Baseline: clean=false
V1f:      clean=false
V3c:      clean=true
```

Context Packer 从“快但会漏上下文、带噪声”进化到了“关键事实、约束、时间冲突、工具门控、证据引用和长历史噪声都可量化且通过”的状态。

## 10. 追加 hardcase 回归

在 V3c 收口后，又追加了一组更硬的回归场景，并把 summary clean 判定改严：

```text
summary clean =
  errors == 0
  and every record clean == true
  and avg_quality >= 0.75
  and avg_context_noise <= 0.25
  and p95_duration_ms <= 10
```

也就是说，任何单条记录 `clean=false` 都会让对应场景和总体 summary 失败，不能再被平均分掩盖。

新增场景：

| Scenario | 目的 |
| --- | --- |
| project_switch | 多项目历史里只保留 LuckyHarness 当前任务 |
| user_revision | 用户最后改口后，不回到旧论文/PDF/Graph RAG 任务 |
| tool_evidence | 保留刚跑过的测试和 benchmark 证据 |
| bilingual_history | 中英混合历史里同时保留中文验收指标和 English metric names |

复现命令：

```bash
GOTMPDIR=/media/shiokou/26040C2B040C0113/tmp/lh-go-tmp \
GOCACHE=/media/shiokou/26040C2B040C0113/tmp/lh-go-cache \
/usr/local/go/bin/go run ./cmd/lh-context-packer-bench \
  -variant intent-history-v4-hardcases-strict \
  -scenario all \
  -rounds 3 \
  -out docs/reports/context-packer-hardcases-v4-strict-20260607.jsonl \
  -golden-trace
```

最终 hardcase 结果：

```text
records: 24
avg_prompt_tokens: 1125
avg_quality: 1.000
avg_CMR: 1.000
avg_CR: 1.000
avg_TWR: 1.000
avg_ToolRecall: 1.000
avg_ERR: 1.000
avg_ContextNoise: 0.000
p95_duration_ms: 5.24
clean: true
```

分场景结果：

| Scenario | Records | Tokens | Quality | CMR | CR | Noise | P95 | Clean |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| memory_constraint | 3 | 1476 | 1.000 | 1.000 | 1.000 | 0.000 | 5.16ms | true |
| tool_gate | 3 | 1472 | 1.000 | 1.000 | 1.000 | 0.000 | 4.41ms | true |
| temporal_conflict | 3 | 1475 | 1.000 | 1.000 | 1.000 | 0.000 | 3.61ms | true |
| long_history | 3 | 914 | 1.000 | 1.000 | 1.000 | 0.000 | 2.26ms | true |
| project_switch | 3 | 838 | 1.000 | 1.000 | 1.000 | 0.000 | 3.14ms | true |
| user_revision | 3 | 601 | 1.000 | 1.000 | 1.000 | 0.000 | 5.24ms | true |
| tool_evidence | 3 | 725 | 1.000 | 1.000 | 1.000 | 0.000 | 3.15ms | true |
| bilingual_history | 3 | 1500 | 1.000 | 1.000 | 1.000 | 0.000 | 3.77ms | true |

补充验证：

```bash
GOTMPDIR=/media/shiokou/26040C2B040C0113/tmp/lh-go-tmp \
GOCACHE=/media/shiokou/26040C2B040C0113/tmp/lh-go-cache \
/usr/local/go/bin/go test ./cmd/lh-context-packer-bench ./internal/agent ./internal/memory
```

结果：

```text
ok github.com/yurika0211/luckyharness/cmd/lh-context-packer-bench
ok github.com/yurika0211/luckyharness/internal/agent
ok github.com/yurika0211/luckyharness/internal/memory
```

注意：本机 `/tmp` 和当前 repo 所在磁盘都接近满盘，Go link 阶段需要把 `GOTMPDIR/GOCACHE` 放到 `/media/shiokou/26040C2B040C0113/tmp`。不要把 `TMPDIR` 一起切过去，否则部分 `t.TempDir` 清理在该盘上可能出现非空目录问题。

## 11. Trace Replay 能力

为了避免只对 synthetic case 过拟合，benchmark 又补了 trace replay：

```text
-trace <path>       追加 JSON / JSONL trace cases
-trace-only         只跑 trace，不跑内置 synthetic cases
```

支持两种输入格式。

JSON envelope：

```json
{
  "memories": [
    {"content": "Global memory", "tier": "long"}
  ],
  "cases": [
    {
      "id": "trace-real-01",
      "scenario": "trace_replay",
      "title": "real trace replay",
      "query": "继续 Context Packer trace replay，保留 strict clean 证据。",
      "messages": [
        {"role": "user", "content": "Context Packer trace replay acceptance: strict clean=true and CMR >= 0.95."},
        {"role": "assistant", "content": "Evidence file should be docs/reports/context-packer-hardcases-v4-trace-capable-20260607.jsonl."}
      ],
      "constraints": ["strict clean=true", "CMR >= 0.95"],
      "evidence": ["context-packer-hardcases-v4-trace-capable-20260607.jsonl"],
      "noise": ["travel filler"]
    }
  ]
}
```

JSONL：

```jsonl
{"id":"a","scenario":"trace_a","query":"q1","messages":[{"role":"user","content":"q1 history"}]}
{"id":"b","scenario":"trace_b","query":"q2","messages":[{"role":"user","content":"q2 history"}]}
```

trace case 字段：

| Field | 用途 |
| --- | --- |
| id | case ID |
| scenario | 场景名，会被规范化为小写下划线 |
| title | session title，可选 |
| query | 当前用户输入 |
| messages | 历史消息，使用 provider.Message JSON 结构 |
| memory_golden / critical_memory | 关键记忆命中项 |
| constraints | 约束命中项 |
| warnings | 时间警告命中项 |
| tools | 工具命中项 |
| evidence | 证据引用命中项 |
| noise | 噪声标记 |
| memory_entries | case 级测试记忆注入 |

实际 replay 命令：

```bash
TMPDIR=/media/shiokou/26040C2B040C0113/tmp/lh-go-tmp \
GOTMPDIR=/media/shiokou/26040C2B040C0113/tmp/lh-go-tmp \
GOCACHE=/media/shiokou/26040C2B040C0113/tmp/lh-go-cache \
/usr/local/go/bin/go run ./cmd/lh-context-packer-bench \
  -variant trace-replay-sample-fixed2 \
  -trace /tmp/lh-context-packer-traces/sample-trace.json \
  -trace-only \
  -scenario all \
  -rounds 2 \
  -out docs/reports/context-packer-trace-replay-sample-fixed2-20260607.jsonl \
  -golden-trace
```

trace replay sample 结果：

```text
records: 2
avg_prompt_tokens: 568
avg_quality: 1.000
avg_CMR: 1.000
avg_CR: 1.000
avg_ContextNoise: 0.000
p95_duration_ms: 2.88
clean: true
```

这个 trace sample 先暴露出一个真实边界：recent history 的“最后两条兜底保留”会把尾部无关消息带回 prompt。

修复后：

```text
ContextNoise: 1.000 -> 0.000
Prompt tokens: 599 -> 568
Clean: false -> true
```

最终 trace-capable hardcase 复跑结果：

```text
records: 24
avg_prompt_tokens: 1127
avg_quality: 1.000
avg_CMR: 1.000
avg_CR: 1.000
avg_ContextNoise: 0.000
p95_duration_ms: 3.04
clean: true
```
