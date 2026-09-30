# LongMemEval 口径

公开结果里有两个检索分，都来自官方 `src/retrieval/eval_utils.py`。不要把它们当成同一个数。

| 名字 | 一道题怎么算对 | 谁在用 |
| --- | --- | --- |
| `recall_all@k` | 这道题的全部证据会话都在前 k 条里 | 官方 `print_retrieval_metrics.py` 打印的就是这个，论文表里的 Recall@k 也是这个 |
| `recall_any@k` | 前 k 条里至少有一段证据会话 | 2026 年仓库自报的 0.97 以上，用的是这个 |

`ndcg_any@k` 看证据排得靠不靠前。它不要求全部证据都在前 k 条里。

题号以 `_abs` 结尾的 30 道拒答题，两个口径都不算。分母是 470。

社区数字还有三个对不齐的地方。第一，有的系统按对话轮次或切块检索，不是按一整段会话。第二，有的先让模型改写问题，或者先从原文抽出事实再索引。第三，答题正确率是模型读完检索结果之后答对的比例，不是检索分。Zep 论文里的 0.712 是答题分。

Aestus 这次的入口两个口径都出。检索单位是整段会话，索引是原文，没有改写问题，也没有抽事实。

```bash
cd workspace/aestus
go test ./eval/ -run TestLongMemEvalDataset -count=1 -v \
  -longmemeval eval/data/longmemeval_s_cleaned.json
```

看输出里的 `recall_any` 才能和那些 0.97 的自报数字放在一起。看 `recall_all` 才能和论文、官方脚本放在一起。

# LongMemEval 检索报告

日期：2026-09-30。只测检索，没有让模型答题。

数据是 [xiaowu0162/LongMemEval](https://github.com/xiaowu0162/LongMemEval) 的 cleaned 版本，MIT。三份文件都在 `eval/data/`，不进 git。

| 文件 | 大小 | 每题对话段数 |
| --- | --- | --- |
| `longmemeval_oracle.json` | 15 MB | 只有证据，1–6 段 |
| `longmemeval_s_cleaned.json` | 265 MB | 38–62，中位数 48 |
| `longmemeval_m_cleaned.json` | 2.6 GB | 460–490，中位数 476 |

每份都是 500 题。题号以 `_abs` 结尾的 30 道是拒答题，官方检索脚本不给它们算分。有效题 470 道。

| 题型 | 题数 | 证据段数 |
| --- | --- | --- |
| temporal-reasoning | 127 | 通常 2 段以上 |
| multi-session | 121 | 通常 2 段以上 |
| knowledge-update | 72 | 2 段 |
| single-session-user | 64 | 1 段 |
| single-session-assistant | 56 | 1 段 |
| single-session-preference | 30 | 1 段 |

470 道里，证据只有 1 段的有 170 道，2 段的有 229 道，3 段及以上的有 71 道。

## 怎么跑

一段对话存成一条长期笔记。笔记正文是这段对话的原文，别名和标签只放会话编号，重要度统一是 0.5。用题目原文去搜，取前 10 条。搜索不更新访问次数。

计分和官方 `src/retrieval/eval_utils.py` 一致。

- `recall_all@k`：这道题的全部证据会话都出现在前 k 条，这题记 1，否则记 0。最后取 470 道的平均。官方打印和论文表用这个。
- `recall_any@k`：前 k 条里至少有一段证据会话，这题记 1。社区自报的高分用这个。
- `ndcg_any@k`：前 k 条里只要排进了证据会话就有分，排得越靠前越高。它不要求全部证据都在里面。

复跑：

```bash
cd workspace/aestus
go test ./eval/ -run TestLongMemEvalDataset -count=1 -v \
  -longmemeval eval/data/longmemeval_m_cleaned.json
```

把路径换成 S 或 oracle 即可。`-longmemeval-limit 20` 只跑前 20 题。

S 用了 22 秒，M 用了 3 分 30 秒。M 不能走逐条 `Save`：每保存一条都会把整个 vault 重写一遍，480 段会慢几个数量级。现在是整题一次性写成 Markdown，再用 `NewStore` 打开。

## 结果

2026-09-30 跑完。`recall_all` 和上次相同，说明改计分没有改检索。

|  | oracle | S | M |
| --- | --- | --- | --- |
| recall_any@5 | 1.000 | 0.904 | 0.704 |
| recall_any@10 | 1.000 | 0.955 | 0.774 |
| recall_all@5 | 0.994 | 0.696 | 0.404 |
| recall_all@10 | 1.000 | 0.815 | 0.504 |
| ndcg_any@5 | 1.000 | 0.746 | 0.495 |
| ndcg_any@10 | 1.000 | 0.778 | 0.526 |

和别人比的时候用第一行。S 的 0.904 对社区自报的 0.97 左右。M 的 0.704 对论文里 Stella 原文索引的 0.706，两边都是「前 5 条里有一段证据」。

`recall_all` 低一截，是因为 300 道题有两段以上的证据。只要有一段没进前 5，`recall_any` 仍是 1，`recall_all` 就是 0。单段题两种口径相同：用户事实在 M 上是 0.750，助手事实是 0.679，偏好是 0.133。

M 上 `recall_any@5` 的分类：

| 题型 | 题数 | recall_any@5 | recall_all@5 |
| --- | --- | --- | --- |
| knowledge-update | 72 | 0.944 | 0.417 |
| single-session-user | 64 | 0.750 | 0.750 |
| multi-session | 121 | 0.702 | 0.240 |
| temporal-reasoning | 127 | 0.693 | 0.323 |
| single-session-assistant | 56 | 0.679 | 0.679 |
| single-session-preference | 30 | 0.133 | 0.133 |

M 的 recall_all@5 按题型：

| 题型 | 题数 | oracle | S | M |
| --- | --- | --- | --- | --- |
| single-session-user | 64 | 1.000 | 0.922 | 0.750 |
| single-session-assistant | 56 | 1.000 | 0.911 | 0.679 |
| knowledge-update | 72 | 1.000 | 0.764 | 0.417 |
| temporal-reasoning | 127 | 0.976 | 0.638 | 0.323 |
| multi-session | 121 | 1.000 | 0.554 | 0.240 |
| single-session-preference | 30 | 1.000 | 0.467 | 0.133 |

## 这些数字说明什么

oracle 的 0.994 不能拿来当检索水平。那份文件里每道题只留证据会话，没有干扰。库里的会话数和标准答案的会话数完全一样。单段题只要搜到任何一条，前 5 名里就一定有答案。

oracle 上没进前 5 的只有 3 道，全是 temporal-reasoning，而且每道都有 6 段证据。6 段都被搜出来了，第 6 段排在第 6 名。`recall_all@5` 装不下它，所以这 3 道记 0。`recall_all@10` 因此是 1。题号是 `a3838d2b`、`gpt4_a1b77f9c`、`gpt4_7abb270c`。

S 和 M 把闲聊加回去之后，分数随段数下降：前 5 条全部命中从 0.994 掉到 0.696，再掉到 0.404。M 把前 10 条看完也只有一半的题能把证据找全。

掉得少的是单段用户事实。题目里的词经常还在用户原话里，词面搜索对得上。掉得多的是三类：

- 偏好。用户当时的说法和后来的问法经常不是同一批词。M 上 30 道里大约只有 4 道能在前 5 条找全。
- 多段综合。证据有两三段，只要有一段被闲聊挤出去，整题就是 0。
- 时间。题目问的是「之前」「几周」「从早到晚」，正文里通常是具体日期和事件，两边的词对不上。

搜索本身是词面分：问题整句出现在正文里加 1 分，每个词再加 0.22，两个词以上再加 0.25。这次别名、链接、重要度都没有帮忙。别名是会话编号，题目里不会出现。笔记之间没有链接，图也没有参与区分。重要度全是 0.5。会话日期间隔几个月，长期记忆 365 天的半衰期拉不出足够的分差。

`ndcg_any` 高于 `recall_all`，是因为很多题已经把其中一段证据排进前 5，只是没把全部证据都排进去。多段题在 `recall_all` 上会整题记 0，在 `ndcg_any` 上仍然有分。

## 这次没有测的

- 模型拿到前几条笔记之后，能不能把问题答对。
- 拒答题应不应该返回空。这 30 道被跳过了。
- 沿着 `[[链接]]` 走两跳。LongMemEval 的 multi-session 是几段互不链接的原文，不是 Aestus 的图多跳。
- 笔记被压缩、合并、改写之后再搜。这次存的是整段原文。
