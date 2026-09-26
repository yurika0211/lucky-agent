# Drift 控制规范

Drift 是 LuckyAgent 长期运行后，运行时事实、记忆、配置、索引和 Agent 行为逐渐偏离当前真实状态或预期规范的现象。

本规范用于约束 drift 的识别、自动处理、人工确认和测试要求。任何会影响上下文、记忆、RAG、工具执行、配置 schema 或多 Agent 状态汇总的改动，都应按本文检查。

## 目标

- 防止过期、冲突或污染的记忆被当成硬事实注入上下文。
- 防止工具结果截断、摘要压缩或 RAG 召回造成事实偏移。
- 防止配置、示例配置、CLI get/set、运行时结构之间出现 schema drift。
- 防止主 Agent、delegate task、autonomy worker 对任务状态做未验证总结。

## Drift 分类

| 类型 | 定义 | 典型风险 | 优先事实源 |
| --- | --- | --- | --- |
| Memory drift | durable memory 与当前事实、用户偏好、项目状态不一致 | 旧记忆被当作硬约束 | memory vault 原始 note + 当前工具检查 |
| Context drift | 会话摘要、工具摘要、截断结果改变原始证据含义 | 模型基于残缺证据补全 | 原始最近消息 + 工具原始输出 |
| RAG drift | 索引内容陈旧、弱相关、来源混入生成答案 | 检索结果被误当权威资料 | 当前文件/网页/数据库状态 |
| Config drift | `config.example.json`、Go config struct、CLI get/set、实际 config 不一致 | 用户设置看似成功但运行时不生效 | Go config struct + CLI get/set 测试 |
| Behavior drift | Agent 路由、工具选择、多 Agent 状态解释偏离设计 | 把已派发当已完成，把失败当成功 | tool result、task status、runtime logs |

## 强制规则

### 1. 记忆进入上下文前必须可清洗

当启用 `context.memory_hygiene_before_context` 时，Agent 在构造上下文前必须运行 memory hygiene。

默认建议：

```json
{
  "context": {
    "memory_hygiene_before_context": true,
    "memory_hygiene_action": "quarantine",
    "memory_hygiene_min_severity": "high",
    "memory_hygiene_max_findings": 25
  }
}
```

要求：

- 自动路径默认使用 `quarantine`，不得默认物理删除。
- `delete` 只能在用户明确要求或维护任务中使用。
- high/critical 级别 dirty memory 不得继续进入 Working Memory。
- hygiene 日志只能记录数量、action、severity，不得输出完整敏感记忆内容。

示例：记忆中存在 `User: ... Assistant: ...` 原始对话片段时，应隔离为 dirty memory，而不是注入 `[Must Use Facts]`。

### 2. 摘要不能变成 durable fact

LLM 生成的会话压缩摘要只能作为中期上下文辅助材料，不得自动写入 durable memory。

要求：

- 摘要必须标记为非权威。
- 摘要和当前 workspace、工具输出、最近原始消息冲突时，必须以后者为准。
- 摘要不得被保存为 `context_compression` 类长期/中期 memory fact。

反例：把 “Assistant progress: 已修复配置” 存成 durable memory，后续 recall 直接当成已修复事实。

### 3. 工具结果截断必须保留可追溯性

工具结果过长时允许截断，但必须保留头尾，并明确标记中间省略。

要求：

- 不得只保留开头导致尾部错误、测试失败、最终状态丢失。
- 截断标记必须提醒模型：省略部分不可当作已验证细节。
- 涉及错误诊断、测试输出、网页证据时，如果缺失片段影响判断，必须再次取证。

示例：测试输出开头是构建过程，尾部才是失败断言；截断后必须保留尾部失败信息。

### 4. RAG 不是权威源

RAG 只表示“检索到的索引材料”，不是当前事实。

要求：

- RAG 与 workspace 文件冲突时，以当前文件为准。
- RAG 与 runtime/log/db 状态冲突时，以实时工具检查为准。
- RAG 召回低分或标题/来源不明确时，回答必须降低确定性或继续验证。
- 生成的 final answer 默认不得重新进入 RAG，除非显式开启并能区分 generated source。

### 5. 配置 schema 必须同步

新增配置项时必须同时检查：

- Go config struct。
- default config。
- normalize/default fallback。
- CLI `config set`。
- CLI `config get`。
- `config.example.json`。
- 相关测试。

示例：新增 `context.memory_hygiene_before_context` 时，如果只加 struct，不加 CLI get/set，用户会看到“配置已写入但不可查询”的 drift。

### 6. 多 Agent 状态不得推断

delegate task、autonomy worker、cron/heartbeat 任务状态只能来自实际状态工具或运行时记录。

要求：

- `running` 只能说已派发/执行中，不能说已完成。
- 总结 worker 结果前必须查 `task_status`、`list_tasks`、`autonomy_status` 或对应 report。
- 占位实现、模拟执行和真实执行必须在输出中区分。

## 严重级别

| 级别 | 标准 | 默认处理 |
| --- | --- | --- |
| Critical | secret、token、prompt injection、会导致越权或泄密 | quarantine，必要时人工删除 |
| High | 原始对话污染、状态冲突、强约束错误 | quarantine |
| Medium | duplicate、expired、低置信 long-term memory | audit 或维护任务 quarantine/delete |
| Low | oversized、弱相关、格式不规范 | audit，批量维护时处理 |

## 测试要求

涉及 drift 控制的改动至少覆盖对应测试：

- Memory drift：dirty memory 不进入 recall/context。
- Context drift：LLM summary 不写 durable memory。
- Tool-result drift：截断保留头尾，并包含省略标记。
- Config drift：新增字段能被 default、normalize、CLI get/set 覆盖。
- Behavior drift：delegate/autonomy 总结必须基于状态查询结果。

建议命令：

```bash
go test ./internal/agent ./internal/memory ./internal/config ./internal/cli/lhcmd
go test ./...
```

## 变更评审清单

提交前检查：

- 是否有新上下文来源进入 system/user/tool message？
- 是否有模型生成内容被持久化为事实？
- 是否有工具输出被摘要、截断或重排？
- 是否有新配置字段但缺少 example/get/set/test？
- 是否有后台任务状态被自然语言推断？
- 是否有 dirty memory 可以绕过 hygiene 进入 Working Memory？

任一答案为“是”，必须补检测、降权、隔离或测试。
