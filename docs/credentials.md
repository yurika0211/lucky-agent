# 本地凭据

LuckyAgent 支持把模型凭据保存到本地加密凭据库。凭据值通过 TTY 隐藏输入，不作为命令行参数传递。模型需要凭据时调用 `request_credential`，用户在安全表单里填写。明文只进入本地加密库，工具结果只返回 `credential_ref`。

```bash
lh credential add openai-main --kind llm_api_key
lh config set models.endpoints.chat.credential_ref openai-main
lh credential list
lh credential remove openai-main
```

凭据库位于 `${HOME}/.luckyagent/runtime/credentials.db`，本地 KEK 位于同目录的 `credential.kek`。每条记录使用独立 DEK 和 AES-256-GCM 加密；配置文件只保存 `credential_ref`。运行时由 provider 内部解析引用，凭据不会作为模型工具结果返回。

这是本地单用户 MVP。当前 KEK 是权限为 0600 的本地文件，后续可以替换为系统 Keyring 或 KMS；当前版本也暂未引入独立 worker 和 OS 沙箱。
