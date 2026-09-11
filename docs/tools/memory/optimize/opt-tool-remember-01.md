# opt-tool-remember-01

## 目标

优化 `remember` 的写入准入、敏感信息防护、去重、状态更新和输出结构，让它继续承担“保存稳定用户事实、偏好、项目规则和可复用结论”的职责，同时降低误存临时对话、密钥泄漏、重复记忆、状态冲突和自动批准写入带来的长期污染风险。

本方案聚焦：

- content 稳定性和长度校验
- 敏感信息拦截
- raw conversation 防污染
- 去重和更新策略
- links / aliases / tags 图元数据质量
- state_key 状态写入语义
- 权限和审批策略
- 测试矩阵补齐

## 当前状态

相关实现：

- `internal/tool/builtin_memory.go`
- `internal/tool/memory_service.go`
- `internal/memory/memory.go`
- `docs/tools/memory/remember.md`

当前 `remember` handler 流程：

1. 检查 memory store 是否初始化。
2. 读取 `content` 和 `category`。
3. `content` 为空时返回 `content is required`。
4. category 为空时通过关键词推断。
5. 根据 `long_term`、`tier`、`importance` 计算 tier 和 importance。
6. 解析 tags、links、aliases、supersedes、status、state_key、state_value、confidence。
7. 解析 `valid_from` 和 `valid_until`。
8. 调用 `store.SaveWithOptions` 写入 memory vault。
9. 返回中文确认。

当前优势：

- 参数覆盖了 category、tier、importance、temporal state 和 graph links。
- importance 会 clamp 到 0 到 1。
- 支持 `StateKey` / `StateValue` 表达会变化的状态。
- 支持 `links`、`aliases`，并会从 content 里的 `[[wikilink]]` 抽取图关系。
- store 层已有同 category + 同 content 去重。

## 主要问题

### 1. `PermAuto` 写入持久记忆风险偏高

`remember` 当前是 `PermAuto`，但它会写入 durable memory。自动批准适合低摩擦记忆，但也会带来：

- agent 误把临时对话保存成长期记忆。
- 保存未经用户确认的推断。
- 保存敏感信息。
- 长期影响后续回答和工具选择。

建议保留默认自动批准，但加入更严格的本地准入规则；对高风险内容升级为审批或拒绝。

### 2. 没有复用 hygiene 规则做写入前检查

`memory_hygiene` 能识别：

- raw conversation
- secret-like 内容
- prompt injection
- oversized 内容
- low confidence long-term

但 `remember` 写入前没有使用这些规则。结果是脏记忆可能先进入 vault，之后再靠 hygiene 清理。

建议在写入前增加 `ValidateMemoryContent`，直接拒绝明显脏内容。

### 3. content 缺少长度边界

当前只检查空字符串。过长 content 会导致：

- memory vault 膨胀。
- recall 输出噪声。
- hygiene 后续再标记 oversized。

建议限制默认单条记忆长度，例如 2000 或 4000 rune。超长内容应提示使用 RAG 或文档索引。

### 4. category 推断偏关键词，缺少显式反馈

category 推断是启发式的。调用方不知道最终为何被归类到某个 category。

建议输出结构中包含：

- `category`
- `category_inferred`
- `tier`
- `importance`
- `memory_id`

默认文本可以保持，但应支持 `format=json`。

### 5. 状态更新语义不够明确

当 `state_key` 相同但 `state_value` 不同时，当前写入会新增一条状态记忆。之后 hygiene 可能识别 `state_conflict`。

更好的做法是在写入阶段支持：

- `mode=append`
- `mode=upsert_state`
- `mode=supersede`

对于状态型记忆，默认应提示或自动 supersede 旧状态。

### 6. 去重反馈不足

store 层去重会避免同 category + 同 content 重复写入，但 tool 输出仍是普通“已保存”文案。调用方无法知道是新建、更新访问时间，还是命中重复。

建议 `SaveWithOptions` 返回 `SaveResult`，包括：

- `created`
- `updated_existing`
- `entry_id`
- `path`
- `duplicate_of`

### 7. tags / links / aliases 缺少规范化约束

当前支持数组和字符串拆分，但缺少：

- 最大数量。
- 单项长度。
- 控制字符过滤。
- 重复项去重反馈。

过多 tags / aliases 会降低召回质量。

### 8. 图元数据写入还不足以支撑可靠多跳查询

`remember` 当前能写入 `links`、`aliases`、`tags`，并会从 content 中抽取 `[[wikilink]]`。这能支撑 `recall` 的一跳图扩展，但要支持可靠多跳，还需要更严格的图元数据质量。

当前主要不足：

- link 只是字符串，没有类型，例如 `depends_on`、`belongs_to`、`contradicts`、`supersedes`。
- aliases 没有语言、来源或置信度。
- tag 容易过宽，shared tag 多跳扩展会带来噪声。
- 没有区分“内容提到某概念”和“这条记忆强依赖某概念”。
- 没有在写入时给多跳查询准备反向路径解释。

如果未来 `recall` 支持 `graph_depth=2/3`，`remember` 必须先保证写进去的边质量足够高，否则多跳会把弱相关记忆一起带出。

## 优化原则

1. `remember` 只保存稳定、可复用、未来有价值的信息。
2. 明显敏感、注入、原始对话内容应在写入前拒绝。
3. 默认文本输出兼容，结构化输出可选。
4. 状态型记忆应支持更新旧状态，而不是制造冲突。
5. 不把 RAG 文档内容塞进 durable memory。
6. links / aliases / tags 是后续图召回的基础，写入时应控制数量、类型和噪声。

## 推荐方案

### 1. 增加写入前校验

新增：

```go
type memoryValidationResult struct {
	Allowed bool
	Reason  string
	Severity string
	Action  string
}

func validateMemoryForSave(content string, opts memory.SaveOptions, tier memory.Tier) memoryValidationResult
```

规则：

- 空 content 拒绝。
- raw conversation 拒绝，提示保存总结而不是原文。
- secret-like 拒绝。
- prompt injection 拒绝或要求审批。
- 超过长度上限拒绝。
- long-term + low confidence 拒绝或降级为 medium。

### 2. 增加长度限制

建议常量：

```go
const maxRememberContentRunes = 4000
```

超过限制时返回：

```text
content exceeds 4000 rune limit; summarize it or index the source with rag_index
```

### 3. 增加 `format=json`

新增参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `format` | 否 | `text` | 输出格式：`text` 或 `json`。 |

JSON 输出示例：

```json
{
  "saved": true,
  "created": true,
  "id": "abc123",
  "path": "10_Project/luckyagent.md",
  "category": "project",
  "category_inferred": false,
  "tier": "long",
  "importance": 0.9
}
```

### 4. 引入 SaveResult

将 store 写入接口扩展为：

```go
type SaveResult struct {
	ID              string
	Path            string
	Created         bool
	UpdatedExisting bool
	DuplicateOf     string
	Superseded       []string
}
```

保留原 `SaveWithOptions` 兼容包装，新增 `SaveWithOptionsResult` 给 tool 使用。

### 5. 状态型记忆支持 upsert

新增参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `mode` | 否 | `append` | 写入模式：`append`、`upsert_state`、`supersede`。 |

行为：

- `append`：保持现状。
- `upsert_state`：相同 `state_key` 的 active 旧记忆自动 supersede 或 archive。
- `supersede`：必须提供 `supersedes`。

### 6. 标签和链接规范化

建议限制：

- tags 最多 20 个。
- links 最多 20 个。
- aliases 最多 20 个。
- 单项最多 80 字符。
- 去除空项和重复项。
- 拒绝控制字符。

### 7. 增加 typed links

新增可选参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `typed_links` | 否 | 无 | 带关系类型的图边。 |

示例：

```json
{
  "typed_links": [
    {"target": "LuckyAgent", "type": "project"},
    {"target": "memory_hygiene", "type": "tool"},
    {"target": "recall", "type": "related_tool"}
  ]
}
```

建议内置关系类型：

| type | 含义 |
| --- | --- |
| `mentions` | 内容提到该概念，弱关系。 |
| `depends_on` | 该记忆依赖目标概念。 |
| `belongs_to` | 归属于目标项目、主题或实体。 |
| `related_tool` | 和某个工具相关。 |
| `supersedes` | 替代旧记忆。 |
| `contradicts` | 与目标记忆冲突。 |

typed links 可以先落到 metadata 中，不必马上改变 Markdown 展示格式。后续 graph index 可按 type 赋不同权重。

### 8. 区分强链接和弱链接

当前从 `[[wikilink]]` 抽出的 links 适合做弱关系。建议新增：

```go
type GraphEdge struct {
	Target string
	Type   string
	Weight float64
	Source string
}
```

默认权重：

- 显式 `typed_links`: 0.8 到 1.0。
- 参数 `links`: 0.6。
- content 自动抽取 wikilink: 0.45。
- shared tag: 0.18。

这样多跳查询时可以优先沿强边扩展，避免 tag 噪声。

### 9. graph 写入预览

新增 `format=json` 后，保存结果应返回图元数据摘要：

```json
{
  "saved": true,
  "id": "abc123",
  "links": ["LuckyAgent", "recall"],
  "aliases": ["记忆召回"],
  "typed_links": [
    {"target": "recall", "type": "related_tool", "weight": 0.9}
  ],
  "graph_warnings": []
}
```

如果 tag 太泛，例如 `project`、`note`、`todo`，可以在 `graph_warnings` 中提示其多跳召回价值低。

### 10. 权限分级

保持 tool 注册为 `PermAuto`，但 handler 内部对高风险写入返回错误：

- secret-like：直接拒绝。
- raw conversation：直接拒绝。
- long-term + 推断内容：建议用户确认。
- status=`conflict`：建议审批或显式参数。

如果工具系统后续支持动态审批，可以把高风险写入升级为 approve。

## 分阶段实施

### 第一阶段：污染拦截

- 写入前复用 hygiene 正则。
- 增加 content 长度限制。
- 拒绝 raw conversation、secret-like、prompt injection。
- 补齐测试。

### 第二阶段：结果结构化

- 增加 `format=json`。
- 引入 `SaveResult`。
- 输出是否新建或命中重复。
- 输出 links / aliases / graph warnings。

### 第三阶段：状态更新语义

- 增加 `mode`。
- 支持 `upsert_state`。
- 对 `state_key` 冲突进行写入期处理。

### 第四阶段：图质量增强

- 增加 `typed_links`。
- 为 links / tags / aliases 建立边权重。
- graph index 支持按 edge type 加权。
- 为 `recall` 多跳查询提供可解释路径。

## 测试建议

- content 为空时报错。
- raw conversation 被拒绝。
- secret-like 内容被拒绝。
- prompt injection 内容被拒绝。
- 超长 content 被拒绝。
- category 为空时仍可推断。
- 显式 tier 覆盖 `long_term`。
- importance 被 clamp 到 0 到 1。
- `format=json` 返回 entry id、path、tier。
- 重复记忆返回 `updated_existing=true`。
- `upsert_state` 会 supersede 旧状态。
- content 中 `[[wikilink]]` 会进入 links。
- links / aliases / tags 数量超过上限时报错。
- `typed_links` 会保存 target、type、weight。
- 泛化 tag 会产生 graph warning。

## 文档更新

同步更新：

- `docs/tools/memory/remember.md`
- 参数表新增 `format`、`mode`
- 参数表新增 `typed_links`
- 写入前拒绝规则
- content 长度限制
- 状态型记忆建议
- links / aliases / tags 对 recall 图扩展的影响
- typed links 示例
- remember 和 rag_index 的边界

## 风险与边界

- 写入前拒绝规则可能影响旧调用方，需要错误信息可操作。
- secret-like 正则可能误报，但 durable memory 中误存密钥风险更高。
- `upsert_state` 涉及历史状态保留，默认不要物理删除旧状态。
- SaveResult 需要兼容现有 store API。
- typed links 会扩展 memory schema，需要兼容旧 Markdown frontmatter。
- 多跳图召回质量取决于写入时边质量，不能只在 recall 侧补救。

## 推荐结论

优先做写入前污染拦截和长度限制。`remember` 是持久记忆入口，防止脏数据进入比事后 hygiene 清理更重要。随后补结构化输出和状态 upsert，让 agent 能明确知道记忆是新建、更新还是替代旧状态。多跳 recall 要可靠，必须同步提升 `remember` 写入的 links / aliases / typed links 质量，否则图扩展会放大噪声。
