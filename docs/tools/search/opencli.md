# opencli Tool

`opencli` 是 LuckyAgent 的内置 OpenCLI 桥接工具，用来调用 OpenCLI 的网页读取、站点 adapter、浏览器会话和原生命令能力。它适合处理普通 `web_fetch` 难以覆盖的网页任务，例如需要站点 adapter、登录态、浏览器状态或 OpenCLI 插件命令的场景。

它不是 shell 工具。不要把 bash、sh、zsh 等 shell 命令直接交给 `opencli`；需要执行系统命令时应使用 `terminal`。

## 工具定义

实现位置：

- `internal/tool/builtin_opencli.go`
- `internal/tool/opencli_config.go`

注册信息：

```go
Name:       "opencli"
Category:   CatBuiltin
Source:     "builtin"
Permission: PermApprove
```

含义：

- `CatBuiltin`：这是核心内置工具，不依赖 skill 或 MCP。
- `PermApprove`：OpenCLI 可能访问外部网站、浏览器会话、下载文件或调用 adapter，默认需要审批。
- 它不是 `ShellAware` 工具；工作目录由 `download_dir` 决定，而不是 agent shell `_cwd`。

## 参数

`opencli` 接收这些参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `action` | 否 | 自动推断 | 操作类型：`web_read`、`site`、`twitter_timeline`、`browser`、`raw`。 |
| `url` | 否 | 无 | `web_read` 的目标 URL。 |
| `site` | 否 | 无 | `site` 模式的 OpenCLI adapter 名，例如 `twitter`、`youtube`、`zhihu`。 |
| `command` | 否 | 无 | `site` 模式的 adapter command，或 `browser` 模式的 browser subcommand。 |
| `args` | 否 | 无 | 额外 OpenCLI 参数。`raw` 模式下这是 opencli binary 后面的完整参数。 |
| `format` | 否 | `md` | 输出格式。没有显式 `-f` / `--format` 时会追加。 |
| `limit` | 否 | 无 | adapter 常用数量限制，例如 timeline 条数。 |
| `feed_type` | 否 | `following` | Twitter timeline 类型。 |
| `browser_session` | 否 | `luckyagent` | browser 模式的会话名。 |
| `download_dir` | 否 | `~/.luckyagent/workspace/downloads/opencli` | OpenCLI 工作/下载目录。必须位于 `~/.luckyagent/workspace` 下。 |
| `max_chars` | 否 | 配置默认，通常 `50000` | 返回给模型的最大字符数，最大限制为 `200000`。 |
| `timeout_seconds` | 否 | 配置默认，通常 `20` | 单次 OpenCLI 命令超时，最大限制为 `120`。 |
| `dry_run` | 否 | `false` | 只预览 OpenCLI invocation plan，不执行命令。 |
| `verbose` | 否 | `false` | 输出 action、risk、workdir、source、fallback 等 metadata。 |
| `format_result` | 否 | `text` | LuckyAgent tool 返回格式：`text` 或 `json`。 |

## action 推断

如果没有显式传 `action`，工具会自动推断：

- 有 `url`：使用 `web_read`。
- 有 `site` 或 `command`：使用 `site`。
- 否则：使用 `raw`。

因此最简单的网页读取调用只需要传 URL：

```json
{
  "url": "https://example.com",
  "max_chars": 20000
}
```

## 配置

LuckyAgent 配置项：

```json
{
  "opencli": {
    "enabled": false,
    "command": "opencli",
    "args": ["web", "read", "--url", "{url}", "--stdout", "true", "--download-images", "false", "-f", "md"],
    "timeout_seconds": 20,
    "max_chars": 50000,
    "fallback_to_web_fetch": true
  }
}
```

对应 config keys：

- `opencli.enabled`
- `opencli.command`
- `opencli.args`
- `opencli.timeout_seconds`
- `opencli.max_chars`
- `opencli.fallback_to_web_fetch`

`opencli.enabled=false` 时，`opencli` tool handler 会拒绝执行并返回：

```text
opencli is disabled by config: opencli.enabled=false
```

## 默认配置规范化

如果配置缺失，会使用这些默认值：

- `command`: `opencli`
- `args`: `web read --url {url} --stdout true --download-images false -f md`
- `timeout_seconds`: `20`
- `max_chars`: `50000`

`opencli.args` 支持占位符：

- `{url}`
- `{max_chars}`

## web_read 模式

`web_read` 用来把一个 URL 通过 OpenCLI 读取为 Markdown。

调用示例：

```json
{
  "action": "web_read",
  "url": "https://go.dev/doc/",
  "format": "md",
  "max_chars": 30000
}
```

构造逻辑：

1. 要求 `url` 非空。
2. 使用与 `web_fetch` 相同的 URL 校验逻辑。
3. 展开配置里的 `opencli.args` 模板。
4. 确保参数包含 `--stdout true`。
5. 确保参数包含 `--download-images false`。
6. 如果没有 `-f` 或 `--format`，追加 `-f <format>`。
7. 追加用户传入的额外 `args`。
8. 标注 invocation risk，常见值包括 `network_read`、`authenticated_read`、`browser_state`、`filesystem_download`、`external_mutation`、`raw_opencli`。

如果 OpenCLI 输出中包含指向 `.md` 文件的 Markdown 表格路径，工具会尝试读取这个保存的 Markdown 文件，并优先返回文件内容。

如果 `web_read` 失败且 `opencli.fallback_to_web_fetch=true`，会回退调用 `web_fetch`。默认 text 输出保持正文兼容；`verbose=true` 或 `format_result=json` 会标注 `source=web_fetch_fallback` 和 OpenCLI 失败摘要。

## dry-run

`dry_run=true` 会完成 URL 校验、download_dir 校验、raw 参数校验和 action 推断，但不会执行 OpenCLI。

输出示例：

```text
Would run OpenCLI
Action: web_read
Command: opencli
Args: web read --url https://example.com --stdout true --download-images false -f md
WorkDir: ~/.luckyagent/workspace/downloads/opencli
Timeout: 20s
MaxChars: 50000
Risk: network_read
Fallback: web_fetch
```

## site 模式

`site` 模式用来调用 OpenCLI adapter。

调用示例：

```json
{
  "action": "site",
  "site": "youtube",
  "command": "transcript",
  "args": ["--url", "https://www.youtube.com/watch?v=..."],
  "format": "md"
}
```

构造逻辑：

```text
opencli <site> <command> <args...> -f <format>
```

如果传了 `limit`，且参数中还没有对应限制，工具会追加 `--limit <limit>`。

Twitter timeline 有特殊处理：当 `site=twitter` 且 `command=timeline` 时，会确保：

```text
--type <feed_type>
--limit <limit>
```

其中 `feed_type` 默认是 `following`。

## twitter_timeline 模式

`twitter_timeline` 是 Twitter timeline 的便捷动作。

调用示例：

```json
{
  "action": "twitter_timeline",
  "feed_type": "following",
  "limit": 10,
  "format": "md"
}
```

构造出的 OpenCLI 参数类似：

```text
twitter timeline --type following --limit 10 -f md
```

`limit<=0` 时会回退为 `10`。

## browser 模式

`browser` 模式用来调用 OpenCLI browser primitives，并支持复用浏览器 session。

调用示例：

```json
{
  "action": "browser",
  "browser_session": "luckyagent",
  "command": "open",
  "args": ["https://example.com"]
}
```

构造逻辑：

```text
opencli browser <browser_session> <command> <args...>
```

如果 `command` 为空，但 `args` 非空，则构造：

```text
opencli browser <browser_session> <args...>
```

如果 `command` 和 `args` 都为空，会返回：

```text
command or args is required for opencli action=browser
```

`browser_session` 默认是 `luckyagent`。复用同一个 session 可以保留 tab 状态。

## raw 模式

`raw` 模式用于 OpenCLI 原生命令，例如 doctor、list、external、plugin 等。

调用示例：

```json
{
  "action": "raw",
  "args": ["doctor"]
}
```

或者：

```json
{
  "action": "raw",
  "args": ["plugin", "list"]
}
```

`raw` 模式下，`args` 是 `opencli` binary 后面的参数，不要包含 shell 命令。

如果 `args` 以 `opencli`、`opencli.cmd` 或 `opencli.exe` 开头，工具会自动去掉这个 binary 名。

如果 `args` 是 shell 包装的 OpenCLI 命令，例如：

```json
{
  "action": "raw",
  "args": ["bash", "-lc", "opencli doctor"]
}
```

工具会尝试拆出 `opencli doctor`，最终只执行 OpenCLI 参数 `doctor`。

如果 shell 命令里不是 OpenCLI，会拒绝：

```text
opencli action=raw only accepts OpenCLI arguments, not shell command "bash"; use the terminal tool for bash or sh commands
```

## download_dir 和 workspace 限制

`download_dir` 默认是：

```text
~/.luckyagent/workspace/downloads/opencli
```

显式传入的 `download_dir` 也必须位于：

```text
~/.luckyagent/workspace/
```

相对路径会解析到 workspace 下。

运行前会创建目录：

```go
os.MkdirAll(resolved, 0o700)
```

OpenCLI 子进程会以这个目录作为工作目录。

## 输出处理

OpenCLI 的 stdout 和 stderr 会一起收集。

输出会经过处理：

- 去掉首尾空白。
- `\r\n` 转为 `\n`。
- 删除末尾 OpenCLI update notice，例如 `Update available:`。
- 如果超过 `max_chars`，截断并追加 `... (truncated)`。

如果输出已经以 `# ` 开头，直接返回。

如果输出中存在 Markdown 一级标题，但不是开头，工具会把第一个一级标题复制到开头：

```markdown
# Title

<original output>
```

## saved Markdown 读取

`web_read` 模式下，如果 OpenCLI stdout 中有 Markdown 表格，并且某个 cell 以 `.md` 结尾，工具会尝试把它当成保存的 Markdown 文件路径读取。

读取条件：

- 路径存在。
- 不是目录。
- 文件大小非 0。
- 文件大小不超过 `max_chars * 4`。
- 如果配置了 workDir，文件必须位于 workDir 下。

如果读取成功，返回保存文件内容，而不是 OpenCLI stdout。

## 错误行为

OpenCLI 命令失败时：

- 如果有输出，错误会包含截断后的输出摘要。
- 如果没有输出，返回命令失败错误。

如果 OpenCLI 成功但输出为空，返回：

```text
opencli returned empty output
```

如果 `web_read` 失败且启用了 fallback，并且 `web_fetch` 成功，则返回 `web_fetch` 内容。

## 适合使用的场景

优先使用 `opencli` 的场景：

- 已知 URL，需要 OpenCLI 转 Markdown。
- 需要站点 adapter，例如 Twitter、YouTube、知乎、小红书、Bilibili。
- 需要浏览器 session 或登录态。
- 页面依赖 JavaScript，`web_fetch` 抓不到完整内容。
- 需要运行 OpenCLI doctor、plugin、external 等原生命令。
- 需要统一通过 OpenCLI 管理网页提取或站点操作。

示例：

```json
{
  "action": "web_read",
  "url": "https://example.com",
  "format": "md",
  "max_chars": 20000
}
```

## 不适合使用的场景

不优先使用 `opencli` 的场景：

- 只需要普通网页正文，且 `web_fetch` 足够。
- 不知道 URL，需要先找页面：使用 `web_search`。
- 需要执行 shell 命令：使用 `terminal`。
- 需要调用 JSON API：使用 `http_request`。
- 需要读取本地文件：使用 `file_read` 或 `document_read`。
- 需要纯粹的目录或文件操作：使用文件类工具。

## 常见调用示例

读取网页：

```json
{
  "action": "web_read",
  "url": "https://go.dev/doc/",
  "format": "md",
  "max_chars": 30000
}
```

调用 YouTube adapter：

```json
{
  "action": "site",
  "site": "youtube",
  "command": "transcript",
  "args": ["--url", "https://www.youtube.com/watch?v=VIDEO_ID"],
  "format": "md"
}
```

读取 Twitter following timeline：

```json
{
  "action": "twitter_timeline",
  "feed_type": "following",
  "limit": 10,
  "format": "md"
}
```

浏览器会话操作：

```json
{
  "action": "browser",
  "browser_session": "luckyagent",
  "command": "open",
  "args": ["https://example.com"]
}
```

运行 OpenCLI doctor：

```json
{
  "action": "raw",
  "args": ["doctor"]
}
```

## 和 web_search 的关系

`web_search` 用来找 URL。

`opencli` 用来读取、操作或通过 adapter 处理 URL / 站点。

常见流程：

1. `web_search` 找候选页面。
2. 选择目标 URL。
3. `opencli` 用 `web_read` 或站点 adapter 读取内容。

## 和 web_fetch 的关系

`web_fetch` 是轻量网页正文提取，固定走 Defuddle、Jina、curl。

`opencli` 更适合复杂网页、登录态、浏览器、站点 adapter 和 OpenCLI 原生命令。

如果 `opencli action=web_read` 失败，且配置 `opencli.fallback_to_web_fetch=true`，工具会自动尝试 `web_fetch`。

`verbose=true` 或 `format_result=json` 会显示最终内容来自 OpenCLI、saved Markdown，还是 `web_fetch` fallback。

## 和 terminal 的关系

`opencli` 只接受 OpenCLI 参数。

不要把 shell 命令交给 `opencli`：

```json
{
  "action": "raw",
  "args": ["bash", "-lc", "ls -la"]
}
```

这种任务应使用 `terminal`。

如果 shell wrapper 里面包的是 OpenCLI 命令，工具会尝试拆出来；否则会拒绝。

## 风险和注意事项

`opencli` 的风险来自外部站点访问、登录态、浏览器状态和 adapter 副作用。

调用前应确认：

- 目标 action 是否正确。
- `raw` 模式只包含 OpenCLI 参数。
- `download_dir` 在 `~/.luckyagent/workspace` 下。
- 需要登录态的站点是否已经配置好。
- `max_chars` 不会把过大内容塞进上下文。
- `opencli.enabled=false` 会阻止 handler 执行。
- `dry_run=true` 可先审计最终 command、args、workdir、timeout 和 risk。
- 对站点 adapter 的写入或关注、发布类操作需要额外谨慎。

## 维护注意事项

如果后续修改 `opencli`，需要同步检查：

- 参数说明是否仍与 `OpenCLITool()` 一致。
- `opencli.enabled` 是否仍真正控制调用。
- action 列表和别名是否变化。
- 默认 `opencli.args` 是否变化。
- risk 分类规则是否变化。
- `web_read` 是否仍强制 `--stdout true` 和 `--download-images false`。
- `twitter_timeline` 默认 `feed_type` 和 `limit` 是否变化。
- `download_dir` workspace 限制是否变化。
- saved Markdown 读取逻辑是否变化。
- 输出清理和截断规则是否变化。
- `fallback_to_web_fetch` 行为是否变化。
- shell wrapper 拆解和拒绝规则是否变化。
