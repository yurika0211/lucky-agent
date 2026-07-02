# heartbeat Tools

LuckyAgent 的 heartbeat 工具组用于手动触发 HEARTBEAT.md 评估，以及查看 heartbeat 运行状态。它包含两个工具：`heartbeat_trigger` 和 `heartbeat_status`。

这组工具依赖外部注入的 handler。工具层只负责注册和转发调用。

## 工具定义

实现位置：

- `internal/tool/heartbeat_service.go`

注册位置：

- `HeartbeatToolService.RegisterTools`

工具列表：

| 工具 | 权限 | 说明 |
| --- | --- | --- |
| `heartbeat_trigger` | `PermAuto` | 手动触发 HEARTBEAT.md evaluation，并执行 active periodic tasks。 |
| `heartbeat_status` | `PermAuto` | 返回 HEARTBEAT.md runtime status 和最新 routed external chat target。 |

两个工具的 `Category` 都是 `CatDelegate`，`Source` 都是 `builtin`。

## heartbeat_trigger

参数：

```json
{}
```

执行流程：

1. 检查 service 和 trigger handler 是否存在。
2. 如果 handler 不存在，返回错误。
3. 调用注入的 trigger handler。
4. 原样返回 handler 的输出。

handler 未配置时返回：

```text
heartbeat trigger handler not configured
```

## heartbeat_status

参数：

```json
{}
```

执行流程：

1. 检查 service 和 status handler 是否存在。
2. 如果 handler 不存在，返回错误。
3. 调用注入的 status handler。
4. 原样返回 handler 的输出。

handler 未配置时返回：

```text
heartbeat status handler not configured
```

## 输出格式

工具层不定义固定输出 schema。

实际输出由构造 `HeartbeatToolService` 时注入的两个函数决定：

```go
trigger func(args map[string]any) (string, error)
status  func(args map[string]any) (string, error)
```

因此文档或调用方不能只根据 `heartbeat_service.go` 假设具体 JSON 字段。

## 适合使用的场景

优先使用 heartbeat 工具的场景：

- 手动触发一次 HEARTBEAT.md 检查。
- 调试周期任务是否会被 heartbeat 路由。
- 查看 heartbeat runtime 状态。
- 查看最新 routed external chat target。

示例：

```json
{}
```

## 不适合使用的场景

不优先使用 heartbeat 工具的场景：

- 添加周期任务，应使用 cron 或 autonomy 相关工具。
- 立即执行 shell 命令，应使用 `terminal`。
- 查看普通任务队列，应使用 autonomy 状态工具。
- 未启用 heartbeat runtime 时，handler 可能未配置。

## 风险和注意事项

heartbeat 工具的主要注意点：

- 两个工具都是 `PermAuto`。
- `heartbeat_trigger` 可能间接执行 active periodic tasks，实际副作用取决于注入 handler。
- 工具层没有参数校验，因为没有参数。
- 工具层不定义输出结构。

## 维护注意事项

如果后续修改 heartbeat 工具，需要同步检查：

- 工具名是否仍是 `heartbeat_trigger` 和 `heartbeat_status`。
- 权限是否仍是 `PermAuto`。
- 是否新增参数。
- handler 未配置时的错误文案是否变化。
- 输出是否开始在工具层定义固定 schema。
