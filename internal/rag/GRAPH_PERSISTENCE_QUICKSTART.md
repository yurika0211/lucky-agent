# Graph RAG 持久化 - 快速开始

## 🚀 一行代码启用持久化

```go
// 之前：内存图谱（重启丢失）
ragManager := rag.NewRAGManagerWithGraph(embedder, config, llmProvider)

// 现在：持久化图谱（自动保存和加载）
ragManager, err := rag.NewRAGManagerWithSQLiteAndGraph(
    embedder, 
    config, 
    "~/.luckyagent/rag/graph.db",  // 指定数据库路径
    llmProvider,
)
```

就是这么简单！所有图谱数据会自动：
- ✅ 保存到 SQLite
- ✅ 重启后加载
- ✅ 增量更新

## 📖 完整示例

```go
package main

import (
    "context"
    "github.com/yurika0211/luckyagent/internal/embedder"
    "github.com/yurika0211/luckyagent/internal/rag"
)

func main() {
    // 1. 配置
    config := rag.DefaultRAGConfig()
    config.EnableGraph = true

    // 2. 创建带持久化的 RAG
    ragManager, _ := rag.NewRAGManagerWithSQLiteAndGraph(
        embedder.NewOpenAIEmbedder("your-key"),
        config,
        "~/.luckyagent/rag/graph.db",
        yourLLMProvider,
    )
    defer ragManager.CloseStore()

    // 3. 索引文档（自动提取实体并持久化）
    ctx := context.Background()
    ragManager.IndexTextWithGraph(ctx, "doc.md", "标题", "内容...")

    // 4. 查询（使用持久化的图谱）
    result, _ := ragManager.SearchWithGraph(ctx, "你的问题")

    // 5. 重启后，图谱数据自动从数据库加载！
}
```

## 🧪 验证持久化

运行演示程序两次，观察输出：

```bash
# 第一次运行
go run ./cmd/graph-rag-persist-demo/main.go
# 输出: ✓ 数据库为空，将创建新图谱

# 第二次运行
go run ./cmd/graph-rag-persist-demo/main.go
# 输出: ✓ 从数据库加载了现有图谱: 3 个节点, 2 条边
```

## 💡 优势

| 特性 | 内存图谱 | 持久化图谱 |
|------|---------|-----------|
| 重启后数据 | ❌ 丢失 | ✅ 保留 |
| 实体提取成本 | 每次重启都需要 | 只需提取一次 |
| 启动速度 | 快 | 略慢（加载时间） |
| 数据共享 | ❌ 不支持 | ✅ 支持 |
| 使用复杂度 | 简单 | **同样简单** |

## 🔍 查看数据

使用 SQLite 命令行工具：

```bash
sqlite3 ~/.luckyagent/rag/graph.db

# 查看节点
SELECT id, name, type, importance FROM knowledge_nodes;

# 查看边
SELECT source_id, target_id, rel_type FROM knowledge_edges;

# 统计
SELECT COUNT(*) FROM knowledge_nodes;
SELECT COUNT(*) FROM knowledge_edges;
```

## 📚 更多信息

- 完整文档：[README_GRAPH_RAG.md](README_GRAPH_RAG.md)
- 实现细节：[GRAPH_PERSISTENCE_IMPLEMENTATION.md](GRAPH_PERSISTENCE_IMPLEMENTATION.md)
- 测试代码：[graph_persist_test.go](graph_persist_test.go)
