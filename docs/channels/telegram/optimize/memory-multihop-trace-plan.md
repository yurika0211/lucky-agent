# Telegram Memory 多跳查询 Trace 方案

本文规划如何把 LuckyAgent 记忆系统的多跳查询过程作为 `Memory Trace` 展示到 Telegram channel，重点覆盖 `recall` 的 graph activation、多跳路径、排序分数、时间状态过滤和最终命中结果。

## 目标

在 Telegram 中新增一类独立 trace 卡片：

```text
Memory Trace
query: 今天带女儿户外活动安全吗
depth: 2 | results: 5 | graph paths: 8
[1] direct health/long score=1.42 @50_Facts/...
[2] hop1 via [[Daughter]] -> pollen allergy score=0.38
[3] hop2 via [[Outdoor Plan]] -> weather rule score=0.22
notes: superseded memory ignored: ...
```

目标能力：

- 展示 query、mode、limit、graph depth、filters、source vault。
- 区分直接命中、图扩散命中、reranker 提升、temporal 过滤。
- 展示每条图路径的 hop、from、to、via、kind、weight/boost。
- 最多展示摘要，避免把大量记忆正文刷屏。
- 不把 Memory Trace 混进最终回答，也不污染模型上下文。
- 与现有 `Tool Trace`、`Agent Trace`、`Reasoning Trace` 并行。

非目标：

- 不把 provider hidden reasoning 暴露为 memory trace。
- 不把全部 memory content 原文发到 Telegram。
- 不要求所有工具都支持结构化 trace，先接入 `recall`。

## 当前实现事实

### memory 查询

相关文件：

- `internal/memory/activation.go`
- `internal/memory/memory.go`
- `internal/tool/memory_service.go`
- `internal/tool/builtin_memory.go`

当前 `recall` 工具已经有这些参数：

- `query`
- `mode`
- `limit`
- `category`
- `tier`
- `include_inactive`
- `include_expired`
- `as_of`
- `graph_depth`
- `explain_graph`
- `format`

`MemoryToolService.HandleRecall()` 在 search 模式中调用：

```go
s.store.SearchWithOptions(query, memory.SearchOptions{
    Limit:        limit,
    IncludeGraph: graphDepth > 0,
    GraphDepth:   graphDepth,
    Explain:      boolArg(args["explain_graph"]),
})
```

`SearchWithOptions()` 会返回 `SearchResult`：

```go
type SearchResult struct {
    Entry       Entry
    Score       float64
    DirectScore float64
    GraphScore  float64
    Paths       []ActivationPath
}
```

`ActivationPath` 当前只有单边信息：

```go
type ActivationPath struct {
    FromID string
    ToID   string
    Via    string
    Kind   string
    Weight float64
}
```

### 当前 graph activation

`ActivationOptions` 已有：

```go
MaxGraphDepth int
```

但注释明确说明当前实现仍是 shallow one-hop graph spread。实际扩散逻辑在 `spreadActivationGraphLocked()` / `spreadActivationFromLocked()`：

- `wikilink_target`
- `backlink`
- `alias_backlink`
- `shared_tag`

也就是说当前实际查询链路更接近：

```text
query -> direct seed -> one-hop related memory
```

还不是完整的：

```text
query -> A -> B -> C
```

### Telegram trace 管线

相关文件：

- `internal/gateway/telegram/handler.go`
- `internal/gateway/telegram/tool_nl.go`
- `internal/agent/agent.go`
- `internal/agent/loop_execution.go`
- `internal/tool/gateway.go`

现有 Telegram trace 依赖 `agent.ChatEvent`：

- `ChatEventThinking`
- `ChatEventToolCall`
- `ChatEventToolResult`
- `ChatEventContent`
- `ChatEventDone`
- `ChatEventError`

Tool Trace 来自 `ChatEventToolCall` / `ChatEventToolResult`，但 `emitChatToolResultEvent()` 发的是 `ShortResult`。`ShortResult` 在 `executeToolCallsOrdered()` 中会被截断到约 200 字符。

因此，Telegram handler 不能可靠地从现有 Tool Trace 中解析完整 memory path。Memory Trace 需要新的结构化事件或工具 metadata。

## 推荐方案

采用三层设计：

1. `internal/memory` 生成结构化 `MemoryTrace`。
2. `internal/tool` 把 `recall` 的 trace 作为工具 metadata 传给 agent，不塞进模型可见文本。
3. `internal/agent` 发出 `ChatEventMemoryTrace`，Telegram 渲染 `Memory Trace` 卡片。

核心原则：工具结果给模型保持紧凑，trace 给 UI/telemetry 单独走。

## 数据结构设计

### memory 层

新增结构建议放在 `internal/memory/trace.go`：

```go
type TraceLevel string

const (
    TraceNone    TraceLevel = "none"
    TraceSummary TraceLevel = "summary"
    TraceFull    TraceLevel = "full"
)

type SearchTrace struct {
    Query        string              `json:"query"`
    Mode         string              `json:"mode"`
    Source       string              `json:"source,omitempty"`
    Limit        int                 `json:"limit,omitempty"`
    GraphDepth   int                 `json:"graph_depth"`
    Filters      SearchTraceFilters  `json:"filters,omitempty"`
    Seeds        []SearchTraceNode   `json:"seeds,omitempty"`
    Hops         []SearchTraceHop    `json:"hops,omitempty"`
    Results      []SearchTraceResult `json:"results,omitempty"`
    Temporal     []string            `json:"temporal_notes,omitempty"`
    Warnings     []string            `json:"warnings,omitempty"`
    DurationMS   int64               `json:"duration_ms,omitempty"`
}

type SearchTraceHop struct {
    Depth       int     `json:"depth"`
    FromID      string  `json:"from_id"`
    FromRef     string  `json:"from_ref,omitempty"`
    ToID        string  `json:"to_id"`
    ToRef       string  `json:"to_ref,omitempty"`
    Via         string  `json:"via,omitempty"`
    Kind        string  `json:"kind"`
    Weight      float64 `json:"weight,omitempty"`
    Boost       float64 `json:"boost,omitempty"`
    SourceScore float64 `json:"source_score,omitempty"`
    TargetScore float64 `json:"target_score,omitempty"`
}
```

`SearchTraceNode` 和 `SearchTraceResult` 应只保留安全摘要：

- `id`
- `ref`: `path#block_id`
- `category`
- `tier`
- `score`
- `direct_score`
- `graph_score`
- `matched_by`
- `content_preview`

`content_preview` 建议截断到 80-120 rune，Telegram 卡片默认不展示，调试配置开启时才展示。

### tool 层

保留当前 `Tool.Handler func(args map[string]any) (string, error)`，新增向后兼容的详细返回能力：

```go
type ToolCallResult struct {
    Output   string
    Metadata map[string]any
}

type Tool struct {
    Handler         func(args map[string]any) (string, error)
    DetailedHandler func(args map[string]any) (ToolCallResult, error)
}
```

`Registry.Call()` 继续返回 string。新增：

```go
func (r *Registry) CallDetailed(name string, args map[string]any) (ToolCallResult, error)
```

如果工具没有 `DetailedHandler`，`CallDetailed()` 包装旧 `Handler`：

```go
ToolCallResult{Output: out}
```

`GatewayResult` 增加：

```go
Metadata map[string]any
```

`recall` 的 `DetailedHandler` 写入：

```go
Metadata["memory_trace"] = memory.SearchTrace{...}
```

### agent 层

`executedToolCall` 增加 metadata：

```go
type executedToolCall struct {
    Index       int
    ToolCall    provider.ToolCall
    Result      string
    ShortResult string
    Duration    time.Duration
    Metadata    map[string]any
}
```

`ChatEventType` 增加：

```go
ChatEventMemoryTrace
```

`ChatEvent` 增加可选字段：

```go
MemoryTrace *memory.SearchTrace
```

在流式执行工具后：

```go
emitChatToolResultEvent(...)
emitChatMemoryTraceEvent(events, execResult)
```

只有当 `execResult.Metadata["memory_trace"]` 存在时才发事件。

### Telegram 层

在 `internal/gateway/telegram/tool_nl.go` 增加：

```go
func renderTelegramMemoryTraceCard(trace memory.SearchTrace) string
```

展示规则：

- 标题：`Memory Trace`
- query 一行。
- depth、results、paths 一行。
- 最多展示 6 个 result。
- 最多展示 8 条 hop。
- hidden/archived/superseded 只展示 ref 和 reason，不展示正文。
- 使用 `<pre><code>` 或 expandable blockquote。

在 `handleChatNarrativeStream()` 和 `handleChatStream()` 中处理：

```go
case agent.ChatEventMemoryTrace:
    memoryTraceCards = append(memoryTraceCards, evt.MemoryTrace)
```

发送时机建议：

- narrative stream：在 Tool Trace 之前发送 Memory Trace。
- 默认 stream：如果配置开启，也在 `ChatEventDone` 后发送 Memory Trace。

## 多跳查询实现计划

### 阶段 1：把现有一跳 graph trace 出来

目标：不改变召回语义，只把现有 graph activation 解释清楚。

改动：

- `SearchWithOptions()` 内部收集 `SearchTrace`。
- `ActivationPath` 转换为 `SearchTraceHop{Depth:1,...}`。
- `recall` metadata 输出 `memory_trace`。
- agent 发 `ChatEventMemoryTrace`。
- Telegram 渲染卡片。

收益：

- 风险低。
- 能立刻解释“为什么 recall 找到了这条 memory”。
- 为真正多跳扩展打 UI 和事件基础。

### 阶段 2：实现 bounded multi-hop graph activation

目标：让 `graph_depth=2/3` 真正生效。

建议算法：

```text
direct matches -> seed frontier
for depth = 1..MaxGraphDepth:
  expand each frontier node by graph edges
  compute boost = source_score * edge_weight * depth_decay * target_weight
  cap per-target graph boost
  append trace hop
  enqueue target for next depth if score passes threshold
```

边类型沿用当前实现：

- `wikilink_target`
- `backlink`
- `alias_backlink`
- `shared_tag`

新增约束：

- `MaxGraphDepth <= 3`
- `MaxGraphSeeds <= 12`
- `MaxTraceHops <= 40`
- `MaxFrontierPerDepth <= 24`
- 每个 target 的 `GraphBoost` 继续受 `MaxGraphBoost` 限制。
- 对 path 做去重：`from_id + to_id + kind + via + depth`。
- 防环：同一 chain 内不重复访问同一 ID。

建议新增：

```go
type ActivationPath struct {
    FromID string
    ToID   string
    Via    string
    Kind   string
    Weight float64
    Depth  int
    Boost  float64
}
```

如果不想改现有 JSON 字段，可新增 `TracePath`，保留 `ActivationPath` 兼容。

### 阶段 3：把 temporal resolution 纳入 trace

目标：让用户知道哪些记忆被忽略，以及原因。

接入点：

- `memory.Store.ResolveTemporal(query, entries)`
- `TemporalResolution.Notes`
- `SupersededRefs`
- `ConflictRefs`
- `ExpiredRefs`
- `FutureRefs`

展示规则：

- 默认只展示 notes 数量和前 2 条。
- 不展示 inactive memory 正文。
- conflict 必须显式标出，避免模型静默合并冲突记忆。

### 阶段 4：配置开关

建议新增配置键：

```json
{
  "msg_gateway": {
    "telegram": {
      "memory_trace": true,
      "memory_trace_level": "summary",
      "memory_trace_max_results": 6,
      "memory_trace_max_hops": 8
    }
  }
}
```

语义：

- `memory_trace=false`：不发送 Telegram Memory Trace。
- `memory_trace_level=summary`：只显示 query、命中数量、路径摘要、refs。
- `memory_trace_level=full`：展示更多 score/component/hop 信息，仍截断内容。

不建议默认打开 `full`。

## 为什么不只解析 recall JSON

可以做一个过渡方案：要求模型调用 `recall` 时带：

```json
{"format":"json","explain_graph":true}
```

然后 Telegram handler 解析 `ChatEventToolResult.Result`。

但这个方案不推荐作为正式实现：

- `ChatEventToolResult.Result` 当前是 `ShortResult`，会被截断。
- 模型不一定总是请求 `format=json`。
- 文本工具输出和 UI trace 耦合，会污染模型上下文。
- Telegram handler 需要理解工具输出格式，边界不干净。

正式方案应让 trace 作为结构化 metadata 穿过 tool gateway 和 agent event。

## Telegram 展示草案

summary 模式：

```text
Memory Trace
query: 今天带女儿户外活动安全吗
depth=2 results=4 hops=5 source=LuckyAgent memory vault
[1] direct health/long score=1.42 @50_Facts/pollen.md#mem-...
[2] hop1 wikilink_target via Daughter -> health/long +0.31
[3] hop2 backlink via Outdoor Plan -> rule/long +0.18
notes: 1 superseded ignored, 1 conflict present
```

full 模式可追加：

```text
components: lexical=0.44 links=0.60 tier=1.20 graph=0.31 tidal=0.08
```

为避免泄露和刷屏，Telegram 卡片默认不要展示完整 memory content，只展示 `content_preview`，且截断。

## 测试计划

memory 层：

- `SearchWithTrace` 返回 direct seed、hop、score、path。
- `graph_depth=0` 不产生 hop。
- `graph_depth=1` 与当前行为兼容。
- `graph_depth=2` 能召回 `A -> B -> C`。
- cycle 不会无限扩散。
- `MaxGraphBoost` 和结果 limit 生效。
- temporal notes 能进入 trace。

tool 层：

- 旧 `Registry.Call()` 行为不变。
- `CallDetailed()` 对旧工具兼容。
- `recall` detailed metadata 包含 `memory_trace`。
- `format=text` 时模型可见输出仍保持紧凑。
- `format=json` 时可选择包含 trace，但不依赖它给 Telegram 展示。

agent 层：

- `executeToolCallsOrdered()` 保存 metadata。
- recall 执行后流式发出 `ChatEventMemoryTrace`。
- 非 recall 工具不会发 memory trace。
- memory gate 自动执行 recall 时也能发 memory trace。

Telegram 层：

- narrative stream 结束时发送 Memory Trace 卡片。
- 默认 stream 在配置开启时也能发送。
- 卡片数量、result 数量、hop 数量按配置截断。
- HTML escape 正确，路径、query、via 不破坏 Telegram HTML。

## 推荐落地顺序

1. 新增 `memory.SearchTrace` 数据结构和一跳 trace 收集，不改变排序。
2. 新增 tool detailed result 和 `GatewayResult.Metadata`，只让 `recall` 接入。
3. 新增 `ChatEventMemoryTrace` 并在 stream tool result 后发出。
4. Telegram 增加 `renderTelegramMemoryTraceCard()` 和配置开关。
5. 实现真正 bounded multi-hop graph activation。
6. 补 temporal notes、reranker before/after rank、tidal boost 展示。

这样做可以先把现有行为透明化，再逐步扩展真正多跳能力，避免一次性同时改 memory ranking、tool gateway、agent event 和 Telegram UI。
