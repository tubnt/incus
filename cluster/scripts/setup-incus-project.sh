#!/bin/bash
# setup-incus-project.sh — 初始化 Incus customers project（受限模式 + 资源配额）
# 安全隔离第 2 层：Project 级别隔离，配合受限证书限制租户操作范围
set -euo pipefail

# ── 颜色 ──
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; NC='\033[0m'
log()  { echo -e "${GREEN}[INFO]${NC} $*"; }
warn() { echo -e "${YELLOW}[WARN]${NC} $*"; }
err()  { echo -e "${RED}[ERR]${NC}  $*" >&2; exit 1; }

# ── 默认配置 ──
PROJECT_NAME="customers"
LIMITS_INSTANCES=50
LIMITS_CPU=100
LIMITS_MEMORY="200GiB"
LIMITS_DISK="5TiB"

usage() {
    cat <<EOF
${CYAN}setup-incus-project.sh${NC} — 初始化 Incus 租户 Project

用法:
  $(basename "$0") [选项]

选项:
  --project <name>       Project 名称（默认: ${PROJECT_NAME}）
  --limits-instances <n> 实例上限（默认: ${LIMITS_INSTANCES}）
  --limits-cpu <n>       CPU 核数上限（默认: ${LIMITS_CPU}）
  --limits-memory <size> 内存上限（默认: ${LIMITS_MEMORY}）
  --limits-disk <size>   磁盘上限（默认: ${LIMITS_DISK}）
  --help                 显示此帮助信息

示例:
  $(basename "$0")
  $(basename "$0") --project customers --limits-instances 100
EOF
    exit 0
}

# ── 参数解析 ──
while [[ $# -gt 0 ]]; do
    case "$1" in
        --project)          PROJECT_NAME="$2";      shift 2 ;;
        --limits-instances) LIMITS_INSTANCES="$2";   shift 2 ;;
        --limits-cpu)       LIMITS_CPU="$2";         shift 2 ;;
        --limits-memory)    LIMITS_MEMORY="$2";      shift 2 ;;
        --limits-disk)      LIMITS_DISK="$2";        shift 2 ;;
        --help|-h)          usage ;;
        *)                  err "未知参数: $1（使用 --help 查看帮助）" ;;
    esac
done

# ── 前置检查 ──
[[ "$(id -u)" -ne 0 ]] && err "请使用 root 执行此脚本"
command -v incus >/dev/null || err "Incus 未安装"

# ── 创建 Project ──
if incus project list --format csv | cut -d',' -f1 | grep -qx "${PROJECT_NAME}"; then
    warn "Project '${PROJECT_NAME}' 已存在，跳过创建"
else
    log "创建 Project: ${PROJECT_NAME}"
    incus project create "${PROJECT_NAME}"
fi

# ── 设置受限模式 ──
log "配置受限模式（restricted=true）"
incus project set "${PROJECT_NAME}" restricted=true
incus project set "${PROJECT_NAME}" restricted.devices.disk=allow

# ── 资源配额 ──
log "设置资源配额"
incus project set "${PROJECT_NAME}" limits.instances="${LIMITS_INSTANCES}"
incus project set "${PROJECT_NAME}" limits.cpu="${LIMITS_CPU}"
incus project set "${PROJECT_NAME}" limits.memory="${LIMITS_MEMORY}"
incus project set "${PROJECT_NAME}" limits.disk="${LIMITS_DISK}"

# ── 验证 ──
log "验证 Project 配置:"
incus project show "${PROJECT_NAME}"

log "Project '${PROJECT_NAME}' 初始化完成"
log "  受限模式: true"
log "  实例上限: ${LIMITS_INSTANCES}"
log "  CPU 上限: ${LIMITS_CPU} 核"
log "  内存上限: ${LIMITS_MEMORY}"
log "  磁盘上限: ${LIMITS_DISK}"
