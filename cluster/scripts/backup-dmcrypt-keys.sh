#!/bin/bash
# ============================================================
# Ceph dmcrypt 密钥备份脚本
# 用途: 导出 MON 数据库中的 dm-crypt 加密密钥
#
# ★ 重要: MON 全部挂掉 = 加密数据永久丢失
#   定期备份密钥至集群外部安全位置是必须的
#
# 用法:
#   backup-dmcrypt-keys.sh [备份目录]
#
# 建议: 通过 cron 定期执行
#   0 2 * * * /path/to/backup-dmcrypt-keys.sh /secure/backup/
# ============================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/../configs/cluster-env.sh"

# ==================== 配置 ====================
BACKUP_DIR="${1:-/var/backup/ceph-dmcrypt}"
MAX_BACKUPS=30  # 保留最近 N 份备份
TIMESTAMP=$(date +%Y%m%d_%H%M%S)

# ==================== 日志 ====================
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log()  { echo -e "${GREEN}[INFO]${NC} $*"; }
warn() { echo -e "${YELLOW}[WARN]${NC} $*"; }
err()  { echo -e "${RED}[ERR]${NC} $*" >&2; exit 1; }

# 远程执行
run_on() {
  local node="$1"; shift
  local ip
  ip=$(get_node_field "$node" 3)
  ssh -o StrictHostKeyChecking=no "${SSH_USER}@${ip}" "$@"
}

# ==================== 主逻辑 ====================
main() {
  # 确保备份文件创建时即为 600 权限，避免短暂 world-readable 窗口
  umask 077

  log "开始备份 dmcrypt 密钥..."

  # 找到可用的 MON 节点
  local mon_node=""
  for node in $(get_mon_nodes); do
    if run_on "$node" "ceph health" >/dev/null 2>&1; then
      mon_node="$node"
      break
    fi
  done

  if [[ -z "$mon_node" ]]; then
    err "无法连接到任何 MON 节点，备份失败"
  fi
  log "使用 MON 节点: ${mon_node}"

  # 创建备份目录
  mkdir -p "${BACKUP_DIR}"
  chmod 700 "${BACKUP_DIR}"

  local backup_file="${BACKUP_DIR}/dmcrypt-keys_${TIMESTAMP}.json"

  # 导出 dmcrypt 密钥（使用 python3 生成合法 JSON，避免 shell 拼接导致格式损坏）
  log "导出 dm-crypt 密钥..."
  local key_count=0

  run_on "$mon_node" "python3 -c '
import subprocess, json, sys
out = subprocess.check_output([\"ceph\", \"config-key\", \"ls\"], text=True)
keys = [k for k in out.strip().splitlines() if \"dm-crypt\" in k]
result = {}
for key in keys:
    val = subprocess.check_output([\"ceph\", \"config-key\", \"get\", key], text=True).strip()
    result[key] = val
json.dump(result, sys.stdout, indent=2)
print()
'" > "$backup_file" 2>/dev/null

  key_count=$(run_on "$mon_node" "ceph config-key ls 2>/dev/null | grep -c dm-crypt" 2>/dev/null || echo "0")

  if [[ "$key_count" -eq 0 ]]; then
    warn "未发现 dmcrypt 密钥（OSD 可能未使用 dmcrypt 或尚未部署）"
    rm -f "$backup_file"
    return 0
  fi

  chmod 600 "$backup_file"
  log "已导出 ${key_count} 个密钥到: ${backup_file}"

  # 同时备份 MON 的其他关键信息
  local extra_file="${BACKUP_DIR}/ceph-auth_${TIMESTAMP}.txt"
  run_on "$mon_node" "ceph auth ls" > "$extra_file" 2>/dev/null
  chmod 600 "$extra_file"
  log "已备份 Ceph 认证信息: ${extra_file}"

  # 清理旧备份（使用 find 替代 ls 管道解析，避免特殊字符问题）
  local backup_count
  backup_count=$(find "${BACKUP_DIR}" -maxdepth 1 -name 'dmcrypt-keys_*.json' -type f | wc -l)
  if [[ "$backup_count" -gt "$MAX_BACKUPS" ]]; then
    local to_delete=$((backup_count - MAX_BACKUPS))
    log "清理 ${to_delete} 份旧备份..."
    find "${BACKUP_DIR}" -maxdepth 1 -name 'dmcrypt-keys_*.json' -type f -printf '%T@ %p\n' \
      | sort -n | head -n "$to_delete" | cut -d' ' -f2- | xargs -d '\n' rm -f
    find "${BACKUP_DIR}" -maxdepth 1 -name 'ceph-auth_*.txt' -type f -printf '%T@ %p\n' \
      | sort -n | head -n "$to_delete" | cut -d' ' -f2- | xargs -d '\n' rm -f
  fi

  log "备份完成"
  log "★ 请将 ${BACKUP_DIR} 同步到集群外部安全位置（如加密 USB、异地备份服务器）"
}

main "$@"
