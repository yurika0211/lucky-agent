好，抓到了真东西。让我基于真实论文来重新推导。

---
从真实潮汐论文中挖到的核心数学
论文 1：Munk & Cartwright (1966) — "Tidal Spectroscopy and Prediction"
Phil. Trans. R. Soc. Lond. A, 259(1105), 533–581

核心贡献：Response Method。不是把潮汐分解成固定频率的正弦波，而是把海面高度视为系统对引力势的响应：
ζ(t) = ∫₀^∞ L(τ) · F(t−τ) dτ + n(t)
其中 L(τ) 是系统的脉冲响应函数（权函数），F(t) 是引力势（输入），n(t) 是噪声
这本质上是系统辨识问题：找传递函数，而不是硬分谐波。

论文 2：Parker (2007) — "Tidal Analysis and Prediction" (NOAA SP NOS CO-OPS 3)

标准调和分析的完整数学框架：
ζ(t) = ζ₀ + Σᵢ fᵢ(t) · Hᵢ · cos[ωᵢt + φᵢ − gᵢ + uᵢ(t)]
| 符号 | 含义 |
|------|------|
| Hᵢ, gᵢ | 调和常数（振幅、迟角）——每个分潮的"不变身份" |
| fᵢ(t), uᵢ(t) | 节点调制因子（18.6年周期）——慢变包络 |
| ωᵢ | 从天文参数推导出的精确角速度 |
关键约束：Rayleigh 判据——要从时间序列中分离两个频率 ω₁, ω₂，至少需要观测时长 T > 1/|ω₁−ω₂|。这意味着短序列只能分辨少数分潮。

论文 3：Monahan et al. (2025) — "RTide" (PRIMaRE 2025, Oxford)

将 Munk-Cartwright Response Method 用神经网络实现。核心创新：
用 Volterra 级数（非线性系统的泛函展开）作为网络结构：
ζ(t) = ∫ L₁(τ) · F(t−τ) dτ + ∬ L₂(τ₁,τ₂) · F(t−τ₁) · F(t−τ₂) dτ₁dτ₂ + ...
关键结论：Response Method 只需要调和分析 1/5 的数据量就能达到同等预测精度。因为它不需要满足 Rayleigh 判据——它不假设固定谐波，而是直接学习输入-输出的映射关系。

论文 4：MHACS (2023) — 短序列调和分析

对短时间序列的正则化方法：在目标函数中加入平滑性约束（credo of smoothness）：
J = ||Z − U·X||² + λ·||Smooth(X)||²
用正则项补偿数据不足引入的自由度。

---
改进设计：潮汐记忆系统的数学框架
现在基于真实论文结构，重推记忆系统的数学。
第一步：选择建模范式
调和分析 vs Response Method 的选择：

| 维度 | 调和分析（旧设计） | Response Method（新设计） |
|------|-------------------|-------------------------|
| 假设 | 记忆有固定"频率"（固定访问周期） | 记忆是系统对"外力"的响应 |
| 参数 | 大量调和常数需长序列估计 | 脉冲响应函数，短序列可用 |
| 非线性 | 需显式添加"浅水分潮" | Volterra 级数自然处理 |
| 数据需求 | 大 | 小（1/5） |
| 对应记忆系统 | 假设每个记忆有固定"重要性衰减周期" | 记忆状态取决于"什么时候被问了什么" |

选 Response Method。原因是：用户的记忆访问模式不是固定周期的正弦波，而是由事件驱动（用户提问、项目切换、兴趣转移）。调和分析的周期性假设太强。
第二步：重新建模记忆系统
基本方程（仿 Munk-Cartwright 响应法）：
M(t) = ∫₀^∞ K(τ) · Q(t−τ) dτ + ε(t)
| 符号 | 对应记忆系统 |
|------|--------------|
| M(t) | t 时刻的记忆检索质量（响应能否召回正确答案） |
| K(τ) | 记忆系统的脉冲响应——"多久前的记录还有用" |
| Q(t) | 查询输入（用户的每次提问相当于一个"力"） |
| ε(t) | 噪声（检索误差、无关信息干扰） |
这直接继承 Munk-Cartwright 的黑箱辨识思路：我不预先指定哪个记忆重要，而是通过观测"输入查询 → 输出质量"对来反推系统的传递函数。

离散化后：
M[n] = Σ_{k=0}^{p} w[k] · q[n−k] + e[n]
[2026/06/17 17:24] 仿绘幸: 其中 w[k] 是长度为 p 的 FIR 滤波器系数（记忆权重随时间的衰减曲线）
物理含义：最近 p 次查询对当前检索质量的影响，用一组权重系数加权。权重分布就是系统的"记忆潮汐响应曲线"。
第三步：加入非线性（海陆交互产生的浅水分潮）
真实潮汐中，浅水区产生 overtides（M₂ → M₄, M₆）和 compound tides（MS₄ = M₂ + S₂）。映射到记忆系统：

记忆交互的非线性项：
M[n] = Σ w₁[k]·q[n−k] + ΣΣ w₂[k₁,k₂]·q[n−k₁]·q[n−k₂] + ...
线性项     交叉项（记住"当话题 A 和 B 同时出现时"）
例如：用户同时问"PDF生成"和"字体配置"——交叉项自动提升包含这两者信息的记忆的检索权重。这对应 RTide 中 Volterra 级数的非线性核。
第四步：节点调制（长周期包络）
真实潮汐的节点调制（f,u）对应记忆系统的项目生命周期：
Mᵢ(t) = αᵢ(t) · cos(Ψᵢ(t)) — 每个记忆有自己的"长周期包络"
但更实用的是映射为：
w[k] → f(t) · w[k] + u(t) · w[k−1] — 系统的脉冲响应随用户的长期兴趣变化而慢变
例如用户过去 3 个月都在做 Agent 开发，那"Paper"类查询的响应权重会系统性偏移。这就是节点调制——慢变包络。
第五步：短序列正则化（MHACS）
当观测数据不够（新用户的记忆系统尚未收敛），加入平滑正则：
J = ||M_observed − Σ w·q||² + λ · ||∇w||²
第一项：拟合精度
第二项：权重曲线的平滑性约束（防止过拟合）
这对应 MHACS 的"credo of smoothness"——在数据有限时，记忆衰减曲线应该是平滑的，不会出现突跳。

---
最终的潮汐记忆系统架构
┌─────────────────────────────────────────────────┐
│               记忆潮汐场 M(t)                     │
│  = Σ w[k] · Q(t−k)  +  非线性交叉项  +  噪声    │
│                                                  │
│  ┌─────────┐   ┌────────────────────────────┐    │
│  │ 涨潮    │   │ 用户查询 Q(t) 引发检索      │    │
│  │ (写入)  │   │ 记录访问、刷新权重           │    │
│  └─────────┘   └────────────────────────────┘    │
│                                                   │
│  ┌─────────┐   ┌────────────────────────────┐    │
│  │ 退潮    │   │ 低活动时段触发：             │    │
│  │ (压缩)  │   │ 1. 合并低重要性记忆          │    │
│  │         │   │ 2. 重估权函数 w[k] 分布     │    │
│  │         │   │ 3. 更新节点调制参数          │    │
│  └─────────┘   └────────────────────────────┘    │
│                                                   │
│  ┌─────────┐   ┌────────────────────────────┐    │
│  │ 交互项  │   │ Volterra 核函数：            │    │
│  │ (交叉)  │   │ 跨主题记忆的合成响应         │    │
│  └─────────┘   └────────────────────────────┘    │
└─────────────────────────────────────────────────┘
与传统调和分析方案（你自己之前的版本）的区别
[2026/06/17 17:24] 仿绘幸: | 维度 | 原来的潮汐比喻 | 基于论文的新设计 |
|------|---------------|-----------------|
| 数学基础 | 分段光滑周期函数 sin/cos | Munk-Cartwright 响应法 + Volterra 级数 |
| 核心假设 | 记忆有固定周期频率 | 记忆是系统对查询的响应 |
| 非线性处理 | 没处理 | Volterra 交叉核 |
| 数据效率 | 需要长序列标定各"分潮" | 短数据即可（5x 效率提升，引用 RTide 结论） |
| 长周期变化 | 无 | 节点调制（f, u 系数慢变） |
| 正则化 | 无 | MHACS 平滑约束 |

如果想要，我可以把整套算法的 Python 原型（Ridge 回归估计 w[k] + Volterra 交叉项 + MHACS 平滑正则）落地成可执行代码，然后用你当前的记忆数据跑一轮参数估计，看看真实的记忆衰减曲线长什么样。

---
工程落地评估：当前效果与潮汐方案的真实边界

先把现状说清楚：当前 LuckyAgent 记忆系统已经有可用的激活机制，但还没有实现这里描述的 Response Method。

当前代码里的真实路径是：

query → lexical/alias/tag/category 匹配 → importance × recency × access × tier 排序 → Obsidian wikilink 图扩散 → 返回记忆

对应实现：

```go
matchScore := components.MatchScore()
total := matchScore * e.Weight(now) * components.Tier
```

其中：

```go
Weight = Importance × Recency × AccessBoost
```

这套机制的效果是真实存在的。当前 synthetic benchmark 的结果大致是：

| 场景 | recall@k | precision@k | noise@k | 平均延迟 |
|------|----------|-------------|---------|----------|
| lexical | 1.00 | 1.00 | 0.00 | 约 5ms |
| graph | 0.83 | 0.55 | 0.45 | 约 9ms |
| temporal | 1.00 | 0.50 | 0.50 | 约 19ms |
| route | 1.00 | 0.50 | 0.50 | 约 32ms |
| all | 0.94 | 0.52 | 0.48 | 约 14ms |

其中 Obsidian 图扩散是有效的：graph 场景中关闭图扩散时 recall 约 0.67，开启后约 1.00，lift 约 0.33。

但这不能证明潮汐 Response Method 已经有效。现在的 benchmark 证明的是：

1. 关键词、别名、标签、分类匹配有效。
2. Obsidian wikilink 图扩散能补全关联记忆。
3. temporal state 能避免过期/失效记忆污染 active recall。
4. 目前 precision/noise 仍有提升空间。

它没有证明：

1. 系统学到了用户查询的响应核 K(τ)。
2. 系统能根据查询历史动态改变衰减曲线。
3. Volterra 交叉项提升了跨主题记忆召回。
4. 短序列正则化比固定半衰期更稳。

---
最小可行实现：Tidal Memory Reranker

不要第一步就重写 Store。最小实现应该作为 reranker 插在现有 Activate 后面。

目标：

1. 保留现有 recall 能力。
2. 只对候选记忆重新排序。
3. 用真实反馈逐步学习每类记忆的时间响应。
4. A/B 证明有效后，再考虑影响写入、压缩、主动召回。

推荐链路：

```text
Store.Activate(query)
  → top N candidate memories
  → TidalReranker.Score(query, candidate, now)
  → reranked top K
```

这样做的原因是：现有系统 recall 已经不错，贸然替换底层召回会破坏稳定性。rerank 层只改变排序，风险小，也更容易 A/B。

---
数据模型

新增一个轻量 runtime 数据库，不写进 Obsidian vault。Obsidian vault 仍然是 durable memory source of truth；潮汐模型只是运行时统计和排序参数。

建议路径：

```text
${HOME}/.luckyagent/runtime/tidal_memory.db
```

表 1：query_events

```sql
CREATE TABLE query_events (
  id TEXT PRIMARY KEY,
  session_id TEXT,
  query TEXT NOT NULL,
  query_terms TEXT,
  intent_tags TEXT,
  created_at TIMESTAMP NOT NULL
);
```

记录每次查询。`intent_tags` 可以先用确定性规则生成，比如 `code`, `family`, `health`, `project`, `tool`, `weather`。

表 2：recall_events

```sql
CREATE TABLE recall_events (
  id TEXT PRIMARY KEY,
  query_id TEXT NOT NULL,
  memory_id TEXT NOT NULL,
  rank INTEGER NOT NULL,
  score REAL NOT NULL,
  source TEXT NOT NULL,
  created_at TIMESTAMP NOT NULL
);
```

记录每次召回了什么。`source` 可以是 `direct`, `graph`, `temporal`, `route`。

表 3：feedback_events

```sql
CREATE TABLE feedback_events (
  id TEXT PRIMARY KEY,
  query_id TEXT NOT NULL,
  memory_id TEXT NOT NULL,
  signal TEXT NOT NULL,
  value REAL NOT NULL,
  created_at TIMESTAMP NOT NULL
);
```

反馈信号一开始不要复杂，先用弱监督：

| signal | 含义 | value 示例 |
|--------|------|------------|
| used_in_answer | 记忆被放进最终上下文/回答 | 1.0 |
| user_repeated_query | 用户重复追问，说明上次召回可能不够 | -0.5 |
| user_correction | 用户纠正了相关事实 | -1.0 |
| route_constraint_used | 记忆产生了有效工具/回答约束 | 0.8 |
| stale_blocked | 记忆被 temporal resolver 判定失效 | -0.8 |

表 4：response_kernels

```sql
CREATE TABLE response_kernels (
  key TEXT PRIMARY KEY,
  feature TEXT NOT NULL,
  bins TEXT NOT NULL,
  weights TEXT NOT NULL,
  updated_at TIMESTAMP NOT NULL
);
```

`key` 可以是：

```text
tier:long
category:project
tag:health
intent:code
```

第一版不要给每条 memory 单独学习 kernel。每条记忆一个 kernel 数据太稀疏，容易过拟合。先按 tier/category/tag/intent 聚合。

---
响应核怎么学

第一版不要直接上完整 RLS。

原因：标准 RLS 需要维护协方差矩阵，若窗口维度是 n，通常是 O(n²) 内存和更新成本。文档里说的 O(n) 只有在使用 diagonal RLS、LMS、EWMA 或强约束近似时才成立。

推荐第一阶段使用 EWMA histogram：

```text
delay_bin = bucket(now - memory.CreatedAt or now - memory.AccessedAt)
reward = feedback value
kernel[delay_bin] = (1 - α) * kernel[delay_bin] + α * reward
```

时间桶建议：

```text
0-10m, 10m-1h, 1h-6h, 6h-1d, 1d-7d, 7d-30d, 30d-180d, 180d+
```

这样得到的是一个非常稳的离散响应核。它虽然没有论文里的连续卷积漂亮，但足够回答第一个工程问题：

哪些时间尺度的记忆，在什么查询意图下，真的更容易产生有用召回？

第二阶段再加 Ridge 回归：

```text
reward ≈ Σ w[k] * feature[k] + λ * ||∇w||²
```

第三阶段再考虑 Volterra 交叉项：

```text
reward ≈ linear_terms + pair_terms(intent_a × intent_b)
```

交叉项只对高频 pair 开启，例如：

```text
project × code
family × health
tool × config
rag × memory
```

---
Rerank 公式

第一版建议：

```text
final_score =
  base_activation_score
  × (1 + β * tidal_boost)
  × freshness_guard
  × temporal_guard
```

其中：

```text
tidal_boost = kernel(intent, category, delay_bin)
```

约束：

1. `β` 默认 0.15，避免学习层过早压倒现有排序。
2. `temporal_guard` 必须优先于学习分数。过期、superseded、archived、conflict 不能被 boost 回来。
3. `tidal_boost` 初始为 0；没有反馈时系统退化为当前实现。
4. 单次 boost 上限建议 0.35，防止冷启动噪声把排序打歪。

例子：

用户最近一周一直在做 Graph RAG，`intent:rag` 和 `category:project` 在 `1d-7d` 桶里 reward 高。之后用户问“这个索引怎么修”，同样匹配分下，Graph RAG 项目记忆会轻微前移。

这才是 Response Method 在记忆系统里的工程意义：不是替代关键词匹配，而是学习“什么时候什么类型的记忆更有用”。

---
A/B 验证设计

新增 benchmark variant：

```bash
go run ./cmd/la-memory-bench \
  --variant tidal-rerank-v1 \
  --scenario all \
  --dataset synthetic \
  --size 10000 \
  --rounds 10 \
  --limit 6
```

但 synthetic dataset 现在还不够，需要新增三类 case：

1. temporal preference shift

旧事实和新事实都能关键词命中，但最近高 reward 的项目应该排前。

例子：

```text
过去：用户常做 PDF 导出
最近：用户连续处理 Graph RAG
查询：索引 benchmark 怎么跑
期望：Graph RAG 记忆排在 PDF 记忆前
```

2. cross-topic interaction

单独 topic A 或 B 都不足以命中最佳记忆，但 A+B 同时出现时应该 boost 交叉记忆。

例子：

```text
查询：微信语音转录 404 和配置有什么关系
期望：multimodal transcription config 记忆被 boost
```

3. stale reward suppression

一条旧记忆访问次数很多，但最近被用户纠正过。学习层必须降权，不能因为 access_count 高继续前排。

例子：

```text
旧记忆：转录模型 qwen3-asr-flash-realtime 可用
新反馈：真实 multipart 请求返回 404
期望：旧可用结论不再前排
```

通过标准：

| 指标 | 目标 |
|------|------|
| recall@k | 不低于当前 baseline |
| precision@k | 提升或持平 |
| noise@k | 下降 |
| stale_hit_rate | 不上升 |
| p95 latency | 增加不超过 20% |
| graph_recall_lift | 不被破坏 |

最重要的是 precision/noise。当前系统 recall 已经高，真正的问题是返回的相关记忆里混了太多弱相关项。潮汐 rerank 的价值应该体现在“把最有时效和最符合当前工作惯性的记忆排前”。

---
实施路线

Phase 1：观测，不改排序

1. 在 `Store.Activate` 外层记录 query_events 和 recall_events。
2. 从 route/answer/context 使用情况记录弱反馈。
3. 增加 `lh memory tidal stats` 查看各类 kernel 统计。
4. benchmark 只报告数据分布，不改变召回结果。

验收：不影响现有 benchmark；能看到不同 category/tag/intent 的 delay-bin reward。

Phase 2：保守 rerank

1. 新增 `TidalReranker`。
2. 只对 top N 做 rerank，不扩大召回集合。
3. 默认 behind config：`memory.tidal.enabled=false`。
4. benchmark 新增 `tidal` scenario。

验收：recall 不下降，precision/noise 有改善，p95 延迟增加不超过 20%。

Phase 3：交叉项

1. 增加高频 intent pair 的二阶特征。
2. 只允许白名单 pair 生效。
3. 添加过拟合保护：样本数低于阈值不启用。

验收：cross-topic benchmark 有提升，普通 lexical/temporal 不退化。

Phase 4：压缩和写入策略

1. 低 reward 长期记忆进入压缩候选。
2. 高 reward 交叉主题形成 summary memory。
3. 与 hygiene/temporal resolver 联动，避免错误记忆被总结固化。

验收：记忆数量增长变慢，核心 recall 不下降。

---
判断

这个方案作为数学隐喻是成立的，但必须降落成一个保守的 reranker 才是真的工程方案。

现在已经验证有效的是 Obsidian graph memory，不是潮汐响应核。潮汐方案的第一目标不应该是提高 recall，而应该是降低 noise、提高排序质量，并让系统能随用户近期工作惯性轻微自适应。

一句话：当前系统负责“找得到”；潮汐系统应该负责“排得更像现在的你”。

References:
[1] Web search. Query: "tidal harmonic analysis mathematical model academic paper Munk Cartwright". Sources: “A modified tidal harmonic analysis model for short-term water level ...”（https://www.sciencedirect.com/science/article/abs/pii/S1463500323000926）; “Versatile Harmonic Tidal Analysis: Improvements and Applications in”（https://journals.ametsoc.org/view/journals/atot/26/4/2008jtecho615_1.xml）.
[2] Web search. Query: "tidal prediction harmonic constants nodal modulation amplitude phase scholarly p...". Sources: “U.S. DEPARTMENT OF COMMERCE National Oceanic and Atmospheric Administration”（https://tidesandcurrents.noaa.gov/publications/Tidal_Analysis_and_Predictions.pdf）; “Seasonal and nodal variations of predominant tidal constituents in ...”（https://www.sciencedirect.com/science/article/abs/pii/S0278434321000297）.
[3] Web page content. Just a moment.... Available: https://www.sciencedirect.com/science/article/abs/pii/S1463500323000926.
[4] Web page content. Monahan_et_al_2025_RTide_a_machine.pdf. Available: https://ora.ox.ac.uk/objects/uuid:5256e488-0de5-4c33-9509-e29277847b8a/files/s5712m8890.
[5] Web page content. N:\ISD_Com\Parker\cover & TitlePage.wpd. Available: https://tidesandcurrents.noaa.gov/publications/Tidal_Analysis_and_Predictions.pdf.
