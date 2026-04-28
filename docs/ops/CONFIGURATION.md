# LuckyHarness 配置指南 v0.64.0

## 配置文件位置

统一使用一个配置文件：

`~/.luckyharness/config.json`

程序启动时会加载这个文件，未显式传 CLI 参数时使用这里的值。

## 完整示例

请直接参考仓库根目录：

`config.example.json`

## 启动命令与配置映射

- `lh chat` 读取：`provider/api_key/api_base/model/soul_path/max_tokens/temperature` 和 `agent.*`
- `lh serve` 读取：`server.*`
- `lh dashboard start` 读取：`dashboard.addr`
- `lh msg-gateway start` 读取：`msg_gateway.*`

说明：

- CLI 参数优先级高于 `config.json`。
- 未传 CLI 参数时，启动命令会自动回落到 `config.json`。

## 常用字段

- LLM 主配置：`provider`, `api_key`, `api_base`, `model`, `max_tokens`, `temperature`
- Embedding / RAG 配置：`embedding.model`, `embedding.api_key`, `embedding.api_base`, `embedding.dimension`
- Provider 额外请求头：`extra_headers`
- 重试/熔断/限流：`retry`, `circuit_breaker`, `rate_limit`
- Agent Loop：`agent.max_iterations`, `agent.timeout_seconds`, `agent.auto_approve`, `agent.repeat_tool_call_limit`, `agent.tool_only_iteration_limit`, `agent.duplicate_fetch_limit`
- Context 调试：`agent.context_debug`
- API Server：`server.addr`, `server.api_keys`, `server.enable_cors`, `server.rate_limit`
- 消息网关：`msg_gateway.platform`, `msg_gateway.telegram.token`, `msg_gateway.onebot.*`
- Telegram 中间步骤展示：`msg_gateway.telegram.progress_as_messages`
- Telegram 中间步骤自然语言播报（结论最后输出）：`msg_gateway.telegram.progress_as_natural_language`
- Telegram 每轮未完成时由 LLM 生成一条总结性进度反馈：`msg_gateway.telegram.progress_summary_with_llm`
- Telegram 最终回答前附加工具摘要：`msg_gateway.telegram.show_tool_details_in_result`
  - CLI 兼容别名：`msg_gateway.telegram.show_tool_chain`
  - 说明：`config.json` / `config.example.json` 中的持久化字段仍然是 `show_tool_details_in_result`

## Provider 治理配置（已接入运行时）

以下配置已在 Provider 实际调用路径生效：

- `retry.*`
- `circuit_breaker.*`
- `rate_limit.*`

也就是不仅仅“可配置”，而是会在运行时通过中间件链包裹当前 provider。

## model_router 生效规则

- 开启 `model_router.enable=true` 后，Agent 会在每轮开始按任务复杂度尝试选模型。
- 如果同时配置了 `fallbacks`，则自动路由会跳过，不与降级链叠加。
- 需要“自动选模型”时，不要同时启用 `fallbacks`。

说明：

- `model_router.*` 目前建议直接编辑 `config.json`。
- `lh config set` 已覆盖大量字段，但并未覆盖全部 `model_router` 子字段。

## Telemetry 环境变量

Telemetry 由环境变量控制，不在 `config.json` 内配置：

- `LH_TELEMETRY_ENABLED`
- `LH_TELEMETRY_EXPORTER`
- `LH_TELEMETRY_OTLP_ENDPOINT`
- `LH_TELEMETRY_SAMPLE_RATE`

示例：

```bash
export LH_TELEMETRY_ENABLED=true
export LH_TELEMETRY_EXPORTER=stdout
export LH_TELEMETRY_SAMPLE_RATE=1.0
```

OTLP 示例：

```bash
export LH_TELEMETRY_ENABLED=true
export LH_TELEMETRY_EXPORTER=otlp
export LH_TELEMETRY_OTLP_ENDPOINT=127.0.0.1:4317
```

## JSON 序列化热路径

当前服务端、Provider 流式路径、Embedder 路径使用 `jsoniter` 兼容模式：

- `internal/server/server.go`
- `internal/provider/openai_stream.go`
- `internal/embedder/providers.go`

兼容模式是 `ConfigCompatibleWithStandardLibrary`，目标是降低开销且保持标准库行为兼容。

## .env 导出注意事项

如果要 `source .env`，赋值不要带空格。错误写法会导致 `command not found`。

正确示例：

```bash
EMBEDDING_MODEL_NAME="jina-embeddings-v4"
EMBEDDING_MODEL_KEY="sk-xxx"
EMBEDDING_MODEL_URL="https://proxy.pieixan.icu/v1"
```

## 生效方式

编辑 `config.json` 后重启对应进程即可生效。

示例：

```bash
pkill -9 lh
lh serve
```

## Agent 重复工具限制

可用字段：

- `agent.repeat_tool_call_limit`
  - 同一工具签名（工具名 + 参数）允许重复的次数上限
  - 默认：`3`
- `agent.tool_only_iteration_limit`
  - 连续“只有工具调用、没有正文回答”的轮次上限
  - 默认：`3`
- `agent.duplicate_fetch_limit`
  - 同一 URL 允许执行 `web_fetch` 的次数上限
  - 默认：`1`
- `agent.context_debug`
  - 打印上下文拼装调试报告（cache hit/miss、token 估算、分块统计）
  - 默认：`false`

示例：

```json
{
  "agent": {
    "max_iterations": 10,
    "timeout_seconds": 60,
    "auto_approve": false,
    "repeat_tool_call_limit": 2,
    "tool_only_iteration_limit": 2,
    "duplicate_fetch_limit": 1,
    "context_debug": true
  }
}
```
