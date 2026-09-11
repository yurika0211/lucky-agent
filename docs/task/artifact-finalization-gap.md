# 文件产物任务早收束问题分析

## 结论

当前 LuckyAgent 对“保存为文件、创建文档、导出报告、发送附件”这类产物任务缺少硬验收门。

一句话版：`file_write` 已经会被开放给模型，但 Agent Loop 不会检查模型是否真的写入文件、验证文件存在，就允许模型用纯文本最终回答收束。

这会导致：

- 模型说“我已保存文件”，但工具轨迹里没有 `file_write`。
- Telegram 解析到 `MEDIA:/path` 后尝试发送文件，但文件不存在。
- 发送失败发生在 gateway 层，agent loop 已经结束，模型没有机会修正路径或补写文件。

## 复现场景

用户要求：

```text
保存为 md 文档
```

实际工具轨迹：

```text
file_read -> file_list -> file_read -> file_list -> file_read -> file_read
```

缺失关键动作：

```text
file_write
file_list 或 file_read 验证目标文件存在
```

Telegram 后续报错：

```text
Failed to send media response: telegram: stat media file: stat ... no such file or directory
```

说明模型输出了一个文件路径或 `MEDIA:` 产物指令，但该路径对应文件并不存在。

## 代码链路

### 1. 保存意图能识别，但只开放工具

`internal/agent/tool_intent_gating.go`

`hasEditToolIntent` 会把这些词识别为编辑/写入意图：

```go
"写个", "写一个", "输出", "生成", "保存",
"创建", "commit", "push",
```

然后在 edit intent 下开放：

```go
file_patch
file_write
file_mkdir
file_move
```

这一步只解决“模型能不能看到写文件工具”，没有解决“最终回答前是否必须用写文件工具”。

### 2. 直接文本回复会被立即视为完成

`internal/agent/loop.go`

`processDirectResponse` 处理没有 tool call 的模型回复。只要内容非空，且不是 length 截断，当前逻辑会直接认为任务完成：

```go
return messages, true, raw
```

因此在产物任务中，模型只要说：

```text
我现在直接创建完整的第二套试卷文件
```

或者：

```text
已保存到 /path/report.md
```

Agent Loop 就可能 finalize，即使没有任何 `file_write` 成功记录。

### 3. file_write 本身是可用的

`internal/tool/builtin_fs.go`

`file_write` 成功时会真实写入文件，并返回：

```text
Written <n> bytes to <path> (sha256 <hash>)
```

所以问题不在文件写入工具本身，而在 loop 没有把“产物任务必须写入并验证”作为收束条件。

### 4. Telegram delivery rule 是软提示

`internal/gateway/telegram/handler.go`

Telegram handler 会把 delivery rule 追加到用户输入：

```go
input = input.WithRoutingText(telegramMediaDeliveryGuidance(input.RoutingText))
```

规则大意是：

```text
If you want Telegram to send a file, save it to a real local file first and include MEDIA:/absolute/path
```

但这仍然是 prompt 约束。模型可能不遵守，系统也没有硬校验。

### 5. MEDIA 解析不验证文件存在

`internal/gateway/telegram/outbound_media.go`

`resolveOutboundMediaResponse` 只解析 `MEDIA:/path` 或 `tg://document ...`，不做 `os.Stat`。

真正的文件存在性检查在：

`internal/gateway/telegram/adapter.go`

```go
info, err := os.Stat(source)
if err != nil {
    return nil, fmt.Errorf("telegram: stat media file: %w", err)
}
```

此时已经进入发送阶段。如果失败，只会由 gateway 发一条错误消息：

```text
Failed to send media response: ...
```

不会回到 agent loop 让模型补写文件或修正路径。

## 根因

根因不是“工具不可用”，而是缺少产物任务的完成判定。

当前完成判定主要是：

```text
模型没有 tool_calls
  -> 文本非空
  -> finalize
```

对文件产物任务，正确完成判定应该是：

```text
用户要求保存/创建/导出/发送文件
  -> 看到成功 file_write 或等价产物生成工具
  -> 验证最终路径存在
  -> 如果需要 Telegram 发送，则 MEDIA 路径必须存在且非目录
  -> finalize
```

## 影响范围

会受影响的任务类型：

- 保存 Markdown、TXT、JSON、CSV。
- 生成试卷、报告、总结文档。
- 导出 PDF、图片、音频。
- Telegram/QQ 要发送附件。
- 模型引用本地文件路径作为最终交付物。

不会直接受影响的任务：

- 只读解释。
- 单纯问答。
- 只需要 inline 文本输出的总结。
- 已经明确“不保存文件，只贴内容”的请求。

## 推荐修复

### 方案 A：增加 artifact finalization guard

新增一个收束守卫：

```text
ArtifactFinalizationGuard
```

职责：

1. 从用户输入识别产物意图。
2. 记录本轮成功的产物工具调用。
3. 解析最终回答中的本地路径和 `MEDIA:` 指令。
4. 在 finalize 前验证路径存在。
5. 如果不满足条件，阻止 finalize，并追加恢复提示让模型继续写文件或修正路径。

建议状态结构：

```go
type artifactFinalizationGuard struct {
    Required       bool
    WantsMedia     bool
    SuccessfulWrites []string
    VerifiedPaths    []string
    FailReason     string
}
```

建议触发词：

```text
保存
创建文件
写入文件
生成文档
导出
发给我
发送附件
保存为 md
保存成 markdown
```

### 方案 B：把 file_write 结果纳入 loop state

在 `processToolCallBatch` 处理工具结果时识别：

```text
file_write -> Written ... to <path>
image_generate -> paths
text_to_speech -> path
opencli_saved_markdown -> saved path
```

记录到 loop runtime state：

```go
loopState.artifacts.RecordToolResult(toolName, result)
```

### 方案 C：finalize 前统一验收

在非流式和流式 finalize 前都调用：

```go
if msg, blocked := a.artifactGuardBlockMessage(turnInput, response, loopState); blocked {
    messages = append(messages, provider.Message{Role: "user", Content: msg})
    continue
}
```

恢复提示应该明确要求模型下一轮做真实工具调用：

```text
The user requested a saved file/artifact, but no successful file_write or verified artifact path was observed.
Call file_write to create the requested file, then verify the path exists before finalizing.
Do not claim the file is saved until the write succeeds.
```

如果最终回答包含 `MEDIA:/path`，但路径不存在：

```text
The final answer references MEDIA:/path, but that file does not exist.
Either create it with file_write or remove the MEDIA line and explain the failure.
```

### 方案 D：Telegram 发送前预校验

在 `resolveOutboundMediaResponse` 或 `sendAssistantResponse` 之后、发送之前增加本地路径校验。

如果本地 `MEDIA:` 路径不存在，不应该先发“已生成文件”的文本，再发送失败。

更安全的行为：

```text
检测到本地媒体路径不存在
  -> 不发送正文成功消息
  -> 返回明确错误
  -> 可选：让 agent retry 一轮修正
```

这不是替代 agent guard，而是 gateway 侧最后防线。

## 最小实现顺序

推荐分三步做：

### Step 1：只做 finalization guard

目标：

- 用户要求保存文件时，没有成功 `file_write` 就不能 finalize。
- 先不处理所有媒体工具，只覆盖 `file_write`。

验收：

```text
用户：保存为 md 文档
模型只输出文本不调用 file_write
系统追加恢复提示并继续一轮
```

### Step 2：验证最终路径

目标：

- 最终回答中出现的本地路径、`MEDIA:/path` 必须存在。
- 路径不存在时阻止 finalize。

验收：

```text
模型输出 MEDIA:/tmp/missing.md
系统检测不存在
系统要求模型创建文件或删除 MEDIA 行
```

### Step 3：扩展到生成类工具

覆盖：

- `image_generate`
- `text_to_speech`
- `opencli` 保存 markdown
- 后续 PDF 导出工具

## 测试建议

### 1. 保存 md 必须调用 file_write

构造 provider：

1. 第一轮返回纯文本：“我已保存到 /tmp/a.md”。
2. 期望 loop 不 finalize，而是追加恢复提示。
3. 第二轮调用 `file_write`。
4. 第三轮正常 finalize。

### 2. MEDIA 路径不存在不能 finalize

构造最终回答：

```text
已生成文件
MEDIA:/tmp/not-exist.md
```

期望：

- guard 阻止 finalize。
- 恢复提示包含 “file does not exist”。

### 3. 成功写入后可以 finalize

工具结果：

```text
Written 123 bytes to /tmp/report.md (sha256 ...)
```

最终回答：

```text
已保存。
MEDIA:/tmp/report.md
```

期望：

- 文件存在。
- finalize 通过。

### 4. 只读任务不触发 guard

用户：

```text
总结这个文件内容，不需要保存
```

期望：

- 不要求 `file_write`。
- 纯文本回答可以 finalize。

## 与现有 multi-agent guard 的关系

`internal/agent/task_finalization.go` 当前只处理 pending/running multi-agent task：

```text
如果还有后台 task 没完成，最终回答追加 pending task 提示。
```

它解决的是“子任务还没结束就收束”的问题。

本文的问题是“文件产物没有生成就收束”。

两者都属于 finalization guard，但检查对象不同：

```text
Task finalization guard
  -> 检查后台任务状态。

Artifact finalization guard
  -> 检查文件/附件/产物是否真实存在。
```

建议后续统一成：

```text
FinalizationGuards
  -> TaskGuard
  -> ArtifactGuard
  -> MemoryGate
  -> SearchSynthesisGate
```

## 判断标准

修复后，下面这类输出不应再出现：

```text
我现在直接创建完整的第二套试卷文件：
References:
...
```

除非工具轨迹里已经出现：

```text
file_write success
file_read/file_list/stat verification
```

如果写入失败，最终回答应该明确说：

```text
文件还没有保存成功，失败原因是 ...
```

而不是声称已经保存。
