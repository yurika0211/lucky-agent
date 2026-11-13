# create_ephemeral_credential_tool

根据已经保存的 `credential_ref` 创建一个临时的、当前会话可见的 HTTP 工具。临时工具只保存凭据引用，不保存或返回凭据明文。

需要把凭据用于 CLI 或其他 shell 命令时，使用 `create_ephemeral_credential_terminal_tool`。它创建的临时 terminal 工具只把凭据放进一次受控子进程的环境变量，命令参数、ShellContext、工具结果和日志都不会保存凭据。

## 使用流程

1. 先调用 `request_credential`，由用户在安全表单中保存凭据。
2. 调用 `create_ephemeral_credential_tool`，提交固定的 HTTP 模板和 `credential_ref`。
3. 用户批准创建后，模型会得到一个 `tmp_credential_*` 工具名。
4. 后续调用这个临时工具时只传业务参数。

## 约束

- 临时工具绑定当前 session，默认 10 分钟过期，最长 1 小时。
- 只支持声明式 HTTP 请求，不执行脚本、命令或表达式。
- 凭据只能注入认证请求头，不能进入 URL、请求体、命令参数、环境变量或 Cookie。
- 临时工具创建和每次执行都需要审批；不能被全局 `auto_approve` 绕过。
- 临时工具不进入持久化配置、Session transcript 或后台 autonomy 任务。
- 过期、删除 session 或 Agent 关闭时，临时工具会被撤销。

## 临时 terminal 工具

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

命令模板可以使用 `input_schema` 声明业务参数，例如 `{repo}`；参数会经过 shell quoting。凭据必须由命令通过 `env_name` 读取，不能把凭据拼进命令模板。创建和每次执行都需要审批，输出和错误流会做凭据脱敏。后台 autonomy 任务不能创建或调用此类工具。

## 示例参数

```json
{
  "credential_ref": "github-main",
  "name": "github_get_user",
  "description": "读取当前 GitHub 用户信息",
  "method": "GET",
  "url_template": "https://api.github.com/user",
  "auth_type": "bearer",
  "ttl_seconds": 600
}
```

支持的认证类型：

- `bearer`：`Authorization: Bearer <credential>`
- `api_key`：`X-API-Key: <credential>`
- `raw_header`：使用经过校验的自定义请求头

`url_template` 的参数必须在 `input_schema` 中声明，使用 `{parameter}` 占位符。`body_template` 只能是渲染后仍然有效的 JSON，并且占位符必须作为 JSON 值使用。
