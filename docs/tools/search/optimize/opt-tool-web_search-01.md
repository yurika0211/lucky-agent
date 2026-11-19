# opt-tool-web_search-01

## 目标

优化 `web_search` 的查询质量、来源可解释性、失败诊断、配置一致性和测试覆盖，让它继续保持“发现候选 URL 和多来源证据、需要审批、只返回搜索结果条目”的定位，同时降低搜索结果不可追溯、provider 配置不透明、snippet 被误当事实的风险。

本方案聚焦：

- 查询参数和 mode 语义
- quick/deep 搜索来源选择
- 搜索结果结构化输出
- provider 失败诊断
- 缓存和配置一致性
- 与 `web_fetch` / `opencli` 的边界
- 测试矩阵补齐

## 当前状态

相关实现：

- `internal/tool/builtin_web.go`
- `internal/tool/search/search.go`
- `internal/tool/search/engines.go`
- `internal/tool/tool_v070_test.go`
- `docs/tools/search/web_search.md`

当前 `web_search` 的流程：

1. 读取 `query`，缺失或类型不对时报错。
2. 解析 `count`，限制到 1-10。
3. 解析 `mode`，默认 `quick`。
4. 读取 `web_search.provider`，空值回退到 `brave`。
5. 构造 `search.Manager`。
6. 使用 8 秒 timeout。
7. `mode=deep` 时调用 `manager.DeepSearch`。
8. 否则调用 `manager.QuickSearch`。
9. 格式化搜索结果。

当前搜索来源：

- `exa`
- `ddgs`
- `searxng`
- `ddg-lite`
- `brave`

当前优势：

- quick 模式能快速返回第一个可用来源结果。
- deep 模式能并发搜索多个来源并按 URL 去重。
- deep 模式会显示 source errors。
- `count` 有上限，避免输出过大。
- 工具只发现候选 URL，不直接抓正文，职责边界清楚。

## 主要问题

### 1. `query` 没有空字符串校验

当前只校验类型，空字符串会继续进入搜索路径。测试也注明“空字符串 query 在实现中是允许的”。

这会导致：

- 无意义联网请求。
- provider 返回不可控结果。
- 错误诊断不清楚。

建议将空白 query 拒绝：

```text
query must not be empty
```

### 2. `mode` 非法值会静默走 quick

当前只有 `mode == "deep"` 时进入 deep，其余值都走 quick。

这对模型调用容错有利，但不利于发现参数错误。建议明确支持：

- `quick`
- `deep`

非法 mode 返回错误，或至少在输出中标注 fallback。更推荐返回错误，因为 tool 参数错误应尽早暴露。

### 3. provider 配置和实际 engine 顺序不透明

`web_search.provider` 会进入 `buildSearchManagerConfig`，但当前 `SearchConfig.BuildEngines` 使用固定顺序：

```go
[]string{"exa", "ddgs", "searxng", "ddg-lite", "brave"}
```

这意味着 provider 对实际 quick 搜索优先级的影响不明显。文档里也提到 `quickSearchOrder`，但当前 Manager 的实际 BuildEngines 是固定顺序。

需要明确策略：

- provider 是默认首选来源。
- 还是 engine 链始终使用质量优先顺序。

当前实现更接近“质量优先固定顺序”。建议要么调整实现让 provider 影响顺序，要么更新文档和配置语义，避免误导。

### 4. quick 模式失败诊断不足

quick 模式全部失败时只返回：

```text
No results found for '<query>' (all search sources failed)
```

没有说明：

- 哪些 engine 被尝试。
- 哪些因为缺 API key 被跳过。
- 哪些因为网络、依赖或 proxy 失败。

deep 模式有 source errors，quick 模式也应提供简短诊断，至少在 debug 或 verbose 模式下展示。

### 5. 搜索结果缺少结构化 metadata

当前输出是纯文本。对 Agent 来说可读，但不利于后续自动选择：

- 官方来源优先。
- 多来源支持优先。
- 同域去重。
- 选择 URL 后自动传给 `web_fetch`。

建议增加可选 `format` 参数：

- `text`：当前默认。
- `json`：结构化结果。

第一版可以只设计，不立即开放给模型，避免输出格式破坏。

### 6. snippet 易被误用为事实

文档已经说明 snippet 不等于事实，但工具输出本身没有提示“需要 fetch 原文”。对于高风险、近期、引用类问题，应在输出末尾建议继续 `web_fetch`。

建议当结果非空时追加一行轻量提示，或仅在 `mode=deep` 时追加：

```text
Use web_fetch on selected URLs before quoting or relying on page content.
```

需要注意不要污染程序化解析。

### 7. 缓存生命周期不清晰

`SearchConfig` 有 `CacheTTL` 和 `CacheSize`，Manager 内部有 cache。但 `handleWebSearch` 每次调用都会 `NewManager`，因此工具调用级别可能无法复用缓存。

需要确认并优化：

- 如果每次新建 manager，cache 实际只在单次调用内有效。
- 如果希望跨调用缓存，需要在服务层复用 manager 或引入共享缓存。

当前文档不应暗示跨调用缓存有效。

### 8. 测试覆盖偏参数不 panic

现有 `tool_v070_test.go` 多数测试只验证不 panic，因为 live search 被跳过。需要把 engine 和 manager 注入做成可测试边界，避免真实网络依赖。

## 优化原则

1. `web_search` 只负责发现候选 URL，不抓正文。
2. 对参数错误显式失败，不静默发起无意义搜索。
3. quick 模式优先快，deep 模式优先可解释和多来源。
4. 搜索结果必须提示来源和失败原因，避免把 snippet 当事实。
5. 不让测试依赖真实网络、API key 或 Python 包。
6. provider 配置语义必须与实际 engine 顺序一致。

## 推荐方案

### 1. 抽出参数解析

新增：

```go
type webSearchOptions struct {
	Query string
	Count int
	Mode  string
	Format string
	Verbose bool
}

func parseWebSearchOptions(cfg *WebSearchConfig, args map[string]any) (webSearchOptions, error)
```

行为：

- `query` 必须是非空字符串。
- `count` 继续限制为 1-10。
- `mode` 只能是 `quick` 或 `deep`。
- `format` 默认 `text`，预留 `json`。
- `verbose` 默认 `false`。

收益：

- 参数行为集中。
- 测试不需要真实搜索。
- 错误信息更清晰。

### 2. 明确 provider 和 engine 顺序

推荐策略：provider 作为首选 engine，其余 engine 按固定 fallback 顺序补齐。

示例：

```go
func searchEngineOrder(provider string) []string {
	base := []string{"exa", "ddgs", "searxng", "ddg-lite", "brave"}
	return moveToFront(base, provider)
}
```

如果 provider 不可用或未配置 key，则跳过或失败后 fallback。

这样：

- `provider=brave` 时 Brave 优先。
- `provider=searxng` 时 SearXNG 优先。
- `provider=exa` 时 Exa 优先。
- 未配置 provider 时使用默认质量顺序。

需要同步调整或删除当前不参与主流程的 `quickSearchOrder` / `deepSearchOrder`，避免死代码或误导测试。

### 3. 为 quick 模式增加失败诊断

调整 Manager QuickSearch 返回结构，或新增：

```go
type QuickSearchResult struct {
	Results []SearchResult
	Source  string
	Errors  []string
	Tried   []string
}
```

第一阶段可以不改 public manager API，只在 `handleWebSearch` 内用一个新 helper：

```go
func quickSearchWithDiagnostics(ctx context.Context, engines []SearchEngine, query string, count int) QuickSearchResult
```

输出策略：

- 默认失败时显示简短原因。
- `verbose=true` 时显示每个 engine 的错误。

示例：

```text
No results found for 'query'
Tried: exa, ddgs, ddg-lite, brave
Errors:
  - exa: missing API key
  - ddgs: executable not found
```

### 4. 增加 structured output 预留

新增 `format` 参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `format` | 否 | `text` | 输出格式：`text` 或 `json`。 |

JSON 输出示例：

```json
{
  "query": "Go os.Rename documentation",
  "mode": "deep",
  "results": [
    {
      "title": "os package - os - Go Packages",
      "url": "https://pkg.go.dev/os",
      "snippet": "...",
      "sources": ["exa", "ddg-lite"]
    }
  ],
  "errors": []
}
```

第一版可以只在内部结构中准备，不暴露给模型。等 `web_fetch` 自动接 URL 的流程明确后再开放。

### 5. 增加结果后处理

结果后处理建议包括：

- URL 规范化去重。
- 空标题时用 URL host 兜底。
- snippet 过长时单条截断。
- 来源名标准化。
- 可选官方来源标记。

官方来源标记可先基于简单规则：

- `site:go.dev` 查询返回 go.dev。
- `docs.github.com`。
- `developer.mozilla.org`。
- 项目官网域名和查询实体匹配。

第一阶段不建议做复杂可信度评分，避免虚假权威。

### 6. 增加 fetch-next 提示

在 text 输出末尾追加可选提示：

```text
Next: use web_fetch on selected URLs before quoting or relying on page content.
```

建议仅在 `verbose=true` 或 deep 模式下追加，避免污染短输出。

### 7. 统一配置来源

当前存在两条配置路径：

- `buildSearchManagerConfig`
- `SearchConfigFromEnv`

建议统一：

```go
func buildSearchManagerConfig(cfg *WebSearchConfig, provider string) *searchpkg.SearchConfig {
	base := searchpkg.DefaultSearchConfig()
	// apply LuckyAgent config
	// then apply env overrides
	return searchpkg.SearchConfigFromEnv(base)
}
```

需要定义优先级：

1. 显式 LuckyAgent config。
2. 环境变量。
3. 默认值。

或：

1. 环境变量覆盖 config。
2. config 覆盖默认值。

必须文档化，避免 API key 来源不一致。

### 8. 复用 Manager 或共享缓存

如果希望 cache 真正跨调用生效，需要：

- 在 Tool service 层持有 `search.Manager`。
- 或引入 package-level cache。
- 或明确 cache 只在 manager 生命周期内有效，不承诺跨 tool call。

推荐短期做法：文档化当前缓存边界，不急于共享缓存。共享缓存涉及并发、配置变更和测试复杂度。

## 分阶段实施

### Phase 1：参数解析和配置语义

改动范围：

- `internal/tool/builtin_web.go`
- `internal/tool/tool_v070_test.go`
- `docs/tools/search/web_search.md`

内容：

- 抽出 `parseWebSearchOptions`。
- 拒绝空白 query。
- 非法 mode 返回错误。
- 明确 provider 是否影响 engine 顺序。
- 更新文档。

验收标准：

- `go test ./internal/tool` 通过。
- 空 query 返回明确错误。
- 非法 mode 返回明确错误。
- provider 顺序测试不依赖真实网络。

### Phase 2：quick 失败诊断

内容：

- quick 搜索收集 tried engines 和 errors。
- 失败输出包含简短诊断。
- 可选 `verbose=true` 显示详细 engine 错误。

验收标准：

- 无 API key、无 ddgs、无 searxng 时错误可读。
- 成功结果仍保持原输出兼容。
- 测试使用 mock engines，不依赖 live search。

### Phase 3：结果结构化和后处理

内容：

- 定义内部 `webSearchResult`。
- text formatter 和可选 json formatter 共用结构。
- 标准化 URL、来源名、snippet 长度。

验收标准：

- quick/deep 输出稳定。
- deep 去重仍按多来源优先。
- 单条 snippet 不会撑爆输出。

### Phase 4：配置统一

内容：

- 统一 `buildSearchManagerConfig` 和 `SearchConfigFromEnv` 的优先级。
- 文档说明 API key、proxy、base_url 来源。
- 增加配置优先级测试。

验收标准：

- Brave、Exa、SearXNG key 来源行为明确。
- proxy 能传给支持的 engine。
- 配置和 env 冲突时结果可预测。

### Phase 5：缓存策略

内容：

- 决定缓存是否跨 tool call。
- 如果跨调用，复用 manager 或共享 cache。
- 如果不跨调用，删除误导性配置或文档明确。

验收标准：

- cache 行为有测试。
- 配置变更不会使用旧 provider 或旧 API key。
- 并发搜索无 data race。

## 测试建议

新增或补充测试：

### 参数解析

- `query` 缺失时报错。
- `query` 非字符串时报错。
- `query` 空白时报错。
- `count` 小于 1 截到 1。
- `count` 大于 10 截到 10。
- `mode=quick` 成功。
- `mode=deep` 成功。
- 非法 mode 报错。

### provider 顺序

- provider 为空使用默认顺序。
- provider=brave 时 brave 优先或文档化不优先。
- provider=searxng 时 searxng 优先。
- provider=unknown 时返回错误或 fallback 行为明确。

### quick 搜索

- 第一个 engine 成功时不再调用后续 engine。
- 前几个 engine 失败后 fallback 成功。
- 全部失败时返回 tried engines 和 errors。
- cache 命中时不调用 engine。

### deep 搜索

- 多 engine 并发返回。
- 部分失败时仍返回结果和 source errors。
- 全部失败时返回诊断。
- 同 URL 多来源合并。
- 结果按来源数量优先。

### 输出格式

- text 输出包含标题、URL、snippet。
- deep 输出包含 source 标记。
- 输出超过限制时截断。
- json 输出结构稳定。

### 配置

- `web_search.api_key` 传给 Brave。
- `EXA_API_KEY` / `LH_SEARCH_EXA_KEY` 能被使用。
- `SEARXNG_BASE_URL` 能被使用。
- `web_search.proxy` 传给支持 engine。
- config 和 env 优先级固定。

## 文档更新

完成实现后，同步更新：

- `docs/tools/search/web_search.md`

如果加入新参数，参数表增加：

```text
format | 否 | text | 输出格式：text 或 json。
verbose | 否 | false | 显示 engine 尝试顺序和失败诊断。
```

并明确 provider 语义：

```text
provider 表示首选搜索来源；如果该来源不可用，web_search 会按 fallback 顺序尝试其他来源。
```

如果最终不让 provider 影响顺序，则文档必须改成：

```text
provider 只用于配置默认 API key / base_url，实际搜索顺序由 search manager 的 engine 顺序决定。
```

二者必须选一个，不能继续模糊。

## 风险与边界

1. 搜索结果不是事实。
   `web_search` 只能定位候选来源，回答事实前仍应使用 `web_fetch` 或 `opencli` 读取原文。

2. provider 质量和可用性不稳定。
   API key、网络、代理、第三方 HTML 结构变化都会影响结果。

3. deep 模式不等于验证完成。
   多来源命中同一 URL 只能提高候选可信度，不能替代原文核验。

4. 结构化输出可能影响模型使用习惯。
   如果开放 `format=json`，需要确保 agent 能继续选择 URL 并调用 `web_fetch`。

5. 共享缓存需要谨慎。
   跨调用缓存可能引入过期结果、配置变更不生效和并发问题。

## 推荐结论

优先实现 Phase 1、Phase 2 和 Phase 4。

Phase 1 先修正参数和 provider 语义，避免无意义 query 和静默 mode fallback。Phase 2 提升失败诊断，降低搜索失败时的排障成本。Phase 4 统一配置来源，避免 API key 和 provider 行为不一致。Phase 3 和 Phase 5 可以作为第二批推进，分别处理结构化输出和缓存策略。
