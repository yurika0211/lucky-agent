# opt-tool-db_schema-01

## 目标

优化 `db_schema` 的 schema 覆盖范围、只读连接、输出结构和大型数据库表现，让它继续保持“自动批准查看 SQLite schema”的定位，同时补齐索引、视图、外键、触发器等常见结构信息。

## 当前状态

相关实现：

- `internal/tool/builtin_query.go`
- `docs/tools/query/db_schema.md`

当前流程：

1. 读取 `path`。
2. 调用 `validatePath(path)`。
3. 打开 SQLite 数据库。
4. 如果指定 `table`，执行 `PRAGMA table_info(table)`。
5. 如果未指定，查询 `sqlite_master` 中 `type='table'` 且非 `sqlite_%` 的表。
6. 对每个表输出 columns。

当前优势：

- 只读工具，自动批准合理。
- 表名通过 quote identifier 处理。
- 输出 JSON。
- 适合 `sql_query` 前置探索。

## 主要问题

### 1. 连接不是显式只读

和 `sql_query` 一样，当前直接打开 path。schema 查看应使用只读 DSN。

### 2. 只列普通表和列

缺少：

- indexes
- foreign keys
- views
- triggers
- table row count estimate

### 3. `default` 无法区分 NULL 和空字符串

当前使用 `sql.NullString.String`，NULL 会显示为空字符串。

建议输出 `default` 为 nil 或字符串。

### 4. 大库输出可能过长

没有 limit 或 include 参数，可能一次输出很多表。

### 5. table 参数只支持单表

想查看多个表需要多次调用。

## 优化原则

1. schema 查看必须只读。
2. 默认输出保持简洁。
3. 详细结构通过 `include` 参数开启。
4. 输出字段要区分 NULL 和空字符串。

## 推荐方案

### 1. 参数扩展

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `include` | 否 | `columns` | 逗号列表：`columns,indexes,foreign_keys,views,triggers`。 |
| `limit_tables` | 否 | `100` | 最多返回多少张表。 |
| `format` | 否 | `json` | 输出格式，预留 text。 |
| `include_internal` | 否 | `false` | 是否包含 `sqlite_%` 内部表。 |

### 2. 只读连接

复用 `sqliteReadOnlyDSN(path)`。

### 3. indexes

对每个表执行：

```sql
PRAGMA index_list(table)
PRAGMA index_info(index_name)
```

输出 index name、unique、columns。

### 4. foreign keys

执行：

```sql
PRAGMA foreign_key_list(table)
```

输出 from/to/table/on_update/on_delete。

### 5. views 和 triggers

从 `sqlite_master` 读取：

```sql
SELECT name, type, sql FROM sqlite_master WHERE type IN ('view','trigger')
```

默认不输出完整 SQL，除非 `include_sql=true`。

### 6. default NULL 保真

列输出：

```json
{
  "default": null,
  "has_default": false
}
```

## 分阶段实施

### 第一阶段：只读和默认值修正

- 只读 DSN。
- default 区分 NULL。
- limit_tables。

### 第二阶段：结构扩展

- indexes。
- foreign_keys。
- views。
- triggers。

### 第三阶段：输出控制

- include 参数。
- include_sql。
- 多表 table pattern。

## 测试建议

- path 为空时报错。
- 单表 schema 兼容。
- 不存在表时报错。
- default NULL 输出为 null。
- index 信息输出正确。
- foreign key 输出正确。
- include_internal=false 排除 sqlite_%。
- limit_tables 生效。
- 只读 DSN 不创建不存在数据库。

## 文档更新

同步更新 `docs/tools/query/db_schema.md` 的 include 参数、索引/外键示例和只读连接说明。

## 风险与边界

- 输出完整 trigger/view SQL 可能很长，默认不要开。
- 对损坏 DB 的错误要清晰。
- row count 可能昂贵，不建议默认统计。

## 推荐结论

优先改只读连接和 default NULL 保真，再补 indexes / foreign keys。这样能让 `db_schema -> sql_query` 流程更可靠。
