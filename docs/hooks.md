# Hooks

LuckyHarness 在工具执行边界上提供可配置的 hook，让运维和二次开发**不改 Go 源码、不重新编译**就能在工具调用前后注入策略。Hook 配置落在 `config.json` 的 `hooks` 段，可随配置热重载生效。

## 事件

| 事件 | 触发时机 | 能做什么 |
|---|---|---|
| `PreToolUse` | 工具执行前 | 放行 / 拦截(block) / 改写参数(modify) |
| `PostToolUse` | 工具返回后、结果回上下文前 | 改写或脱敏输出(modify) / 撤回输出(block) |

> 现有的 `toolExecutionGuard`(按用户意图写死的安全闸)保持不变，与 hook **并存**。两者都会在 PreToolUse 这一点参与判断。

## 配置

```jsonc
{
  "hooks": {
    "enabled": true,            // 总开关；false 时所有 hook 不生效（默认）
    "timeout_seconds": 30,      // 单个 hook 的执行超时
    "fail_closed": false,       // hook 出错/超时时：false=放行，true=拦截
    "pre_tool_use": [
      {
        "match":   ["file_delete", "shell"],  // 工具名，空数组=匹配全部
        "sources": [],                          // 来源 cli/telegram/qq/...，空=全部
        "command": "/path/to/pre_guard.sh"      // 外部命令；或用 "script": "/path/to/x.py"
      }
    ],
    "post_tool_use": [
      { "command": "/path/to/redact_secrets.sh" }
    ]
  }
}
```

- `command`：经平台 shell 执行(`sh -c` / Windows `cmd /C`)。
- `script`：脚本路径，按扩展名选择解释器(`.py`→python3、`.js`→node、其它→sh)。
- `match` / `sources` 为空表示匹配全部。

## 协议(JSON over stdin/stdout)

每次匹配的 hook 会被执行一次。LuckyHarness 通过 **stdin** 传入一个 JSON Payload，hook 通过 **stdout** 返回一个 JSON Decision。

**输入(stdin):**

```json
{
  "event": "PreToolUse",
  "tool": "file_delete",
  "arguments": "{\"path\":\"/etc/hosts\"}",
  "source": "",
  "session_id": "sess-1",
  "output": "",   // 仅 PostToolUse
  "error": ""     // 仅 PostToolUse
}
```

**输出(stdout):**

```json
{
  "decision": "allow",              // allow | block | modify（空或无法解析=allow）
  "reason": "protected path",       // block 时回给模型的原因
  "modified_arguments": "{...}",    // PreToolUse modify：替换后的参数 JSON
  "modified_output": "[redacted]"   // PostToolUse modify：替换后的输出
}
```

- hook 无输出 / 退出码 0 且 stdout 为空 → 视为 `allow`。
- hook 非零退出或超时 → 按 `fail_closed` 处理(默认放行)。
- PreToolUse 返回 `block` → 工具不执行，模型收到一条说明被拦截的工具结果。
- PostToolUse 返回 `block` → 输出被撤回并替换为占位说明。

## 示例

**1. 拦截删除受保护路径(PreToolUse, bash):**

```sh
#!/bin/sh
# pre_guard.sh
payload=$(cat)
case "$payload" in
  *'/etc/'*|*'/.ssh/'*) echo '{"decision":"block","reason":"protected path"}' ;;
  *)                    echo '{"decision":"allow"}' ;;
esac
```

**2. 脱敏工具输出里的 token(PostToolUse, python):**

```python
#!/usr/bin/env python3
import sys, json, re
p = json.load(sys.stdin)
out = re.sub(r'(token|sk-)[A-Za-z0-9_-]+', '[REDACTED]', p.get("output", ""))
print(json.dumps({"decision": "modify", "modified_output": out}))
```

## 接入点

- PreToolUse：`internal/agent/loop_execution.go` 的 `executeToolCallsOrderedGuarded`，紧接在 guard 检查之后。
- PostToolUse：`internal/agent/loop.go` 的 `executeToolWithSession`,在返回 output 之前（所有工具执行的唯一咽喉)。
- 运行时配置由 `internal/agent/agent.go` 的 `buildHookRuntimeConfig` 从 `config.HooksConfig` 构建，实现位于 `internal/hook/`。

## v1 限制

- `sources` 过滤暂未生效：当前所有调用传入的来源为空，建议先把 `sources` 留空。网关来源接入后该过滤才有意义。
- PreToolUse 的外部命令在执行前**串行**评估；一次有多个工具调用时 hook 延迟会叠加。
- 暂只支持 `PreToolUse` / `PostToolUse`；`UserPromptSubmit` / `Stop` 待后续补充。
- `enabled` 默认为 `false`：未配置 hook 的运行时行为与之前完全一致。
