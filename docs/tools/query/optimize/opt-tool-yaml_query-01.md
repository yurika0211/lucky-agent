# opt-tool-yaml_query-01

## 目标

优化 `yaml_query` 的多文档 YAML、锚点/类型处理、查询语法和输出控制，让它继续保持“只读查询本地 YAML 文件”的定位，同时更好支持 Kubernetes、CI 和复杂配置文件。

## 当前状态

相关实现：

- `internal/tool/builtin_query.go`
- `docs/tools/query/yaml_query.md`

当前流程：

1. 读取 `path`。
2. 调用 `validatePath(path)`。
3. `os.ReadFile(path)` 整文件读取。
4. `yaml.Unmarshal` 到 `any`。
5. 调用 `normalizeYAMLValue` 把 map key 转成 string。
6. 使用和 `json_query` 相同的 `queryStructuredValue`。

当前优势：

- 查询语法和 JSON 一致。
- YAML map key 会转成 string。
- 适合读取普通配置文件。

## 主要问题

### 1. 不支持多文档 YAML

Kubernetes manifest 常见：

```yaml
---
apiVersion: v1
kind: Service
---
apiVersion: apps/v1
kind: Deployment
```

当前 `yaml.Unmarshal` 只按单个文档处理。

### 2. 没有文件大小限制

和 `json_query` 一样，当前整文件读取，没有大小上限。

### 3. query 语法继承 JSON 的限制

不支持 bracket key、wildcard、连续下标等。

### 4. YAML 类型信息丢失

转成 `any` 后无法区分部分 YAML node 信息，例如：

- anchor
- alias
- tag
- line number

多数查询不需要这些，但错误诊断和 manifest 排查时 line number 有价值。

### 5. 输出整个 manifest 可能过长

query 为空时 pretty JSON 输出整个 YAML，可能占用大量上下文。

## 优化原则

1. 和 `json_query` 共享路径查询 parser。
2. 支持多文档 YAML，但默认保持单文档兼容。
3. 大文件和大输出要有限制。
4. 不做完整 yq 替代品。

## 推荐方案

### 1. 参数扩展

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `document` | 否 | `0` | 多文档 YAML 的文档下标。 |
| `all_documents` | 否 | `false` | 是否查询所有文档。 |
| `max_file_bytes` | 否 | `20 MiB` | 最大文件大小。 |
| `max_output_chars` | 否 | `12000` | 最大输出字符数。 |
| `summary` | 否 | `false` | query 为空时输出摘要。 |

### 2. 多文档解析

使用 `yaml.Decoder`：

```go
decoder := yaml.NewDecoder(bytes.NewReader(data))
for {
	var doc any
	err := decoder.Decode(&doc)
	...
}
```

行为：

- 默认查询第 0 个文档。
- `all_documents=true` 时对每个文档执行同一 query，返回数组。

### 3. 共享 structured path parser

和 `json_query` 一起升级：

- bracket key
- wildcard
- length
- 连续下标

### 4. manifest summary

`summary=true` 输出：

```json
{
  "documents": [
    {"index": 0, "kind": "Service", "name": "api"},
    {"index": 1, "kind": "Deployment", "name": "api"}
  ]
}
```

对 Kubernetes manifest 特别有用。

### 5. 输出截断

统一使用 `max_output_chars` 和 `_meta.truncated`。

## 分阶段实施

### 第一阶段：边界和多文档

- 文件大小限制。
- 输出大小限制。
- `document` 参数。
- `all_documents` 参数。

### 第二阶段：查询语法共享

- 抽出 structured path parser。
- 和 JSON 测试共用 case。

### 第三阶段：YAML 诊断

- summary 模式。
- 可选保留 line number。
- 对 anchor/alias 给出风险说明。

## 测试建议

- 单文档查询保持兼容。
- 多文档默认查第 0 个。
- `document=1` 查第二个文档。
- `all_documents=true` 返回数组。
- 超大 YAML 被拒绝。
- bracket key 查询 Kubernetes label。
- query 为空且 summary=true 输出 kind/name。

## 文档更新

同步更新 `docs/tools/query/yaml_query.md` 的多文档行为、参数表、Kubernetes 示例和输出限制。

## 风险与边界

- `yaml.Decoder` 行为要兼容空文档。
- all_documents + wildcard 可能输出过大。
- line number 需要使用 `yaml.Node`，实现复杂度更高，可放后续。

## 推荐结论

优先支持多文档 YAML 和输出限制。Kubernetes/CI 场景下，这是 `yaml_query` 和 `json_query` 最大的差异点。
