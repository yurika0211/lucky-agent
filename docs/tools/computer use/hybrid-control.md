# LuckyAgent 终端 + Computer Use 混合控制

## 结论

LuckyAgent 可以在同一个 Agent Loop 中同时使用 `terminal`、`computer_observe` 和
`computer_act`。推荐把终端作为确定性状态接口，把 computer use 作为可见桌面接口：

```text
终端/API 检查状态或启动程序
        ↓
computer_observe 获取最新画面
        ↓
computer_act 执行一个原子 GUI 动作
        ↓
使用 computer_act 返回的截图验证
        ↓
必要时再回到终端确认进程、端口或文件状态
```

## 当前实现

- `internal/agent/tool_intent_gating.go` 能识别“终端、terminal、shell、命令行、进程、
  服务、端口、启动、重启”等终端意图，以及桌面、鼠标、键盘、点击、窗口等 computer
  use 意图。混合请求会同时暴露两类工具。
- `internal/agent/system_prompt.go` 仅在三个工具同时对模型可见时注入
  `Hybrid terminal + computer protocol`，不会污染普通文本任务的提示词前缀。
- `internal/agent/loop_execution.go` 检测到一个批次包含 computer 工具时，会强制该批次
  串行执行，避免终端启动、截图和 GUI 动作竞争全局桌面状态。
- computer 工具返回结构化截图 Observation；Agent Loop 将最新截图作为临时视觉消息
  回灌模型，并删除旧的临时截图，避免视觉上下文无限增长。
- `computer_act` 使用最新 `frame_id`，一次只执行一个原子动作，并在动作后返回新截图。

## 工具选择原则

| 任务 | 首选工具 | 原因 |
| --- | --- | --- |
| 查看进程、端口、日志、文件、配置 | `terminal` / 文件工具 | 结果结构化、可复现 |
| 启动或停止服务、打开应用 | `terminal` | 状态变化明确，避免猜坐标 |
| 点击按钮、拖拽、输入、快捷键 | `computer_act` | 需要真实可见桌面 |
| 判断窗口是否出现、按钮是否变为完成态 | `computer_observe` 或 `computer_act` 返回截图 | 需要视觉证据 |
| 稳定网页或已有 API | API、`opencli`、DOM/可访问性接口 | 比像素点击稳定 |

不要用截图回答终端/API 可以直接回答的问题，也不要用终端命令模拟本应由
`computer_act` 完成的 GUI 动作。

## 执行协议

1. 明确目标和完成条件。
2. 若需要桌面，先调用一次 `computer_observe`。
3. 每一轮最多执行一个依赖当前画面的 `computer_act`。
4. 使用最新 `frame_id`，不要复用旧截图。
5. `computer_act` 已经返回新截图，不要紧接着重复 `computer_observe`。
6. 依赖步骤必须串行。例如先用终端启动 Chrome，再观察桌面，最后切换窗口。
7. 看到完成条件后立即停止，不为“保持活跃”而重复截图。

示例请求：

```text
先用终端检查 Chrome 进程和 8080 端口；如果服务正常，再观察桌面并用 computer use
切换到 Chrome 窗口，最后确认窗口标题。
```

模型应先调用终端，再调用 `computer_observe`，再调用一次 `computer_act`，然后根据
返回截图决定是否完成；不能在同一轮并行发起终端和 GUI 动作。

## 配置

Computer use 默认关闭。启用并允许本地 CLI/TUI：

```json
{
  "tools": {
    "computer_use": {
      "enabled": true,
      "mode": "assist",
      "backend": "auto",
      "allowed_sources": ["cli", "tui"],
      "require_approval": true
    }
  }
}
```

`mode` 可为 `observe`、`assist` 或 `control`。`observe` 只能截图；`assist` 对控制
动作要求批准；`control` 允许在配置的策略范围内自动控制，但敏感输入、发送/发布、
支付、删除和权限授予仍建议保留人工确认。

默认 `allowed_sources` 只有 `cli`、`tui`。HTTP 和 Telegram 等远程来源需要显式加入：

```json
"allowed_sources": ["cli", "tui", "http", "telegram"]
```

这会授予远程入口控制宿主机桌面的能力，应同时配置认证、来源白名单、审批和窗口白名单。
如果 Telegram 仍提示来源不允许，检查 Agent Loop 的 `Source` 是否为 `telegram`，以及
`tools.computer_use.allowed_sources` 是否包含该值。

## 验证

```bash
go test ./internal/agent
go test ./internal/provider ./internal/computer
```

`internal/tool` 中仍存在与本功能无关的旧格式测试失败：测试期待 `✅/❌`，当前实现
返回 `OK/ERR`；不应把该失败误判为混合控制失败。

## 后续增强

- 为不同入口提供独立审批/授权租约，而不是只依赖 `AutoApprove`。
- 增加桌面全局锁和窗口白名单的运行时强校验。
- 对终端启动应用和 GUI 操作建立显式的“状态变更事件”，让模型更容易判断下一步。
- 为 Wayland、Windows、macOS 补齐平台权限诊断和输入后端。
