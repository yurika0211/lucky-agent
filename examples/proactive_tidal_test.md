# 触发 Tidal Proactive 系统方案

## 📋 系统概述

Proactive 系统通过 **Tidal Memory Reranker（潮汐记忆重排器）** 来学习和优化记忆检索。系统包含两个关键组件：

1. **Proactive Runtime Service** - 定期运行决策管道，收集运行时事件
2. **Tidal Memory Reranker** - 基于 EWMA 的记忆重排器，学习响应核（response kernel）

## 🎯 触发方案

### 方案 1：通过正常使用自动触发（推荐）

这是最自然的方式，系统在后台自动学习你的使用模式。

#### 步骤：

1. **确保 Proactive 已启用**

你的配置已正确设置：
```json
{
  "proactive": {
    "enabled": true,
    "dry_run": false,  // ⚠️ 注意：设为 false 才会实际执行
    "confidence_threshold": 0.60,
    "horizon_seconds": 300,
    "action_interval_seconds": 300
  }
}
```

2. **启动 LuckyAgent 服务**

```bash
# 启动 API 服务
go run ./cmd/la serve

# 或启动聊天
go run ./cmd/la chat
```

3. **正常使用系统**

Proactive 系统会自动收集以下事件：
- 🔧 **工具调用**（file_read, web_search, etc.）
- 💬 **聊天轮次**（chat_turn）
- 🧠 **记忆检索**（memory_recall）
- 📊 **RAG 查询**（rag_search）
- 🔄 **会话切换**（session_switch）

4. **Tidal 自动学习**

系统会：
- 每 5 分钟（300 秒）运行一次决策周期
- 收集运行时事件到 `~/.luckyagent/runtime/proactive.db`
- 学习记忆年龄和查询的响应核
- 调整记忆激活分数的 `TidalBoost` 加成

### 方案 2：通过编程方式主动触发

如果你想立即测试或手动控制 Proactive 系统。

#### 创建测试脚本

<function_calls>
<invoke name="Write">
<parameter name="content">package main

import (
	"context"
	"fmt"
	"time"

	"github.com/yurika0211/luckyagent/internal/config"
	"github.com/yurika0211/luckyagent/internal/proactive"
)

func main() {
	fmt.Println("=== Proactive Tidal 测试 ===\n")

	// 1. 加载配置
	mgr, err := config.NewManager()
	if err != nil {
		panic(err)
	}
	if err := mgr.Load(); err != nil {
		panic(err)
	}

	cfg := mgr.Get()
	if !cfg.Proactive.Enabled {
		fmt.Println("❌ Proactive 未启用，请在配置中启用")
		return
	}

	fmt.Printf("✓ Proactive 已启用\n")
	fmt.Printf("  Dry Run: %v\n", *cfg.Proactive.DryRun)
	fmt.Printf("  Confidence: %.2f\n", cfg.Proactive.ConfidenceThreshold)
	fmt.Printf("  Interval: %ds\n\n", cfg.Proactive.ActionIntervalSecs)

	// 2. 创建 Proactive Store
	storePath := cfg.Proactive.StorePath
	if storePath == "" {
		storePath = "~/.luckyagent/runtime/proactive.db"
	}

	store, err := proactive.NewStore(storePath)
	if err != nil {
		panic(err)
	}
	defer store.Close()

	// 3. 模拟运行时事件（触发 Tidal 学习）
	fmt.Println("📊 记录运行时事件...")

	ctx := context.Background()
	now := time.Now()

	// 模拟一系列工具调用事件
	events := []proactive.RuntimeEvent{
		{
			ID:        fmt.Sprintf("test-event-1-%d", now.Unix()),
			Source:    "test",
			SessionID: "test-session",
			Type:      "tool_call",
			Name:      "file_read",
			Value:     1.0,
			Metadata:  map[string]string{"path": "test.txt"},
			CreatedAt: now,
		},
		{
			ID:        fmt.Sprintf("test-event-2-%d", now.Unix()),
			Source:    "test",
			SessionID: "test-session",
			Type:      "chat_turn",
			Name:      "user_message",
			Value:     1.0,
			Metadata:  map[string]string{"tokens": "50"},
			CreatedAt: now.Add(1 * time.Second),
		},
		{
			ID:        fmt.Sprintf("test-event-3-%d", now.Unix()),
			Source:    "test",
			SessionID: "test-session",
			Type:      "memory_recall",
			Name:      "memory_search",
			Value:     1.0,
			Metadata:  map[string]string{"query": "test query"},
			CreatedAt: now.Add(2 * time.Second),
		},
		{
			ID:        fmt.Sprintf("test-event-4-%d", now.Unix()),
			Source:    "test",
			SessionID: "test-session",
			Type:      "rag_search",
			Name:      "rag_query",
			Value:     1.0,
			Metadata:  map[string]string{"query": "test RAG query"},
			CreatedAt: now.Add(3 * time.Second),
		},
	}

	for i, event := range events {
		if err := store.RecordRuntimeEvent(event); err != nil {
			fmt.Printf("  ❌ 事件 %d 记录失败: %v\n", i+1, err)
		} else {
			fmt.Printf("  ✓ 事件 %d: %s/%s\n", i+1, event.Type, event.Name)
		}
	}

	// 4. 查看统计信息
	fmt.Println("\n📈 Proactive 统计:")

	stats, err := store.Stats()
	if err != nil {
		panic(err)
	}

	fmt.Printf("  信号数: %d\n", stats.Signals)
	fmt.Printf("  状态估计: %d\n", stats.Estimates)
	fmt.Printf("  动作记录: %d\n", stats.Actions)
	fmt.Printf("  反馈事件: %d\n", stats.FeedbackEvents)
	fmt.Printf("  运行时事件: %d\n", stats.RuntimeEvents)
	fmt.Printf("  动作执行: %d\n", stats.Executions)

	// 5. 查看运行时事件统计
	runtimeStats, err := store.RuntimeEventStats()
	if err != nil {
		panic(err)
	}

	fmt.Println("\n📊 运行时事件统计:")
	fmt.Printf("  总事件数: %d\n", runtimeStats.Events)
	if len(runtimeStats.ByType) > 0 {
		fmt.Println("  按类型分布:")
		for typ, count := range runtimeStats.ByType {
			fmt.Printf("    - %s: %d\n", typ, count)
		}
	}

	// 6. 查看最近的事件
	fmt.Println("\n🕐 最近的事件:")
	recentEvents, err := store.RecentRuntimeEvents(10)
	if err != nil {
		panic(err)
	}

	if len(recentEvents) == 0 {
		fmt.Println("  (无)")
	} else {
		for i, event := range recentEvents {
			fmt.Printf("  %d. [%s] %s/%s (value: %.2f)\n",
				i+1, event.CreatedAt.Format("15:04:05"),
				event.Type, event.Name, event.Value)
		}
	}

	// 7. 模拟 Proactive 决策周期
	fmt.Println("\n🔮 运行 Proactive 决策...")

	// 创建简单的采样器和估计器
	sampler := proactive.NewSignalSampler()
	estimator := proactive.NewStateEstimator(proactive.Config{
		Enabled:             true,
		DryRun:              *cfg.Proactive.DryRun,
		ConfidenceThreshold: cfg.Proactive.ConfidenceThreshold,
		Horizon:             time.Duration(cfg.Proactive.HorizonSeconds) * time.Second,
	})

	// 采样信号
	signals := sampler.Sample(ctx, now)
	fmt.Printf("  采样到 %d 个信号\n", len(signals))

	// 状态估计
	estimate := estimator.Estimate(ctx, signals, now)
	fmt.Printf("  预测状态: %s (confidence: %.2f)\n",
		estimate.PredictedState, estimate.Confidence)

	// 8. 测试 Kernel Learning
	fmt.Println("\n🧠 Kernel Learning 状态:")
	kernelStats, err := store.KernelStats()
	if err != nil {
		panic(err)
	}

	fmt.Printf("  权重数: %d\n", kernelStats.Weights)
	fmt.Printf("  样本数: %d\n", kernelStats.Samples)

	fmt.Println("\n✅ 测试完成！")
	fmt.Println("\n💡 提示:")
	fmt.Println("  - 持续使用系统以积累更多数据")
	fmt.Println("  - Tidal 会在后台自动学习你的使用模式")
	fmt.Println("  - 数据存储在:", storePath)
}
