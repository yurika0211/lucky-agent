# opt-tool-log_tail-01

## 目标

优化 `log_tail` 的大文件读取、行号输出、编码处理和轮转日志支持，让它继续保持“快速查看日志尾部”的定位，同时避免整文件读取超大日志带来的内存问题。

## 当前状态

相关实现：

- `internal/tool/builtin_query.go`
- `docs/tools/query/log_tail.md`

当前流程：

1. 读取 `path`。
2. 调用 `validatePath(path)`。
3. 读取 `lines`，默认 100，最大 500。
4. `os.ReadFile(path)` 整文件读取。
5. CRLF 规范化为 LF。
6. Split 后返回最后 N 行。

当前优势：

- 简单可靠。
- 自动批准。
- lines 有边界。
- 输出纯日志片段，适合直接阅读。

## 主要问题

### 1. 整文件读取不适合大日志

日志文件可能数百 MB 或数 GB。只取尾部却读取整个文件不合理。

建议改为从文件尾部反向读取块。

### 2. 不返回行号

排查时常需要知道尾部行号，尤其要和 `log_grep` 关联。

### 3. 不支持 byte 限制

只限制行数，不限制输出字节。长行可能导致输出过大。

### 4. 不支持轮转文件

常见日志有：

```text
app.log
app.log.1
app.log.2.gz
```

当前只能读一个文件。

### 5. 编码和二进制检测不足

误读二进制文件会输出乱码。

## 优化原则

1. tail 应按尾部读取，不整文件加载。
2. 默认输出保持纯文本兼容。
3. 可选行号和 metadata。
4. 大输出必须有字节上限。

## 推荐方案

### 1. 参数扩展

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `with_line_numbers` | 否 | `false` | 是否输出原文件行号。 |
| `max_bytes` | 否 | `65536` | 最大输出字节数。 |
| `include_meta` | 否 | `false` | 是否返回文件大小、截断信息。 |
| `encoding` | 否 | `utf-8` | 预留编码参数。 |

### 2. 反向读取 tail

实现：

1. `os.Open`。
2. 从文件末尾按 32 KiB block 往前读。
3. 统计换行数。
4. 达到需要行数后停止。
5. 只对尾部片段做 split。

### 3. 输出字节限制

如果尾部行超过 `max_bytes`：

- 截断前部。
- 标记 `truncated=true`。
- 保留最后内容。

### 4. 行号支持

要得到精确行号需要统计文件总行数。大文件下可选：

- `with_line_numbers=false`：最快。
- `with_line_numbers=true`：额外扫描换行数或估算。

### 5. metadata 输出

`include_meta=true`：

```json
{
  "lines": ["..."],
  "meta": {
    "path": "app.log",
    "file_size": 123456,
    "returned_lines": 100,
    "truncated": false
  }
}
```

## 分阶段实施

### 第一阶段：大文件安全

- 反向读取。
- max_bytes。
- 二进制检测。

### 第二阶段：诊断增强

- include_meta。
- with_line_numbers。

### 第三阶段：轮转支持

- `include_rotated`。
- gzip 轮转可后续支持。

## 测试建议

- 小文件输出保持一致。
- 大文件只读取尾部。
- CRLF 正常处理。
- lines 最大 500。
- max_bytes 截断可见。
- with_line_numbers 输出正确。
- 二进制文件拒绝。

## 文档更新

同步更新 `docs/tools/query/log_tail.md` 的大文件行为、max_bytes、line number 和 metadata 示例。

## 风险与边界

- 精确行号可能需要额外扫描。
- gzip 轮转支持不应混入第一阶段。
- UTF-16 等编码可作为后续能力。

## 推荐结论

优先把整文件读取改成尾部块读取，并加 `max_bytes`。这是 `log_tail` 最核心的性能和稳定性问题。
