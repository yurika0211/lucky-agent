# opt-tool-recall-01

## 目标

优化 `recall` 的查询边界、结果排序、结构化输出、过滤能力和污染防护，让它继续保持“只读召回 durable memory”的定位，同时降低召回噪声、过期记忆混入、无法追溯引用和结果不可机器消费的问题。

本方案聚焦：

- query 参数校验
- limit / category / tier 过滤
- active 状态和 temporal 过滤
- 多跳图查询能力
- 结果排序解释
- JSON 输出
- 与 RAG 搜索边界
- 测试矩阵补齐

## 当前状态

相关实现：

- `internal/tool/builtin_memory.go`
- `internal/tool/memory_service.go`
- `internal/memory/memory.go`
- `docs/tools/memory/recall.md`

当前 `recall` handler 流程：

1. 检查 memory store 是否初始化。
2. 读取 `query`。
3. query 为空时返回 `store.Recent(5)`。
4. query 非空时调用 `store.Search(query)`。
5. 无结果时返回中文无结果消息。
6. 有结果时最多格式化前 10 条。
7. 输出 memory source notice，说明 RAG SQLite 不是 durable memory source。

当前优势：

- 是只读工具，适合 `PermAuto`。
- 输出明确区分 memory vault 和 RAG。
- 搜索结果包含 category、tier、links、path、block id。
- query 为空可查看最近记忆。
- `store.Search` 默认启用一跳图扩展，可通过 wikilink、backlink、alias backlink 和 shared tag 拉起邻近记忆。

## 主要问题

### 1. 参数能力过窄

当前只有 `query`。实际召回常需要：

- 限制返回数量。
- 只查某个 category。
- 只查 long-term。
- 包含或排除 archived / expired。
- 返回 JSON 供 agent 后续处理。

缺少这些参数会导致 agent 只能拿一批文本结果再自行判断。

### 2. query 空字符串返回最近 5 条，可能暴露不相关记忆

query 为空时返回 recent 是有用的调试能力，但在自动工具选择中可能带来噪声。

建议增加：

- `mode=recent|search`
- query 为空时默认 recent 保持兼容。
- 未来 agent 自动调用时优先要求 query 非空。

### 3. 输出文本不利于后续链路解析

当前输出是自然语言文本。Agent 可以读，但程序不容易稳定解析：

- id
- path
- block id
- category
- tier
- score / weight
- status
- valid_from / valid_until
- confidence

建议增加 `format=json`。

### 4. 结果排序不可解释

`store.Search` 内部有权重和匹配逻辑，但 tool 输出没有展示 score 或排序依据。调用方难以判断哪条更可信。

建议输出：

- score 或 weight。
- match fields。
- temporal note。
- conflict refs。

至少 JSON 模式应返回这些字段。

### 5. active / expired 语义没有显式参数

默认 store 搜索一般应排除 inactive，但 tool 层没有可见参数说明。排查记忆污染时，用户可能需要包含 archived 或 expired。

建议新增：

- `include_inactive`
- `include_expired`
- `as_of`

### 6. 缺少查询长度限制

query 当前没有长度上限。超长 query 会增加搜索成本，并污染输出标题。

建议限制 query 长度，例如 1000 rune。

### 7. 当前不是完整多跳记忆查询

当前 `recall` 调用：

```go
store.Search(query)
```

而 `Store.Search` 使用：

```go
DefaultActivationOptions()
```

默认图扩展参数是：

```go
IncludeGraph:  true
MaxGraphDepth: 1
MaxGraphBoost: 0.45
MaxGraphSeeds: 12
```

`activation.go` 中也明确说明 `MaxGraphDepth` 当前被限制为 shallow one-hop graph spread。实际行为是：

```text
query 直接命中 A
A 通过 link/backlink/alias/shared tag 拉起 B
```

但不会继续递归：

```text
A -> B -> C -> D
```

这意味着当前能力更准确地说是“一跳图传播召回”，不是多跳路径查询。对于“从用户偏好 -> 项目 -> 工具规则 -> 具体文件约束”这类链式问题，当前 recall 可能只能召回中间一层，不能保证沿路径完整展开。

## 优化原则

1. `recall` 只读，不修改 memory vault。
2. 默认只返回 active、当前有效记忆。
3. 文本输出保持人类可读，JSON 输出给 agent 和 API。
4. 不用 `recall` 查 RAG 文档。
5. 查询为空的 recent 模式保留，但应明确是调试/浏览行为。
6. 多跳扩展必须有深度、数量、分数衰减和路径证据限制，避免图噪声放大。

## 推荐方案

### 1. 增加 recall options

新增参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `limit` | 否 | query 为空 5，query 非空 10 | 最大返回数量。 |
| `category` | 否 | 无 | 只返回指定 category。 |
| `tier` | 否 | 无 | 只返回指定 tier。 |
| `include_inactive` | 否 | `false` | 是否包含 archived、superseded、conflict。 |
| `include_expired` | 否 | `false` | 是否包含已过期记忆。 |
| `format` | 否 | `text` | 输出 `text` 或 `json`。 |
| `as_of` | 否 | 当前时间 | 按指定时间判断 temporal validity。 |
| `graph_depth` | 否 | `1` | 图扩展深度。默认保持一跳，最大建议 3。 |
| `graph_max_paths` | 否 | `20` | 最多返回或考虑多少条图路径。 |
| `explain_graph` | 否 | `false` | 是否输出图扩展路径证据。 |

内部结构：

```go
type recallOptions struct {
	Query           string
	Limit           int
	Category        string
	Tier            memory.Tier
	IncludeInactive bool
	IncludeExpired  bool
	Format          string
	AsOf            time.Time
	GraphDepth      int
	GraphMaxPaths   int
	ExplainGraph    bool
}
```

### 2. query 长度限制

建议常量：

```go
const maxRecallQueryRunes = 1000
```

query 超长时报错：

```text
query exceeds 1000 rune limit
```

### 3. 增加 SearchOptions

store 层新增：

```go
type SearchOptions struct {
	Limit           int
	Category        string
	Tier            *Tier
	IncludeInactive bool
	IncludeExpired  bool
	AsOf            time.Time
}

func (s *Store) SearchWithOptions(query string, opts SearchOptions) []SearchResult
```

保留 `Search(query)` 作为兼容 wrapper。

### 4. 增加 bounded multi-hop graph search

新增 store 层 API：

```go
type GraphSearchOptions struct {
	Depth      int
	MaxSeeds   int
	MaxPaths   int
	MaxResults int
	MinScore   float64
	Decay      float64
}

type GraphPath struct {
	FromID string `json:"from_id"`
	ToID   string `json:"to_id"`
	Via    string `json:"via"`
	Kind   string `json:"kind"`
	Depth  int    `json:"depth"`
	Weight float64 `json:"weight"`
}

type SearchResult struct {
	Entry Entry
	Score float64
	DirectScore float64
	GraphScore float64
	Paths []GraphPath
}
```

执行策略：

1. 先做直接 activation，得到 seeds。
2. 只从 top seeds 出发做 BFS。
3. 最大深度默认 1，允许显式调到 2 或 3。
4. 每跳分数衰减，例如 `score *= decay`，默认 `0.55`。
5. 对每个结果限制累计 graph boost。
6. 记录路径证据，输出为什么被召回。
7. 遇到 inactive / expired / concept-only entry 默认不扩展。

示例路径：

```text
A --wikilink: Daughter--> B --backlink: Pollen Allergy--> C
```

这能支持受控的多跳查询，但不会无限沿图扩散。

### 5. 多跳查询参数边界

建议常量：

```go
const (
	defaultRecallGraphDepth = 1
	maxRecallGraphDepth     = 3
	defaultRecallMaxPaths   = 20
	maxRecallMaxPaths       = 100
)
```

规则：

- 默认保持当前一跳行为。
- `graph_depth=0` 表示关闭图扩展。
- `graph_depth>3` 报错。
- 多跳只在 query 非空时启用。
- `explain_graph=true` 时输出 paths；否则只用于排序。

### 6. JSON 输出

示例：

```json
{
  "source": "/home/user/.luckyagent/memory",
  "query": "LuckyAgent 文档风格",
  "graph_depth": 2,
  "count": 1,
  "results": [
    {
      "id": "abc123",
      "category": "project",
      "tier": "long",
      "content": "用户要求 LuckyAgent 文档保持中文...",
      "path": "10_Project/luckyagent.md",
      "block_id": "abc123",
      "links": ["LuckyAgent"],
      "status": "active",
      "confidence": 0.9,
      "score": 0.87,
      "direct_score": 0.42,
      "graph_score": 0.45,
      "paths": [
        {
          "from_id": "seed1",
          "to_id": "abc123",
          "via": "LuckyAgent",
          "kind": "wikilink_target",
          "depth": 1,
          "weight": 0.55
        }
      ]
    }
  ]
}
```

### 7. 输出排序信号

文本模式可轻量补充：

```text
- [project/long score=0.87 @path#block] ...
```

如果不想改变文本格式，至少在 JSON 模式提供 `score`。

### 8. recent 模式显式化

新增：

```json
{
  "mode": "recent",
  "limit": 5
}
```

兼容逻辑：

- `query=""` 且 `mode` 为空时走 recent。
- `mode=search` 且 query 为空时报错。

## 分阶段实施

### 第一阶段：参数和输出

- 增加 `limit`。
- 增加 query 长度限制。
- 增加 `format=json`。
- 文档明确 recent 行为。

### 第二阶段：过滤能力

- 增加 category / tier 过滤。
- 增加 include_inactive / include_expired。
- store 层新增 `SearchWithOptions`。

### 第三阶段：一跳解释能力

- 暴露 score / weight。
- 输出 temporal validity 信息。
- 对 conflict 结果加明显标记。
- `explain_graph=true` 时输出当前一跳 paths。

### 第四阶段：受控多跳查询

- 实现 bounded BFS graph expansion。
- 增加 `graph_depth` 和 `graph_max_paths`。
- 增加分数衰减和路径数量限制。
- 补充多跳路径测试和噪声回归测试。

## 测试建议

- store 未初始化时报错。
- query 为空返回 recent。
- `mode=search` 且 query 为空时报错。
- query 超长时报错。
- `limit` 生效且有上限。
- category 过滤生效。
- tier 过滤生效。
- 默认不返回 archived / expired。
- `include_inactive=true` 返回 archived。
- `format=json` 返回稳定字段。
- 文本输出仍包含 memory source notice。
- 默认 `graph_depth=1` 保持当前一跳召回。
- `graph_depth=0` 不返回纯图扩展结果。
- `graph_depth=2` 能召回 A -> B -> C 的 C。
- `graph_depth>3` 报错。
- 多跳结果包含 paths。
- 图扩展不会包含 inactive / expired entry。
- shared tag 多跳不会无限扩散。

## 文档更新

同步更新：

- `docs/tools/memory/recall.md`
- 参数表新增 limit、category、tier、format 等
- 参数表新增 `graph_depth`、`graph_max_paths`、`explain_graph`
- recent/search 模式区别
- JSON 输出示例
- 一跳召回和多跳查询边界
- recall 和 rag_search 的边界

## 风险与边界

- 文本输出增加 score 可能影响已有断言，应优先把 score 放入 JSON。
- `SearchWithOptions` 需要避免复制过多 store 搜索逻辑。
- include_inactive 不应成为默认值，否则会重新引入污染记忆。
- 多跳图扩展容易放大 shared tag 噪声，必须限制 depth、paths、seeds 和 score decay。
- 多跳查询不能替代 RAG；长文档证据仍应走 `rag_search`。

## 推荐结论

优先补 `limit`、query 长度限制和 `format=json`。然后把过滤能力下沉到 store 层，避免 tool handler 自己过滤导致排序和 temporal 逻辑不一致。多跳记忆查询应作为后续独立阶段实现：默认继续一跳，显式 `graph_depth=2/3` 才启用 bounded BFS，并返回路径证据。
