# TUI 消息显示与 Codex 风格重构方案

更新时间：2026-06-02

## 结论

本次优化直接把 `UI/TUI/src/tui-app.tsx` 从 dashboard 式 TUI 改成 Codex 风格的聊天优先界面。核心不是增加更多面板，而是让 transcript 成为主界面：用户输入、assistant 回复、工具事件、状态事件都按对话流组织。

已经落地的方向：

1. 去掉左侧 session 面板、右侧 status 面板和大块横向分隔线。
2. 顶部只保留一行上下文：产品名、模型、连接状态、当前会话和运行状态。
3. 中间区域完全交给消息 transcript，支持 PgUp/PgDn/Home/End 滚动。
4. 底部改成 Codex 式 `›` 输入提示，placeholder 为 `message or /command`。
5. `/resume` 和 Tab 命令面板保留为临时 overlay，不占据常驻布局空间。

## 消息显示逻辑

### 主对话

用户消息使用 `› message` 样式，贴近 Codex 的终端输入感。多行用户消息从第二行开始缩进，避免把每行都渲染成新的输入。

assistant 消息只显示一个轻量 `assistant` 标签，正文完整显示并保留流式更新。这样主回答不会被时间、行数、边框和装饰抢视觉权重。

### 辅助事件

状态、thinking、tool_call、tool_result、error、meta 都改成紧凑事件流：

- `• status: connected`
- `• thinking: analyzing repository state`
- `• tool ls: UI/TUI/src`
- `• result rg: ...`
- `• error: socket error`

辅助事件默认只展示摘要和少量正文。长工具输出会折叠并显示隐藏行数，避免工具日志淹没 assistant 回复。

### Markdown 与代码块

普通 Markdown 按行轻量清理，保留列表、引用和 inline code 的可读文本。代码块走独立折行逻辑，保留 ``` fence 和缩进，避免 JSON、命令、代码片段被普通文本规则破坏。

## 布局方案

### 顶部

顶部两行只承担状态定位：

- 左侧：`LuckyHarness · model · socketState`
- 右侧：`session · status`
- 第二行：`apiBase` 和 live/scroll 指示

这部分不再承担 dashboard 信息展示，避免主界面被状态数据切碎。

### 中部

中部 transcript 根据终端高度自动计算可用行数。没有消息时显示一个极简空状态：

- `assistant`
- `Ready. Ask a question or run a slash command.`
- `/resume to switch sessions, /review for repo context, Tab after / for commands.`

有消息时只渲染当前滚动窗口内的消息行，并用空行补齐高度，保证输入栏位置稳定。

### 底部

底部输入区固定为：

- `›` prompt
- `message or /command` placeholder
- `Enter send · Esc clear · Ctrl+C quit`
- `PgUp/PgDn scroll · Tab commands`

这让主要交互路径变成“看消息、输入、继续”，而不是在多个面板之间找状态。

## 已实施改动

### StreamItem 兼容

`StreamItem` 保留 `createdAt` 字段，历史消息兼容读取 `created_at`、`createdAt` 和 `timestamp`。当前 Codex 风格渲染不展示时间，但保留该字段方便后续实现 hover/detail/expand 类能力。

### 渲染函数

新增并收敛到 `renderItemLines`：

- user：渲染为 `›` 输入块。
- assistant：渲染为 `assistant` 标签加完整正文。
- event：渲染为 `• kind: summary` 的紧凑事件。

旧的 dashboard 分隔线、角色块头、闪烁状态和 review 输出面板逻辑已经移除。

### 事件折叠

`auxLineLimit` 控制辅助事件正文显示行数：

- status：0 行，只显示事件头。
- reasoning：最多 2 行。
- tool_call/tool_result：最多 4 行。
- meta/error：最多 3 行。

超过限制显示 `... N more lines hidden in this view`。

## 后续可选增强

1. `/expand`：展开最近一条被折叠的工具结果。
2. `/copy last`：复制最后一条 assistant 回复。
3. `/theme compact|roomy`：在更像 Codex 的紧凑模式和更宽松的阅读模式之间切换。

## 验收标准

- TUI 首屏不再出现 session 侧栏、status dashboard 和横向大分隔线。
- 用户消息显示为 `› message`。
- assistant 流式输出保持在同一个 `assistant` 消息块内更新。
- status/tool/reasoning/error 显示为 `•` 紧凑事件。
- 长工具输出不会挤掉主对话。
- PgUp/PgDn/Home/End 仍能滚动 transcript。
- `/resume` 和 Tab 命令面板仍可用。
- `npm run typecheck --workspace TUI` 通过。
