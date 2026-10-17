# MCP 服务连接

LuckyAgent 的 MCP 客户端位于 `internal/mcpclient`，独立管理连接、工具目录和调用；`internal/tool/builtin_mcp.go` 负责把目录接到工具注册表。CLI、HTTP、消息网关通过同一个 `Admin` 接口管理连接。其他后端可以直接使用 `Manager.Tools()` 和 `Caller.CallTool()`，无需初始化 Agent 或工具注册表。

协议实现使用官方 Go SDK。支持 stdio 与 Streamable HTTP，以及 SDK 的协议版本协商；不提供旧版独立 HTTP+SSE 传输、OAuth 登录流程、resources/prompts 入口或跨用户连接共享。本阶段一个 Manager 对应一个运行时身份；不同用户或租户应使用独立 Manager 和凭据解析器。

## 配置与启动

在 `~/.luckyagent/config.json` 添加：

```json
{
  "mcp": {
    "servers": {
      "local": {
        "transport": "stdio",
        "command": "/absolute/path/to/mcp-server",
        "args": ["serve"],
        "env": {"MODE": "production"},
        "cwd": "/your/workspace",
        "timeout_seconds": 30
      },
      "remote": {
        "transport": "http",
        "url": "https://your-service.example/mcp",
        "credential_ref": "mcp-token",
        "timeout_seconds": 30
      }
    }
  }
}
```

服务名仅支持字母、数字、点、下划线与连字符，最多 64 个字符，首字符须为字母或数字。`transport` 省略时根据 `url` 推断。`timeout_seconds` 的范围是 0–3600，0 使用 30 秒。`disabled: true` 不启动服务。示例配置文件中的服务默认关闭。

stdio 使用可执行文件和参数数组启动，不经过 shell；继承宿主环境，再应用 `env` 覆盖值。环境值会按原样保存，秘密建议由宿主环境或服务自身的凭据管理提供。HTTP 使用 `credential_ref` 从已有本地加密凭据库按请求解析 Bearer token，不将 token 写入配置。连接不跟随 HTTP 重定向。

配置中的服务在运行时启动时恢复。单个服务失败会记录失败状态，其他服务继续启动。修改 `mcp.servers` 后需要重启运行时；配置重载会标记 `runtime_services` 需要重启。工具默认使用已有的 `approve` 权限流程。

## 管理命令

本地 REPL、Telegram 和 QQ Official 共用：

```text
/mcp status
/mcp connect remote
/mcp refresh remote
/mcp disconnect remote
```

`connect` 使用启动时载入的配置快照；`refresh` 重新发现工具，移除远端已经删除的工具。目录替换保持现有同名工具的权限覆盖。工具名称由服务名、原名和稳定摘要构成，避免规范化后冲突。旧目录的调用引用会报目录已变更，不会误调新连接上的工具。

保留旧命令 `/mcp <名称> <URL> [api_key]`，仅创建当前进程内的临时 HTTP 连接，退出后不会恢复。连接和发现失败会返回错误，不会打印连接成功。

HTTP 管理入口遵循现有 API 认证：

```text
GET /api/v1/mcp/servers
POST /api/v1/mcp/servers
{"name":"remote","action":"connect"}
```

POST 支持 `connect`、`refresh`、`disconnect`，只接受名称和动作，不能通过此入口提交任意子进程命令。状态中不包含 URL、环境变量、token 或凭据引用。

## 调用与生命周期

客户端保留完整 `inputSchema`（包括嵌套结构和 `$defs`）、原始结果、`structuredContent`、各类内容块和 `isError`。工具适配器将文本与结构化结果提供给模型，将图片转换为观察结果，其他内容保留为 JSON 和结果元数据。`isError` 会按失败返回。

每次调用传递请求上下文并应用服务超时。取消一次调用不会结束整个连接；连接替换或断开可能中止旧连接上的在途调用，不会重放它们。失败的替换保留可用的旧连接和目录。关闭 Agent 时统一移除 MCP 工具、关闭会话并回收 stdio 子进程。

服务通知不会自动刷新目录；当前使用显式 `refresh`。运行时可检测连接结束，但没有自动重连或任务恢复逻辑。资源、提示模板、多用户授权和服务沙箱属于后续扩展，不应在工具适配器里增加第二套协议实现。
