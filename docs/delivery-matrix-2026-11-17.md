# 交付矩阵草稿（2026-11-17）

这是仓库内的当前草稿。旧的 2026-11-05 / 2026-11-07 快照留在 GitHub issue #395，
不再当作合并队列。看板、Obsidian 和关单文字由 docs 代理处理。本文件不改 GitHub。

核对时间：2026-11-17。主仓 `origin/master` 在开始本分支时为
`596f265866c1115a3ef163d5648b170a7f22080f`（Merge PR #473）。
本表只记录本 worktree 已核对的事实。未在本机查询的外部仓 PR 标成未复核。

| 项 | 提供方 | 默认分支 | 本仓状态 | 验证 | 下一步 |
| --- | --- | --- | --- | --- | --- |
| #449 迭代上限测试 | lucky-agent `agent/` | PR #451 `d68a8a6` 已是 `596f265` 祖先 | 不新增补丁 | 本 worktree `go test -race` 两测试通过 | docs 关单 |
| #450 delegate message race | lucky-agent `agent/` | 同上，`f247bec` + `441a161` | 不新增补丁 | 同上 | docs 关单 |
| #453 长会话输入 | lucky-agent `agent/` | 本分支 | 同一轮工具结果回灌改为有界投影；会话仍存全文 | 见实现记录中的样品数字 | 独立审查 |
| #471 45s 摘要超时 | luckyagent-tools `compact.SummaryTimeout` | 未改第二仓 | 本仓不能放大该常量，也不猜测未配置模型 | 无第二仓测试 | 外仓接口见实现记录 |
| #464 默认 sandbox=dev | lucky-agent Agent 提示 + docs | 本分支 | 提示与文档写明 dev=宿主 shell | 提示单测 | app `/set` 文案仍在外仓 |
| #395 进度看板 | docs | 本文件是草稿 | 不关 GitHub issue | 未改 GitHub | docs 代理写看板 |

## 仍不要合并

- lucky-agent PR #398：本轮未复核其最新状态。即使它已变干净，也不在本任务授权内合并。

## 未在本机复核的外部项

- luckyagent-android、luckyagent-ios、luckyagent-tools 的 OPEN PR 数量。
- Android versionName。
- 本 launcher 在 `GOWORK=off` 下选中的 Agent 版本。本分支只改了本仓源码，
  不能说根 launcher 已经选中这次提交。
