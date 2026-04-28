# LuckyHarness 运行手册

## 1) 在源码目录运行

```bash
cd /media/shiokou/DevRepo43/DevHub/Projects/2026-myapp/luckyharness
```

建议直接用源码命令启动，避免使用历史构建产物。

## 2) 初始化配置

```bash
go run ./cmd/lh init
```

默认配置文件位置：

`~/.luckyharness/config.json`

完整模板：

`config.example.json`

## 3) 配置 LLM（最小可用）

```bash
go run ./cmd/lh config set provider openai
go run ./cmd/lh config set api_key sk-xxx
go run ./cmd/lh config set model gpt-4o-mini
go run ./cmd/lh config set api_base https://api.openai.com/v1
```

## 4) 启动交互对话

```bash
go run ./cmd/lh chat
```

## 5) 启动 API 服务

```bash
go run ./cmd/lh serve --addr 127.0.0.1:9090
```

常用健康检查：

```bash
curl -sS http://127.0.0.1:9090/api/v1/health
```

## 6) 启动 Telegram 网关

```bash
go run ./cmd/lh msg-gateway start \
  --platform telegram \
  --token "<YOUR_TG_BOT_TOKEN>" \
  --api-addr 127.0.0.1:9090
```

常用管理命令：

```bash
go run ./cmd/lh msg-gateway status --api-addr 127.0.0.1:9090
go run ./cmd/lh msg-gateway stop telegram --api-addr 127.0.0.1:9090
```

## 7) 可选：启用 Telemetry

Telemetry 通过环境变量控制，主要用于 HTTP 请求链路和 Agent Loop span。

```bash
export LH_TELEMETRY_ENABLED=true
export LH_TELEMETRY_EXPORTER=stdout
export LH_TELEMETRY_SAMPLE_RATE=1.0
go run ./cmd/lh serve --addr 127.0.0.1:9090
```

如果使用 OTLP：

```bash
export LH_TELEMETRY_ENABLED=true
export LH_TELEMETRY_EXPORTER=otlp
export LH_TELEMETRY_OTLP_ENDPOINT=127.0.0.1:4317
go run ./cmd/lh serve --addr 127.0.0.1:9090
```

## 8) 使用项目内 HOME 隔离配置（可选）

```bash
export HOME="$PWD/.lh-home"
go run ./cmd/lh init
go run ./cmd/lh chat
```

这样会把配置和会话数据写到 `./.lh-home/.luckyharness`，便于测试隔离。
