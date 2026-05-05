# LuckyHarness 🍀

> Go 版自主 AI Agent 框架 — 仿 [Hermes Agent](https://github.com/NousResearch/hermes-agent) 架构，迭代式开发。

## 项目定位

LuckyHarness 是一个用 Go 重写的 AI Agent 框架，参考 Hermes Agent 的核心架构设计，逐步实现其关键特性：

- 🧠 **SOUL 系统** — 可定制的 Agent 人格与行为指令
- 🔌 **Provider 路由** — 多 LLM 提供商自动解析与切换
- 💾 **持久记忆** — 跨会话记忆与自动学习
- 🛠️ **工具系统** — 可扩展的 Skill/Tool 插件架构
- 🔄 **Agent Loop** — 自主推理-行动循环
- 📱 **多平台网关** — Telegram/Discord/Slack/微信等消息平台接入
- ⏰ **定时任务** — 自然语言 cron 调度

## 当前运行态（2026-04-28）

以下能力是当前代码已接入并在运行路径生效的：

- Provider 治理中间件：`retry` / `circuit_breaker` / `rate_limit`
- 模型路由：`model_router` 在 chat/loop 入口生效
- Telemetry：支持 HTTP 中间件链路 + Agent Loop span
- JSON 热路径：server/provider/embedder 切到 `jsoniter` 兼容模式

关键行为说明：

- 当配置了 `fallbacks` 时，`model_router` 会主动跳过，避免两套路由策略冲突。
- Telemetry 使用环境变量开关，不走 `config.json` 字段。
- `jsoniter` 当前使用兼容模式，行为与标准库保持一致优先。

Telemetry 环境变量：

- `LH_TELEMETRY_ENABLED`
- `LH_TELEMETRY_EXPORTER`
- `LH_TELEMETRY_OTLP_ENDPOINT`
- `LH_TELEMETRY_SAMPLE_RATE`

最小示例：

```bash
export LH_TELEMETRY_ENABLED=true
export LH_TELEMETRY_EXPORTER=stdout
go run ./cmd/lh serve --addr 127.0.0.1:9090
```

说明：

- 当前版本未声明已完成 `zap` 全量替换，请继续按现有日志路径使用。


## v0.24.0 新特性

### 工作流引擎

支持 DAG（有向无环图）任务编排，实现复杂工作流自动化：

```go
// 定义工作流
workflow := workflow.NewWorkflow("data-pipeline", []*workflow.Task{
    {ID: "fetch", Name: "Fetch Data", Action: "http", Params: map[string]interface{}{"url": "https://api.example.com/data"}},
    {ID: "parse", Name: "Parse Data", Action: "script", DependsOn: []string{"fetch"}},
    {ID: "validate", Name: "Validate", Action: "script", DependsOn: []string{"parse"}},
    {ID: "store", Name: "Store Results", Action: "http", DependsOn: []string{"validate"}},
})

// 注册并启动
engine.RegisterWorkflow(workflow)
instance, _ := engine.StartWorkflow(workflow.ID)
```

#### 特性

- **DAG 定义** — YAML/JSON 工作流定义，支持复杂依赖关系
- **拓扑排序** — 自动计算执行顺序，检测循环依赖
- **并行执行** — 无依赖任务并行执行，提升效率
- **重试机制** — 可配置重试次数和延迟
- **超时控制** — 任务级别超时设置
- **状态管理** — 实时追踪任务状态和结果

#### API 端点

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/v1/workflows` | 列出所有工作流 |
| POST | `/api/v1/workflows` | 创建工作流 |
| GET | `/api/v1/workflows/{id}` | 获取工作流详情 |
| DELETE | `/api/v1/workflows/{id}` | 删除工作流 |
| GET | `/api/v1/workflow-instances` | 列出所有实例 |
| POST | `/api/v1/workflow-instances` | 启动工作流 |
| GET | `/api/v1/workflow-instances/{id}` | 获取实例状态 |
| GET | `/api/v1/workflow-instances/{id}/results` | 获取执行结果 |
| DELETE | `/api/v1/workflow-instances/{id}` | 取消实例 |

## v0.23.0 新特性

### 流式 RAG 系统

支持文件变更监控和增量索引，实现实时知识库更新：

```bash
# 添加监控目录
lh rag watch ./docs

# 扫描变更
lh rag scan

# 启动后台索引
lh rag start

# 查看状态
lh rag status

# 处理队列
lh rag process 10

# 停止后台索引
lh rag stop
```

#### 特性

- **StreamIndexer** — 流式索引器，支持增量添加/更新/删除
- **ChangeDetector** — 文件哈希对比，识别新增/修改/删除
- **IndexQueue** — 索引任务队列，支持优先级和批处理
- **File Watcher** — 目录监控，自动触发索引
- **API 端点** — 8 个 RESTful API 端点
- **REPL 命令** — lh rag watch/unwatch/scan/start/stop/status/queue/process

#### API 端点

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/v1/rag/stream/watch` | 添加监控目录 |
| DELETE | `/api/v1/rag/stream/watch` | 移除监控目录 |
| POST | `/api/v1/rag/stream/scan` | 扫描变更 |
| POST | `/api/v1/rag/stream/start` | 启动后台索引 |
| POST | `/api/v1/rag/stream/stop` | 停止后台索引 |
| GET | `/api/v1/rag/stream/status` | 查看状态 |
| POST | `/api/v1/rag/stream/index` | 立即索引路径 |
| DELETE | `/api/v1/rag/stream/index` | 移除路径 |
| GET | `/api/v1/rag/stream/queue` | 查看队列 |
| POST | `/api/v1/rag/stream/process` | 处理队列 |

## v0.22.0 新特性

### 多 Agent 协作系统

支持多个 Agent 协同完成复杂任务，提供三种协作模式：

```bash
# 列出注册的 Agent
lh agent list

# 创建并行协作任务
lh agent delegate parallel "分析这段代码" agent-1 agent-2

# 查看任务状态
lh agent task collab-1

# 取消任务
lh agent cancel collab-1
```

#### 特性

- **Agent Registry** — 注册、发现、健康检查、能力匹配
- **任务委派** — 任务拆分、超时管理、取消、重试
- **结果聚合** — 5 种聚合策略（concat/best/vote/merge/summary）
- **协作模式** — Pipeline（串行）、Parallel（并行）、Debate（辩论）
- **API 端点** — 8 个 RESTful API 端点
- **REPL 命令** — lh agent list/delegate/task/tasks/cancel

#### 协作模式

| 模式 | 说明 | 适用场景 |
|------|------|----------|
| Pipeline | 串行执行，前一个输出作为后一个输入 | 多步骤流水线 |
| Parallel | 并行执行，结果聚合 | 多 Agent 同时处理 |
| Debate | 辩论模式，多轮讨论后达成共识 | 决策、评审 |

#### API 端点

| 方法 | 路径 | 说明 |
|------|------|------|
| GET  | `/api/v1/agents` | 列出所有 Agent |
| GET  | `/api/v1/agents/get?id=` | 获取单个 Agent |
| POST | `/api/v1/agents/register` | 注册新 Agent |
| DELETE | `/api/v1/agents/deregister?id=` | 注销 Agent |
| POST | `/api/v1/agents/delegate` | 创建协作任务 |
| GET  | `/api/v1/agents/task?id=` | 获取任务状态 |
| GET  | `/api/v1/agents/tasks` | 列出所有任务 |
| POST | `/api/v1/agents/cancel?id=` | 取消任务 |

## v0.21.0 新特性

### 嵌入模型管理系统

统一的嵌入模型管理，支持多 Provider 注册、切换和缓存：

```bash
# 列出嵌入模型
/embedder

# 切换嵌入模型
/embedder switch openai-default

# 测试嵌入
/embedder test "Hello, world!"
```

#### 特性

- **Embedder 接口** — 统一的 Embed/EmbedBatch/Dimension/Name/Model 接口
- **Embedder Registry** — 注册、切换、列表管理多个嵌入模型
- **LRU 缓存** — 相同输入自动缓存向量结果，避免重复 API 调用
- **OpenAI Provider** — 支持 text-embedding-3-small/large, ada-002 及兼容端点
- **Ollama Provider** — 支持 nomic-embed-text, mxbai-embed-large 等本地模型
- **RAG 集成** — RAG 管理器使用 Embedder Registry 的 active embedder（带缓存）

#### API 端点

| 方法 | 路径 | 说明 |
|------|------|------|
| GET  | `/api/v1/embedders` | 列出所有嵌入模型 |
| GET  | `/api/v1/embedders/{id}` | 获取嵌入模型详情 |
| POST | `/api/v1/embedders/register` | 注册新嵌入模型 |
| POST | `/api/v1/embedders/switch` | 切换活跃嵌入模型 |
| POST | `/api/v1/embedders/{id}/test` | 测试嵌入模型 |

## v0.20.0 新特性

### RAG SQLite 持久化存储

RAG 知识库支持 SQLite 后端持久化，替代纯内存/JSON 方案，支持增量更新和高效查询：

```bash
# 使用 SQLite 后端（默认启用）
/rag store sqlite --db ./data/rag.db

# 查看存储状态
/rag store status

# 切换回内存存储
/rag store memory
```

#### 特性

- **SQLite 向量存储** — 向量和元数据持久化到 SQLite 数据库
- **WAL 模式** — 启用 Write-Ahead Logging，支持并发读写
- **增量更新** — Upsert 语义，支持插入和更新
- **内存缓存** — 懒加载缓存，搜索时自动从 DB 加载
- **并发安全** — RWMutex 保护，支持多 goroutine 并发访问
- **自动持久化** — Agent 关闭时自动保存，启动时自动加载

#### API 端点

| 方法 | 路径 | 说明 |
|------|------|------|
| GET  | `/api/v1/rag/store` | 查看存储后端状态 |
| POST | `/api/v1/rag/store` | 切换存储后端 (sqlite/memory) |

## v0.19.0 新特性

### 多语言 SOUL 模板系统

内置 SOUL 模板管理器，支持多语言人格模板的加载、变量插值和语言检测：

```bash
# 列出可用模板
lh soul templates

# 使用模板创建 SOUL
lh soul apply --template coder --lang zh

# 查看模板详情
lh soul template-info coder
```

#### 6 个内置模板

| 模板 | 说明 | 适用场景 |
|------|------|----------|
| `coder` | 编程助手 | 代码生成、调试、重构 |
| `writer` | 写作助手 | 文案、文章、翻译 |
| `analyst` | 数据分析师 | 数据分析、报告生成 |
| `tutor` | 教学助手 | 知识讲解、学习指导 |
| `creative` | 创意助手 | 头脑风暴、创意生成 |
| `minimal` | 极简模板 | 自定义起点 |

#### 变量插值

模板支持 `{{.Variable}}` 格式的变量插值，自动检测语言并填充默认值。

#### API 端点

| 方法 | 路径 | 说明 |
|------|------|------|
| GET  | `/api/v1/soul/templates` | 模板列表 |
| GET  | `/api/v1/soul/templates/{name}` | 模板详情 |
| POST | `/api/v1/soul/apply` | 应用模板 |

## v0.18.0 新特性

### WebSocket 实时通信

内置 WebSocket 支持，实现双向实时通信、会话绑定、心跳保活和断线重连：

#### 连接与通信

```bash
# 启动 API Server（自动启用 WebSocket）
lh serve

# WebSocket 端点
ws://localhost:9090/api/v1/ws?session=my-session

# 查看 WebSocket 统计
lh ws stats
```

#### 消息协议

所有消息使用 JSON 格式：

```json
// 客户端 → 服务端
{"type": "chat", "session_id": "my-session", "data": {"message": "hello", "stream": true}}
{"type": "ping", "session_id": "my-session", "data": null}
{"type": "reconnect", "session_id": "my-session", "data": {"last_message_id": "ws-xxx"}}

// 服务端 → 客户端
{"type": "stream_chunk", "session_id": "my-session", "data": {"content": "Hello", "done": false}}
{"type": "stream_end", "session_id": "my-session", "data": {"full_response": "...", "iterations": 1}}
{"type": "status", "session_id": "my-session", "data": {"state": "thinking", "message": "processing"}}
{"type": "pong", "session_id": "my-session", "data": null}
{"type": "error", "session_id": "my-session", "data": {"code": "ERR001", "message": "..."}}
```

#### 特性

- **会话绑定** — 多客户端可连接同一 session，消息广播到同 session 所有客户端
- **心跳保活** — 自动 ping/pong，54s 间隔，60s 超时
- **断线重连** — 客户端发送 `reconnect` 消息携带 `last_message_id`
- **流式推送** — Agent 流式输出通过 WebSocket 实时推送
- **工具调用通知** — 工具调用状态通过 `tool_call` / `tool_result` 实时推送

#### API 端点

| 端点 | 方法 | 说明 |
|------|------|------|
| `/api/v1/ws` | WebSocket | WebSocket 连接（`?session=<id>`） |
| `/api/v1/ws/stats` | GET | WebSocket 统计信息 |

## v0.17.0 新特性

### Observability & Metrics

内置可观测性系统，支持结构化日志、Prometheus 指标和三级健康检查：

#### 结构化日志

```bash
# 启动时配置日志级别和格式
lh serve --log-level debug --log-format json

# 日志级别: debug, info, warn, error
# 日志格式: json, text (默认)
```

#### Prometheus 指标

```bash
# 获取 Prometheus 格式指标
curl http://localhost:9090/api/v1/metrics

# CLI 查看指标
lh metrics
```

指标包括：
- `lh_requests_total` — 总请求数
- `lh_chat_requests_total` — 聊天请求数
- `lh_error_requests_total` — 错误请求数
- `lh_tool_calls_total` — 工具调用数
- `lh_function_calls_total` — Function Call 数
- `lh_active_sessions` — 活跃会话数
- `lh_provider_calls_total{provider=}` — Provider 调用数
- `lh_provider_errors_total{provider=}` — Provider 错误数
- `lh_provider_latency_ms{provider=}` — Provider 平均延迟

#### 三级健康检查

| 端点 | 用途 | 说明 |
|------|------|------|
| `GET /api/v1/health` | 兼容旧版 | 简单状态检查 |
| `GET /api/v1/health/live` | Liveness | 进程是否存活 |
| `GET /api/v1/health/ready` | Readiness | 是否可以接受流量 |
| `GET /api/v1/health/detail` | Detail | 详细健康状态 |

Readiness 检查包含：
- **memory** — 记忆系统是否初始化
- **provider** — Provider 是否配置

返回状态：
- `healthy` — 一切正常
- `degraded` — 部分功能降级（仍可服务）
- `unhealthy` — 关键组件不可用（返回 503）

#### Serve 命令新增参数

```bash
lh serve --metrics-addr :9091    # 独立 metrics 端口
lh serve --log-level debug       # 日志级别
lh serve --log-format json       # JSON 格式日志
```

## v0.16.0 新特性

### Function Calling (OpenAI 原生)

内置 OpenAI Function Calling 协议适配，支持多轮工具调用：

```bash
# 列出 Function Calling 工具
lh fc tools

# 查看调用历史
lh fc history

# 清除历史
lh fc clear
```

### API 端点

```bash
# 执行 function calling
curl -X POST http://localhost:9090/api/v1/fc \
  -H "Content-Type: application/json" \
  -d '{"message": "What is the weather in Tokyo?", "auto_approve": true}'

# 列出可用工具
curl http://localhost:9090/api/v1/fc/tools

# 查看调用历史
curl http://localhost:9090/api/v1/fc/history
```

### FunctionCallingProvider 接口

```go
// 支持 Function Calling 的 Provider 接口
type FunctionCallingProvider interface {
    Provider
    ChatWithOptions(ctx, messages, opts) (*Response, error)
    ChatStreamWithOptions(ctx, messages, opts) (<-chan StreamChunk, error)
}
```

## v0.15.0 新特性

### Plugin Marketplace

内置插件市场系统，支持插件的安装、卸载、更新、搜索和权限管理：

```bash
# 安装插件（本地路径）
lh plugin install /path/to/my-plugin

# 列出已安装插件
lh plugin list

# 查看插件详情
lh plugin info my-plugin

# 搜索插件
lh plugin search "web search"

# 更新插件
lh plugin update my-plugin /path/to/new-version

# 启用/禁用插件
lh plugin enable my-plugin
lh plugin disable my-plugin

# 卸载插件
lh plugin remove my-plugin
```

### plugin.yaml 清单格式

```yaml
name: my-plugin
version: 1.0.0
author: author-name
description: A cool plugin
license: MIT
homepage: https://example.com
entry: main.go
type: skill          # skill | tool | provider | hook
min_version: 0.14.0
tags:
  - search
  - web
dependencies:
  - base-plugin@1.0.0
permissions:
  - filesystem
  - network
```

### 权限系统

8 种权限级别，默认受限模式：

| 权限 | 说明 |
|------|------|
| filesystem | 文件系统访问 |
| network | 网络访问 |
| memory | 记忆系统访问 |
| tool | 工具注册 |
| rag | RAG 知识库访问 |
| session | 会话访问 |
| config | 配置修改 |
| admin | 管理员操作 |

### API 端点

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/v1/plugins` | 插件列表（支持 ?type=&status= 过滤） |
| GET | `/api/v1/plugins/search?q=` | 搜索插件 |
| POST | `/api/v1/plugins/install` | 安装插件 |
| DELETE | `/api/v1/plugins/{name}` | 卸载插件 |
| POST | `/api/v1/plugins/{name}/enable` | 启用插件 |
| POST | `/api/v1/plugins/{name}/disable` | 禁用插件 |
| GET | `/api/v1/plugins/{name}/permissions` | 查看权限 |
| POST | `/api/v1/plugins/{name}/permissions` | 授予/撤销权限 |

### 资源限制

默认沙箱限制：

| 限制项 | 默认值 |
|--------|--------|
| 最大内存 | 256 MB |
| 最大 CPU | 50% |
| 最大 Goroutine | 10 |
| 执行超时 | 30s |
| 最大输出 | 1 MB |
| 调用频率 | 60/min |

## v0.14.0 新特性

### RAG 知识库

内置 RAG（Retrieval-Augmented Generation）知识库，支持文档索引、语义检索和上下文注入：

```bash
# 索引文件到知识库
/rag index /path/to/document.md

# 索引文本内容
/rag index --text "source" "title" "content"

# 搜索知识库
/rag search "programming language"

# 查看知识库统计
/rag stats

# 列出所有文档
/rag list

# 删除文档
/rag remove <doc_id>
```

### API 端点

| 端点 | 方法 | 说明 |
|------|------|------|
| `/api/v1/rag/index` | POST | 索引文件/文本/目录 |
| `/api/v1/rag/index` | DELETE | 删除文档 |
| `/api/v1/rag/search` | POST | 语义搜索 |
| `/api/v1/rag/stats` | GET | 知识库统计 |

### 特性

- **向量索引**：内存向量存储 + 余弦相似度搜索
- **智能分块**：按段落/句子分割，支持重叠窗口
- **MMR 重排**：Maximal Marginal Relevance 多样性重排
- **持久化**：JSON 序列化，启动自动加载，关闭自动保存
- **可扩展 Embedder**：MockEmbedder（测试）/ OpenAI Embedder（生产）/ Ollama Embedder（本地）
- **Embedder Registry**：多模型注册、切换、LRU 缓存（v0.21.0）
- **自动上下文注入**：对话时自动检索相关知识注入 system prompt

## v0.13.0 新特性

### Context Window 管理

自动管理上下文窗口，防止超出模型 token 限制：

```bash
# 查看上下文窗口状态
/context

# 手动触发裁剪
/context fit

# 查看裁剪策略
/context strategy
```

### 4 种裁剪策略

| 策略 | 说明 |
|------|------|
| TrimOldest | 优先裁剪最旧消息 |
| TrimLowPriority | 优先裁剪低优先级消息 |
| TrimSlidingWindow | 滑动窗口保留最近 N 条 |
| TrimSummarize | 摘要压缩低优先级消息 |

### Token 估算

启发式 token 估算，支持英文/中文/代码/混合文本：

```go
estimator := contextx.NewTokenEstimator()
tokens := estimator.Estimate("你好世界 Hello World")
```

### API 端点

| 方法 | 路径 | 说明 |
|------|------|------|
| GET  | `/api/v1/context` | 上下文窗口配置查询 |
| POST | `/api/v1/context/fit` | 手动触发上下文裁剪 |

## v0.12.0 新特性

### API Server

启动 HTTP API Server，暴露 RESTful API 供外部程序调用：

```bash
# 启动 API Server
lh serve

# 自定义地址和认证
lh serve --addr :8080 --api-keys key1,key2 --rate-limit 120

# 禁用 CORS
lh serve --no-cors
```

### API 端点

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/v1/chat` | 流式聊天 (SSE) |
| POST | `/api/v1/chat/sync` | 同步聊天 |
| GET  | `/api/v1/sessions` | 会话列表 |
| GET  | `/api/v1/memory` | 记忆统计 |
| POST | `/api/v1/memory` | 保存记忆 |
| GET  | `/api/v1/memory/recall?q=` | 搜索记忆 |
| GET  | `/api/v1/memory/stats` | 记忆统计 |
| GET  | `/api/v1/tools` | 工具列表 |
| GET  | `/api/v1/stats` | 服务器统计 |
| GET  | `/api/v1/soul` | SOUL 信息 |
| GET  | `/api/v1/health` | 健康检查 |

### 认证

支持三种 API Key 传递方式：

```bash
# Header
curl -H "X-API-Key: your-key" http://localhost:9090/api/v1/stats

# Bearer Token
curl -H "Authorization: Bearer your-key" http://localhost:9090/api/v1/stats

# Query Parameter
curl "http://localhost:9090/api/v1/stats?api_key=your-key"
```

### SSE 流式聊天

```bash
curl -N -X POST http://localhost:9090/api/v1/chat \
  -H "Content-Type: application/json" \
  -d '{"message": "Hello!", "stream": true}'
```

## 生产环境部署

### 前置要求

- Docker 20.10+ 和 Docker Compose v2
- 一个 LLM API Key（OpenAI / Anthropic / 兼容 OpenAI 格式的第三方服务）
- （可选）Telegram Bot Token

### 1. 拉取镜像

```bash
docker pull ghcr.io/yurika0211/luckyharness:latest
```

指定版本：

```bash
docker pull ghcr.io/yurika0211/luckyharness:v0.36.0
```

### 2. 准备配置文件

```bash
git clone https://github.com/yurika0211/luckyharness.git
cd luckyharness

# 容器会直接读取仓库根目录下的 config.json
# 按你的 provider / telegram / web_search 等实际参数编辑它
```

开发/生产共用的关键点：

- LuckyHarness 主配置来自 `./config.json`
- Docker 只负责挂载 `config.json` 到 `/var/lib/luckyharness/config.json`
- `HOME` 固定为 `/var/lib/luckyharness`，其他运行数据仍然走持久化 volume

容器级环境变量示例（`.env.prod`，只放镜像/端口/时区）：

```bash
LH_IMAGE=ghcr.io/yurika0211/luckyharness:latest
LH_PORT=9090
TZ=Asia/Shanghai
```

### 3. 启动

**开发环境：本地构建镜像**

```bash
docker compose up -d --build
```

开发环境使用仓库根目录的 [docker-compose.yml](docker-compose.yml)，会：

- 本地构建 `luckyharness:dev`
- 直接挂载 `./config.json`
- 将容器 `HOME` 固定到 `/var/lib/luckyharness`
- 把 LuckyHarness 的运行目录持久化到 Docker volume
- 默认同时启动 API 服务和 Telegram 网关

**生产环境：使用预构建镜像**

```bash
cp .env.prod.example .env.prod
docker compose -f docker-compose.prod.yml --env-file .env.prod up -d
```

如果还要一起启动 Telegram 网关：

```bash
docker compose -f docker-compose.prod.yml --env-file .env.prod --profile telegram up -d
```

生产环境使用 [docker-compose.prod.yml](docker-compose.prod.yml)，会：

- 使用 `ghcr.io/yurika0211/luckyharness:latest`（可通过 `LH_IMAGE` 覆盖）
- 直接只读挂载 `./config.json`
- 复用同一个持久化 volume 给 API 服务和 Telegram 网关

**纯 Docker：**

```bash
docker run -d \
  --name luckyharness \
  --entrypoint luckyharness \
  -e HOME=/var/lib/luckyharness \
  -e TZ=Asia/Shanghai \
  -p 9090:9090 \
  -v lh-home:/var/lib/luckyharness \
  -v "$(pwd)/config.json:/var/lib/luckyharness/config.json:ro" \
  ghcr.io/yurika0211/luckyharness:latest \
  serve
```

### 4. 验证

```bash
# 健康检查
curl http://localhost:9090/api/v1/health

# 同步聊天
curl -X POST http://localhost:9090/api/v1/chat/sync \
  -H "Content-Type: application/json" \
  -d '{"message": "Hello!"}'

# 流式聊天
curl -N -X POST http://localhost:9090/api/v1/chat \
  -H "Content-Type: application/json" \
  -d '{"message": "Hello!", "stream": true}'
```

### 5. 自定义 SOUL

挂载自定义人格文件：

```bash
# 准备 SOUL.md
cat > ./SOUL.md << 'EOF'
# SOUL

You are a helpful coding assistant.
Answer concisely in the user's language.
EOF

# 若 config.json 中的 soul_path 指向 /var/lib/luckyharness/SOUL.md
# 则在 compose 的 volumes 下增加:
#   - ./SOUL.md:/var/lib/luckyharness/SOUL.md:ro
```

### 6. 持久化目录结构

```
/var/lib/luckyharness/
├── config.json      # 运行时配置（自动生成）
├── SOUL.md          # 人格定义
├── sessions/        # 会话持久化
├── memory/          # 记忆存储
├── skills/          # Skill 插件
├── rag/             # RAG 知识库
├── logs/            # 日志
├── tokens/          # provider token 缓存
├── mission.md       # cron / mission 存储
└── knowledge/       # 最终答案归档
```

### 7. 常用运维

```bash
# 查看日志
docker compose logs -f luckyharness

# 重启
docker compose restart

# 开发环境重建
docker compose up -d --build

# 生产环境更新镜像
docker compose -f docker-compose.prod.yml --env-file .env.prod pull
docker compose -f docker-compose.prod.yml --env-file .env.prod up -d

# 进入容器调试
docker compose exec luckyharness sh

# 清理
docker compose down -v  # ⚠️ 删除数据卷
```

### 容器环境变量参考

| 变量 | 必填 | 默认值 | 说明 |
|------|------|--------|------|
| `LH_IMAGE` | 生产可选 | `ghcr.io/yurika0211/luckyharness:latest` | 生产镜像 |
| `LH_PORT` | ❌ | `9090` | 宿主机映射端口 |
| `TZ` | ❌ | `Asia/Shanghai` | 容器时区 |

### config.json 负责的业务配置

- provider / api_key / api_base / model
- server.addr / server.api_keys / server.log_level
- msg_gateway.telegram.token / proxy / timeout
- web_search 相关配置
- soul_path / rag / memory / agent loop 等其余 LuckyHarness 运行时参数

## 快速开始

```bash
# 安装
go install github.com/yurika0211/luckyharness/cmd/lh@latest

# 初始化
lh init

# 配置 Provider
lh config set provider openai
lh config set api_key sk-xxx

# 开始对话
lh chat

# 指定 SOUL
lh chat --soul ./SOUL.md

# 查看可用模型
lh models

# 交互模式切换模型
/model gpt-4o
/models
```

### 本地接入 Telegram（含代理）

如果你是源码运行（`go run ./cmd/lh ...`），推荐按下面流程配置：

```bash
# 可选：把数据目录放到项目内，避免 ~/.luckyharness 不可写
export HOME="$PWD/.lh-home"
mkdir -p "$HOME"

# 初始化与 LLM 配置
go run ./cmd/lh init
go run ./cmd/lh config set provider openai
go run ./cmd/lh config set api_key sk-xxx
go run ./cmd/lh config set model gpt-4o
go run ./cmd/lh config set api_base https://api.openai.com/v1
go run ./cmd/lh config set msg_gateway.platform telegram
go run ./cmd/lh config set msg_gateway.telegram.token 123456:ABC-DEF
go run ./cmd/lh config set msg_gateway.telegram.proxy http://127.0.0.1:7897
go run ./cmd/lh config get api_base
```

`api_base` 存在配置文件中：
- 默认路径：`~/.luckyharness/config.json`
- 若设置 `HOME="$PWD/.lh-home"`：`./.lh-home/.luckyharness/config.json`

先验证 Telegram API 连通性（示例使用本地代理 `127.0.0.1:7897`）：

```bash
export TG_TOKEN="123456:ABC-DEF"
curl -x http://127.0.0.1:7897 -m 20 "https://api.telegram.org/bot${TG_TOKEN}/getMe"
```

返回 `{"ok":true,...}` 后再启动网关。若你已把 `msg_gateway.telegram.proxy` 写入 `config.json`，则无需再 `export HTTPS_PROXY/HTTP_PROXY`：

```bash
export HTTPS_PROXY=http://127.0.0.1:7897
export HTTP_PROXY=http://127.0.0.1:7897
export NO_PROXY=127.0.0.1,localhost
go run ./cmd/lh msg-gateway start --platform telegram --token "$TG_TOKEN" --api-addr=127.0.0.1:19090
```

常见问题：

- `flag needs an argument: --api-addr`：命令被换行拆断，建议使用 `--api-addr=127.0.0.1:19090`。
- `Conflict: terminated by other getUpdates request`：同一 Bot 有多个实例同时在轮询，关闭其他实例，仅保留一个进程。
- `Connection timed out`（`api.telegram.org`）：当前网络不可达，需配置可用代理后重试。
- Token 泄露后请立刻到 `@BotFather` 重新生成新 Token。

## v0.3.0 新特性

### Provider 自动降级链

在 `config.yaml` 中配置降级链，当主 Provider 失败时自动切换：

```yaml
provider: openai
api_key: sk-xxx
model: gpt-4o
fallbacks:
  - provider: anthropic
    api_key: sk-ant-xxx
    model: claude-sonnet-4-20250514
  - provider: ollama
    model: llama3
```

降级链行为：
- 连续 3 次失败后自动降级到下一个 Provider
- 冷却期 5 分钟后自动恢复
- 成功调用后自动切回更高优先级的 Provider
- 支持自定义切换回调

### 新 Provider 支持

| Provider | 说明 | 认证方式 |
|----------|------|----------|
| OpenAI | GPT-4o / GPT-4o Mini / GPT-3.5 | API Key |
| Anthropic | Claude Sonnet 4 / Claude 3.5 Sonnet / Claude 3 Haiku | API Key (x-api-key header) |
| Ollama | Llama 3 / Mistral / Qwen 2 (本地) | 无需认证 |
| OpenRouter | 聚合多模型 (OpenAI 格式) | API Key |

### Model Catalog

内置 17 个模型信息，支持按 Provider / 能力筛选：

```go
catalog := provider.NewModelCatalog()
catalog.ListByProvider("anthropic")
catalog.FindByCapability("vision")
catalog.ResolveProvider("gpt-4o") // → "openai"
```

### OAuth Token 生命周期

Token 自动管理：存储、过期检测、刷新、脱敏列表。

```go
ts, _ := provider.NewTokenStore("~/.luckyharness/tokens")
ts.Set(&provider.TokenEntry{Provider: "openai", AccessToken: "sk-xxx", ExpiresAt: ...})
ts.RefreshIfNeeded("openai", refreshTokenFn)
```

## 架构

```
┌─────────────────────────────────────────────┐
│                  CLI (Cobra)                 │
├─────────┬──────────┬──────────┬─────────────┤
│  Config │  SOUL    │ Profile  │   Memory    │
├─────────┴──────────┴──────────┴─────────────┤
│              Agent Loop                      │
│  ┌─────────┐  ┌──────────┐  ┌────────────┐ │
│  │ Reason  │→ │ Act      │→ │ Observe    │ │
│  └─────────┘  └──────────┘  └────────────┘ │
├─────────────────────────────────────────────┤
│           Provider Resolution               │
│  OpenAI │ Anthropic │ Ollama │ OpenRouter   │
├─────────────────────────────────────────────┤
│           Tool / Skill System               │
│  Built-in │ MCP │ Plugins │ Sub-agents     │
├─────────────────────────────────────────────┤
│           Messaging Gateway                 │
│  Telegram │ Discord │ Slack │ WeChat        │
└─────────────────────────────────────────────┘
```

## 开发

```bash
go test ./...
go build -o lh ./cmd/lh
```

## License

MIT
