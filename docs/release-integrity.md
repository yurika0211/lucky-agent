# 发布清单与安装完整性

`lh update`、Linux/macOS 的 `install-lh.sh` 和 Windows 的 `install-lh.ps1`
都从 release 的 `update.json` 选择平台压缩包。下载后必须先验证 SHA-256，
再解压或执行包内安装程序；校验值缺失或不匹配时中止安装。

清单仍为 schema 1，不增加运行配置，`config.example.json` 无需修改：

- `tag_name` 表示该清单的版本，资源 URL 固定到 `/releases/download/<tag>/<asset>`。
  只有查找最新清单的入口使用 `/releases/latest/download/update.json`。
- `published_at` 是有时区的 ISO 8601 时间。tag push 时 release 尚未创建，使用清单
  生成时的 UTC 时间；如果提供了 release 发布时间，则优先使用该时间。
- 每个 asset 包含 `name`、`download_url`、`size`、`sha256`。
  校验大小不能代替 SHA-256，摘要也不等于数字签名；仍需信任 HTTPS 来源及清单。

下载页用 `published_at` 展示日期。浏览器直接下载不会运行本地校验；推荐通过安装脚本
或 `lh update` 安装。Unix 安装脚本复用已有 Python 3 依赖计算摘要，不额外依赖 macOS
默认没有的 `sha256sum`。

此修复改变未来生成的清单，不重写已发布 release 的资产。旧版清单中浮动的 URL
在目标版本与最新版本不同时可能导致校验失败；这时安装会停止，不会静默装错版本。
缺少摘要的旧清单也会被拒绝。

## 离线回归

```sh
python3 -m unittest discover -s scripts -p test_release_integrity.py -v
sh -n scripts/install-lh.sh
```

测试直接执行工作流中的清单生成代码，并用本地假下载覆盖 Linux、macOS 平台选择及
Windows PowerShell 的验证顺序。覆盖正确摘要、大小写摘要、缺失摘要和篡改摘要，
不下载真实安装包、不访问模型、不修改用户安装目录。未安装 PowerShell 的环境会明确
跳过 Windows 用例；应在 Windows 或带 `pwsh` 的 CI 中补跑。
