# Codex App Server 集成

LuckyAgent 通过专用 `codex.*` 工具调用本机的 `codex app-server`，Codex 继续负责编码 agent loop、sandbox 和文件/命令执行。该能力默认关闭。

## 配置

在 `~/.luckyagent/config.json` 中启用：

```json
{
  "codex": {
    "enabled": true,
    "command": "codex",
    "args": ["app-server"],
    "approval_mode": "gateway",
    "default_sandbox": "workspace-write",
    "cwd_allowlist": ["/home/me/src"],
    "max_events": 256
  }
}
```

`approval_mode` 有三种值：

- `gateway`：默认值。审批请求保存在 LuckyAgent 中，使用 `codex.respond_approval` 明确处理。
- `auto`：自动返回 `accept`，只适合明确隔离的自动化环境。
- `deny`：自动返回 `decline`。

`cwd_allowlist` 为空表示不增加目录限制；配置后，`codex.start_thread` 和 `codex.resume_thread` 只能使用白名单目录及其子目录。

## 工具

- `codex.start_thread`
- `codex.resume_thread`
- `codex.start_turn`
- `codex.steer_turn`
- `codex.subscribe_events`
- `codex.respond_approval`
- `codex.get_turn_summary`

事件工具返回有界的摘要和游标，不把原始 JSON-RPC 流全部放进模型上下文。审批项包含请求 ID、命令或理由、thread/turn 关联信息；网关模式下 turn 会保持等待，直到调用 `codex.respond_approval`。

当前适配器使用本机 stdio JSON-RPC。远程 WebSocket、跨机鉴权、持久化的 LuckyAgent session 映射和取消接口留在后续阶段。
