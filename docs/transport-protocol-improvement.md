# LuckyAgent 传输协议改进

> 状态：结论已沉淀，**不建议全量替换为 gRPC**  
> 日期：2026-10-09  
> 范围：对外传输层（HTTP / SSE / WebSocket / gRPC），不含模型 provider 的 Completion vs Responses 协议  
> 依据：`internal/server/server.go`、`internal/websocket/`、`api/proto/luckyagent.proto`、`api/grpc/server.go`

---

## 1. 一句话结论

**主聊天继续用 SSE；实时双向继续用 WebSocket；gRPC 只做可选并行通道，不要「全换成 gRPC」。**

全量替换的改造成本和兼容破坏，远大于可预期的延迟/带宽收益。真正该补的是：**统一事件契约、补齐 gRPC 真流式、按客户端类型分流**，而不是换传输名词。

---

## 2. 现状（代码事实）

### 2.1 已接线、可对外使用

| 通道 | 路径 | 角色 | 实现位置 |
|------|------|------|----------|
| **SSE（主路径）** | `POST /api/v1/chat` | 流式聊天默认 | `internal/server`，`Content-Type: text/event-stream` |
| **同步 HTTP** | `POST /api/v1/chat/sync` | 整包返回 | 同左 |
| **WebSocket** | `GET /api/v1/ws` | 双向实时、Dashboard、任务事件 | `internal/websocket` + `handleWebSocket` |
| **WS 统计** | `GET /api/v1/ws/stats` | 连接观测 | 同左 |
| 其余控制面 | `/api/v1/memory`、`/rag`、`/tasks`、`/approvals`… | CRUD / 运维 | HTTP JSON |

根路由说明也写明：`POST /api/v1/chat — 流式聊天 (SSE)`，`GET /api/v1/ws — WebSocket 实时通信`。

### 2.2 WebSocket 消息类型（已有事件模型）

客户端 → 服务端大致包括：`chat`、`cancel`、`approval_response`、`stream_ack`、`ping`、`reconnect` 等。  
服务端 → 客户端大致包括：`stream_chunk`、`stream_end`、`reasoning`、`tool_call`、`tool_result`、`compact`、`approval`、`status`、`task_event`、`error`、`pong`。

这说明 **实时语义已经在 WS 侧成型**，不是「只有一条裸连接」。

### 2.3 gRPC：有骨架，未进主服务默认入口

- Proto：`api/proto/luckyagent.proto`（`LuckyAgentService`，含 `Chat` / `ChatStream` 等）
- 生成代码与实现：`api/grpc/*.go`
- **`lh serve` / `internal/server` 路由表未挂 gRPC 为默认对外主入口**（控制面仍是 HTTP）
- `ChatStream` 当前实现是：先 `agent.Chat` **整段拿完**，再按 100 字符切片 `stream.Send`  
  → 这是 **假流式（fake streaming）**，不是 token/工具事件级真流式

### 2.4 和「Completion / Response」的边界

- **Completion / Response**：上游模型 API 形态（OpenAI Chat Completions vs Responses 等）
- **SSE / WS / gRPC**：LuckyAgent **对外**把 agent 结果推给客户端的传输形态  

二者正交。换 gRPC **不会**自动解决 provider 协议迁移。

---

## 3. 全量改 gRPC 会带来什么

### 3.1 可能的收益（真实但有限）

| 收益 | 说明 | 对 LuckyAgent 的实际分量 |
|------|------|--------------------------|
| 强类型契约 | proto 多端生成，少手写 JSON 对字段 | 中：Android / 多语言客户端长期有用 |
| 二进制帧 | protobuf 比 JSON 省带宽与解析 | 低～中：聊天文本为主时收益有限；工具事件 JSON 已够用 |
| HTTP/2 多路复用 | 单连接并行 RPC | 低：浏览器与部分网关场景反而别扭 |
| 双向流模型 | `stream` 在 IDL 里更整齐 | 中：但 **WS 已覆盖双向实时** |
| 截止时间/状态码 | gRPC code + deadline 统一 | 中：HTTP 也能做好，属于工程纪律不是协议魔法 |
| 服务间调用 | agent ↔ worker ↔ 内网工具 | **高（仅内网）**：BFF/sidecar/多实例时更划算 |

### 3.2 明确不划算或变差的地方

1. **浏览器 / Dashboard**  
   浏览器不能当一等 gRPC 客户端（需 gRPC-Web 或网关再包一层）。现有 Dashboard 已走 WS。

2. **Cloudflare Tunnel / 反代**  
   当前公网路径（如 `lh.shiokou.asia`）对 **HTTP/1.1 + SSE** 最省事。gRPC（HTTP/2、trailer、长流）在隧道、CDN、部分反向代理上更容易踩超时、缓冲、不支持。

3. **Telegram / 消息网关**  
   入口是 Bot API 长轮询或 webhook，**不是**浏览器流。内层再套 gRPC 对用户体验几乎无感。

4. **兼容与迁移成本**  
   Android okhttp 客户端、curl、脚本、现有 SSE 消费者、审批轮询都要改。  
   「全换」= 双栈长期或硬切；硬切会炸现网。

5. **假流式没有性能故事**  
   若只是把 JSON SSE 换成「整段生成再 chunk 的 gRPC stream」，用户体感 **不会更快**，只是多一层 IDL。

6. **运维与调试**  
   SSE/HTTP 用 curl 即可；gRPC 需要 grpcurl/反射/证书与更多工具链。本地 agent 场景更痛。

### 3.3 结论表

| 方案 | 推荐度 | 原因 |
|------|--------|------|
| 全量废弃 SSE/WS，只留 gRPC | ❌ 不推荐 | 收益小、破坏大、浏览器与隧道不友好 |
| 维持现状，什么都不做 | ⚠️ 可维持 | 能跑，但事件契约分裂、gRPC 半成品会误导 |
| **SSE 主路径 + WS 实时 + gRPC 可选（增量）** | ✅ 推荐 | 匹配现有客户端；把钱花在统一语义和真流式 |

---

## 4. 真正该改进的点（按优先级）

### P0 — 统一「事件契约」，传输无关

问题：SSE 事件名、WS `MessageType`、将来 gRPC `ChatChunk` 字段可能不一致；客户端要写三套解析。

目标：定义一份 **Agent Event Schema（逻辑事件）**，三种传输只做编码层。

建议最小事件集（逻辑名 → 已有 WS 近似）：

| 逻辑事件 | WS 现状 | SSE 应对齐 | 含义 |
|----------|---------|------------|------|
| `stream.delta` | `stream_chunk` | content 增量 | 可见文本增量 |
| `stream.end` | `stream_end` | `complete` / done | 成功终态（仅验收通过后） |
| `reasoning` | `reasoning` | 同名或 data.type | 思考摘要（可选） |
| `tool.call` | `tool_call` | 同 | 工具调用 |
| `tool.result` | `tool_result` | 同 | 工具结果 |
| `approval.needed` | `approval` | 同 | 人机确认 |
| `task.event` | `task_event` | 同 | 子代理/后台 |
| `status` | `status` | 同 | 阶段状态 |
| `error` | `error` | 同 | 失败 |
| `compact` | `compact` | 可选 | 上下文压缩进度 |

规则：

- **成功终态只有 `stream.end` / SSE `complete`**；过程 `content` 不是完成证据（与长任务文档一致）。
- 字段用稳定 `type` + `session_id` + `run_id` + `payload`；禁止各通道同义不同名长期漂移。
- 文档源头：`docs/API.md` + 本文件；代码源头建议集中到 `internal/protocol` 或现有 event 包，避免 server/websocket 各写一份。

### P1 — 修 gRPC：真流式或降级标注

现状：`ChatStream` = 同步 `Chat` + 字符串切片。

选项：

1. **推荐**：gRPC stream 订阅与 SSE **同一条 agent 事件总线**（token/工具/审批真实推送）。
2. **最低**：在 proto/注释/文档标明 `ChatStream` 为 buffered mock，**禁止**对外宣传为流式 API，直到接真流。

未做 P1 前，不要把 gRPC 当性能优化项。

### P2 — 按客户端分流，而不是「一个协议打天下」

| 客户端 | 推荐通道 | 原因 |
|--------|----------|------|
| curl / 脚本 / 简单集成 | HTTP SSE 或 sync | 零依赖、好调试 |
| Web Dashboard | WebSocket | 双向、审批、任务推送 |
| Android / 原生 | SSE 或 未来 gRPC | 长连接保活与省电要实测；先 SSE 最稳 |
| 内网服务间 | gRPC（可选） | 强类型、二进制、deadline |
| Telegram/QQ 网关 | 进程内调用 / 本地 HTTP | 不经过公网 gRPC |

### P3 — 控制面保持 HTTP JSON

memory / rag / cron / config / health **不必**强迁 gRPC。  
若做 gRPC，只优先：

- `Chat` / `ChatStream`
- 任务订阅（与 `task_event` 对齐）
- health（已有 grpc health 模式可复用）

其余继续 REST，避免双倍 handler。

### P4 — 可观测与背压

无论哪种传输：

- 统一 `run_id`，日志/metrics 可关联
- SSE：心跳注释行或 status 事件，防止代理空闲断连
- WS：已有 ping/pong，保留；断线 `reconnect` 与 run 续订语义写清
- 慢消费者：队列有界 + 丢弃策略或断开（长任务文档已提背压，传输层要落地）

### P5 — 网关与公网

- 公网入口默认 **HTTP/SSE + WS**
- gRPC 若启用：优先 **内网端口** 或独立 listener，不要默认绑在 Cloudflare 隧道同一条「仅 HTTP 假设」路径上
- 文档写清：隧道超时、nginx `proxy_buffering off`、WS Upgrade 头

---

## 5. 建议落地路线（可执行）

### Phase A — 文档与契约（0.5–1 天）

- [x] 本改进文档入库  
- [ ] `docs/API.md` 增加「传输层对照」小节：SSE / WS / gRPC 各自职责  
- [ ] 标明 gRPC `ChatStream` 当前为假流式  
- [ ] 列出逻辑事件表与字段稳定性承诺

### Phase B — 代码层事件归一（2–5 天）

- [ ] 抽出共享 event 类型（或 codegen 自一份 schema）  
- [ ] SSE writer 与 WS hub 都消费同一 event stream  
- [ ] 单测：同一 agent run 在 SSE 与 WS 上事件序列一致（允许封装头不同）

### Phase C — gRPC 可选接入（按需）

- [ ] `lh serve` 增加可选 `--grpc-addr`（默认关闭）  
- [ ] 反射 + health 可选打开，便于 grpcurl  
- [ ] `ChatStream` 接到真实事件总线；禁止整段切片假流  
- [ ] Android 仅在有明确收益（电量/类型安全）时再切

### Phase D — 明确不做什么

- [ ] 不删除 SSE  
- [ ] 不强制 Dashboard 改 gRPC-Web  
- [ ] 不把 Telegram 通路改成公网 gRPC  
- [ ] 不把「换协议」当成 QPS/延迟优化的主手段（先做 provider 与工具路径 profiling）

---

## 6. 成功标准

1. **契约**：新客户端只学一套 event `type`，换 SSE/WS 只换 transport。  
2. **兼容**：现有 `POST /api/v1/chat` 与 `/api/v1/ws` 行为不破坏（可加字段，不改旧字段含义）。  
3. **诚实**：任何标注为 streaming 的 API 必须是真事件流，或文档降级。  
4. **可测**：至少一组集成测试覆盖「工具调用 + 最终 complete」在双通道一致。  
5. **默认拓扑**：公网仍 SSE/WS；gRPC 默认关或仅本机/内网。

---

## 7. 非目标

- 迁移上游模型从 Chat Completions 到 Responses API（另开文档）  
- 替换 msg gateway 协议  
- 为了「技术时髦」重写 Dashboard  
- 保证 exactly-once 跨机器投递（传输层只保证 at-most-once/at-least-once 语义写清即可）

---

## 8. 相关代码与文档

| 路径 | 说明 |
|------|------|
| `internal/server/server.go` | HTTP 路由、SSE chat、WS 入口 |
| `internal/websocket/message.go` | WS 消息类型 |
| `api/proto/luckyagent.proto` | gRPC IDL |
| `api/grpc/server.go` | gRPC 实现（含假流式 ChatStream） |
| `docs/API.md` | HTTP API 说明 |
| `docs/long-task-reliability.md` | 终态/验收与流式事件语义 |
| `docs/transport-protocol-improvement.md` | 本文件 |

---

## 9. 决策记录

| 决策 | 选择 | 理由 |
|------|------|------|
| 是否全量换 gRPC | **否** | 浏览器/隧道/网关/兼容成本 > 收益 |
| 主聊天传输 | **SSE** | 已是默认；简单、可 curl、公网友好 |
| 实时双向 | **WebSocket** | 已有事件模型与 Dashboard |
| gRPC 定位 | **可选、内网/原生优先** | 强类型与服务间；需真流式后才有价值 |
| 第一优先改进 | **统一事件契约 + 真流式** | 比换传输名词更能减客户端复杂度 |

---

## 10. 修订

| 日期 | 变更 |
|------|------|
| 2026-10-09 | 初稿：基于协议讨论与代码核对沉淀 |
