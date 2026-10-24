# Agent 请求、验收与交付合同

## 请求服务边界

`request_budget.go` 和 `tool_schema_budget.go` 在 provider 调用边界统一计算请求预算，
同步、原生流式、模拟流式、前台恢复以及摘要/协作调用使用相同的检查入口。
当前回合固定 provider、输入预算和显式必需工具。文本估算包含 ContentParts，schema
按实际选中的工具计算；图片使用 detail 对应的固定余量，不声称等于供应商计费。

大量工具超出预算时，可选定义按当前任务意图和名称/描述相关性选择，目标是输入预算的
三分之一。已禁用/不在 allowlist 中的工具不会重新加入。显式点名、强制工具和最新工具
回合优先保留；必需定义本身过大时明确失败。小窗口下不会同时暴露所有可选工具，指定
需要的工具名或扩大合法模型上下文可以获得更多定义。剩余空间按完整工具回合裁剪历史，
保留当前请求和最新操作证据；不会经由文本转换丢失图片、附件或 blob 元数据。

## 完成验收服务边界

`completion_verifier.go` 只接受目标、验收条件、候选答案和不可变操作视图，通过注入的
模型调用完成审查，不持有 Agent 或队列。最近 24 项观察可缩减，尚未解决的失败单独
保留；即使证据超窗也报错，不把失败省略后宣布完成。

相同工具、规范化参数和 shell 范围的后续成功是确定性的重新核对。其他解决方式要求
验收引用更晚、已完成且未失败的真实操作。无关失败要求明确理由。每条验收条件都有
唯一索引和独立证据；未知 ID、失败证据、缺项或未核对失败会转为继续执行。
恢复一个已验收检查点时，也重新核对这份证明与当前账本是否一致。

这些规则验证记录身份和操作状态。条件含义、证据是否足够、失败是否无关仍存在模型
判断，不能替代具体业务的编译、文件内容、健康查询等实际工具检查。

## 可选资源与职责

`NewWithDependencies` 只强制配置、provider、session manager 和工具注册表。记忆、
RAG、delegate/autonomy/cron、MCP、media 和完整工具服务可省略。没有提供的产品引擎
不会被构造或注册；已有完整 bootstrap 的行为保持兼容。默认只补齐内存中的预算、
gateway 和 metrics。`execution_services.go` 管理可选引擎与 Agent 执行器的绑定。

这是运行时依赖和执行策略的收口。Agent 仍有兼容 API 和完整适配器，Go 编译依赖尚未
变成独立的最小循环模块；后续包级拆分应沿这些服务边界进行。

## 持久化成本

重复调用检测只需要结果相等性，检查点改存 `last_result_digests`，加载时可读取并迁移
旧的 `last_results`。消息/operations 原文继续保存。orchestrator 队列用紧凑 JSON 保存
同一个 version-1 结构，仍采用原有原子写、fsync、rename 和目录同步。

基准使用合成结果，分别测量 100/1000 次操作、每次保存后的累计编码字节与 P95 编码
时间，以及四任务队列的序列化和落盘持锁时间：

```bash
cd agent
GOWORK=off go test -run '^$' -bench BenchmarkCheckpointTrajectory -benchtime=1x .
# 在 orchestrator 仓库中
GOWORK=off go test ./autonomy -run '^$' -bench 'BenchmarkQueue(SnapshotEncoding|PersistLock)' -benchtime=30x
```

全量 checkpoint/队列的复杂度仍随历史增长。基准帮助区分模型上下文有界与存储成本，
没有将一次模拟基准等同于现网瓶颈。追加日志/周期快照需要另行设计恢复、备份和回收。

## CI 与发布

CI 独立检查 Agent 最低依赖图，也在 launcher 的实际 workspace 依赖图上运行请求、
验收、轻量构造和检查点兼容合同。固定录制式 provider/工具测试不调用付费模型。

`python3 scripts/verify-agent-graph.py` 比较 launcher 实际选择的 Agent 模块与源码中的
生产文件和 manifests，拒绝本机 replace。PR 阶段生成差异报告，允许先开发再发布依赖；
tag CI 及 Release 的前置 job 必须完全相同，并在公开依赖图重跑合同，成功后才构建
安装包。先发布 Agent 提交，再升级 launcher pin；两个图都能编译不再等于发布就绪。

没有新增配置字段，`config.example.json` 的预算字段继续使用 runtime schema。
当前不包含真实模型的任务成功率、费用或延迟门槛；这些需要明确模型、样本和费用预算。
