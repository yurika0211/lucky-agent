# LuckyAgent 记忆系统研究

本文基于当前代码研究 LuckyAgent 的记忆系统。结论以 `internal/memory`、`internal/agent`、`internal/tool`、`internal/server`、`internal/cli/lhcmd` 和相关测试为准。

## 结论

LuckyAgent 的记忆系统不是单一的聊天历史缓存。它由四层组成：

- session history：`internal/session` 保存原始对话连续性。
- short-term buffer：`memory.ShortTermBuffer` 保存最近若干轮对话和溢出摘要，只在进程内作为短期上下文。
- mid-term session summary：`memory.MidTermStore` 把压缩会话摘要写成 Markdown，用于跨会话召回历史主题。
- durable memory vault：`memory.Store` 是长期可召回事实、偏好、规则和项目约束的事实源，存放在 `${HOME}/.luckyagent/memory` 下的 Obsidian-compatible Markdown vault。

RAG 与 memory 是两套系统。RAG 保存和检索 indexed documents；durable memory 的事实源是 Markdown vault，不是 RAG SQLite。

## 主要代码入口

- `internal/memory/memory.go`：durable memory store、Markdown note 格式、搜索、图索引、时态解析、层级统计。
- `internal/memory/activation.go`：召回激活评分、图传播、reranker 插件点。
- `internal/memory/short_term.go`：短期滑动窗口和溢出摘要。
- `internal/memory/mid_term.go`：中期会话摘要 Markdown 存储和检索。
- `internal/memory/hygiene.go`：脏记忆检测、隔离、删除和恢复。
- `internal/memory/graph_migration.go`：把旧记忆迁移为 Obsidian-first graph memory。
- `internal/memory/tidal_reranker.go` / `tidal_store.go`：可选 tidal reranker 和 SQLite telemetry。
- `internal/agent/context_planner.go`：把 memory 注入模型上下文。
- `internal/agent/memory_gate.go`：从记忆推导必须执行的工具检查。
- `internal/tool/memory_service.go`、`builtin_memory.go`：`remember`、`recall`、`memory_hygiene` 工具。
- `internal/server/server.go`：`/api/v1/memory*` HTTP API。

## 运行时初始化

`initMemoryRuntime` 在 agent 初始化时创建：

- `memory.NewStore(cfg.HomeDir() + "/memory")`
- `memory.NewShortTermBuffer(memory.short_term_max_turns)`
- `memory.NewMidTermStore(${home}/memory/30_Sessions, memory.midterm_max_summaries)`
- `session.NewManager(${home}/sessions)`

如果 `memory.tidal.enabled=true`，还会打开 `${home}/runtime/tidal_memory.db` 或配置的 `memory.tidal.store_path`，并把 `TidalMemoryReranker` 安装到 `memory.Store`。

## Durable Memory Vault

`memory.Store` 以 Markdown 文件为事实源。初始化会确保以下目录存在：

- `00_Index`
- `10_Profile`
- `20_Projects`
- `30_Sessions`
- `40_Decisions`
- `50_Facts`
- `60_Rules`
- `70_Concepts`
- `70_Trajectories`
- `90_Archive`
- `.lh-index`

记忆条目写成带 YAML frontmatter 的 Markdown。frontmatter 关键字段包括：

- `id`
- `type: memory`
- `tier: short|medium|long`
- `category`
- `importance`
- `access_count`
- `created_at`
- `accessed_at`
- `tags`
- `summary_of`
- `expires_at`
- `status`
- `valid_from`
- `valid_until`
- `links`
- `aliases`
- `state_key`
- `state_value`
- `confidence`
- `supersedes`
- `route_policies`
- `block_id`

正文使用 `## Memory` 保存内容，尾部写入 Obsidian block id，例如 `^mem-...`。`links` 和正文中的 `[[wikilink]]` 会共同参与图索引。

目录映射由 category 决定：

- `identity`、`preference`、`profile`、`user` -> `10_Profile`
- `project`、`context`、`code`、`repo` -> `20_Projects`
- `decision`、`architecture` -> `40_Decisions`
- `rule`、`tool`、`workflow` -> `60_Rules`
- `concept` -> `70_Concepts`
- `conversation`、`task`、`session` -> `30_Sessions`
- `archive` -> `90_Archive`
- 其他默认 -> `50_Facts`

## Entry 模型和层级

`memory.Entry` 的核心字段是 `Content`、`Category`、`Tier`、`Importance`、`AccessCount`、`CreatedAt`、`AccessedAt`、`Tags` 和时态/图字段。

层级定义：

- `TierShort`：短期 durable note。默认半衰期约 1 小时。
- `TierMedium`：中期 durable note。默认半衰期约 1 周。
- `TierLong`：长期核心记忆。默认半衰期约 1 年，`Decay` 不会删除 long-tier。

基础权重：

```text
weight = importance * recency_factor * access_boost
```

`access_boost` 随 `AccessCount` 增长，上限约 1.75 倍。tier 还会在激活阶段额外乘以倍率：short `0.8`、medium `1.0`、long `1.2`。

## 写入路径

### 自动保存

`Agent.saveConversationMemoryFromTurn` 每轮结束后会：

- 把 user/assistant 加入 `ShortTermBuffer`。
- 只在输入被推断为 `preference`、`project` 或 `identity` 时，把用户输入作为 short-tier durable memory 写入 `memory.Store`。
- 不把普通原始对话写入 `memory.Store`。

这是当前设计的关键边界：原始对话属于 session history；durable memory 只应保存可复用事实、偏好、项目规则和约束。

### 显式工具保存

`remember` 工具通过 `MemoryToolService.HandleRemember` 写入记忆，支持：

- `content`
- `category`
- `tier`
- `importance`
- `tags`
- `links`
- `aliases`
- `status`
- `state_key`
- `state_value`
- `confidence`
- `supersedes`
- `valid_from`
- `valid_until`
- `route_policies`
- `long_term`
- `mode: append|upsert_state|supersede`
- `format: text|json`

写入前会拒绝：

- 空内容
- 原始对话格式
- 疑似 secret、password、token、API key
- prompt-injection-like 文本
- 超过 `MaxDurableMemoryContentRunes` 的内容

`mode=upsert_state` 会按 `state_key` 找到旧 active state，写入新状态后把旧条目标记为 `superseded`。

### HTTP 保存

`POST /api/v1/memory` 只支持较简单的保存参数：

- `content`
- `category`
- `long_term`

它走 `Agent.Remember` 或 `Agent.RememberLongTerm`，没有工具层的完整 metadata 参数和脏内容拒绝逻辑。

## 召回与激活

普通召回入口：

- `Agent.Recall(query)` -> `memory.Store.Search(query)`
- `recall` 工具 -> `SearchWithOptions`
- context planner -> `memory.Store.Route(query)`

激活评分由 `activation.go` 计算。直接匹配组件包括：

- content lexical match
- category match
- tags match
- aliases match
- wikilinks match

查询会被拆成英文/数字 token 和中文 n-gram，并经过内置 alias 规则扩展。内置规则覆盖一些家庭、花粉过敏、户外计划、天气、空气质量、LuckyAgent Memory、Obsidian、RAG、gateway 等概念。

图传播在直接命中的 seed 上进行一跳扩散：

- `wikilink_target`
- `backlink`
- `alias_backlink`
- `shared_tag`

默认 `DefaultActivationOptions`：

- `IncludeGraph=true`
- `MaxGraphDepth=1`
- `MaxGraphBoost=0.45`
- `MaxGraphSeeds=12`
- `UpdateAccessStats=true`

`RouteActivationOptions` 用于 deterministic routing，不更新 access stats，limit 为 12，graph seeds 为 6。

## 时态和状态解析

普通召回只返回 active 条目。以下条目默认不会进入普通召回：

- `status` 不是 `active`
- `valid_from` 在未来
- `valid_until` 已过
- `expires_at` 已过

`ResolveTemporal` 会对召回结果做当前状态解析：

- 同一 `state_key` 只保留最新条目。
- 新旧比较优先级是 `valid_from/created_at`，再看 `confidence`，再看 `importance`，最后看 `created_at`。
- `supersedes` 指向的旧条目会被忽略并产生 temporal note。
- 相关的 conflict、superseded、expired、future-dated 记忆会作为 warning refs 暴露给路由层。

## 上下文注入

`contextPlanner.BuildInput` 的 memory 注入顺序：

1. `[Core Memory]`：long-tier 记忆，最多选少量核心条目。
2. `[Working Memory - Retrieved Evidence]`：通过 `memory.Route(query)` 找到的相关 active memories。
3. `[Session History - Mid-term]`：中期会话摘要。
4. `[Recent Context]`：short-term buffer 的溢出摘要。

Working Memory 消息明确要求：

- 记忆是 prior evidence，不是当前任务本身。
- 当记忆和最新用户消息或明确 session history 冲突时，优先当前用户消息和显式历史。
- 如果记忆要求实时数据或外部检查，必须使用可用工具，或说明无法检查。

context planner 还会过滤：

- short-tier 中 `User:` / `Assistant:` 开头的原始对话记忆。
- 不属于当前 gateway/user/group scope 的 scoped memory。

被注入上下文的记忆会通过 `RecordActivationFeedback(..., "context_selected", 0.15, ...)` 给可选 reranker 提供弱反馈。

## Memory Router 和 Memory Gate

`memory.Store.Route(query)` 只负责召回、时态消解、scope 过滤和类型化策略求值，不再包含户外、家庭、花粉、城市或具体工具的业务词表。没有 `route_policies` 的普通记忆只提供证据，不会生成工具动作或风险标记。

策略保存在 memory note 的 YAML frontmatter 中：

```yaml
route_policies:
  - id: family-outdoor-live-check
    match:
      query_all:
        - any: [出门, 户外, outdoor, park]
        - any: [女儿, 孩子, daughter, child]
      states:
        - key: family.daughter.pollen_allergy
          values: [active]
    risks:
      - name: child_health_outdoor_plan
        priority: 100
      - name: pollen_allergy
        priority: 80
    required_tools:
      - name: current_time
      - name: web_search
        calls:
          - arguments:
              query: "{{query}} weather pollen AQI"
              count: 5
              mode: quick
    constraints:
      - Check live conditions before the final answer.
```

`match.query_all` 是 AND 关系，每个 `any` 组内是 OR；还支持 `query_any`、`query_none` 和按精确 `state_key` 匹配的 `states.values/not_values`。工具参数是结构化对象，字符串值可使用 `{{query}}`、`{{policy.id}}`、`{{memory.id}}`、`{{memory.content}}` 和 `{{state.<state_key>}}`。

route 会输出 `ToolRequirements`、`Risks` 和 `AppliedPolicies`，同时派生兼容字段 `RequiredTools`、`RiskFlags` 和 `SuggestedSearches`。`AppliedPolicies` 保留策略 ID、memory entry ID 和证据引用，便于审计。

`memory_gate.go` 会在 loop 中阻止直接最终回答并执行策略声明的 calls。只有工具注册时明确设置 `PolicySafe=true` 才允许由 durable-memory policy 自动执行；写文件、shell、委派和控制类工具默认不可执行。工具失败或不可用时，gate 会强制再做一次披露性综合，不会静默放过。

`RouteWithOptions` 接受 entry filter。context planner 和 memory gate 都使用当前 `TurnScope` 过滤 scoped memory，避免其他用户或群组的策略参与路由。

## Short-term Buffer

`ShortTermBuffer` 是进程内滑动窗口：

- 默认 `maxTurns=10`
- 保存最近 user/assistant 消息
- 超出窗口时把溢出的消息压缩为结构化摘要
- 摘要字段包括 user said、assistant responded、key entities、decisions
- `GetContext` 返回摘要 system message 和最近消息

注意：它不直接等同于 durable short-tier memory。自动保存 durable memory 有额外的类别过滤。

## Mid-term Session Summary

`MidTermStore` 位于 `${home}/memory/30_Sessions`，保存 `type: session_summary` Markdown。结构包括：

- `session_id`
- `user_id`
- `created_at`
- `topics`
- `key_decisions`
- `open_questions`
- `code_context`
- `raw_summary`

搜索时按 `raw_summary`、`topics`、`key_decisions`、`code_context`、`open_questions` 匹配，并按约 90 天半衰期做时间衰减。

写入来源包括：

- `autoSummarize`
- context compression 后的 `persistCompressedSummary`

测试明确要求：压缩摘要不会写入 durable `memory.Store`，而是进入 mid-term summary。

## Hygiene

`memory_hygiene` 工具和 context hygiene hook 用于清理脏记忆。检测原因包括：

- `empty`
- `raw_conversation`
- `secret_like`
- `prompt_injection`
- `expired`
- `low_confidence_long_term`
- `duplicate`
- `state_conflict`
- `oversized`

动作：

- `audit`：只扫描。
- `quarantine` / `archive`：把条目标记为 `archived`，加 `hygiene`、`dirty` 标签，降低 confidence，使其不参与普通召回。
- `delete` / `purge`：物理删除记忆文件。工具层要求 `confirm_delete=true`。
- `restore`：恢复被 hygiene 归档的条目。

配置项：

- `context.memory_hygiene_before_context`
- `context.memory_hygiene_action`
- `context.memory_hygiene_min_severity`
- `context.memory_hygiene_max_findings`

默认不在上下文构建前执行 hygiene；默认 action 是 `quarantine`，severity 是 `high`，max findings 是 `25`。

## Tidal Reranker

Tidal memory 是可选的 post-recall reranker。默认关闭。

配置项：

- `memory.tidal.enabled`
- `memory.tidal.beta`
- `memory.tidal.max_boost`
- `memory.tidal.learning_rate`
- `memory.tidal.min_samples`
- `memory.tidal.store_path`

默认值：

- `enabled=false`
- `beta=0.15`
- `max_boost=0.35`
- `learning_rate=0.20`
- `min_samples=1`

工作方式：

- 基础激活分数仍由 `memory.Store.Activate` 决定。
- reranker 根据记忆 age buckets 和弱反馈学习 response kernel。
- 没有反馈时不改变排序。
- 有反馈时最多以保守 boost 调整分数。
- telemetry 和 kernels 可以持久化到 SQLite。

CLI 可通过 `lh memory tidal-stats` 或 `lh memory tidal stats` 查看统计。

## 工具、CLI 和 API

模型可见工具：

- `remember`：写 durable memory。
- `recall`：搜索或查看 recent memory，支持 category/tier/as_of/graph_depth/json/explain_graph。
- `memory_hygiene`：审计、隔离、删除、恢复脏记忆。

REPL 命令：

- `/remember <content>`
- `/remember-long <content>`
- `/recall <query>`
- `/memory-stats`
- `/memory-decay`
- `/promote <memory-id>`

CLI：

- `lh memory migrate-graph [--apply] [--archive-dirty] [--limit N]`
- `lh memory tidal-stats`
- `lh memory tidal stats`

HTTP API：

- `GET /api/v1/memory`
- `POST /api/v1/memory`
- `GET /api/v1/memory/recall?q=...`
- `GET /api/v1/memory/stats`

Gateway：

- Telegram 暴露 `/remember`、`/remember_long`、`/recall`、memory stats、decay、promote 等命令。
- gateway scope 会给自动保存的用户偏好/项目/身份记忆打标签，context planner 按当前 sender/group 过滤 scoped memory。

## 配置

核心 memory 配置：

```json
{
  "memory": {
    "short_term_max_turns": 10,
    "midterm_expire_days": 365,
    "midterm_max_summaries": 100,
    "tidal": {
      "enabled": false,
      "beta": 0.15,
      "max_boost": 0.35,
      "learning_rate": 0.2,
      "min_samples": 1,
      "store_path": ""
    }
  },
  "context": {
    "memory_hygiene_before_context": false,
    "memory_hygiene_action": "quarantine",
    "memory_hygiene_min_severity": "high",
    "memory_hygiene_max_findings": 25
  }
}
```

初始化 `${home}/memory/prompts` 时还会创建 `SOUL.md`、`AGENTS.md`、`mission.md`、`HEARTBEAT.md` 和 prompt policy 文件。这些属于 agent prompt/runtime configuration，不是普通 durable memory entries。

## 设计边界

- 不要把 session history、short-term buffer、mid-term summary、durable memory 和 RAG 混为一谈。
- durable memory 是 prior evidence；最新用户消息和当前 session 的明确事实优先。
- 普通对话不应直接进入 durable memory，否则会污染召回。
- secret、prompt injection 和超长原文不应写入 durable memory。
- `long` tier 应用于稳定事实和长期约束；低置信长期记忆会被工具层拒绝或被 hygiene 标记。
- `state_key` 应用于会变化的事实；更新状态时优先用 `mode=upsert_state`。
- HTTP memory API 当前比工具层弱，缺少完整 metadata 和 hygiene preflight。

## 当前可改进点

- HTTP `POST /api/v1/memory` 可考虑复用 `MemoryToolService.HandleRemember`，以获得同样的脏内容拒绝和 metadata 支持。
- durable memory 的自动保存类别依赖关键词推断，精度有限；重要事实最好通过显式 `remember` 写入。
- `route_policies` 目前通过 Markdown frontmatter 或 `remember.route_policies` JSON 写入；HTTP memory API 尚未暴露完整策略字段。
- context hygiene 默认关闭，历史脏数据需要手动 `memory_hygiene` 或开启 `context.memory_hygiene_before_context`。
- 源码中部分中文注释显示为乱码，应以英文标识、测试和实际逻辑为准，后续可单独修复注释编码。
