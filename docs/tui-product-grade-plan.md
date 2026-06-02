# LuckyHarness TUI 产品级规划

更新时间：2026-06-02

## 结论

`UI/TUI` 已经不是空壳，它现在是一个能连接 LuckyHarness 后端、展示会话、收发流式消息、查看 reasoning/tool 过程的 Ink（终端 React 渲染框架）应用。要达到“可用的产品级别”，下一步重点不是再堆更多命令，而是把它从“单文件可运行原型”推进到“稳定、可测试、可发布、可恢复、可观测”的终端产品。

最短路径是三步：

1. 先做稳定性和协议收口：明确 API/WebSocket 协议、连接恢复、错误展示、会话操作边界。
2. 再做产品体验：命令体系、快捷键、滚动/搜索、会话管理、帮助提示、输出复制/导出。
3. 最后做工程化：拆模块、测试、打包、发布、回归脚本和可观测指标。

## 当前现状

### 已具备的能力

TUI 入口在 `UI/TUI/src/index.tsx`，通过 `--api-base`、`--session`、`--model` 三个参数启动，并要求交互式 TTY（真实终端输入环境）。它使用 Ink alternate screen（备用屏幕：进入应用时切到独立终端画面，退出后恢复原屏幕），适合作为全屏终端客户端。

主实现集中在 `UI/TUI/src/tui-app.tsx`。现有能力包括：

- API 地址规范化：支持 `9090`、`:9090`、`ws://...`、`http://...` 等输入，并转换为 HTTP base。
- 会话列表：调用 `GET /api/v1/sessions`，支持本地过滤和后端查询参数 `q`。
- 会话恢复：调用 `GET /api/v1/sessions/{id}` 加载历史消息。
- 会话创建/重命名：通过 `POST /api/v1/sessions` 创建新会话或更新标题。
- 运行时状态：调用 `GET /api/v1/stats` 获取 provider/model 等信息。
- WebSocket 聊天：连接 `/api/v1/ws?session=...`，发送 `{ type: "chat", data: { message, stream: true, max_iterations: 8 } }`。
- 流式展示：处理 `status`、`reasoning`、`tool_call`、`tool_result`、`stream_chunk`、`stream_end`、`error` 等消息类型。
- 命令入口：已有 `/review`、`/resume`、`/rename`、`/help`、`/sessions`、`/new`。
- 终端交互：支持 Tab 命令面板、会话 picker、PgUp/PgDn/Home/End 滚动、Esc 清空、Ctrl+C 退出。

对应后端接口已经在 `internal/server/server.go` 注册：

- `GET/POST /api/v1/sessions`
- `GET /api/v1/sessions/{id}`
- `GET /api/v1/stats`
- `GET /api/v1/ws`
- `GET /api/v1/ws/stats`

### 关键问题

当前 TUI 离产品级主要差在“边界确定性”。一句话说：功能已经有形，但错误、恢复、测试、协议和发布还没有形成产品闭环。

具体问题如下：

- 单文件过大：`tui-app.tsx` 同时承担协议、状态、渲染、命令、滚动、Git review、会话操作，后续改动容易互相影响。
- 协议是隐式约定：WebSocket 消息类型、REST 返回结构、错误格式都写在组件里，没有独立 schema（结构约定）或契约测试。
- 错误处理偏静默：`refreshSessions`、`loadSessionHistory`、`loadRuntimeStatus` 等失败时多处直接吞掉错误，用户只看到空状态，难以判断是后端未启动、网络失败还是接口变化。
- 连接恢复不完整：断线时可以排队消息，但缺少重试退避、队列上限、重复发送保护、超时提示。
- 会话一致性不足：创建失败会 fallback 到本地 `lh-${Date.now()}`，这可能制造一个后端不存在的会话 ID。
- UI 信息密度有了，但缺少“产品级任务流”：比如多行输入、历史命令、复制最后回答、导出会话、搜索 transcript、清理当前输出、切换模型等。
- 缺少 TUI 专属测试：目前 `UI/TUI/package.json` 只有 `dev/start/typecheck`，没有组件测试、协议解析测试、键盘交互测试或快照测试。
- 发布形态不完整：TUI 还没有作为 CLI 二进制/包的稳定入口、版本展示、安装说明、故障排查矩阵。

## 产品级目标

产品级不是“界面更漂亮”，而是用户能把它当日常工具使用。判断标准如下：

- 可启动：给一个后端地址就能运行；后端不可用时给清晰提示。
- 可恢复：断线、后端重启、终端 resize、会话切换后状态不乱。
- 可理解：用户知道当前 session、model、连接状态、消息是否已发送、工具是否在运行。
- 可操作：常用动作都可用键盘完成，帮助信息准确，不需要读源码。
- 可验证：协议、命令、渲染、失败场景都有自动化测试。
- 可发布：有 CLI 入口、版本、README、安装方式、最小环境要求。

## 推荐架构

把 TUI 拆成五层，先拆边界，再补功能：

1. `api/`：REST 客户端。负责 sessions、stats、health、tools 等 HTTP 请求。
2. `ws/`：WebSocket 客户端。负责连接、重连、消息收发、队列、事件标准化。
3. `domain/`：TUI 内部模型。比如 `SessionInfo`、`StreamItem`、`RuntimeStatus`、`CommandSpec`。
4. `commands/`：命令系统。负责解析 `/resume foo`、执行命令、生成帮助文本。
5. `components/`：Ink 组件。只负责展示和键盘交互。

例子：现在 `/rename` 既解析输入，又拼请求，又更新 UI。拆分后应变成：

- `commands/rename.ts` 解析标题并调用 service。
- `api/sessions.ts` 发送 `POST /api/v1/sessions`。
- `components/App.tsx` 只负责显示“重命名成功/失败”。

这样做的好处是，协议变化只改 `api/`，交互变化只改 `components/`，测试也能精确覆盖。

## 分阶段路线

### P0：稳定可用

目标：让 TUI 在真实日常使用中不容易卡死、不误导、不静默失败。

改动：

- 增加 `GET /api/v1/health/ready` 启动探测；启动后先显示 backend reachable/unreachable。
- REST 请求统一走 `apiClient`，返回 `{ ok, data, error }`，错误展示到状态区和 transcript。
- WebSocket 增加指数退避重连（失败间隔逐步增加），并显示下一次重连时间。
- 消息队列加上限，例如最多 20 条；超过时提示用户，而不是无限堆积。
- 移除“后端创建会话失败时本地伪造 session”的行为，改为显式失败。
- 对 `stream_end`、`error`、socket close 做状态复位，避免 draft 卡住。
- `/review` 的 Git 调用保留，但限制输出长度，并标出 cwd。

验收：

- 后端未启动时，TUI 不崩溃，顶部明确显示 API unreachable。
- WebSocket 断开后能自动重连，重连前不会丢已排队消息。
- 会话创建失败不会切到不存在的 session。
- `npm run typecheck --workspace TUI` 通过。

### P1：核心产品体验

目标：让它成为可长期使用的终端工作台。

改动：

- 多行输入：支持粘贴长 prompt、编辑多行、提交前预览。
- 历史输入：上/下键在输入区有内容时浏览命令/消息历史。
- Transcript 搜索：`/search keyword`，在当前会话内高亮或跳转匹配结果。
- 导出会话：`/export markdown|json`，把当前 session 输出到本地文件。
- 复制能力：`/copy last` 或快捷键复制最后一条 assistant 回复。
- 命令帮助升级：`/help rename` 显示具体用法和例子。
- 模型与参数展示：展示当前 provider/model/max_iterations，并允许 `/set max_iterations 12`。
- 会话操作补齐：`/delete`、`/pin`、`/open`，但删除必须二次确认。

验收：

- 新用户只看 `/help` 就能完成：创建会话、发送消息、恢复会话、重命名、导出。
- 长回答可搜索、可滚动、可导出。
- 所有命令都有单元测试覆盖解析和错误提示。

### P2：工程化和测试

目标：让 TUI 改动可回归，不靠人工盯屏幕。

改动：

- 新增 TUI 测试依赖，比如 `vitest`（TypeScript 测试框架）和 Ink 测试工具。
- 抽出纯函数测试：`normalizeApiBase`、`parseCommandParts`、`renderMarkdown`、session normalization。
- 增加 WebSocket mock 测试：模拟 chunk/end/error/tool_call 顺序。
- 增加组件快照测试：验证主界面、空会话、错误态、picker 态。
- 增加协议契约测试：后端接口返回结构与 TUI 类型一致。
- 在 `UI/package.json` 增加 `test:tui`，在根构建链路中纳入最小回归。

验收：

- `npm run typecheck --workspace TUI` 通过。
- `npm run test --workspace TUI` 或 `npm run test:tui` 通过。
- 协议新增字段不影响旧 UI，删除必需字段会被测试抓住。

### P3：发布和运维

目标：让 TUI 能被安装、升级、诊断。

改动：

- 增加 CLI 包入口：例如 `luckyharness-tui --api-base ...`。
- 增加 `--version`、`--help`、`--debug-log <path>`。
- 增加环境变量：`LUCKYHARNESS_API_BASE`、`LUCKYHARNESS_SESSION`、`LUCKYHARNESS_MODEL`。
- 增加日志文件：记录连接失败、协议错误、命令执行结果，不把敏感 prompt 默认写入日志。
- README 增加安装、启动、故障排查、快捷键表。
- 打包时固定 Node/React/Ink 兼容矩阵。

验收：

- 用户能通过一条命令启动 TUI。
- `--help` 能说明所有启动参数和环境变量。
- 常见故障有明确诊断路径：后端未启动、端口错、WebSocket 失败、TTY 不支持。

## 协议收口建议

### REST

建议把 TUI 依赖的 REST 响应整理成显式契约：

```ts
type SessionInfo = {
  id: string;
  title: string;
  message_count: number;
  created_at: string;
  updated_at: string;
};

type RuntimeStatus = {
  provider: string;
  model: string;
  uptime: string;
  version: string;
};
```

后端可以继续返回更多字段，但 TUI 只依赖这些必需字段。例子：`/api/v1/stats` 可以返回 `context_cache`，但 TUI 不应该因为这个字段缺失而失败。

### WebSocket

建议定义统一 envelope（消息外壳）：

```ts
type WsEnvelope =
  | { type: 'status'; data: { state?: string; message?: string } }
  | { type: 'reasoning'; data: { summary?: string; text?: string } }
  | { type: 'tool_call'; data: { name?: string; args?: unknown; display?: string } }
  | { type: 'tool_result'; data: { name?: string; output?: unknown; display?: string } }
  | { type: 'stream_chunk'; data: { content: string } }
  | { type: 'stream_end'; data: { full_response?: string } }
  | { type: 'error'; data: { message: string } };
```

反例：不要让 TUI 到处猜 `data.text || data.summary || data.reasoning || data.content`。这短期兼容性好，但长期会掩盖后端协议漂移。

## 推荐目录结构

```text
UI/TUI/src/
  index.tsx
  app.tsx
  api/
    client.ts
    sessions.ts
    status.ts
  ws/
    client.ts
    protocol.ts
  commands/
    registry.ts
    parser.ts
    handlers.ts
  domain/
    session.ts
    stream.ts
    runtime.ts
  components/
    Header.tsx
    SessionList.tsx
    StatusPanel.tsx
    Transcript.tsx
    CommandPicker.tsx
    PromptBox.tsx
  __tests__/
    commands.test.ts
    api-base.test.ts
    protocol.test.ts
```

## 验收清单

产品级可用的最低验收清单：

- 启动：TTY 支持、参数解析、环境变量、后端探测都可验证。
- 连接：WebSocket 连接、断线、重连、排队、失败提示都可验证。
- 会话：列表、搜索、恢复、创建、重命名、导出都可验证。
- 消息：user/assistant/reasoning/tool/error/status 都能稳定渲染。
- 滚动：长回答、窗口 resize、自动跟随底部、手动浏览都不乱。
- 命令：每条命令有帮助、参数校验、错误提示和测试。
- 工程：typecheck、unit tests、协议 mock tests 进入 CI 或 `make ui-typecheck` 体系。
- 发布：README、CLI help、版本号、故障排查齐全。

## 优先级建议

第一周先做 P0 和拆分最小骨架。不要一开始重写整个界面，先把协议、错误、重连和测试底座稳住。

第二周做 P1 的高频操作：多行输入、历史输入、搜索、导出、复制。它们会直接决定用户愿不愿意长期用。

第三周做 P2/P3：测试闭环、打包、文档、诊断日志。这个阶段决定它能不能从“开发者自己用”变成“别人也能装、能排错、能升级”。

## 当前不建议做的事

- 不建议先做复杂主题皮肤。终端产品先要稳定、清楚、可恢复。
- 不建议继续把所有逻辑塞进 `tui-app.tsx`。这会让每个新功能都扩大回归风险。
- 不建议无测试地扩展命令。命令越多，越需要 parser 和 handlers 的单元测试。
- 不建议默认把 prompt 全量写入 debug log。日志要能诊断连接和协议问题，但默认避免记录敏感内容。

## 下一步实施顺序

1. 抽出 `normalizeApiBase`、命令解析、协议类型，给纯函数补测试。
2. 引入 `apiClient`，统一 REST 错误展示。
3. 引入 `wsClient`，实现重连、队列上限、状态事件。
4. 拆出 Header、SessionList、StatusPanel、Transcript、PromptBox。
5. 增加 `/export`、`/search`、多行输入和输入历史。
6. 完成 README、CLI help、debug log、发布说明。

