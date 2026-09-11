# opt-tool-image_generate-01

## 目标

优化 `image_generate` 的参数校验、输入图片处理、输出文件安全、结果元数据和测试覆盖，让它继续保持“调用外部图片生成服务、写入 workspace、需要审批”的定位，同时降低费用不可控、覆盖文件、路径逃逸、输入格式异常和 provider 行为不透明的问题。

本方案聚焦：

- prompt 和生成参数校验
- 输入图片数量、大小和 MIME 限制
- 远程输入 URL 安全
- workspace 输出路径和文件名安全
- dry-run / generation plan
- 原子写入和覆盖策略
- 结构化结果和测试矩阵

## 当前状态

相关实现：

- `internal/tool/builtin_media.go`
- `internal/tool/builtin_test.go`
- `docs/tools/media/image_generate.md`

当前 `image_generate` 支持：

- 文本生成图片
- 单图或多图参考生成
- 本地输入、远程 URL 输入、Base64 输入
- 单张或多张输出
- 输出到 LuckyAgent workspace

当前 handler 流程：

1. 检查 image generator 是否配置。
2. 调用 `buildImageGenerationRequest` 解析参数。
3. 创建 2 分钟 timeout context。
4. 调用 `generator.GenerateImage`。
5. 调用 `saveGeneratedImages` 写入 workspace。
6. 返回 JSON 结果摘要。

当前优势：

- `input_url` 已调用 `validateFetchURL`。
- 远程输入下载有 30 秒 timeout。
- 远程输入最多读取 20 MiB。
- `output_path` 和 `output_dir` 会限制在 workspace。
- `count` 限制在 1 到 10。
- 已有路径逃逸和私网 URL 测试。

## 主要问题

### 1. prompt 只有非空校验

当前只检查 `prompt` 是否为空。缺少：

- 最大长度限制。
- 控制字符清理。
- 空白折叠策略。
- 明确的费用提示或 plan。

超长 prompt 可能造成 provider 拒绝、费用不可控或日志过长。

### 2. 输入图片缺少总数量限制

当前本地 path、URL、Base64 三类输入会被合并到 `req.InputImages`，但没有统一数量上限。

风险：

- 多张大图导致内存和请求体过大。
- provider 对参考图数量有上限，错误发生在远端。
- agent 难以预估成本。

建议设置默认上限，例如 4 或 8 张。

### 3. 本地和 Base64 输入缺少大小限制

远程 URL 输入读取限制为 20 MiB，但本地 path 和 Base64 当前没有同等限制。

建议所有输入图片统一限制：

- 单张最大 20 MiB。
- 总输入最大 50 MiB。

这样不同输入来源行为一致。

### 4. MIME 类型没有严格 allowlist

当前本地输入根据扩展名或 `http.DetectContentType` 推断，Base64 根据 `input_mime_types` 或内容推断，但没有拒绝非图片类型。

风险：

- 文本、PDF、HTML 被当作图片传给 provider。
- `Content-Type` 带参数时后续扩展名判断不稳定。
- provider 返回错误不如 agent 本地错误清晰。

### 5. `filename_prefix` 未显式清理

当前使用：

```go
filename := fmt.Sprintf("%s-%02d%s", filenamePrefix, i+1, ext)
path := filepath.Join(dir, filename)
resolved, err := resolveWorkspacePath(path)
```

最终有 workspace 校验，能挡住明显逃逸，但错误发生较晚。更好的行为是提前把 `filename_prefix` 限制为文件名片段：

- 不允许 `/`、`\`、`..`
- 不允许空字节和控制字符
- 限制长度

这样错误信息更明确。

### 6. 写文件可能覆盖已有文件

默认 `filename_prefix=generated-image` 时，多次调用同一个 `output_dir` 会写：

```text
generated-image-01.png
```

这可能覆盖之前生成结果。`output_path` 也会直接覆盖已有文件。

建议增加覆盖策略：

- 默认 `create_new`
- 可选 `overwrite=true`
- 冲突时自动追加时间戳或短 hash

### 7. 写回不是原子写入

当前使用 `os.WriteFile` 直接写入目标文件。异常中断时可能留下部分文件。

生成图片通常是二进制资产，应使用临时文件 + rename 的方式原子写入。

### 8. 缺少 dry-run

图片生成可能产生费用，也会写文件。当前没有只返回计划的能力。

建议支持：

```json
{
  "prompt": "...",
  "dry_run": true
}
```

返回将使用的 provider、model、size、quality、count、输出路径计划和输入图片摘要，不调用外部 provider。

### 9. provider 参数 allowlist 不明确

`size`、`quality`、`background`、`output_format` 当前多数是透传或简单规范化。不同 provider 支持范围不同。

建议：

- common 层只做基础合法性校验。
- provider 层返回支持矩阵。
- 错误信息说明哪个 provider 不支持哪个参数。

## 优化原则

1. `image_generate` 必须继续要求审批。
2. 所有输出必须留在 LuckyAgent workspace。
3. 远程输入必须继续执行 SSRF 校验。
4. 生成前应能预估 provider、模型、数量和输出路径。
5. 默认不覆盖已有文件。
6. 不把 provider 特定参数硬编码进通用 handler。

## 推荐方案

### 1. 抽出参数解析和生成计划

新增：

```go
type imageGenerationOptions struct {
	Prompt            string
	InputPaths        []string
	InputURLs         []string
	InputBase64s      []string
	InputMIMETypes    []string
	Model             string
	Size              string
	Quality           string
	Background        string
	OutputFormat      string
	OutputCompression int
	Count             int
	OutputPath        string
	OutputDir         string
	FilenamePrefix    string
	Overwrite         bool
	DryRun            bool
	BaseDir           string
}

type imageGenerationPlan struct {
	Provider      string
	Model         string
	Size          string
	Quality       string
	Background    string
	OutputFormat  string
	Count         int
	InputCount    int
	InputBytes    int64
	OutputTargets []string
	DryRun        bool
}
```

让 handler 流程变成：

1. parse options。
2. build request。
3. build output plan。
4. dry-run 时直接返回 plan。
5. 非 dry-run 时调用 provider。
6. 保存文件并返回结果。

### 2. prompt 边界校验

建议常量：

```go
const maxImagePromptChars = 8000
```

行为：

- 空 prompt 报错。
- 超长 prompt 报错。
- 去除首尾空白。
- 保留正文换行，不强制压缩用户 prompt。

错误示例：

```text
prompt exceeds 8000 character limit
```

### 3. 输入图片统一限制

建议常量：

```go
const (
	maxImageGenerateInputs     = 8
	maxImageGenerateInputBytes = 20 << 20
	maxImageGenerateTotalBytes = 50 << 20
)
```

对本地、URL、Base64 三类输入统一执行：

- 数量限制。
- 单张大小限制。
- 总大小限制。
- MIME allowlist。

### 4. MIME allowlist

新增：

```go
func validateImageInputMIME(mimeType string) error
```

默认允许：

- `image/png`
- `image/jpeg`
- `image/webp`

可选允许：

- `image/gif`

对 provider 不支持的 MIME，由 provider adapter 给出更具体错误。

### 5. 文件名和路径策略

新增：

```go
func sanitizeMediaFilenamePrefix(prefix string) (string, error)
```

规则：

- trim 空白。
- 默认值由调用方决定。
- 不允许路径分隔符。
- 不允许 `.` 或 `..`。
- 不允许控制字符。
- 最大长度 80。

输出策略：

- `output_path` 只允许 `count=1`，保持现状。
- `output_dir` 生成多个文件。
- 默认不覆盖已有文件。
- 冲突时返回错误或生成唯一文件名。

建议默认生成唯一文件名：

```text
<filename_prefix>-20260703-153012-01.png
```

这比覆盖旧文件更安全。

### 6. 原子写入

新增 helper：

```go
func writeFileAtomic(path string, data []byte, perm fs.FileMode) error
```

流程：

1. 在同目录创建临时文件。
2. 写入内容。
3. fsync 或至少 close。
4. rename 到目标路径。
5. 失败时清理临时文件。

### 7. dry-run 输出

示例：

```json
{
  "dry_run": true,
  "model": "gpt-image-1.5",
  "size": "1024x1024",
  "quality": "auto",
  "count": 2,
  "input_count": 1,
  "output_targets": [
    "/home/user/.luckyagent/workspace/generated-images/demo-01.png",
    "/home/user/.luckyagent/workspace/generated-images/demo-02.png"
  ]
}
```

dry-run 不调用外部 provider，不写文件。

### 8. 返回结果补充 metadata

当前结果已有 `provider`、`model`、`count`、`paths`、`revised_prompt`、`created_at`、`metadata`。

建议补充：

- `size`
- `quality`
- `background`
- `output_format`
- `input_count`
- `bytes_written`
- `overwritten`

这些字段便于调用方记录生成资产。

## 分阶段实施

### 第一阶段：安全限制

- 增加 prompt 长度限制。
- 增加输入图片数量限制。
- 本地和 Base64 输入补齐大小限制。
- 增加 MIME allowlist。
- `filename_prefix` 提前校验。

### 第二阶段：输出可靠性

- 默认避免覆盖已有文件。
- 增加原子写入。
- 输出结果加入 `bytes_written` 和生成参数摘要。

### 第三阶段：dry-run 和 provider 能力

- 增加 `dry_run`。
- provider adapter 暴露支持矩阵或参数校验。
- 错误信息加入 provider、model 和参数名。

## 测试建议

新增或补齐测试：

- prompt 为空时报错。
- prompt 超长时报错。
- `count` 小于 1 或大于 10 时被边界限制。
- `output_path` 且 `count>1` 报错。
- 本地输入超过大小限制时报错。
- Base64 输入超过大小限制时报错。
- 输入图片数量超过上限时报错。
- 非图片 MIME 被拒绝。
- `input_url` 私网地址被拒绝。
- `filename_prefix` 包含路径分隔符时报错。
- 已存在输出文件时默认不覆盖。
- `overwrite=true` 时允许覆盖。
- `dry_run=true` 不调用 fake generator，不写文件。
- fake generator 返回空图片时报错。
- 多图输出返回稳定 paths 和 count。

测试应继续使用 fake image generator，不依赖真实图片生成服务。

## 文档更新

同步更新：

- `docs/tools/media/image_generate.md`
- 参数表新增 `dry_run`、`overwrite`
- prompt 长度限制
- 输入图片数量和大小限制
- MIME allowlist
- 输出文件冲突策略
- dry-run 示例

## 风险与边界

- 默认不覆盖文件会改变旧行为，需要在文档和错误信息中明确。
- 输入数量和大小限制可能拒绝过去能透传给 provider 的请求。
- provider 能力矩阵需要避免在 common handler 里过度硬编码。
- 原子写入在跨文件系统 rename 时可能失败，因此临时文件必须创建在目标目录。

## 推荐结论

优先补齐输入数量、大小、MIME 和文件覆盖策略。这些直接影响费用、稳定性和本地文件安全。随后加入 dry-run，把 `image_generate` 从“直接执行生成”改成“可先审计生成计划，再执行落盘”的工具，更符合需要审批的媒体生成场景。
