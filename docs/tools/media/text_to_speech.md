# text_to_speech Tool

`text_to_speech` 是 LuckyAgent 的内置语音合成工具，用来把输入文本生成语音音频文件。它适合生成配音、语音摘要和需要落盘保存的音频输出。

这是会访问外部语音服务并写入文件的工具，因此被标记为需要批准。

## 工具定义

实现位置：

- `internal/tool/builtin_media.go`
- `internal/agent/agent.go`

注册信息：

```go
Name:         "text_to_speech"
Category:     CatBuiltin
Source:       "builtin"
Permission:   PermApprove
ShellAware:   true
ParallelSafe: false
```

含义：

- `CatBuiltin`：这是核心内置工具，不依赖 skill 或 MCP。
- `PermApprove`：语音合成可能访问外部服务、产生费用并写文件，默认需要审批。
- `ShellAware=true`：工具能读取 `_cwd`，不过当前输出路径最终仍限制在 workspace。
- `ParallelSafe=false`：工具会写文件，不适合无约束并行执行。

## 参数

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `text` | 是 | 无 | 需要合成为语音的文本。 |
| `model` | 否 | 配置默认值 | TTS 模型覆盖。 |
| `voice` | 否 | 配置默认值 | 声音名称，例如 `alloy`、`nova`、`shimmer` 或 provider 自定义 voice ID。 |
| `format` | 否 | 配置默认值 | 音频格式，例如 `mp3`、`wav`、`opus`、`aac`、`flac`。 |
| `speed` | 否 | 配置默认值或 `1.0` | 播放速度倍率，范围 `0.25` 到 `4.0`。 |
| `output_path` | 否 | 无 | 目标文件路径，必须在 `~/.luckyagent/workspace` 下。 |
| `output_dir` | 否 | `~/.luckyagent/workspace/generated-audio` | 输出目录，必须在 workspace 下。 |
| `filename_prefix` | 否 | `tts-audio-<unixnano>` | 使用 `output_dir` 时的文件名前缀。 |
| `overwrite` | 否 | `false` | 是否允许覆盖已有输出文件。 |
| `dry_run` | 否 | `false` | 只返回合成计划，不调用 provider，不写文件。 |
| `allow_custom_format` | 否 | `false` | 允许 provider 自定义音频格式。 |

示例参数：

```json
{
  "text": "hello from luckyagent",
  "voice": "alloy",
  "format": "mp3"
}
```

## 执行流程

`text_to_speech` 的执行过程是：

1. 读取并校验必填参数 `text`，最长 12000 字符。
2. 校验 `model`、`voice`、`format`、`speed`、输出路径和文件名前缀。
3. 根据调用参数和配置默认值构造合成计划。
4. 如果 `dry_run=true`，返回计划，不调用 provider，也不写文件。
5. 检查 speech synthesizer 是否已配置。
6. 在调用 provider 前检查输出文件冲突。
7. 创建 2 分钟超时的 context。
8. 调用 `synthesizer.SynthesizeSpeech`。
9. 检查返回音频是否为空。
10. 将音频原子写入 workspace。
11. 返回 JSON 格式的合成结果摘要。

如果 synthesizer 没有配置，返回：

```text
text-to-speech is not configured
```

如果没有 text，返回：

```text
text is required
```

如果 provider 返回空音频，返回：

```text
text-to-speech returned no audio
```

## 格式规范化

`format` 会先经过 `normalizeTTSFormat`。

| 输入 | 实际格式 |
| --- | --- |
| 空字符串 | `mp3` |
| `mp3`, `audio/mpeg` | `mp3` |
| `wav`, `audio/wav` | `wav` |
| `opus`, `audio/opus` | `opus` |
| `aac`, `audio/aac` | `aac` |
| `flac`, `audio/flac` | `flac` |
| `pcm`, `pcm16`, `audio/pcm` | `pcm` |
| 其他 | 默认返回错误 |

未知格式默认拒绝。确实需要 provider 扩展格式时，传入：

```json
{
  "allow_custom_format": true
}
```

保存文件时根据 provider 返回的 MIME 选择扩展名：

| MIME | 扩展名 |
| --- | --- |
| `audio/wav` | `.wav` |
| `audio/opus` | `.opus` |
| `audio/aac` | `.aac` |
| `audio/flac` | `.flac` |
| `audio/pcm` | `.pcm` |
| 其他 | `.mp3` |

## speed 行为

`speed` 支持 `float64` 和 `int` 类型。

允许范围是 `0.25 <= speed <= 4.0`：

```json
{
  "speed": 1.25
}
```

如果没有提供，会使用配置默认值。配置默认值小于等于 0 时，回退为：

```text
1.0
```

## 输出路径

所有输出必须在：

```text
~/.luckyagent/workspace
```

默认输出目录：

```text
~/.luckyagent/workspace/generated-audio
```

如果没有指定 `filename_prefix`，会使用：

```text
tts-audio-<time.Now().UnixNano()>
```

这能避免默认情况下多次调用写到同一个文件名。

使用 `output_path` 时：

```json
{
  "text": "hello",
  "output_path": "audio/hello.mp3"
}
```

使用 `output_dir` 时：

```json
{
  "text": "hello",
  "output_dir": "generated-audio/demo",
  "filename_prefix": "hello"
}
```

`output_path` 和 `output_dir` 都会通过 `resolveWorkspacePath` 校验。绝对路径或相对路径最终都不能逃出 `~/.luckyagent/workspace`。

覆盖策略：

- 默认 `overwrite=false`。
- 目标文件已存在时会在调用 provider 前报错。
- 只有 `overwrite=true` 才会覆盖已有文件。
- 写文件使用临时文件加 rename 的原子写入流程。

`filename_prefix` 必须是文件名片段，不能包含路径分隔符、`..` 或控制字符。

## 配置

相关配置位于：

```json
{
  "tts": {
    "provider": "openai",
    "api_key": "",
    "api_base": "https://api.openai.com/v1",
    "auth_mode": "bearer",
    "model": "gpt-4o-mini-tts",
    "voice": "alloy",
    "format": "mp3",
    "speed": 1.0
  }
}
```

agent 初始化时会从 `tts.*` 构造默认值，并根据 provider 初始化 speech synthesizer。

当前 agent 里支持的 provider 分支：

- `openai`

## 输出格式

成功时返回 JSON 字符串，经过 `prettyStructuredValue` 格式化。

字段包括：

```json
{
  "provider": "openai",
  "model": "gpt-4o-mini-tts",
  "voice": "alloy",
  "path": "/home/user/.luckyagent/workspace/generated-audio/tts-audio-123.mp3",
  "format": "mp3",
  "created_at": "2026-07-03T00:00:00Z",
  "metadata": {}
}
```

其中：

- `path` 是实际写入的本地音频文件路径。
- `format` 根据 provider 返回 MIME 反推。
- `created_at` 只有 provider 返回时间时才出现。
- `metadata` 只有 provider 返回 metadata 时才出现。

## 适合使用的场景

优先使用 `text_to_speech` 的场景：

- 把短文本生成语音。
- 生成讲解、提示音、播报内容或音频摘要。
- 需要把生成音频保存到 LuckyAgent workspace。
- 需要指定 voice、format 或 speed。

示例：

```json
{
  "text": "Deployment completed successfully.",
  "voice": "alloy",
  "format": "mp3",
  "filename_prefix": "deploy-success"
}
```

## 不适合使用的场景

不优先使用 `text_to_speech` 的场景：

- 只需要朗读但不需要生成文件。
- 需要转写音频，应使用语音识别或多模态转写能力。
- 需要剪辑、拼接、降噪或转换已有音频，应使用 `terminal` 调用音频工具。
- 需要输出到 workspace 之外的路径，当前工具会拒绝。

## 风险和注意事项

`text_to_speech` 的主要注意点：

- 需要配置可用的 speech synthesizer。
- 会调用外部 provider，可能产生费用和网络延迟。
- 会写入本地文件，因此权限是 `PermApprove`。
- 输出路径必须在 `~/.luckyagent/workspace` 下。
- `speed` 小于等于 0 会被忽略。
- 文件扩展名依据 provider 返回的 MIME，而不是调用参数 `format`。
- 默认文件名前缀包含纳秒时间，便于避免覆盖。

## 维护注意事项

如果后续修改 `text_to_speech`，需要同步检查：

- 参数表是否仍与 `TextToSpeechTool()` 一致。
- context timeout 是否仍是 2 分钟。
- 默认输出目录是否仍是 `generated-audio`。
- workspace 限制是否仍由 `resolveWorkspacePath` 执行。
- `format` 规范化和扩展名映射是否变化。
- `speed` 支持类型和默认值逻辑是否变化。
- 支持 provider 是否变化。
- 输出 JSON 字段是否变化。
