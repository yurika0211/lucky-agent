# document_read Tool

`document_read` 是 LuckyAgent 的内置本地文档文本提取工具，用来从 PDF、DOCX、PPTX 文件中抽取可读文本。它适合在 Office 文档或 PDF 是事实来源时使用。

它和 `file_read` 的定位不同：`file_read` 直接读取普通文本文件；`document_read` 会先解析文档格式，再把提取出的文本按行返回。

## 工具定义

实现位置：

- `internal/tool/builtin_document.go`

注册信息：

```go
Name:         "document_read"
Category:     CatBuiltin
Source:       "builtin"
Permission:   PermAuto
ShellAware:   true
ParallelSafe: true
```

含义：

- `CatBuiltin`：这是核心内置工具，不依赖 skill 或 MCP。
- `PermAuto`：文本提取是只读操作，默认可以自动执行。
- `ShellAware`：agent 可以向工具注入当前工作目录 `_cwd`，相对路径会基于该目录解析。
- `ParallelSafe=true`：工具本身不修改共享状态，可以并发读取不同文档。

## 参数

`document_read` 接收三个参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `path` | 是 | 无 | 本地 `.pdf`、`.docx` 或 `.pptx` 文件路径。 |
| `offset` | 否 | `1` | 起始行号，基于提取后的文本行，按 1 开始计数。 |
| `limit` | 否 | `2000` | 最多返回多少行提取文本。 |

示例参数：

```json
{
  "path": "docs/report.pdf",
  "offset": 1,
  "limit": 120
}
```

## 支持格式

当前支持：

- `.pdf`
- `.docx`
- `.pptx`

明确不支持：

- `.doc`
- `.ppt`

如果传入 `.doc`，会提示先转换为 `.docx`。如果传入 `.ppt`，会提示先转换为 `.pptx`。

其他扩展名会返回：

```text
unsupported document format "<ext>"; supported formats: .pdf, .docx, .pptx
```

## 执行流程

`document_read` 的执行过程是：

1. 读取必填参数 `path`，没有提供时返回 `path is required`。
2. 通过 `resolvePathArg(args, "path")` 解析路径。
3. 调用 `os.Stat` 确认路径存在。
4. 如果路径是目录，返回错误。
5. 如果文件大于 50MB，返回错误。
6. 按扩展名选择文档解析方式。
7. 对提取文本做规范化。
8. 如果没有可提取文本，返回错误。
9. 根据 `offset` 和 `limit` 截取提取后的文本行。
10. 输出文档路径、格式、总行数和带行号的文本。

## 提取方式

### PDF

PDF 通过系统命令 `pdftotext` 提取：

```sh
pdftotext -layout -enc UTF-8 <path> -
```

要求运行环境中能找到 `pdftotext`。如果没有安装，会返回：

```text
pdf text extraction requires pdftotext; install poppler-utils or convert the PDF to text first
```

PDF 提取有 30 秒超时。超时会返回：

```text
pdftotext timed out after 30 seconds
```

### DOCX

DOCX 按 ZIP 包读取 Office XML 内容，当前提取这些部分：

- `word/document.xml`
- `word/footnotes.xml`
- `word/endnotes.xml`

工具会从 XML 文本节点中抽取正文、脚注和尾注文本。

### PPTX

PPTX 按 ZIP 包读取幻灯片 XML：

- `ppt/slides/slide*.xml`

工具会按 slide 编号排序，并在输出中插入类似：

```text
[Slide 1]
...
```

## 文本规范化

提取出的文本会经过规范化：

- `\r\n` 和 `\r` 统一为 `\n`。
- 每行会用 `strings.Fields` 折叠多余空白。
- 连续空行会压缩为一个空行。
- 首尾空白会被去掉。

因此输出适合阅读和后续引用，但不保留原文档的精确排版。

## 输出格式

输出头部包含文档信息：

```text
Document: /path/to/report.pdf
Format: pdf
Lines: 86
```

如果只显示部分行，会显示范围：

```text
Lines: 300 (showing 1-120)
```

正文部分带 1-based 行号：

```text
1| Title
2| Section heading
3| Paragraph text
```

如果还有后续内容，会在末尾提示：

```text
... truncated; use offset=121 to continue
```

如果 `offset` 超过提取文本总行数，会返回：

```text
offset <N> exceeds extracted text line count <M>
```

## 路径和大小限制

`document_read` 和其他文件工具共用路径解析逻辑：

- 支持 `~` 和 `~/...` 展开到当前用户 home。
- 相对路径优先相对 `_cwd` 解析。
- 路径清理后如果包含 `..`，会被拒绝。
- 最终路径必须通过 `validateSandbox`。

当前文件大小限制是 50MB：

```go
const maxDocumentReadBytes = 50 * 1024 * 1024
```

超过限制会返回：

```text
document is too large (<size> bytes, max 52428800)
```

## 适合使用的场景

优先使用 `document_read` 的场景：

- 阅读 PDF 报告、论文、合同、说明书中的可复制文本。
- 提取 DOCX 文档正文、脚注、尾注。
- 查看 PPTX 幻灯片中的文字内容。
- 按行读取较长文档的一部分。
- 在回答前确认本地文档里的真实内容。

示例：

```json
{
  "path": "docs/spec.docx",
  "offset": 1,
  "limit": 200
}
```

## 不适合使用的场景

不优先使用 `document_read` 的场景：

- 普通文本、Markdown、代码文件：使用 `file_read`。
- 扫描版 PDF 或图片型 PDF：使用 OCR 或 `image_analyze` 更合适。
- 图片、截图、图表识别：使用 `image_analyze`。
- 需要保留完整排版、表格结构、批注、样式：当前工具只抽取文本。
- 需要修改文档内容：`document_read` 只读，不负责编辑。
- 需要解析 `.doc` 或 `.ppt`：先转换为 `.docx` 或 `.pptx`。

## 常见调用示例

读取 PDF 开头：

```json
{
  "path": "docs/report.pdf",
  "offset": 1,
  "limit": 100
}
```

继续读取长文档：

```json
{
  "path": "docs/report.pdf",
  "offset": 101,
  "limit": 100
}
```

读取 DOCX：

```json
{
  "path": "docs/proposal.docx",
  "offset": 1,
  "limit": 200
}
```

读取 PPTX：

```json
{
  "path": "docs/slides.pptx",
  "offset": 1,
  "limit": 200
}
```

## 和 file_read 的关系

当文件是纯文本时，使用 `file_read`。

当文件是 PDF、DOCX 或 PPTX 时，使用 `document_read`。

不要用 `file_read` 直接读取 Office 文档或 PDF，因为它会把二进制内容当作普通字符串处理，输出通常不可读。`document_read` 会先理解文档容器格式，再返回提取后的文本。

## 和 terminal 的关系

不要优先用 `terminal` 手写：

```sh
pdftotext file.pdf -
```

除非你需要调试 `pdftotext` 本身、查看版本、确认安装状态，或者执行 `document_read` 不支持的特殊提取参数。

正常文档阅读应使用 `document_read`，因为它提供统一的路径解析、大小限制、格式分派、输出行号和分页读取。

## 维护注意事项

如果后续修改 `document_read`，需要同步检查：

- 支持格式是否变化。
- 50MB 文件大小限制是否变化。
- PDF 是否仍依赖 `pdftotext`。
- PDF 提取超时时间是否变化。
- DOCX 提取的 XML 部件是否变化。
- PPTX slide 排序和 `[Slide N]` 输出是否变化。
- 文本规范化策略是否变化。
- 输出头部和行号格式是否变化。
- 路径解析和 sandbox 规则是否变化。

