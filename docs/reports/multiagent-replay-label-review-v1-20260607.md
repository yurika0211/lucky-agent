# Multi-Agent Replay Label Review v1 - 2026-06-07

## 结论

本轮完成了 75 条真实 LuckyHarness session case 的第一版人工复核标签。复核产物保留原始 prompt，并把半自动标签升级为 `manual_review_v1_codex`。

这不是最终论文级双人标注，但已经比启发式标签更接近真实任务编排需求，尤其修正了定时任务、后台任务、RAG/搜索/文件入库任务和需要真实验收的任务。

## 产物

- 原始半自动标签：`docs/reports/multiagent-replay-labels-real-20260607.jsonl`
- 人工复核标签：`docs/reports/multiagent-replay-labels-real-reviewed-v1-20260607.jsonl`
- 人工复核 TSV：`docs/reports/multiagent-replay-label-review-v1-20260607.tsv`
- reviewed replay 结果：`docs/reports/multiagent-replay-math-full-real-reviewed-v1-20260607.jsonl`

## 复核规则

| 标签 | 人工判定规则 |
| --- | --- |
| `single` | 普通对话、单次问答、简单状态读取、无需拆分的单步任务 |
| `pipeline` | 检索/读取 -> 处理/实现 -> 验证/汇总，存在明确先后依赖 |
| `autonomy_queue` | 定时、后台、持续等待、队列触发、异步投递类任务 |
| `needs_verifier=true` | 需要真实搜索、文件存在性、RAG 入库、配置/队列状态、天气/实时事实、工具调用结果或输出约束验证 |
| `needs_critic=true` | 涉及安全/策略边界或需要对输出进行审查 |
| `allows_background=true` | 任务本体需要后台队列、定时触发、等待条件或异步投递 |

## 标签分布变化

| 指标 | 半自动标签 | 人工复核 v1 |
| --- | ---: | ---: |
| records | 75 | 75 |
| single | 63 | 51 |
| pipeline | 11 | 14 |
| autonomy_queue | 1 | 10 |
| should_split=true | 12 | 24 |
| needs_verifier=true | 0 | 40 |
| needs_critic=true | 0 | 4 |
| allows_background=true | 1 | 10 |
| review_required=true | 75 | 0 |
| avg_confidence | 0.5628 | 0.8275 |

主要变化是把 cron、每天/每两小时、autonomy trigger、等待文件入库、持续搜索投递等任务从 `single/pipeline` 修正为 `autonomy_queue`，并把真实搜索、RAG、文件、配置、图片和实时事实类任务补上 `needs_verifier=true`。

## Reviewed Replay 结果

| 指标 | 数值 |
| --- | ---: |
| records | 75 |
| success_rate | 1.0000 |
| split_accuracy | 1.0000 |
| mode_accuracy | 0.8667 |
| avg_subtask_recall | 1.0000 |
| avg_subtask_precision | 1.0000 |
| avg_capability_recall | 1.0000 |
| avg_capability_precision | 0.5894 |
| avg_route_risk | 0.1857 |
| avg_dependency_violations | 0.0000 |
| avg_speedup | 1.2870 |
| avg_parallel_efficiency | 0.7821 |
| avg_coordination_overhead | 0.1897 |
| avg_multi_agent_score | 0.9419 |
| avg_expected_utility | 8.9547 |
| avg_estimated_success | 0.9868 |
| avg_edge_nll | 0.0455 |
| calibration_ece | 0.1679 |
| avg_lyapunov_decrease | 2.4375 |
| lyapunov_decrease_rate | 1.0000 |
| replan_rate | 0.0000 |
| verifier_required_count | 50 |
| verifier_available_count | 50 |
| verifier_catch_rate | 0.5063 |
| verifier_need_accuracy | 0.6667 |
| ood_count | 0 |
| ood_rate | 0.0000 |
| ood_false_negative_rate | 0.0000 |
| avg_path_regret | 0.0000 |
| constraint_violation_count | 0 |
| forbidden_mode_count | 0 |
| dependency_violation_count | 0 |
| clean | true |

## 主要误判

人工复核后，`math-full-v1` 的 10 个 mode error 全部是同一类：gold 是 `autonomy_queue`，planner 预测为 `pipeline`。

| 数量 | predicted | gold | 任务类型 |
| ---: | --- | --- | --- |
| 10 | pipeline | autonomy_queue | 定时搜索、cron、autonomy heartbeat、等待文件/RAG 入库、后台投递 |

典型样本包括：

- `cron-agent-intel-2m`
- `cron-arxiv-agent-tech-2h`
- `cron-backnumber-daily-news`
- `1777475849941593705`：立刻触发 autonomy 调度
- `1777573578077201114`：等待文件并入库 RAG

## 解释

这说明当前数学编排器已经能识别“需要拆分”的任务，`split_accuracy=1.0`，但对“有序 pipeline”和“后台 autonomy queue”的边界建模不足。它把定时/等待/队列投递类任务当作普通 pipeline 处理，导致 `mode_accuracy=0.8667`。

从数学模型角度看，下一步应该给 `autonomy_queue` 增加更强的状态特征和边权项，例如：

- `scheduled_execution`：是否存在定时/周期触发。
- `background_delivery`：是否需要后台发送结果。
- `wait_condition`：是否需要等待外部文件、API 或队列状态。
- `session_nonblocking`：是否明确要求不阻塞当前会话。
- `queue_observability`：是否需要查询队列/heartbeat/runtime 状态。

这些特征应进入 Markov/SSP 的状态转移概率，也应进入 Lyapunov 势函数的任务剩余风险项。否则 planner 会倾向选择局部成本更低的 pipeline。

## 验证

`go test ./cmd/lh-multiagent-bench` 已通过。
