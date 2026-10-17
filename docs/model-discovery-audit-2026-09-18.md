# 模型发现与切换调研（2026-09-18）

当前实现存在可复现的模型列表与切换缺陷。最直接的链路是：普通列表读取本地目录，在线发现仅由 Telegram 的 `refresh` 分支调用；在线结果临时注册进目录，但下一次切换又重建目录，导致后续切换失败。

本次仅增加调研报告，没有修改运行代码或生产配置。工作区存在并持续产生其他未提交改动，因此验证使用独立源码快照，并通过 Go overlay 在已提交版本 `5753ecfe2831b3a625494b98919fc1dcd30dea2b` 上复核。

## 本机检查

- 使用运行目录配置中的 chat endpoint 和凭据，执行了一次只读 `GET /models`：HTTP 200，返回 28 个模型，包含当前 `grok-4.5`。
- 该配置没有 `custom_models`，并启用了 `model_router.enable`。凭据未输出，也未调用收费的聊天生成接口。
- 本机 `lh` 是启动脚本，实际二进制为 `~/.local/share/luckyagent/lh`。构建信息为 `v1.4.6+dirty`，基于提交 `05c1ca7b59206a1763586fbf2675f6d7a2ff8875`，与本次源码基线不同；不能据此断言运行中的网关已经包含当前源码的全部行为。
- 返回模型列表只证明本次列表请求成功，不代表逐个模型都已通过聊天调用验证。

## 主要问题

### 1. “本地模型目录”和“当前账号可用模型”没有统一

入口行为如下：

| 入口 | 实际来源 | 是否在线发现 |
| --- | --- | --- |
| Telegram `/models`、`/models chat`、`/models provider <name>` | `Agent.ListModels`：当前选择、本地预置目录、自定义模型、进程内已注册结果 | 否 |
| Telegram `/models refresh` | 当前 chat endpoint 的发现接口 | 是 |
| 终端 `/models [kind]` | `Agent.ListModels` | 否 |
| HTTP `GET /api/v1/models` | `Agent.ListModels`，可按 kind/provider 过滤 | 否 |
| 网页聊天命令 `/models` | `Agent.ListModels` | 否 |
| QQ `/models` | `Catalog.List` | 否 |

HTTP handler 没有读取 `refresh` 参数；添加 `?refresh=true` 也不会发起在线查询。Telegram 的生产适配器实现了 `unifiedModelRuntime`，普通 `/models` 会直接走本地分支。

影响：上游存在的新模型不会自动出现，其他 provider 的预置模型却会混入列表。以当前配置为例，上游确实返回 `grok-4.5`，但本地预置目录不认识它。

证据：`internal/gateway/telegram/handler.go` 的 `handleModels`、`handleUnifiedModels`；`internal/agent/models.go` 的 `ListModels`；`internal/server/models_handlers.go` 的 `handleModels`；`internal/cli/lhcmd/chat_repl.go`；`internal/provider/catalog.go` 的 `registerDefaults`。

### 2. 列表显示当前模型，但切换时拒绝同一个 ID

`ListModels` 会把配置中的当前模型加入输出，而 `validateModelKind` 对 chat 只接受 `custom_models` 或 Catalog 中的 ID。两者的准入条件不同。

复现：配置 chat=`grok-4.5`，不设置 `custom_models`，启动 Agent 后切换到 `grok-4.5`，得到：

```text
chat model "grok-4.5" is not registered in the model catalog
```

该错误由本地校验产生，请求尚未发到模型服务商。当前配置与这一复现条件吻合，但未对生产进程执行切换。

证据：`internal/agent/models.go` 的 `ListModels`、`validateModelKind`。

### 3. 切换一次后，刚发现的模型丢失

在线发现调用 `catalog.Register(model)`，只更新进程内 Catalog。`SwitchModelKind` 随后调用 `ApplyRuntimeConfig`，后者使用 `NewModelCatalog()` 重建目录，只补充配置中的 `CustomModels`，没有保留发现结果。

复现顺序：

1. 将 `discovered-one` 和 `discovered-two` 注册为发现结果。
2. 切换到 `discovered-one`：成功。
3. 再切换到 `discovered-two`：报 `not registered in the model catalog`。

这可以解释“刷新后能切一次，之后又找不到”的表现。发现缓存和 Catalog 是不同对象，缓存存在并不能防止目录丢失。

证据：`internal/gateway/telegram/handler.go` 的 `handleModels`；`internal/agent/agent.go` 的 `ApplyRuntimeConfig`。

### 4. 切模型时可能误换协议适配器，却沿用旧地址和密钥

未显式传入 provider 时，`SwitchModelKind` 把目录中 `ModelInfo.Provider` 写入 endpoint。配置合并又保留旧 API base 和 key。

复现：当前使用 OpenAI-compatible 中转，切换到目录内的 `claude-sonnet-4-20250514`，provider 从 `openai-compatible` 变成 `anthropic`，地址和密钥仍属于原中转。

对于通过 OpenAI 协议提供 Claude 模型的中转，这会改变请求协议和鉴权方式。是否最终请求失败取决于该中转是否也支持 Anthropic 协议；本次确认的是错误的自动适配器切换，没有向真实服务商发送生成请求。

证据：`internal/agent/models.go` 的 `SwitchModelKind`；`internal/config/models.go` 的 `SetModelSelection`、`mergeModelEndpoint`。

### 5. 刷新结果会覆盖已有能力元数据

发现接口只提取 ID、显示名和 provider；`ModelCatalog.Register` 按 ID 整条覆盖，导致已有 capabilities、context window、价格字段消失。

复现：注册同 ID 的发现结果后，`gpt-5.4-mini` 原有的 `[chat streaming tools vision]` 变成空，context window 从 128000 变成 0。

同时，`modelKindsForInfo` 对非自定义记录默认返回 chat。在线列出的 embedding、image、tts 等模型没有能力分类依据，却会进入 chat 列表。模型目录也仅以 ID 为键，无法区分多个 endpoint 下的同名模型。

证据：`internal/provider/model_discovery.go` 的响应解析；`internal/provider/catalog.go` 的 `Register`；`internal/agent/models.go` 的 `modelKindsForInfo`。

## 其他已确认问题

| 问题 | 原因与表现 | 验证方式 |
| --- | --- | --- |
| `/models all` 返回 Usage | 第一个判断显式跳过 all，随后落入 `len(args)>0` 的错误分支 | 命令 handler 复现 |
| HTTP 404 不回退手工模型 | HTTP 不支持错误是 `ModelDiscoveryError{Kind: unsupported}`，回退却只检查另一种 sentinel `ErrModelDiscoveryUnsupported` | mock endpoint 返回 404，配置已有模型仍返回错误 |
| Anthropic 根地址发现路径不一致 | 聊天支持根地址并追加 `/v1/messages`，发现却直接追加 `/models` | 实际捕获请求路径；发现应与适配器的版本路径规则一致 |
| 发现缓存忽略 ExtraHeaders | cache key 只有 provider/base/model/key，不包括自定义请求头 | 修改 `X-Account` 后仍返回旧账号列表 |
| Telegram 长列表没有翻页 | 两个列表分支都最多输出 40 项，剩余仅显示数量 | 静态检查；当前上游 28 项不触发此限制 |

## 自动模型路由与手动切换

本机启用了 `model_router.enable`。`providerSnapshotForTurn` 每轮会依据任务选择模型，因此“全局配置模型”和“本轮实际模型”可以不同，这是当前路由行为，不能单独作为切换失败的证据。

Telegram 接受 `--pin`，但 `SwitchModelOptions.Pin` 没有完成会话固定：handler 只提示兼容接受该参数，没有阻止后续路由。应实现固定语义，或明确拒绝尚未实现的选项。

证据：`internal/agent/provider_snapshot.go`；`internal/agent/models.go` 的 `SwitchModelOptions`；`internal/gateway/telegram/handler.go` 的 `handleUnifiedModel`。

## 验证记录与边界

独立快照和 9 个缺陷复现测试位于 `/tmp/luckyagent-model-audit-fl3ncspz`。测试仅使用临时配置和本地 mock HTTP 服务，未发送 Telegram/QQ 消息，未切换生产配置。

- 工作区源码快照：9 个 `TestAudit*` 全部复现预期缺陷。
- 已提交版本 `5753ecf`：相同 9 个测试全部复现。这里“测试通过”表示成功确认现有缺陷，并不表示缺陷已修复。
- 已有模型相关测试首次检查时，工作区的 `TestManagerSetMultimodalImageProvider`、`TestAnalyzeAttachmentsUsesConfiguredProvider` 失败；这些与当时进行中的多模态配置变更相关，不能用来否定上述在已提交版本上复现的问题。
- 已提交版本的已有相关测试中，provider/config/server/telegram 通过，CLI 没有匹配测试；agent 一轮通过，后一次出现 `TestAgentSwitchModelKindPersistsNonChatSelection` 的临时目录清理失败（`rag: directory not empty`）。其测试创建 Agent 后没有关闭，存在清理时仍写目录的可能，未在本次修改。
- 最初一次复核遇到 `/tmp` 空间不足；把构建临时目录移到项目所在磁盘并串行运行后，9 个复现测试全部完成。已清理本次构建临时目录。
- 未执行完整 `go test ./...`，未验证每个真实模型的聊天权限，未重启或重新部署网关。

复现命令（在上述快照目录执行）：

```bash
go test -p 1 -overlay=./head-overlay.json \
  ./internal/provider ./internal/agent \
  ./internal/gateway/telegram ./internal/server \
  -run '^TestAudit' -count=1 -v
```

结果记录：`head-reproductions-retry.log`、`head-existing-tests-retry.log`。如果 `/tmp` 空间仍不足，需将 `TMPDIR` 和 `GOTMPDIR` 指向有足够空间的目录。

## 建议修复顺序

1. 在 Agent 层提供统一的发现服务，所有入口共用；返回来源、更新时间、错误状态，并区分“已知模型”“当前 endpoint 列出的模型”“手工配置模型”。
2. 让列表与切换共用校验规则；当前选择可重复选择，同一 endpoint 的新模型可以通过发现结果验证。普通模型切换保持 endpoint/provider/protocol/key，不从模型名称猜服务商。
3. 将发现结果与静态能力目录分离，以 endpoint 身份和模型 ID 联合管理；运行时重建不丢失有效发现结果，刷新不清空能力元数据。
4. 补齐 HTTP/CLI/Telegram 的 refresh、过滤和分页，修复 unsupported 回退、路径标准化、请求头缓存失效；未知能力不能自动宣称是 chat。
5. 区分配置模型与本轮实际模型，实现 `--pin` 或拒绝该参数，并同步命令说明及 GitHub Pages。若新增配置字段，再同步 `config.example.json`。

最先应修第 1～3 项，它们直接决定“能否看到真实列表”以及“列表中的模型能否连续切换”。
