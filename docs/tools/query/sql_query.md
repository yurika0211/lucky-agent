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
| `query` | 是 | 无 | 只读 SQL 查询。允许 `SELECT`、安全 `WITH`、allowlisted `PRAGMA`、只读 `EXPLAIN`。 |
| `limit` | 否 | `50` | 最多返回多少行，最小 1，最大 200。 |
| `timeout_seconds` | 否 | `10` | 查询超时秒数，最小 1，最大 60。 |
| `include_meta` | 否 | `false` | 是否返回列名、行数、截断和耗时 metadata。 |

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
5. 调用 `validateReadOnlySQL(query)` 检查单语句、只读前缀和 PRAGMA allowlist。
6. 读取 `limit` 和 `timeout_seconds` 并限制范围。
7. 使用 `sqliteReadOnlyDSN(path)` 以 `mode=ro` 打开数据库文件。
8. 执行 `PRAGMA query_only = ON`。
9. 使用 `context.WithTimeout` 和 `db.QueryContext(query)` 执行查询。
10. 读取列名。
11. 逐行扫描结果。
12. 每行转换成 `map[string]any`。
13. 遇到 `[]byte` 值时，UTF-8 内容转字符串，非 UTF-8 BLOB 输出 base64 对象。
14. 达到 `limit` 后停止扫描，并标记 `truncated=true`。
15. 使用 pretty JSON 输出结果数组，或带 `rows` / `meta` 的对象。

## 只读 SQL 检查

只读检查包括：

- 拒绝多语句；末尾单个分号可以接受，分号后还有内容会拒绝。
- `SELECT` 允许。
- `WITH` 允许，但拒绝 `insert`、`update`、`delete`、`replace`、`create`、`drop`、`alter`、`attach`、`detach`、`vacuum`、`reindex` 等写类关键字。
- `PRAGMA` 只允许常见只读项。
- `EXPLAIN` / `EXPLAIN QUERY PLAN` 的目标 SQL 也必须通过只读检查。

允许的 PRAGMA：

- `table_info`
- `table_xinfo`
- `index_list`
- `index_info`
- `index_xinfo`
- `foreign_key_list`
- `database_list`
- `integrity_check`
- `quick_check`

其他 SQL 会返回：

```text
only read-only queries are allowed
```

此外，连接使用 SQLite `mode=ro` 和 `PRAGMA query_only = ON`。审批仍然必要，但不是唯一防线。

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
- SQLite driver 返回的 UTF-8 `[]byte` 会转成字符串。
- 非 UTF-8 BLOB 输出为 `{"type":"blob","bytes":N,"base64":"..."}`。
- 最多输出 `limit` 行。

`include_meta=true` 时：

```json
{
  "rows": [
    {"name": "Ada"}
  ],
  "meta": {
    "columns": ["name"],
    "returned_rows": 1,
    "limit": 1,
    "truncated": true,
    "duration_ms": 12
  }
}
```

## 错误行为

常见错误包括：

```text
open sqlite database: <error>
enable sqlite query_only: <error>
query sqlite database: <error>
read columns: <error>
scan row: <error>
only read-only queries are allowed
only one SQL statement is allowed
pragma is not allowlisted for read-only sql_query
EXPLAIN target is not read-only: <error>
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
- 需要完整 SQL sandbox 或非 SQLite 权限模型。

## 和 db_schema 的关系

使用顺序通常是：

1. 先用 `db_schema` 看有哪些表和列。
2. 再用 `sql_query` 写具体 SELECT。

`db_schema` 是自动权限；`sql_query` 需要批准。

## 维护注意事项

如果后续修改 `sql_query`，需要同步检查：

- 参数名是否仍与 `SQLQueryTool()` 一致。
- 权限是否仍是 `PermApprove`。
- 只读 SQL 检查、PRAGMA allowlist、`mode=ro` 和 `query_only` 是否仍生效。
- `limit` 默认值和最大值是否变化。
- `timeout_seconds` 默认值和最大值是否变化。
- SQLite driver 是否仍是 `github.com/mattn/go-sqlite3`。
- 默认输出是否仍是 pretty JSON 数组。
- BLOB 输出策略是否变化。
