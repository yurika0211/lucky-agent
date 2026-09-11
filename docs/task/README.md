# LuckyAgent Task Docs

## 目标

本目录记录 LuckyAgent 的任务收束、任务观测和任务优化方案。

一句话版：先说明 agent 为什么能停下来，再说明如何在任务开始前选择更合适的执行路径，最后把执行结果回写成可复盘、可学习的 outcome。

## 文档地图

| 文档 | 作用 | 适合阅读时机 |
| --- | --- | --- |
| `la-agent-task-convergence.md` | 解释当前 Agent Loop 如何收束任务。 | 调试循环、重复工具调用、搜索不停止、memory gate、tool guard。 |
| `optimize/opt-task-mdp-dijkstra-01.md` | 设计 TaskOptimizer，用 Dijkstra 和 MDP 做任务路径优化。 | 准备实现任务级 planner、trace、outcome、MDP 学习。 |

## 推荐阅读顺序

```text
la-agent-task-convergence.md
  -> optimize/opt-task-mdp-dijkstra-01.md
```

原因：

- 当前收束机制是底座。没有先理解 `LoopConfig`、重复工具检测、搜索综合和 tool guard，TaskOptimizer 容易设计成越权控制器。
- TaskOptimizer 应先做 trace/advisory，再逐步接管低风险参数，不能直接替代 Agent Loop。

## 核心边界

Task 系统分三层：

```text
Convergence layer
  -> 控制一次 Agent Loop 何时停止。

Optimization layer
  -> 在执行前推荐路径、预算和验证策略。

Observation layer
  -> 在执行后记录 stop reason、actual path、outcome 和 feedback。
```

例子：

- 用户问“解释 MDP”：convergence layer 应让模型直接回答并停止，optimization layer 不应开启复杂 planner。
- 用户要求“修复 Go 单测并验证”：optimization layer 可以推荐 `inspect -> edit -> focused test -> final`，convergence layer 仍负责执行中防循环。
- 用户说“只读分析”：policy guard 必须让所有写路径不可达，即使历史 MDP 认为修改后测试成功率更高。

## 当前优先级

推荐按下面顺序实现：

1. `StopReason` 和 convergence trace。
2. TaskOptimizer trace-only。
3. `actual_path` 映射和 path deviation 记录。
4. advisory plan hint。
5. 低风险配置接管。
6. outcome 持久化。
7. MDP namespace learning。

不要一开始就让 MDP 直接控制执行。MDP（马尔可夫决策过程：从历史结果学习策略）只有在 outcome、验证结果和用户反馈足够干净时才有价值。

## 最小可交付

第一版只需要做到：

```text
RunLoop
  -> 记录 StopReason
  -> 记录 actual_path
  -> TaskOptimizer 生成 recommended_path
  -> 保存 trace
  -> 不改变执行
```

验收标准：

- 现有任务输出不变。
- 每个任务能看到为什么停止。
- trace 能比较 recommended path 和 actual path。
- 低置信度分类不影响执行。
- trace 不包含 token、cookie、secret 或长原始输出。
