# current_time Tool

`current_time` 是 LuckyAgent 的内置时间查询工具，用来获取当前日期和时间。它可以直接返回本地时间，也可以根据城市/地区或 IANA timezone 返回指定时区时间，并尝试通过网络时间源做校验。

这是只读工具，不修改本地状态，因此被标记为自动批准。

## 工具定义

实现位置：

- `internal/tool/builtin_web.go`

注册信息：

```go
Name:         "current_time"
Category:     CatBuiltin
Source:       "builtin"
Permission:   PermAuto
ParallelSafe: true
```

含义：

- `CatBuiltin`：这是核心内置工具，不依赖 skill 或 MCP。
- `PermAuto`：查询时间是只读操作，默认可以自动执行。
- `ParallelSafe=true`：工具不修改共享状态，可以和其他只读工具并行。

## 参数

`current_time` 接收两个可选参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `location` | 否 | 无 | 城市或地区名，例如 `北京`、`Shanghai`、`Tokyo`、`New York`。 |
| `timezone` | 否 | 无 | IANA timezone，例如 `Asia/Shanghai`。提供后优先于 `location` 映射。 |

示例参数：

```json
{
  "timezone": "Asia/Shanghai"
}
```

或：

```json
{
  "location": "Tokyo"
}
```

## 执行流程

`current_time` 的执行过程是：

1. 获取本机当前时间 `time.Now()`。
2. 读取 `location` 和 `timezone`。
3. 如果 `timezone` 为空，尝试把 `location` 映射为 IANA timezone。
4. 如果仍没有 timezone，直接返回本机当前时间和本机时区。
5. 如果有 timezone，尝试用 `time.LoadLocation` 加载该时区。
6. 如果时区加载失败，回退返回本机当前时间。
7. 如果时区加载成功，计算本地转换后的指定时区时间。
8. 调用 worldtimeapi 获取网络时间。
9. 如果网络时间失败，返回本地转换时间，标记 `source: local`。
10. 如果网络时间成功，比较网络时间和本地转换时间。
11. 如果两者差值小于 2 秒，返回本地转换时间，标记 `source: local-verified`。
12. 如果差值大于等于 2 秒，返回网络时间，标记 `source: network`。

## 输出格式

没有指定 location 或 timezone 时：

```text
Current time: 2026-07-03 00:15:30 (Asia/Shanghai)
```

指定 timezone 并成功转换时：

```text
Current time: 2026-07-03 00:15:30 (Asia/Shanghai, source: local-verified, location: Asia/Shanghai)
```

指定 location 时：

```text
Current time: 2026-07-03 01:15:30 (Asia/Tokyo, source: local-verified, location: Tokyo)
```

网络校验失败时：

```text
Current time: 2026-07-03 00:15:30 (Asia/Shanghai, source: local, location: Shanghai)
```

## location 映射

当 `timezone` 为空时，工具会把部分常见城市/地区映射到 IANA timezone。

当前支持：

| location 示例 | timezone |
| --- | --- |
| `beijing`, `北京` | `Asia/Shanghai` |
| `shanghai`, `上海` | `Asia/Shanghai` |
| `guangzhou`, `广州` | `Asia/Shanghai` |
| `shenzhen`, `深圳` | `Asia/Shanghai` |
| `hangzhou`, `杭州` | `Asia/Shanghai` |
| `chengdu`, `成都` | `Asia/Shanghai` |
| `hong kong`, `hongkong`, `香港` | `Asia/Hong_Kong` |
| `tokyo`, `东京` | `Asia/Tokyo` |
| `seoul`, `首尔` | `Asia/Seoul` |
| `singapore`, `新加坡` | `Asia/Singapore` |
| `taipei`, `台北` | `Asia/Taipei` |
| `new york`, `newyork`, `纽约` | `America/New_York` |
| `los angeles`, `losangeles` | `America/Los_Angeles` |
| `san francisco`, `sanfrancisco` | `America/Los_Angeles` |
| `london`, `伦敦` | `Europe/London` |
| `paris`, `巴黎` | `Europe/Paris` |
| `berlin`, `柏林` | `Europe/Berlin` |
| `sydney`, `悉尼` | `Australia/Sydney` |

location 会先做规范化：

- 去掉首尾空白。
- 转小写。
- `_` 替换为空格。
- 合并连续空白。

例如：

```text
New_York
```

会规范化为：

```text
new york
```

## timezone 优先级

如果同时提供 `location` 和 `timezone`，`timezone` 优先。

示例：

```json
{
  "location": "Tokyo",
  "timezone": "Asia/Shanghai"
}
```

实际会使用：

```text
Asia/Shanghai
```

输出中的 location label 仍会优先显示传入的 `location`，因为 `fallbackLocationLabel` 会在 location 非空时返回 location。

## 网络校验

工具会请求：

```text
https://worldtimeapi.org/api/timezone/<timezone>
```

HTTP client 超时是 8 秒。

网络响应需要包含：

```json
{
  "datetime": "..."
}
```

`datetime` 使用 RFC3339 解析。

如果网络请求失败、状态码非 2xx、JSON 解析失败、缺少 datetime 或时间解析失败，工具不会报错给用户，而是回退到本地转换时间，并标记：

```text
source: local
```

## local-verified 和 network

如果网络时间成功获取，工具会比较：

```go
abs(networkTime.Sub(localTime))
```

如果差值小于 2 秒：

```text
source: local-verified
```

并返回本地转换时间。

如果差值大于等于 2 秒：

```text
source: network
```

并返回网络时间。

这个设计用于在本机时间明显漂移时优先使用网络时间。

## 适合使用的场景

优先使用 `current_time` 的场景：

- 用户问“现在几点”。
- 需要确认当前日期。
- 需要把相对日期换算成绝对日期。
- 需要知道某个城市当前时间。
- 需要在回答里避免把今天、明天、昨天说错。
- 需要判断定时任务、日志、会议安排的当前时间上下文。

示例：

```json
{
  "location": "北京"
}
```

## 不适合使用的场景

不优先使用 `current_time` 的场景：

- 查询天气：使用天气相关工具或联网查询。
- 查询日历事件：使用日历工具。
- 查询时区规则历史变更：需要更专业数据源。
- 精确计时、延迟测量、性能 benchmark：使用 `terminal` 或专门测试工具。
- 只需要 Go 程序内部时间逻辑：看代码或测试更合适。

## 常见调用示例

获取本机当前时间：

```json
{}
```

获取北京时间：

```json
{
  "location": "北京"
}
```

获取东京时间：

```json
{
  "location": "Tokyo"
}
```

使用明确 IANA timezone：

```json
{
  "timezone": "America/New_York"
}
```

location 和 timezone 同时提供：

```json
{
  "location": "New York",
  "timezone": "America/Los_Angeles"
}
```

## 和 web_fetch 的关系

`current_time` 会访问 worldtimeapi 做时间校验，但它不是通用网页抓取工具。

如果目标是读取网页正文，应使用 `web_fetch`。

如果目标是获取当前时间，应使用 `current_time`。

## 和 terminal 的关系

不要优先用 `terminal` 手写：

```sh
date
```

正常问当前时间时，使用 `current_time` 更合适，因为它支持 timezone/location 参数和网络校验。

`terminal` 适合调试系统时间、查看 `timedatectl`、检查容器时区或运行项目脚本。

## 风险和注意事项

`current_time` 的主要注意点：

- location 映射只覆盖代码里列出的城市/地区。
- 未识别 location 时会回退到本机当前时间。
- timezone 必须是有效 IANA timezone，否则回退到本机当前时间。
- 网络校验依赖 worldtimeapi，可失败或超时。
- 输出格式固定到秒，不包含毫秒或纳秒。
- 如果同时传 `location` 和 `timezone`，timezone 生效，但 location label 可能仍显示传入 location。

## 维护注意事项

如果后续修改 `current_time`，需要同步检查：

- 参数说明是否仍与 `CurrentTimeTool()` 一致。
- 支持的 location 映射是否变化。
- timezone 优先级是否变化。
- worldtimeapi endpoint 是否变化。
- HTTP timeout 是否仍是 8 秒。
- 网络时间与本地时间差值阈值是否仍是 2 秒。
- 输出格式是否变化。
- 网络失败时是否仍回退到本地转换时间。
- 是否新增了更多地区或语言别名。

