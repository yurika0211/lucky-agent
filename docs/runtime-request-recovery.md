# 运行时请求与恢复选项

## WebSocket 启动恢复

API 模块的 `websocket.RunStore.Restoreable` 扫描会话 ID、任务记录和日志序号，
恢复 `queued`、`running` 任务，不把所有历史事件加载进常驻会话表。扫描同时覆盖
JSON 快照与 JSONL 增量日志，包括尚未生成快照的会话；按序号应用较新的任务
更新，流式跳过事件内容。旧 JSON 字段顺序仍兼容。

已缓存会话与纯内存 store 的行为保留。重连回放或任务更新仍按需读取完整快照
和日志，因此恢复执行中的会话后仍可能加载它的历史。附件、TaskID、MaxIter
和重连游标保持兼容。备份必须同时保留两种文件；旧二进制不能读取未合并日志，
回退前需兼容读取器或导出。启动元数据扫描不代替完整历史校验，回放错误通过
带错误返回值的接口报告。本次没有测量 RSS 或启动耗时。

详细实现说明位于 API 仓库的 `docs/websocket-recovery.md`。

## 提示词缓存 key

可选配置：

```json
{
  "models": {
    "endpoints": {
      "chat": {
        "prompt_cache_key": "luckyagent-project-a"
      }
    }
  }
}
```

```bash
lh config set models.endpoints.chat.prompt_cache_key luckyagent-project-a
lh config set models.endpoints.chat.prompt_cache_key ""
```

默认留空，不发送请求字段；仅在端点支持 `prompt_cache_key` 时填写。主节点的
Chat Completions / Responses 协议、流式 / 非流式请求都会传递该值。
`fallbacks[].prompt_cache_key` 为降级节点独立配置，空值不继承主节点。
旧 `llm_provider.prompt_cache_key` 可读取，并迁移到 models 配置。

key 由配置明确指定，不从用户文本或系统提示词自动计算。工具列表按名称排序，
JSON Schema 的 `required` 排序，输入对象与 `enum`、`oneOf` 等顺序保留。
这能减少无意义的请求前缀变化；实际缓存是否命中由服务端决定，本次没有测量
命中率、费用或延迟收益。参考 [OpenAI 缓存说明](https://developers.openai.com/api/docs/guides/prompt-caching)。

详细实现说明位于 runtime 仓库的 `docs/prompt-caching.md`。

## X11 原生截图回退

X11 后端先调用 ImageMagick `import`，缺失或失败时尝试本地 Unix socket 的
GetImage。原生路径仅支持 Linux 本地 DISPLAY、TrueColor 及 8/16/24/32 位像素，
支持当前窗口裁剪，最多 64 Mi 像素；连接和读取受五秒超时及调用取消约束。
认证读取 `XAUTHORITY` 或 `~/.Xauthority` 的 MIT cookie。

输入与窗口定位仍使用 `xdotool`。远程 DISPLAY 仍需 ImageMagick。WSLg 后端独立
处理 Windows 桌面，X11 不会隐式切换桌面。无需新增 computer 配置字段，tools
模块新增 Go 依赖 `github.com/jezek/xgb`。本次未进行真实桌面截屏验证。

详细实现说明位于 tools 仓库的 `docs/x11-capture.md`。
