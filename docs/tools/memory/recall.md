# recall Tool

`recall` 是 LuckyAgent 的内置记忆召回工具，用来从 LuckyAgent Obsidian-compatible Markdown memory vault 中搜索已保存的用户偏好、项目事实、长期规则和可复用结论。

这是只读工具，不修改本地状态，因此被标记为自动批准。

## 工具定义

实现位置：

- `internal/tool/builtin_memory.go`
- `internal/tool/memory_service.go`

注册信息：

```go
Name:         "recall"
Category:     CatBuiltin
Source:       "builtin"
Permission:   PermAuto
ParallelSafe: true
```

## 参数

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `query` | 否 | 空字符串 | 要召回的事实或偏好。为空时返回最近 5 条记忆。 |

示例：

```json
{
  "query": "Python preference"
}
```

## 执行流程

`recall` 的执行过程是：

1. 检查 memory store 是否已初始化。
2. 读取可选参数 `query`。
3. 如果 `query` 为空，调用 `store.Recent(5)`。
4. 如果没有最近记忆，返回 `没有找到记忆`。
5. 如果 `query` 非空，调用 `store.Search(query)`。
6. 如果搜索为空，返回 `没有找到关于「<query>」的记忆`。
7. 如果有结果，最多格式化前 10 条。
8. 每条结果包含 category、tier、links、文件路径和 block id。

如果 handler 未配置，默认错误是：

```text
recall handler not configured
```

如果 store 未初始化，返回：

```text
memory store not initialized
```

## 输出格式

每次有结果时，输出前都会说明记忆源：

```text
记忆源：LuckyAgent Obsidian-compatible Markdown vault at /home/user/.luckyagent/memory。RAG SQLite 不是当前 durable memory 事实源。
```

query 为空时：

```text
最近的记忆：
- [preference/medium] 用户喜欢Python
```

query 非空时：

```text
找到 1 条关于「Python」的记忆：
- [preference/medium @/home/user/.luckyagent/memory/preference.md#abc123] 用户喜欢Python
```

如果记忆有 links，格式类似：

```text
[project/long links=LuckyAgent,Tools @path#block]
```

links 最多显示 4 个。

content 会被截断：

- 最近记忆：80 字符。
- 搜索结果：100 字符。

## 一跳图扩展能力

`recall` 不是简单的纯关键词查找。它调用的是：

```go
store.Search(query)
```

而 `Store.Search` 内部使用：

```go
s.Activate(query, DefaultActivationOptions())
```

默认 activation 参数包含：

```go
IncludeGraph:      true
MaxGraphDepth:     1
MaxGraphBoost:     0.45
MaxGraphSeeds:     12
UpdateAccessStats: true
```

这表示普通 `recall` 支持浅层图扩展：先找到直接命中的记忆 seed，再通过 Obsidian wikilink、backlink、alias backlink 和 shared tag 给相关记忆加权召回。

当前实现可以通过一跳关系把相关记忆带出来。例如：

```text
Outdoor walks often include [[Daughter]].
[[Daughter]] has [[Pollen Allergy]].
```

查询：

```text
Outdoor walks
```

可能会召回包含 `Pollen Allergy` 的相关记忆，因为两条记忆共享 `[[Daughter]]` 关系。

但这不是多跳查询能力。当前 `MaxGraphDepth` 默认是 1，而且 `spreadActivationGraphLocked` 只从直接命中的 seeds 做一轮扩展；它不是无限深度的路径搜索，也没有给 `recall` 暴露 `depth` 参数。因此更准确的说法是：

- 支持一跳图传播召回。
- 支持通过 wikilink/backlink/alias/shared tag 拉起邻近记忆。
- 不支持用户指定多跳深度。
- 不保证沿 A -> B -> C -> D 做任意深度链式查询。

## 适合使用的场景

优先使用 `recall` 的场景：

- 用户问“你还记得我之前说过什么吗”。
- 需要确认用户偏好、项目规则、长期上下文。
- 当前任务可能受历史约束影响。
- 回答前需要避免重复询问已经保存的信息。
- 需要查 Markdown memory vault，而不是 RAG 索引。
- 需要利用已保存记忆中的 Obsidian wikilink 关系召回一跳相关事实。

示例：

```json
{
  "query": "LuckyAgent 文档风格"
}
```

## 不适合使用的场景

不优先使用 `recall` 的场景：

- 查项目代码，应使用文件工具或 `terminal`。
- 查已索引文档知识库，应使用 `rag_search`。
- 查当前会话最新消息，应直接看会话上下文。
- 查网页或外部事实，应使用搜索/抓取工具。

## 和 remember 的关系

`remember` 写入 durable memory，`recall` 读取 durable memory。

典型流程：

```text
remember -> recall
```

如果事实没有被记住，`recall` 不应该编造，只会返回没有找到。

## 和 RAG 的关系

`recall` 明确声明：

```text
RAG SQLite 不是当前 durable memory 事实源。
```

这意味着：

- 用户偏好、身份、长期规则：优先 `recall`。
- 已索引文档、资料、final answer artifact：优先 `rag_search`。

## 维护注意事项

如果后续修改 `recall`，需要同步检查：

- 参数名是否仍是 `query`。
- query 为空时是否仍返回最近 5 条。
- 搜索结果是否仍最多显示 10 条。
- 输出是否仍包含 memory source notice。
- links 显示数量是否仍限制为 4。
- content 截断长度是否变化。
- `DefaultActivationOptions()` 是否仍启用 `IncludeGraph`。
- 图扩展深度和 boost 参数是否变化。
