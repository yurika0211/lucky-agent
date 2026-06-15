# LuckyHarness

LuckyHarness 与多数的agent不同，它采用golang为开发语言，集成了API服务、TUI、GUI网关、多社交软件接入的能力，Luckyharness目前的特点主要在于两个方面，首先是它的记忆系统，另外一部分就是它的多agent编排能力。
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
- `lh msg-gateway start --platform napcat`：启动 NapCat / OneBot v11 反向 WebSocket QQ 网关
- `lh msg-gateway start --platform weixin`：启动个人微信消息网关（iLink Bot API）
- SOUL 人格与提示词体系
- 内置记忆、RAG 检索与知识注入能力
- 多 Provider 接入，以及重试、限流、路由等治理能力
- 可配置的网页内容抽取链，以及统一 `opencli` 工具入口
- 面向容器的部署结构，支持持久化 HOME 与配置挂载

## 运行模型

当前仓库最核心的三个真实入口是：

| 入口 | 作用 | 典型场景 |
|---|---|---|
| `lh chat` | 本地调试聊天 / REPL | 调 prompt、测工具、快速验证 |
| `lh serve` | 启动 HTTP API 服务 | 本地联调、服务接入、线上 API |
| `lh msg-gateway start --platform telegram` | 启动 Telegram 网关 | Telegram 机器人部署、消息收发 |
| `lh msg-gateway start --platform qqofficial` | 启动 QQ 官方机器人网关 | QQ 官方机器人部署、消息收发 |
| `lh msg-gateway start --platform napcat` | 启动 NapCat QQ 网关 | NapCat OneBot v11 反向 WebSocket 接入 |
| `lh msg-gateway start --platform weixin` | 启动个人微信网关 | 个人微信消息接入、文本收发 |

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
    },
    "napcat": {
      "listen_addr": "127.0.0.1:6701",
      "path": "/onebot/v11/ws",
      "access_token": "",
      "allowed_chats": [],
      "allowed_users": [],
      "remove_at": true,
      "group_trigger_mode": "mention"
    },
    "weixin": {
      "token": "",
      "account_id": "",
      "base_url": "https://ilinkai.weixin.qq.com",
      "dm_policy": "open",
      "group_policy": "disabled",
      "allowed_users": [],
      "group_allowed_users": [],
      "split_multiline_messages": false,
      "poll_timeout_ms": 35000,
      "send_chunk_delay_ms": 350
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
- `msg_gateway.napcat.listen_addr`（如果你要接 NapCat）
- `msg_gateway.napcat.path`（如果你要接 NapCat）
- `msg_gateway.weixin.token`（如果你要接个人微信 iLink Bot API）
- `msg_gateway.weixin.account_id`（如果你要接个人微信 iLink Bot API）
- `opencli.command` / `opencli.args` / `opencli.timeout_seconds`（如果你要自定义 OpenCLI 网页读取模板）

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
export LH_OPENCLI_ENABLED=true
export LH_OPENCLI_COMMAND=opencli
export LH_OPENCLI_ARGS='web,read,--url,{url},--stdout,true,--download-images,false,-f,md'
export LH_OPENCLI_TIMEOUT_SECONDS=20
export LH_OPENCLI_MAX_CHARS=50000
export LH_OPENCLI_FALLBACK_TO_WEB_FETCH=true
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

#### 以源码方式启动 NapCat QQ 网关

```bash
go run ./cmd/lh msg-gateway start --platform napcat
```

默认会监听：

```text
ws://127.0.0.1:6701/onebot/v11/ws
```

在 NapCat 的 OneBot v11 反向 WebSocket 配置里，把连接地址填成上面的地址即可。如果你要换端口或路径：

```bash
go run ./cmd/lh msg-gateway start --platform napcat \
  --napcat-listen 127.0.0.1:6701 \
  --napcat-path /onebot/v11/ws
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

NapCat 也可以这样启动：

```bash
HOME="$PWD/.lh-home" go run ./cmd/lh msg-gateway start --platform napcat
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
- `msg_gateway.napcat.listen_addr`
- `msg_gateway.napcat.path`

#### 只启动 API 服务

```bash
docker compose up -d luckyharness
```

#### 同时启动 API、Telegram 和 NapCat

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

#### 启动生产 API + NapCat

```bash
docker compose -f docker-compose.prod.yml --profile napcat up -d
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

### 运行 NapCat QQ 网关容器

```bash
docker run -d \
  --name luckyharness-napcat \
  -p 6701:6701 \
  -e HOME=/var/lib/luckyharness \
  -v "$PWD/config.json:/var/lib/luckyharness/.luckyharness/config.json:ro" \
  luckyharness:local \
  msg-gateway start --platform napcat --napcat-listen 0.0.0.0:6701
```

镜像入口脚本也支持通过环境变量覆盖部分配置，例如：

- `LH_PROVIDER`
- `LH_API_KEY`
- `LH_API_BASE`
- `LH_MODEL`
- `LH_API_ADDR`
- `LH_TELEGRAM_TOKEN`
- `LH_TELEGRAM_PROXY`
- `LH_NAPCAT_LISTEN_ADDR`
- `LH_NAPCAT_PATH`
- `LH_NAPCAT_ACCESS_TOKEN`

但这个仓库更推荐的方式仍然是：业务配置以 `config.json` 为主，环境变量只作为局部覆盖手段。

## 消息网关部署说明

当前 CLI 明确暴露出来的消息网关启动平台包括：

- `telegram`
- `qqofficial`
- `napcat`
- `weixin`
- `openclawweixin`

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

### NapCat QQ

LuckyHarness 的 NapCat 渠道使用 OneBot v11 反向 WebSocket。连接方向是：

```text
NapCat QQ 客户端  --->  LuckyHarness WebSocket 服务端
```

也就是说，LuckyHarness 先监听一个 WebSocket 地址，NapCat 再作为客户端主动连进来。这个模式不需要 QQ 官方机器人 AppID / AppSecret，直接复用 NapCat 登录的 QQ 账号。

#### 1. 准备条件

- NapCat 已经能正常登录目标 QQ 账号
- LuckyHarness 已经配置好可用的 LLM provider、`api_key`、`api_base` 和 `model`
- NapCat 和 LuckyHarness 能互相访问网络
- 如果跨机器部署，防火墙要放行 LuckyHarness 的 NapCat 监听端口，默认是 `6701`

本地源码运行时先初始化 LuckyHarness：

```bash
go run ./cmd/lh init
go run ./cmd/lh config set provider openai
go run ./cmd/lh config set api_key sk-your-api-key
go run ./cmd/lh config set api_base https://api.openai.com/v1
go run ./cmd/lh config set model gpt-5.4-mini
```

如果你已经安装了二进制命令，也可以把上面的 `go run ./cmd/lh` 换成 `lh` 或 `luckyharness`。

#### 2. 配置 LuckyHarness 的 NapCat 网关

推荐先用默认路径和端口：

```json
{
  "msg_gateway": {
    "platform": "napcat",
    "napcat": {
      "listen_addr": "127.0.0.1:6701",
      "path": "/onebot/v11/ws",
      "access_token": "",
      "allowed_chats": [],
      "allowed_users": [],
      "remove_at": true,
      "group_trigger_mode": "mention"
    }
  }
}
```

也可以用命令写入配置：

```bash
lh config set msg_gateway.platform napcat
lh config set msg_gateway.napcat.listen_addr 127.0.0.1:6701
lh config set msg_gateway.napcat.path /onebot/v11/ws
lh config set msg_gateway.napcat.group_trigger_mode mention
```

配置项含义：

- `listen_addr`：LuckyHarness 监听地址。NapCat 和 LuckyHarness 在同一台机器时用 `127.0.0.1:6701`；需要被其他机器或容器访问时用 `0.0.0.0:6701`
- `path`：WebSocket 路径，默认 `/onebot/v11/ws`
- `access_token`：可选访问令牌；设置后 NapCat 连接 URL 必须带同一个 token
- `allowed_chats`：允许响应的会话白名单。可以填 QQ 原始 ID，也可以填 `private:<QQ号>` / `group:<群号>`
- `allowed_users`：允许触发 Agent 的 QQ 用户 ID 白名单
- `remove_at`：群聊里移除 `@bot` 文本后再交给 Agent
- `group_trigger_mode`：群聊触发方式，`mention` 表示只响应 @bot 或回复 bot，`all` 表示群内所有消息都进入 Agent，`none` 表示不响应群聊

有公网或局域网暴露需求时建议设置 token：

```bash
lh config set msg_gateway.napcat.listen_addr 0.0.0.0:6701
lh config set msg_gateway.napcat.access_token your-strong-token
```

#### 3. 启动 LuckyHarness 网关

本地源码启动：

```bash
go run ./cmd/lh msg-gateway start --platform napcat
```

二进制启动：

```bash
lh msg-gateway start --platform napcat
```

如果只想临时覆盖监听地址、路径或 token，不写入配置文件：

```bash
lh msg-gateway start --platform napcat \
  --napcat-listen 0.0.0.0:6701 \
  --napcat-path /onebot/v11/ws \
  --napcat-access-token your-strong-token
```

启动成功后终端会看到类似日志：

```text
NapCat QQ 网关已启动，等待 NapCat 连接 ws://127.0.0.1:6701/onebot/v11/ws
```

#### 4. 在 NapCat 里添加反向 WebSocket

在 NapCat 管理界面中找到 OneBot v11 的 WebSocket 客户端 / 反向 WebSocket 配置。不同 NapCat 版本入口名称可能略有不同，但要点相同：

- 类型选择 WebSocket 客户端或反向 WebSocket
- URL 填 LuckyHarness 的监听地址
- 启用消息上报
- 保存并重连

本机部署时 URL：

```text
ws://127.0.0.1:6701/onebot/v11/ws
```

如果设置了 `access_token`，最稳的写法是在 URL 上带参数：

```text
ws://127.0.0.1:6701/onebot/v11/ws?access_token=your-strong-token
```

跨机器部署时，把 `127.0.0.1` 换成 LuckyHarness 所在机器的局域网 IP 或域名：

```text
ws://192.168.1.10:6701/onebot/v11/ws?access_token=your-strong-token
```

Docker 部署但 NapCat 跑在宿主机时，NapCat 仍然连接宿主机映射端口：

```text
ws://127.0.0.1:6701/onebot/v11/ws
```

如果 NapCat 也在同一个 Docker network 里，可以连接 LuckyHarness NapCat 服务名：

```text
ws://luckyharness-napcat:6701/onebot/v11/ws
```

#### 5. 测试绑定是否成功

LuckyHarness 终端看到连接日志即表示 NapCat 已连上：

```text
[napcat] reverse websocket connected from 127.0.0.1:xxxxx
```

然后用 QQ 测试：

- 私聊 bot：直接发送 `你好`
- 群聊默认模式：`@bot 你好`
- 群聊回复模式：回复 bot 的上一条消息
- 命令测试：发送 `/help` 或 `/status`

如果希望群里所有消息都进入 Agent：

```bash
lh config set msg_gateway.napcat.group_trigger_mode all
lh msg-gateway start --platform napcat
```

生产环境一般不建议长期使用 `all`，除非这个群就是专门给 Agent 用的。

#### 6. Docker Compose 部署

开发环境 compose 已经包含 `luckyharness-napcat` 服务。先准备 `config.json`：

```bash
cp config.example.json config.json
```

编辑 `config.json`，至少设置 provider 和 NapCat：

```json
{
  "provider": "openai",
  "api_key": "sk-your-api-key",
  "api_base": "https://api.openai.com/v1",
  "model": "gpt-5.4-mini",
  "msg_gateway": {
    "platform": "napcat",
    "napcat": {
      "listen_addr": "0.0.0.0:6701",
      "path": "/onebot/v11/ws",
      "access_token": "your-strong-token",
      "group_trigger_mode": "mention"
    }
  }
}
```

启动 API 和 NapCat 网关：

```bash
docker compose up -d --build luckyharness luckyharness-napcat
docker compose logs -f luckyharness-napcat
```

NapCat 连接地址：

```text
ws://127.0.0.1:6701/onebot/v11/ws?access_token=your-strong-token
```

如果端口被占用，可以改宿主机映射端口：

```bash
LH_NAPCAT_PORT=16701 docker compose up -d --build luckyharness luckyharness-napcat
```

此时 NapCat 连接：

```text
ws://127.0.0.1:16701/onebot/v11/ws?access_token=your-strong-token
```

#### 7. 生产 Compose 部署

生产 compose 使用 profile 管理 NapCat：

```bash
cp config.example.json config.json
# 编辑 config.json，设置 provider/api_key/model/msg_gateway.napcat
docker compose -f docker-compose.prod.yml --profile napcat up -d
docker compose -f docker-compose.prod.yml logs -f luckyharness-napcat
```

常用环境变量：

```bash
export LH_IMAGE=ghcr.io/yurika0211/luckyharness:latest
export LH_PORT=9090
export LH_NAPCAT_PORT=6701
docker compose -f docker-compose.prod.yml --profile napcat up -d
```

生产部署建议：

- `msg_gateway.napcat.listen_addr` 使用 `0.0.0.0:6701`
- 设置 `msg_gateway.napcat.access_token`
- 用反向代理或防火墙限制只有 NapCat 所在机器能访问 `6701`
- 用 `allowed_chats` 和 `allowed_users` 收窄触发范围
- 用 `docker compose logs -f luckyharness-napcat` 观察连接和处理错误

#### 8. systemd 部署

如果不用 Docker，可以把二进制放到服务器上用 systemd 托管。示例：

```ini
[Unit]
Description=LuckyHarness NapCat Gateway
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=luckyharness
WorkingDirectory=/opt/luckyharness
Environment=HOME=/var/lib/luckyharness
ExecStart=/usr/local/bin/luckyharness msg-gateway start --platform napcat --napcat-listen 0.0.0.0:6701
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

启用：

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now luckyharness-napcat
sudo journalctl -u luckyharness-napcat -f
```

#### 9. 常见问题

- NapCat 一直显示连接失败：确认 LuckyHarness 终端已经启动，NapCat URL 的 IP、端口、路径完全一致
- LuckyHarness 没有 connected 日志：确认 `listen_addr` 是否绑定到 NapCat 可访问的地址；跨机器不要用 `127.0.0.1`
- 返回 401 或连接后立刻断开：确认 `access_token` 一致，或先清空 token 测试网络链路
- 私聊能用、群聊没反应：默认只响应 @bot 或回复 bot；要全量响应请设置 `group_trigger_mode=all`
- 群里 @ 了仍没反应：确认 NapCat 上报的 `self_id` 是当前 bot QQ，且消息里确实包含对 bot 的 at
- 能收到消息但发不出回复：确认 NapCat 反向 WebSocket 仍保持连接，LuckyHarness 日志里没有 `reverse websocket is not connected`
- 只想让指定群可用：设置 `allowed_chats`，例如 `group:123456789` 或直接 `123456789`
- 只想让指定用户触发：设置 `allowed_users` 为 QQ 用户 ID 列表

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

# 启动 NapCat QQ 网关
lh msg-gateway start --platform napcat

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
## Weixin 网关指南

LuckyHarness 现在支持一个最小可用版个人微信渠道，平台名是 `weixin`。这个实现参考 Hermes 的个人微信接入方式，走的是腾讯 iLink Bot API，不是企业微信，也不是桌面端协议注入。

最少需要配置这几个字段：
```json
{
  "msg_gateway": {
    "platform": "weixin",
    "weixin": {
      "token": "your-ilink-token",
      "account_id": "your-account-id",
      "base_url": "https://ilinkai.weixin.qq.com",
      "dm_policy": "open",
      "group_policy": "disabled",
      "allowed_users": [],
      "group_allowed_users": [],
      "split_multiline_messages": false,
      "poll_timeout_ms": 35000,
      "send_chunk_delay_ms": 350
    }
  }
}
```

字段说明：
- `msg_gateway.weixin.token`：iLink Bot API 令牌
- `msg_gateway.weixin.account_id`：对应微信账号的 account id
- `msg_gateway.weixin.base_url`：默认 `https://ilinkai.weixin.qq.com`
- `msg_gateway.weixin.dm_policy`：私聊入口策略，可选 `open` / `disabled` / `allowlist`
- `msg_gateway.weixin.group_policy`：群入口策略，可选 `disabled` / `open` / `allowlist`
- `msg_gateway.weixin.allowed_users`：私聊白名单
- `msg_gateway.weixin.group_allowed_users`：群白名单

源码启动：
```bash
go run ./cmd/lh msg-gateway start --platform weixin
```

如果你还没有 `token` 和 `account_id`，可以先运行二维码登录辅助命令：
```bash
go run ./cmd/lh msg-gateway weixin-login
```

这个命令会请求 iLink 登录二维码，轮询扫码结果，并在登录成功后自动写回：
- `msg_gateway.weixin.token`
- `msg_gateway.weixin.account_id`
- `msg_gateway.weixin.base_url`（如果服务端返回了新地址）

如果只想打印结果，不写 `config.json`：
```bash
go run ./cmd/lh msg-gateway weixin-login --no-save
```

如果你在仓库里做本地开发，推荐显式指定项目内 HOME：
```bash
HOME="$PWD/.lh-home" go run ./cmd/lh msg-gateway start --platform weixin
```

如果你当前 Windows PowerShell 里 `go` 不在 PATH，可以这样：
```powershell
$env:PATH='G:\SoftRepo\DevTools\SDKs\go1.24.4.windows-amd64\go\bin;' + $env:PATH
$env:HOME="$PWD\\.lh-home"
go run ./cmd/lh msg-gateway start --platform weixin
```

当前实现范围：
- 支持长轮询收消息
- 支持文本消息回复
- 支持基于 `context_token` 的连续对话
- 支持私聊/群聊策略和基础白名单

当前还没有实现：
- 图片、语音、文件收发
- typing 状态
- `context_token` 持久化
- 微信专用富文本格式优化
