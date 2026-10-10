# image_generate Tool

`image_generate` 是 LuckyAgent 的内置图片生成工具，用来根据文本 prompt 生成图片，也可以在提供输入图片时执行 image-to-image 或多图参考生成。生成结果会写入 LuckyAgent workspace。

这是会访问外部生成服务并写入文件的工具，因此被标记为需要批准。

## 工具定义

实现位置：

- `internal/tool/builtin_media.go`
- `internal/agent/agent.go`

注册信息：

```go
Name:         "image_generate"
Category:     CatBuiltin
Source:       "builtin"
Permission:   PermApprove
ShellAware:   true
ParallelSafe: false
```

含义：

- `CatBuiltin`：这是核心内置工具，不依赖 skill 或 MCP。
- `PermApprove`：图片生成可能访问外部服务、产生费用并写文件，默认需要审批。
- `ShellAware=true`：工具能读取 `_cwd`，相对输入路径会相对当前 shell cwd 解析。
- `ParallelSafe=false`：工具会写文件，不适合无约束并行执行。

## 参数

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `prompt` | 是 | 无 | 描述要生成或编辑的图片。 |
| `input_path` | 否 | 无 | 单个本地输入图片路径。 |
| `input_paths` | 否 | 无 | 多个本地输入图片路径。 |
| `input_url` | 否 | 无 | 单个远程输入图片 URL。 |
| `input_urls` | 否 | 无 | 多个远程输入图片 URL。 |
| `input_base64_data` | 否 | 无 | 单个 Base64 输入图片。 |
| `input_base64_datas` | 否 | 无 | 多个 Base64 输入图片。 |
| `input_mime_type` | 否 | 自动推断 | 单个 Base64 输入图片的 MIME。 |
| `input_mime_types` | 否 | 自动推断 | 多个 Base64 输入图片对应的 MIME 列表。 |
| `model` | 否 | 配置默认值 | 图片生成模型覆盖。 |
| `size` | 否 | 配置默认值 | 图片尺寸，例如 `1024x1024`、`1536x1024`、`1024x1536` 或 `auto`。 |
| `quality` | 否 | 配置默认值 | 质量，例如 `low`、`medium`、`high` 或 `auto`。 |
| `background` | 否 | 配置默认值 | 背景模式，例如 `auto`、`opaque` 或 `transparent`。 |
| `output_format` | 否 | 配置默认值 | 输出格式，支持 `png`、`jpeg`、`webp`。 |
| `output_compression` | 否 | 配置默认值 | JPEG/WebP 压缩质量，0 到 100。 |
| `count` | 否 | 配置默认值或 `1` | 生成图片数量，限制在 1 到 10。 |
| `output_path` | 否 | 无 | 单张图片目标路径，必须在 `~/.luckyagent/workspace` 下。 |
| `output_dir` | 否 | `~/.luckyagent/workspace/generated-images` | 输出目录，必须在 workspace 下。 |
| `filename_prefix` | 否 | `generated-image` | 使用 `output_dir` 时的文件名前缀。 |
| `overwrite` | 否 | `false` | 是否允许覆盖显式 `output_path`。 |
| `dry_run` | 否 | `false` | 只返回生成计划，不调用 provider，不写文件。 |

示例参数：

```json
{
  "prompt": "a minimal product poster",
  "size": "1024x1024",
  "output_format": "png"
}
```

## 执行流程

`image_generate` 的执行过程是：

1. 读取并校验必填参数 `prompt`，最长 8000 字符。
2. 收集本地路径、远程 URL 和 Base64 输入图片，并限制最多 8 张。
3. 解析输出路径参数、`filename_prefix`、`overwrite`、`dry_run` 和 `_cwd`。
4. 根据配置默认值和调用参数构造生成计划。
5. 限制 `count` 在 1 到 10。
6. 如果设置了 `output_path` 且 `count > 1`，返回错误。
7. 如果 `dry_run=true`，返回计划，不调用 provider，也不写文件。
8. 检查 image generator 是否已配置。
9. 对显式 `output_path` 做生成前冲突检查。
10. 读取本地输入图片、下载远程输入图片或解码 Base64 输入，并校验大小和 MIME。
11. 创建 2 分钟超时的 context。
12. 调用 `generator.GenerateImage`。
13. 将返回图片原子写入 workspace。
14. 返回 JSON 格式的生成结果摘要。

如果 generator 没有配置，返回：

```text
image generation is not configured
```

如果没有 prompt，返回：

```text
prompt is required
```

## 输入图片

本地输入支持单个和多个：

```json
{
  "input_path": "reference.png"
}
```

```json
{
  "input_paths": ["front.png", "back.png"]
}
```

相对路径会通过 `_cwd` 解析：

```go
filepath.Join(baseDir, path)
```

解析后的本地路径会经过 `validatePath`。

远程输入支持：

```json
{
  "input_url": "https://example.com/image.png"
}
```

远程输入会调用 `validateFetchURL`，拒绝 localhost、私网、link-local 等地址，并使用 30 秒 HTTP timeout。响应体最多读取 20 MiB。

Base64 输入支持：

```json
{
  "input_base64_data": "...",
  "input_mime_type": "image/png"
}
```

当 `input_mime_type` 存在且 `input_mime_types` 为空时，会自动转成单元素 MIME 列表。

输入图片限制：

- 总数量最多 8 张。
- 单张最大 20 MiB。
- 所有输入合计最大 50 MiB。
- 支持 MIME：`image/png`、`image/jpeg`、`image/webp`、`image/gif`。

## 输出路径

所有输出必须在：

```text
~/.luckyagent/workspace
```

默认输出目录：

```text
~/.luckyagent/workspace/generated-images
```

默认文件名前缀：

```text
generated-image
```

使用 `output_dir` 时，文件名格式是：

```text
<filename_prefix>-01.<ext>
<filename_prefix>-02.<ext>
```

使用 `output_path` 时，只能生成一张图片：

```json
{
  "prompt": "one icon",
  "count": 1,
  "output_path": "icons/result.png"
}
```

`output_path` 和 `output_dir` 都会通过 `resolveWorkspacePath` 校验。绝对路径或相对路径最终都不能逃出 `~/.luckyagent/workspace`。

覆盖策略：

- 默认 `overwrite=false`。
- 显式 `output_path` 已存在时会报错；只有 `overwrite=true` 才会覆盖。
- 使用 `output_dir` 时，如果目标文件名已存在，会自动追加唯一后缀，避免覆盖旧文件。
- 写文件使用临时文件加 rename 的原子写入流程。

`filename_prefix` 必须是文件名片段，不能包含路径分隔符、`..` 或控制字符。

## 格式和扩展名

`output_format` 会规范化：

| 输入 | 实际格式 |
| --- | --- |
| 空字符串 | `png` |
| `png` | `png` |
| `jpg` | `jpeg` |
| `jpeg` | `jpeg` |
| `webp` | `webp` |
| 其他 | 返回错误 |

保存文件时根据生成结果 MIME 决定扩展名：

| MIME / 格式 | 扩展名 |
| --- | --- |
| `image/jpeg`, `jpeg`, `jpg` | `.jpg` |
| `image/webp`, `webp` | `.webp` |
| 其他 | `.png` |

## 配置

相关配置位于：

```json
{
  "image_generation": {
    "provider": "openai",
    "api_key": "",
    "api_base": "https://api.openai.com/v1",
    "auth_mode": "bearer",
    "model": "gpt-image-1.5",
    "size": "1024x1024",
    "quality": "auto",
    "background": "auto",
    "output_format": "png",
    "output_compression": 0,
    "count": 1
  }
}
```

agent 初始化时会从 `image_generation.*` 构造默认值，并根据 provider 初始化 generator。

当前 agent 里支持的 provider 分支：

- `gemini`
- `openai`

如果 `image_generation` 未配置但 OpenAI 多模态配置可用，OpenAI media provider 也可能作为 image generator。

## 输出格式

成功时返回 JSON 字符串，经过 `prettyStructuredValue` 格式化。

字段包括：

```json
{
  "provider": "openai",
  "model": "gpt-image-1.5",
  "count": 1,
  "paths": [
    "/home/user/.luckyagent/workspace/generated-images/generated-image-01.png"
  ],
  "revised_prompt": "...",
  "created_at": "2026-07-03T00:00:00Z",
  "metadata": {}
}
```

其中：

- `paths` 是实际写入的本地文件路径。
- `revised_prompt` 由 provider 返回，可能为空。
- `created_at` 只有 provider 返回时间时才出现。
- `metadata` 只有 provider 返回 metadata 时才出现。

## 适合使用的场景

优先使用 `image_generate` 的场景：

- 根据文字生成图片。
- 根据一张或多张参考图做图片变体。
- 生成应用素材、海报草图、图文封面。
- 需要把生成图片保存到本地 workspace。

示例：

```json
{
  "prompt": "a clean app icon for a note taking tool",
  "output_dir": "generated-images/icons",
  "filename_prefix": "note-icon"
}
```

## 不适合使用的场景

不优先使用 `image_generate` 的场景：

- 只需要分析已有图片，应使用 `image_analyze`。
- 需要编辑任意本地路径外的文件；输出只能在 workspace 下。
- 需要无费用、无外部服务的本地图像处理，应使用 `terminal` 调用本地工具。
- 需要批量生成大量图片，当前 `count` 单次限制 1 到 10。

## 风险和注意事项

`image_generate` 的主要注意点：

- 需要配置可用的 image generator。
- 会调用外部 provider，可能产生费用和网络延迟。
- 会写入本地文件，因此权限是 `PermApprove`。
- 输出路径必须在 `~/.luckyagent/workspace` 下。
- `filename_prefix` 如果包含 `../` 并导致路径逃逸，会被拒绝。
- 远程输入 URL 会做 SSRF 校验。
- 远程输入响应体最多读取 20 MiB。
- `output_path` 只允许 `count == 1`。

## 维护注意事项

如果后续修改 `image_generate`，需要同步检查：

- 参数表是否仍与 `ImageGenerateTool()` 一致。
- `count` 上下限是否仍是 1 到 10。
- context timeout 是否仍是 2 分钟。
- 默认输出目录是否仍是 `generated-images`。
- workspace 限制是否仍由 `resolveWorkspacePath` 执行。
- 支持 provider 是否变化。
- `output_format` 规范化和扩展名映射是否变化。
- 远程输入 URL 校验和 20 MiB 限制是否变化。
