# Compact 标识符与可恢复性边界

工具结果先按行去重复、进度和空行，再在 rune 上限内优先保留路径、错误、命令等。
这是确定性规则，不是熵模型，也不承诺任意大的证据都能无损放进固定窗口。
保护行本身超过预算时仍会缩短；完整结果保留在会话中，通过工具结果引用读回。

按行选择会改变文本组成，token 数不随 rune cap 严格单调。
`fitToolEvidenceToBudget` 因此逐步收紧 cap 并校验实际候选，而不对这个非单调条件做二分。
压缩过程缓存当前字符数，避免每删一行重扫整个日志；最终分配也不反复扫描全部行找最长项。

摘要校验检查路径、URL、命令、commit、错误首行的有界集合。路径和命令标志区分大小写；
没有路径的命令也参与检查。测量值可以改写，不作为强制逐字回忆条件。
这仍是启发式检查，不是全部事实的正确性证明；最多校验 40 项，长标识符受采集上限限制。

本地摘要器只输出少数文件/命令条目，可能遗漏校验要求的来源。ForceLocal 模式补一段
从原文直接抽取的标识符列表，遵守共享引擎的 `MaxSummaryRunes` 上限，不放松校验门槛。
LLM 摘要缺失标识符仍会被拒绝；自动压缩可以再走既有的本地降级流程。

写入 compact boundary 前比对覆盖范围的完整消息指纹，包括工具参数和结构化字段。
这能发现摘要生成期间的改写，但读取、比较、追加并不是存储级原子 compare-and-append；
不宣称能约束其他进程或所有并发写入。

共享 tools 引擎的整体转写窗口、旧轨迹回放指针等仍由配套 PR 处理，
这里不代表 #337 的全部验收已完成。没有新增 runtime 配置字段。

只在能读回全文的会话附 `context_history_read` 引用。无会话不发不可读引用，
prompt 保留原文。

`context.compact_model` 为空时仍使用当前已配置的 turn provider。本仓不猜测
一个未配置的更快模型。需要更快摘要时，显式设置已配置的 `compact_model`。
摘要预算按本次 `TranscriptRunes` 从 45s 线性升到 180s：0 或负数是 45s，
48000 rune 及以上是 180s。用户取消仍立即停止，不放宽已取消的 deadline。
host 把这个函数传给 tools `Config.SummaryBudget`。实际选中的模块用
`GOWORK=off go list -m` 核对，不把某次功能分支提交写进产品说明。

## 指标口径

`agent/compact_ab.go` 的 `SavedRatio` 是 `(controlBytes-treatmentBytes)/controlBytes`。
control 是旧行压缩器，treatment 是形状改写后再走同一行压缩器。至少 30 对才
`RatioReady`。它只表示新压缩器相对旧行压缩器多省了多少，不是整段会话只省 1.3%。

一份 ForceLocal 样品里，摘要估计 651、压缩前输入估计 111104。651/111104 只是
摘要估算除以被摘要覆盖的输入估算。它不含保留的 tail、附件、system prompt 和
工具 schema，也不能拿来和 `SavedRatio` 比较。

历史 566 条正 input usage、86 个会话，来自 segment_v1 的 assistant usage 元数据。
筛选是正 input，时间落在 2026-09-24 到 2026-11-07。分位数用 nearest-rank：
升序后取 `ceil(n*p)-1`。按这个口径，p50 是 264544，p95 是 2550139，最大是
11590241，grok-4.7 是 303/566。缓存命中比的 p50 约 0.957，均值约 0.879。
其中 565/566 是下一条 user 之前的最后一个 assistant。这不是严格 turn id，
也不是 566 次 provider 请求。正式最终 assistant usage 会把一轮里多次 provider
响应累加。没有逐请求成本流水时，不能把这个 p50 当成单次 prompt。

`SavedRatio` 约 0.013 只表示新压缩器相对旧行压缩器再省约 1.3% 字节。
离线完整请求样品 16593→2048 固定了 system 和 schema，只比较工具正文投影。
它不是线上 p50，也不能说等待已经下降。
这些 before/after 比较同一输入的原文与既有投影，不是本次提交相对主线基线的性能收益。

## 无收益与观测

提交 compact boundary 前，主仓先冻结一次非 history 上下文，再把同一份
system、project、memory、RAG、skill hint 代入压缩前后的 history。history
使用完整 transcript。after 候选复制完整历史，并在末尾追加一个
boundary，与 `AddCompactBoundary` 相同。它不删除 raw，不搬动 retained tail，
也不按正文搜索插入点。`CompactSegments` 用非 boundary 消息计数的绝对
`[From, To)` 读取覆盖范围。旧 boundary 物理位置之前的 retained raw 仍留在
原处。两侧都保留已有 boundary 和它们的附件。不把旧摘要再包一层。比较包含工具 schema 和 `prepareModelRequest` 的最后一层 fit。fit 返回
错误时停止，不把残缺 candidate 当成成功。人工 compact 没有下一条 user 文本。
自动 compact 带上触发该轮的 routing text。比较不包含本轮新附件：没有新的附件
字节，也不为比较重跑媒体解析。ReadOnly 不写 memory hygiene、context cache 或
skill-route 计数。

压缩后的候选请求不小于压缩前时，返回 `ErrCompactNoBenefit`。错误文本包含
`no-benefit: skipped`。不写 backup，不写 boundary，不报 saving。DryRun 同样
返回这个错误，也不写。现有 API 在 `err != nil` 时返回 400，CLI 走既有失败通道。
这个 400 是兼容既有端点处理，不是模型失败。自动路径用 `errors.Is` 识别后跳过，
不计普通 failure，不走 ForceLocal。跳过复用既有自动 cooldown 计数，避免每轮
重复同一次昂贵调用。

成功 compact 的 `pre_token_estimate` / `post_token_estimate` 是同一次完整候选
请求的 before/after。它们包含冻结的非 history 快照、history、schema 和
`prepareModelRequest` 的 fit。这是本地估算，不是 provider usage。
`summary_tokens` 只表示摘要正文。API、`CompactDisplay`、done/degraded/dry-run
进度和 latest trace 都读这一对字段。文案写明 “full request estimate”，不宣称
线上省了 token。

`policy_version` 为 `checkpoint-segments-v2` 的记录用这个口径。更早的
`checkpoint-segments-v1` 记录里，pre 是被覆盖消息的局部估算，post 等于摘要
正文估算。读取旧 session 时保留原数字，不回写。

start/progress 里的 `pre_token_estimate` 仍是摘要输入的局部估算，message 写明
summary-input tokens。它和 done 事件不是同一口径。skip 事件上的 pre/post 已是
完整请求估算，并且 message 写明这一点；skip 不写 metadata。

摘要调用的 usage 只在真正写入 boundary 时进入 `CompactMetadata` /
`CompactTrace` 的 `summary_usage`。没有可见 usage 时这些键省略，不用 0 冒充
完整账。没有首 token 时间，不写 TTFT。失败或 skip 没有 boundary，进度和日志
里仍能看到阶段、回调次数和原因。
