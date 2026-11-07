# LuckyAgent 上下文编排策略

公开 Agent 模块位于主仓 `agent/`，负责选取与排列上下文。共享配置、窗口裁剪和会话
存储位于 `luckyagent-runtime`；通用压缩器位于 `luckyagent-tools/compact`。
CLI/API/渠道组装位于 `luckyagent-app`。旧 `internal/*` 路径不再是当前源码入口。

## 源码入口

| 文件 | 责任 |
| --- | --- |
| `agent/context_planner.go` | 输入规范化、顺序、软预算、历史/记忆/RAG、缓存 |
| `agent/system_prompt.go` | SOUL、工具策略、skills、手册、项目文件加载 |
| `agent/context_projection.go` | 项目规则、摘要/工具证据投影、最终文本预算检查 |
| `agent/context_history_tool.go` | 当前会话归档/完整工具结果的分页读取 |
| `agent/compact_auto.go` | 自动压缩阈值、超窗触发、失败降级 |
| `agent/loop.go` | 工具结果追加与最终窗口裁剪 |
| `agent/durable_loop.go`, `agent/task_context.go` | 原任务、验收条件、执行账本、继续与验收 |
| runtime `bootstrap/helpers.go`, `contextx/` | 统一窗口与完整工具轮次选择 |

## 预算口径

`bootstrap.ContextTokensFor` 优先使用正数 `context.max_context_tokens`，其次旧
`max_tokens`，最后 8000。JSON 加载会迁移只有旧值的配置；显式 canonical 值优先。
`context_budget.reserved_ratio` 默认 0.25，因此默认可用输入为 6000。
`limits.max_tokens` 是独立输出上限。独立库 `contextx.DefaultWindowConfig()` 的
4096/1024 仍保留，但应用 bootstrap 不再拿它覆盖配置。

可用输入按现有配置比例形成软预算：

| 类别 | 默认占比 | 默认 token |
| --- | --- | --- |
| System | 15% | 900 |
| Memory | 10% | 600 |
| RAG | 20% | 1200 |
| History | 25% | 1500 |
| ToolResult | 30% | 1800 |

项目规则另有 `min(4096, max(128, available/3))` 的位置，默认 2000。这不是额外
窗口容量；所有类别仍受最终共同输入上限约束。类别的最小值和单独投影可能使候选
之和超过输入预算，由最终 fitter 决定保留组。固定指令/当前任务本身超窗时返回错误。

## 实际消息顺序

1. 核心 system：SOUL、工具/skill/记忆策略、运行手册、metadata、平台提示。
2. 独立 `[Project Context]`：当前工作目录向上找到的项目 `AGENTS.md`。
3. 会话历史：有界压缩摘要投影，加最近原始消息。
4. 记忆证据：Core、Working、Mid-term、Recent。
5. RAG 检索证据。
6. 当前请求的 skill 路由提示。
7. 当前附件说明和内容。
8. 当前用户消息。

历史与当前 query 无关，先于查询相关的记忆和 RAG，减少稳定前缀变化。历史只读取
最后压缩边界后的原始消息；归档摘要最多使用 History 软预算的约 2/3，剩余留给
近期消息。归档过多时给出索引和近期摘要。原始会话和已保存摘要不因投影而改写。
项目文件内容进入缓存 key，修改规则后不会继续复用旧的项目投影。

`RoutingText` 用于路由和检索；最终保留真实 `input.Message`，包括结构化内容。
附件缺失/不可读时不能从旧会话或工作区猜同名替代文件。memory/RAG/摘要是历史证据，
与当前用户指令冲突时以当前指令和最新执行事实为准。

## 最终选择

| 消息 | 优先级 |
| --- | --- |
| 核心 system、项目规则、当前用户请求/持久任务原目标 | Critical |
| 最新工具调用及其结果、Task State、Core Memory、RAG | High |
| 压缩归档/旧任务进度、Working/Mid-term Memory、普通历史工具轮次 | Normal |
| Recent Context、旧 Conversation Summary/Themes | Low |

默认选择 Critical → High → Normal → Low，同级先选较新的组，再恢复原顺序。
assistant tool-call 和它的全部匹配连续结果必须一起保留；裁剪时不完整调用和孤立
result 会被移除。最新工具组位于检索证据之后，因此同为 High 时优先入选。
Critical 不会被静默丢弃；实际文本超窗时在模型请求前报错。

项目规则使用独立的头尾文本裁剪；工具结果先按行去重复、进度和空行，再按路径、
错误、命令等标识符优先级压缩，最后在 ToolResult token 预算内投影。并行调用共享预算。
截短结果给出 `context_history_read action=result tool_call_id=...`。
该工具从宿主绑定会话读原结果（含 blob），或读取旧 compact 片段；它不接受模型
选择其他会话。参数、分页和执行验收说明见[长任务上下文](../long-task-context.md)。

## 观测和限制

`lh config set agent.context_debug true` 开启编排日志。`BuildContextPackerSnapshot`
及上下文 API 提供 messages、total_tokens、bucket_tokens、bucket_counts。
bucket 包含 `system/project/history/memory/rag/tool_result/user`，消费者应允许新增 key。

计数包含文本、reasoning、名称和调用参数，但不是供应商精确 tokenizer；不包含完整
工具 schema 和图像费用。结构化 ContentParts 仍跳过最终文本裁剪，电脑画面只保留
最新临时帧。项目规则保留头尾，工具结果按行优先级省略，模型需要读回原文件/证据。
验收依赖模型，确定性回归通过不代表所有模型的长任务成功率已提升。
