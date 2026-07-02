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
| `method` | 否 | `GET` | HTTP 方法，例如 `GET`、`POST`、`PUT`、`PATCH`、`DELETE`。 |
| `headers_json` | 否 | 无 | JSON 对象字符串，用来设置请求头。 |
| `body` | 否 | 空字符串 | 请求体字符串。 |
| `timeout` | 否 | `15` | 超时时间，单位秒，最小 1，最大 60。 |

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
5. 读取 `timeout`，通过 `boundedIntArg(args, "timeout", 15, 1, 60)` 限制范围。
6. 读取 `body` 字符串。
7. 创建 HTTP request。
8. 默认设置 `User-Agent: luckyagent-http-request`。
9. 如果提供 `headers_json`，解析为 `map[string]string` 并设置请求头。
10. 使用带超时的 HTTP client 发送请求。
11. 最多读取响应体前 32 KiB。
12. 如果响应体是 JSON，尝试格式化缩进。
13. 输出 HTTP 状态、Content-Type 和响应体。

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

## 输出格式

输出是文本。

基本结构：

```text
Status: 200 OK
Content-Type: application/json

{
  "ok": true
}
```

如果响应没有 `Content-Type`，则不输出该行。

如果响应体为空，只返回状态和可能的 content type。

响应体处理规则：

- 最多读取 32 KiB 原始响应。
- 如果 `json.Valid(data)` 为 true，会用 `json.Indent` 美化。
- 最终 body 还会经过 `utils.Truncate(bodyText, 12000)`。

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
- 发送简单 POST/PUT/PATCH 请求。
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
- 需要下载大文件，响应体读取限制为 32 KiB。
- 需要查看完整 headers，当前只返回 `Content-Type`。

## 风险和注意事项

`http_request` 的主要注意点：

- 默认需要审批。
- URL 会做 SSRF 风险控制。
- 超时最大 60 秒。
- 响应体最多读取 32 KiB，最终输出最多约 12000 字符。
- 不区分 2xx/4xx/5xx，都会返回响应状态和 body。
- `method` 只做大写转换，不限制方法白名单。

## 维护注意事项

如果后续修改 `http_request`，需要同步检查：

- 参数表是否仍与 `HTTPRequestTool()` 一致。
- URL 校验是否仍使用 `validateFetchURL`。
- timeout 默认值和最大值是否变化。
- 响应体读取上限是否仍是 32 KiB。
- 输出截断是否仍是 12000 字符。
- 默认 User-Agent 是否变化。
- 返回 header 范围是否变化。
