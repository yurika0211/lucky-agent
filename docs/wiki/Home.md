# LuckyAgent Wiki

LuckyAgent 是一个面向长期运行的 Agent runtime。它把 CLI、HTTP API、TUI、GUI Dashboard、Telegram、QQ、NapCat、微信网关、记忆、RAG、工具调用和部署流程放在同一套运行时里管理。

如果你是第一次使用，可以按下面顺序阅读：

1. [[使用指南]]
   - 初始化运行目录
   - 配置模型和服务地址
   - 启动 CLI、API、TUI、GUI 和消息网关
   - 常用命令和排障入口

2. [[特色功能]]
   - 统一运行时
   - 长期记忆
   - RAG 知识库
   - 多平台消息网关
   - Cron、Autonomy 和工具系统
   - 多 Agent / benchmark 能力

3. [[使用场景]]
   - 本地 Agent 开发调试
   - 个人知识库问答
   - Telegram / QQ / 微信机器人
   - 团队内部 Agent API
   - 定时任务与自动化助手
   - Agent 能力评测与实验

4. [[Drift控制规范]]
   - 记忆、上下文、RAG、配置和多 Agent 行为漂移的分类
   - 自动 hygiene、摘要持久化和工具结果截断的约束
   - drift 相关测试与评审清单

## 快速入口

```bash
go run ./cmd/lh init
go run ./cmd/lh config set provider openai
go run ./cmd/lh config set api_key sk-your-api-key
go run ./cmd/lh config set model gpt-5.4-mini
go run ./cmd/lh chat
```

启动 HTTP API：

```bash
go run ./cmd/lh serve --addr 127.0.0.1:9090
```

启动 Telegram 网关：

```bash
go run ./cmd/lh msg-gateway start --platform telegram
```

## 核心心智模型

LuckyAgent 的关键不是“多几个入口”，而是所有入口都共享同一个 Agent 核心、同一份配置、同一套运行目录和同一组能力。

这意味着你可以先在本地用 `lh chat` 调试，再用 `lh serve` 接入内部系统，最后把同一套配置挂到 Docker 或消息网关上长期运行。
