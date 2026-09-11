# file_patch Tool

`file_patch` 是 LuckyAgent 的内置文件局部修改工具，用来对已有本地文件做原地编辑。它适合修改少量代码、配置、文档段落，或者应用带上下文的行级补丁。

和 `file_write` 不同，`file_patch` 不要求调用方提供完整文件内容。它通过精确字符串替换或行级 diff hunk 修改目标文件，因此更适合保留文件中未触及的内容。

## 工具定义

实现位置：

- `internal/tool/builtin_fs.go`

注册信息：

```go
Name:       "file_patch"
Category:   CatBuiltin
Source:     "builtin"
Permission: PermApprove
ShellAware: true
```

含义：

- `CatBuiltin`：这是核心内置工具，不依赖 skill 或 MCP。
- `PermApprove`：修改文件会改变磁盘状态，默认需要审批。
- `ShellAware`：agent 可以向工具注入当前工作目录 `_cwd`，相对路径会基于该目录解析。

## 参数

`file_patch` 接收这些参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `path` | 是 | 无 | 要修改的目标文件路径。 |
| `match` | 否 | 无 | 精确匹配的原始文本。 |
| `replace` | 否 | 无 | 替换后的文本。 |
| `diff` | 否 | 无 | 行级 diff hunk。提供非空 `diff` 时优先使用 diff 模式。 |
| `occurrence` | 否 | `1` | 当 `match` 出现多次时，替换第几个匹配项，1-based。 |
| `replace_all` | 否 | `false` | 是否替换所有精确匹配项。 |

## 两种修改模式

`file_patch` 有两种模式：

1. 精确字符串替换：使用 `match` + `replace`。
2. 行级 diff hunk：使用 `diff`。

如果 `diff` 非空，工具会优先进入 diff 模式，忽略 `match`、`replace`、`occurrence` 和 `replace_all`。

## 精确字符串替换

精确字符串替换适合小范围、确定文本的修改。

示例：

```json
{
  "path": "docs/example.md",
  "match": "old text",
  "replace": "new text",
  "occurrence": 1,
  "replace_all": false
}
```

执行逻辑：

1. 读取目标文件。
2. 统计 `match` 在文件中出现的次数。
3. 如果 `replace_all=true`，替换所有匹配。
4. 否则替换第 `occurrence` 个匹配。
5. 写回完整文件。

成功输出类似：

```text
Patched /path/to/file.md (1 replacement)
```

多个替换时：

```text
Patched /path/to/file.md (3 replacements)
```

### 精确替换错误

如果没有提供 `replace`，返回：

```text
replace is required
```

如果 `match` 为空或只有空白，返回：

```text
match must not be empty
```

如果找不到 `match`，返回：

```text
match text not found in /path/to/file
```

如果 `occurrence` 超过匹配次数，返回：

```text
occurrence <N> exceeds <M> matches in /path/to/file
```

如果 `occurrence<=0`，实现会把它回退为 `1`。

## replace_all 行为

默认 `replace_all=false`，只替换一个匹配项。

当 `replace_all=true` 时，工具会调用：

```go
strings.ReplaceAll(content, match, replace)
```

并把替换数量设为 `strings.Count(content, match)`。

只有在确认所有匹配都应该被替换时，才应使用 `replace_all=true`。

## 行级 diff hunk

diff 模式适合更复杂的多行修改，尤其是需要包含上下文、删除行、插入行的场景。

示例：

```json
{
  "path": "docs/example.md",
  "diff": "@@\n Old line\n-Text before\n+Text after\n Another line"
}
```

diff 行规则：

- 以空格开头：上下文行，修改前后都保留。
- 以 `-` 开头：删除行。
- 以 `+` 开头：新增行。
- 以 `@@` 开头：hunk 分隔符。
- 以 `--- ` 或 `+++ ` 开头：文件头，会被忽略。
- `\\ No newline at end of file` 会被忽略。

空行必须也带前缀，例如：

```text
 
```

也就是一个空格表示上下文空行，不能传真正的空字符串行。否则会返回：

```text
diff line <N> must start with space, '+', '-', or '@@'
```

成功输出类似：

```text
Patched /path/to/file.md (1 hunk)
```

多个 hunk 时：

```text
Patched /path/to/file.md (2 hunks)
```

## diff 匹配方式

diff 模式不是直接调用系统 `patch` 命令。它在 Go 代码里解析 hunk，然后用行序列匹配目标文件。

每个 hunk 会构造：

- `before`：上下文行和删除行组成的修改前序列。
- `after`：上下文行和新增行组成的修改后序列。

工具会在目标文件行列表中查找 `before` 序列。找到后，用 `after` 替换这段行。

如果某个 hunk 匹配不到，返回：

```text
diff hunk <N> did not match target file
```

## 换行处理

diff 模式会把 `\r\n` 规范化为 `\n`。

工具会记录目标文件原本是否以换行结尾：

- 原文件以 `\n` 结尾：写回时保留末尾换行。
- 原文件不以 `\n` 结尾：写回时不强行添加末尾换行。

## 路径解析

`file_patch` 和其他文件工具共用路径解析逻辑：

- 支持 `~` 和 `~/...` 展开到当前用户 home。
- 相对路径优先相对 `_cwd` 解析。
- `_cwd` 本身必须通过 sandbox 校验才会被采用。
- 路径清理后如果包含 `..`，会被拒绝。
- 最终路径必须通过 `validateSandbox`。

示例：

```json
{
  "path": "docs/tools/example.md",
  "match": "Old heading",
  "replace": "New heading"
}
```

如果当前 `_cwd` 是仓库根目录，这会解析到仓库下的 `docs/tools/example.md`，前提是 sandbox 允许该路径。

## 访问限制

`file_patch` 使用 `validateSandbox` 做路径限制。当前允许范围包括：

- `~/.luckyagent/`
- 系统临时目录，例如 `/tmp/`
- `/dev/null`
- 测试场景下，如果 home 目录名是 `.lh-home`，也允许该 home 目录

明确拒绝的敏感路径包括：

- `~/.nanobot`
- `~/.ssh`
- `~/.gnupg`
- `~/.aws`
- `~/.config/gcloud`
- Windows gcloud 配置目录
- `/etc/shadow`
- `/etc/ssh`

此外，路径中包含 `..` 会被视为路径穿越并拒绝。

## Agent 层执行保护

agent 的 `tool_execution_guard` 会根据用户当前请求中的限制拦截文件修改行为。

如果用户说了：

- `只读`
- `只查看`
- `不要修改文件`
- `不要写文件`
- `不要写入文件`

那么 `file_patch` 调用会被阻止。

这层保护用于尊重用户当前意图，但不替代审批。

## 适合使用的场景

优先使用 `file_patch` 的场景：

- 修改已有文件中的一个函数、段落或配置项。
- 替换一个明确字符串。
- 对多行代码应用带上下文的修改。
- 保留文件中未触及内容。
- 避免用 `file_write` 覆盖整个文件。

示例：

```json
{
  "path": "docs/example.md",
  "match": "Status: draft",
  "replace": "Status: final"
}
```

## 不适合使用的场景

不优先使用 `file_patch` 的场景：

- 创建全新文件：使用 `file_write`。
- 完整重写文件：使用 `file_write`。
- 创建目录：使用 `file_mkdir`。
- 移动文件：使用 `file_move`。
- 删除文件：使用 `file_delete`。
- 读取文件：使用 `file_read`。
- 修改二进制文件、图片、PDF、Office 文档。

## 常见调用示例

替换第一个匹配项：

```json
{
  "path": "docs/example.md",
  "match": "draft",
  "replace": "final",
  "occurrence": 1,
  "replace_all": false
}
```

替换第二个匹配项：

```json
{
  "path": "docs/example.md",
  "match": "TODO",
  "replace": "DONE",
  "occurrence": 2
}
```

替换所有匹配项：

```json
{
  "path": "docs/example.md",
  "match": "LuckyHarness",
  "replace": "LuckyAgent",
  "replace_all": true
}
```

使用 diff hunk：

```json
{
  "path": "docs/example.md",
  "diff": "@@\n # Title\n-Old paragraph\n+New paragraph\n"
}
```

## 和 file_write 的关系

`file_patch` 修改已有文件的一部分。

`file_write` 写入完整文件，并可能覆盖全部内容。

判断标准：

- 只改局部，保留其他内容：`file_patch`。
- 创建新文件或完整重写：`file_write`。

为了小修改使用 `file_write` 覆盖整个文件风险更高，容易丢失未注意到的内容。

## 和 terminal 的关系

不要优先用 `terminal` 手写：

```sh
sed -i 's/old/new/' file
```

也不要优先用：

```sh
perl -pi -e 's/old/new/g' file
```

正常局部修改应使用 `file_patch`，因为它有统一的路径解析、权限标记、sandbox 校验、可描述的替换语义和清晰的返回结果。

`terminal` 只应在需要运行项目脚本、格式化工具、测试命令或外部 CLI 时使用。

## 风险和注意事项

`file_patch` 的主要风险是误匹配。

调用前应确认：

- `match` 足够具体，不会改错位置。
- 使用 `replace_all=true` 前确认所有匹配都该替换。
- 使用 `occurrence` 前确认匹配数量和顺序。
- 使用 diff hunk 时上下文行与目标文件当前内容一致。
- 用户没有要求只读或禁止修改文件。

不确定时，先用 `file_read` 读取目标段落，再构造补丁。

## 维护注意事项

如果后续修改 `file_patch`，需要同步检查：

- 参数说明是否仍与 `FilePatchTool()` 一致。
- `diff` 是否仍优先于 `match` / `replace`。
- `occurrence` 默认值是否仍是 `1`。
- `replace_all` 默认值是否仍是 `false`。
- 精确替换是否仍使用 `strings.Count` 和 `strings.ReplaceAll`。
- diff 行前缀规则是否变化。
- diff hunk 匹配算法是否变化。
- 末尾换行保留策略是否变化。
- 写回文件权限是否仍是 `0644`。
- 路径解析和 sandbox 规则是否变化。
- agent 层 `tool_execution_guard` 对写入意图的拦截规则是否变化。

