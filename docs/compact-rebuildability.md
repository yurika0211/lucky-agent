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

同一轮里，只有当前会话会保存工具全文时，送回模型的那一份才使用有界投影，
并附上 `context_history_read` 读回引用。没有持久会话时，prompt 保留原文，
不发出读不回的引用。

`context.compact_model` 为空时仍使用当前已配置的 turn provider。本仓不猜测
一个未配置的更快模型。需要更快摘要时，显式设置已配置的 `compact_model`。
摘要预算按本次 `TranscriptRunes` 从 45s 线性升到 180s：0 或负数是 45s，
48000 rune 及以上是 180s。用户取消仍立即停止，不放宽已取消的 deadline。
host 把这个函数传给 tools `Config.SummaryBudget`。tools 功能分支已发布，
默认分支尚未合并。根 launcher 的 published 图还没有选中这次 agent 提交。

## 指标口径

`agent/compact_ab.go` 的 `SavedRatio` 是 `(controlBytes-treatmentBytes)/controlBytes`。
control 是旧行压缩器，treatment 是形状改写后再走同一行压缩器。至少 30 对才
`RatioReady`。它只表示新压缩器相对旧行压缩器多省了多少，不是整段会话只省 1.3%。

一份 ForceLocal 样品里，摘要估计 651、压缩前输入估计 111104。651/111104 只是
摘要估算除以被摘要覆盖的输入估算。它不含保留的 tail、附件、system prompt 和
工具 schema，也不能拿来和 `SavedRatio` 比较。

历史 usage 的 input p50 265k 提取源没有在本轮重测。最终 assistant usage 可以
累计一整轮里多次 provider 请求。本轮的 16520→1769 是一条离线工具正文样品，
不是线上 p50，也不能说真实 p50 已经下降。
