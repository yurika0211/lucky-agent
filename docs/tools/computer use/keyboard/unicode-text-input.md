# LuckyAgent Computer Use 键盘与 Unicode 文本输入设计

> 状态：计划中  
> 适用范围：Linux X11 backend，CLI/TUI/Telegram 等所有已获授权的 computer use 来源  
> 最后核对代码：2026-08-06

## 1. 结论

LuckyAgent 的 `computer_act(action=type)` 应表示“向已聚焦的控件写入指定文本”，而不是“逐键模拟当前系统输入法”。

当前 X11 实现位于 `internal/computer/backend_x11.go`，文本动作直接执行：

```text
xdotool type --clearmodifiers -- <text>
```

它依赖当前 X11 键盘布局，无法稳定处理中文、emoji、组合字符和输入法候选词。因此仅修改提示词不能解决问题：模型即使知道要输入中文，底层仍没有可靠的 Unicode 注入路径。

V1 应使用剪贴板粘贴完成 Unicode 文本输入；不要在 V1 驱动 Fcitx/IBus 的拼音组合态、候选词窗口或选词逻辑。

## 2. 目标与边界

### 2.1 目标

- 在任意输入法开关状态下，稳定输入中文、英文、emoji、标点和多行文本。
- 保持 `computer_act` 的现有工具参数不变。
- 将输入策略放在 computer backend，Telegram 与本地调用共享同一行为。
- 输入失败时返回明确、可诊断的错误，不产生乱码或半截拼音。
- 不将用户原有剪贴板内容写入日志、工具结果、会话、截图元数据或模型上下文。

### 2.2 非目标

- 不模拟人类输入拼音并从 Fcitx/IBus 候选列表选词。
- 不承诺密码框、远程桌面或禁止粘贴的控件一定可写入。
- 不在本次改动中实现 Wayland、Windows 或 macOS backend。
- 不放宽已有 `allow_text_input`、来源白名单或审批策略。

## 3. 对外行为

`computer_act` 的调用方式不变：

```json
{
  "action": "type",
  "frame_id": "frame-17",
  "text": "你好，LuckyAgent 😀\n第二行",
  "reason": "填写消息输入框"
}
```

新增配置：

```json
{
  "tools": {
    "computer_use": {
      "text_input_mode": "auto",
      "preserve_clipboard": true
    }
  }
}
```

| 配置 | 行为 |
| --- | --- |
| `auto` | ASCII 文本使用按键输入；出现非 ASCII 字符时使用剪贴板粘贴。 |
| `clipboard` | 所有文本都使用剪贴板粘贴。适合需要一致行为的远程调用。 |
| `keystroke` | 只允许 ASCII 按键输入；非 ASCII 文本返回错误，不尝试拼音输入。 |

`preserve_clipboard=true` 时，只在确认剪贴板仍是本次注入的文本后才恢复原有纯文本内容，以免覆盖用户在执行期间新复制的内容。旧剪贴板值只存在于本地短生命周期内存中，不出现在任何返回值中。

## 4. 实现设计

### 4.1 配置层

在 `internal/config/config.go` 的 `ComputerUseToolConfig` 增加：

```go
TextInputMode     string `json:"text_input_mode,omitempty"`     // auto, clipboard, keystroke
PreserveClipboard bool   `json:"preserve_clipboard,omitempty"`
```

默认 `text_input_mode=auto`，`preserve_clipboard=true`。空值归一化为默认值；其他值应在加载或 backend 初始化时明确拒绝。以下位置必须同步更新，避免出现“CLI 显示已设置但运行时未生效”的配置断层：

- `DefaultConfig` 与 `normalizeConfig`。
- 配置文件示例 `config.example.json`。
- `ConfigManager.Set`、旧版 `Extra` 键迁移，以及 `lh config get`。
- `internal/config/config_test.go` 中的设置、克隆和默认值测试。

### 4.2 Backend 注入点

保持 `computer.Action` 与模型工具 schema 不变。在 `internal/computer` 内部引入可替换的文本注入器，由 X11 backend 在处理 `ActionTypeText` 时调用：

```go
type TextInputStrategy interface {
    WriteText(ctx context.Context, text string) (TextInputResult, error)
}

type TextInputResult struct {
    Method            string // keystroke or clipboard
    ClipboardRestored bool
}
```

`NewBackend` 需要接收一个可选的 backend 配置，`internal/agent/agent.go` 在创建 computer backend 时把 `ComputerUseToolConfig` 的两项新配置传入。这样策略属于平台后端，而不是模型提示词或 Telegram adapter。

### 4.3 X11 执行流程

```text
computer_act(type)
  -> 现有权限、来源、frame_id 校验
  -> X11 TextInputStrategy
     -> auto：判断是否包含非 ASCII rune
     -> keystroke：xdotool type --clearmodifiers -- <text>
     -> clipboard：写入 UTF-8 CLIPBOARD selection
                    -> xdotool key --clearmodifiers ctrl+v
                    -> 条件恢复旧剪贴板
  -> 现有等待与截图观察
```

剪贴板实现必须抽象为独立接口，便于单元测试和将来接入 Wayland：

```go
type Clipboard interface {
    ReadText(ctx context.Context) (text string, available bool, err error)
    WriteText(ctx context.Context, text string) error
}
```

先在目标桌面验证 `xclip` 或 `xsel` 的 X11 selection 持有行为。若外部命令不能可靠地在粘贴期间持有 `CLIPBOARD` selection，则采用受控的 X11 原生实现；不能以“后台命令偶尔可用”作为生产实现基础。

恢复逻辑如下：

1. 仅当 `preserve_clipboard=true` 且能读到原有纯文本时保存旧值。
2. 写入目标文本并发送 `Ctrl+V`。
3. 再次读取剪贴板；只有它仍与本次目标文本完全一致时才恢复旧值。
4. 读取、写入或恢复失败只返回状态和错误类别，不返回剪贴板文本。

如果当前环境没有可用剪贴板能力，`auto` 和 `clipboard` 对 Unicode 文本必须失败并提示配置/依赖问题；不可悄悄退化为 `xdotool type`。

## 5. 模型提示与工具说明

提示词不是功能实现，但必须避免模型绕过可靠路径。

在 `internal/agent/system_prompt.go` 的 computer use 规则后补充：

```text
需要输入中文、emoji 或其他 Unicode 文本时，直接使用 computer_act 的 type/text 参数。
不要模拟拼音、切换输入法或操作候选词窗口；后端负责文本注入。
```

同时在 `internal/tool/builtin_computer.go` 的 `computer_act` 参数说明中注明：`text` 接受 Unicode，实际注入方法受本地 computer_use 配置控制。

## 6. 测试计划

### 6.1 单元测试

- 配置默认值、`lh config get/set`、非法 `text_input_mode`、配置副本隔离。
- `auto` 对 `hello` 选择按键策略，对 `你好`、`😀`、换行文本选择剪贴板策略。
- `clipboard` 始终走剪贴板，`keystroke` 拒绝非 ASCII 文本。
- 剪贴板调用顺序为“读旧值 -> 写目标 -> 粘贴 -> 比对 -> 条件恢复”。
- 用户在执行期间改变剪贴板时，不恢复旧值。
- 剪贴板错误、`xdotool` 错误和超时均不泄露文本内容。
- `allow_text_input=false` 仍会在调用 backend 前拒绝文本动作。

### 6.2 集成与人工验收

- X11 下分别在输入法开启和关闭时输入 `你好，LuckyAgent 😀`。
- 验证英文、包含换行的文本、中文标点和 emoji。
- 使用浏览器、终端和一个原生 GUI 文本框各验证一次。
- 从 Telegram 发起一次输入任务，确认与 CLI 行为一致。
- 在操作期间手工复制另一段文本，确认 `preserve_clipboard` 不会覆盖用户的新剪贴板。

建议自动化测试使用 mock `Clipboard` 和命令执行器；真实桌面验收可作为环境依赖的集成测试，不应让普通 `go test ./...` 必须依赖输入法或显示服务器。

## 7. 交付顺序

1. 配置与策略接口、纯单元测试。
2. X11 剪贴板策略及错误处理。
3. 模型工具描述与系统提示更新。
4. 本机 X11 验收，随后重启 Telegram gateway 使新 Agent 配置生效。

完成标准是：无论当前中文输入法是否开启，`computer_act(type)` 都能将 Unicode 文本写入正常的可粘贴文本控件；并且失败场景可定位、不会以拼音或乱码代替用户原文。
