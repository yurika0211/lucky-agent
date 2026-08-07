# LuckyAgent Computer Use 能力设计

> 状态：MVP 已实现，跨平台与完整审批闭环仍按阶段推进  
> 适用范围：LuckyAgent 本地 Agent Loop、CLI/TUI、HTTP API 与消息网关  
> 参考资料：`luckyAgent：如何实现computer use能力.md`  
> 最后核对代码：2026-08-06

## 1. 结论

LuckyAgent 应当把 computer use（模型观察屏幕并驱动鼠标、键盘）实现为一组有状态的内置工具，而不是把一个平台相关的 `ComputerAdapter` 直接挂到 `Agent` 上。

当前代码已经完成第一版闭环：`internal/computer` 提供会话化 Manager 和 X11 backend，`internal/tool` 提供 `computer_observe` / `computer_act`，Agent Loop 会把最新截图作为临时视觉消息回灌模型，HTTP SSE 会输出 observation 与 approval_required 事件。默认仍关闭 computer use；本机 X11 截图可用，输入动作需要额外安装 `xdotool`。

推荐架构是：

```text
视觉模型
  -> computer_observe / computer_act
  -> 权限与审批层
  -> computer.Manager（按会话管理状态）
  -> Backend（Windows / X11 / Wayland / macOS）
  -> Observation（截图、窗口、尺寸、frame_id）
  -> Agent Loop 将最新截图作为临时视觉消息回灌模型
  -> 模型验证结果并决定下一步
```

参考文档提出的“模型建立、执行器、屏幕反馈”三层闭环是正确起点。LuckyAgent 已经具备模型调用工具、执行工具、回灌结果和继续收敛的 Agent Loop，因此主要工作不是重新实现一个 Agent，而是补齐以下四个缺口：

1. 工具结果无法携带可供模型继续观察的截图。
2. 工具执行没有完整的会话、来源、取消和审批上下文。
3. `PermApprove` 当前只是权限标签，没有真实的人工批准闭环。
4. 图片观察的上下文裁剪、生命周期和前端事件还未定义。

例如，模型点击“设置”按钮后，工具目前只能返回 `click succeeded`。模型看不到点击后的设置窗口，就无法判断动作是否成功；补上截图观察通道后，Agent Loop 才真正构成 computer use。

## 2. 目标与非目标

### 2.1 目标

- 支持截图、点击、双击、拖拽、文本输入、组合键、滚动和等待。
- 每次改变界面后自动获取新观察，让模型依据真实反馈继续行动。
- 使用统一领域接口屏蔽 Windows、Linux 和 macOS 的平台差异。
- 按 LuckyAgent session 隔离状态，避免不同会话抢占同一桌面控制流。
- 对远程调用、敏感输入和不可逆操作提供明确审批边界。
- 复用现有工具注册、Provider、Hook、日志、指标和流式事件体系。

例子：用户说“打开设置并把主题切到深色”，Agent 应先截图定位设置入口，点击后重新截图，再定位主题选项，而不是一次性猜测三个坐标并连续执行。

### 2.2 非目标

- 第一版不追求在所有 Wayland 桌面环境中无条件注入全局输入。
- 第一版不替代 OpenCLI 已有的网页 adapter 和浏览器结构化自动化。
- 第一版不自动处理验证码、支付、密码、授权确认等高风险环节。
- 第一版不把每一张桌面截图永久写入 session、Memory 或 RAG。
- 第一版不直接绑定某一家模型提供商的原生 computer-call 协议。

反例：一个网页有稳定 DOM（网页结构树）和 OpenCLI browser primitive 时，直接根据按钮文本定位通常比对屏幕像素进行视觉点击更稳定，不能因为有了 computer use 就放弃结构化接口。

## 3. 能力路由原则

LuckyAgent 应按下列顺序选择执行路径：

```text
API / 专用 adapter
  -> CLI
  -> DOM / accessibility tree（可访问性结构树）
  -> 视觉 computer use
```

原因是越靠前的接口语义越明确，执行成本和误操作概率通常越低。

- 发布一条平台内容：优先使用平台 adapter，而不是打开浏览器找“发布”按钮。
- 读取网页：优先使用 `web_fetch` 或 `opencli action=web_read`。
- 操作登录态网页：优先使用 `opencli action=browser`。
- 操作只有本地 GUI、没有 API/CLI 的软件：使用 computer use。

现有 `internal/tool/builtin_opencli.go` 已提供 browser session 和 browser primitive，因此 computer use 主要补齐本机 GUI，以及结构化浏览器接口无法覆盖的界面。

## 4. 当前代码基础与缺口

### 4.1 Agent Loop 已具备行动闭环

`internal/agent/loop.go` 的 `processToolCallBatch` 已经完成：

```text
assistant tool_calls
  -> executeToolCallsOrderedGuarded
  -> tool result messages
  -> fitContextWindow
  -> 下一轮模型调用
```

流式路径在 `internal/agent/agent.go` 中也执行相同逻辑。这意味着 computer use 可以复用现有 loop，不需要在平台适配器内部再启动一个 LLM 子循环。

反例：如果让 Windows adapter 自己调用模型决定下一步，LuckyAgent 的超时、会话、日志、Hook 和循环收敛策略都会被绕开，最终形成两套互相不知道状态的 Agent Loop。

### 4.2 工具结果只有文本

当前 `internal/tool/tool.go` 中：

```go
type ToolCallResult struct {
	Output   string
	Metadata map[string]any
}
```

`internal/agent/loop_execution.go` 虽然会保留 Metadata，但 Agent Loop 最终只构造文本 `tool` 消息。Metadata 目前主要用于 memory trace，并不会变成视觉输入。

需要新增结构化 Observation（观察结果），让工具可以返回截图而不必把大段 base64 塞进文本。

### 4.3 Provider 已能发送图片，但图片必须是 user 消息

`internal/provider/provider.go` 已定义 `ContentPart` 和 `ImagePart`。OpenAI 与 Anthropic 适配器都能读取本地图片路径并构造视觉请求。

当前 OpenAI 适配器明确限制图片只能出现在 `user` 消息中。因此一次 computer action 后的消息顺序应是：

```text
assistant: computer_act(...)
tool: action completed, frame_id=frame-13
user: [Computer Observation frame-13] + image content part
```

这样既满足 function calling（函数调用）协议，也能让模型看到新屏幕。

### 4.4 当前审批语义不完整

完整的“暂停—前端批准—恢复”请求协议仍未完成，但 MVP 已在工具层阻止未批准动作，并通过 `approval_required` 事件暴露动作、frame_id 和 reason。`internal/agent/tool_execution_detailed.go` 会把同步/流式调用的 `AutoApprove` 和来源传给工具。

早期缺口示例：

```go
_ = autoApprove
```

`Registry.CallDetailed` 和 `Gateway.checkExecutable` 只会真正拒绝 `PermDeny`。`PermApprove` 会出现在工具菜单中，但不会自动产生等待用户批准的状态。

因此不能仅把 `computer_act` 标成 `PermApprove` 就认为它是安全的。第一版必须在 computer service 内部执行严格策略，后续再将审批机制推广为所有工具共享的能力。

### 4.5 图片会改变上下文裁剪行为

`internal/agent/loop.go` 的 `fitContextWindow` 在发现任意 `ContentParts` 后会直接返回原消息列表，避免裁剪过程丢失图片。

这对单张用户附件可接受，但 computer use 每一步都会产生新截图。如果一直保留旧截图，上下文和视觉 token 会快速增长。

例如一个 20 步操作保留 20 张全屏截图，既增加费用，也让模型可能依据旧界面做出错误判断。正确做法是只保留最新完整帧，旧帧转换为简短文本摘要。

### 4.6 流式事件无法承载截图和审批

`internal/agent/agent.go` 中的 `ChatEvent` 只有文本、工具名、参数、结果和错误。`internal/server/server.go` 的 SSE（服务端事件流）又只输出 `type`、`content` 和 `session_id`。

computer use 至少需要两种新事件：

- `observation`：通知前端有新截图和 frame metadata。
- `approval_required`：通知前端某个动作正在等待批准。

## 5. 总体架构

### 5.1 分层

```text
┌──────────────────────────────────────────────┐
│ Agent Loop                                   │
│ 规划、工具调用、观察回灌、收敛、超时          │
└──────────────────────┬───────────────────────┘
                       │
┌──────────────────────▼───────────────────────┐
│ Tool Layer                                   │
│ computer_observe / computer_act              │
│ 参数校验、工具说明、权限分类                   │
└──────────────────────┬───────────────────────┘
                       │
┌──────────────────────▼───────────────────────┐
│ Computer Service                             │
│ session、frame_id、审批、限步、截图生命周期   │
└──────────────────────┬───────────────────────┘
                       │
┌──────────────────────▼───────────────────────┐
│ Platform Backend                             │
│ Capture / Click / Type / Key / Scroll / Drag │
└──────────────────────────────────────────────┘
```

平台 Backend 只负责操作系统动作，不理解“帮用户打开设置”这样的自然语言目标；自然语言规划由 Agent Loop 和视觉模型负责。

### 5.2 包边界

建议新增：

```text
internal/computer/
  action.go
  observation.go
  backend.go
  manager.go
  policy.go
  storage.go
  backend_x11.go
  backend_wayland.go
  backend_windows.go
  backend_darwin.go
  backend_unsupported.go

internal/tool/
  builtin_computer.go
```

`internal/computer` 不应依赖 `internal/agent`，否则会形成反向依赖。Agent 可以依赖 computer service，tool 层负责把模型参数翻译成领域动作。

## 6. 领域接口设计

### 6.1 Backend 接口

```go
package computer

type Backend interface {
	Name() string
	Capabilities(ctx context.Context) (Capabilities, error)
	Capture(ctx context.Context, target Target) (Observation, error)
	Perform(ctx context.Context, action Action) error
	Close() error
}
```

所有方法都需要 `context.Context`，用于超时和取消。例如用户在点击前取消任务，正在等待窗口变化的操作应立即停止，而不是继续控制桌面。

### 6.2 Action

```go
type ActionKind string

const (
	ActionClick       ActionKind = "click"
	ActionDoubleClick ActionKind = "double_click"
	ActionMove        ActionKind = "move"
	ActionDrag        ActionKind = "drag"
	ActionTypeText    ActionKind = "type"
	ActionKeypress    ActionKind = "keypress"
	ActionScroll      ActionKind = "scroll"
)

type Action struct {
	Kind       ActionKind
	FrameID    string
	DisplayID  string
	X          int
	Y          int
	EndX       int
	EndY       int
	DeltaX     int
	DeltaY     int
	Button     string
	Text       string
	Keys       []string
	DurationMS int
}
```

不建议把 `wait` 作为操作系统 Backend 的动作。等待属于 service 的执行策略：完成动作后等待界面稳定，再调用 `Capture`。

### 6.3 Observation

```go
type Observation struct {
	FrameID      string
	CapturedAt   time.Time
	FilePath     string
	MimeType     string
	Width        int
	Height       int
	ScaleFactor  float64
	DisplayID    string
	ActiveWindow string
	WindowBounds Rect
	SHA256       string
}
```

字段含义：

- `FrameID`：截图版本号，用来防止基于旧画面点击。
- `ScaleFactor`：模型截图坐标到物理屏幕坐标的映射比例。
- `WindowBounds`：限定捕获和点击的目标窗口。
- `SHA256`：检测等待后画面是否仍未改变，避免重复发送相同截图。

例子：模型看到的是宽 1440 的缩放截图，而物理显示器宽 2880，`ScaleFactor=2`。模型点击 `x=720` 时，Backend 应转换为物理坐标 `x=1440`。

### 6.4 Manager

```go
type Manager interface {
	Observe(ctx context.Context, sessionID string, req ObserveRequest) (Observation, error)
	Step(ctx context.Context, sessionID string, action Action) (Observation, error)
	CloseSession(sessionID string) error
}
```

`Step` 应封装：

```text
校验 session 和 frame_id
  -> 审批/策略检查
  -> Perform
  -> settle wait
  -> Capture
  -> 保存最新 frame
  -> 返回 Observation
```

这样模型调用一次 `computer_act` 就能自动收到新截图，不需要再猜测是否应该补一次 screenshot。

## 7. 模型工具设计

### 7.1 computer_observe

用途：读取当前屏幕，不改变鼠标键盘状态。

建议参数：

| 参数 | 必填 | 说明 |
| --- | --- | --- |
| `display_id` | 否 | 目标显示器。 |
| `window` | 否 | 目标窗口标题或标识。 |
| `wait_ms` | 否 | 截图前等待时间。 |
| `reason` | 否 | 模型说明为什么需要观察。 |

建议权限：`PermAuto`，但仍要受 `computer_use.enabled` 和调用来源限制。

返回文本示例：

```text
Observed display=0, frame_id=frame-12, size=1440x900, active_window="Settings"
```

同时返回一条结构化图片 Observation。

### 7.2 computer_act

用途：执行一个鼠标或键盘动作，并自动返回新截图。

建议参数：

| 参数 | 必填 | 说明 |
| --- | --- | --- |
| `action` | 是 | `click`、`double_click`、`drag`、`type`、`keypress`、`scroll`。 |
| `frame_id` | 是 | 模型作出决定时看到的截图版本。 |
| `x` / `y` | 视动作 | 截图坐标系中的目标点。 |
| `end_x` / `end_y` | 拖拽必填 | 拖拽终点。 |
| `text` | 输入必填 | 要输入的文本。 |
| `keys` | 按键必填 | 例如 `["CTRL", "L"]`。 |
| `delta_x` / `delta_y` | 滚动必填 | 水平和垂直滚动量。 |
| `reason` | 是 | 动作目的，供审批界面展示。 |

建议权限：`PermApprove`，并在 computer policy 内做强制二次校验。

一次工具调用只允许一个动作。模型若在同一响应中返回多个 `computer_act`，Agent 应执行第一个，其余返回 `stale observation`，要求模型查看新截图后重新规划。

## 8. 工具结果与视觉观察回灌

### 8.1 ToolCallResult 扩展

建议使用 tool 层自己的中立类型，避免 `internal/tool` 依赖 `internal/provider`：

```go
type Observation struct {
	Kind     string
	FilePath string
	MimeType string
	Metadata map[string]any
}

type ToolCallResult struct {
	Output       string
	Metadata     map[string]any
	Observations []Observation
}
```

Agent 再把 tool Observation 转换成 Provider 的 `ContentPart`。

### 8.2 消息追加顺序

一次 computer tool round 应按以下顺序处理：

1. 追加 assistant tool-call 消息。
2. 执行所有获准的工具。
3. 为每个 call 追加完整的 `tool` 文本结果，保证 tool-call ID 闭合。
4. 删除上一张临时 computer observation。
5. 对纯文本历史执行上下文裁剪。
6. 追加最新的临时 `user` 图片 observation。
7. 调用下一轮模型。

不能在一批 tool calls 尚未全部得到 tool result 时插入 user 图片消息，否则会破坏 `assistant(tool_calls) -> tool(tool_call_id)` 的协议状态机。

### 8.3 统一三条 Loop 路径

当前非流式、native stream 和 simulated stream 都各自追加工具结果。computer use 接入时应抽取统一 helper，例如：

```go
appendExecutedToolResults(
	messages []provider.Message,
	executed []executedToolCall,
	options appendToolResultOptions,
) appendToolResultResult
```

否则很容易出现同步聊天能看截图，但 SSE 流式聊天只能看到文本的行为差异。

## 9. 执行上下文与审批

### 9.1 Context-aware Handler

为了不破坏现有工具，可以增量增加新字段：

```go
type ExecutionContext struct {
	Context     context.Context
	SessionID   string
	Source      string
	UserID      string
	AutoApprove bool
}

type Tool struct {
	// 现有字段保持兼容
	Handler         func(args map[string]any) (string, error)
	DetailedHandler func(args map[string]any) (ToolCallResult, error)

	ContextDetailedHandler func(
		exec ExecutionContext,
		args map[string]any,
	) (ToolCallResult, error)
}
```

Gateway 优先调用 `ContextDetailedHandler`，不存在时回退旧 Handler。

### 9.2 审批级别

建议使用三层控制：

| 模式 | 行为 | 示例 |
| --- | --- | --- |
| `observe` | 只能截图和读取状态 | 帮我看看当前弹窗写了什么。 |
| `assist` | 每个控制动作都需要确认 | 点击“下一步”前展示动作和截图。 |
| `control` | 在限定会话租约内自动控制 | 允许 5 分钟内操作“测试应用”窗口。 |

租约（lease：限定时间和范围的授权）至少包含：

- session ID
- 允许来源
- 允许窗口/应用
- 过期时间
- 最大动作数
- 是否允许文本输入
- 是否允许高风险确认动作

### 9.3 强制人工确认场景

即使处于 `control` 模式，也建议在以下动作前暂停：

- 输入密码、验证码、密钥、银行卡等敏感内容。
- 发送消息、邮件或发布公开内容。
- 确认购买、支付、转账或订阅。
- 删除文件、账号、数据或取消不可恢复操作。
- 同意系统权限、隐私协议或安装未知软件。

纯像素点击无法可靠理解按钮语义，因此第一版不能只依赖模型自报风险。默认策略应保守：远程来源禁用、控制范围限定到窗口、不可逆动作由用户确认。

### 9.4 来源隔离

当前已为 CLI/TUI、HTTP 和 Telegram 路径注入来源；HTTP/Telegram 不在默认 `allowed_sources=["cli","tui"]` 白名单内，因此默认不能控制桌面。后续新增 QQ、飞书等网关时，应继续沿用同一字段。

默认允许：

```json
["cli", "tui"]
```

默认拒绝：

```json
["http", "telegram", "qqofficial", "napcat", "feishu", "weixin"]
```

原因是远程聊天账号一旦被接管，攻击者不应因此获得宿主机桌面控制权。

## 10. 会话、并发与 stale frame

### 10.1 每会话状态

Manager 建议维护：

```go
type SessionState struct {
	SessionID       string
	LatestFrameID   string
	LatestFramePath string
	Target          Target
	StepCount       int
	Lease           *ControlLease
	mu              sync.Mutex
}
```

### 10.2 全局桌面锁

本机通常只有一个真实鼠标指针和一个键盘焦点。即使 LuckyAgent 有多个 session，也不能并行控制同一桌面。

建议采用：

- session 内串行锁：保证同一任务动作有序。
- desktop 全局控制锁：保证两个任务不会交叉输入。
- observe 可以并行时仍应限制截图频率。

例子：会话 A 准备在编辑器输入代码，会话 B 同时把焦点切到浏览器。如果没有全局锁，A 的代码会被输入浏览器地址栏。

### 10.3 stale frame 校验

执行动作前必须满足：

```text
action.frame_id == session.latest_frame_id
```

不满足时返回：

```text
stale observation: expected frame-13, got frame-12; observe the current screen before acting
```

## 11. 坐标与截图规范

### 11.1 坐标系

模型使用的坐标必须与发送给模型的图片像素一致。若图片经过缩放，应保留从模型坐标到物理坐标的映射。

建议：

- 左上角为 `(0, 0)`。
- 坐标使用整数像素。
- 每个 Observation 明确 `width`、`height` 和 `scale_factor`。
- 默认限定单一显示器或活动窗口。
- 点击前验证坐标仍位于目标边界内。

### 11.2 截图格式

- 默认使用 PNG，避免 UI 文字因有损压缩模糊。
- 模型输入最长边建议限制在 1440～1920 像素。
- 图片必须保留原始宽高和缩放信息。
- 相同画面可通过哈希复用，避免重复发送。
- 第一版只保留最近 1～2 张完整截图。

### 11.3 存储位置

截图属于运行时状态，不应写入源码仓库。建议保存到：

```text
${LUCKYAGENT_HOME}/computer/sessions/<session-id>/frame-<n>.png
```

要求：

- 目录和文件仅当前用户可读写。
- session 结束或 TTL 到期后自动删除。
- session 历史只保存 frame metadata，不保存 base64。
- 恢复旧 session 时重新 observe，不复用过期截图。

## 12. 平台 Backend 策略

### 12.1 Linux X11

适合作为首个 PoC（可行性验证）。当前开发环境已验证为 Ubuntu X11，`DISPLAY=:0`，并存在 ImageMagick `import` 截图命令。

当前环境未发现 `xdotool` 或 `ydotool`，因此输入注入仍需要：

- 增加受控的 X11 输入依赖；或
- 使用 Go X11 库实现输入事件；或
- 在 PoC 阶段显式依赖 `xdotool`。

PoC 可使用 `exec.CommandContext` 调用固定二进制和经过验证的参数，不能把模型参数拼接进 shell 字符串。

### 12.2 Linux Wayland

Wayland（Linux 新图形会话协议）通常限制任意全局截图和输入注入，不存在一个对所有桌面环境都通用的 X11 式实现。

建议：

- 启动时检测 `XDG_SESSION_TYPE`。
- 未配置受支持 portal/compositor backend 时返回明确错误。
- 不要静默回退到可能失效或需要 root 的输入方式。

### 12.3 Windows

建议使用独立 `backend_windows.go`，通过 Windows 原生输入和截图 API 实现。需要验证：

- 多显示器与缩放比例。
- UAC（用户账户控制）窗口权限边界。
- 远程桌面和锁屏状态。
- 普通权限进程无法操作高完整性窗口时的错误反馈。

### 12.4 macOS

建议使用独立 `backend_darwin.go`。运行前需要检查：

- Screen Recording（屏幕录制）权限。
- Accessibility（辅助功能控制）权限。
- 权限缺失时给出可操作的诊断信息。

### 12.5 RobotGo 或 sidecar 的取舍

RobotGo 可用于快速验证跨平台鼠标键盘能力，但会引入 CGO（Go 调用 C 依赖的机制）和平台构建依赖，且不能消除 Wayland 与系统权限限制。

独立 sidecar（伴随主进程运行的执行程序）可以把平台依赖和 LuckyAgent 主进程隔离，但会增加安装、版本协商和进程管理成本。

建议：第一阶段使用 X11 backend 验证闭环；领域接口稳定后，再根据发布形态决定原生 Go backend 还是 sidecar，不要在第一阶段同时实现三套生产级平台适配器。

## 13. 建议配置

```json
{
  "computer_use": {
    "enabled": false,
    "backend": "auto",
    "mode": "observe",
    "allowed_sources": ["cli", "tui"],
    "allowed_windows": [],
    "require_approval": true,
    "max_steps": 20,
    "timeout_seconds": 300,
    "step_timeout_seconds": 15,
    "settle_ms": 350,
    "max_screenshot_width": 1440,
    "keep_frames": 2,
    "frame_ttl_seconds": 600
  }
}
```

字段说明：

| 配置 | 说明 |
| --- | --- |
| `enabled` | 总开关，默认关闭。 |
| `backend` | `auto`、`x11`、`wayland`、`windows`、`darwin`。 |
| `mode` | `observe`、`assist`、`control`。 |
| `allowed_sources` | 允许发起 computer use 的入口。 |
| `allowed_windows` | 可控制的窗口白名单；空值不代表远程调用无限制。 |
| `require_approval` | 控制动作是否要求审批。 |
| `max_steps` | 单次任务最多控制动作数。 |
| `settle_ms` | 动作后等待界面稳定的基础时间。 |
| `keep_frames` | 每个 session 保留的完整截图数。 |
| `frame_ttl_seconds` | 截图自动清理时间。 |

上述配置已经同步到 `internal/config/config.go`、默认值合并、`config get/set` 和 `config.example.json`；文档中的 `max_steps=20` 是概念示例，当前 LuckyAgent 默认值为 50；`max_screenshot_width=0` 表示不限制（当前 MVP 尚未做缩放，只做显式超限拒绝）。

## 14. Agent Loop 收敛策略

当前默认 Agent Loop 通常只有 10 次迭代，连续 tool-only 轮次上限通常是 3。computer use 经常需要超过三次观察与动作。

建议引入 computer 专用限制：

- `computer_use.max_steps`：控制动作数上限。
- 总任务超时：限制整个 computer use 过程。
- 单步超时：限制截图或输入 backend 卡死。
- 连续无变化帧上限：例如三次动作后截图哈希仍相同就停止。
- 同一坐标重复点击上限：避免模型陷入无效点击循环。

例子：模型连续三次点击同一坐标，截图哈希都未改变。系统应停止并返回“界面没有响应”，而不是继续点击到全局 Agent 超时。

## 15. Prompt 与模型行为约束

当 computer tools 可见时，系统提示需要增加：

```text
Computer-use policy:
- Observe before the first action.
- Base every action on the latest frame_id.
- Perform at most one computer action per model round.
- Re-observe after every state-changing action.
- Prefer API, CLI, OpenCLI, DOM, or accessibility data when available.
- Stop and request confirmation before credentials, purchases, sends,
  publishes, destructive actions, or permission grants.
- Do not claim success until the resulting screen visibly confirms it.
```

模型能力门控也需要调整：只有同时具备 vision（视觉输入）和 tools/function calling 的模型，才默认暴露像素 computer tools。

没有视觉能力时，可以用 OCR（光学字符识别）或 accessibility tree 做有限操作，但不能把一段截图摘要等同于精确视觉定位。

## 16. 意图门控

启用 `LH_TOOL_INTENT_GATING` 时，`internal/agent/tool_intent_gating.go` 需要识别 computer use 意图。

建议关键词：

```text
电脑、桌面、屏幕、窗口、鼠标、点击、双击、拖拽、滚动、键盘、按键、
GUI、computer use、desktop、screen、window、click、scroll、keypress
```

门控结果：

- “看看当前屏幕”：允许 `computer_observe`。
- “点击设置按钮”：允许 observe 和 act。
- “分析这张用户上传的截图”：只允许 `image_analyze`，不能误开桌面控制。

## 17. 前端与 API 事件

建议扩展 `ChatEvent`：

```go
type ChatEvent struct {
	Type          ChatEventType
	Content       string
	Name          string
	Args          string
	Result        string
	Observation   *ObservationEvent
	Approval      *ApprovalEvent
	Err           error
}
```

SSE 示例：

```json
{
  "type": "observation",
  "session_id": "session-123",
  "frame_id": "frame-12",
  "width": 1440,
  "height": 900,
  "image_url": "/api/v1/computer/sessions/session-123/frames/frame-12"
}
```

审批事件示例：

```json
{
  "type": "approval_required",
  "request_id": "approval-456",
  "tool": "computer_act",
  "action": "click",
  "reason": "Click the Send button",
  "frame_id": "frame-12"
}
```

图片 URL 必须绑定 session 和授权身份，不能暴露任意本地文件路径。

## 18. Provider 原生 Computer Use

部分 Provider 可能提供原生 computer-call / computer-call-output 协议。这类协议通常不是普通 function tool 加一条文本结果可以完整表达的。

当前 `provider.Provider` 和 `FunctionCallingProvider` 主要抽象 Chat 与 function calling。若后续接原生协议，建议增加可选能力接口，而不是污染所有 Provider：

```go
type ComputerUseProvider interface {
	Provider
	ChatComputer(ctx context.Context, input ComputerInput) (*ComputerResponse, error)
}
```

第一版更推荐模型无关的 function-tool 路线，因为它能复用当前 OpenAI、Anthropic 和兼容 Provider 的视觉输入能力，也能先验证 LuckyAgent 自身的执行与安全边界。

## 19. 实施阶段

### 阶段 0：领域模型和 fake backend

目标：不控制真实桌面，先验证工具到视觉观察的完整闭环。

工作项：

- 新增 `internal/computer` 的 Action、Observation、Backend、Manager。
- 实现 fake backend：根据动作返回预置的下一张截图。
- 扩展 `ToolCallResult` 和 context-aware Handler。
- 抽取统一的工具结果追加 helper。
- 实现只保留最新 computer observation。

验收例子：fake backend 依次返回“设置首页”“主题页面”“深色主题已选中”三张图片，Agent 能按 frame 顺序调用 observe、click、click 并在最终画面确认成功。

### 阶段 1：Linux X11 PoC

目标：在当前开发机完成真实截图和输入。

工作项：

- X11 全屏或活动窗口截图。
- click、type、keypress、scroll。
- 坐标缩放和 frame_id 校验。
- 本地 CLI/TUI only。
- `observe` 与显式 `control` 配置。

这一阶段可以显式依赖受控的外部输入工具，但要检查启动依赖并返回明确错误。

### 阶段 2：安全与产品化

目标：让能力可以被日常使用，而不仅是本地演示。

工作项：

- 审批 request 和控制租约。
- 来源注入和来源白名单。
- SSE、TUI、GUI 截图与审批界面。
- 截图鉴权、清理和审计日志。
- computer 专用步数与超时。
- 敏感操作确认策略。

### 阶段 3：跨平台 Backend

目标：补齐 Windows、macOS 和受支持的 Wayland 环境。

每个平台都应通过相同 Backend contract test（契约测试），而不是复制 Agent Loop。

### 阶段 4：Provider 原生协议与性能优化

目标：按模型能力选择更高效的协议。

可选优化：

- Provider 原生 computer-call。
- 局部截图或变化区域截图。
- accessibility tree 与视觉融合。
- 相同帧去重。
- 更准确的视觉 token 预算。

## 20. 测试计划

### 20.1 单元测试

- Action 参数校验：缺坐标、非法按键、超出窗口边界。
- stale frame 拒绝。
- DPI 和缩放坐标转换。
- session 锁和 desktop 全局锁。
- 控制租约过期、来源拒绝和最大步数。
- 截图 TTL 和保留数量。
- 同一帧哈希去重。

### 20.2 Agent Loop 集成测试

- tool result 后图片 observation 的消息顺序正确。
- 一轮多个 function calls 时，所有 tool-call ID 先闭合。
- native stream、simulated stream、sync 三条路径行为一致。
- 上一张 computer 截图会被替换，不会无限累计。
- 非视觉模型看不到 `computer_act`。
- tool intent gating 能区分桌面控制和用户上传截图分析。

### 20.3 Provider 请求测试

- OpenAI 请求体包含 user `image_url` part。
- Anthropic 请求体包含 user image block。
- 本地图片不存在时返回明确错误。
- computer observation 不会作为非法的 image tool message 发送。

### 20.4 平台集成测试

Linux X11 可在 Xvfb（虚拟 X 显示器）或隔离桌面中运行一个确定性测试窗口：

```text
初始：按钮文本为 "Open"
点击后：出现输入框
输入 "LuckyAgent"
回车后：显示 "Success"
```

验收不应只检查 API 返回 success，还要通过最终截图或窗口状态确认界面出现 `Success`。

### 20.5 安全测试

- HTTP/Telegram 默认不能调用 computer tools。
- `observe` 模式不能执行 click/type。
- 未批准的 `computer_act` 不会到达 Backend。
- 过期 frame 无法点击。
- 图片接口无法通过路径穿越读取任意本地文件。
- 取消请求后 Backend 不再继续输入。

## 21. 验收标准

第一版可交付需要同时满足：

- 使用视觉模型和真实 X11 桌面完成“观察、点击、输入、确认结果”的闭环。
- 每个改变界面的动作都会生成新 frame，并要求下一步使用最新 `frame_id`。
- 同一轮不会盲执行多个 computer action。
- 默认配置关闭 computer use。
- 默认仅本地 CLI/TUI 可用。
- 未批准控制动作不会执行。
- session 中最多保留配置数量的截图，过期截图会清理。
- sync、native stream 和 simulated stream 行为一致。
- OpenCLI 浏览器路径仍然优先于视觉网页点击。
- 测试覆盖 fake backend、Agent Loop、Provider 图片请求和安全策略。

## 22. 主要风险与应对

| 风险 | 后果 | 应对 |
| --- | --- | --- |
| 旧截图坐标 | 点击错误位置 | 强制 `frame_id` 校验。 |
| 多 session 争抢焦点 | 输入进入错误窗口 | desktop 全局锁和窗口限定。 |
| 图片无限累计 | 上下文和费用失控 | 只保留最新完整帧。 |
| 远程账号被接管 | 宿主机桌面被控制 | 默认只允许 CLI/TUI。 |
| Wayland 限制 | 截图或输入不可用 | 能力探测和明确 unsupported。 |
| 模型误判危险按钮 | 删除、发送或支付 | 人工审批和控制租约。 |
| 跨平台依赖复杂 | 构建和安装失败 | 稳定领域接口，分阶段实现 Backend。 |
| Loop 上限过低 | 三步后提前终止 | computer 专用 step/timeout 配置。 |

## 23. 最终建议

不要从“同时支持 Windows、Linux、macOS”开始，也不要先接某一家 Provider 的原生 computer-use API。

最稳妥的知识链条是：

```text
先用 fake backend 验证截图回灌
  -> 再用当前 X11 环境验证真实输入
  -> 再补审批、来源和前端事件
  -> 最后扩展跨平台与原生 Provider 协议
```

这样每一阶段都有可以独立验证的结果，而且核心 Agent Loop、工具协议和安全模型不会被平台细节绑死。
