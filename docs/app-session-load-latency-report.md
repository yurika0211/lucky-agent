# App 端会话加载延迟分析报告

- **日期**: 2026-10-01
- **目标**: 首屏 / 切会话加载延迟稳定在 **≤ 1s**
- **范围**: LuckyAgent Android App ↔ HTTP API ↔ `internal/session` 磁盘会话存储
- **结论摘要**: API 已支持 history 分页，但存储层仍是「整文件 Read + 全量 JSON Unmarshal 后再切片」。大会话下 **只改 App `limit` 无法把首屏压进 1s**，必须改服务端加载路径。

---

## 1. 问题陈述

用户反馈 App 端加载很慢，期望加载延迟停留在一秒之内。

结合代码与本地数据，慢主要集中在：

1. **打开 / 切换会话时的历史消息加载**（首屏聊天内容）
2. **服务端冷启动 / 会话管理器初始化**（全量扫描 sessions 目录）
3. **超大会话文件本身的 I/O 与反序列化成本**

UI 白屏体感会被「先清空 bubbles 再等网络」放大，但根因是服务端在分页响应前已经付了全量加载成本。

---

## 2. 证据与基线

### 2.1 本地会话体量（`~/.luckyagent/sessions`）

| 指标 | 数值 |
|---|---|
| 会话 `.md` 数量 | 164 |
| sessions 目录总量 | ~1.3 GB（含备份等）；仅 `.md` 合计约 **135 MB** |
| 最大会话 | `1790186436411739830.md` ≈ **61.06 MB** |
| 次大会话 | `1789380747236957841.md` ≈ **14.84 MB** |
| 再往下 | 8.09 / 7.96 / 5.11 MB … |

主会话 `1790186436411739830`（标题相关：luckyagent：dev）是日常 arXiv 推送写回目标，历史持续膨胀，是 App 体感最差的典型路径。

### 2.2 App 调用路径

| 组件 | 行为 |
|---|---|
| `AppViewModel.selectSession` | 清空 `bubbles` → `wsClient.replaySession` → `loadHistory` |
| `AppViewModel.loadHistory` | `container.api.sessionHistory(sessionId)` |
| `LuckyAgentApi.sessionHistory` | `GET /api/v1/sessions/{id}?limit=100`（默认 limit=**100**） |
| `SessionHistory` 模型 | 已有 `limit/offset/returned/has_more` 字段 |
| Trajectory 回退 | 失败时可能再 `GET /api/v1/sessions/{id}` **无 limit**（次要放大） |

App 侧**已经按分页 API 设计**，默认要最近 100 条，并不是故意拉全量。

### 2.3 服务端 API 路径

文件：`internal/server/server.go` → `handleSessionByID`

```text
allMessages := sess.GetMessages()          // ← 全量
total := len(allMessages)
// 默认 newest-first page
limit  = defaultHistoryLimit (60), max 500
offset = 从末尾向前跳过
page   = allMessages[start:end]
payload: messages + limit + offset + returned + has_more
?all=1 仍可走 legacy 全量
```

**分页只发生在「已经拿到全部 messages 之后」。**

### 2.4 Session 存储路径

文件：`internal/session/session.go`

| 函数 | 注释意图 | 实际行为 |
|---|---|---|
| `NewManager` → `loadFromDisk` | 「仅元数据，消息按需加载」 | 对每个 `.md` 仍 `ReadFile` + 抽 JSON fence + **完整 Unmarshal**，只为 `messageCount = len(sd.Messages)`，然后把 `Messages` 丢弃 |
| `loadMessages` | 懒加载 | 再次 `ReadFile` + 全量 Unmarshal 进内存 |
| `GetMessages` | 可按 turn 窗口截断 | HTTP handler 调用时**未传窗口**，返回拷贝后的全部消息，再由 handler 切片 |

结果：

- **启动时**：164 个会话 ≈ 135MB+ JSON 解析一遍（只为列表元数据）
- **每次打开大会话（冷）**：再读一遍 61MB 并建完整消息数组，即使客户端只要 60/100 条
- **热路径**：进程内若已 `messagesLoaded`，后续切页会快很多；App 体感差通常出在冷加载与大 JSON 序列化

### 2.5 延迟构成（定性）

对 ~60MB 会话，单次冷 `GET ?limit=100` 大致包含：

1. 磁盘读 60MB
2. 从 markdown fence 抽 JSON + `json.Unmarshal` 全量 messages
3. 切片出末尾 100 条
4. `historyMessages` 转换 + `sendJSON` 编码响应
5. 网络传输（局域网通常不是主因；响应若仍含大 tool 输出则仍可能偏大）
6. App 反序列化 + `historyToBubbles`（含 tool/reasoning/附件路径解析）

其中 **1–4 在服务端，且与 `limit` 几乎无关**。这就是「limit 已传仍慢」的直接原因。

> 注：本机探测 `/api/v1/health` 与 sessions 接口返回 401（未带 token），未在本报告中写入带鉴权的实测量毫秒数。上线验证应以鉴权后的 `time_total` + 服务端 span 为准。

---

## 3. 根因分级

### P0 — 存储与读取模型（必须改，否则 ≤1s 不可达）

1. **单文件整包 JSON 会话**：消息、tool 大输出、reasoning 全进一个 `.md` fence。
2. **`loadFromDisk` 名不副实**：为了 `message_count` 解析全量。
3. **`handleSessionByID` 先 `GetMessages()` 再 page**：API 分页是「响应分页」，不是「存储分页」。

### P1 — App 交互与请求形态（改了能明显改善体感，但吃不掉 60MB 解析）

1. `selectSession` 先清空再等：无 skeleton / 无磁盘缓存，白屏时间 = 网络 RTT + 服务端全量成本。
2. `sessionHistory` 默认 `limit=100`，服务端默认 60；可降到 30–40 做首屏，但收益有上限。
3. 未使用 `offset/has_more` 做「上拉更早历史」。
4. Trajectory 错误回退无 limit 的 full GET。

### P2 — 产品 / 数据治理

1. 长生命周期会话（dev + 每日 arXiv 写回）无限追加 → 单会话 60MB+。
2. sessions 目录 1.3GB，备份与历史文件放大启动扫描成本。
3. 列表 `Search` 若触发未加载会话的消息扫描，可能被动 `loadMessages`（需避免在列表路径碰正文）。

---

## 4. 目标定义（建议把「1 秒」写清楚）

| 场景 | 成功标准（建议） | 说明 |
|---|---|---|
| **S1 首屏最近消息** | p95 ≤ 1.0s（同局域网 / 本机 loopback） | 打开会话后可见最近 N 条 user/assistant |
| **S2 会话列表** | p95 ≤ 300ms | 只依赖 sidecar 元数据，不读正文 |
| **S3 上拉更早历史** | p95 ≤ 800ms / 页 | 按页从存储读，不重载全文件 |
| **S4 服务冷启动** | 可接受数秒，但不阻塞首个 list/history | `loadFromDisk` 不解析全量 JSON |

「1 秒」应对齐 **S1**，而不是「一次拉完 8000+ 条历史」。

建议首屏 N：**30–60 条消息**（或按「最近 20 轮 user/assistant」），tool 大输出默认截断 / 懒加载。

---

## 5. 方案分层

### 5.1 立刻可做（1–2 天，体感优化 + 降低最坏响应）

**App**

1. `sessionHistory` 首屏 `limit` 改为 **40**（或 30）；保留 `loadMore(offset)`。
2. `selectSession`：保留上一会话 UI 直到新 history 返回，或显示 placeholder，避免硬白屏。
3. 本地 Room/DataStore 缓存「每会话最近一页」：二次打开可先上屏再静默刷新。
4. Trajectory 回退禁止无 `limit` 的 full session GET。
5. bubbles 转换放到后台，主线程只提交结果；大 tool 输出折叠。

**Server（小改，不改存储格式）**

1. 列表与 history 路径确保 **Search/ListInfo 绝不调用 `GetMessages`**。
2. `loadFromDisk` 改为只读 frontmatter / 旁路 `meta.json`（见 5.2）；若短期不能改格式，至少用流式/正则只抽 `id/title/created_at/updated_at/message_count`，避免全量 struct 化 messages。
3. 为 `handleSessionByID` 加 server-timing 或 debug log：`read_ms / unmarshal_ms / page_ms / encode_ms`。
4. history 响应对单条 `content` / tool result 做 **max_chars** 截断（完整内容走 subresource）。

**预期**：小会话可进 1s；**主会话 60MB 冷加载仍大概率 >1s**。

### 5.2 短期结构性改造（真正解锁 ≤1s，推荐主路径）

#### A. Session sidecar 元数据（启动与列表）

每个会话旁路：

```text
{id}.meta.json
{
  "id": "...",
  "title": "...",
  "message_count": 8320,
  "created_at": "...",
  "updated_at": "...",
  "byte_size": 64000000,
  "format": "legacy_md" | "segment_v1"
}
```

- `loadFromDisk` **只读 meta**（或 md 头部 YAML），O(会话数) 小文件。
- `message_count` / 列表排序不再解析 60MB。

#### B. 真正的尾部读取 API（即使暂留 legacy 单文件）

在 `Session` 增加：

```text
GetMessagesPage(limit, offsetFromEnd) ([]Message, total, hasMore, error)
```

实现策略（可渐进）：

1. **Legacy 兼容层（过渡）**
   - 进程内 LRU：按 session id 缓存已解析 messages（控制 RSS 上限）。
   - 第一次仍全量解析，但后续切会话/翻页命中缓存 → 热路径 ≪ 1s。
   - 用 `sync.Once` / singleflight 避免并发重复解析同一 60MB 文件。

2. **Segment 存储（目标态）**
   ```text
   sessions/{id}/
     meta.json
     messages/
       000001.jsonl  # append-only，每行一条 message
       000002.jsonl
     index.json      # 每段起止序号、条数、时间
   ```
   - 首屏 = 读 index + 读最后一段尾部 N 条。
   - 成本与会话总时长解耦。
   - 写入路径改为 append；compact 时再合并。

#### C. HTTP 契约保持兼容

```text
GET /api/v1/sessions/{id}?limit=40&offset=0
→ messages, message_count, returned, has_more, limit, offset
```

内部从「全量再切」换成「页读」；App 几乎不用改协议。

**预期**：S1 在本机/局域网 p95 ≤ 1s 可达（N≤60，且大字段截断时）。

### 5.3 中期：渲染与载荷治理

1. Tool result 默认摘要（前 2–4KB + hash/path），全量 `GET /sessions/{id}/messages/{mid}` 或 tools subresource。
2. Reasoning 默认不进首屏，或单独字段 `include=reasoning,tools`。
3. 图片/附件只传 URL，不内嵌 base64。
4. 超长会话自动 **roll**：按月/按 2k messages 切新 session，旧会话只读。
5. 每日 arXiv 写回若导致单会话膨胀，考虑写入独立 session 或日报 note，避免污染主聊天 timeline。

### 5.4 可选：传输层

1. HTTP 响应 `Content-Encoding: gzip`（大 JSON 收益明显）。
2. 条件请求 `ETag/If-None-Match` 基于 `updated_at + limit + offset`。
3. WebSocket 推送「history page」减少 HTTP 头尾，优先级低于存储改造。

---

## 6. 推荐落地顺序（执行清单）

| 优先级 | 项 | 负责面 | 验收 |
|---|---|---|---|
| **P0.1** | `loadFromDisk` 停止全量 Unmarshal；引入 `.meta.json` 或头部元数据 | server/session | 冷启动 list 不读 60MB；list p95 ≤ 300ms |
| **P0.2** | session 解析 LRU + singleflight | server/session | 同一大会话第二次 history ≪ 第一次 |
| **P0.3** | `GetMessagesPage`；`handleSessionByID` 不再隐式全量 | server | 追踪显示 unmarshal 只在冷缓存未命中 |
| **P1.1** | App 首屏 limit=40 + loadMore(offset) | android | 首屏请求 bytes 下降；可上拉 |
| **P1.2** | 切会话保留旧 UI / 本地缓存一页 | android | 二次打开 < 200ms 上屏 |
| **P1.3** | history 大字段截断 + include 参数 | server+android | 单页响应稳定在数百 KB 内 |
| **P2.1** | segment/jsonl 存储迁移 | server | 冷首屏与会话体积解耦 |
| **P2.2** | 超大会话拆分 / arXiv 写回策略 | product | 主会话体积回落 |

---

## 7. 验证方法

### 7.1 服务端

```bash
# 鉴权后
curl -sS -o /tmp/h.json -w 't=%{time_total} size=%{size_download}\n' \
  -H "Authorization: Bearer $TOKEN" \
  'http://127.0.0.1:9090/api/v1/sessions/1790186436411739830?limit=40'

curl -sS -o /tmp/h2.json -w 't=%{time_total} size=%{size_download}\n' \
  -H "Authorization: Bearer $TOKEN" \
  'http://127.0.0.1:9090/api/v1/sessions/1790186436411739830?limit=40&offset=40'
```

对比：

- 改造前：`limit=40` 与 `limit=500` 的 **server 处理时间接近**（全量解析主导）
- 改造后：冷首屏只付「一页」成本；`offset` 增大不重读全文件（segment 态）

### 7.2 App

- 埋点：`selectSession` → first bubble commit 的耗时
- 区分：`cache_hit` / `network` / `parse` / `compose_ui`
- 目标：S1 p95 ≤ 1000ms；cache_hit ≤ 200ms

### 7.3 回归

- 已有 `session_history_paging_test.go`：newest-first、has_more、clamp、`all=1` legacy
- 增补：大 fixture（或 mock reader）断言 **page API 不要求 full slice 常驻**（至少在 segment 实现后）

---

## 8. 风险与兼容

| 风险 | 缓解 |
|---|---|
| 改存储格式破坏旧会话 | dual-read：先 meta/segment，fallback legacy `.md` |
| LRU 抬高 RSS | 按 MB 上限驱逐；大 session 只缓存 tail page |
| 截断 tool 输出影响排障 | `?include=tools,full` 或 subresource 显式拉取 |
| App 旧版依赖全量 | 保留 `?all=1`；文档标注仅调试 |
| meta 与正文 count 不一致 | 每次 Save 原子写 meta；启动可异步 verify |

---

## 9. 最终判断

1. **现在的分页是 API 层分页，不是存储层分页。**
2. 主会话 ~**61MB** 单文件是硬顶；在「每次冷读全量解析」模型下，**App 端单独优化无法稳定 ≤1s**。
3. 最小充分集合：
   **meta sidecar + 解析 LRU/singleflight + 首屏小 limit/loadMore + 大字段截断**
   即可让热路径和中等会话进入 1s；再靠 **segment/jsonl** 把冷路径与体积解耦，达到长期目标。
4. 不建议继续把每日自动写回无限追加进同一主聊天会话，否则任何缓存策略都会被数据增长打穿。

---

## 10. 关键代码索引

| 路径 | 说明 |
|---|---|
| `internal/server/server.go` | `handleSessionByID`；`defaultHistoryLimit=60`；`maxHistoryLimit=500` |
| `internal/server/session_history_paging_test.go` | 分页行为测试 |
| `internal/session/session.go` | `loadFromDisk` / `loadMessages` / `GetMessages` |
| `luckyagent-android/.../LuckyAgentApi.kt` | `sessionHistory(limit=100)` |
| `luckyagent-android/.../AppViewModel.kt` | `selectSession` / `loadHistory` / `historyToBubbles` |
| `~/.luckyagent/sessions/1790186436411739830.md` | 当前最大问题会话 ≈ 61MB |

---

## 11. 一句话方案

> **把「读整本再撕一页」改成「直接读最后一页」：会话元数据与消息正文分离，首屏只取最近 N 条并截断大工具输出，App 做一页缓存与上拉翻页；大会话再迁 append-only segment。**
