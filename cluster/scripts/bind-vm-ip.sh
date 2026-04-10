#!/usr/bin/env bash
# VM 公网 IP 绑定脚本
# 绑定 IP 地址并启用安全过滤（MAC/IP/端口隔离）
set -euo pipefail

# ── 常量 ──────────────────────────────────────────────
IP_PREFIX="202.151.179"
IP_MIN=232
IP_MAX=254
SUBNET_MASK=27
GATEWAY="${IP_PREFIX}.225"
DEVICE="eth0"

# ── 帮助 ──────────────────────────────────────────────
usage() {
    cat <<'EOF'
用法: bind-vm-ip.sh <VM名称> <IP地址> [--bandwidth <限速>]

参数:
  VM名称       Incus 虚拟机名称
  IP地址       要绑定的公网 IP（范围 202.151.179.232-254）

选项:
  --bandwidth <限速>   设置带宽限速，如 100Mbit（同时限制上下行）
  --help              显示此帮助信息

示例:
  bind-vm-ip.sh my-vm 202.151.179.232
  bind-vm-ip.sh my-vm 202.151.179.240 --bandwidth 100Mbit
EOF
    exit 0
}

# ── 日志 ──────────────────────────────────────────────
log()  { echo "[$(date '+%H:%M:%S')] $*"; }
err()  { echo "[$(date '+%H:%M:%S')] 错误: $*" >&2; }
die()  { err "$@"; exit 1; }

# ── 参数解析 ──────────────────────────────────────────
[[ "${1:-}" == "--help" ]] && usage
[[ $# -lt 2 ]] && { err "参数不足"; usage; }

VM_NAME="$1"
IP_ADDR="$2"
shift 2

BANDWIDTH=""
while [[ $# -gt 0 ]]; do
    case "$1" in
        --bandwidth) BANDWIDTH="$2"; shift 2 ;;
        --help)      usage ;;
        *)           die "未知参数: $1" ;;
    esac
done

# ── 校验 IP ──────────────────────────────────────────
validate_ip() {
    local ip="$1"

    # 格式检查
    if ! [[ "$ip" =~ ^${IP_PREFIX}\.([0-9]+)$ ]]; then
        die "IP 地址 ${ip} 不在允许的子网 ${IP_PREFIX}.0/${SUBNET_MASK} 内"
    fi

    local last_octet="${BASH_REMATCH[1]}"
    if (( last_octet < IP_MIN || last_octet > IP_MAX )); then
        die "IP 地址最后一段 ${last_octet} 不在允许范围 ${IP_MIN}-${IP_MAX} 内"
    fi
}

# ── 校验 VM ──────────────────────────────────────────
validate_vm() {
    local vm="$1"
    if ! incus info "$vm" &>/dev/null; then
        die "虚拟机 '${vm}' 不存在或无法访问"
    fi
}

# ── 主流程 ──────────────────────────────────────────
log "开始绑定 IP: VM=${VM_NAME}, IP=${IP_ADDR}"

# 校验 VM 名称格式
if ! [[ "$VM_NAME" =~ ^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$ ]]; then
    die "VM 名称不合法（仅允许小写字母、数字和连字符，长度 1-63）: ${VM_NAME}"
fi

validate_ip "$IP_ADDR"
log "IP 地址校验通过"

validate_vm "$VM_NAME"
log "虚拟机校验通过"

# 绑定 IP + 启用三重过滤
log "绑定 IP 并启用安全过滤..."
incus config device override "${VM_NAME}" "${DEVICE}" \
    ipv4.address="${IP_ADDR}" \
    security.ipv4_filtering=true \
    security.mac_filtering=true \
    security.port_isolation=true

# 可选：带宽限速
if [[ -n "$BANDWIDTH" ]]; then
    log "设置带宽限速: ${BANDWIDTH}..."
    incus config device set "${VM_NAME}" "${DEVICE}" limits.ingress="${BANDWIDTH}"
    incus config device set "${VM_NAME}" "${DEVICE}" limits.egress="${BANDWIDTH}"
fi

# 验证绑定结果
log "验证绑定结果..."
RESULT=$(incus config device show "${VM_NAME}" | grep -A 20 "^${DEVICE}:" || true)

if echo "$RESULT" | grep -q "ipv4.address: ${IP_ADDR}"; then
    log "IP 绑定成功"
else
    die "IP 绑定验证失败，请手动检查配置"
fi

if echo "$RESULT" | grep -q "security.ipv4_filtering: \"true\""; then
    log "IPv4 过滤已启用"
else
    err "警告: IPv4 过滤状态异常"
fi

if echo "$RESULT" | grep -q "security.mac_filtering: \"true\""; then
    log "MAC 过滤已启用"
else
    err "警告: MAC 过滤状态异常"
fi

if echo "$RESULT" | grep -q "security.port_isolation: \"true\""; then
    log "端口隔离已启用"
else
    err "警告: 端口隔离状态异常"
fi

log "完成: ${VM_NAME} <- ${IP_ADDR}"
