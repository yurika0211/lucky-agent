# http_request Tool

`http_request` 是 LuckyAgent 的内置受控 HTTP 请求工具，用来访问公开 HTTP(S) API 或接口，并返回状态、内容类型和响应体。它适合读取 JSON API、调试公开 endpoint，或处理不适合 `web_fetch` 的接口响应。

这是会访问网络的工具，因此被标记为需要批准。

## 工具定义

实现位置：

- `internal/tool/builtin_query.go`

注册信息：

```go
Name:         "http_request"
Category:     CatBuiltin
Source:       "builtin"
Permission:   PermApprove
ParallelSafe: true
```

含义：

- `CatBuiltin`：这是核心内置工具，不依赖 skill 或 MCP。
- `PermApprove`：工具会访问外部 URL，默认需要审批。
- `ParallelSafe=true`：工具不修改本地状态，可以和其他只读网络请求并行。

## 参数

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `url` | 是 | 无 | 要请求的 HTTP 或 HTTPS URL。 |
| `method` | 否 | `GET` | HTTP 方法。默认只允许 `GET`、`HEAD`、`OPTIONS`。 |
| `headers_json` | 否 | 无 | JSON 对象字符串，用来设置请求头。 |
| `body` | 否 | 空字符串 | 请求体字符串，最大 1 MiB。 |
| `timeout` | 否 | `15` | 超时时间，单位秒，最小 1，最大 60。 |
| `allow_mutation` | 否 | `false` | 是否允许 `POST`、`PUT`、`PATCH`、`DELETE`。 |
| `max_response_bytes` | 否 | `32768` | 最大读取响应字节数，最大 1048576。 |
| `format` | 否 | `text` | 输出格式：`text` 或 `json`。 |
| `include_headers` | 否 | `false` | 是否输出响应 headers。 |
| `redact_headers` | 否 | `true` | 输出 headers 时是否脱敏敏感 header。 |

示例参数：

```json
{
  "url": "https://api.example.com/v1/status",
  "method": "GET",
  "timeout": 20
}
```

带请求头和 body：

```json
{
  "url": "https://api.example.com/v1/items",
  "method": "POST",
  "allow_mutation": true,
  "headers_json": "{\"Content-Type\":\"application/json\"}",
  "body": "{\"name\":\"demo\"}"
}
```

## 执行流程

`http_request` 的执行过程是：

1. 读取必填参数 `url`。
2. 如果 URL 为空，返回 `url is required`。
3. 调用 `validateFetchURL(rawURL)` 做 URL 安全校验。
4. 读取 `method`，默认为 `GET`，并转成大写。
5. 调用 `validateHTTPMethod` 检查方法白名单；mutation 方法需要 `allow_mutation=true`。
6. 读取 `timeout` 和 `max_response_bytes` 并限制范围。
7. 读取 `format`、`include_headers`、`redact_headers`。
8. 读取 `body` 字符串，超过 1 MiB 时拒绝。
9. 创建 HTTP request。
10. 默认设置 `User-Agent: luckyagent-http-request`。
11. 如果提供 `headers_json`，解析为 `map[string]string`，校验 header 后设置请求头。
12. 使用带超时和 `CheckRedirect` 的 HTTP client 发送请求；每次 redirect 都重新调用 `validateFetchURL`。
13. 最多读取 `max_response_bytes + 1` 字节，用额外 1 字节判断是否截断。
14. 如果响应体是 JSON，尝试格式化缩进，并在 `format=json` 时作为 JSON 值输出。
15. 输出 HTTP 状态、Content-Type、读取字节数、截断状态和响应体。

## HTTP 方法边界

默认允许：

- `GET`
- `HEAD`
- `OPTIONS`

以下方法必须显式传入 `allow_mutation=true`：

- `POST`
- `PUT`
- `PATCH`
- `DELETE`

其他方法默认拒绝。默认拒绝 POST 的错误示例：

```text
POST requires allow_mutation=true
```

## URL 安全校验

`http_request` 使用和 `web_fetch` 相同的 `validateFetchURL`。

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

这意味着它不能用来访问本机服务、内网服务、metadata endpoint 或非 HTTP(S) URL。

HTTP redirect 目标也会重新执行同一 URL 校验，避免公开 URL 302 到内网地址。

## headers_json

`headers_json` 必须是 JSON 对象字符串，并解析为：

```go
map[string]string
```

示例：

```json
{
  "headers_json": "{\"Accept\":\"application/json\",\"Authorization\":\"Bearer token\"}"
}
```

解析失败时返回：

```text
parse headers_json: <error>
```

用户提供的 header 会用 `req.Header.Set(k, v)` 设置，因此可以覆盖默认 `User-Agent`。

以下 header 不能覆盖：

- `Host`
- `Connection`
- `Content-Length`
- `Transfer-Encoding`

header 名和值不能包含控制字符或换行。

输出响应 headers 时，默认会脱敏：

- `Authorization`
- `Proxy-Authorization`
- `Cookie`
- `Set-Cookie`
- `X-API-Key`
- `X-Auth-Token`

## 输出格式

输出是文本。

基本结构：

```text
Status: 200 OK
Content-Type: application/json
Bytes-Read: 12

{
  "ok": true
}
```

如果响应没有 `Content-Type`，则不输出该行。

如果响应体为空，只返回状态和可能的 content type。

响应体处理规则：

- 最多读取 `max_response_bytes` 原始响应。
- 使用额外 1 字节判断 `truncated`。
- 如果 `json.Valid(data)` 为 true，会用 `json.Indent` 美化文本输出。
- 最终 body 还会经过 `utils.Truncate(bodyText, 12000)`。

截断时文本输出包含：

```text
Truncated: true (limit 32768 bytes)
```

`format=json` 输出：

```json
{
  "status": "200 OK",
  "status_code": 200,
  "content_type": "application/json",
  "bytes_read": 32768,
  "response_limit": 32768,
  "truncated": true,
  "body": {"ok": true}
}
```

`include_headers=true` 时会额外返回 `headers`。

## 和 web_fetch 的关系

`web_fetch` 适合抓取网页正文、文章和文档内容。

`http_request` 适合：

- JSON API
- 非 HTML endpoint
- 需要自定义 method
- 需要请求头或 body
- 需要看 HTTP 状态码

读取网页文章时优先用 `web_fetch`。调接口时优先用 `http_request`。

## 适合使用的场景

优先使用 `http_request` 的场景：

- 查询公开 JSON API。
- 验证接口状态码。
- 发送显式批准的简单 POST/PUT/PATCH 请求。
- 带自定义 header 调试 endpoint。
- 读取不需要浏览器渲染的响应体。

示例：

```json
{
  "url": "https://api.github.com/repos/yurika0211/luckyagent",
  "headers_json": "{\"Accept\":\"application/vnd.github+json\"}"
}
```

## 不适合使用的场景

不优先使用 `http_request` 的场景：

- 访问本机或内网服务，安全校验会拒绝。
- 抓取网页正文，应使用 `web_fetch`。
- 需要浏览器登录态、点击或渲染，应使用浏览器/OpenCLI 类工具。
- 需要下载大文件，响应体读取受 `max_response_bytes` 限制。

## 风险和注意事项

`http_request` 的主要注意点：

- 默认需要审批。
- URL 会做 SSRF 风险控制。
- redirect 目标会重新做 SSRF 风险控制。
- 默认偏读取；mutation 方法必须显式 `allow_mutation=true`。
- 超时最大 60 秒。
- 请求体最大 1 MiB。
- 响应体默认最多读取 32 KiB，可调到 1 MiB；最终文本输出最多约 12000 字符。
- 不区分 2xx/4xx/5xx，都会返回响应状态和 body。
- 敏感响应 headers 默认只在输出中脱敏，不影响实际请求。

## 维护注意事项

如果后续修改 `http_request`，需要同步检查：

- 参数表是否仍与 `HTTPRequestTool()` 一致。
- URL 校验是否仍使用 `validateFetchURL`。
- redirect 是否仍重新校验 URL。
- timeout 默认值和最大值是否变化。
- request body 和 response body 上限是否变化。
- 输出截断是否仍是 12000 字符。
- 默认 User-Agent 是否变化。
- 返回 header 范围是否变化。
