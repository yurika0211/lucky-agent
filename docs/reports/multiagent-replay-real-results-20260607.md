# Multi-Agent Real Replay Results - 2026-06-07

## 结论

本轮已经用本机 LuckyHarness 真实会话数据跑通 replay。数据来自 `/home/shiokou/.luckyharness/sessions`，共导出 75 个真实 session case，并使用 `math-full-v1` 编排器完成一轮回放。

结果整体是 clean pass，但当前标签是半自动启发式标签，全部样本都标记为 `review_required=true`，因此本结果可以作为工程回放 smoke test 和初步趋势，不应直接作为最终人工标注实验结论。

## 输入与输出

- 真实数据源：`/home/shiokou/.luckyharness/sessions`
- 标签导出：`docs/reports/multiagent-replay-labels-real-20260607.jsonl`
- 回放结果：`docs/reports/multiagent-replay-math-full-real-20260607.jsonl`
- 编排器版本：`math-full-v1`
- 场景：`replay`
- 轮数：1

## 标签分布

| 指标 | 分布 |
| --- | --- |
| records | 75 |
| gold_mode | single=63, pipeline=11, autonomy_queue=1 |
| should_split | false=63, true=12 |
| needs_verifier | false=75 |
| needs_critic | false=75 |
| allows_background | false=74, true=1 |
| review_required | true=75 |
| avg_confidence | 0.5628 |

标签集明显偏向 single-agent 类型，复杂多 agent 样本较少。后续要评估泛化性，需要补充人工标注后的复杂任务、长链任务、跨工具任务和高风险验收任务。

## 回放汇总

| 指标 | 数值 |
| --- | ---: |
| records | 75 |
| success_rate | 1.0000 |
| split_accuracy | 1.0000 |
| mode_accuracy | 1.0000 |
| avg_subtask_recall | 1.0000 |
| avg_subtask_precision | 1.0000 |
| avg_capability_recall | 1.0000 |
| avg_capability_precision | 0.5962 |
| avg_route_risk | 0.0097 |
| avg_dependency_violations | 0.0000 |
| avg_speedup | 1.2889 |
| avg_parallel_efficiency | 0.8907 |
| avg_coordination_overhead | 0.1618 |
| avg_multi_agent_score | 0.9764 |
| avg_expected_utility | 9.0188 |
| avg_estimated_success | 0.9786 |
| avg_edge_nll | 0.0481 |
| calibration_ece | 0.0468 |
| avg_lyapunov_decrease | 2.2304 |
| lyapunov_decrease_rate | 1.0000 |
| replan_rate | 0.0000 |
| verifier_required_count | 36 |
| verifier_available_count | 36 |
| verifier_catch_rate | 0.3156 |
| verifier_need_accuracy | 0.4800 |
| ood_count | 0 |
| ood_rate | 0.0000 |
| ood_false_negative_rate | 0.0000 |
| avg_path_regret | 0.0000 |
| constraint_violation_count | 0 |
| forbidden_mode_count | 0 |
| dependency_violation_count | 0 |
| clean | true |

## 使用到的模式与代理

- used_modes：`single`, `pipeline`, `autonomy_queue`
- unique_assigned_agents：`repo-agent`, `backend-agent`, `data-agent`, `docs-agent`, `ops-agent`, `test-agent`

## 解释

`math-full-v1` 在真实 session replay 中没有出现 forbidden mode、约束违规或依赖违规。Lyapunov 势函数在所有样本上都下降，说明当前编排路径满足“任务状态朝验收态收敛”的工程约束。

但 verifier 相关指标显示模型与标签之间还不一致：标签导出阶段所有样本的 `needs_verifier=false`，而编排器内部有 36 个样本触发了 verifier-required 判断，导致 `verifier_need_accuracy=0.48`。这不是运行失败，更像是标签体系还太粗，需要把“轻量检查”“基础 debug”“真实场景验收”拆成更细的 gold label。

## 下一步

1. 人工复核 `review_required=true` 的 75 条标签，优先确认 12 条 `should_split=true` 样本。
2. 扩充真实复杂任务集，增加 Hermes-like reproduction、跨仓库开发、长链调研、浏览器自动化、失败后 debug 和真实验收任务。
3. 把 verifier 标签从单一布尔值升级为 `none/basic_debug/acceptance/runtime_validation`。
4. 在人工标签稳定后，重跑 `math-mdp-v1`、`math-ssp-v1`、`math-lyapunov-v1`、`math-verifier-v1` 和 `math-full-v1` 的对照实验。
