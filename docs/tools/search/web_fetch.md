# web_fetch Tool

`web_fetch` 是 LuckyAgent 的内置网页正文抓取工具，用来从一个明确 URL 中提取可读文本。它适合在已经知道目标页面地址，并且需要读取页面正文、文档内容或文章内容时使用。

它和 `web_search` 的职责不同：`web_search` 用来找 URL，`web_fetch` 用来读 URL。

## 工具定义

实现位置：

- `internal/tool/builtin_web.go`
- `internal/tool/search/engines.go`
- `internal/tool/search/search.go`

注册信息：

```go
Name:         "web_fetch"
Category:     CatBuiltin
Source:       "builtin"
Permission:   PermApprove
ParallelSafe: true
```

含义：

- `CatBuiltin`：这是核心内置工具，不依赖 skill 或 MCP。
- `PermApprove`：抓取外部 URL 会访问网络，默认需要审批。
- `ParallelSafe=true`：抓取网页不修改本地状态，可以和其他只读查询并行。

## 参数

`web_fetch` 接收两个参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `url` | 是 | 无 | 要抓取并转换为可读文本的明确 URL。 |
| `max_chars` | 否 | `50000` | 最多返回多少字符的正文。 |

示例参数：

```json
{
  "url": "https://go.dev/pkg/os/",
  "max_chars": 20000
}
```

## 执行流程

`web_fetch` 的执行过程是：

1. 读取必填参数 `url`，没有提供时返回 `url is required`。
2. 调用 `validateFetchURL(url)` 做 URL 安全校验。
3. 解析 `max_chars`，没有提供时默认为 `50000`。
4. 先尝试 Defuddle 提取。
5. Defuddle 失败或返回空结果时，尝试 Jina Reader。
6. Jina 失败或返回空结果时，尝试 curl + HTML stripping。
7. 任一方法成功且结果非空时，立即返回。
8. 全部失败时，返回失败消息。

全部失败时输出：

```text
Failed to fetch <url> (all methods failed)
```

注意：当前 handler 会吞掉单个 fetch engine 的具体错误，只返回最终失败消息。调试具体原因时，需要用 `terminal` 单独检查 `defuddle`、`curl`、网络或代理。

## URL 安全校验

`web_fetch` 会调用 `ValidateFetchURL` 做 SSRF 风险控制。

只允许：

- `http`
- `https`

会拒绝：

- 空 host
- `localhost`
- `127.*`
- `10.*`
- `192.168.*`
- `172.16.*` 到 `172.31.*`
- `169.254.*`
- `0.*`
- `::1`
- `fc*`
- `fd*`
- `fe80*`

这意味着 `web_fetch` 不能用来访问本机服务、内网地址、link-local metadata 地址或非 HTTP(S) URL。

如果 URL 校验失败，错误格式是：

```text
url validation failed: <reason>
```

## 提取顺序

当前 `web_fetch` 工具固定按以下顺序尝试，不走 `SearchConfig.BuildFetchEngines()` 的 preferred fetch 配置：

1. Defuddle
2. Jina Reader
3. curl + HTML stripping

### Defuddle

Defuddle 路径会检查本机是否有 `defuddle` 命令：

```sh
which defuddle
```

然后执行：

```sh
defuddle parse <url> --md
```

特点：

- 输出 Markdown。
- 不包含额外标题包装。
- 如果输出超过 `max_chars`，会截断并追加 `... (truncated)`。

失败原因可能包括：

- `defuddle` 未安装。
- `defuddle parse` 失败。
- 返回空结果。

### Jina Reader

Jina 路径使用 curl 请求：

```text
https://r.jina.ai/<url>
```

并请求 JSON：

```text
Accept: application/json
```

如果环境变量 `JINA_API_KEY` 存在，会添加：

```text
Authorization: Bearer <key>
```

如果配置了 `web_search.proxy`，会传给 curl 的 `--proxy`。

Jina 成功时会读取 JSON 中的：

- `data.title`
- `data.content`
- `data.url`

返回时，如果有 title，会包装成：

```markdown
# Title

Content
```

### curl fallback

curl fallback 会直接请求原 URL：

```sh
curl -s -L <url> -H "User-Agent: Mozilla/5.0 ..." --max-time 15
```

如果配置了 `web_search.proxy`，会传给 curl 的 `--proxy`。

随后会：

- strip HTML tags
- normalize whitespace
- 如果提取文本少于 50 字符，视为失败
- 如果超过 `max_chars`，截断并追加 `... (truncated)`

curl fallback 是最后兜底路径，适合简单 HTML 页面，不适合复杂动态网页。

## max_chars 行为

默认：

```text
50000
```

`max_chars` 传给每个 fetch engine。不同 engine 的处理点略有差异，但都会限制最终返回内容长度。

超过限制时，会追加：

```text
... (truncated)
```

当只需要快速确认页面主题或抽取一小段内容时，应调低 `max_chars`，避免把大量正文塞进上下文。

## 配置

`web_fetch` 使用 `WebSearchConfig` 中的部分配置：

```json
{
  "web_search": {
    "proxy": ""
  }
}
```

当前直接影响 `web_fetch` 的配置：

- `web_search.proxy`：传给 Jina 和 curl fallback。

相关环境变量：

- `JINA_API_KEY`：用于 Jina Reader 授权。

注意：`SearchConfig` 中的 `PreferredFetch` 和 `BuildFetchEngines()` 在当前 `web_fetch` handler 中没有直接使用。当前顺序固定为 Defuddle、Jina、curl。

## 输出内容

输出是纯文本或 Markdown 风格文本。

不同 engine 输出略有差异：

- Defuddle：通常是 Markdown 正文。
- Jina：如果有标题，会以 `# Title` 开头。
- curl fallback：HTML strip 后的普通文本。

`web_fetch` 不返回 HTTP 状态码、响应头或结构化 metadata。如果需要 API 状态、headers 或 JSON 响应，应使用 `http_request`。

## 适合使用的场景

优先使用 `web_fetch` 的场景：

- 已经有明确 URL，需要读取页面正文。
- 从 `web_search` 结果中进一步验证原文。
- 抓取公开文章、文档、博客、README 页面。
- 获取可引用的页面内容。
- 快速把网页转换成可读文本。

示例：

```json
{
  "url": "https://go.dev/doc/",
  "max_chars": 30000
}
```

## 不适合使用的场景

不优先使用 `web_fetch` 的场景：

- 不知道 URL：先用 `web_search`。
- 需要调用 JSON API 并查看状态码/headers：使用 `http_request`。
- 需要登录态、浏览器会话或站点 adapter：使用 `opencli`。
- 页面强依赖 JavaScript 渲染：使用 `opencli` 的 browser 或 site 能力。
- 需要访问 localhost、内网地址或非 HTTP(S) scheme：`web_fetch` 会拒绝。
- 需要读取本地文件：使用 `file_read` 或 `document_read`。

## 常见调用示例

抓取官方文档：

```json
{
  "url": "https://go.dev/pkg/os/",
  "max_chars": 20000
}
```

抓取搜索结果里的文章：

```json
{
  "url": "https://example.com/article",
  "max_chars": 50000
}
```

只抓取较短内容：

```json
{
  "url": "https://example.com/changelog",
  "max_chars": 8000
}
```

## 和 web_search 的关系

`web_search` 用来发现候选 URL。

`web_fetch` 用来读取某个 URL 的正文。

推荐流程：

1. `web_search` 查询主题。
2. 从结果里选择官方、原始或可信 URL。
3. `web_fetch` 抓取正文。
4. 基于正文回答，而不是只基于搜索 snippet。

## 和 opencli 的关系

`web_fetch` 是轻量网页正文提取。

`opencli` 更适合：

- 需要登录态的页面。
- 需要浏览器执行 JavaScript 的页面。
- 需要站点 adapter 的任务。
- 需要下载图片或处理复杂网页结构。
- 已有 OpenCLI adapter 的站点，例如 Twitter、YouTube、知乎、小红书、Bilibili。

如果 `web_fetch` 返回内容太少、正文不完整或页面依赖 JS，应考虑改用 `opencli`。

## 和 http_request 的关系

`web_fetch` 面向“网页正文”。

`http_request` 面向“HTTP 请求结果”。

如果目标是 REST API、JSON、状态码、headers 或 POST/PUT/PATCH/DELETE 请求，应使用 `http_request`。

如果目标是文章、文档、网页正文，应使用 `web_fetch`。

## 风险和注意事项

`web_fetch` 的主要风险是网页提取不完整或被站点反爬影响。

调用后应注意：

- 返回内容可能是截断后的文本。
- 动态网页可能只抓到壳。
- curl fallback 的 HTML stripping 会丢失结构。
- Jina 或 Defuddle 可能受服务可用性和网络影响。
- URL 安全校验会拒绝内网和 localhost。
- 抓取外部网页不等于事实验证，仍应判断来源可信度。

## 维护注意事项

如果后续修改 `web_fetch`，需要同步检查：

- 参数说明是否仍与 `WebFetchTool()` 一致。
- 默认 `max_chars` 是否仍是 `50000`。
- URL 校验规则是否变化。
- fetch 顺序是否仍是 Defuddle、Jina、curl。
- 是否改为使用 `BuildFetchEngines()`。
- Defuddle 命令参数是否变化。
- Jina Reader 请求方式和 `JINA_API_KEY` 是否变化。
- curl fallback 的 User-Agent、timeout、proxy、最小文本长度是否变化。
- 输出是否仍是纯文本/Markdown。
- 与 `web_search`、`opencli`、`http_request` 的职责边界是否变化。

