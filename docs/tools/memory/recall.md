# recall

记忆搜索现在是 `memory` 的 `action=recall`。它搜索 LuckyAgent 的 Obsidian-compatible Markdown memory vault，找回稳定的用户偏好、项目事实或已保存的结论。

这是只读操作，自动放行。

一次 recall 自己的上限是 15 秒，到点返回已经打开的笔记，并带 `memory recall timed out`。空结果在搜索结束时返回，不等这 15 秒。

日志 `memory recall` 带这些字段：`scan_ms`、`scan_scope`（走过的笔记数）、`graph_ms`、`graph_hits`、`read_ms`、`read_count`、`hits`。超时再加 `timed_out` 和 `timeout_phase`（`scan`、`graph` 或 `read`）。扫描和图展开在同一次加锁搜索里，两者的耗时相同；`graph_hits` 和 `timeout_phase` 用来区分是全库扫描还是图展开。`read_ms` 只计搜索之后逐篇打开的笔记。
