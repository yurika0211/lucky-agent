# 图片输入与统一模型配置

`models.vision_mode` 控制图片如何进入主对话，默认 `auto`。

| 值 | 主模型支持视觉 | 主模型不支持视觉或能力未知 |
| --- | --- | --- |
| `auto` | 原图直接发送给主模型，不调用外部识图 | 独立视觉模型识别，主模型接收文字结果 |
| `external` | 独立视觉模型识别，主模型接收文字结果 | 独立视觉模型识别，主模型接收文字结果 |

```bash
lh config get models.vision_mode
lh config set models.vision_mode auto
lh config set models.active.vision your-vision-model
lh config set models.endpoints.vision.provider openai
lh config set models.endpoints.vision.api_base https://your-endpoint.example/v1
```

直传时，同一张附件只加入一次用户消息。系统跳过图片预识别，并隐藏和拦截
`image_analyze`。需要读取本地路径、图片 URL 或 Base64 时，使用 `image_read`
把原图放进主模型下一轮输入；该工具不调用识图模型。音频转写和文档提取仍按各自流程处理。

图片读取或请求失败会返回错误，不会因此启动外部图片分析。补充识图未配置可用提供商时，
附件上下文会包含识图错误，不能把本地文件元信息当作已经识别图片内容。

视觉能力来自模型目录中的 `vision` 标签。未收录的模型可声明：

```json
{
  "models": {
    "vision_mode": "auto",
    "active": { "chat": "your-chat-model", "vision": "your-vision-model" }
  },
  "custom_models": [
    {
      "id": "your-chat-model",
      "provider": "openai",
      "capabilities": ["chat", "streaming", "tools", "vision"]
    }
  ]
}
```

能力判断使用当轮模型；设置 `models.active.vision` 只选择补充识图模型，不会给主模型增加视觉能力。
配置热重载影响后续轮次，已开始的轮次保留图片路由选择。

## 配置迁移

模型 ID 统一放在 `models.active.<用途>`；连接配置统一放在
`models.endpoints.<用途>`。用途包括 `chat`、`vision`、`transcription`、
`embedding`、`image`、`tts` 和 `reranker`。视觉和转写端点分别初始化，不共享覆盖地址或密钥。

旧版 `llm_provider`、`multimodal` 及功能区内的模型、地址、密钥仍可加载。
显式的新配置优先，包括显式空密钥；保存和配置 API 只输出统一模型格式。
旧 `llm_provider.vision=true` 迁移到它原来描述的模型能力，不会套用到不同的新模型。
旧 `multimodal.image_provider` 迁移到视觉端点提供商，`openai-media` 映射为 `openai`。
旧 `multimodal.api_key/api_base/provider` CLI 写入同步更新视觉和语音端点；新配置应分别设置。

`embedding.dimension`、`image_generation` 的尺寸、质量等参数，以及 `tts` 的
音色、格式、语速保持原位置。API 返回的密钥经过脱敏，设置界面留空会保留已有密钥。

旧配置无需手动删除字段：下一次通过 CLI 或设置界面保存时会输出新格式。
