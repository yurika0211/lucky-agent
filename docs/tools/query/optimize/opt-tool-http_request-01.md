# opt-tool-http_request-01

## 目标

优化 `http_request` 的 HTTP 方法边界、SSRF 防护、请求/响应大小限制、header 安全、输出结构和诊断能力，让它继续保持“受控访问公开 HTTP(S) API，需要审批”的定位，同时降低误调用 mutation API、泄漏敏感 header、响应截断不可见和 URL 校验绕过的风险。

本方案聚焦：

- method allowlist 和危险方法提示
- URL / redirect / DNS 安全
- headers_json 安全过滤
- request body 和 response body 大小限制
- JSON / text 输出格式
- 状态码和响应 metadata
- 测试矩阵补齐

## 当前状态

相关实现：

- `internal/tool/builtin_query.go`
- `docs/tools/query/http_request.md`

当前 handler 流程：

1. 读取必填 `url`。
2. 调用 `validateFetchURL(rawURL)`。
3. 读取 `method`，默认 `GET`，转大写。
4. 读取 `timeout`，限制在 1 到 60 秒。
5. 读取 `body`。
6. 创建 HTTP request。
7. 设置默认 `User-Agent: luckyagent-http-request`。
8. 解析 `headers_json` 为 `map[string]string` 并设置到 request。
9. 使用 `http.Client{Timeout: ...}` 发送请求。
10. 最多读取响应体前 32 KiB。
11. 如果响应体是 JSON，格式化缩进。
12. 返回 status、content-type 和 body 文本。

当前优势：

- 工具需要审批。
- URL 已经过 `validateFetchURL`。
- timeout 有上下限。
- 响应体读取有 32 KiB 上限。
- JSON body 会自动 pretty print。

## 主要问题

### 1. method 不做 allowlist

当前 method 只做大写转换，任何字符串都可能传给 `http.NewRequestWithContext`。

风险：

- 非标准 method。
- `DELETE`、`PATCH`、`POST` 等 mutation 请求被轻易执行。
- 用户以为它主要是读取工具，但实际可写外部服务。

建议区分 read 和 mutation：

- 默认允许 `GET`、`HEAD`、`OPTIONS`。
- `POST`、`PUT`、`PATCH`、`DELETE` 要求显式 `allow_mutation=true` 或额外审批说明。

### 2. redirect 后没有重新校验 URL

`http.Client` 默认会跟随重定向。初始 URL 经过 `validateFetchURL`，但 redirect 目标不一定再校验。

风险：

- 公网 URL 302 到内网或 metadata endpoint。
- DNS rebinding / redirect 绕过 SSRF 校验。

建议自定义 `CheckRedirect`，每次 redirect 后重新校验。

### 3. DNS 解析安全不完整

如果 `validateFetchURL` 主要基于字面 host/IP 判断，域名解析到私网 IP 可能绕过。

建议在 HTTP transport DialContext 层做解析后 IP 检查，或至少在 `validateFetchURL` 内补 DNS 解析。

### 4. headers_json 允许覆盖任意 header

当前用户 header 可以覆盖 `User-Agent`，也可以传入 `Authorization`、`Cookie` 等敏感 header。

这不一定错误，但需要可见策略：

- 是否允许带认证信息。
- 是否在日志和输出中脱敏。
- 是否拒绝危险 header，例如 `Host`、`Connection`。

### 5. request body 没有大小限制

当前 body 是字符串，未限制长度。超大 body 会增加内存和网络风险。

建议限制默认 1 MiB 或更小。这个工具定位是轻量 API 调试，不是上传工具。

### 6. 响应截断不可见

读取使用 `io.LimitReader(resp.Body, 32*1024)`，但输出没有标注是否截断。用户可能误以为返回了完整响应。

建议输出：

- `bytes_read`
- `response_limit`
- `truncated`

### 7. 输出不是结构化 JSON

当前输出是文本，后续工具链难以解析状态码、headers、body。

建议支持 `format=json`。

### 8. 缺少响应 header 控制

当前只输出 `Content-Type`。排查 API 时常需要：

- rate limit header
- location
- request id
- cache header

建议支持 `include_headers=true` 和 header allowlist。

## 优化原则

1. `http_request` 只访问公开 HTTP(S) URL。
2. 默认偏读取，mutation 必须显式。
3. SSRF 防护要覆盖 redirect 和 DNS。
4. 默认不泄漏敏感 header。
5. 截断必须可见。
6. 文本输出兼容，JSON 输出用于程序化处理。

## 推荐方案

### 1. 参数扩展

新增参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `format` | 否 | `text` | 输出 `text` 或 `json`。 |
| `allow_mutation` | 否 | `false` | 是否允许 POST/PUT/PATCH/DELETE。 |
| `max_response_bytes` | 否 | `32768` | 最大读取响应字节数。 |
| `include_headers` | 否 | `false` | 是否返回响应 headers。 |
| `redact_headers` | 否 | `true` | 是否脱敏敏感请求/响应 header。 |

### 2. method 校验

新增：

```go
func validateHTTPMethod(method string, allowMutation bool) error
```

规则：

- 始终允许 `GET`、`HEAD`、`OPTIONS`。
- `POST`、`PUT`、`PATCH`、`DELETE` 需要 `allow_mutation=true`。
- 其他 method 默认拒绝。

### 3. redirect 校验

创建 client 时设置：

```go
client := &http.Client{
	Timeout: timeout,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return validateFetchURL(req.URL.String())
	},
}
```

并限制最多 10 次 redirect。

### 4. header 安全

新增：

```go
func validateRequestHeader(name, value string) error
```

建议：

- 拒绝空 header 名。
- 拒绝控制字符。
- 默认拒绝覆盖 `Host`、`Connection`。
- 输出时脱敏 `Authorization`、`Cookie`、`Set-Cookie`、`X-API-Key`。

### 5. body 和 response 限制

建议常量：

```go
const (
	maxHTTPRequestBodyBytes = 1 << 20
	defaultHTTPResponseBytes = 32 << 10
	maxHTTPResponseBytes = 1 << 20
)
```

读取时使用 limit + 额外 1 字节判断是否截断。

### 6. JSON 输出

示例：

```json
{
  "status": "200 OK",
  "status_code": 200,
  "content_type": "application/json",
  "bytes_read": 32768,
  "truncated": true,
  "body": {"ok": true}
}
```

如果 body 不是 JSON，`body` 为字符串。

## 分阶段实施

### 第一阶段：安全边界

- method allowlist。
- mutation 需要 `allow_mutation=true`。
- redirect 后重新校验。
- body 长度限制。

### 第二阶段：输出增强

- `format=json`。
- `truncated` metadata。
- include_headers 和 header 脱敏。

### 第三阶段：DNS 防护

- 在 URL 校验或 DialContext 中补 DNS 解析后 IP 检查。
- 增加 redirect 到内网的测试。

## 测试建议

- URL 为空时报错。
- localhost / 私网 URL 被拒绝。
- redirect 到私网 URL 被拒绝。
- 默认 `POST` 被拒绝。
- `allow_mutation=true` 允许 POST。
- 非标准 method 被拒绝。
- headers_json 非对象时报错。
- 敏感 header 输出被脱敏。
- response 超过上限时 `truncated=true`。
- `format=json` 返回 status_code 和 body。

## 文档更新

同步更新：

- `docs/tools/query/http_request.md`
- 参数表新增 `format`、`allow_mutation`、`max_response_bytes`
- redirect SSRF 说明
- header 脱敏说明
- 截断 metadata 示例

## 风险与边界

- 默认拒绝 POST 可能影响旧用法，需要错误信息指向 `allow_mutation=true`。
- DNS 防护可能增加延迟。
- header 脱敏不能影响实际请求，只影响输出和日志。

## 推荐结论

优先补 method allowlist、redirect 校验和截断 metadata。这三项直接影响网络安全和结果可信度。随后补 JSON 输出和 header 控制，让 `http_request` 更适合 API 调试和自动化链路。
