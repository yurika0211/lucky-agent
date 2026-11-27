# 本地凭据

LuckyAgent 支持把模型凭据保存到本地加密凭据库。凭据值通过 TTY 隐藏输入，不作为命令行参数传递。模型需要凭据时调用 `request_credential`，用户在安全表单里填写。明文只进入本地加密库，工具结果只返回 `credential_ref`。

```bash
lh credential add openai-main --kind llm_api_key
lh config set models.endpoints.chat.credential_ref openai-main
lh credential list
lh credential remove openai-main
```

凭据库位于 `${HOME}/.luckyagent/runtime/credentials.db`，本地 KEK 位于同目录的 `credential.kek`。每条记录使用独立 DEK 和 AES-256-GCM 加密；配置文件只保存 `credential_ref`。运行时由 provider 内部解析引用，凭据不会作为模型工具结果返回。

如果需要让模型调用一个带凭据的外部 HTTP API，可以在 `request_credential` 之后调用 `create_ephemeral_credential_tool`。它会在当前 session 内创建一个有 TTL 的临时 HTTP 工具。临时工具只持有 `credential_ref`，在执行器内部短暂注入认证请求头，不把明文返回给模型，也不会写入 Session 或后台任务。详见 [`docs/tools/basic/create_ephemeral_credential_tool.md`](tools/basic/create_ephemeral_credential_tool.md)。

需要让 CLI 使用凭据时，调用 `create_ephemeral_credential_terminal_tool`。它只在一次受控子进程中注入指定环境变量，禁止把 secret 放进命令行参数、持久化 `ShellContext.Env`、工具输出或日志；创建和执行都强制人工审批。详见 [`docs/tools/basic/create_ephemeral_credential_terminal_tool.md`](tools/basic/create_ephemeral_credential_terminal_tool.md)。

这是本地单用户 MVP。当前 KEK 是权限为 0600 的本地文件，后续可以替换为系统 Keyring 或 KMS；当前版本也暂未引入独立 worker 和 OS 沙箱。
