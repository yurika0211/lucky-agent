# image_analyze Tool

`image_analyze` 是 LuckyAgent 的内置多模态分析工具，用来分析图片、截图、图表和简单文档。它可以提取可见文字、总结 UI 或视觉内容，并返回模型识别到的关键信号。

这是只读工具，不写入文件，因此被标记为自动批准。

## 工具定义

实现位置：

- `internal/tool/builtin_media.go`
- `internal/agent/agent.go`

注册信息：

```go
Name:         "image_analyze"
Category:     CatBuiltin
Source:       "builtin"
Permission:   PermAuto
ParallelSafe: true
```

含义：

- `CatBuiltin`：这是核心内置工具，不依赖 skill 或 MCP。
- `PermAuto`：分析输入内容是只读操作，默认可以自动执行。
- `ParallelSafe=true`：工具不修改共享状态，可以和其他只读工具并行。

## 参数

`image_analyze` 至少需要 `path`、`url`、`base64_data` 中的一个。

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `path` | 否 | 无 | 本地图片或文档路径。 |
| `url` | 否 | 无 | 远程图片或文档 URL。 |
| `base64_data` | 否 | 无 | 已经在内存中的文件内容，使用 Base64 编码。 |
| `mime_type` | 否 | 自动推断 | MIME 类型，例如 `image/png` 或 `application/pdf`。 |
| `provider` | 否 | 配置默认值 | 多模态 provider 名称覆盖。 |

示例参数：

```json
{
  "path": "/tmp/screenshot.png",
  "mime_type": "image/png"
}
```

或：

```json
{
  "base64_data": "ZmFrZS1pbWFnZS1ieXRlcw==",
  "mime_type": "image/png"
}
```

## 执行流程

`image_analyze` 的执行过程是：

1. 检查 `multimodal.Processor` 是否已配置。
2. 读取 `path`、`url`、`base64_data`、`mime_type`。
3. 如果三种输入来源都为空，返回 `one of path, url, or base64_data is required`。
4. 根据 `mime_type` 或文件扩展名推断输入 modality。
5. 如果是本地 `path`，先执行 `validatePath(path)`，再构造本地输入。
6. 如果是 `url`，直接构造 URL 输入。
7. 如果是 `base64_data`，先解码，再通过 `http.DetectContentType` 推断 MIME。
8. 设置 metadata，例如 `file_path`、`filename` 或 `url`。
9. 创建 90 秒超时的 context。
10. 如果传入 `provider`，调用 `AnalyzeWithProvider`。
11. 如果没有传入 `provider`，使用配置的默认 provider；仍为空时调用 processor 默认分析路径。
12. 格式化 `AnalysisResult` 为文本返回。

## 输入优先级

当多个输入来源同时提供时，当前实现按以下顺序选择：

1. `path`
2. `url`
3. `base64_data`

也就是说，如果同时传入 `path` 和 `url`，实际会使用 `path`。

## modality 推断

当前只有两类推断：

| 条件 | modality |
| --- | --- |
| `mime_type == application/pdf` | `document` |
| 文件扩展名是 `.pdf` | `document` |
| 其他情况 | `image` |

本地路径输入会根据扩展名补充 MIME：

```go
mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
```

Base64 输入如果没有传 `mime_type`，会使用：

```go
http.DetectContentType(data)
```

## provider 选择

`image_analyze` 支持显式传入：

```json
{
  "provider": "openai-media"
}
```

如果没有传 `provider`，handler 会使用注册工具时传入的默认 provider。正常 agent 初始化时，该默认值来自：

```json
{
  "multimodal": {
    "image_provider": ""
  }
}
```

如果默认 provider 也为空，则调用 `processor.Analyze(ctx, input)`，由 processor 自己选择可用 provider。

## 输出格式

成功输出是多行文本。

可能包含：

```text
Modality: image
Summary: ...
Visible text / analysis:
...
Labels: ...
Confidence: 0.95
Model: ...
Source: ...
```

字段来自 `multimodal.AnalysisResult`：

- `Modality`
- `Summary`
- `Text`
- `Labels`
- `Confidence`
- `Metadata["model"]`
- `Metadata["source"]`

可见文字或分析正文会被截断到 4000 字符：

```go
utils.Truncate(text, 4000)
```

如果 result 为空，返回：

```text
Image analysis unavailable.
```

## 配置

相关配置位于：

```json
{
  "multimodal": {
    "provider": "openai",
    "api_key": "",
    "api_base": "",
    "image_model": "gpt-5.4-mini",
    "transcription_model": "whisper-1",
    "image_provider": ""
  }
}
```

agent 初始化时会始终注册一个 local provider，并在 OpenAI 多模态配置可用时注册 OpenAI media provider。

注意：如果 `processor` 为 nil，工具会返回：

```text
image analysis is not configured
```

## 适合使用的场景

优先使用 `image_analyze` 的场景：

- 分析截图里的错误信息。
- 读取图片或扫描件里的可见文字。
- 总结 UI 页面、图表、表格截图。
- 判断图片中明显的结构、标签和状态。
- 从本地图片、远程图片或 Base64 图片中提取信息。

示例：

```json
{
  "path": "screenshots/error.png"
}
```

## 不适合使用的场景

不优先使用 `image_analyze` 的场景：

- 需要生成新图片，应使用 `image_generate`。
- 需要读取文本型 PDF 的完整正文，应优先考虑 `document_read`。
- 需要 OCR 大批量文件，应使用专门 OCR 流程。
- 需要下载网页正文，应使用 `web_fetch`。
- 需要读取本地纯文本文件，应使用 `file_read`。

## 和 document_read 的关系

`image_analyze` 可以处理 PDF 输入，但它定位是多模态分析，适合扫描件、截图式文档和需要视觉理解的内容。

如果文档本身是文本型 PDF、Markdown、TXT、DOCX 等，`document_read` 更适合做正文提取。

## 风险和注意事项

`image_analyze` 的主要注意点：

- 需要已配置可用的 multimodal processor。
- 本地 `path` 会经过路径校验。
- URL 输入没有在此处执行 `validateFetchURL`。
- `path`、`url`、`base64_data` 同时提供时，只有优先级最高的输入会被使用。
- 输出不是结构化 JSON，而是面向阅读的文本。
- 分析正文最多保留 4000 字符。
- 远程 provider 的准确度、费用和延迟取决于实际配置。

## 维护注意事项

如果后续修改 `image_analyze`，需要同步检查：

- 输入参数是否仍是 `path`、`url`、`base64_data`、`mime_type`、`provider`。
- 三种输入来源的优先级是否变化。
- 本地路径是否仍调用 `validatePath`。
- URL 输入是否新增安全校验。
- modality 推断规则是否扩展。
- context timeout 是否仍是 90 秒。
- 输出字段和 4000 字符截断规则是否变化。
- 默认 provider 是否仍来自 `multimodal.image_provider`。
