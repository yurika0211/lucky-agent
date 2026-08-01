# opt-tool-sql_query-01

## 目标

优化 `sql_query` 的只读隔离、SQL 安全检查、超时、结果限制和输出 metadata，让它继续保持“只读查询本地 SQLite，需要审批”的定位，同时降低前缀判断绕过、长查询卡住、隐式写入和结果不可审计的问题。

## 当前状态

相关实现：

- `internal/tool/builtin_query.go`
- `docs/tools/query/sql_query.md`

当前流程：

1. 读取 `path` 和 `query`。
2. 调用 `validatePath(path)`。
3. `isReadOnlySQL(query)` 做前缀判断。
4. 打开 sqlite3 数据库。
5. `db.Query(query)`。
6. 扫描最多 `limit` 行。
7. 输出 JSON 数组。

当前优势：

- 权限是 `PermApprove`。
- limit 最大 200。
- 非 SELECT/WITH/PRAGMA/EXPLAIN 前缀会拒绝。
- 输出结构化 JSON。

## 主要问题

### 1. 只读检查只是前缀判断

`WITH`、`PRAGMA` 等可能包含副作用或危险操作。前缀判断不是 SQL sandbox。

建议使用 SQLite 只读连接和 query_only 模式。

### 2. 数据库连接不是显式只读

当前 `sql.Open("sqlite3", path)` 没有加 `mode=ro`。

建议 DSN：

```text
file:<path>?mode=ro&immutable=1
```

或至少 `mode=ro`。

### 3. 没有 query timeout

复杂查询可能长时间运行。

建议使用 `context.WithTimeout` 和 `db.QueryContext`。

### 4. 没有自动追加 LIMIT

虽然 scanSQLRows 最多扫描 limit 行，但数据库仍可能执行完整排序/聚合。对无 LIMIT 查询，可提示或包裹。

### 5. PRAGMA 范围过宽

部分 PRAGMA 可能改变连接行为。建议 allowlist 常见只读 PRAGMA。

### 6. 输出缺少 metadata

不知道是否达到 limit、列名、行数、执行耗时。

## 优化原则

1. 审批不能替代只读隔离。
2. SQLite 连接必须尽量只读。
3. 长查询必须可超时。
4. 前缀检查和 SQLite 只读模式都要有。
5. 输出应说明是否被 limit 截断。

## 推荐方案

### 1. 只读 DSN

新增：

```go
func sqliteReadOnlyDSN(path string) string
```

使用：

```go
db, err := sql.Open("sqlite3", sqliteReadOnlyDSN(path))
```

并执行：

```sql
PRAGMA query_only = ON;
```

### 2. SQL parser / statement 检查

短期增强：

- 拒绝多语句 `;` 后还有非空内容。
- `WITH` 中拒绝 `insert`、`update`、`delete`、`replace`。
- PRAGMA 使用 allowlist。

长期可引入 SQLite authorizer，但 Go sqlite3 driver 支持需要确认。

### 3. timeout 参数

新增：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `timeout_seconds` | 否 | `10` | 查询超时，最大 60。 |
| `include_meta` | 否 | `false` | 返回行数、耗时、截断信息。 |

使用 `QueryContext`。

### 4. PRAGMA allowlist

允许：

- `table_info`
- `index_list`
- `index_info`
- `foreign_key_list`
- `database_list`
- `integrity_check`
- `quick_check`

其他 PRAGMA 默认拒绝。

### 5. metadata 输出

`include_meta=true`：

```json
{
  "rows": [],
  "meta": {
    "columns": ["id", "name"],
    "returned_rows": 50,
    "limit": 50,
    "truncated": true,
    "duration_ms": 12
  }
}
```

### 6. BLOB 处理

当前 `[]byte` 直接转 string。建议：

- UTF-8 可转字符串。
- 非 UTF-8 输出 base64 或 `<blob N bytes>`。

## 分阶段实施

### 第一阶段：只读隔离

- SQLite DSN `mode=ro`。
- `PRAGMA query_only=ON`。
- 多语句拒绝。
- PRAGMA allowlist。

### 第二阶段：超时和 metadata

- QueryContext。
- timeout_seconds。
- include_meta。
- truncated 判断。

### 第三阶段：结果类型增强

- BLOB 安全输出。
- 日期/数字类型保真说明。

## 测试建议

- 非 SELECT 拒绝。
- 多语句拒绝。
- PRAGMA 写类操作拒绝。
- mode=ro 下写入失败。
- timeout 生效。
- limit 生效且 truncated=true。
- include_meta 返回 columns/duration。
- BLOB 非 UTF-8 不直接转乱码。

## 文档更新

同步更新 `docs/tools/query/sql_query.md` 的只读隔离说明、timeout 参数、PRAGMA allowlist 和 metadata 输出。

## 风险与边界

- `immutable=1` 对正在变化的 DB 不一定合适，可只默认 `mode=ro`。
- PRAGMA allowlist 可能影响旧用法。
- SQL parser 不应自研过深，SQLite authorizer 是更稳的长期方向。

## 推荐结论

优先把连接改成只读模式并加 query timeout。当前只靠 SQL 前缀判断不够稳，尤其是本地 SQLite 可能承载 memory/RAG/session 等重要数据。
