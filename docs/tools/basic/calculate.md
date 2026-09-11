# calculate Tool

`calculate` 是 LuckyAgent 的内置本地计算工具，用来执行小型算术表达式。它适合做快速数值校验，不需要启动 shell，也不需要调用外部模型或网络服务。

这是只读工具，不修改本地状态，因此被标记为自动批准。

## 工具定义

实现位置：

- `internal/tool/builtin_web.go`

注册信息：

```go
Name:         "calculate"
Category:     CatBuiltin
Source:       "builtin"
Permission:   PermAuto
ParallelSafe: true
```

含义：

- `CatBuiltin`：这是核心内置工具，不依赖 skill 或 MCP。
- `PermAuto`：本地算术计算是只读操作，默认可以自动执行。
- `ParallelSafe=true`：工具不修改共享状态，可以和其他只读工具并行。

## 参数

`calculate` 接收一个必填参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `expression` | 是 | 无 | 算术表达式，例如 `(12.5*8)/3`、`sqrt(144)`、`max(3,7,2)` 或 `2^10`。 |

示例参数：

```json
{
  "expression": "sqrt(144)+pow(2,3)"
}
```

## 执行流程

`calculate` 的执行过程是：

1. 读取必填参数 `expression`。
2. 如果参数不存在、不是字符串或去掉空白后为空，返回 `expression is required`。
3. 使用 Go 标准库 `parser.ParseExpr` 解析表达式。
4. 递归计算 AST 中允许的数字字面量、括号、一元运算、二元运算和函数调用。
5. 如果表达式中出现不支持的语法或函数，返回错误。
6. 如果结果是 `NaN` 或无穷大，返回 `expression produced non-finite result`。
7. 使用 `strconv.FormatFloat(value, 'f', -1, 64)` 输出结果字符串。

## 输出格式

成功时只返回结果数字，不带单位、不带解释文本。

例如：

```text
20
```

浮点数会按 Go 的 `FormatFloat` 规则输出，尽量使用必要的最短小数形式。

例如：

```json
{
  "expression": "10/4"
}
```

返回：

```text
2.5
```

## 支持的表达式

当前支持以下表达式节点：

| 类型 | 示例 | 说明 |
| --- | --- | --- |
| 数字字面量 | `12`, `12.5` | 支持整数和浮点数字面量。 |
| 括号 | `(1+2)*3` | 通过 Go 表达式解析器处理优先级。 |
| 一元加号 | `+3` | 返回原值。 |
| 一元减号 | `-3` | 返回相反数。 |
| 二元运算 | `1+2`, `5*6` | 支持加减乘除、取模和幂运算。 |
| 函数调用 | `sqrt(144)` | 只支持白名单函数。 |

## 支持的运算符

| 运算符 | 含义 | 示例 |
| --- | --- | --- |
| `+` | 加法 | `1+2` |
| `-` | 减法 | `5-3` |
| `*` | 乘法 | `4*6` |
| `/` | 除法 | `10/4` |
| `%` | 取模 | `10%3` |
| `^` | 幂运算 | `2^10` |

注意：`calculate` 使用 Go 表达式解析器读取语法，但在求值时把 `^` 解释为幂运算，也就是 `math.Pow(left, right)`。这和 Go 语言本身的按位异或含义不同。

除法和取模会检查右侧是否为 0：

- 除以 0 返回 `division by zero`。
- 对 0 取模返回 `modulo by zero`。

## 支持的函数

函数名会先做：

```go
strings.ToLower(strings.TrimSpace(name))
```

当前支持：

| 函数 | 参数数量 | 说明 |
| --- | --- | --- |
| `sqrt(x)` | 1 | 平方根。负数会返回 `sqrt of negative number`。 |
| `abs(x)` | 1 | 绝对值。 |
| `ceil(x)` | 1 | 向上取整。 |
| `floor(x)` | 1 | 向下取整。 |
| `round(x)` | 1 | 四舍五入到最近整数，使用 Go `math.Round`。 |
| `min(x, ...)` | 至少 1 | 返回最小值。 |
| `max(x, ...)` | 至少 1 | 返回最大值。 |
| `pow(x, y)` | 2 | 幂运算。 |

参数数量不匹配时会返回对应错误，例如：

```text
sqrt expects 1 argument
```

不支持的函数会返回：

```text
unsupported function "<name>"
```

## 语法来源

`calculate` 使用：

```go
parser.ParseExpr(strings.TrimSpace(expression))
```

这意味着表达式必须能被 Go 表达式解析器解析。

可以写：

```text
sqrt(144)+pow(2,3)
```

不能写自然语言或命令式语句：

```text
what is 1 plus 2
```

也不能写 Go 语句、变量声明、赋值、数组、对象或访问器等复杂结构。

## 常见调用示例

基础四则运算：

```json
{
  "expression": "(12.5*8)/3"
}
```

幂运算：

```json
{
  "expression": "2^10"
}
```

函数组合：

```json
{
  "expression": "sqrt(144)+pow(2,3)"
}
```

最大值：

```json
{
  "expression": "max(3,7,2)"
}
```

取整：

```json
{
  "expression": "ceil(12.1)"
}
```

## 适合使用的场景

优先使用 `calculate` 的场景：

- 做简单数值计算。
- 验证模型回答里的算术结果。
- 计算比例、百分比、单位换算中的中间值。
- 需要快速得到确定的本地计算结果。
- 不需要 shell、Python、bc 或联网服务的小计算。

示例：

```json
{
  "expression": "round((1280*0.618))"
}
```

## 不适合使用的场景

不优先使用 `calculate` 的场景：

- 需要变量、循环、条件分支或复杂脚本。
- 需要矩阵、统计、符号运算或高精度十进制。
- 需要日期时间计算，应使用 `current_time` 或代码处理。
- 需要访问文件、命令输出或项目数据，应使用 `terminal` 或文件工具。
- 需要财务级精度，不应依赖 `float64`。

## 和 terminal 的关系

简单算术优先用 `calculate`，因为它更小、更快、权限更低。

例如：

```json
{
  "expression": "15*32"
}
```

比启动：

```sh
python -c 'print(15*32)'
```

更适合作为 agent 的默认计算路径。

如果计算需要读取文件、运行项目脚本、调用系统命令或使用第三方库，则应使用 `terminal`。

## 风险和注意事项

`calculate` 的主要注意点：

- 计算基于 `float64`，不是任意精度。
- `^` 在此工具中表示幂运算，不是 Go 的异或。
- 表达式必须能被 Go 表达式解析器解析。
- 不支持变量、常量名、字段访问、数组、map 或方法调用。
- 不支持 `sin`、`cos`、`log` 等未列入白名单的函数。
- 除以 0、对 0 取模、负数平方根会返回错误。
- 非有限结果会被拒绝。

## 维护注意事项

如果后续修改 `calculate`，需要同步检查：

- 参数名是否仍是 `expression`。
- 权限是否仍是 `PermAuto`。
- 是否新增或移除了支持的 AST 节点。
- `^` 是否仍被解释为幂运算。
- 支持函数列表和参数数量是否变化。
- 除以 0、取模 0、负数平方根等错误行为是否变化。
- 输出是否仍使用 `strconv.FormatFloat(value, 'f', -1, 64)`。
- 是否仍拒绝 `NaN` 和无穷大结果。
