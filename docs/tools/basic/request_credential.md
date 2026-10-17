# request_credential Tool

`request_credential` 让用户在安全表单里填写 API key、token 或密码。明文只写入本地加密凭据库。模型拿到的结果只有 `credential_ref`。

实现位置：

- `internal/tool/builtin_credential.go`
- `internal/agent/credential_form.go`

注册信息：

```go
Name:       "request_credential"
Category:   CatBuiltin
Source:     "builtin"
Permission: PermAuto
```

`PermAuto` 只表示工具本身不需要再套一层“是否允许调用”的审批。表单仍然必须由人填写，自动批准不会替用户填凭据。

## 参数

| 参数 | 必填 | 说明 |
| --- | --- | --- |
| `id` | 是 | 凭据引用，只允许字母、数字、点、下划线和连字符，最长 64。 |
| `prompt` | 是 | 表单上的短说明。 |
| `kind` | 否 | 凭据类型，模型 key 用 `llm_api_key`，默认 `generic`。 |
| `scope` | 否 | 凭据域，默认 `default`。 |
| `bind` | 否 | 保存后绑定的模型端点：`chat`、`compact`、`vision`、`image`、`transcription`、`tts`、`embedding`、`reranker`。 |

模型传入 `value`、`secret`、`password`、`token`、`api_key` 会被丢掉，不会进入表单，也不会入库。

## 结果

成功时结果类似：

```text
Credential saved. credential_ref=openai-main kind=llm_api_key scope=default. The secret is not available to tools.
```

带 `bind=chat` 时，还会写入 `models.endpoints.chat.credential_ref`。取消或空提交不会写库。

## 客户端

安卓和 GUI 把 `kind=credential` 渲染成密码框。CLI 用终端隐藏输入。聊天回复不会被当成凭据提交。
