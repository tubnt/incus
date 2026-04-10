#!/usr/bin/env bash
#
# setup-firewall.sh — Incus 集群防火墙部署脚本
#
# 功能：
#   - 从模板生成 nftables 规则配置
#   - 部署 systemd 服务确保规则在 Incus 启动前加载
#   - 支持 --apply（应用）和 --dry-run（预览）模式
#
# 用法：
#   setup-firewall.sh --apply  [--node-ip IP] [--bridge NAME]
#   setup-firewall.sh --dry-run [--node-ip IP] [--bridge NAME]
#
set -euo pipefail

# ============================================================
# 默认配置
# ============================================================
MGMT_NET="10.0.10.0/24"
CEPH_PUBLIC="10.0.20.0/24"
CEPH_CLUSTER="10.0.30.0/24"
BRIDGE_NAME="br-pub"
NODE_IP=""
MODE=""

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TEMPLATE="${SCRIPT_DIR}/../configs/nftables/nftables.conf.template"
SYSTEMD_UNIT_SRC="${SCRIPT_DIR}/../configs/systemd/nftables-vm-filter.service"

NFTABLES_CONF="/etc/nftables-vm-filter.conf"
SYSTEMD_UNIT_DST="/etc/systemd/system/nftables-vm-filter.service"

# ============================================================
# 辅助函数
# ============================================================
log_info()  { echo "[INFO]  $*"; }
log_warn()  { echo "[WARN]  $*" >&2; }
log_error() { echo "[ERROR] $*" >&2; }

# 校验 IPv4 地址格式
validate_ip() {
    local ip="$1" label="$2"
    if [[ ! "${ip}" =~ ^[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}$ ]]; then
        log_error "${label} 格式无效: ${ip}"
        exit 1
    fi
}

# 校验 CIDR 格式
validate_cidr() {
    local cidr="$1" label="$2"
    if [[ ! "${cidr}" =~ ^[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}/[0-9]{1,2}$ ]]; then
        log_error "${label} CIDR 格式无效: ${cidr}"
        exit 1
    fi
}

# 校验网卡名（仅允许字母数字和连字符）
validate_ifname() {
    local name="$1" label="$2"
    if [[ ! "${name}" =~ ^[a-zA-Z0-9_-]+$ ]]; then
        log_error "${label} 接口名无效: ${name}"
        exit 1
    fi
}

usage() {
    cat <<'USAGE'
用法:
  setup-firewall.sh --apply   应用防火墙规则并启用 systemd 服务
  setup-firewall.sh --dry-run 预览生成的 nftables 规则（不修改系统）

选项:
  --node-ip IP       当前节点公网 IP（默认自动检测 br-pub 上的 IP）
  --bridge NAME      VM 桥接网卡名（默认 br-pub）
  --mgmt-net CIDR    管理网段（默认 10.0.10.0/24）
  --ceph-public CIDR Ceph Public 网段（默认 10.0.20.0/24）
  --ceph-cluster CIDR Ceph Cluster 网段（默认 10.0.30.0/24）
  -h, --help         显示帮助
USAGE
    exit 0
}

detect_node_ip() {
    local ip
    # 从桥接网卡获取公网 IP
    ip=$(ip -4 addr show dev "${BRIDGE_NAME}" 2>/dev/null \
        | grep -oP 'inet \K[0-9.]+' | head -1) || true
    if [[ -z "${ip}" ]]; then
        # 回退：从默认路由接口获取
        ip=$(ip -4 route get 8.8.8.8 2>/dev/null \
            | grep -oP 'src \K[0-9.]+' | head -1) || true
    fi
    echo "${ip}"
}

# ============================================================
# 参数解析
# ============================================================
while [[ $# -gt 0 ]]; do
    case "$1" in
        --apply)      MODE="apply";  shift ;;
        --dry-run)    MODE="dry-run"; shift ;;
        --node-ip)    NODE_IP="$2";   shift 2 ;;
        --bridge)     BRIDGE_NAME="$2"; shift 2 ;;
        --mgmt-net)   MGMT_NET="$2";   shift 2 ;;
        --ceph-public)  CEPH_PUBLIC="$2";  shift 2 ;;
        --ceph-cluster) CEPH_CLUSTER="$2"; shift 2 ;;
        -h|--help)    usage ;;
        *)            log_error "未知参数: $1"; usage ;;
    esac
done

if [[ -z "${MODE}" ]]; then
    log_error "必须指定 --apply 或 --dry-run"
    usage
fi

# ============================================================
# 检测 / 验证节点 IP
# ============================================================
if [[ -z "${NODE_IP}" ]]; then
    NODE_IP=$(detect_node_ip)
fi

if [[ -z "${NODE_IP}" ]]; then
    log_error "无法检测节点 IP，请使用 --node-ip 手动指定"
    exit 1
fi

# 校验所有输入参数
validate_ip    "${NODE_IP}"      "节点 IP"
validate_cidr  "${MGMT_NET}"     "管理网段"
validate_cidr  "${CEPH_PUBLIC}"  "Ceph Public"
validate_cidr  "${CEPH_CLUSTER}" "Ceph Cluster"
validate_ifname "${BRIDGE_NAME}" "桥接设备"

log_info "节点 IP: ${NODE_IP}"
log_info "桥接设备: ${BRIDGE_NAME}"
log_info "管理网段: ${MGMT_NET}"
log_info "Ceph Public: ${CEPH_PUBLIC}"
log_info "Ceph Cluster: ${CEPH_CLUSTER}"

# ============================================================
# 检查模板文件
# ============================================================
if [[ ! -f "${TEMPLATE}" ]]; then
    log_error "模板文件不存在: ${TEMPLATE}"
    exit 1
fi

# ============================================================
# 生成 nftables 配置
# ============================================================
generate_config() {
    sed \
        -e "s|__NODE_IP__|${NODE_IP}|g" \
        -e "s|__BRIDGE_NAME__|${BRIDGE_NAME}|g" \
        -e "s|__MGMT_NET__|${MGMT_NET}|g" \
        -e "s|__CEPH_PUBLIC__|${CEPH_PUBLIC}|g" \
        -e "s|__CEPH_CLUSTER__|${CEPH_CLUSTER}|g" \
        "${TEMPLATE}"
}

# ============================================================
# dry-run 模式：仅预览
# ============================================================
if [[ "${MODE}" == "dry-run" ]]; then
    log_info "=== dry-run 模式：预览生成的 nftables 规则 ==="
    echo ""
    generate_config
    echo ""
    log_info "=== 预览结束（未修改系统）==="

    # 验证语法（如果 nft 可用）
    if command -v nft &>/dev/null; then
        local_tmp=$(mktemp)
        generate_config > "${local_tmp}"
        if nft -c -f "${local_tmp}" 2>/dev/null; then
            log_info "nft 语法检查: 通过"
        else
            log_warn "nft 语法检查: 失败（可能需要 root 权限）"
        fi
        rm -f "${local_tmp}"
    fi
    exit 0
fi

# ============================================================
# apply 模式：部署规则
# ============================================================
if [[ "${MODE}" == "apply" ]]; then
    # 需要 root 权限
    if [[ $EUID -ne 0 ]]; then
        log_error "apply 模式需要 root 权限"
        exit 1
    fi

    # 1. 备份现有配置
    if [[ -f "${NFTABLES_CONF}" ]]; then
        backup="${NFTABLES_CONF}.bak.$(date +%Y%m%d%H%M%S)"
        cp "${NFTABLES_CONF}" "${backup}"
        log_info "已备份现有配置到 ${backup}"
    fi

    # 2. 生成并写入 nftables 配置
    generate_config > "${NFTABLES_CONF}"
    chmod 600 "${NFTABLES_CONF}"
    log_info "已写入 ${NFTABLES_CONF}"

    # 3. 语法验证
    if ! nft -c -f "${NFTABLES_CONF}"; then
        log_error "nftables 语法验证失败！"
        if [[ -n "${backup:-}" ]]; then
            cp "${backup}" "${NFTABLES_CONF}"
            log_info "已回滚到备份配置"
        fi
        exit 1
    fi
    log_info "nftables 语法验证通过"

    # 4. 加载规则
    nft -f "${NFTABLES_CONF}"
    log_info "nftables 规则已加载"

    # 5. 部署 systemd 服务
    if [[ ! -f "${SYSTEMD_UNIT_SRC}" ]]; then
        log_error "systemd unit 文件不存在: ${SYSTEMD_UNIT_SRC}"
        exit 1
    fi

    cp "${SYSTEMD_UNIT_SRC}" "${SYSTEMD_UNIT_DST}"
    chmod 644 "${SYSTEMD_UNIT_DST}"
    log_info "已部署 ${SYSTEMD_UNIT_DST}"

    systemctl daemon-reload
    systemctl enable nftables-vm-filter.service
    log_info "systemd 服务已启用（开机自动加载，Incus 启动前执行）"

    # 6. 验证
    echo ""
    log_info "=== 当前 nftables 规则集 ==="
    nft list ruleset bridge
    echo ""
    nft list table inet host_filter
    echo ""
    log_info "=== 部署完成 ==="

    # 7. 提示确认
    echo ""
    log_info "验证清单："
    log_info "  1. SSH 连接是否正常（当前会话未断开 = 正常）"
    log_info "  2. 测试 VM 访问: nft list chain bridge vm_filter forward"
    log_info "  3. 测试 Ceph 端口: ceph -s（应正常）"
    log_info "  4. 测试 Incus API: incus list（应正常）"
    log_info "  5. systemd 状态: systemctl status nftables-vm-filter"
fi
