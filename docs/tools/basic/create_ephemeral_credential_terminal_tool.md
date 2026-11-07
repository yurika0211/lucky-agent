# create_ephemeral_credential_terminal_tool

根据已保存的 `credential_ref` 创建一个只在当前 session 可见的临时 terminal 工具。

```json
{
  "credential_ref": "github-main",
  "name": "github_cli",
  "description": "调用已认证的 GitHub CLI",
  "command_template": "gh api user",
  "env_name": "GH_TOKEN",
  "ttl_seconds": 600,
  "timeout_seconds": 30
}
```

`command_template` 是创建时固定的 shell 命令。业务输入通过 `input_schema` 声明并以 shell quoting 方式渲染；凭据只能通过 `env_name` 从子进程环境读取。

- secret 不进入命令行参数、`ShellContext.Env`、工具结果、stdout/stderr、日志或 hook。
- 生成的工具和每次执行都强制 HITL，`auto_approve` 不能绕过。
- 默认 TTL 为 10 分钟，最长 1 小时；单次命令默认超时 30 秒，最长 5 分钟。
- 工具只绑定当前 session，过期、删除 session 或 Agent 关闭时撤销。
- 后台 autonomy 任务不能创建或调用此工具。
