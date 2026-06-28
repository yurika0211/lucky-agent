#!/bin/bash
# LuckyAgent 配置文件迁移脚本
set -e

echo "=== LuckyAgent 配置文件迁移 ==="
echo ""

LUCKYAGENT_HOME="${HOME}/.luckyagent"
NEW_DIR="${LUCKYAGENT_HOME}/memory/prompts"

# 检查安装
if [ ! -d "${LUCKYAGENT_HOME}" ]; then
    echo "❌ 未找到 LuckyAgent: ${LUCKYAGENT_HOME}"
    exit 1
fi

# 创建新目录
mkdir -p "${NEW_DIR}/platform" "${NEW_DIR}/functions"

# 备份
BACKUP_FILE="${LUCKYAGENT_HOME}/backup-$(date +%Y%m%d-%H%M%S).tar.gz"
tar -czf "${BACKUP_FILE}" -C "${LUCKYAGENT_HOME}" . 2>/dev/null
echo "✅ 备份已保存: ${BACKUP_FILE}"
echo ""

# 迁移文件
migrate() {
    local old="$1" new="$2" name="$3"
    if [ -f "${old}" ]; then
        if [ -f "${new}" ]; then
            echo "⚠️  ${name} 已存在，跳过"
        else
            cp "${old}" "${new}"
            echo "✅ ${name} 已迁移"
        fi
    fi
}

migrate "${LUCKYAGENT_HOME}/SOUL.md" "${NEW_DIR}/SOUL.md" "SOUL.md"
migrate "${LUCKYAGENT_HOME}/mission.md" "${NEW_DIR}/mission.md" "mission.md"
migrate "${LUCKYAGENT_HOME}/description/AGENTS.md" "${NEW_DIR}/AGENTS.md" "AGENTS.md"
migrate "${LUCKYAGENT_HOME}/workspace/HEARTBEAT.md" "${NEW_DIR}/HEARTBEAT.md" "HEARTBEAT.md"

echo ""
echo "✅ 更新 config.json 中的 soul_path"
if command -v la >/dev/null 2>&1; then
    la config set soul_path "${NEW_DIR}/SOUL.md" >/dev/null 2>&1
    echo "   soul_path 已更新"
elif command -v luckyagent >/dev/null 2>&1; then
    luckyagent config set soul_path "${NEW_DIR}/SOUL.md" >/dev/null 2>&1
    echo "   soul_path 已更新"
else
    echo "   ⚠️  请手动运行: la config set soul_path ~/.luckyagent/memory/prompts/SOUL.md"
fi

echo ""
echo "=== 迁移完成 ==="
echo "配置文件已迁移到: ${NEW_DIR}"
echo ""
echo "清理旧文件:"
echo "  rm ~/.luckyagent/SOUL.md"
echo "  rm ~/.luckyagent/mission.md"
echo "  rm ~/.luckyagent/description/AGENTS.md"
echo "  rm ~/.luckyagent/workspace/HEARTBEAT.md"
