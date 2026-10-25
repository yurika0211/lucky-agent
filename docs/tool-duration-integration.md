# 工具耗时的接入边界

主仓 PR #334 的 Agent 生产事件与中立 turn 适配器提供 `time.Duration`。
毫秒换算属于应用/协议消费者，不能把源代码出现字段等同于最终 `lh` 已发送该字段。
本次没有新增配置键，`config.example.json` 不变。

## 仍需配套的变更

| 层 | 变更 | 状态 |
| --- | --- | --- |
| Agent | [主仓 #334](https://github.com/yurika0211/lucky-agent/pull/334) | 本 PR，未合并 |
| 中立事件 | [gateway #1](https://github.com/yurika0211/luckyagent-gateway/pull/1) | 已 pin 到可下载的 PR 提交，仍未合并 |
| WS 字段 | [API #9](https://github.com/yurika0211/luckyagent-api/pull/9) | 未合并；当前选择的 API v0.2.3 不含该字段 |
| WS/SSE/SDK 消费 | [app #17](https://github.com/yurika0211/luckyagent-app/pull/17) | 未合并，且与 app 当前主线有冲突 |

launcher 保留已接入更新校验和会话清理修复的 app 版本，没有为了显示耗时降回旧 app。
要完成实时链路，应先在 app 分支保留新主线修复并解决冲突，将 API 配套变更发布为
不会被 MVS 更高版本覆盖的提交，然后更新消费者与 launcher 的公开 pin。
不提交本机 replace，不把旧版最低依赖当作最终实际选择版本。

这些外部仓库当前没有挂载到本线程，未通过修改模块缓存或恢复旧 internal 路径绕过归属。
此记录不代表相关 PR 已获合并许可。

## 已验证的主仓合同

- 正常结果、错误文本通过中立事件转换时保留耗时。
- durable 执行失败的结果事件与保存的 tool message 毫秒数一致。
- 重放已完成操作不重新执行工具，事件使用原记录的耗时。
- 没有测量的事件仍为零；协议使用整数毫秒后，不足一毫秒的实测也会是零。

外部 WS/SSE/SDK 的端到端验收仍待消费者接入，不能用上述单元测试代替。
