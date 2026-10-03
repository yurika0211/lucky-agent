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
- `computer_act` 使用最新 `frame_id`，执行一个原子动作或有长度上限的确定性短序列，
  然后返回新的截图或控件树。其他会话操作桌面后，旧帧会失效。

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

`mode` 可为 `observe`、`assist` 或 `control`。`observe` 只能观察截图/控件树；`assist` 对控制
动作要求批准；`control` 允许在配置的策略范围内自动控制，但敏感输入、发送/发布、
支付、删除和权限授予仍建议保留人工确认。

默认 `allowed_sources` 只有 `cli`、`tui`。HTTP 和 Telegram 等远程来源需要显式加入：

```json
"allowed_sources": ["cli", "tui", "http", "telegram"]
```

这会授予远程入口控制宿主机桌面的能力，应同时配置认证、来源白名单、审批和窗口白名单。
如果 Telegram 仍提示来源不允许，检查 Agent Loop 的 `Source` 是否为 `telegram`，以及
`tools.computer_use.allowed_sources` 是否包含该值。

## 大分辨率截图

默认截图捕获整个后端画面，再按配置缩小，保留完整画面。只有显式传入 `window` 或
`region` 时才缩小捕获范围；Wayland 的画面范围是用户在门户中选择的一个显示器。

- `tools.computer_use.max_screenshot_width` 默认 `0`，不限制宽度。
  例如设为 `1920` 后，3840×2160 的截图会返回为 1920×1080；不会只截取左上角。
- `tools.computer_use.max_observation_bytes` 默认 `10485760`（10 MiB）。
  PNG 超过该上限时，继续等比缩小并重新编码，直到满足上限。
  如果配置小到连 1×1 PNG 都无法容纳，会返回明确错误。
- 两项上限均满足的截图保持原样，不额外解码或重编码。
- `computer_act` 的 `x/y/end_x/end_y` 使用**返回截图**的像素坐标。
  Manager 自动换算到原始桌面坐标。例如 4K 桌面缩到 1920×1080 后，
  截图中的 `(960, 540)` 会映射到桌面 `(1920, 1080)`；模型无需自行乘倍率。
  `width/height` 为返回图片尺寸，`scale_factor` 包含宽度缩放比例。
- `capture_bounds` 记录后端原始捕获范围，`origin_x/origin_y` 记录返回图片左上角的偏移；
  可对照原始范围、返回尺寸和显示器尺寸判断是缩小、显式区域选择，还是底层未捕获完整画面。

这些处理发生在后端成功捕获之后。如果 Linux 的 ImageMagick 截图命令本身失败，
仍需根据其错误信息排查显示环境或资源限制。缩小会减少小字细节，但不会裁掉屏幕边缘。

`config.example.json` 中已有这两项；可按需要调整最大宽度。

## 窗口和区域

X11 的 `window` 支持 `active`、窗口 ID、唯一匹配的窗口标题。返回截图包含窗口在
桌面上的可见部分；被遮挡或伸出屏幕的内容不会凭空恢复。标题匹配多个窗口时要求
显式指定 ID。空 `window` 保持整个 X11 根显示区域，不自动截取活动窗口。

```json
{"window":"active","region":{"x":100,"y":80,"width":800,"height":500},"format":"image"}
```

`region` 使用选中窗口/显示画面的原始像素，必须完整落在画面内。返回图片中的 `(0,0)`
对应区域左上角；Manager 自动叠加窗口偏移、区域偏移和缩放比例。动作后会继续观察
同一个窗口/区域。X11 动作前会检查窗口移动、缩放及焦点变化，避免沿用失效坐标。

## 短动作序列和动态等待

`computer_act` 可传 `action` 或 `actions`，二者不能同时使用。例如确定已经找到了输入框：

```json
{
  "frame_id":"frame-1",
  "reason":"填写已定位的输入框",
  "actions":[
    {"action":"click","x":120,"y":80},
    {"action":"type","text":"你好 LuckyAgent"}
  ]
}
```

- `max_batch_actions` 默认 `5`，配置范围 `1–10`。整批先验证参数、权限和剩余步数，
  在同一个桌面锁内顺序执行，最后返回一次观察。每个动作仍计入 `max_steps`。
- 仅首项允许点击、移动、拖拽、`invoke` 或 `focus`；后续只能输入、按键、滚动或
  `set_text`。需要根据新画面选择另一个目标时，应结束批次、先观察结果。
- 失败立即停止，错误报告已完成数量；失败的动作本身也可能执行了一部分。
  后续操作必须重新观察，不能自动重放整批动作。
- `settle_mode` 默认 `adaptive`：比较 PNG 图像数据，忽略时间戳等非画面信息。
  连续两次比较一致时提前返回，`settle_ms` 是采样等待预算。设为 `fixed` 可恢复固定等待。
  持续动画会耗尽预算，返回最后一帧且 `stable=false`；单次截图耗时另计。
- `step_timeout_seconds` 现在限制一次观察或整批动作，包括后端命令和辅助进程。
  `stable=true` 只表示画面暂时未变化，模型仍需检查应用是否真正完成任务。

## AT-SPI 控件操作

AT-SPI 是 Linux 桌面应用的无障碍接口，可以直接读取控件名称、角色、边界和支持的动作。
X11 默认定位活动窗口；Wayland 可用窗口标题筛选。应用没有提供无障碍信息时仍使用截图。

```json
{"window":"active","format":"tree"}
```

`format` 可取 `image`（默认）、`tree`（只读控件树，不生成或发送截图）、`both`。
树最多返回 200 个可见节点，截断时标记 `truncated=true`。用最新帧里的 ID 操作控件：

```json
{"action":"set_text","frame_id":"frame-2","element_id":"来自返回树的ID","text":"你好","reason":"填写输入框"}
```

`invoke` 调用节点列出的 `element_action`，`focus` 聚焦控件，`set_text` 设置完整文本
（空字符串用于清空）。`set_text` 与键盘输入一样受 `allow_text_input` 控制；窗口白名单、
来源限制和审批策略仍然生效。树模式只接受控件操作；像素点击或全局键盘操作需要先获取图片。
控件 ID 不允许猜测；辅助进程重启、控件失效或名称/角色变化后必须重新观察。

X11 截图仍只需要 `import`、`xdotool`。AT-SPI 额外需要 Python 3、PyGObject 和 Atspi typelib
（Ubuntu/Debian 通常为 `python3-gi`、`gir1.2-atspi-2.0`）。辅助进程按需启动并复用。

## Wayland 原生后端

`backend=auto` 看到 Wayland 会话时优先走桌面门户，即使同时存在 `DISPLAY` 也不会选择
只能看到部分窗口的 XWayland。首次观察由系统弹窗让用户选择一个显示器；`observe`
模式只申请屏幕共享，控制模式另申请键盘/鼠标权限。拒绝或撤销权限会返回错误。

后端复用一个门户会话和 PipeWire 视频流，只在请求截图时编码 PNG。动作使用门户输入接口，
自动将物理截图像素映射到显示器的逻辑坐标，处理桌面缩放。分辨率改变或旧流失效时要求重新观察。
当前支持一个选中显示器及其 `region`，不支持将多个显示器拼接成一个画面，也不支持按标题截取
Wayland 窗口；控件树仍可按标题筛选。没有窗口标题证据时，配置了窗口白名单的像素操作会拒绝执行。

额外依赖：桌面环境提供的 `xdg-desktop-portal` 实现、PipeWire、Python GI/GStreamer、
`pipewiresrc`、`videoconvert`、`pngenc`、`appsrc` 和 `appsink` 插件。Ubuntu/Debian 通常涉及
`gir1.2-gstreamer-1.0`、`gir1.2-gst-plugins-base-1.0`、`gstreamer1.0-pipewire`、
`gstreamer1.0-plugins-base` 和 `gstreamer1.0-plugins-good`；其他发行版包名不同。

协议依据：[RemoteDesktop](https://flatpak.github.io/xdg-desktop-portal/docs/doc-org.freedesktop.portal.RemoteDesktop.html)、
[ScreenCast](https://flatpak.github.io/xdg-desktop-portal/docs/doc-org.freedesktop.portal.ScreenCast.html)、
[AT-SPI Action](https://gnome.pages.gitlab.gnome.org/at-spi2-core/libatspi/method.Action.do_action.html)。

## WSLg 后端

WSLg 使用 Weston RDP backend，当前没有 ScreenCast/RemoteDesktop Portal。`backend=auto` 检测到
WSLg 后会选择 `wslg`，Linux 版 LA 通过 `powershell.exe` 调用宿主 Windows 的 GDI 截图和
`SendInput`。请求参数通过临时 JSON 文件传递，不经过 shell 命令拼接。该后端覆盖完整 Windows 虚拟桌面，因此截图和输入可能涉及 WSLg 应用以及宿主
Windows 应用；现有 `allowed_windows`、来源白名单、审批和 `allow_text_input` 仍然生效。

需要 Windows interop、`WSL2_GUI_APPS_ENABLED=1` 和可调用的 `powershell.exe`。如果路径不在
默认位置，可设置 `LA_WSLG_POWERSHELL`。WSLg 后端支持完整桌面截图、点击、双击、移动、拖拽、
滚动、快捷键和 Unicode 文本输入；当前使用完整虚拟桌面，不提供按窗口的宿主截图。

## 验证

```bash
go test ./internal/computer
go test ./internal/tool ./internal/config ./internal/agent -run 'Computer|Hybrid|ToOpenAIFormat'
go test -race ./internal/computer
go test ./internal/computer -run '^$' -bench 'BenchmarkComputer(Sequence|Settle)' -benchmem -benchtime=3x
# 可选：在 X11 桌面创建自己的、不抢焦点的 GTK 测试窗口，验证截图/中文输入/按钮调用
LA_COMPUTER_X11_SMOKE=1 go test ./internal/computer -run TestX11DesktopIntegration -v
```

本次修改已通过 `internal/computer`、`internal/tool`、`internal/provider`、`internal/config`
和 `internal/agent` 的包级测试；真实 X11/GTK 测试验证了窗口截图、中文文本设置及按钮调用。

## 后续增强

- 为不同入口提供独立审批/授权租约，而不是只依赖 `AutoApprove`。
- 对终端启动应用和 GUI 操作建立显式的“状态变更事件”，让模型更容易判断下一步。
- 在真实 Wayland 桌面验收门户权限、缩放及恢复流程；当前开发机为 GNOME/X11，已验证协议单测
  和真实 GStreamer 编码链路，尚未完成 Wayland 桌面端到端验收。
- 扩展 Wayland 多显示器、窗口选择和 macOS 后端。
