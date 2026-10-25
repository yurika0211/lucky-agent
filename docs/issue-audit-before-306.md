# #306 之前的未关闭 issue 核对

核对日期：2026-10-25 UTC。范围是 `yurika0211/lucky-agent` 中编号小于 306、
开始核对时仍为 open 的 issue，不包含 PR、已关闭 issue 或 Android 仓库。
共 14 项，其中 #81 与 #84 是同一 WebSocket 测试问题。

合并后补查结论：**13 项完成，#274 重新打开，尚未全部完成。** 9 MiB 的旧 Markdown
单行 JSON 在冷搜索时仍漏结果，见下表。主修复通过 [#351](https://github.com/yurika0211/lucky-agent/pull/351)
合入 `master`（`1694ad6`）；#274 的继续实现需要在所属的 runtime 仓库进行。

这是一次交付快照，不替代项目的长期进度总览。下表区分本次补齐与已有修复，
不把历史修复计为本次新写代码。

## 问题与交付

| issue | 处理 | 代码或证据 |
| --- | --- | --- |
| [#81](https://github.com/yurika0211/lucky-agent/issues/81)、[#84](https://github.com/yurika0211/lucky-agent/issues/84) | 合并现成修复并更新 app 依赖 | [app #20](https://github.com/yurika0211/luckyagent-app/pull/20)：取消后必须等待 handler 退出，不能用 `PendingCount == 0` 替代 join；race 回归重复 20 次通过 |
| [#102](https://github.com/yurika0211/lucky-agent/issues/102) | 合并现成修复并更新 tools 依赖 | [tools #14](https://github.com/yurika0211/luckyagent-tools/pull/14)：Codex resume 的 cwd 按显式参数、服务端返回、缓存顺序保留 |
| [#268](https://github.com/yurika0211/lucky-agent/issues/268) | 整合 #350 并补发布契约测试 | 清单内下载地址固定到 tag，不能使用浮动 latest |
| [#269](https://github.com/yurika0211/lucky-agent/issues/269) | 整合 #350、合并 app #20、接入主程序 | updater 和两种安装脚本解压前验证 SHA-256；Unix 复用 Python，避免依赖 macOS 默认缺失的 sha256sum |
| [#270](https://github.com/yurika0211/lucky-agent/issues/270) | 修正 #350 未覆盖的 tag-push 场景 | `published_at` 在 release event 字段为空时使用生成时的 UTC 时间，下载页已有日期映射不变 |
| [#274](https://github.com/yurika0211/lucky-agent/issues/274) | **未完成，已重新打开** | 小会话回归通过，但额外 9 MiB 旧格式复现仍是 `cold_hits=0`、加载后 `hot_hits=1`；`fileContainsFold` 的 Scanner 单行上限为 8 MiB 且忽略扫描错误 |
| [#275](https://github.com/yurika0211/lucky-agent/issues/275) | 已有修复，核验保留 | runtime 的加载失败返回错误，不提升为空的已加载历史；`TestSaveCorruptSegmentDoesNotWipe` 通过 |
| [#276](https://github.com/yurika0211/lucky-agent/issues/276) | 合并当前所属模块的修复并接入 | [runtime #18](https://github.com/yurika0211/luckyagent-runtime/pull/18) 取代旧路径 PR #287；构造时解析环境配置，校验向量维数/响应索引，保持 RAG fingerprint 一致 |
| [#281](https://github.com/yurika0211/lucky-agent/issues/281) | 已有修复，核验保留 | orchestrator `4c324046`：等任务完成而非固定 sleep；`TestDelegateManagerAutoModeUsesPlanner` 的 race 测试重复 50 次通过 |
| [#285](https://github.com/yurika0211/lucky-agent/issues/285) | 已有中文句界修复，补核 required schema | `05d6da9` 已修原文案例；显式 read/否定 write 回归通过，英文残留由 #291 补齐 |
| [#288](https://github.com/yurika0211/lucky-agent/issues/288) | 已有修复，核验保留 | `068730b`：含 ContentParts 时仍裁剪文本历史，保留结构化内容；两个 `TestFitContextWindow*` 回归通过 |
| [#289](https://github.com/yurika0211/lucky-agent/issues/289) | 本次新增修复 | 计量、交接、替换结果共用按调用分类规则；支持合并后的读取入口，读写混批及未知调用不交接 |
| [#290](https://github.com/yurika0211/lucky-agent/issues/290) | 审查、验证并合并现成修复 | [#291](https://github.com/yurika0211/lucky-agent/pull/291)：ASCII 句号隔离否定、按当前命中位置判断同名工具，不把逗号分隔的否定列表误判为肯定 |

## 生产依赖图

上游先合并，再接入消费者；没有本机 `replace`。

| 模块 | launcher 选择的版本 | 归属 |
| --- | --- | --- |
| Agent | `v0.3.2-0.20261025161000-2fb8b86095f3` | 本次已推送的叶模块源码 |
| app | `v0.4.2-0.20261025152028-0d7b953a6987` | 已合入 app #20 的测试过的 head，合并提交为 `ec2c61d3` |
| runtime | `v0.3.1-0.20261025155307-54def1957a16` | runtime #18 合并提交 |
| tools | `v0.2.4-0.20261025155312-14ca32a46742` | tools #14 合并提交 |
| orchestrator | `v0.1.1` | 原已选择版本，包含 #281 修复 |
| API | `v0.2.3` | app 当前 manifest 要求的版本 |

根 `go.mod` 与 `agent/go.mod` 分别验证。只跑根 `./...` 不代表测试了 Agent。
最终源码与下载版本的一致性用 `scripts/verify-agent-graph.py` 检查。

## #274 的剩余工作

在最终选择的 runtime 上，用标题不含关键词的旧 `.md` 会话，正文为 9 MiB 字符
后跟 `unique-zebra-42`。结果为 `legacy bytes=9437360 cold_hits=0`，
`GetMessagesWithError` 返回一条消息且无错误，随后同一查询 `hot_hits=1`。
原测试仅有短文本，因此没有覆盖真实大会话的单行限制。

需要在 `luckyagent-runtime/session` 将冷搜索改为按记录/消息解码的扫描，不靠提高
Scanner 上限解决；同时补超长旧文件、转义文本及保持懒加载的回归。本线程尚未挂载
runtime 仓库，因此没有在模块缓存或已删除的旧 `internal/session` 路径修改源码。
前面的关单已撤回，并在 issue 和 #351 留下更正说明。

## 验证环境和限制

- Windows / Go 1.25：工具意图、探索交接、embedder/RAG/Codex、updater 定向 race 回归通过。
- Linux WSL / Go 1.27.1：Agent 全量 `-race` 与 `go vet` 通过；会话和协作旧 issue 回归通过；
  WebSocket 取消回归重复 20 次通过。
- 清单生成、Linux/macOS 安装脚本通过离线 fixture 验证；真实 Windows PowerShell
  运行正确、大写、缺失和错误摘要四种场景，验证失败时没有进入解压。
- Windows 上现有 context fixture 的未关闭 SQLite 句柄会导致 TempDir 清理失败；
  同两项测试在 Linux 通过。没有把这个平台失败描述为全平台通过。
- WSL 联网下载受限，使用 Windows Go 已校验的模块缓存作为离线代理；
  本地 Linux 运行的 Go 版本不同于 CI 的 Go 1.25。
- tools 仓库没有 CI workflow：#14 的证据是 diff 审查和本地 Codex race 测试，
  不声称该 PR 有远端 CI。runtime #18、app #20、主仓 #291 的当前 head CI 已通过。
- 不重写历史 release 资产、不创建 release tag、不部署或重启用户服务。
  旧清单中的浮动 URL 可能触发校验失败；参见[发布完整性](release-integrity.md)。
- 本次没有修改配置 schema；已比对 `config.example.json`，无需新增配置。
