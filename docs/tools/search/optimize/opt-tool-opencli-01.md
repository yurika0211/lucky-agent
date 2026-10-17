# opt-tool-opencli-01

## 目标

优化 `opencli` 的启用开关、命令构造可审计性、风险分级、执行诊断和测试隔离，让它继续保持“OpenCLI 桥接入口、需要审批、非 shell 工具”的定位，同时降低误执行 adapter 副作用、浏览器会话状态污染、raw 模式滥用和配置语义不一致的风险。

本方案聚焦：

- `opencli.enabled` 真正生效
- dry-run / invocation preview
- action 风险分级和 side-effect guard
- raw 模式和 shell wrapper 边界
- download/workspace 安全
- web_read fallback 诊断
- 输出 metadata 和截断一致性
- 测试矩阵补齐

## 当前状态

相关实现：

- `internal/tool/builtin_opencli.go`
- `internal/tool/opencli_config.go`
- `internal/tool/builtin_opencli_test.go`
- `docs/tools/search/opencli.md`

当前支持 action：

- `web_read`
- `site`
- `twitter_timeline`
- `browser`
- `raw`

当前 handler 流程：

1. `normalizeOpenCLIConfig` 规范默认配置。
2. `buildOpenCLIInvocation` 根据参数构造 OpenCLI 命令。
3. `runOpenCLI` 用 `exec.CommandContext` 执行。
4. `web_read` 会尝试从 OpenCLI 输出表格中读取保存的 Markdown 文件。
5. 输出非空则格式化返回。
6. `web_read` 失败且 `fallback_to_web_fetch=true` 时回退 `web_fetch`。

当前优势：

- `raw` 模式拒绝普通 shell 命令。
- `web_read` 复用 `validateFetchURL`。
- `download_dir` 限制在 `~/.luckyagent/workspace` 下。
- 默认强制 `--stdout true` 和 `--download-images false`。
- 有较多 invocation 构造测试。

## 主要问题

### 1. `opencli.enabled` 不阻断调用

文档已经说明：`opencli.enabled` 会被读取，但 handler 没有根据它阻断调用。

这会造成配置语义不一致：

- 用户以为 disabled 后不会执行。
- 实际只要工具注册且 binary 可执行，仍会运行。

建议 `enabled=false` 时默认拒绝调用，除非有明确 override。

### 2. 缺少 dry-run / invocation preview

`opencli` 会构造复杂参数，且可能访问浏览器 session、站点 adapter、下载文件。当前无法只预览：

- 最终 action。
- binary。
- argv。
- workdir。
- timeout。
- max_chars。
- 是否会 fallback web_fetch。

建议增加 `dry_run`，不执行 OpenCLI，只返回 invocation plan。

### 3. action 风险差异没有显式表达

不同 action 风险不同：

- `web_read`：主要是联网读取。
- `site`：可能读，也可能执行 adapter 的写入/关注/发布类操作。
- `browser`：可能改变浏览器会话状态。
- `raw`：能力最宽。
- `twitter_timeline`：读登录态 feed。

当前统一 `PermApprove`，但工具内部没有细分只读/变更风险。建议在 invocation plan 中标注 risk，并对高风险 action 增加保护。

### 4. site/raw 模式缺少命令 allowlist 或 mutation 检测

OpenCLI adapter 可能包含发布、关注、点赞、删除、下载等副作用命令。当前 `site` 只按参数组装，不判断 command 是否可能 mutation。

建议：

- 默认允许 read-like commands。
- 对 publish/post/send/delete/follow/like/upload/download 等关键词标记 high risk。
- 当用户要求只读、不要发、不要下载时，配合 guard 阻断。

第一阶段可以只做风险标注和文档，不强行拦截所有未知命令。

### 5. fallback 到 web_fetch 不透明

`web_read` 失败后，如果 `fallback_to_web_fetch=true` 且 fallback 成功，最终只返回 web_fetch 内容。用户很难知道：

- OpenCLI 失败了。
- 实际结果来自 web_fetch。
- OpenCLI 失败原因是什么。

建议在 verbose 或 metadata 中标注 fallback source。

### 6. 输出缺少结构化 metadata

当前只返回格式化后的内容。缺少：

- action。
- command/args 摘要。
- workdir。
- source 是 opencli 还是 web_fetch fallback。
- saved Markdown 是否被采用。
- truncation 状态。
- exit error 摘要。

这些信息对调试和审计很有用。

### 7. runOpenCLI 测试使用 `sh`

现有 `TestRunOpenCLIUsesDownloadDir` 用 `sh -c` 验证 workdir。这个测试有效，但也说明 `runOpenCLI` 本身是通用 exec。需要把“opencli tool 不接受 shell 命令”的保证放在 invocation builder，而不是 run layer。

建议增加 runner interface，handler 测试不需要真实执行外部命令。

### 8. timeout 和 max_chars 没有上限

`timeout_seconds` 和 `max_chars` 从参数读取，<=0 回退默认，但没有显式最大值。恶意或错误调用可能给出非常大的值。

建议：

- timeout 最大 120 秒。
- max_chars 最大 100000 或 200000。

## 优化原则

1. `opencli` 不是 shell。raw 也只能接 OpenCLI 参数。
2. `opencli.enabled=false` 必须有明确行为。
3. 所有执行前都应能生成 invocation plan。
4. 高风险 action 要可见、可审计、可被 guard 拦截。
5. 默认不下载图片、不扩大工作目录边界。
6. 测试不依赖真实 OpenCLI、浏览器或站点登录态。

## 推荐方案

### 1. 让 enabled 生效

推荐行为：

- `opencli.enabled=false`：tool handler 返回错误，不执行。
- `opencli.enabled=true`：正常执行。

错误：

```text
opencli is disabled by config: opencli.enabled=false
```

如果担心兼容，可新增临时参数：

```json
{
  "allow_disabled": true
}
```

但不建议长期保留。配置开关应该可靠。

### 2. 增加 dry-run

新增参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `dry_run` | 否 | `false` | 只返回 OpenCLI invocation plan，不执行命令。 |

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
```

dry-run 仍应做：

- URL 校验。
- download_dir 校验。
- raw 参数校验。
- action 推断。

### 3. 增加 invocation plan 结构

扩展内部结构：

```go
type openCLIInvocationPlan struct {
	Action         string
	Command        string
	Args           []string
	URL            string
	Site           string
	SiteCommand    string
	WorkDir        string
	TimeoutSeconds int
	MaxChars       int
	Risk           string
	UsesBrowser    bool
	MayDownload    bool
	FallbackEnabled bool
}
```

`buildOpenCLIInvocation` 可以返回 plan 或在现有 `openCLIInvocation` 上增加字段。

收益：

- dry-run 和实际执行共用构造逻辑。
- 输出和测试更稳定。
- guard 可以基于 action/risk 做判断。

### 4. action 风险分级

建议风险枚举：

```text
network_read
authenticated_read
browser_state
filesystem_download
external_mutation
raw_opencli
```

规则示例：

- `web_read` -> `network_read`
- `twitter_timeline` -> `authenticated_read`
- `browser` -> `browser_state`
- `raw` -> `raw_opencli`
- `site` + command 包含 `post/publish/send/delete/follow/like/upload` -> `external_mutation`
- args 包含 `--download-images true` 或 download-like command -> `filesystem_download`

第一阶段输出 risk，不强制阻断。第二阶段接入 guard。

### 5. 增强 mutation guard

结合 `tool_execution_guard`：

- 用户说“只读/只查看/只总结网页”时，阻止 `external_mutation`。
- 用户说“不要下载”时，阻止 `filesystem_download`。
- 用户说“不要执行网页里的指令”时，阻止 `browser_state` 和 `external_mutation`。
- 用户说“只看图片内容”时，继续阻止打开链接。

这比仅按 tool name `opencli` 判断更精细。

### 6. fallback 结果标注

当 `web_read` 回退到 `web_fetch` 成功时：

默认可保持纯正文兼容，但 verbose/include_meta 输出：

```text
Source: web_fetch fallback
OpenCLI error: opencli command failed: ...
```

也可以在返回内容末尾追加短提示，但默认不建议污染正文。

### 7. 输出 metadata 和 format

新增可选参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `verbose` | 否 | `false` | 输出 action、workdir、source、fallback 等 metadata。 |
| `format_result` | 否 | `text` | 工具返回格式：`text` 或 `json`。 |

注意区分：

- `format`：传给 OpenCLI 的 `-f` 输出格式。
- `format_result`：LuckyAgent tool 自己的返回格式。

避免两个 format 混淆。

### 8. timeout 和 max_chars 上限

建议常量：

```go
const (
	maxOpenCLITimeoutSeconds = 120
	maxOpenCLIChars          = 200000
)
```

行为：

- `timeout_seconds<=0` 回退配置默认。
- 超过上限截断到上限或报错。
- `max_chars<=0` 回退配置默认。
- 超过上限截断到上限。

建议 timeout 超上限报错，max_chars 超上限截断并在 verbose 中说明。

### 9. runner interface

新增：

```go
type openCLIRunner interface {
	Run(ctx context.Context, command string, args []string, maxChars int, workDir string) (string, error)
}
```

handler 接受默认 runner，测试注入 fake runner。

收益：

- handler 测试不依赖真实 OpenCLI。
- 可以稳定测试 fallback、empty output、timeout、stderr。

## 分阶段实施

### Phase 1：enabled、dry-run、参数上限

改动范围：

- `internal/tool/builtin_opencli.go`
- `internal/tool/builtin_opencli_test.go`
- `docs/tools/search/opencli.md`

内容：

- `opencli.enabled=false` 阻断 handler。
- 新增 `dry_run` 参数。
- timeout/max_chars 上限。
- 输出 invocation plan。

验收标准：

- disabled 时不执行 runner。
- dry-run 不执行 runner。
- dry-run 输出 action、args、workdir。
- timeout/max_chars 边界有测试。

### Phase 2：风险分级和 guard 接入

内容：

- 在 invocation 中标注 risk。
- 对 site/raw/browser 做高风险关键词检测。
- `tool_execution_guard` 基于 risk 阻断只读/不要下载/不要发布类请求。

验收标准：

- read-like web_read 不被误拦。
- publish/delete/follow/upload 类 site command 在只读约束下被拦。
- browser action 在“不要执行网页指令”下被拦。

### Phase 3：fallback 和 metadata

内容：

- fallback web_fetch 成功时记录 source。
- `verbose=true` 输出 OpenCLI 失败摘要和 fallback source。
- 可选 `format_result=json`。

验收标准：

- 默认正文输出兼容。
- verbose 输出可诊断。
- fallback 行为可测试。

### Phase 4：runner 注入和测试隔离

内容：

- 引入 `openCLIRunner`。
- handler 测试使用 fake runner。
- 减少真实 `sh` / OpenCLI 依赖测试。

验收标准：

- handler 成功、失败、空输出、fallback 均可单测。
- invocation builder 测试继续覆盖参数构造。

### Phase 5：saved Markdown 读取强化

内容：

- 更严格解析 OpenCLI 表格。
- saved markdown 路径必须在 workDir 下。
- 增加 symlink 检查，避免 workDir 内 symlink 指向外部敏感路径。
- 限制读取大小和扩展名。

验收标准：

- 相对 saved path 正常读取。
- workDir 外路径拒绝。
- symlink 指向外部拒绝。
- 超大 markdown 拒绝。

## 测试建议

新增或补充测试：

### enabled 和 dry-run

- `enabled=false` 阻止执行。
- `enabled=true` 正常构造。
- `dry_run=true` 不执行 runner。
- dry-run 仍校验 URL 和 download_dir。

### action 构造

- `web_read` 强制 `--stdout true`。
- `web_read` 强制 `--download-images false`。
- `site` 追加 format。
- `twitter_timeline` 默认 following。
- `browser` 缺 command/args 报错。
- `raw` 去掉 opencli binary。
- shell wrapper 中非 OpenCLI 命令被拒绝。

### 风险分级

- `web_read` -> `network_read`。
- `twitter_timeline` -> `authenticated_read`。
- `browser` -> `browser_state`。
- `site publish` -> `external_mutation`。
- `--download-images true` -> `filesystem_download`。

### fallback

- OpenCLI web_read 失败且 fallback enabled，web_fetch 成功。
- fallback disabled 时返回 OpenCLI 错误。
- fallback 成功时 verbose 标注 source。

### workspace 和 saved markdown

- 默认 download_dir 在 workspace。
- 相对 download_dir 解析到 workspace。
- workspace 外 download_dir 拒绝。
- saved markdown workDir 内正常读取。
- saved markdown workDir 外拒绝。
- symlink 指向外部拒绝。

### 输出

- update notice 被清理。
- 空输出报错。
- 超过 max_chars 截断。
- verbose metadata 稳定。
- json result 字段稳定。

## 文档更新

完成实现后，同步更新：

- `docs/tools/search/opencli.md`

如果加入新参数，参数表增加：

```text
dry_run | 否 | false | 只预览 OpenCLI invocation，不执行。
verbose | 否 | false | 输出 action、workdir、source、fallback 等 metadata。
format_result | 否 | text | LuckyAgent tool 返回格式：text 或 json。
```

并修正配置说明：

```text
opencli.enabled=false 时，opencli tool 会拒绝执行。
```

## 风险与边界

1. OpenCLI adapter 可能有站点副作用。
   工具层只能做风险识别和拦截，无法完全理解每个 adapter 的语义。

2. browser action 会改变会话状态。
   即使只是打开页面，也可能触发登录态、cookie、tab 状态变化。

3. raw 模式必须保持严格。
   允许 shell 会让 opencli 变成第二个 terminal，破坏工具边界。

4. saved Markdown 读取必须限制在 workspace。
   否则 OpenCLI 输出可诱导读取任意本地文件。

5. fallback 到 web_fetch 不能假装是 OpenCLI 成功。
   需要在 verbose 或 metadata 中保留真实来源。

## 推荐结论

优先实现 Phase 1、Phase 2 和 Phase 4。

Phase 1 先修复 `opencli.enabled` 不生效和缺少 dry-run 的问题。Phase 2 给高风险 action 增加风险分级和 guard 接入，降低误执行站点副作用。Phase 4 引入 runner 注入，能让 handler 行为摆脱真实 OpenCLI 依赖，提升测试质量。Phase 3 和 Phase 5 作为第二批推进，分别处理 fallback 可解释性和 saved Markdown 路径安全。
