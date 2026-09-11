# LuckyAgent 超时配置指南

使用 `la config timeout` 查看当前配置归一化后的有效值。输出同时包含配置路径，便于直接调整：

```bash
la config timeout
la config set msg_gateway.telegram.chat_timeout_seconds 1200
la config set agent.timeout_seconds 120
```

主要层级：

| 层级 | 配置项 | 默认值 |
| --- | --- | ---: |
| Telegram 对话总超时 | `msg_gateway.telegram.chat_timeout_seconds` | 600 秒 |
| Agent 单轮循环 | `agent.timeout_seconds` | 60 秒 |
| 简单本地检查 | `agent.simple_local_inspection.timeout_seconds` | 25 秒 |
| OpenCLI | `opencli.timeout_seconds` | 20 秒 |
| Computer Use 总时长 | `tools.computer_use.timeout_seconds` | 300 秒 |
| Computer Use 单步 | `tools.computer_use.step_timeout_seconds` | 30 秒 |
| 熔断器冷却 | `circuit_breaker.timeout_seconds` | 30 秒 |
| Autonomy Worker | `autonomy.worker.timeout_seconds` | 300 秒 |
| Hooks | `hooks.timeout_seconds` | 30 秒 |

Telegram 出现“请求超时”时，先区分是对话总超时还是 Agent 单轮超时：前者通常持续较久并终止整个请求，后者通常发生在某一轮模型调用或工具观察阶段。增加 Telegram 总超时不会改变 Agent 单轮上限，两个值需要分别调整。
