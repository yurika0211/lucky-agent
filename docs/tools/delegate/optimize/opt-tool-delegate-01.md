# opt-tool-delegate-01

## 目标

优化 LuckyAgent delegate 工具组的任务生命周期、并发控制、状态持久化、取消能力、结果可观测性和注册边界，让它从“可用的异步委派工具”升级为“可恢复、可审计、可控并发的子代理任务系统”。

本方案覆盖：

- `delegate_task`
- `task_status`
- `list_tasks`
- `delegate_parallel`
- `delegate_to_skill`
- `delegate_to_mcp`

当前默认注册的是：

- `delegate_task`
- `task_status`
- `list_tasks`

`delegate_parallel`、`delegate_to_skill`、`delegate_to_mcp` 已有工具定义和测试，但默认服务装配路径还没有注册。

## 当前状态

相关实现：

- `internal/tool/delegate.go`
- `internal/tool/delegate_enhanced.go`
- `internal/tool/services.go`
- `internal/agent/agent.go`
- `docs/tools/delegate/delegate.md`

当前核心结构：

```go
type DelegateManager struct {
	mu            sync.RWMutex
	config        DelegateConfig
	tasks         map[string]*DelegateTask
	nextID        int
	agentExecutor AgentExecutorFunc
}
```

默认配置：

```go
MaxConcurrent: 3
Timeout:       120 * time.Second
AutoApprove:   false
```

`delegate_task` 流程：

1. 校验 `description`。
2. 解析 `context` 和 `timeout`。
3. 统计当前 running 任务数。
4. 超过 `MaxConcurrent` 则拒绝。
5. 生成 `task-<n>`。
6. 创建 delegate workspace。
7. 将任务写入内存 map。
8. 启动 goroutine 异步执行。
9. 立即返回 task id 和 running 状态。

Agent 初始化时会设置真实 executor：

```go
supportRT.delegateMgr.SetAgentExecutor(func(ctx context.Context, description, contextStr string) (string, error) {
	sess := memoryRT.sessions.NewWithTitle("delegate-task")
	...
	loopCfg := DefaultLoopConfig()
	loopCfg.AutoApprove = false
	loopCfg.MaxIterations = 5
	result, err := a.RunLoopWithSession(ctx, sess, prompt, loopCfg)
	...
})
```

因此，当前 `delegate_task` 在完整 Agent runtime 里会真正走 Agent Loop；在没有 executor 的测试或轻量装配中会返回占位结果。

## 主要问题

### 1. 任务状态只在内存中

当前任务记录保存在 `DelegateManager.tasks` 里。进程退出、重启或 manager 重建后，任务状态丢失。

影响：

- `task_status` 无法查询历史任务。
- Telegram/QQ 等网关里用户拿到的 task id 在重启后失效。
- 长任务完成结果没有持久化审计记录。
- 无法区分“任务不存在”和“任务曾存在但运行时已重启”。

delegate 是异步工具，状态持久化比同步工具更重要。

### 2. 没有取消工具

代码里定义了 `StatusCancelled`，但工具层没有 `delegate_cancel`。

当前用户只能：

- 等任务完成；
- 等 timeout；
- 或通过外部方式停止整个运行时。

缺少单任务取消会导致：

- 错误委派后无法撤销；
- 长任务占用并发槽；
- 子代理可能继续执行无意义工作；
- UI 和渠道层无法给出明确的取消操作。

### 3. 并发控制是“拒绝”而不是“排队”

`delegate_task` 当前统计 running 数，如果达到 `MaxConcurrent` 就直接返回错误：

```text
max concurrent tasks reached
```

这能保护资源，但体验较硬。对异步任务系统而言，更合理的是：

- running 满了则进入 pending queue；
- worker 按并发限制拉取任务；
- `task_status` 能看到 `pending`；
- 用户可以取消 pending 任务。

当前 `delegate_enhanced.go` 已有 `PriorityTaskQueue`，但没有接入 `delegate_task` 主路径。

### 4. timeout 参数缺少边界

`delegate_task` 接收 `timeout`，直接转成 `time.Duration(timeout)*time.Second`。

当前问题：

- 没有最小值。
- 没有最大值。
- 负数和 0 的语义不明确。
- 超大值可能导致长期 goroutine 和资源占用。

建议把 timeout 做配置化边界：

```text
min_timeout_seconds
max_timeout_seconds
default_timeout_seconds
```

### 5. 任务 ID 只用内存递增

当前任务 ID 是：

```text
task-1
task-2
parallel-task-3
skill-4
mcp-5
```

问题：

- 重启后 ID 会重复。
- 多运行时实例无法区分来源。
- 外部渠道引用 task id 时容易撞。

建议改为时间前缀或 ULID 风格：

```text
task-20260703-153012-01H...
delegate_task_01h...
```

保持短 ID 展示可以另加 `short_id`。

### 6. 任务状态快照没有深拷贝

`handleStatus` 读取指针后释放锁，再 marshal 字段。虽然字段访问很快，但更稳妥的方式是锁内复制 `DelegateTask` 快照，再锁外 JSON marshal。

`handleList` 当前持有读锁构造整个列表，任务多时会阻塞写入。

建议统一：

- 锁内 copy。
- 锁外排序、截断和 marshal。

### 7. list_tasks 缺少过滤和排序

当前 `list_tasks` 返回所有任务，map 遍历顺序不稳定。

问题：

- 输出顺序不可预测。
- 任务多后结果过长。
- 用户无法只看 running / failed / recent。

建议增加参数：

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `status` | 空 | 按状态过滤。 |
| `limit` | `20` | 返回数量上限。 |
| `order` | `desc` | 按开始时间升序或降序。 |
| `include_result` | `false` | 是否包含结果摘要。 |

### 8. 结果输出没有结构化摘要

`task_status` 当前返回完整 `result`。如果子代理输出很长，会导致：

- tool result 过长；
- agent 上下文被挤占；
- Telegram/QQ 输出不稳定；
- 用户只想看状态时被长结果淹没。

建议把结果拆成：

```json
{
  "result_summary": "...",
  "result": "...",
  "result_bytes": 12345,
  "result_truncated": true
}
```

默认只返回摘要和截断结果，完整结果写入 artifact 文件。

### 9. workspace 生命周期不清晰

当前 workspace 会从 description/context 中识别，识别不到则使用：

```text
/tmp/luckyagent-delegate/<task-id>
```

问题：

- `/tmp` 下结果不持久。
- 没有 workspace 清理策略。
- 没有把 workspace artifact 和 task 状态绑定。
- `Allowed file roots` 固定为 `/tmp/` 和 `~/.luckyagent/`，但任务状态里没有记录 allowed roots。

建议默认 workspace 放到 runtime home：

```text
$LUCKYAGENT_HOME/delegate/tasks/<task-id>/workspace
```

并把 task metadata、logs、result、artifacts 放在同一任务目录。

### 10. delegate_parallel 是同步聚合

`delegate_parallel` 当前会启动多个 goroutine 并等待全部完成，然后返回汇总。

这和 `delegate_task` 的异步语义不同：

- `delegate_task` 返回 task id。
- `delegate_parallel` 阻塞到所有任务完成。

建议明确两种模式：

1. `delegate_parallel` 默认也创建一个 parent task，异步返回 parent task id。
2. 可选 `wait=true` 时同步等待。

否则模型可能误以为 `delegate_parallel` 和 `delegate_task` 都是异步。

### 11. delegate_to_skill / delegate_to_mcp 仍是占位执行

`delegate_to_skill` 和 `delegate_to_mcp` 当前执行函数里有注释：

```go
// Simplified — real implementation would invoke skill tools
// Simplified — real implementation would use MCPClient.CallTool
```

也就是说它们现在更像接口草图，不是真实 skill/MCP 委派。

如果注册给模型使用，需要先补真实执行路径；否则应该保持隐藏，避免模型产生错误能力假设。

### 12. 缺少任务事件流

当前状态查询是 pull 模式：

```text
delegate_task -> task_status -> task_status -> task_status
```

没有事件流或订阅能力。

对于 Telegram、HTTP SSE、Dashboard 来说，更好的模型是：

- task created；
- task started；
- tool call；
- progress；
- completed；
- failed；
- cancelled。

这样 UI 不必轮询，agent 也能更稳地把委派结果接回主任务。

## 优化原则

1. 保持 `delegate_task` 的默认定位：异步子代理任务，不替代普通同步工具。
2. 状态必须可恢复；至少已完成、失败、取消任务要可查询。
3. 执行必须可取消；context ownership 要明确。
4. 结果默认摘要化，完整内容写 artifact。
5. 默认注册只暴露真实可用工具；占位实现不要暴露给模型。
6. 不引入复杂分布式任务系统，先做单进程可恢复队列。

## 推荐方案

### 1. 引入 DelegateStore

新增持久化接口：

```go
type DelegateStore interface {
	SaveTask(ctx context.Context, task DelegateTaskRecord) error
	LoadTask(ctx context.Context, taskID string) (DelegateTaskRecord, bool, error)
	ListTasks(ctx context.Context, filter DelegateTaskFilter) ([]DelegateTaskRecord, error)
	AppendEvent(ctx context.Context, taskID string, event DelegateTaskEvent) error
	LoadEvents(ctx context.Context, taskID string, limit int) ([]DelegateTaskEvent, error)
}
```

本地实现优先使用 JSONL 或 SQLite。

建议路径：

```text
$LUCKYAGENT_HOME/delegate/tasks/<task-id>/task.json
$LUCKYAGENT_HOME/delegate/tasks/<task-id>/events.jsonl
$LUCKYAGENT_HOME/delegate/tasks/<task-id>/result.md
$LUCKYAGENT_HOME/delegate/tasks/<task-id>/workspace/
```

收益：

- 重启后仍能查任务。
- 长结果不用塞进内存。
- Dashboard 和渠道层可以读取 artifact。
- 方便后续做任务审计。

### 2. 扩展任务模型

建议新增内部 record，不直接扩大 tool 输出结构：

```go
type DelegateTaskRecord struct {
	ID            string
	ParentID      string
	Type          string
	Description   string
	Context       string
	Workspace     string
	Status        TaskStatus
	Priority      TaskPriority
	Timeout       time.Duration
	ResultSummary string
	ResultPath    string
	Error         string
	CreatedAt     time.Time
	StartedAt     time.Time
	CompletedAt   time.Time
	UpdatedAt     time.Time
	Metadata      map[string]string
}
```

`DelegateTask` 可以保留为运行时结构，但对外查询从 record 构造。

### 3. 增加 delegate_cancel

新增工具：

```text
delegate_cancel
```

参数：

| 参数 | 必填 | 说明 |
| --- | --- | --- |
| `task_id` | 是 | 要取消的委派任务。 |
| `reason` | 否 | 取消原因。 |

行为：

- pending：从队列移除，状态变为 `cancelled`。
- running：调用该任务的 cancel func，状态最终变为 `cancelled` 或 `failed`。
- completed / failed：返回不可取消。
- unknown：返回 task not found。

权限建议：

```text
PermApprove
```

取消正在执行的任务可能中断写操作，所以不建议自动批准。

### 4. 使用 worker queue 替代即时 goroutine

新增运行时结构：

```go
type delegateRuntimeTask struct {
	record DelegateTaskRecord
	cancel context.CancelFunc
}

type DelegateManager struct {
	mu       sync.RWMutex
	config   DelegateConfig
	store    DelegateStore
	queue    *PriorityTaskQueue
	running  map[string]*delegateRuntimeTask
	executor AgentExecutorFunc
}
```

执行模型：

```text
submit task
  -> SaveTask(pending)
  -> Enqueue
  -> worker pick
  -> SaveTask(running)
  -> executor
  -> SaveTask(completed/failed/cancelled)
```

并发由 worker 数控制，不再每次提交都自己判断 running 数。

### 5. 规范 timeout

扩展配置：

```go
type DelegateConfig struct {
	MaxConcurrent       int
	DefaultTimeout      time.Duration
	MinTimeout          time.Duration
	MaxTimeout          time.Duration
	AutoApprove         bool
	MaxTasksRetained    int
	MaxResultBytesInline int
}
```

参数处理：

```text
timeout <= 0 -> default
timeout < min -> min
timeout > max -> max 或返回错误
```

建议默认：

```text
default: 120s
min: 5s
max: 30m
```

### 6. 改进 task_status

新增参数：

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `include_result` | `true` | 是否返回结果文本。 |
| `include_events` | `false` | 是否返回最近事件。 |
| `event_limit` | `10` | 返回事件数量。 |

输出示例：

```json
{
  "task_id": "task-01H...",
  "status": "completed",
  "workspace": "...",
  "result_summary": "已完成代码检查，发现 2 个问题。",
  "result_path": "$HOME/.luckyagent/delegate/tasks/task-01H/result.md",
  "result_truncated": false,
  "error": "",
  "created_at": "...",
  "started_at": "...",
  "completed_at": "..."
}
```

对未完成任务，不要输出零值 `completed_at`，而是省略字段或返回空字符串。

### 7. 改进 list_tasks

新增参数：

```json
{
  "status": "running",
  "limit": 20,
  "order": "desc",
  "include_result": false
}
```

排序规则：

- 默认按 `created_at` 倒序。
- 同一时间下按 task id 排序，保证稳定。

输出中增加聚合：

```json
{
  "tasks": [],
  "count": 20,
  "total": 137,
  "by_status": {
    "pending": 4,
    "running": 3,
    "completed": 120,
    "failed": 8,
    "cancelled": 2
  }
}
```

### 8. 改进 delegate_task 输出

当前只返回 task id、running、workspace、message。

建议返回：

```json
{
  "task_id": "task-01H...",
  "status": "pending",
  "workspace": "...",
  "timeout_seconds": 120,
  "position": 2,
  "message": "Task queued. Use task_status to check progress."
}
```

如果 worker 立即启动，也可以是 `running`。

### 9. 结果 artifact 化

executor 返回后：

1. 写入 `result.md`。
2. 生成 `result_summary`。
3. 内联结果按 `MaxResultBytesInline` 截断。
4. `task_status(include_result=true)` 返回截断结果和 result path。

摘要生成可以先用简单规则：

- 第一段非空文本；
- 或前 N 字符；
- 后续再接 LLM summary。

### 10. 真实接入 skill / MCP 前保持隐藏

对 `delegate_to_skill`：

- 如果没有真实 skill runner，不注册。
- 注册前应能调用指定 skill 的入口，并把结果写回 task record。
- 校验 skill 是否存在。
- 记录 skill name、skill version 或 path。

对 `delegate_to_mcp`：

- 如果没有 MCP client registry，不注册。
- 校验 server 和 tool 是否存在。
- 支持 MCP 调用超时和错误结构化。
- 不要只返回占位成功。

### 11. 给 delegate_parallel 增加 parent task

建议输出：

```json
{
  "task_id": "parallel-01H...",
  "status": "running",
  "child_task_ids": ["task-01H-a", "task-01H-b"],
  "message": "Parallel task started. Use task_status on parent task."
}
```

parent task 完成时写入：

- success_count；
- failed_count；
- child summaries；
- final summary；
- child task ids。

可选参数：

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `wait` | `false` | 是否同步等待全部子任务完成。 |
| `fail_fast` | `false` | 是否有任务失败后取消剩余任务。 |

### 12. 增加事件流

新增事件类型：

```go
type DelegateTaskEvent struct {
	TaskID    string
	Type      string
	Message   string
	ToolName  string
	CreatedAt time.Time
	Metadata  map[string]string
}
```

事件类型：

```text
created
queued
started
progress
tool_call
tool_result
completed
failed
cancelled
```

短期先写 `events.jsonl`；中期可接到 HTTP SSE / WebSocket / gateway progress。

### 13. 补充配置项

建议在 `config.json` 增加：

```json
{
  "delegate": {
    "max_concurrent": 3,
    "default_timeout_seconds": 120,
    "min_timeout_seconds": 5,
    "max_timeout_seconds": 1800,
    "max_tasks_retained": 500,
    "max_result_bytes_inline": 4000,
    "persist": true
  }
}
```

如果不想新增顶层配置，也可以放入 `agent.delegate`，但要避免和 `autonomy` 混淆。

## 分阶段实施

### Phase 1：状态和输出稳定化

目标：不改变执行模型，先让现有工具更稳。

任务：

1. `handleStatus` 锁内复制 task snapshot。
2. `handleList` 支持排序、limit、status filter。
3. `completed_at` 未完成时返回空字符串。
4. timeout 参数增加 min/max 校验。
5. task result 增加截断字段。
6. 输出 JSON pretty 或稳定字段顺序。

验证：

```bash
go test ./internal/tool
```

重点测试：

- running task 的 `completed_at`。
- list 顺序稳定。
- result 截断。
- timeout 边界。

### Phase 2：持久化和 artifact

目标：重启后仍能查历史任务。

任务：

1. 新增 `DelegateStore`。
2. 实现本地文件 store。
3. `delegate_task` 创建任务时写 `task.json`。
4. 任务状态变化时保存。
5. 结果写 `result.md`。
6. `task_status` 从内存和 store 合并读取。

验证：

```bash
go test ./internal/tool
```

新增测试：

- 创建任务后磁盘存在 `task.json`。
- 完成后磁盘存在 `result.md`。
- 新 manager 加载后能查询已完成任务。

### Phase 3：取消和队列

目标：把并发限制从拒绝改成排队，并支持取消。

任务：

1. 引入 worker queue。
2. running task 记录 cancel func。
3. 新增 `delegate_cancel`。
4. pending/running/completed/failed/cancelled 状态转换测试。
5. `MaxConcurrent` 只控制 worker 数。

验证：

```bash
go test -race ./internal/tool
```

重点测试：

- 并发提交 100 个任务不丢失。
- MaxConcurrent 生效。
- cancel pending。
- cancel running。
- completed 任务不可取消。

### Phase 4：parallel parent task

目标：统一异步语义。

任务：

1. `delegate_parallel` 默认返回 parent task。
2. child task 与 parent task 关联。
3. parent 汇总 child 状态。
4. 增加 `wait` 参数保持同步兼容。

验证：

```bash
go test ./internal/tool
```

重点测试：

- parent task 完成条件。
- child failed 后 parent failed_count。
- `wait=true` 兼容旧同步输出。

### Phase 5：真实 skill / MCP 委派

目标：只有真实能力成熟后再默认注册。

任务：

1. 接入 skill runner。
2. 接入 MCP client registry。
3. 对目标不存在、权限不足、执行失败做结构化错误。
4. 在 `Services.RegisterAll` 中按能力条件注册。

验证：

```bash
go test ./internal/tool ./internal/agent
```

## 建议的工具参数变化

### delegate_task

新增：

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `priority` | `normal` | 任务优先级。 |
| `wait` | `false` | 是否同步等待完成。 |
| `timeout` | `120` | 保留，但增加边界。 |

### task_status

新增：

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `include_result` | `true` | 是否返回结果。 |
| `include_events` | `false` | 是否返回事件。 |
| `event_limit` | `10` | 事件数量上限。 |

### list_tasks

新增：

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `status` | 空 | 状态过滤。 |
| `limit` | `20` | 返回数量。 |
| `order` | `desc` | 排序方向。 |
| `include_result` | `false` | 是否返回结果摘要。 |

### delegate_cancel

新增工具：

| 参数 | 必填 | 说明 |
| --- | --- | --- |
| `task_id` | 是 | 要取消的任务。 |
| `reason` | 否 | 取消原因。 |

## 测试矩阵

### 单任务

- description 为空。
- timeout 缺省。
- timeout 小于最小值。
- timeout 大于最大值。
- executor 成功。
- executor 失败。
- executor 超时。
- 无 executor fallback。

### 并发

- 并发提交超过 `MaxConcurrent`。
- queue 模式下 pending 数正确。
- race test 无数据竞争。
- cancel running 后 goroutine 退出。

### 状态查询

- pending。
- running。
- completed。
- failed。
- cancelled。
- task not found。
- completed_at 未完成时为空。
- result 截断。
- result artifact path 存在。

### 持久化

- task.json 写入。
- events.jsonl 追加。
- result.md 写入。
- manager 重启后恢复 task。
- 损坏 JSON 文件时给出可诊断错误。

### parallel

- 空 tasks。
- 多 tasks 全成功。
- 部分失败。
- fail_fast。
- wait=true。
- parent/child 关系。

### skill / MCP

- 目标不存在。
- 权限不足。
- 执行超时。
- 执行失败。
- 返回大结果。

## 风险

### 1. 队列化会改变用户预期

当前超过并发直接报错。改成 pending queue 后，任务会被接受但延迟执行。

缓解：

- 输出里明确 `status=pending`。
- 返回队列位置。
- 提供 `delegate_cancel`。

### 2. 持久化会引入清理问题

任务多后会占用磁盘。

缓解：

- `max_tasks_retained`。
- 按时间清理。
- 保留 failed/cancelled 时间更长，completed 可更早清理。

### 3. 子代理写入风险

delegate 会启动新的 agent loop，可能执行工具。

当前子代理 `AutoApprove=false` 是正确方向。后续应继续保留：

- 子代理不自动批准危险工具。
- 子代理 workspace 明确。
- 子代理 allowed roots 明确。
- 子代理工具集可按任务类型收缩。

### 4. skill / MCP 过早注册会误导模型

如果 `delegate_to_skill` 和 `delegate_to_mcp` 没有真实执行路径，不应默认注册。

## 推荐优先级

优先做：

1. `task_status` / `list_tasks` 输出稳定化。
2. timeout 边界。
3. result 截断和 result artifact。
4. `delegate_cancel`。
5. 本地持久化。

中期做：

1. worker queue。
2. parallel parent task。
3. 事件流。
4. Dashboard / Telegram 任务进度展示。

后期做：

1. 真实 skill delegate。
2. 真实 MCP delegate。
3. 多进程任务恢复和 worker reclaim。

## 验收标准

完成优化后，delegate 工具组应满足：

- 任务提交后能稳定返回 task id。
- 任务重启后仍能查到最终状态。
- 用户能取消 pending/running 任务。
- `list_tasks` 输出稳定且可过滤。
- 大结果不会直接污染 agent 上下文。
- 子代理执行有明确 workspace 和 timeout。
- 默认注册的工具都是真实可用能力。
- `go test -race ./internal/tool` 无数据竞争。
