# Grok agent 集成

LuckyAgent 通过专用 `grok.*` 工具调用本机的 `grok agent stdio`。Grok 继续负责编码 agent loop、工具执行和文件修改。该能力默认关闭。

## 配置

在 `~/.luckyagent/config.json` 中启用：

```json
{
  "grok": {
    "enabled": true,
    "command": "grok",
    "args": ["agent", "stdio"],
    "approval_mode": "gateway",
    "model": "",
    "cwd_allowlist": ["/home/me/src"],
    "max_events": 256
  }
}
```

认证使用本机 `~/.grok/auth.json`，或环境变量 `XAI_API_KEY`。

`approval_mode` 有三种值：

- `gateway`：默认值。审批请求保存在 LuckyAgent 中，使用 `grok.respond_approval` 明确处理。
- `auto`：自动允许当前这一次工具调用，只适合明确隔离的自动化环境。
- `deny`：自动拒绝。

`cwd_allowlist` 为空表示不增加目录限制；配置后，`grok.start_session` 和 `grok.resume_session` 只能使用白名单目录及其子目录。

## 工具

- `grok.start_session`
- `grok.resume_session`
- `grok.start_turn`
- `grok.subscribe_events`
- `grok.respond_approval`
- `grok.get_turn_summary`

`grok.start_turn` 在 `gateway` 模式下会立即返回正在运行的 turn，避免权限请求阻塞当前模型调用；用 `grok.subscribe_events` 和 `grok.get_turn_summary` 轮询进度，权限请求出现后调用 `grok.respond_approval`。`auto` 和 `deny` 模式仍会等待这一轮 `session/prompt` 结束。决策值是 `allow`、`allow_always`、`deny`、`cancel`，适配器会按 ACP 请求中的实际 option ID 回包。

当前适配器使用本机 stdio JSON-RPC。ACP 没有和 Codex `turn/steer` 对应的飞行中追加指令，下一句说明要等当前 turn 结束后再发一次 `grok.start_turn`。
