<div align="center">
  <img src="public/brand.png" alt="LuckyAgent Brand" width="520">
</div>

# LuckyAgent

LuckyAgent 是一个用 Go 构建的长期运行 Agent runtime。它把 Agent loop、模型路由、工具和技能、长期记忆、RAG、HTTP API 以及消息网关放在同一个运行时里，支持从本地调试逐步走向容器和线上部署。

它不是只提供一个聊天窗口，而是提供一套可以观察、配置和持续运行的 Agent 基础设施。

<p align="center">
  <a href="https://github.com/yurika0211/lucky-agent">
    <img src="https://img.shields.io/badge/GitHub-项目主页-24292f?style=for-the-badge&amp;logo=github&amp;logoColor=white" alt="GitHub project">
  </a>
  <a href="public/Qgroup.png">
    <img src="https://img.shields.io/badge/QQ群-加入交流群-12b7f5?style=for-the-badge&amp;logo=tencentqq&amp;logoColor=white" alt="QQ group">
  </a>
  <a href="https://yurika0211.github.io/lucky-agent/#config">
    <img src="https://img.shields.io/badge/配置说明-Config-237452?style=for-the-badge&amp;logo=readthedocs&amp;logoColor=white" alt="Configuration guide">
  </a>
  <a href="https://yurika0211.github.io/lucky-agent/">
    <img src="https://img.shields.io/badge/部署知识库-Deployment-2d6f93?style=for-the-badge&amp;logo=docker&amp;logoColor=white" alt="Deployment knowledge base">
  </a>
  <a href="docs/API.md">
    <img src="https://img.shields.io/badge/API-文档-9a5f24?style=for-the-badge&amp;logo=swagger&amp;logoColor=white" alt="API documentation">
  </a>
</p>

## 界面预览

<p align="center">
  <img src="public/GUI-chat.png" alt="LuckyAgent empty chat workspace" width="49%">
  <img src="public/GUI-skills.png" alt="LuckyAgent skills workspace" width="49%">
</p>
<p align="center">
  <img src="public/GUI-memory-graph.png" alt="LuckyAgent memory graph workspace" width="49%">
  <img src="public/GUI-gateways.png" alt="LuckyAgent messaging gateways workspace" width="49%">
</p>

## Embed SDK（进程内）

把 LuckyAgent 当作 Go library 嵌入宿主进程，无需先起 `lh serve`：

```bash
go run ./examples/embed_minimal
```

详情见 [`sdk/README.md`](sdk/README.md)。稳定面（v0）：`New` / `Chat*`（含多模态 Input） / Sessions（含 Compact） / Memory / RAG / Tools / Models / Skills。

## 核心能力

- **统一运行时**：CLI、HTTP API 和消息网关共享同一个 Agent 核心。
- **长期记忆**：用 Obsidian-compatible Markdown vault 保存用户画像、事实、规则、决策和会话轨迹。
- **RAG 知识库**：索引项目文档、个人笔记和运维资料，并在 Agent 运行时检索。
- **工具与技能**：支持内置工具、技能加载、技能安装、MCP 和 OpenCLI。
- **多平台入口**：支持 Telegram、QQ Official、NapCat、飞书、微信和 OpenClaw Weixin。
- **外部客户端**：会话、工具轨迹、Memory Graph、技能和网关通过 HTTP API 提供。手机工作区在独立仓库 `luckyagent-android`。
- **多 Agent 能力**：提供协作、委派和实验/benchmark 相关运行能力。

## 快速开始

### 环境要求

- Go 1.25+
- 一个可用的模型服务和对应 API 凭证

### 从源码运行

初始化运行目录：

```bash
go run ./cmd/la init
```

配置一个 OpenAI-compatible provider：

```bash
go run ./cmd/la config set provider openai
go run ./cmd/la config set api_base https://api.openai.com/v1
go run ./cmd/la config set model gpt-5.4-mini
go run ./cmd/la credential add openai-main --kind llm_api_key
go run ./cmd/la config set models.endpoints.chat.credential_ref openai-main
```

凭据值通过 TTY 隐藏输入，保存在 `~/.luckyagent/runtime/credentials.db` 中；配置文件只保存引用。`credential list` 只显示凭据元数据，`credential remove <id>` 删除凭据。模型调用 `request_credential` 时，客户端弹出掩码表单，明文不进入模型上下文。

需要调用带凭据的外部 HTTP API 时，可在 `request_credential` 之后调用 `create_ephemeral_credential_tool`。它会创建一个绑定当前会话、带 TTL 的临时工具；工具只持有 `credential_ref`，明文只在请求执行期间注入认证头，不写入会话或后台任务。详见 [`docs/tools/basic/create_ephemeral_credential_tool.md`](docs/tools/basic/create_ephemeral_credential_tool.md)。

需要让 CLI 或 shell 命令使用凭据时，可调用 `create_ephemeral_credential_terminal_tool`。它只在一次受控子进程中注入环境变量，secret 不进入命令参数、ShellContext、工具结果或日志，并且创建和执行都需要审批。

如需在回答末尾显示工具来源引用，可开启自动引用尾注（默认关闭）：

```bash
go run ./cmd/la config set agent.enable_citations true
```

开始一次本地对话：

```bash
go run ./cmd/la chat "Hello, LuckyAgent"
```

启动 HTTP API：

```bash
go run ./cmd/la serve --addr 127.0.0.1:9090
```

健康检查：

```bash
curl http://127.0.0.1:9090/api/v1/health/live
```

默认运行配置位于：

```text
${HOME}/.luckyagent/config.json
```

容器或 systemd 部署时，请明确设置 `HOME`，并确保这个目录及其下的 `config.json`、sessions、memory、RAG 和 runtime 数据可持久化。

## 部署与安装

完整部署知识库已经独立到网页，包含：

- Linux、macOS、Windows 发布包安装
- 源码运行和运行目录规划
- 开发 Docker Compose
- 生产 Docker Compose 和 GHCR 镜像
- 镜像直跑、systemd 和长期运行
- Telegram、QQ、NapCat、飞书和微信网关
- 配置、健康检查、网络、鉴权和常见排障

请优先阅读：

[打开 LuckyAgent 部署知识库](https://yurika0211.github.io/lucky-agent/)

仓库中也提供了两套 Compose：

```bash
# 开发环境：从当前源码构建
docker compose up -d --build luckyagent

# 生产环境：使用预构建镜像
docker compose -f docker-compose.prod.yml up -d luckyagent
```

生产环境启用消息网关时，使用对应 Compose profile：

```bash
docker compose -f docker-compose.prod.yml --profile telegram up -d
docker compose -f docker-compose.prod.yml --profile napcat up -d
```

不要把 README 中的简短命令当作完整生产配置。生产部署前应按照部署知识库确认 `HOME`、配置挂载、`server.addr`、端口、防火墙、访问白名单和网关 token。

## 常用命令

安装发行版后使用 `lh`；从源码运行时，将 `lh` 替换为 `go run ./cmd/la`：

```bash
lh init
lh config list
lh config get provider
lh config set model gpt-5.4-mini
lh chat
lh chat "Summarize this repository"
lh serve
lh qr
lh msg-gateway start --platform telegram
lh msg-gateway start --platform qqofficial
lh msg-gateway start --platform napcat
lh rag index ./docs
lh rag search "deployment"
```

## 文档导航

- [部署知识库](https://yurika0211.github.io/lucky-agent/)
- [使用指南](docs/wiki/使用指南.md)：初始化、配置、CLI、API、网关和 Docker
- [特色功能](docs/wiki/特色功能.md)：记忆、RAG、工具、自动化和多 Agent
- [使用场景](docs/wiki/使用场景.md)：本地调试、知识库问答、机器人和团队 API
- [HTTP API](docs/API.md)
- [消息网关与服务接口的模块边界](docs/edge-modules.md)
- [独立工具模块与宿主适配](docs/tool-module.md)
- [Codex App Server 集成](docs/codex-app-server.md)
- [Grok agent 集成](docs/grok-agent.md)
- [记忆系统](docs/memory_system.md)
- [Graph RAG 快速开始](docs/GRAPH_RAG_QUICKSTART.md)
- [多 Agent 协作](docs/multi-agent/collaboration.md)
- [Benchmark：Hybrid 检索](docs/benchmarks/rag-hybrid-retrieval/REPORT.md)：精确标识符 Recall@1 `0.000 → 1.000`，语义召回无回退，含延迟代价与复现步骤

## Prompt 和运行数据

运行时数据默认保存在 `${HOME}/.luckyagent`。其中：

```text
~/.luckyagent/
├── config.json
├── sessions/
├── memory/
├── skills/
├── rag/
├── logs/
├── runtime/
├── workspace/
└── knowledge/
```

Agent 的行为 prompt 位于：

```text
~/.luckyagent/memory/prompts/
```

常见文件包括 `SOUL.md`、`AGENTS.md`、`core.md`、`tool_policy.md`、`skill_policy.md` 和 `memory_policy.md`。修改 prompt 后，后续请求会使用新的内容，通常不需要重新编译程序。

本地开发或测试时，可以隔离运行目录：

```bash
HOME="$PWD/.lh-home" go run ./cmd/la chat "Test the local runtime"
```

## 项目结构

```text
cmd/la                   CLI 入口
internal/agent           Agent 核心运行时和 Agent loop
internal/config          配置加载、默认值和运行目录
internal/memoryaestus     Aestus 记忆后端适配与资源生命周期
internal/memory          记忆 SDK 导入边界检查
internal/rag             RAG 索引、检索和持久化
internal/tool            独立工具模块与宿主适配的兼容入口
internal/toolapp         凭据存储适配；通用工具运行逻辑由独立模块提供
internal/server          API 构造兼容入口
internal/apiapp          API 业务与运行时组装
internal/gateway         消息平台兼容入口
internal/channelapp      网关业务与 Agent 接入
luckyagent-tools         公共工具、工具适配与能力接口（独立模块）
luckyagent-orchestrator  委派、多 Agent 编排、autonomy 与任务存储（独立模块）
luckyagent-gateway       平台协议与回复展示（独立模块）
luckyagent-api           HTTP/WS/gRPC、资源处理与重连存储（独立模块）
docker-compose.yml       开发环境 Compose
docker-compose.prod.yml  生产环境 Compose
config.example.json      配置模板
```

## 开发

运行 Go 测试：

```bash
go test ./...
```

如果需要修改 API、记忆、RAG、技能或消息网关，建议先阅读对应 package 和 `docs/` 下的专题文档，再运行相关 focused tests。

## License

[License](LICENSE)
