# sql_query Tool

`sql_query` 是 LuckyAgent 的内置 SQLite 查询工具，用来对本地 SQLite 数据库执行只读 SQL，并以 JSON 形式返回行数据。它适合查看本地 `.db` 文件中的表数据、RAG/会话/缓存类 SQLite 存储内容。

这是数据库访问工具，虽然只允许只读 SQL，但仍被标记为需要批准。

## 工具定义

实现位置：

- `internal/tool/builtin_query.go`
- `internal/tool/builtin_helpers.go`

注册信息：

```go
Name:         "sql_query"
Category:     CatBuiltin
Source:       "builtin"
Permission:   PermApprove
ParallelSafe: true
```

## 参数

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `path` | 是 | 无 | SQLite 数据库文件路径。 |
| `query` | 是 | 无 | 只读 SQL 查询。允许 `SELECT`、`WITH`、`PRAGMA`、`EXPLAIN` 开头。 |
| `limit` | 否 | `50` | 最多返回多少行，最小 1，最大 200。 |

示例：

```json
{
  "path": "accounts.db",
  "query": "SELECT name FROM users ORDER BY id",
  "limit": 100
}
```

## 执行流程

`sql_query` 的执行过程是：

1. 读取必填参数 `path` 和 `query`。
2. 如果 `path` 不是字符串，返回 `path is required`。
3. 如果 `query` 为空，返回 `query is required`。
4. 调用 `validatePath(path)` 做路径校验。
5. 调用 `isReadOnlySQL(query)` 检查 SQL 前缀。
6. 读取 `limit`，通过 `boundedIntArg(args, "limit", 50, 1, 200)` 限制范围。
7. 使用 sqlite3 driver 打开数据库文件。
8. 执行 `db.Query(query)`。
9. 读取列名。
10. 逐行扫描结果。
11. 每行转换成 `map[string]any`。
12. 遇到 `[]byte` 值时转成字符串。
13. 达到 `limit` 后停止扫描。
14. 使用 pretty JSON 输出结果数组。

## 只读 SQL 检查

当前只读检查是简单前缀判断：

```go
strings.TrimSpace(strings.ToLower(query))
```

允许以下前缀：

| 前缀 | 说明 |
| --- | --- |
| `select ` | 普通查询。 |
| `with ` | CTE 查询。 |
| `pragma ` | SQLite PRAGMA。 |
| `explain ` | 查询计划或解释。 |

其他 SQL 会返回：

```text
only read-only queries are allowed
```

注意：这是语法前缀级限制，不是 SQLite 只读连接模式。写文档或使用时应把它理解为轻量防护，而不是完整 SQL sandbox。

## 输出格式

输出是 JSON 数组。

示例：

```json
[
  {
    "name": "Ada"
  },
  {
    "name": "Bob"
  }
]
```

值处理：

- 普通值原样输出。
- SQLite driver 返回的 `[]byte` 会转成字符串。
- 最多输出 `limit` 行。

## 错误行为

常见错误包括：

```text
open sqlite database: <error>
query sqlite database: <error>
read columns: <error>
scan row: <error>
only read-only queries are allowed
```

## 适合使用的场景

优先使用 `sql_query` 的场景：

- 查看 SQLite 表数据。
- 查询本地 `.db` 文件里的少量记录。
- 验证某个表是否写入了数据。
- 查询 RAG、session、cache 等 SQLite 存储。
- 执行 `PRAGMA` 或 `EXPLAIN` 辅助排查。

示例：

```json
{
  "path": "~/.luckyagent/rag.db",
  "query": "SELECT id, title FROM documents ORDER BY id DESC",
  "limit": 20
}
```

## 不适合使用的场景

不优先使用 `sql_query` 的场景：

- 修改数据库；工具拒绝非只读前缀。
- 需要事务、迁移、导入导出，应使用 `terminal`。
- 需要连接 PostgreSQL/MySQL 等非 SQLite 数据库。
- 需要流式读取大量结果；当前最多返回 200 行。
- 需要强隔离的 SQL sandbox。

## 和 db_schema 的关系

使用顺序通常是：

1. 先用 `db_schema` 看有哪些表和列。
2. 再用 `sql_query` 写具体 SELECT。

`db_schema` 是自动权限；`sql_query` 需要批准。

## 维护注意事项

如果后续修改 `sql_query`，需要同步检查：

- 参数名是否仍是 `path`、`query`、`limit`。
- 权限是否仍是 `PermApprove`。
- 只读 SQL 前缀列表是否变化。
- `limit` 默认值和最大值是否变化。
- SQLite driver 是否仍是 `github.com/mattn/go-sqlite3`。
- 输出是否仍是 pretty JSON 数组。
- `[]byte` 是否仍转成字符串。
