# opt-tool-terminal-01

## 目标

优化 `terminal` 在 Agent 层的执行保护，让 `tool_execution_guard` 对用户约束的执行更稳定、更可测试，同时保持它的定位清晰：这是尊重当前用户请求的执行前拦截层，不是系统级安全沙箱，也不替代审批。

本方案聚焦 `terminal.command` 的写入、删除、push 识别。

## 当前状态

相关实现：

- `internal/agent/tool_execution_guard.go`
- `internal/agent/tool_execution_guard_test.go`
- `docs/tools/terminal.md`

当前 guard 的流程是：

1. 从用户输入中识别约束，例如 `只读`、`不要修改文件`、`不要删除`、`不要 push`。
2. 对不同 tool 做不同拦截。
3. 对 `terminal` 读取 `command` 参数。
4. 用字符串包含和简单 shell 单词匹配识别写入、删除、push。

当前识别方式主要包括：

- `strings.Contains(command, ">")`
- `strings.Contains(command, "sed -i")`
- `strings.Contains(command, "perl -pi")`
- `strings.Contains(command, "git add")`
- `strings.Contains(command, "git commit")`
- `hasShellWord(command, "rm")`
- `hasShellWord(command, "touch")`

这套逻辑简单、直接、容易理解，但已经开始暴露几个维护问题。

## 主要问题

### 1. 识别逻辑分散在 guard 内

`toolExecutionGuard` 同时负责三件事：

- 解析用户约束。
- 判断 tool 类型。
- 判断 shell 命令是否属于写入、删除、push。

这会让后续扩展越来越难。例如新增 `git clean`、`truncate`、`install`、`npm version`、`python -c open(..., "w")` 等识别时，guard 本体会继续膨胀。

### 2. 字符串包含容易误判和漏判

误判示例：

```sh
echo "git commit docs"
rg ">"
printf "rm -rf"
```

漏判示例：

```sh
git    push origin main
git -C repo push
rm -fr tmp
sed --in-place s/a/b/ file
python -c 'open("x", "w").write("a")'
cat file > out.txt
cat file 1>out.txt
cat file >> out.txt
```

当前方案不需要追求完整 shell 安全解析，但应该把“启发式识别”的边界显式化，并让高价值场景更稳定。

### 3. Guard 结果没有结构化原因

当前 shell 判断只返回 bool，最终原因由上层拼接。后续如果要记录日志、给用户解释、做测试表格，缺少结构化信息。

建议让命令分类器返回：

- 命令类别：`read`、`write`、`delete`、`push`、`unknown`
- 命中的规则名
- 命中的 token 或片段
- 是否高置信度

### 4. 测试覆盖偏少

现有测试覆盖了几个关键约束，但没有覆盖命令分类矩阵。后续修改识别规则时，容易无意改变行为。

## 优化原则

1. 不引入重型 shell parser 作为第一步。
2. 保持默认行为保守：用户明确只读时，宁可挡住明显风险命令。
3. 命令识别逻辑从 guard 中抽离，成为独立、可测试的小模块。
4. 对简单命令做 token 级识别，对复杂 shell 语法只做有限支持。
5. 明确文档说明边界：这是启发式拦截，不是沙箱。

## 推荐方案

新增一个内部命令分类器，例如：

```text
internal/agent/shell_command_classifier.go
```

核心 API：

```go
type shellCommandEffect string

const (
	shellEffectRead    shellCommandEffect = "read"
	shellEffectWrite   shellCommandEffect = "write"
	shellEffectDelete  shellCommandEffect = "delete"
	shellEffectPush    shellCommandEffect = "push"
	shellEffectUnknown shellCommandEffect = "unknown"
)

type shellCommandFinding struct {
	Effect     shellCommandEffect
	Rule       string
	Match      string
	Confidence string
}

func classifyShellCommand(command string) []shellCommandFinding
```

`tool_execution_guard.go` 只负责根据用户约束消费分类结果：

```go
findings := classifyShellCommand(lower)

if g.noPush && hasEffect(findings, shellEffectPush) {
	return "the user explicitly said not to push"
}
if (g.noDelete || g.noWrite || g.readOnly) && hasEffect(findings, shellEffectDelete) {
	return "the user explicitly disallowed deletion or mutation"
}
if (g.noWrite || g.readOnly || g.readOnlyExternal) && hasEffect(findings, shellEffectWrite) {
	return "the user requested a read-only/no-file-modification task"
}
```

这样可以把“用户约束解释”和“命令行为识别”拆开。

## 分类规则设计

### 第一阶段：轻量 token 化

实现一个轻量 tokenizer，目标不是完整解析 shell，而是比 `strings.Fields` 更稳：

- 保留引号内字符串的边界信息。
- 识别 shell 控制符：`;`、`&&`、`||`、`|`。
- 识别重定向：`>`、`>>`、`1>`、`2>`、`&>`、`>|`。
- 识别命令和参数的粗略 token。

可以先实现最小版本：

- 按空白切分。
- 额外拆出 `>`, `>>`, `1>`, `2>`, `&>`。
- trim 常见包裹符号。
- 不把引号内文本作为命令 token 参与强匹配。

### 第二阶段：规则表驱动

用规则表替代散落的 `strings.Contains`：

```go
var shellCommandRules = []shellCommandRule{
	{Effect: shellEffectDelete, Rule: "delete-command", Commands: []string{"rm", "rmdir", "unlink", "del"}},
	{Effect: shellEffectWrite, Rule: "write-command", Commands: []string{"tee", "touch", "mv", "cp", "chmod", "chown", "truncate"}},
	{Effect: shellEffectPush, Rule: "git-push", Command: "git", ArgsAny: []string{"push"}},
	{Effect: shellEffectWrite, Rule: "git-index-write", Command: "git", ArgsAny: []string{"add", "commit"}},
	{Effect: shellEffectWrite, Rule: "in-place-edit", Command: "sed", ArgsAny: []string{"-i", "--in-place"}},
	{Effect: shellEffectWrite, Rule: "perl-in-place-edit", Command: "perl", ArgsAny: []string{"-pi", "-p", "-i"}},
}
```

规则表可以继续扩展：

- `git clean` 归为 delete。
- `git reset --hard` 归为 write。
- `install`, `mkdir`, `ln` 归为 write。
- 包管理器安装命令归为 write，例如 `npm install`、`pnpm add`、`go install`。

是否纳入这些规则需要结合 LuckyAgent 的日常用法逐步推进，避免一次性过度拦截。

### 第三阶段：重定向识别

重定向建议单独作为规则：

- `>`：write
- `>>`：write
- `1>`：write
- `&>`：write
- `2>`：默认可先标记为 write，原因是会创建或覆盖 stderr 输出文件

注意：`rg ">"` 不应该因为字符串字面量中的 `>` 被误判。轻量 tokenizer 至少要知道 token 来自 quoted text 时，不把它作为 redirection。

### 第四阶段：复杂命令策略

对管道和多命令串联，按片段扫描，只要任一片段命中高风险 effect，就返回 finding。

例如：

```sh
cat a | tee b
git status && git push
rg x; rm tmp
```

这类命令应被识别为 write、push 或 delete。

## 分阶段实施

### Phase 1：抽离分类器，保持行为兼容

新增：

- `internal/agent/shell_command_classifier.go`
- `internal/agent/shell_command_classifier_test.go`

迁移现有函数：

- `shellCommandDeletes`
- `shellCommandWrites`
- `hasShellWord`

第一阶段不改变识别范围，只把现有行为搬进分类器，并补上测试。

验收标准：

- 现有 `go test ./internal/agent` 通过。
- `tool_execution_guard.go` 中 shell 识别逻辑明显变薄。
- 文档仍与现有行为一致。

### Phase 2：补齐高价值命令模式

新增识别：

- `git -C repo push`
- `git clean`
- `git reset --hard`
- `rm -fr`
- `rm --recursive --force`
- `sed --in-place`
- `>`、`>>`、`1>`、`&>` 的 token 级识别

验收标准：

- 新增表格测试覆盖每条规则。
- 保留 `echo "rm -rf"`、`rg ">"` 这类误判回归测试。

### Phase 3：结构化 finding 和日志

让分类器返回结构化 finding，并在 guard 阻止时保留规则名。

对用户展示仍保持简洁：

```text
Blocked by tool execution guard: the user requested a read-only/no-file-modification task.
```

内部日志或 debug 输出可以包含：

```text
rule=git-push effect=push match="git push"
```

验收标准：

- 用户可读的 block message 不变或基本不变。
- 测试可以断言命中规则，避免只断言非空 reason。

## 测试建议

新增表格测试：

### 应识别为 write

```sh
touch a
tee out
cat a > b
cat a >> b
sed -i s/a/b/ file
sed --in-place s/a/b/ file
perl -pi -e s/a/b/ file
git add .
git commit -m test
git reset --hard HEAD
chmod 600 file
```

### 应识别为 delete

```sh
rm file
rm -rf dir
rm -fr dir
rm --recursive --force dir
rmdir dir
unlink file
git clean -fd
```

### 应识别为 push

```sh
git push origin main
git -C repo push
git push --tags
```

### 不应误判

```sh
git status
git diff
rg ">"
echo "rm -rf"
printf "git push"
cat file
go test ./internal/agent
```

## 文档更新

完成实现后，同步更新：

- `docs/tools/terminal.md`

建议把当前“当前写入类命令识别包括”改成“当前高置信度写入识别包括”，并补一句：

```text
guard 使用轻量命令分类器做启发式识别，能覆盖常见写入、删除和 push 命令，但不会完整解释所有 shell 语法。
```

## 风险与边界

1. 误判无法完全消除。
   例如命令字符串里包含生成脚本、嵌套 shell、变量展开时，轻量分类器无法知道最终行为。

2. 漏判仍然存在。
   例如 `python -c`、`node -e`、自定义脚本、间接调用写入命令，除非做语言级或运行时级沙箱，否则无法可靠判断。

3. 不应该把 guard 包装成安全承诺。
   它只服务于“用户本轮明确约束”，真正的执行安全仍依赖 tool 权限、审批、运行环境隔离和系统策略。

4. 规则扩展要谨慎。
   过度拦截会让正常开发命令难以执行，尤其是用户明确要求实现修改时。

## 推荐结论

优先做 Phase 1 和 Phase 2。

Phase 1 解决结构问题，让 guard 更薄、更容易维护。Phase 2 提升常见命令识别质量，尤其是 `git -C repo push`、`rm -fr`、`sed --in-place` 和 token 级重定向。Phase 3 可以等需要更强 debug 或审计能力时再做。
