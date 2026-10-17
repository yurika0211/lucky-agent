# 配置文件迁移指南

**版本**: v0.9.0 及以后  
**日期**: 2026-06-28

## 📢 重要变更

从本版本开始，所有配置文件统一移到 `~/.luckyagent/memory/prompts/` 目录。

## 📦 变更内容

### 文件位置变更

| 文件 | 旧位置 | 新位置 |
|------|--------|--------|
| SOUL.md | `~/.luckyagent/SOUL.md` | `~/.luckyagent/memory/prompts/SOUL.md` |
| mission.md | `~/.luckyagent/mission.md` | `~/.luckyagent/memory/prompts/mission.md` |
| AGENTS.md | `~/.luckyagent/description/AGENTS.md` | `~/.luckyagent/memory/prompts/AGENTS.md` |
| HEARTBEAT.md | `~/.luckyagent/workspace/HEARTBEAT.md` | `~/.luckyagent/memory/prompts/HEARTBEAT.md` |

### 新增功能

- ✅ Prompt 外部化 - 所有系统 prompt 可自定义
- ✅ 热重载 - 修改立即生效，无需重启
- ✅ Fallback 机制 - 删除文件会使用内置默认值
- ✅ 模板变量支持 - 如 `{{memory_vault}}`

## 🔄 迁移步骤

### 自动迁移（推荐）

下载并运行迁移脚本：

```bash
curl -fsSL https://raw.githubusercontent.com/yurika0211/luckyagent/main/scripts/migrate-prompts.sh | bash
```

### 手动迁移

#### 1. 创建新目录

```bash
mkdir -p ~/.luckyagent/memory/prompts/{platform,functions}
```

#### 2. 移动文件

```bash
# SOUL.md
if [ -f ~/.luckyagent/SOUL.md ]; then
  mv ~/.luckyagent/SOUL.md ~/.luckyagent/memory/prompts/SOUL.md
fi

# mission.md
if [ -f ~/.luckyagent/mission.md ]; then
  mv ~/.luckyagent/mission.md ~/.luckyagent/memory/prompts/mission.md
fi

# AGENTS.md
if [ -f ~/.luckyagent/description/AGENTS.md ]; then
  mv ~/.luckyagent/description/AGENTS.md ~/.luckyagent/memory/prompts/AGENTS.md
fi

# HEARTBEAT.md
if [ -f ~/.luckyagent/workspace/HEARTBEAT.md ]; then
  mv ~/.luckyagent/workspace/HEARTBEAT.md ~/.luckyagent/memory/prompts/HEARTBEAT.md
fi
```

#### 3. 验证

```bash
ls -la ~/.luckyagent/memory/prompts/
```

应该看到：
```
SOUL.md
AGENTS.md
mission.md
HEARTBEAT.md
README.md
platform/
functions/
```

#### 4. 清理旧目录（可选）

确认文件已正确迁移后：

```bash
# 删除空目录
rmdir ~/.luckyagent/description 2>/dev/null || true
```

## ✨ 新功能使用

### 自定义 Agent 人格

```bash
vim ~/.luckyagent/memory/prompts/SOUL.md
```

### 自定义工具策略

```bash
vim ~/.luckyagent/memory/prompts/tool_policy.md
```

### 查看所有配置

```bash
tree ~/.luckyagent/memory/prompts/
```

## ⚠️ 注意事项

1. **备份先行** - 迁移前建议备份
   ```bash
   tar -czf ~/.luckyagent-backup-$(date +%Y%m%d).tar.gz ~/.luckyagent/
   ```

2. **重启服务** - 如果 Agent 正在运行，迁移后需重启

3. **向后兼容** - 新版本优先读取新位置，但保留旧位置支持

4. **文件编码** - 所有文件应为 UTF-8 编码

## 🐛 问题排查

### 问题：修改 SOUL.md 后没生效

**原因**：可能在旧位置修改了文件

**解决**：
```bash
# 检查是否在正确位置
cat ~/.luckyagent/memory/prompts/SOUL.md

# 如果不存在，执行迁移
mv ~/.luckyagent/SOUL.md ~/.luckyagent/memory/prompts/SOUL.md
```

### 问题：Agent 找不到配置文件

**原因**：文件未正确迁移或权限问题

**解决**：
```bash
# 检查文件权限
ls -la ~/.luckyagent/memory/prompts/

# 修复权限
chmod 644 ~/.luckyagent/memory/prompts/*.md
```

### 问题：想恢复默认配置

**解决**：
```bash
# 方法1：删除自定义文件，系统会使用内置默认值
rm ~/.luckyagent/memory/prompts/SOUL.md

# 方法2：重新初始化（不会覆盖已有文件）
la init
```

## 📚 更多资源

- [完整文档](https://github.com/yurika0211/luckyagent)
- [Prompt 自定义指南](~/.luckyagent/memory/prompts/README.md)
- [提交问题](https://github.com/yurika0211/luckyagent/issues)

## 📅 时间线

- **2026-06-28**: v0.9.0 发布，配置文件统一到 memory/prompts
- **2026-07-28**: 旧位置将被标记为 deprecated（仍可用）
- **2026-10-28**: 移除对旧位置的支持
