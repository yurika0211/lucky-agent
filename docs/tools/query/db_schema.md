# db_schema Tool

`db_schema` 是 LuckyAgent 的内置 SQLite schema 查看工具，用来检查本地 SQLite 数据库中的表、列、索引、外键、视图和触发器。它适合在写 `sql_query` 之前先确认数据库结构。

这是只读工具，不修改数据库，因此被标记为自动批准。

## 工具定义

实现位置：

- `internal/tool/builtin_query.go`
- `internal/tool/builtin_helpers.go`

注册信息：

```go
Name:         "db_schema"
Category:     CatBuiltin
Source:       "builtin"
Permission:   PermAuto
ParallelSafe: true
```

## 参数

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `path` | 是 | 无 | SQLite 数据库文件路径。 |
| `table` | 否 | 无 | 指定表名。为空时列出所有非 sqlite 内部表。 |
| `include` | 否 | `columns` | 逗号列表：`columns,indexes,foreign_keys,views,triggers`。 |
| `limit_tables` | 否 | `100` | 未指定 `table` 时最多返回多少张表，最大 1000。 |
| `include_internal` | 否 | `false` | 是否包含 `sqlite_%` 内部表或对象。 |
| `include_sql` | 否 | `false` | 是否返回 view / trigger 的原始 SQL。 |

示例：

```json
{
  "path": "accounts.db",
  "table": "users"
}
```

## 执行流程

`db_schema` 的执行过程是：

1. 读取必填参数 `path`。
2. 如果 `path` 不是字符串，返回 `path is required`。
3. 调用 `validatePath(path)` 做路径校验。
4. 读取可选参数 `table`、`include`、`limit_tables`、`include_internal`、`include_sql`。
5. 使用 `sqliteReadOnlyDSN(path)` 以 `mode=ro` 打开数据库文件。
6. 执行 `PRAGMA query_only = ON`。
7. 如果指定了 `table`，按 `include` 调用 `sqliteTableDetails`。
8. 如果没有指定 `table`，查询 `sqlite_master` 列出表，并受 `limit_tables` 限制。
9. 按需读取 indexes、foreign_keys、views、triggers。
10. 使用 pretty JSON 输出 schema。

## 表列表行为

没有指定 `table` 时，工具执行：

```sql
SELECT name
FROM sqlite_master
WHERE type='table'
  AND name NOT LIKE 'sqlite_%'
ORDER BY name
LIMIT ?
```

这会排除 SQLite 内部表，例如 `sqlite_sequence`。

传入 `include_internal=true` 时会包含 `sqlite_%` 内部表。

## 单表行为

指定 `table` 时，工具返回：

```json
{
  "table": "users",
  "columns": [],
  "indexes": [],
  "foreign_keys": [],
  "triggers": []
}
```

底层使用：

```sql
PRAGMA table_info("<table>")
```

表名会通过 `sqliteQuoteIdentifier` 加双引号，并把内部双引号转义为两个双引号。

如果没有可见列，返回：

```text
table "<name>" not found or has no visible columns
```

## 输出字段

每个 column 对象包含：

| 字段 | 说明 |
| --- | --- |
| `cid` | SQLite column id。 |
| `name` | 列名。 |
| `type` | SQLite 类型声明。 |
| `not_null` | 是否声明 NOT NULL。 |
| `default` | 默认值；没有默认值时为 `null`。 |
| `has_default` | 是否存在默认值，用来区分 NULL 和空字符串。 |
| `primary` | 是否是主键列。 |

示例输出：

```json
{
  "table": "users",
  "columns": [
    {
      "cid": 0,
      "name": "id",
      "type": "INTEGER",
      "not_null": false,
      "default": null,
      "has_default": false,
      "primary": true
    }
  ]
}
```

未指定表时：

```json
{
  "tables": [
    {
      "name": "users",
      "columns": []
    }
  ]
}
```

## 扩展结构

`include=indexes` 时，每个表会包含：

```json
{
  "indexes": [
    {
      "name": "idx_users_name",
      "unique": false,
      "origin": "c",
      "partial": false,
      "columns": ["name"]
    }
  ]
}
```

`include=foreign_keys` 时：

```json
{
  "foreign_keys": [
    {
      "table": "orgs",
      "from": "org_id",
      "to": "id",
      "on_update": "NO ACTION",
      "on_delete": "CASCADE"
    }
  ]
}
```

`include=views,triggers` 会从 `sqlite_master` 读取对象名称和关联表；`include_sql=true` 时额外输出原始 SQL。

## 适合使用的场景

优先使用 `db_schema` 的场景：

- 不确定 SQLite 数据库有哪些表。
- 写 SQL 前确认列名。
- 快速查看数据库结构。
- 检查迁移是否创建了预期表。
- 理解 `.db` 文件大致用途。

示例：

```json
{
  "path": "~/.luckyagent/rag.db"
}
```

## 不适合使用的场景

不优先使用 `db_schema` 的场景：

- 需要查询实际数据，应使用 `sql_query`。
- 需要修改 schema，应使用 `terminal` 或迁移脚本。
- 需要估算行数、统计大小或分析查询计划，应使用 `sql_query` / `EXPLAIN` 或 SQLite 专门工具。
- 需要分析非 SQLite 数据库。

## 和 sql_query 的关系

`db_schema` 用来发现结构，`sql_query` 用来读取数据。

推荐流程：

```text
db_schema -> sql_query
```

例如先查表：

```json
{
  "path": "accounts.db"
}
```

再查数据：

```json
{
  "path": "accounts.db",
  "query": "SELECT * FROM users LIMIT 10"
}
```

## 风险和注意事项

`db_schema` 的主要注意点：

- 只支持 SQLite。
- 默认只列 `type='table'` 的普通表和列。
- 索引、外键、视图或触发器需要通过 `include` 显式打开。
- `default` 为 `null` 且 `has_default=false` 表示没有默认值。
- 指定表不存在时以 error 返回。

## 维护注意事项

如果后续修改 `db_schema`，需要同步检查：

- 参数名是否仍与 `DBSchemaTool()` 一致。
- 权限是否仍是 `PermAuto`。
- 是否仍排除 `sqlite_%` 内部表。
- 只读连接和 `PRAGMA query_only` 是否仍生效。
- columns 是否仍来自 `PRAGMA table_info`。
- indexes、foreign_keys、views、triggers 的 include 行为是否稳定。
- 输出是否仍是 pretty JSON。
