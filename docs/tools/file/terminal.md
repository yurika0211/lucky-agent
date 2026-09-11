# terminal Tool

`terminal` 是 LuckyAgent 的内置终端执行工具，用来运行真实的系统命令。它适合处理必须依赖本地运行环境的问题，例如检查仓库状态、运行测试、查看进程、执行项目脚本、确认 CLI 输出，或者完成文件类工具无法表达的操作。

它不是普通问答工具，也不是网页工具。调用 `terminal` 会在宿主机上启动一个 shell 子进程，因此它被标记为需要审批的工具。

## 工具定义

实现位置：

- `internal/tool/builtin_fs.go`
- `internal/tool/shell_exec.go`

注册信息：

```go
Name:         "terminal"
Category:     CatBuiltin
Source:       "builtin"
Permission:   PermApprove
ShellAware:   true
ParallelSafe: false
```

含义：

- `CatBuiltin`：这是核心内置工具，不依赖 skill 或 MCP。
- `PermApprove`：命令会产生真实系统行为，默认需要审批。
- `ShellAware`：agent 可以向工具注入当前工作目录和环境变量。
- `ParallelSafe=false`：不应假设多个终端命令可以安全并发执行，尤其是会写文件、启动服务或依赖当前状态的命令。

## 参数

`terminal` 接收三个参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `command` | 是 | 无 | 要执行的具体终端命令。 |
| `timeout` | 否 | `30` | 超时时间，单位秒。小于等于 0 时回退到 30，最大 300。 |
| `workdir` | 否 | 当前 shell 上下文目录 | 命令执行目录。适合在指定项目或子目录中运行命令。 |

示例参数：

```json
{
  "command": "go test ./internal/tool",
  "timeout": 120,
  "workdir": "/media/shiokou/DevRepo60/DevHub/Projects/2026-myapp/luckyagent"
}
```

## 执行流程

`terminal` 的执行过程是：

1. 读取 `command`，没有提供时返回 `command is required`。
2. 调用 `validateShellSandbox(command)` 做命令文本层面的限制检查。
3. 解析 `timeout`，默认 30 秒，最大 300 秒。
4. 读取 shell 上下文中的 `_cwd` 和 `_env`。
5. 如果传入 `workdir`，优先使用 `workdir`；否则使用 `_cwd`。
6. 将 `_env` 里的合法环境变量前置到命令里。
7. 非 Windows 使用 `sh -c <command>` 执行。
8. Windows 使用 PowerShell 的 `-NoLogo -NoProfile -NonInteractive -Command` 执行。
9. 收集 stdout 和 stderr。
10. 命令失败时仍返回输出，并在末尾追加 exit code。
11. 输出超过 10000 字符时截断。
12. 超时时杀掉进程并返回超时错误。

## 输出格式

正常输出直接返回 stdout。

如果有 stderr，会追加：

```text
[stderr]
...
```

如果命令退出码非零，会追加类似：

```text
[exit code: exit status 1]
```

如果输出过长，会保留前 10000 字符并追加：

```text
... (truncated)
```

这意味着调用方不能只根据是否有文本判断命令成功，应该检查输出里是否包含 stderr、exit code 或明确的失败信息。

## 工作目录

`terminal` 支持两层工作目录：

- shell 上下文注入的 `_cwd`
- 显式参数 `workdir`

显式 `workdir` 优先级更高。典型用法是在仓库根目录运行 Go 测试：

```json
{
  "command": "go test ./internal/agent",
  "workdir": "/media/shiokou/DevRepo60/DevHub/Projects/2026-myapp/luckyagent",
  "timeout": 120
}
```

如果命令必须在 UI 子项目中运行，应显式指定对应目录：

```json
{
  "command": "npm test",
  "workdir": "/media/shiokou/DevRepo60/DevHub/Projects/2026-myapp/luckyagent/UI/GUI",
  "timeout": 120
}
```

## 环境变量注入

因为 `terminal` 是 `ShellAware`，agent loop 可以传入 `_env`。工具会把 `_env` 转换成 shell 前缀。

在 Unix 系统中，形式类似：

```sh
export KEY='value'; <command>
```

在 Windows 中，形式类似：

```powershell
$env:KEY = 'value'; <command>
```

环境变量名必须匹配：

```text
^[a-zA-Z_][a-zA-Z0-9_]*$
```

不合法的变量名会被跳过。

## 内置限制

`terminal` 的 `validateShellSandbox` 会拒绝命令文本中出现这些敏感路径片段：

- `.nanobot`
- `.ssh/`
- `.gnupg/`
- `.aws/`
- `/etc/shadow`
- `/etc/ssh/`
- `config.json`

也会拒绝命令文本中出现这些敏感环境变量名：

- `FILEBROWSER_`
- `NANOBOT_`
- `OPENAI_API_KEY`
- `ANTHROPIC_API_KEY`

这些检查是字符串级别的防线，不是完整系统沙箱。`terminal` 仍然会运行真实 shell 命令，所以调用前需要结合用户意图、权限审批和执行 guard 一起判断。

## Agent 层执行保护

除了 tool 自身的检查，agent 还有一层 `tool_execution_guard`。它会根据用户输入中的约束拦截部分终端命令。

例如用户说了：

- `只读`
- `只查看`
- `不要修改文件`
- `不要删除`
- `不要 push`

那么 guard 会检查 `terminal.command`，并阻止明显的写入、删除或 push 行为。

当前写入类命令识别包括：

- 输出重定向 `>`
- `sed -i`
- `perl -pi`
- `tee`
- `touch`
- `mv`
- `cp`
- `chmod`
- `chown`
- `git add`
- `git commit`

删除类命令识别包括：

- `rm`
- `rmdir`
- `unlink`
- `del`
- `rm -rf`
- `rm -r`

这层保护的目标是尊重用户当前请求中的限制。它不是系统级安全沙箱，也不能替代审批。

## 适合使用的场景

优先使用 `terminal` 的场景：

- 运行测试：`go test ./internal/tool`
- 查看 Git 状态：`git status --short`
- 搜索代码：`rg -n "TerminalTool" internal`
- 检查命令是否存在：`command -v opencli`
- 运行项目 CLI：`go run ./cmd/la --help`
- 查看进程或端口：`ss -ltnp`
- 执行构建、lint、格式化命令
- 启动需要真实 shell 的本地服务

不优先使用 `terminal` 的场景：

- 读取普通文件：优先用 `file_read`
- 列目录：优先用 `file_list`
- 修改文件：优先用 `file_patch` 或 `file_write`
- 删除文件：优先用 `file_delete`
- 读取 PDF/DOCX/PPTX：优先用 `document_read`
- 查询 JSON/YAML/CSV/SQLite：优先用对应 query 工具
- 抓取网页：优先用 `web_fetch` 或 `opencli`

这样做的好处是工具语义更明确，输出更稳定，也更容易被权限和 guard 精确控制。

## 命令编写建议

使用 `terminal` 时，命令应该具体、可复现、范围清楚。

推荐：

```sh
rg -n "RegisterCoreTools" internal/tool internal/agent
```

不推荐：

```sh
find / -name "*tool*" 2>/dev/null
```

推荐：

```sh
go test ./internal/tool
```

不推荐：

```sh
go test ./...
```

除非确实需要全量验证。

推荐：

```sh
git status --short
```

不推荐在没有明确授权时执行：

```sh
git reset --hard
```

## 常见调用示例

检查项目状态：

```json
{
  "command": "git status --short",
  "workdir": "/media/shiokou/DevRepo60/DevHub/Projects/2026-myapp/luckyagent"
}
```

运行聚焦测试：

```json
{
  "command": "go test ./internal/tool",
  "timeout": 120,
  "workdir": "/media/shiokou/DevRepo60/DevHub/Projects/2026-myapp/luckyagent"
}
```

检查工具注册位置：

```json
{
  "command": "rg -n \"TerminalTool|RegisterCoreTools|RegisterTools\" internal/tool internal/agent",
  "workdir": "/media/shiokou/DevRepo60/DevHub/Projects/2026-myapp/luckyagent"
}
```

查看 CLI 帮助：

```json
{
  "command": "go run ./cmd/la --help",
  "timeout": 120,
  "workdir": "/media/shiokou/DevRepo60/DevHub/Projects/2026-myapp/luckyagent"
}
```

## 和其他工具的关系

`terminal` 是兜底能力，不是所有本地操作的首选入口。

如果任务可以由更窄的工具完成，优先使用更窄的工具：

- `file_read` 比 `cat` 更适合读文件。
- `file_patch` 比 `sed -i` 更适合可审计的小修改。
- `json_query` 比 `jq` 更适合简单 JSON 字段读取。
- `db_schema` 比 `sqlite3 .schema` 更适合只看 SQLite schema。
- `web_fetch` 比 `curl` 更适合提取网页正文。

当任务需要项目原生命令、真实运行结果、测试框架、编译器、系统状态或外部 CLI 时，再使用 `terminal`。

## 维护注意事项

如果后续修改 `terminal`，需要同步检查：

- 参数说明是否仍与 `TerminalTool()` 一致。
- `timeout` 默认值和最大值是否变化。
- shell 执行方式是否变化。
- `validateShellSandbox` 的限制列表是否变化。
- `tool_execution_guard` 对 shell 写入、删除、push 的识别是否变化。
- 是否新增了更细粒度的工具，可以替代部分 terminal 场景。

