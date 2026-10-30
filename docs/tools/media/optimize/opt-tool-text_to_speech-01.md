# opt-tool-text_to_speech-01

## 目标

优化 `text_to_speech` 的文本边界、voice/model/format 校验、输出文件安全、dry-run 能力和测试覆盖，让它继续保持“调用外部语音合成服务、写入 workspace、需要审批”的定位，同时降低费用不可控、输出覆盖、格式不一致和 provider 错误不透明的问题。

本方案聚焦：

- 文本长度和字符校验
- voice / model / format / speed 参数边界
- workspace 输出路径和文件名安全
- dry-run / synthesis plan
- 原子写入和覆盖策略
- 输出 metadata 完整性
- 测试矩阵补齐

## 当前状态

相关实现：

- `internal/tool/builtin_media.go`
- `internal/tool/builtin_test.go`
- `docs/tools/media/text_to_speech.md`

当前 handler 流程：

1. 检查 speech synthesizer 是否配置。
2. 调用 `buildSpeechSynthesisRequest` 解析参数。
3. 创建 2 分钟 timeout context。
4. 调用 `synthesizer.SynthesizeSpeech`。
5. 检查返回音频非空。
6. 调用 `saveSynthesizedAudio` 写入 workspace。
7. 返回 JSON 结果摘要。

当前优势：

- `text` 有非空校验。
- `format` 有规范化逻辑。
- `speed` 会回退到默认值或 `1.0`。
- 输出路径限制在 workspace。
- 默认文件名带 `UnixNano`，能降低默认覆盖概率。
- 成功结果已返回 provider、model、voice、path、format。

## 主要问题

### 1. text 只有非空校验

当前没有最大长度限制。风险：

- 长文本导致 provider 拒绝。
- 单次合成费用不可控。
- HTTP 请求体过大。
- 返回音频过大，影响本地存储。

建议设置默认长度上限，例如 8000 或 12000 字符，并在未来支持分段合成。

### 2. 不支持分段合成

长文本 TTS 常见需求是按段落拆分后分别合成，再拼接。当前工具只做单次 provider 调用。

短期建议先限制长度。长期可以新增：

- `chunking`
- `max_chunk_chars`
- `merge_output`

但这需要音频拼接能力，不能作为第一阶段。

### 3. speed 没有上限

当前 `speed > 0` 即生效。过大的 speed 可能导致 provider 报错或输出异常。

建议限制：

- 最小：0.25
- 最大：4.0

具体范围可由 provider adapter 进一步约束。

### 4. voice 和 model 缺少基础校验

当前 `voice`、`model` 是字符串透传。需要避免：

- 空白字符串。
- 控制字符。
- 过长 provider ID。
- 错误信息不包含参数名。

不建议在 common handler 硬编码所有 voice 名称，但应做基础字符串安全校验。

### 5. format 对未知值会透传

`normalizeTTSFormat` 对未知值会返回 lower 后的原值。这样兼容 provider 扩展，但也会让拼写错误延迟到 provider 侧才报错。

建议提供两层策略：

- 默认允许常见格式 allowlist。
- 如需 provider 扩展格式，使用 `allow_custom_format=true`。

### 6. `filename_prefix` 未显式清理

当前 `filename_prefix` 会直接参与：

```go
filename := fmt.Sprintf("%s%s", filenamePrefix, ext)
path := filepath.Join(dir, filename)
```

最终有 workspace 校验，但建议提前限制为文件名片段：

- 不允许路径分隔符。
- 不允许 `.` 或 `..`。
- 不允许控制字符。
- 限制长度。

### 7. `output_path` 会直接覆盖文件

默认文件名带时间戳，但显式 `output_path` 会覆盖已有文件。显式 `filename_prefix` 也可能在相同目录下重复。

建议增加：

- 默认 `overwrite=false`
- 文件存在时报错
- 用户显式 `overwrite=true` 才覆盖

### 8. 写文件不是原子写入

当前使用 `os.WriteFile`。音频文件是二进制结果，写入中断可能留下损坏文件。

建议统一媒体写入 helper，使用临时文件 + rename。

### 9. 缺少 dry-run

TTS 会访问外部服务并写文件，和 `image_generate` 一样适合提供 dry-run。

dry-run 应返回：

- model
- voice
- format
- speed
- text length
- output target
- 是否会覆盖文件

并且不调用 provider、不写文件。

## 优化原则

1. `text_to_speech` 必须继续要求审批。
2. 所有音频输出必须留在 LuckyAgent workspace。
3. 默认避免覆盖已有音频。
4. 长文本先限制，再考虑分段合成。
5. provider 特定 voice/model 不在 common handler 里硬编码。
6. dry-run 不应产生费用或文件。

## 推荐方案

### 1. 抽出参数解析和合成计划

新增：

```go
type speechSynthesisOptions struct {
	Text              string
	Model             string
	Voice             string
	Format            string
	Speed             float64
	OutputPath        string
	OutputDir         string
	FilenamePrefix    string
	Overwrite         bool
	DryRun            bool
	AllowCustomFormat bool
	BaseDir           string
}

type speechSynthesisPlan struct {
	Model        string
	Voice        string
	Format       string
	Speed        float64
	TextChars    int
	OutputTarget string
	Overwrite    bool
	DryRun       bool
}
```

handler 流程调整为：

1. parse options。
2. validate options。
3. build request。
4. build output target。
5. dry-run 时返回 plan。
6. 非 dry-run 时调用 provider。
7. 原子写入音频。
8. 返回结果 payload。

### 2. text 长度限制

建议常量：

```go
const maxTTSInputChars = 12000
```

行为：

- 空文本报错。
- 超长文本报错。
- 保留换行和标点。
- 错误信息说明限制。

示例：

```text
text exceeds 12000 character limit; split it into smaller requests
```

### 3. speed 范围限制

新增：

```go
func validateTTSSpeed(speed float64) error
```

默认范围：

- `0.25 <= speed <= 4.0`

如果 provider 范围更窄，由 provider adapter 返回更具体错误。

### 4. format allowlist

默认允许：

- `mp3`
- `wav`
- `opus`
- `aac`
- `flac`
- `pcm`

未知格式默认报错：

```text
unsupported audio format "ogg"; supported formats: mp3, wav, opus, aac, flac, pcm
```

如果确实需要 provider 扩展格式，可通过：

```json
{
  "allow_custom_format": true
}
```

继续透传。

### 5. voice / model 基础校验

新增：

```go
func validateProviderID(name, value string) error
```

规则：

- 允许空值，由 defaults 决定。
- 不允许控制字符。
- 最大长度 128。
- trim 后使用。

不在 common 层限制具体 voice 列表。

### 6. 文件名和覆盖策略

新增：

```go
func sanitizeMediaFilenamePrefix(prefix string) (string, error)
func resolveMediaOutputTarget(outputPath, outputDir, filenamePrefix, ext, baseDir string) (string, error)
```

规则：

- `output_path` 直接指定目标文件，但必须在 workspace。
- `output_dir` + `filename_prefix` 构造目标文件。
- 默认 `overwrite=false`。
- 文件存在时返回错误。
- `overwrite=true` 才允许覆盖。

默认文件名仍可使用 `UnixNano`，保持低冲突。

### 7. 原子写入

和 `image_generate` 共用媒体写入 helper：

```go
func writeMediaFileAtomic(path string, data []byte, perm fs.FileMode) error
```

写入成功后返回：

- path
- bytes_written
- overwritten

### 8. dry-run 输出

示例：

```json
{
  "dry_run": true,
  "model": "gpt-4o-mini-tts",
  "voice": "alloy",
  "format": "mp3",
  "speed": 1,
  "text_chars": 128,
  "output_target": "/home/user/.luckyagent/workspace/generated-audio/demo.mp3",
  "overwrite": false
}
```

dry-run 不调用 `SynthesizeSpeech`，不写文件。

### 9. 结果 payload 增强

当前 payload 建议补充：

- `bytes_written`
- `mime_type`
- `text_chars`
- `speed`
- `overwritten`

示例：

```json
{
  "provider": "openai",
  "model": "gpt-4o-mini-tts",
  "voice": "alloy",
  "path": ".../demo.mp3",
  "format": "mp3",
  "mime_type": "audio/mpeg",
  "speed": 1,
  "text_chars": 128,
  "bytes_written": 34567,
  "overwritten": false
}
```

## 分阶段实施

### 第一阶段：参数安全

- 增加 text 长度限制。
- 增加 speed 上下限。
- 增加 format allowlist。
- 增加 voice/model 基础字符串校验。
- 增加 `filename_prefix` 清理。

### 第二阶段：输出可靠性

- 默认不覆盖已有文件。
- 增加 `overwrite` 参数。
- 使用原子写入。
- 结果 payload 增加 `bytes_written`、`mime_type`、`speed`。

### 第三阶段：dry-run 和长文本路线

- 增加 `dry_run`。
- 输出 synthesis plan。
- 后续再设计长文本分段合成和音频拼接。

## 测试建议

新增或补齐测试：

- `text` 为空时报错。
- `text` 超长时报错。
- `speed` 小于 0.25 或大于 4.0 时报错。
- 默认 speed 回退为 `1.0`。
- format 常见别名正常规范化。
- 未知 format 默认报错。
- `allow_custom_format=true` 时允许未知 format。
- voice/model 包含控制字符时报错。
- `filename_prefix` 包含路径分隔符时报错。
- `output_path` 逃出 workspace 时报错。
- 输出文件已存在且 `overwrite=false` 时报错。
- `overwrite=true` 时允许覆盖。
- `dry_run=true` 不调用 fake synthesizer，不写文件。
- fake synthesizer 返回空音频时报错。
- 成功写入时返回 path、format、bytes_written。

测试应使用 fake speech synthesizer，不依赖真实 TTS provider。

## 文档更新

同步更新：

- `docs/tools/media/text_to_speech.md`
- 参数表新增 `dry_run`、`overwrite`、`allow_custom_format`
- text 长度限制
- speed 合法范围
- format allowlist
- 文件覆盖策略
- dry-run 示例
- 结果 payload 示例

## 风险与边界

- 默认不覆盖文件会改变旧行为，需要明确迁移说明。
- format allowlist 可能阻止 provider 新格式，需保留 `allow_custom_format`。
- 长文本分段合成涉及音频拼接和时长 metadata，不应混入第一阶段。
- provider voice 列表不稳定，common 层只做基础字符串校验。

## 推荐结论

优先补 text 长度、speed 范围、format allowlist 和输出覆盖策略。这些能直接控制成本和文件安全。随后加入 dry-run，让 `text_to_speech` 在审批前能展示合成计划，最后再考虑长文本分段合成。
