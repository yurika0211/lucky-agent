# opt-tool-document_read-01

## 目标

优化 `document_read` 的格式识别、文本提取质量、错误提示和测试覆盖，让它继续保持“只读、本地文档文本提取、自动批准”的定位，同时更可靠地区分 PDF、DOCX、PPTX 和伪装或损坏文件。

本方案聚焦：

- DOCX/PPTX 的 ZIP 容器校验
- Office XML 提取覆盖范围
- PDF 依赖和错误提示
- 参数边界和输出一致性
- 测试矩阵补齐

## 当前状态

相关实现：

- `internal/tool/builtin_document.go`
- `internal/tool/builtin_test.go`
- `docs/tools/document_read.md`

当前 `document_read` 的流程：

1. 通过 `resolvePathArg(args, "path")` 解析路径。
2. `os.Stat` 检查路径存在、不是目录、大小不超过 50MB。
3. 根据扩展名分派到 PDF、DOCX、PPTX 提取逻辑。
4. 对提取出的文本做规范化。
5. 根据 `offset` 和 `limit` 输出带行号文本。

当前支持：

- `.pdf`
- `.docx`
- `.pptx`

明确不支持：

- `.doc`
- `.ppt`

当前 DOCX/PPTX 实现依赖 Office Open XML 的事实：`.docx` 和 `.pptx` 本质上是 ZIP 容器，里面存放 XML 文档部件。

## 主要问题

### 1. 扩展名和真实格式没有分开说明

当前 `ExtractDocumentText` 先看扩展名。如果扩展名是 `.docx`，就直接 `zip.OpenReader(path)`。

这对合法 DOCX 是正确的，但对下面情况错误提示还不够友好：

- 旧版 `.doc` 被改名成 `.docx`。
- HTML、RTF、TXT 被伪装成 `.docx`。
- 下载失败的错误页被保存成 `.docx`。
- 文件损坏，不再是合法 ZIP。

当前错误类似：

```text
open docx: zip: not a valid zip file
```

这个错误是真实的，但对用户来说不够解释性。应该明确说明：现代 `.docx` 必须是 Office Open XML ZIP 包。

### 2. DOCX 提取部件覆盖偏窄

当前 DOCX 只提取：

- `word/document.xml`
- `word/footnotes.xml`
- `word/endnotes.xml`

实际常见文本还可能在：

- `word/header*.xml`
- `word/footer*.xml`
- `word/comments.xml`
- `word/textbox` 相关 drawing 节点中的文本

第一版不一定要全部支持，但需要把“当前只提正文、脚注、尾注”的边界写清楚，并考虑补齐页眉页脚。

### 3. PPTX 只读 slide XML，缺少备注页和讲者备注

当前 PPTX 只提取：

- `ppt/slides/slide*.xml`

常见可读文本还可能在：

- `ppt/notesSlides/notesSlide*.xml`
- slide layout 或 master 中的默认文本

对 Agent 阅读汇报材料来说，讲者备注有时很重要。建议优先支持 notes slide，layout/master 可以暂不支持。

### 4. XML 文本抽取过于通用

当前 `extractTextFromOfficeXML` 对所有 `xml.CharData` 都抽取，遇到 `p`、`br`、`tab`、`tr` 做换行。

优点是简单，能覆盖 Word 和 PowerPoint 的大部分文本。

风险是：

- 可能抽到非正文元数据。
- 表格单元格边界不够清楚。
- hyperlink、field code、批注等语义没有标注。
- 文本框、备注、页眉页脚来源不明显。

建议先不引入复杂 OOXML 模型，只增加“部件来源标签”，让输出更可解释。

### 5. `limit` 没有上限

当前 `limit <= 0` 回退到 2000，但没有最大值。非常大的 `limit` 会导致输出和上下文压力变大。

建议与 `file_read` 对齐：

- 默认：2000
- 最小：1
- 最大：5000

### 6. PDF 依赖错误可以更可诊断

PDF 依赖系统 `pdftotext`。当前缺失时提示安装 poppler-utils，已经可用。

可以继续优化：

- 在错误中保留 `pdftotext` 查找失败事实。
- 对扫描版 PDF 的空文本给出 OCR 建议。
- 对超时和非零退出码区分错误类型。

## 优化原则

1. 保持 `document_read` 只读，不执行除 `pdftotext` 以外的任意 shell。
2. 保持输出格式兼容：头部信息 + `行号| 文本`。
3. 先做高价值格式校验和错误提示，不急于实现完整 Office 语义解析。
4. 对 DOCX/PPTX 继续使用标准库 `archive/zip` 和 `encoding/xml`，避免引入重依赖。
5. 文档中明确边界：当前是文本提取工具，不保留完整排版、表格结构、批注语义或 OCR。

## 推荐方案

### 1. 增加文档格式预检

新增内部函数：

```go
func detectDocumentContainer(path string, ext string) (documentContainerInfo, error)
```

结构示例：

```go
type documentContainerInfo struct {
	Ext       string
	IsZip     bool
	IsPDF     bool
	Signature string
}
```

行为：

- `.pdf`：检查文件头是否以 `%PDF-` 开始。
- `.docx` / `.pptx`：检查 ZIP 签名或直接尝试打开 ZIP。
- `.doc` / `.ppt`：继续返回 legacy conversion error。
- 扩展名不支持：继续返回 unsupported format。

对伪装 DOCX 返回更明确错误：

```text
invalid docx container: .docx files must be Office Open XML ZIP packages; convert the source document to .docx again
```

对伪装 PDF 返回：

```text
invalid pdf file: missing %PDF- header
```

注意：文件头检查不替代真正解析，只用于提前给出更清楚的错误。

### 2. 抽出统一参数范围

新增：

```go
const (
	defaultDocumentReadLimit = 2000
	maxDocumentReadLimit     = 5000
)

func documentReadRange(args map[string]any) (offset, limit int)
```

行为：

- `offset < 1` 回退到 1。
- `limit <= 0` 回退到 2000。
- `limit > maxDocumentReadLimit` 截断到 5000。

这样 `document_read`、`file_read` 的分页语义会更一致。

### 3. 增加 DOCX 部件清单

用显式部件规则替代硬编码数组：

```go
type officePartRule struct {
	Pattern string
	Label   string
	Kind    string
}
```

DOCX 第一阶段建议支持：

- `word/document.xml` -> `Document`
- `word/footnotes.xml` -> `Footnotes`
- `word/endnotes.xml` -> `Endnotes`
- `word/header*.xml` -> `Header`
- `word/footer*.xml` -> `Footer`

输出可以插入轻量标签：

```text
[Header]
...
[Document]
...
[Footnotes]
...
```

如果担心改变输出太多，可只对新增部件加标签，正文保持不变。长期建议所有部件都带标签，方便引用来源。

### 4. 增加 PPTX notes slide 提取

PPTX 第一阶段继续提取：

- `ppt/slides/slide*.xml`

新增：

- `ppt/notesSlides/notesSlide*.xml`

输出示例：

```text
[Slide 3]
...
[Slide 3 Notes]
...
```

需要处理 notes 和 slide 的编号对应关系。最小实现可以按文件名数字排序，不解析 `.rels`。

### 5. 改善 Office XML 文本边界

当前遇到 `p`、`br`、`tab`、`tr` 换行。可以轻量增强：

- `tc` 表格单元格结束时写入 tab 或空格。
- `tbl` 前后增加换行。
- 保留 `tab` 为一个空格或制表符，而不是直接换行。

建议第一阶段只做低风险调整：

- `tab` 写空格。
- `p`、`br`、`tr` 继续换行。
- 表格单元格边界先不引入复杂格式。

这样不会明显破坏现有输出。

### 6. PDF 空文本提示 OCR

当前如果 `pdftotext` 成功但提取后为空，会走：

```text
no extractable text found in pdf document
```

建议对 PDF 增加更具体提示：

```text
no extractable text found in pdf document; it may be scanned or image-only, use OCR or image_analyze
```

这能减少用户误以为工具坏了。

### 7. 结构化提取结果

当前 `ExtractDocumentText` 返回：

```go
func ExtractDocumentText(path string) (text, format string, err error)
```

后续可以改为内部结构：

```go
type documentExtractResult struct {
	Text   string
	Format string
	Parts  []documentTextPart
}

type documentTextPart struct {
	Label string
	Text  string
}
```

外部函数可以先保持兼容，新增内部函数：

```go
func extractDocumentParts(path string) (documentExtractResult, error)
```

收益：

- 可标注 DOCX/PPTX 部件来源。
- 测试可以断言提取了 Header、Footer、Notes。
- 未来可以支持 `include_parts` 这类可选参数。

## 分阶段实施

### Phase 1：错误提示和参数边界

改动范围：

- `internal/tool/builtin_document.go`
- `internal/tool/builtin_test.go`
- `docs/tools/document_read.md`

内容：

- 新增 `documentReadRange`。
- 限制 `limit` 最大值。
- 对 PDF 空文本返回 OCR 建议。
- 对无效 DOCX/PPTX ZIP 返回更清晰错误。

验收标准：

- `go test ./internal/tool` 通过。
- `.docx` 伪装文本文件返回“必须是 Office Open XML ZIP 包”的提示。
- `.pdf` 空文本提示 OCR。
- `limit=0`、超大 `limit` 行为确定。

### Phase 2：DOCX 部件扩展

内容：

- 将 DOCX 部件硬编码数组改为规则表。
- 增加 `word/header*.xml` 和 `word/footer*.xml`。
- 可选增加部件标签。

验收标准：

- 正文、脚注、尾注仍能提取。
- 页眉、页脚能提取。
- 缺少某些部件不报错。
- 部件输出顺序稳定。

### Phase 3：PPTX notes 提取

内容：

- 提取 `ppt/notesSlides/notesSlide*.xml`。
- 和 slide 文本一起按编号输出。

验收标准：

- slide 顺序仍按编号排序。
- notes 文本出现在对应 slide 附近。
- `limit` 能正确分页包含 notes 后的行。

### Phase 4：结构化提取结果

内容：

- 新增内部 `documentExtractResult`。
- 让 DOCX/PPTX 提取以 parts 组织文本。
- 保持最终输出格式兼容。

验收标准：

- 现有调用方不需要改。
- 测试可断言 part label。
- 文档说明哪些部件会被提取。

## 测试建议

新增或补充测试：

### 格式识别

- `.docx` 是合法 ZIP，但缺少 `word/document.xml`。
- `.docx` 是普通文本文件。
- `.pptx` 是普通文本文件。
- `.pdf` 缺少 `%PDF-` 文件头。
- `.doc` 返回转换提示。
- `.ppt` 返回转换提示。

### DOCX

- `word/document.xml` 正文。
- `word/footnotes.xml` 脚注。
- `word/endnotes.xml` 尾注。
- `word/header1.xml` 页眉。
- `word/footer1.xml` 页脚。
- 缺少可读文本时返回 `no extractable text`。

### PPTX

- slide 编号乱序时输出按编号排序。
- `notesSlide*.xml` 能提取。
- 空 slide 被跳过或不产生无意义输出。
- `limit` 截断时仍提示正确 offset。

### PDF

- 缺失 `pdftotext` 时提示安装依赖。
- `pdftotext` 失败时包含 stderr。
- 空文本 PDF 提示 OCR。
- 超时返回明确错误。

### 输出格式

- 头部包含 `Document`、`Format`、`Lines`。
- 正文保持 `行号| 文本`。
- 截断时提示 `use offset=<N> to continue`。
- `offset` 超过总行数时返回准确错误。

## 文档更新

完成实现后，同步更新：

- `docs/tools/document_read.md`

建议补充说明：

```text
DOCX 和 PPTX 必须是合法 Office Open XML ZIP 包。将旧版 .doc/.ppt 或其他文件直接改扩展名不会生效，需要用 Office/LibreOffice 转换为 .docx/.pptx。
```

并把 DOCX 提取部分更新为实际支持清单，例如：

- 正文
- 脚注
- 尾注
- 页眉
- 页脚

PPTX 部分更新为：

- 幻灯片正文
- 讲者备注

## 风险与边界

1. Office Open XML 很复杂。
   仅靠标准库解析 XML 文本节点无法完整还原 Word/PPT 的视觉结构、样式、批注语义、修订记录和复杂表格。

2. ZIP 校验只能说明容器格式正确。
   合法 ZIP 不一定是合法 DOCX/PPTX，还需要检查关键部件是否存在。

3. PDF 文本提取质量取决于 `pdftotext`。
   扫描件、图片型 PDF、复杂双栏排版、表格和公式都可能提取不理想。

4. 增加部件标签可能改变输出行数。
   需要在测试中固定行为，并在文档里说明分页基于提取后的规范化文本行。

5. 不建议在这个工具里加入 OCR。
   OCR 依赖更重，也更慢，应该交给专门的 OCR 或 image 工具。

## 推荐结论

优先实现 Phase 1 和 Phase 2。

Phase 1 能快速改善错误提示和参数稳定性，特别是解释“伪装 DOCX 为什么不能读”。Phase 2 补齐 DOCX 页眉页脚，提升 Word 文档提取实用性。Phase 3 的 PPTX notes 对汇报材料很有价值，可以排在第二批。Phase 4 等提取部件变多后再做，避免一开始引入过多结构改造。
