# web_search Tool

`web_search` 是 LuckyAgent 的内置联网搜索工具，用来查找外部网页、近期信息、候选来源或多来源证据。它适合在本地文件、记忆、RAG 都不足以回答问题时，用搜索结果定位可信页面。

`web_search` 只返回搜索结果条目，不负责抓取页面正文。拿到目标 URL 后，如果需要阅读网页内容，应继续使用 `web_fetch` 或 `opencli`。

## 工具定义

实现位置：

- `internal/tool/builtin_web.go`
- `internal/tool/search/search.go`

注册信息：

```go
Name:         "web_search"
Category:     CatBuiltin
Source:       "builtin"
Permission:   PermApprove
ParallelSafe: true
```

含义：

- `CatBuiltin`：这是核心内置工具，不依赖 skill 或 MCP。
- `PermApprove`：联网搜索会访问外部网络，默认需要审批。
- `ParallelSafe=true`：搜索本身不修改本地状态，可以和其他只读查询并行。

## 参数

`web_search` 接收五个参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `query` | 是 | 无 | 搜索查询。应围绕要验证的事实、实体或概念编写。 |
| `count` | 否 | `5` | 返回结果数量，范围会被限制到 1-10。 |
| `mode` | 否 | `quick` | 搜索模式：`quick` 或 `deep`。 |
| `format` | 否 | `text` | 返回格式：`text` 或 `json`。 |
| `verbose` | 否 | `false` | 显示 engine 尝试顺序和失败诊断。 |

示例参数：

```json
{
  "query": "LuckyAgent web_search tool implementation",
  "count": 5,
  "mode": "quick"
}
```

## 执行流程

`web_search` 的执行过程是：

1. 读取必填参数 `query`，没有提供时返回 `query is required`，空白字符串返回 `query must not be empty`。
2. 解析 `count`。
3. 如果 `count<1`，改为 1。
4. 如果 `count>10`，改为 10。
5. 解析 `mode`，默认 `quick`；只接受 `quick` 或 `deep`。
6. 读取 `web_search.provider` 配置，空值回退到 `brave`。
7. 构造 search manager。
8. 使用 8 秒上下文超时执行搜索。
9. 如果 `mode=deep`，执行多来源并发搜索和合并。
10. 否则执行 quick 搜索。
11. 格式化搜索结果；`format=json` 时返回结构化结果、tried/errors 和 next 提示。

如果所有搜索来源失败或没有结果，会返回：

```text
No results found for '<query>'
Tried: exa, ddgs, ddg-lite, brave
```

## quick 模式

`quick` 是默认模式，适合快速查找候选页面。

quick 模式会按 search manager 的 engine 顺序尝试搜索来源，拿到第一个成功且非空的结果后返回。`web_search.provider` 会被移动到 engine 顺序最前面，其余 engine 按 `exa`、`ddgs`、`searxng`、`ddg-lite`、`brave` 的 fallback 顺序补齐。

输出格式类似：

```text
[Source: DDG Lite] Results for: query

1. Result title
   https://example.com
   Result snippet
```

quick 输出最多 8000 字符，超过会截断：

```text
... (truncated)
```

## deep 模式

`deep` 模式适合需要交叉验证、多来源证据或更稳妥候选来源的任务。

deep 模式会并发调用多个搜索 engine，然后：

- 收集每个来源的结果。
- 记录每个来源的错误。
- 按规范化 URL 去重。
- 如果同一个 URL 来自多个来源，会把来源合并到 `Source` 字段。
- 优先返回被更多来源支持的结果。

输出格式类似：

```text
Results for: query (deep search, 3 sources)

1. Result title [exa+ddgs]
   https://example.com
   Result snippet

Source errors:
  - brave: brave: no API key
```

deep 输出最多 12000 字符，超过会截断：

```text
... (truncated)
```

## 搜索来源

当前搜索实现包含这些 engine：

- `brave`：Brave Search API，需要 API key。
- `ddgs`：通过 ddgs Python 包搜索。
- `ddg-lite`：通过 DuckDuckGo Lite HTML 搜索。
- `searxng`：通过自托管 SearXNG 实例搜索。
- `exa`：通过 Exa API 搜索，需要 API key。

实际可用性取决于本机环境、依赖、API key、网络和配置。

## 配置

LuckyAgent 配置项：

```json
{
  "web_search": {
    "provider": "brave",
    "api_key": "",
    "base_url": "",
    "max_results": 5,
    "proxy": ""
  }
}
```

对应 config keys：

- `web_search.provider`
- `web_search.api_key`
- `web_search.base_url`
- `web_search.max_results`
- `web_search.proxy`

默认配置：

```go
Provider:   "brave"
MaxResults: 5
```

## API Key 和环境变量

按当前 `web_search` 调用路径，显式 LuckyAgent 配置会先进入 search manager，然后 `SearchConfigFromEnv` 应用 `LH_SEARCH_*` 环境变量覆盖。相关 key 来源包括：

- Brave：`web_search.api_key`，或环境变量 `BRAVE_API_KEY`。
- Exa：当 provider 是 `exa` 时可用 `web_search.api_key`，也可用 `LH_SEARCH_EXA_KEY` 或 `EXA_API_KEY`。
- SearXNG：`web_search.base_url`，或环境变量 `SEARXNG_BASE_URL`。
- Jina：`JINA_API_KEY` 会进入 search manager 配置，但 `web_search` 本身主要用 search engine；Jina 更常用于 fetch。

`web_search.proxy` 会传给支持代理的 engine。

`web_search.provider` 表示首选搜索来源；如果该来源不可用或失败，工具会继续按 fallback 顺序尝试其他来源。

## 输出内容

每条结果包含：

- 标题
- URL
- snippet
- deep 模式下还包含来源标记

`web_search` 不保证 snippet 是完整、最新或足以回答问题的正文。snippet 只适合判断候选来源是否值得进一步抓取。

如果需要引用、总结或验证页面内容，应继续调用：

- `web_fetch`：抓取指定 URL 的可读正文。
- `opencli`：对需要 OpenCLI adapter、登录态浏览器或更复杂网页提取的场景更合适。

## 适合使用的场景

优先使用 `web_search` 的场景：

- 查询近期信息。
- 查找官方文档、规范、公告、仓库或论文页面。
- 获取多个候选来源。
- 在不知道具体 URL 时定位页面。
- 需要 deep 模式做多来源交叉验证。

示例：

```json
{
  "query": "Go os.Rename documentation",
  "count": 5,
  "mode": "quick"
}
```

## 不适合使用的场景

不优先使用 `web_search` 的场景：

- 已经有明确 URL：使用 `web_fetch` 或 `opencli`。
- 需要读取本地文件：使用 `file_read` 或 `document_read`。
- 需要查本地代码：使用 `file_list` / `file_read`，或通过 `terminal` 跑 `rg`。
- 需要调用 JSON API：使用 `http_request`。
- 需要已登录网页、社交平台或站点 adapter：使用 `opencli`。
- 只需要当前时间：使用 `current_time`。

## 常见调用示例

快速搜索：

```json
{
  "query": "LuckyAgent GitHub",
  "count": 5,
  "mode": "quick"
}
```

深度搜索：

```json
{
  "query": "OpenAI Responses API official documentation",
  "count": 8,
  "mode": "deep"
}
```

找官方文档：

```json
{
  "query": "site:go.dev os Rename package os",
  "count": 3,
  "mode": "quick"
}
```

找近期信息：

```json
{
  "query": "latest SQLite release notes",
  "count": 5,
  "mode": "deep"
}
```

## 和 web_fetch 的关系

`web_search` 用来找 URL。

`web_fetch` 用来读 URL。

常见流程：

1. 用 `web_search` 找候选页面。
2. 选择最可信的 URL。
3. 用 `web_fetch` 抓取正文。
4. 基于正文回答，并说明来源。

不要只根据搜索 snippet 对复杂问题下结论。

## 和 opencli 的关系

`opencli` 更适合：

- 已知 URL 的网页转 Markdown。
- 需要站点 adapter 的场景。
- 需要浏览器会话或登录态的页面。
- Twitter、YouTube、知乎、小红书、Bilibili 等站点特定操作。

`web_search` 更适合：

- 不知道 URL 时找候选来源。
- 快速查找公开网页。
- deep 模式做多来源检索。

## 风险和注意事项

`web_search` 的主要风险是搜索结果不等于事实。

调用后应注意：

- snippet 可能过时或不完整。
- 搜索结果排序不代表可信度。
- deep 模式能增加候选来源质量，但仍需要读取原文验证。
- 查询近期、高风险或需要引用的信息时，应继续抓取具体页面。
- 搜索失败可能是 API key、依赖、网络、代理或 provider 配置问题。

## 维护注意事项

如果后续修改 `web_search`，需要同步检查：

- 参数说明是否仍与 `WebSearchTool()` 一致。
- `count` 限制是否仍是 1-10。
- quick 和 deep 的默认行为是否变化。
- 搜索超时是否仍是 8 秒。
- quick 输出截断是否仍是 8000 字符。
- deep 输出截断是否仍是 12000 字符。
- 支持的 engine 列表是否变化。
- API key 和环境变量解析是否变化。
- `buildSearchManagerConfig` 和 `SearchConfigFromEnv` 的覆盖优先级是否变化。
- `web_search` 与 `web_fetch` / `opencli` 的职责边界是否变化。
