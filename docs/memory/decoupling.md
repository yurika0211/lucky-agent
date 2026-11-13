# 记忆系统解耦：Aestus SDK 接入

本分支已将持久记忆、图召回、短期缓冲、中期摘要存储和 Tidal 实现切换到 Aestus。原研究基线为 LuckyAgent `b2df9cc`；初始研究和 SDK 方案分别见历史提交 `6627d01`、`8555205`。本文记录实现后的边界。

Aestus 配套分支为 [feat/luckyagent-sdk](https://github.com/yurika0211/aestus/tree/feat/luckyagent-sdk)，SDK 固定到 `9361afb4f42d`（pseudo-version `v0.0.0-20261017093339-9361afb4f42d`）。不需要本地 checkout、replace 或 go.work。两边最低 Go 版本均为 1.25。

## 依赖与职责

```text
Agent / 工具 / HTTP / gRPC / CLI
                  │
internal/memory：Reader / Writer、宿主准入、证据 DTO
                  │
internal/memoryaestus：类型转换、SDK 调用、资源生命周期
                  │  Go module，固定版本
github.com/yurika0211/aestus/memory
                  │
Markdown vault / 派生图索引 / 可选 Tidal
```

| 所有者 | 实现内容 |
| --- | --- |
| LuckyAgent memory | Reader、Writer、ContextReader、中立 DTO、共享 Write 准入、证据呈现 |
| LuckyAgent memoryaestus | SDK 构造与调用、双向类型投影、请求取消传递、Tidal hooks/关闭 |
| Aestus | 持久化、去重、图召回、时态解析、状态更新、维护、缓冲和摘要存储算法 |
| LuckyAgent Agent | 捕获意图、可信 TurnScope、session 生命周期、LLM 摘要触发、上下文预算、provider 消息、工具门禁 |
| session / RAG | 各自保持会话历史与资料索引的事实源 |

生产代码的 Aestus import 只允许出现在 `internal/memoryaestus`，有源码边界测试约束。memory 包不依赖 provider、SDK、数据库或 tools。核心上下文使用 ContextReader；工具使用包含所需读写/卫生/追踪方法的小接口。

运行时使用 RuntimeStore，Agent.Memory() 只暴露 Inspector；工具使用 ToolBackend，gRPC 声明自己的读写管理接口。旧 Store/Save*/Search 方法仅供 adapter 兼容、测试和 benchmark，不进入 Agent、gateway、工具容器或 API 的依赖签名。没有通过 alias 将 SDK DTO 导出到消费者。

后端构造集中于 memoryaestus.Open；Agent 默认装配点为 memory_runtime.go，并提供 NewWithMemory 注入完整运行时。session.Manager 由应用单独创建。上下文通过 MidTermStore.SaveSummary 提交中立 SummaryRequest，不直接调用适配包。Tidal 内核/配置/统计值位于 memory/diagnostic，统计经显式端口读取；运行时与 CLI 共用 TidalStorePath。单缓冲 shortTerm 字段及 sessionless 回退已删除，测试使用明确 session。

短期消息返回中立的 Message；provider 消息构造归宿主。会话缓冲、摘要与事实虽然复用同一库，仍由不同端口管理。TierShort 是持久事实的保留层级，不等于会话窗口。

## 读写路径

```go
type Reader interface {
    Recall(context.Context, RecallRequest) ([]SearchResult, error)
}
type Writer interface {
    Remember(context.Context, RememberRequest) (RememberResult, error)
}
```

memory.Write 执行共享内容准入，再调用 Writer.Remember。工具的呈现格式、metadata 和时间参数在写入前校验；格式错误不会产生已保存的记忆。HTTP、gRPC、显式 Remember 和自动捕获复用此准入，因此会拒绝此前弱入口可能保存的原始对话、密钥样式、提示注入和超长原文。这是明确的行为变化。

工具调用通过 ContextDetailedHandler 保留请求取消。HTTP 使用请求 context，gRPC 直接传递 RPC context；兼容的无 context 方法使用 background。自动捕获失败记录可观察错误，不再静默忽略。输入拒绝与持久化故障区分，HTTP 拒绝返回 400，gRPC 返回 InvalidArgument。

HTTP 普通召回、trace、工具及 gRPC 均使用 Reader.Recall(ctx, request)。普通 HTTP 保留原先图深度 1 的默认行为，trace 保留 SkipAccessStats；请求取消/超时返回 408。诊断通过 SourceInfo 描述来源，消费者无需调用存储目录方法。

自动维护改为 SDK CompactShortTerm：按类别、规范化标签集合、ExpiresAt 和 ValidUntil 独立分组，每组保留最新 5 条；超过 500 字符预算的完整事实保留原文，不截断后删除。状态、supersedes、路由策略记录不参与压缩。摘要继承作用域与结束时间、链接/别名，使用最晚起始时间和最低置信度。它只写 durable vault，不再复制进会话摘要库；错误被返回并记录。旧 SDK Summarize 也拒绝不兼容、无效或缺失的来源。

记忆用例只返回证据与策略信号。工具可执行性、PolicySafe、审批和实际执行保留在 agent/memory_gate.go。SDK 不调用 LLM、不执行工具。RAG 不并入记忆事实写入。

## Aestus 配套能力

- 新增 Remember / Recall 类型化 API；写入返回真实 ID、路径、created/updated、superseded IDs。
- upsert_state 与 supersede 在同一 Store 锁内协调，完成一次持久化遍历，避免宿主的查旧→保存→失效旧状态三次调用交错。
- 重复当前状态保留 ID；返回历史值时创建新版本，保留旧记录，避免 self-supersession 和历史循环。
- EntryFilter 在种子、图传播、top-K、时态解析之前生效；历史搜索也应用过滤。不可见别名所有者不会让可见目标变成歧义。
- ByTierWithOptions 先对可见事实跨层级解析当前状态，再选 tier。context planner 使用它，避免旧长期事实在新版本属于其他 tier 时重新注入。
- 普通召回忽略被取代的旧 active 元数据记录；Route 保留相应时态解释。
- Get 在读锁内复制记录值，策略规范化先复制切片再处理，避免并发读取与重复写入发生竞态。SDK 嵌套元数据按只读值使用，adapter 再投影为宿主数据；两边都有并发 getter 回归测试。
- 最低 Go 版本由 1.27 调整为经过实际测试的 1.25，增加 Go 版本矩阵 CI；修复已有 eval 对 bot token 检测的失败。

EntryFilter 是宿主提供的可信谓词，不是认证系统。context planner 绑定当前 TurnScope；自动捕获写入用宿主 scope tags 限定去重范围。既有全库管理入口没有因此变成多租户 API，工具/HTTP 的 owner 权限与用户身份绑定需要由入口继续管理，不能让模型自由填写 namespace 获得权限。

## 保证与限制

状态更新提供同一 Store 内的逻辑串行性，**不提供跨进程锁或多文件崩溃原子事务**。同一 vault 应只有一个写入 Store/process。持久化故障可能留下部分磁盘提交；调用方收到错误，不自动重试、不双写、不静默回退到另一套引擎。

SDK 在变更前检查取消，包括取得写锁之后；写入开始后同步完成并返回实际结果，不能因超时伪装成没有写入。召回在同步调用前后检查取消，不能中断文件操作或自定义 reranker。诊断读取应设置 SkipAccessStats；反馈与可选遥测可能有自身副作用。

配置继续使用既有 memory/context 字段，没有新增 backend 开关或远程服务地址。生产 vault 路径仍由运行时 home 决定；SDK 负责保持 Markdown、ID、相对路径、wikilink、heading/block 引用及已有索引笔记兼容。实施前应备份真实 vault，并在副本上验证，当前测试只操作临时目录。

## 验证与升级

Aestus 维护原引擎测试以及新增的 SDK 并发状态、去重、作用域、当前事实与取消契约测试。LuckyAgent 新增真实 SDK 的旧 vault 读写兼容、引用/策略投影隔离、会话隔离、摘要和 Tidal 关闭测试，保留现有工具、Agent、HTTP、gRPC、gateway 和 benchmark 回归，并约束 SDK import 边界。

验证命令：

```bash
# Aestus
GOTOOLCHAIN=go1.25.0 GOCACHE=/tmp/aestus-go-cache go test ./... -race

# LuckyAgent，关闭临时开发 workspace，检查真实固定依赖
GOWORK=off GOTOOLCHAIN=go1.25.0 go test ./... -race
GOWORK=off GOTOOLCHAIN=go1.25.0 go build ./cmd/la
```

SDK 升级先改固定版本和 adapter，跑相同契约测试；语义变化仍需显式审查。当前依赖的是配套分支提交，未创建正式发布 tag。后续 Aestus 合并或发布稳定版本后，再以已验证版本替换 pseudo-version。

本地 cleanup/delta-memory-state-20261017 的暂态记忆工作未在此分支合入；后续合并需按新 Writer/ContextReader 端口迁移，避免恢复本地引擎或重复状态推断。
