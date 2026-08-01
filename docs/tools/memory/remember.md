# remember Tool

`remember` 是 LuckyAgent 的内置记忆写入工具，用来把稳定的用户事实、偏好、长期项目约束或可复用结论保存到 LuckyAgent 的 Obsidian-compatible Markdown memory vault。

这是对持久记忆库的写入工具，但当前注册权限是自动批准。使用时仍应只保存明确、稳定、值得之后召回的信息。

## 工具定义

实现位置：

- `internal/tool/builtin_memory.go`
- `internal/tool/memory_service.go`

注册信息：

```go
Name:         "remember"
Category:     CatBuiltin
Source:       "builtin"
Permission:   PermAuto
ParallelSafe: false
```

## 参数

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `content` | 是 | 无 | 要保存的稳定事实或可复用笔记。 |
| `category` | 否 | 自动推断 | 分类，例如 `identity`、`preference`、`project`、`health`、`rule`。 |
| `tier` | 否 | `medium` | 记忆层级：`short`、`medium`、`long`。提供后覆盖 `long_term` 推导。 |
| `importance` | 否 | 按 tier 推导 | 重要性，0.0 到 1.0。 |
| `tags` | 否 | 无 | 标签数组，或逗号/中文逗号/分号/换行分隔字符串。 |
| `links` | 否 | 无 | Obsidian wikilink 目标数组。 |
| `aliases` | 否 | 无 | 可用于召回的别名数组。 |
| `status` | 否 | 空字符串 | 时间状态，例如 `active`、`superseded`、`archived`、`conflict`。 |
| `state_key` | 否 | 无 | 可随时间变化的稳定状态键。 |
| `state_value` | 否 | 无 | `state_key` 的当前值。 |
| `confidence` | 否 | 无 | 状态置信度，0.0 到 1.0。 |
| `supersedes` | 否 | 无 | 被这条记忆替代的 memory ID 列表。 |
| `valid_from` | 否 | 无 | 生效日期，支持 RFC3339 或 `YYYY-MM-DD`。 |
| `valid_until` | 否 | 无 | 失效日期，支持 RFC3339 或 `YYYY-MM-DD`。 |
| `route_policies` | 否 | 无 | 类型化路由策略的 JSON 对象或数组；可声明 query/state 匹配、风险、结构化 required tool calls、约束和澄清项。 |
| `long_term` | 否 | `false` | 为 true 时默认写成长长期记忆，重要性设为 0.9。 |

## 执行流程

`remember` 的执行过程是：

1. 检查 memory store 是否已初始化。
2. 读取必填参数 `content`。
3. 如果 `content` 为空，返回 `content is required`。
4. 读取 `category`；为空时根据内容关键词自动推断。
5. 默认 tier 为 `medium`，importance 为 `0.5`。
6. 如果 `long_term=true`，tier 改为 `long`，importance 改为 `0.9`。
7. 如果显式传入 `tier`，使用 `parseMemoryToolTier` 解析。
8. 如果显式传入 `importance`，使用 `clamp01` 限制到 0 到 1。
9. 解析 tags、links、aliases、supersedes。
10. 解析 temporal 字段、状态字段和 `route_policies` JSON。
11. 校验策略 ID、query/state matcher、风险和结构化 tool calls。
12. 调用 `store.SaveWithOptions` 写入 Markdown memory vault。
13. 返回中文保存确认信息。

如果 handler 未配置，默认错误是：

```text
remember handler not configured
```

如果 store 未初始化，返回：

```text
memory store not initialized
```

## category 推断

未提供 `category` 时，会按关键词推断：

| 分类 | 关键词示例 |
| --- | --- |
| `preference` | 喜欢、偏好、prefer、like、讨厌 |
| `project` | 项目、project、代码、bug、部署、repo |
| `health` | 过敏、花粉、健康、allergy、pollen |
| `rule` | 必须、应该、工具、tool、rule、workflow |
| `location` | 城市、地点、位置、location、city |
| `knowledge` | 什么是、如何、what is、how to、解释、调研 |
| `identity` | 我叫、我是、my name、学校、公司 |
| `conversation` | 默认分类 |

## tier 和 importance

`tier` 解析规则：

| 输入 | tier |
| --- | --- |
| `short`, `短期` | short |
| `long`, `长期` | long |
| 其他或空 | medium |

如果没有显式传入 `importance`，默认重要性按 tier 推导：

| tier | importance |
| --- | --- |
| short | `0.25` |
| medium | `0.5` |
| long | `0.9` |

显式传入的 `importance` 会被限制在 0 到 1。

## 时间字段

`valid_from` 和 `valid_until` 支持：

- RFC3339，例如 `2026-07-03T12:00:00+08:00`
- 日期，例如 `2026-07-03`

格式不合法时返回：

```text
invalid time "<value>": use RFC3339 or YYYY-MM-DD
```

## 输出格式

成功时输出中文确认：

```text
✅ 已保存为中期记忆 [preference] 到 LuckyAgent Markdown 记忆库 /home/user/.luckyagent/memory: 用户喜欢Python
```

其中：

- 记忆层级会显示为 `短期`、`中期`、`长期`。
- content 会被截断到 80 字符。
- vault 路径来自 `store.Dir()`；为空时显示 `~/.luckyagent/memory`。

## 适合使用的场景

优先使用 `remember` 的场景：

- 用户明确要求“记住”。
- 用户提供长期偏好或项目规则。
- 当前任务中得出可复用结论。
- 保存不会快速过期的事实。
- 保存之后会影响未来工具选择或回答方式的约束。

示例：

```json
{
  "content": "用户在 LuckyAgent 项目里要求文档保持中文、实现驱动、不要泛泛而谈。",
  "category": "project",
  "tier": "long",
  "importance": 0.9,
  "tags": ["luckyagent", "docs"]
}
```

## 不适合使用的场景

不优先使用 `remember` 的场景：

- 临时聊天内容。
- 用户没有要求记住，也不是稳定事实。
- 密码、token、隐私敏感内容。
- 会很快过期的状态。
- 已经存在且没有变化的重复事实。

## 和 RAG 的关系

`remember` 写入的是 LuckyAgent Markdown memory vault，是 durable memory 的事实源。

RAG SQLite 是索引知识库，不等同于 durable memory。需要保存用户偏好、身份、规则和长期项目事实时，应使用 `remember`，不是 `rag_index`。

## 维护注意事项

如果后续修改 `remember`，需要同步检查：

- 参数表是否仍与 `RememberTool()` 一致。
- 权限是否仍是 `PermAuto`。
- category 推断关键词是否变化。
- tier 和 importance 默认逻辑是否变化。
- 时间格式是否仍只支持 RFC3339 和 `YYYY-MM-DD`。
- 输出确认文案是否变化。
- memory vault 路径说明是否仍准确。
