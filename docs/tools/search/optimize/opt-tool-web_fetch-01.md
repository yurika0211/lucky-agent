# opt-tool-web_fetch-01

## 目标

优化 `web_fetch` 的 URL 校验、fetch engine 统一调度、失败诊断、输出结构和测试覆盖，让它继续保持“读取明确 URL 的网页正文、需要审批、不处理本地文件或 API mutation”的定位，同时降低 SSRF 绕过、正文提取不完整、失败原因不可见和配置路径不一致的问题。

本方案聚焦：

- 参数解析和 `max_chars` 边界
- SSRF / DNS rebinding 防护
- fetch engine 顺序和配置统一
- 失败诊断和 verbose 输出
- 结构化 metadata
- 缓存策略
- 与 `web_search` / `opencli` / `http_request` 的边界
- 测试矩阵补齐

## 当前状态

相关实现：

- `internal/tool/builtin_web.go`
- `internal/tool/search/search.go`
- `internal/tool/search/engines.go`
- `internal/tool/search/search_test.go`
- `docs/tools/search/web_fetch.md`

当前 `web_fetch` handler 流程：

1. 读取必填参数 `url`。
2. 调用 `validateFetchURL(url)`。
3. 解析 `max_chars`，默认 `50000`。
4. 依次尝试：
   - `fetchWithDefuddle`
   - `fetchWithJina`
   - `fetchWithCurl`
5. 任一成功且结果非空则返回文本。
6. 全部失败时返回：

```text
Failed to fetch <url> (all methods failed)
```

当前优势：

- 明确只接受 URL，不做搜索。
- 有 SSRF 基础校验。
- 多 engine fallback。
- 输出正文而不是只返回 snippet。
- `max_chars` 限制返回长度。

## 主要问题

### 1. handler 绕过了 `SearchConfig.BuildFetchEngines`

`search.SearchConfig` 已经有：

- `PreferredFetch`
- `BuildFetchEngines`
- `Manager.FetchURL`
- `FetchCache`

但 `handleWebFetch` 当前直接固定调用：

```go
fetchWithDefuddle
fetchWithJina
fetchWithCurl
```

这导致：

- `PreferredFetch` 不生效。
- `Manager.FetchURL` 的 fetch cache 不生效。
- Manager 层已有 fallback 测试不能覆盖 handler 行为。
- 配置路径和文档需要长期解释“这里没有用 BuildFetchEngines”。

建议统一走 `search.Manager.FetchURL`，或把 handler 的固定逻辑收敛到 Manager。

### 2. 失败诊断不足

当前所有 engine 失败时只返回统一失败消息。单个 engine 的错误被吞掉。

常见失败原因包括：

- Defuddle 未安装。
- Jina API 失败或限流。
- curl 不存在或被代理阻断。
- URL 校验失败。
- 页面返回空壳。
- TLS / DNS / timeout。

缺少诊断会导致排障必须手动用 `terminal`。

### 3. `max_chars` 没有明确上下限

当前只解析 `max_chars`，未在 handler 层设置上限和下限。Manager 层 `FetchURL` 对 `maxChars <= 0` 回退为 `50000`，但 handler 直接调用 fetch functions。

建议统一：

- 默认：50000
- 最小：1000 或 1
- 最大：100000

过小可能导致无意义输出，过大容易占用上下文。

### 4. URL 校验存在 DNS 解析盲区

`ValidateFetchURL` 主要基于 URL 字面 host / IP 范围判断。对于域名：

```text
http://internal.example.com
```

如果 DNS 解析到内网 IP，字面校验无法发现。需要考虑：

- DNS 解析后 IP 检查。
- 禁止重定向到内网地址。
- 每次 redirect 后重新校验。

curl / external engine 的 redirect 也应纳入风险说明。

### 5. 输出缺少 metadata

当前输出只返回内容。缺少：

- 实际最终 URL。
- 使用的 engine。
- title。
- 是否截断。
- 原始长度或返回长度。
- fetch 失败时每个 engine 的错误。

这会影响来源引用和调试。

### 6. 不同 engine 输出不一致

当前：

- Defuddle 返回 Markdown 正文。
- Jina 可能加 `# Title`。
- curl fallback 返回 strip 后普通文本。

这导致同一个 URL 结果格式取决于 engine。可以接受，但应在输出中标注 engine，或者统一包装 metadata。

### 7. 测试仍有 live dependency

`TestHandleWebFetch` 和 `TestFetchWithDefuddle` 只确保函数可调用，不能稳定验证行为。

应将 fetch engine 注入或复用 Manager mock engine，避免测试依赖真实网络、defuddle、curl 或 Jina。

## 优化原则

1. `web_fetch` 只抓取公开 HTTP(S) URL 的可读正文。
2. SSRF 防护优先于便利性。
3. handler 逻辑应复用 `search.Manager`，避免两套 fetch 路径。
4. 失败诊断必须足够排障，但默认输出不能过长。
5. text 输出继续兼容，结构化输出作为可选能力。
6. 测试不能依赖真实网络或外部 CLI。

## 推荐方案

### 1. 抽出参数解析

新增：

```go
type webFetchOptions struct {
	URL      string
	MaxChars int
	Format   string
	Verbose  bool
}

func parseWebFetchOptions(args map[string]any) (webFetchOptions, error)
```

行为：

- `url` 必须是非空字符串。
- `max_chars <= 0` 回退默认值。
- `max_chars` 限制到最大值。
- `format` 默认 `text`，预留 `json`。
- `verbose` 默认 `false`。

建议常量：

```go
const (
	defaultWebFetchMaxChars = 50000
	maxWebFetchChars        = 100000
)
```

### 2. 统一走 Manager.FetchURL

将 handler 改为：

```go
manager := searchpkg.NewManager(buildSearchManagerConfig(cfg, provider))
result, err := manager.FetchURL(ctx, url, maxChars)
```

并让 `buildSearchManagerConfig` 传入：

- `PreferredFetch`
- `JinaAPIKey`
- `Proxy`
- `CacheTTL`
- `CacheSize`

如果 `WebSearchConfig` 目前没有 `PreferredFetch`，可新增到配置结构，或在 `buildSearchManagerConfig` 中继续默认 `defuddle`。

收益：

- `BuildFetchEngines` 生效。
- `FetchCache` 生效。
- fallback 行为集中。
- 测试可复用 manager mock engine。

### 3. 增加 fetch diagnostics

调整 Manager FetchURL 返回结构，或新增：

```go
type FetchAttempt struct {
	Engine string
	Err    string
}

type WebFetchResult struct {
	URL       string
	FinalURL  string
	Title     string
	Content   string
	Source    string
	Truncated bool
	Attempts  []FetchAttempt
}
```

短期可新增内部 helper：

```go
func fetchURLWithDiagnostics(ctx context.Context, engines []FetchEngine, rawURL string, maxChars int) (*WebFetchResult, error)
```

默认失败输出：

```text
Failed to fetch https://example.com
Tried: defuddle, jina, curl
```

`verbose=true` 时：

```text
Errors:
  - defuddle: command not found
  - jina: HTTP 429
  - curl: content too short
```

### 4. 增强 URL 安全校验

短期：

- 保持现有 scheme / host / literal IP 检查。
- 对 URL 做 trim。
- 拒绝 userinfo，例如 `https://user:pass@example.com`，避免凭据泄漏。
- 拒绝非标准 host 表达方式，例如 octal / hex IPv4，如果 Go parser 能解析则统一用 `netip`。

中期：

- DNS resolve host。
- 对解析出的 IP 做私网/loopback/link-local 检查。
- 对 redirect 后 URL 重新校验。

注意：外部 CLI engine 如 curl / defuddle 的 redirect 行为更难拦截。若需要强 SSRF 防护，最好由 Go HTTP client 统一执行 fetch，或禁用外部 CLI 对不可信 URL 的 redirect。

### 5. 结构化输出

新增 `format` 参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `format` | 否 | `text` | 输出格式：`text` 或 `json`。 |

JSON 示例：

```json
{
  "url": "https://go.dev/doc/",
  "final_url": "https://go.dev/doc/",
  "title": "Documentation",
  "source": "defuddle",
  "truncated": false,
  "content": "...",
  "attempts": []
}
```

默认 text 继续返回正文，避免破坏模型使用习惯。

### 6. 输出 metadata header

可选在 text 输出顶部增加轻量 header：

```text
URL: https://go.dev/doc/
Source: defuddle
Title: Documentation

...
```

为兼容，建议只在 `verbose=true` 或 `include_meta=true` 时启用。

### 7. 统一截断策略

当前不同 engine 内部各自截断。建议统一：

- engine 返回尽可能完整的提取内容。
- handler 根据 `max_chars` 做最终截断。
- 设置 `Truncated=true`。
- 输出统一追加：

```text
... (truncated; increase max_chars to continue)
```

这能减少重复截断和不一致。

### 8. 明确动态网页 fallback

如果 curl/Jina/Defuddle 都返回内容过短，应提示：

```text
Fetched content was too short; page may require JavaScript or login. Use opencli for browser/session extraction.
```

这能明确 `web_fetch` 和 `opencli` 的边界。

## 分阶段实施

### Phase 1：参数解析和诊断

改动范围：

- `internal/tool/builtin_web.go`
- `internal/tool/tool_v067_test.go`
- `docs/tools/search/web_fetch.md`

内容：

- 抽出 `parseWebFetchOptions`。
- 规范 `max_chars` 下限和上限。
- 失败时返回 tried engines。
- `verbose=true` 返回每个 engine 错误。

验收标准：

- `go test ./internal/tool` 通过。
- URL 缺失、空 URL、非法 URL 错误明确。
- `max_chars` 行为确定。
- 全部失败时有诊断信息。

### Phase 2：统一 Manager.FetchURL

内容：

- handler 改为复用 `search.Manager.FetchURL` 或等价统一 helper。
- `BuildFetchEngines` 的 `PreferredFetch` 生效。
- 删除 handler 内重复 fallback 逻辑。

验收标准：

- defuddle / jina / curl 顺序由配置决定。
- fallback 行为测试使用 mock engines。
- fetch cache 行为与 Manager 一致。

### Phase 3：增强 SSRF 防护

内容：

- trim URL。
- 拒绝 userinfo。
- DNS 解析后检查私网 IP。
- redirect 后重新校验，至少对 Go HTTP engine 生效。

验收标准：

- localhost、私网、link-local、IPv6 私网继续拒绝。
- 域名解析到私网 IP 时拒绝。
- redirect 到私网 IP 时拒绝。

### Phase 4：结构化输出和 metadata

内容：

- 新增内部 `WebFetchResult`。
- 增加 `format=json`。
- 可选 `include_meta` 或 `verbose=true` 输出 metadata header。

验收标准：

- 默认 text 输出兼容。
- json 输出字段稳定。
- source、title、final_url、truncated 可用。

### Phase 5：统一截断和动态网页提示

内容：

- handler 统一最终截断。
- engine 内部尽量不重复截断，或保留但标记。
- 内容过短时提示 opencli。

验收标准：

- max_chars 截断一致。
- truncated 标记准确。
- 短内容失败提示可操作。

## 测试建议

新增或补充测试：

### 参数解析

- `url` 缺失时报错。
- `url` 非字符串时报错。
- `url` 空白时报错。
- `max_chars<=0` 回退默认值。
- `max_chars` 超过上限时截断到上限。
- `format` 非法时报错。

### URL 校验

- http/https 正常。
- ftp/javascript/file 拒绝。
- localhost / 127.0.0.1 拒绝。
- 10/8、172.16/12、192.168/16 拒绝。
- 169.254/16 拒绝。
- IPv6 loopback / ULA / link-local 拒绝。
- userinfo URL 拒绝。
- DNS 解析到私网 IP 拒绝。

### fetch fallback

- 第一个 engine 成功时不调用后续 engine。
- 前两个 engine 失败，第三个成功。
- nil result 继续 fallback。
- 空 content 继续 fallback。
- 全部失败返回 tried engines 和 errors。

### 输出

- 默认 text 只返回正文。
- `verbose=true` 包含 URL、source、title。
- `format=json` 字段稳定。
- 超过 `max_chars` 时截断并标记。
- 内容过短时提示可能需要 opencli。

### 配置和缓存

- preferred fetch=jinа 时 Jina 优先。
- preferred fetch=curl 时 curl 优先。
- proxy 传给 Jina 和 curl。
- JINA_API_KEY 生效。
- cache 区分 URL 和 max_chars。
- cache 命中不再次调用 engine。

## 文档更新

完成实现后，同步更新：

- `docs/tools/search/web_fetch.md`

如果加入新参数，参数表增加：

```text
format | 否 | text | 输出格式：text 或 json。
verbose | 否 | false | 显示 fetch engine、最终 URL 和失败诊断。
include_meta | 否 | false | 在 text 输出中包含 URL、source、title 等 metadata。
```

并明确：

```text
web_fetch 会复用 SearchConfig.BuildFetchEngines，因此 preferred fetch 配置会影响 Defuddle、Jina、curl 的尝试顺序。
```

如果暂不统一 Manager，则文档必须继续明确 handler 固定顺序，避免配置误解。

## 风险与边界

1. SSRF 防护很难和外部 CLI 完全统一。
   curl、defuddle、Jina 的 redirect 和 DNS 行为不完全受 Go 代码控制。高安全场景应优先用 Go HTTP client 统一执行。

2. 网页正文提取不是浏览器渲染。
   动态网页、登录态页面、反爬页面仍应使用 `opencli`。

3. Jina 和 Defuddle 可用性不稳定。
   依赖外部服务、CLI 安装、网络和 API key。

4. 结构化输出会改变模型消费方式。
   默认保持 text，json 作为显式参数。

5. 跨调用缓存需要配置失效策略。
   如果 provider、proxy、JINA_API_KEY 改变，共享缓存不能继续返回旧配置下的结果。

## 推荐结论

优先实现 Phase 1、Phase 2 和 Phase 3。

Phase 1 先补齐参数边界和失败诊断。Phase 2 统一 handler 与 Manager 的 fetch 路径，消除 `PreferredFetch`、cache 和 handler 固定顺序之间的不一致。Phase 3 增强 SSRF 防护，是联网抓取工具的关键安全改进。Phase 4 和 Phase 5 可作为第二批推进，分别处理结构化输出和提取质量提示。
