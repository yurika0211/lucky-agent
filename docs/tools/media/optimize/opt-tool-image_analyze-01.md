# opt-tool-image_analyze-01

## 目标

优化 `image_analyze` 的输入选择、远程 URL 安全、MIME 校验、输出结构和测试覆盖，让它继续保持“只读、多模态分析、可自动批准”的定位，同时降低输入歧义、SSRF 风险、provider 诊断不足和结果不可机器消费的问题。

本方案聚焦：

- 输入来源互斥和优先级
- URL SSRF 防护
- 文件大小和 MIME 类型校验
- provider 选择诊断
- 输出结构化 metadata
- 与 `document_read` 的边界
- 测试矩阵补齐

## 当前状态

相关实现：

- `internal/tool/builtin_media.go`
- `internal/tool/builtin_test.go`
- `docs/tools/media/image_analyze.md`

当前 `image_analyze` 支持三种输入：

- `path`
- `url`
- `base64_data`

当前 handler 流程：

1. 检查 `multimodal.Processor` 是否配置。
2. 调用 `buildImageAnalyzeInput` 构造输入。
3. 创建 90 秒 timeout context。
4. 如果传入 `provider`，调用 `AnalyzeWithProvider`。
5. 如果没有传入 `provider`，使用工具注册时的默认 provider。
6. 默认 provider 为空时调用 `processor.Analyze`。
7. 将 `AnalysisResult` 格式化为文本返回。

当前优势：

- 工具是只读操作，适合 `PermAuto`。
- 本地 `path` 会经过 `validatePath`。
- 支持本地文件、URL 和 Base64 三类输入。
- 支持默认 provider 和显式 provider。
- 输出对人类阅读友好。

## 主要问题

### 1. 多输入来源存在隐式优先级

当前同时传入多个输入时，按以下顺序选择：

1. `path`
2. `url`
3. `base64_data`

这会导致调用方以为分析的是 URL，实际分析了 path。对于 agent 自动构造参数的场景，隐式优先级不利于排错。

建议改成：

- 默认要求三种输入来源只能出现一种。
- 如需兼容旧行为，可先输出 warning metadata。
- 长期收敛为互斥校验。

### 2. URL 输入没有经过 `validateFetchURL`

`image_generate` 的 `input_url` 已经调用 `validateFetchURL`，但 `image_analyze` 的 `url` 当前直接构造 `multimodal.NewInputFromURL`。

风险：

- localhost / 私网地址访问。
- link-local / metadata service 地址访问。
- DNS rebinding 或 redirect 到内网地址。
- 不同 provider 对 URL 拉取的安全策略不可控。

即使实际下载由 provider 或 processor 完成，agent 层也应先做 URL 字面和 DNS 级校验。

### 3. 缺少文件大小限制

本地 path 和 base64 输入目前没有明确大小上限。风险：

- 超大图片或 PDF 进入多模态 provider。
- Base64 参数占用大量上下文和内存。
- 大文件导致请求成本不可控。

建议设置默认上限，例如：

- 图片：20 MiB
- PDF / document：50 MiB
- Base64 解码后同样执行大小限制

### 4. MIME 校验较弱

当前 MIME 推断主要依赖：

- `mime_type`
- 文件扩展名
- `http.DetectContentType`

问题：

- 显式 `mime_type` 和实际内容不一致时没有诊断。
- 非图片文件可能被当成 image。
- `.pdf` 以外的文档类型边界不清晰。

建议补充 MIME allowlist：

- `image/png`
- `image/jpeg`
- `image/webp`
- `image/gif`
- `application/pdf`

其他类型默认拒绝，或要求显式 `allow_unknown_mime=true`。

### 5. 输出只有文本，缺少结构化结果

当前输出适合人读，但后续链路难以稳定解析：

- provider
- model
- modality
- confidence
- labels
- source
- truncated
- text length

建议保留默认文本输出，同时支持 `format=json`。

### 6. provider 失败诊断不足

当前 provider 选择逻辑本身清晰，但失败时很难判断：

- 是 processor 未配置。
- 是 provider 名称不存在。
- 是 provider 超时。
- 是输入类型不被 provider 支持。
- 是远程 provider 拒绝 URL。

建议在错误包装中加入 provider 名称和输入类型，不泄漏 API key。

### 7. 与 `document_read` 的边界可再明确

当前 `image_analyze` 可处理 PDF，但定位是视觉理解。文本型 PDF、DOCX、TXT 等应优先走 `document_read`。

建议在工具描述和 router 提示里强调：

- 扫描件、截图式 PDF、图表：`image_analyze`
- 文本型文档正文提取：`document_read`

## 优化原则

1. `image_analyze` 保持只读，不落盘，不改变文件。
2. 自动批准成立的前提是输入安全校验足够明确。
3. 三类输入来源应避免隐式覆盖。
4. MIME 和大小限制要在调用 provider 前完成。
5. 文本输出保持兼容，结构化输出作为可选能力。
6. 不把 `image_analyze` 扩展成通用文档读取工具。

## 推荐方案

### 1. 抽出参数解析

新增：

```go
type imageAnalyzeOptions struct {
	Path       string
	URL        string
	Base64Data string
	MIMEType   string
	Provider   string
	Format     string
}

func parseImageAnalyzeOptions(args map[string]any) (imageAnalyzeOptions, error)
```

行为：

- `path`、`url`、`base64_data` 至少一个。
- 默认只允许一个输入来源。
- `format` 默认 `text`，支持 `json`。
- 所有字符串统一 `strings.TrimSpace`。

### 2. 对 URL 输入使用统一 fetch 校验

在 `buildImageAnalyzeInput` 的 URL 分支增加：

```go
if err := validateFetchURL(url); err != nil {
	return nil, fmt.Errorf("url validation failed: %w", err)
}
```

后续可把 URL 下载收敛到 agent 侧，避免把 URL 直接交给 provider：

1. agent 校验 URL。
2. agent 下载并限制大小。
3. agent sniff MIME。
4. 以 bytes 输入 provider。

这样可以统一 SSRF、redirect、timeout 和 MIME 策略。

### 3. 增加大小限制

建议常量：

```go
const (
	maxImageAnalyzeImageBytes = 20 << 20
	maxImageAnalyzeDocBytes   = 50 << 20
)
```

检查位置：

- 本地 path：`os.Stat` 后检查 size。
- Base64：解码后检查 `len(data)`。
- URL：如果改为 agent 侧下载，读取时用 `io.LimitReader`。

错误示例：

```text
image_analyze input exceeds 20 MiB limit for image/png
```

### 4. 增加 MIME allowlist

新增：

```go
func validateImageAnalyzeMIME(mimeType string) error
```

默认允许：

- `image/png`
- `image/jpeg`
- `image/webp`
- `image/gif`
- `application/pdf`

对带参数的 Content-Type 先解析主类型：

```go
mediaType, _, err := mime.ParseMediaType(mimeType)
```

### 5. 增加结构化输出

新增输出 payload：

```go
type imageAnalyzePayload struct {
	Modality   string            `json:"modality"`
	Summary    string            `json:"summary,omitempty"`
	Text       string            `json:"text,omitempty"`
	Labels     []string          `json:"labels,omitempty"`
	Confidence float64           `json:"confidence,omitempty"`
	Provider   string            `json:"provider,omitempty"`
	Model      string            `json:"model,omitempty"`
	Source     string            `json:"source,omitempty"`
	Truncated  bool              `json:"truncated"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}
```

默认 `format=text` 继续返回当前文本；`format=json` 返回结构化结果。

### 6. provider 诊断增强

错误包装建议：

```go
return "", fmt.Errorf("image_analyze provider %q failed for %s input: %w", providerName, input.Modality, err)
```

如果 provider 为空，则使用：

```text
image_analyze default provider failed for image input: ...
```

### 7. router 提示增强

工具描述可以补充：

- 对截图、UI、图表、扫描件使用 `image_analyze`。
- 对文本型 PDF、DOCX、TXT 使用 `document_read`。
- 对远程网页正文使用 `web_fetch`。

## 分阶段实施

### 第一阶段：安全和参数收敛

- 增加 `parseImageAnalyzeOptions`。
- 对 URL 调用 `validateFetchURL`。
- 增加输入来源互斥校验。
- 增加 base64 解码后大小限制。

### 第二阶段：MIME 和输出增强

- 增加 MIME allowlist。
- 增加 `format=json`。
- 输出 truncation metadata。
- provider 错误信息加入 provider 和 modality。

### 第三阶段：URL 下载统一

- 将 URL 输入改为 agent 侧安全下载。
- 统一 redirect 校验、timeout、size limit。
- 不再让 provider 自行拉 URL，除非 provider 明确要求且已通过校验。

## 测试建议

新增或补齐测试：

- `path`、`url`、`base64_data` 都为空时报错。
- 多输入来源同时出现时报错。
- `url` 为 localhost / 私网地址时报错。
- base64 非法时报错。
- base64 解码后超过大小限制时报错。
- MIME 不在 allowlist 时拒绝。
- `provider` 显式传入时调用 `AnalyzeWithProvider`。
- 默认 provider 为空时调用 `Analyze`。
- `format=json` 返回结构化字段。
- 长文本截断时 `truncated=true`。

测试应使用 fake multimodal processor，不依赖真实 provider 或网络。

## 文档更新

同步更新：

- `docs/tools/media/image_analyze.md`
- tool 参数表
- 风险和注意事项
- 与 `document_read` 的边界说明
- URL 安全策略
- `format=json` 输出示例

## 风险与边界

- 输入来源互斥可能影响依赖旧优先级的调用方，可先灰度为 warning。
- URL 改为 agent 侧下载后，部分 provider 原生 URL 处理能力不会直接使用。
- MIME allowlist 可能拒绝少见格式，需根据实际需求逐步放开。
- 结构化输出不应破坏默认文本输出。

## 推荐结论

优先做 URL 安全校验、输入来源互斥和大小限制。这三项直接决定 `PermAuto` 是否站得住。随后补 `format=json` 和 provider 诊断，让 `image_analyze` 从“能看图”升级为“可安全、可调试、可被后续链路稳定消费”的多模态读取工具。
