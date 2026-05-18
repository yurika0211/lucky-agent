# LuckyHarness

LuckyHarness 是一个面向真实部署场景的 Go 版 AI Agent 运行时。它不是单纯的聊天壳子，而是把 Agent 的核心能力、HTTP 服务能力、消息网关能力和配置体系放在同一套运行时里，让你可以从本地调试一路走到线上机器人部署，而不用中途换架构。

如果要用一句话描述它：LuckyHarness 想做的是一个“可开发、可接入、可部署”的 Agent 基础设施。

## 品牌定位

- Bot First：先把 Agent 跑起来，再把它接到 CLI、API、Telegram 等入口
- Config First：运行时行为以 `config.json` 为中心，而不是散落在各种临时命令里
- Deploy First：源码运行、开发环境 Docker、生产环境 Docker 都是正式支持路径
- Runtime First：SOUL、工具、记忆、RAG、模型路由、消息网关都围绕统一运行时组织

## 核心能力

- `lh chat`：本地调试对话、单轮验证、REPL 交互
- `lh serve`：启动 HTTP API 服务，适合内网接入、二次开发、线上部署
- `lh msg-gateway start --platform telegram`：启动 Telegram 消息网关
- `lh msg-gateway start --platform qqofficial`：启动 QQ 官方机器人消息网关
- SOUL 人格与提示词体系
- 内置记忆、RAG 检索与知识注入能力
- 多 Provider 接入，以及重试、限流、路由等治理能力
- 面向容器的部署结构，支持持久化 HOME 与配置挂载

## 运行模型

当前仓库最核心的三个真实入口是：

| 入口 | 作用 | 典型场景 |
|---|---|---|
| `lh chat` | 本地调试聊天 / REPL | 调 prompt、测工具、快速验证 |
| `lh serve` | 启动 HTTP API 服务 | 本地联调、服务接入、线上 API |
| `lh msg-gateway start --platform telegram` | 启动 Telegram 网关 | Telegram 机器人部署、消息收发 |
| `lh msg-gateway start --platform qqofficial` | 启动 QQ 官方机器人网关 | QQ 官方机器人部署、消息收发 |

服务健康检查接口：

```text
GET /api/v1/health
```

## 配置约定

运行时配置默认从这里加载：

```text
${HOME}/.luckyharness/config.json
```

这件事对所有部署方式都很重要：

- 在本机运行时，它会从当前用户的 home 目录读取配置
- 在容器里运行时，应该显式设置 `HOME=/var/lib/luckyharness` 或别的持久化目录
- 如果容器里的 `HOME` 不对，LuckyHarness 就不会读到你以为它会读到的那份配置

推荐先执行初始化命令：

```bash
go run ./cmd/lh init
```

它会初始化 `~/.luckyharness` 运行目录，创建默认 `config.json`、`SOUL.md` 以及运行期会用到的目录骨架。

最小配置示例：

```json
{
  "provider": "openai",
  "api_key": "sk-your-api-key",
  "api_base": "https://api.openai.com/v1",
  "model": "gpt-5.4-mini",
  "server": {
    "addr": "127.0.0.1:9090",
    "enable_cors": true,
    "cors_origins": ["*"],
    "rate_limit": 60,
    "log_level": "info",
    "log_format": "text"
  },
  "msg_gateway": {
    "platform": "telegram",
    "api_addr": "127.0.0.1:9090",
    "telegram": {
      "token": "",
      "proxy": ""
    },
    "qqofficial": {
      "app_id": "",
      "app_secret": "",
      "sandbox": true
    }
  }
}
```

## 快速开始

### 1. 初始化运行目录

```bash
go run ./cmd/lh init
```

初始化后，默认目录结构大致如下：

```text
~/.luckyharness/
├── config.json
├── SOUL.md
├── mission.md
├── sessions/
├── memory/
│   ├── 00_Index/
│   ├── 10_Profile/
│   ├── 20_Projects/
│   ├── 30_Sessions/
│   ├── 40_Decisions/
│   ├── 50_Facts/
│   ├── 60_Rules/
│   ├── 70_Trajectories/
│   └── 90_Archive/
├── logs/
├── skills/
├── tokens/
├── rag/
├── workspace/
│   └── HEARTBEAT.md
├── knowledge/
│   └── final_answers/
├── runtime/
├── data/
│   └── telegram/
└── description/
    └── LUCKYHARNESS_AGENT_MANUAL.md
```

然后重点修改这些字段：

- `provider`
- `api_key`
- `api_base`
- `model`
- `server.addr`
- `msg_gateway.telegram.token`（如果你要接 Telegram）
- `msg_gateway.qqofficial.app_id`（如果你要接 QQ 官方机器人）
- `msg_gateway.qqofficial.app_secret`（如果你要接 QQ 官方机器人）

也可以用命令直接修改，例如：

```bash
go run ./cmd/lh config set api_key sk-your-api-key
go run ./cmd/lh config set provider openai
go run ./cmd/lh config set model gpt-5.4-mini
```

### 2. 启动本地对话调试

```bash
go run ./cmd/lh chat "Hello"
```

或者进入 REPL：

```bash
go run ./cmd/lh chat
```

### 3. 启动 API 服务

```bash
go run ./cmd/lh serve --addr 127.0.0.1:9090
```

### 4. 健康检查

```bash
curl http://127.0.0.1:9090/api/v1/health
```

## 部署说明

这个仓库当前比较适合从三个角度来部署：

1. 源码运行部署
2. 开发环境 Docker 部署
3. 生产环境 Docker 部署

### A. 源码运行部署

适合场景：

- 正在开发功能
- 想直接从源码调试 prompt / tool / agent loop
- 想排查真实运行路径，而不是只看容器表现

#### 前置要求

- Go 1.25+
- 有可写的 home 目录
- 已准备好 `${HOME}/.luckyharness/config.json`

#### 以源码方式启动 API

```bash
go run ./cmd/lh init
go run ./cmd/lh serve --addr 127.0.0.1:9090
```

#### 以源码方式启动 Telegram 网关

```bash
go run ./cmd/lh msg-gateway start --platform telegram
```

#### 以源码方式启动 QQ 官方机器人网关

```bash
go run ./cmd/lh msg-gateway start --platform qqofficial
```

如果你想在启动时临时覆盖 QQ 凭证，也可以直接传 CLI 参数：

```bash
go run ./cmd/lh msg-gateway start --platform qqofficial \
  --qq-appid your-app-id \
  --qq-appsecret your-app-secret \
  --qq-sandbox
```

如果你不想污染自己真实的 home 目录，推荐把运行目录隔离到仓库内：

```bash
mkdir -p .lh-home
HOME="$PWD/.lh-home" go run ./cmd/lh serve --addr 127.0.0.1:9090
```

Telegram 也可以这样启动：

```bash
HOME="$PWD/.lh-home" go run ./cmd/lh msg-gateway start --platform telegram
```

QQ 官方机器人也可以这样启动：

```bash
HOME="$PWD/.lh-home" go run ./cmd/lh msg-gateway start --platform qqofficial
```

这是本地开发里最稳的一种方式，因为配置和运行数据都落在项目目录里，便于复现和清理。

### B. 开发环境 Docker 部署

适合场景：

- 希望开发环境更接近容器运行方式
- 但镜像仍然基于你当前本地源码构建
- 想一边改代码，一边验证 Compose 侧的运行结构

仓库已经提供开发用 Compose：

- `docker-compose.yml`

这套开发 Compose 的特点是：

- 从本地 `Dockerfile` 构建
- 使用镜像标签 `luckyharness:dev`
- API 服务显式通过 `command: ["serve"]` 启动
- 可以同时带起 Telegram 辅助服务
- 按源码约定，运行时配置应该位于 `/var/lib/luckyharness/.luckyharness/config.json`
- 显式设置 `HOME=/var/lib/luckyharness`
- named volume `lh-home` 持久化整个 `HOME`
- 宿主机 `./config.json` 挂载到 `/var/lib/luckyharness/.luckyharness/config.json`

#### 先准备宿主机 `./config.json`

这里的 `./config.json` 指的是：

- 你执行 `docker compose` 命令时所在目录里的 `config.json`
- 在这个仓库里，通常就是仓库根目录下的 `config.json`

如果仓库根目录下还没有这个文件，推荐这样准备：

```bash
go run ./cmd/lh init
cp ~/.luckyharness/config.json ./config.json
```

然后修改 `./config.json` 里的关键字段，例如：

- `provider`
- `api_key`
- `api_base`
- `model`
- `server.addr`
- `msg_gateway.telegram.token`
- `msg_gateway.qqofficial.app_id`
- `msg_gateway.qqofficial.app_secret`

#### 只启动 API 服务

```bash
docker compose up -d luckyharness
```

#### 同时启动 API 和 Telegram

```bash
docker compose up -d
```

#### 停止

```bash
docker compose down
```

#### 开发环境 Docker 说明

- 启动前请确认容器内最终可读到的是 `${HOME}/.luckyharness/config.json`
- `./config.json` 是宿主机文件，不是容器内文件
- 你通常应该修改宿主机仓库根目录下的 `./config.json`，而不是进入容器里改
- 如果需要让宿主机外部访问 API，请把 `server.addr` 设为 `0.0.0.0:9090`
- 健康检查走的是容器内部的 `http://127.0.0.1:9090/api/v1/health`
- Telegram 容器依赖 API 容器健康检查通过后再启动，便于整体运维

### C. 生产环境 Docker 部署

适合场景：

- 部署到 VPS、云主机或长期运行节点
- 希望直接使用预构建镜像
- 希望把开发态和生产态分开

仓库已经提供生产用 Compose：

- [docker-compose.prod.yml](/media/shiokou/DevRepo44/DevHub/Projects/2026-myapp/luckyharness/docker-compose.prod.yml)

默认镜像：

```text
ghcr.io/yurika0211/luckyharness:latest
```

#### 启动生产 API

```bash
docker compose -f docker-compose.prod.yml up -d luckyharness
```

#### 启动生产 API + Telegram

```bash
docker compose -f docker-compose.prod.yml --profile telegram up -d
```

#### 停止

```bash
docker compose -f docker-compose.prod.yml down
```

#### 生产环境 Docker 说明

- 生产环境里也应保证配置最终落在 `${HOME}/.luckyharness/config.json`
- 运行时 HOME 仍然是 `/var/lib/luckyharness`
- `docker-compose.prod.yml` 会把宿主机 `./config.json` 挂到 `/var/lib/luckyharness/.luckyharness/config.json:ro`
- 生产环境推荐在宿主机先维护好这份 `./config.json`，再启动容器
- 如果要对外暴露 API，请确认 `server.addr` 是 `0.0.0.0:9090`
- Telegram 服务被放在 `telegram` profile 后面，是否启用可以按需决定

## 从镜像角度理解部署

如果你不想直接使用 Compose，也可以直接从镜像层面运行。

这里的 `"$PWD/config.json"` 同样指宿主机当前目录下的 `config.json`，通常建议放在仓库根目录，或你专门的部署目录里。

### 构建镜像

```bash
docker build -t luckyharness:local .
```

### 运行 API 容器

```bash
docker run -d \
  --name luckyharness \
  -p 9090:9090 \
  -e HOME=/var/lib/luckyharness \
  -v "$PWD/config.json:/var/lib/luckyharness/.luckyharness/config.json:ro" \
  luckyharness:local
```

### 运行 Telegram 容器

```bash
docker run -d \
  --name luckyharness-telegram \
  -e HOME=/var/lib/luckyharness \
  -v "$PWD/config.json:/var/lib/luckyharness/.luckyharness/config.json:ro" \
  luckyharness:local \
  msg-gateway start --platform telegram
```

### 运行 QQ 官方机器人容器

```bash
docker run -d \
  --name luckyharness-qqofficial \
  -e HOME=/var/lib/luckyharness \
  -v "$PWD/config.json:/var/lib/luckyharness/.luckyharness/config.json:ro" \
  luckyharness:local \
  msg-gateway start --platform qqofficial
```

镜像入口脚本也支持通过环境变量覆盖部分配置，例如：

- `LH_PROVIDER`
- `LH_API_KEY`
- `LH_API_BASE`
- `LH_MODEL`
- `LH_API_ADDR`
- `LH_TELEGRAM_TOKEN`
- `LH_TELEGRAM_PROXY`

但这个仓库更推荐的方式仍然是：业务配置以 `config.json` 为主，环境变量只作为局部覆盖手段。

## 消息网关部署说明

当前 CLI 明确暴露出来的消息网关启动平台有两个：

- `telegram`
- `qqofficial`

### Telegram

启动前请先确认：

- `msg_gateway.telegram.token` is set
- `msg_gateway.telegram.proxy` 正确，或者明确留空
- 没有其他 Bot 进程在同时轮询同一个 token

常用启动命令：

```bash
lh msg-gateway start --platform telegram
```

常见问题：

- `Conflict: terminated by other getUpdates request`
  一般表示另一个 Telegram 进程已经在使用同一个 token 轮询。
- `proxyconnect tcp ... connection refused`
  说明当前配置的 Telegram 代理不可达，或者已经失效。
- Bot 已启动，但外部访问不到 API
  大概率是 `server.addr` 还绑定在 `127.0.0.1:9090`。

### QQ 官方机器人

启动前请先确认：

- `msg_gateway.qqofficial.app_id` 已配置
- `msg_gateway.qqofficial.app_secret` 已配置
- `msg_gateway.qqofficial.sandbox` 与你的 QQ 机器人环境一致
- 如果需要限制入口，补充 `msg_gateway.qqofficial.allowed_chats` 或 `msg_gateway.qqofficial.allowed_users`

常用启动命令：

```bash
lh msg-gateway start --platform qqofficial
```

也支持直接传入启动参数：

```bash
lh msg-gateway start --platform qqofficial \
  --qq-appid your-app-id \
  --qq-appsecret your-app-secret \
  --qq-sandbox
```

当前 QQ 渠道内置的常用命令包括：

- `/help`
- `/chat <消息>`
- `/model [模型]`
- `/soul`
- `/tools`
- `/skills`
- `/cron`
- `/metrics`
- `/health`
- `/status`
- `/new`
- `/reset`
- `/stop`
- `/restart`
- `/session`
- `/history`

## 常用命令

```bash
# 初始化运行目录
lh init

# 查看当前配置
lh config list

# 读取单个配置项
lh config get provider

# 修改单个配置项
lh config set model gpt-5.4-mini

# 本地聊天
lh chat

# 单轮聊天
lh chat "Summarize this repository"

# 启动 HTTP API
lh serve

# 启动 Telegram 网关
lh msg-gateway start --platform telegram

# 启动 QQ 官方机器人网关
lh msg-gateway start --platform qqofficial

# 将目录写入 RAG
lh rag index ./docs

# 查询 RAG
lh rag search "deployment"
```

## 项目结构

```text
cmd/lh                  CLI 入口
internal/cli/lhcmd      命令注册与执行
internal/server         HTTP API 服务
internal/gateway        消息网关体系
internal/agent          Agent 核心运行时
internal/config         配置加载与持久化
docker-compose.yml      开发环境 Docker 部署
docker-compose.prod.yml 生产环境 Docker 部署
config.example.json     配置模板
```

## 运维建议

- 运行时行为尽量以 `config.json` 为准
- 本地调试时，推荐用 `HOME="$PWD/.lh-home"` 做隔离
- 开发阶段优先用开发 Compose，便于验证本地源码构建
- 生产阶段优先用生产 Compose，便于使用预构建镜像
- Docker 运行异常时，优先检查 `HOME`、配置挂载路径和 `server.addr`
- 如果容器启动后只打印帮助信息，先确认它是否真的在执行 `serve`

## 总结

LuckyHarness 更像一套可落地的 Agent 运行时底座，而不是一个只适合演示的聊天项目。它的价值在于：你可以先在本地用源码调试，再用开发 Docker 验证容器化运行，最后切到生产 Docker 做稳定部署，整个过程始终围绕同一套 CLI、同一份配置和同一个 Agent 核心展开。
