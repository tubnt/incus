#!/usr/bin/env bash
# WireGuard 隧道部署脚本
# 用途: 生成密钥对、渲染配置、启动隧道、健康检查
set -euo pipefail

# ============================================================
# 配置
# ============================================================
WG_IFACE="${WG_IFACE:-wg0}"
WG_PORT="${WG_PORT:-51820}"
WG_CONFIG_DIR="${WG_CONFIG_DIR:-/etc/wireguard}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TEMPLATE_DIR="${SCRIPT_DIR}/../configs/wireguard"

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log_info()  { echo -e "${GREEN}[INFO]${NC} $*"; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC} $*"; }
log_error() { echo -e "${RED}[ERROR]${NC} $*" >&2; }

# ============================================================
# 前置检查
# ============================================================
check_prerequisites() {
    if [[ $EUID -ne 0 ]]; then
        log_error "请以 root 权限运行此脚本"
        exit 1
    fi

    if ! command -v wg &>/dev/null; then
        log_info "安装 WireGuard..."
        apt-get update && apt-get install -y wireguard-tools
    fi
}

# ============================================================
# 密钥管理
# ============================================================
generate_keypair() {
    local name="$1"

    # 防止路径穿越：名称仅允许字母数字._-
    if [[ ! "$name" =~ ^[a-zA-Z0-9._-]+$ ]]; then
        log_error "密钥名称包含非法字符: ${name}（仅允许字母数字._-）"
        exit 1
    fi

    local key_dir="${WG_CONFIG_DIR}/keys"
    mkdir -p "$key_dir"
    chmod 700 "$key_dir"

    if [[ -f "${key_dir}/${name}.key" ]]; then
        log_warn "密钥已存在: ${name}，跳过生成"
    else
        # 私钥直接写入文件，不经过 stdout，防止泄露
        wg genkey > "${key_dir}/${name}.key"
        chmod 600 "${key_dir}/${name}.key"
        wg pubkey < "${key_dir}/${name}.key" > "${key_dir}/${name}.pub"
        log_info "已生成密钥对: ${name}"
    fi

    log_info "密钥路径: ${key_dir}/${name}.key"
    log_info "公钥: $(cat "${key_dir}/${name}.pub")"
}

# ============================================================
# 配置生成: Paymenter 端（客户端）
# ============================================================
setup_client() {
    local wg_address="${1:-10.100.0.1/24}"

    log_info "生成 Paymenter 端密钥..."
    generate_keypair "paymenter"

    local private_key
    private_key=$(cat "${WG_CONFIG_DIR}/keys/paymenter.key")

    cat > "${WG_CONFIG_DIR}/${WG_IFACE}.conf" <<EOF
[Interface]
Address = ${wg_address}
PrivateKey = ${private_key}
ListenPort = ${WG_PORT}
EOF

    chmod 600 "${WG_CONFIG_DIR}/${WG_IFACE}.conf"
    log_info "Paymenter 端基础配置已生成: ${WG_CONFIG_DIR}/${WG_IFACE}.conf"
    log_info "使用 add-peer 命令添加集群 Peer"
}

# ============================================================
# 添加 Peer（集群端点）
# ============================================================
add_peer() {
    local peer_name="$1"
    local peer_pubkey="$2"
    local peer_endpoint="$3"
    local peer_allowed_ips="$4"
    local config_file="${WG_CONFIG_DIR}/${WG_IFACE}.conf"

    # 输入验证: 防止配置注入
    if [[ ! "$peer_name" =~ ^[a-zA-Z0-9._-]+$ ]]; then
        log_error "Peer 名称包含非法字符: ${peer_name}（仅允许字母数字._-）"
        exit 1
    fi
    if [[ ! "$peer_pubkey" =~ ^[A-Za-z0-9+/]{42}[AEIMQUYcgkosw048]=$ ]]; then
        log_error "公钥格式无效: ${peer_pubkey}（应为 44 字符 Base64）"
        exit 1
    fi
    # Endpoint 必须是 host 或 IP（不含端口），端口由 WG_PORT 统一指定
    if [[ ! "$peer_endpoint" =~ ^[a-zA-Z0-9._-]+$ ]] && \
       [[ ! "$peer_endpoint" =~ ^\[?[0-9a-fA-F:]+\]?$ ]]; then
        log_error "Endpoint 格式无效: ${peer_endpoint}（应为主机名或 IP，不含端口）"
        exit 1
    fi
    if [[ ! "$peer_allowed_ips" =~ ^[0-9a-fA-F.:,/ ]+$ ]]; then
        log_error "AllowedIPs 格式无效: ${peer_allowed_ips}"
        exit 1
    fi

    if [[ ! -f "$config_file" ]]; then
        log_error "配置文件不存在: ${config_file}，请先运行 setup-client 或 setup-server"
        exit 1
    fi

    cat >> "$config_file" <<EOF

# ${peer_name}
[Peer]
PublicKey = ${peer_pubkey}
Endpoint = ${peer_endpoint}:${WG_PORT}
AllowedIPs = ${peer_allowed_ips}
PersistentKeepalive = 25
EOF

    log_info "已添加 Peer: ${peer_name} (${peer_endpoint})"
}

# ============================================================
# 配置生成: 集群端（服务端）
# ============================================================
setup_server() {
    local node_name="${1:?用法: setup-server <节点名> <隧道IP>}"
    local wg_address="${2:?用法: setup-server <节点名> <隧道IP>}"

    # 节点名校验（generate_keypair 内部也会校验，此处提前报错给出更明确的上下文）
    if [[ ! "$node_name" =~ ^[a-zA-Z0-9._-]+$ ]]; then
        log_error "节点名称包含非法字符: ${node_name}（仅允许字母数字._-）"
        exit 1
    fi

    log_info "生成 ${node_name} 端密钥..."
    generate_keypair "$node_name"

    local private_key
    private_key=$(cat "${WG_CONFIG_DIR}/keys/${node_name}.key")

    cat > "${WG_CONFIG_DIR}/${WG_IFACE}.conf" <<EOF
[Interface]
Address = ${wg_address}
PrivateKey = ${private_key}
ListenPort = ${WG_PORT}
EOF

    chmod 600 "${WG_CONFIG_DIR}/${WG_IFACE}.conf"
    log_info "${node_name} 端基础配置已生成"
}

# ============================================================
# 启动/停止隧道
# ============================================================
start_tunnel() {
    if wg show "$WG_IFACE" &>/dev/null; then
        log_warn "隧道 ${WG_IFACE} 已运行，重启中..."
        wg-quick down "$WG_IFACE" || true
    fi

    wg-quick up "$WG_IFACE"
    systemctl enable "wg-quick@${WG_IFACE}" 2>/dev/null || true
    log_info "隧道 ${WG_IFACE} 已启动并设为开机自启"
}

stop_tunnel() {
    wg-quick down "$WG_IFACE" || true
    systemctl disable "wg-quick@${WG_IFACE}" 2>/dev/null || true
    log_info "隧道 ${WG_IFACE} 已停止"
}

# ============================================================
# 健康检查
# ============================================================
health_check() {
    log_info "WireGuard 隧道健康检查..."

    if ! wg show "$WG_IFACE" &>/dev/null; then
        log_error "隧道 ${WG_IFACE} 未运行"
        return 1
    fi

    log_info "接口状态:"
    wg show "$WG_IFACE"

    echo ""
    log_info "Peer 连通性检查:"
    local all_ok=true

    while IFS= read -r allowed_ip; do
        # 提取第一个 IP（去掉 CIDR）
        local ip
        ip=$(echo "$allowed_ip" | cut -d',' -f1 | cut -d'/' -f1 | tr -d ' ')
        if ping -c 2 -W 3 "$ip" &>/dev/null; then
            log_info "  ${ip} — 可达"
        else
            log_warn "  ${ip} — 不可达"
            all_ok=false
        fi
    done < <(wg show "$WG_IFACE" allowed-ips | awk '{print $2}')

    if $all_ok; then
        log_info "所有 Peer 连通正常"
    else
        log_warn "部分 Peer 不可达，请检查配置"
        return 1
    fi
}

# ============================================================
# 显示状态
# ============================================================
show_status() {
    if wg show "$WG_IFACE" &>/dev/null; then
        wg show "$WG_IFACE"
    else
        log_warn "隧道 ${WG_IFACE} 未运行"
    fi
}

# ============================================================
# 主入口
# ============================================================
usage() {
    cat <<EOF
用法: $(basename "$0") <命令> [参数]

命令:
  setup-client [隧道IP]                           生成 Paymenter 端配置（默认 10.100.0.1/24）
  setup-server <节点名> <隧道IP>                   生成集群端配置
  add-peer <名称> <公钥> <端点> <AllowedIPs>       添加 Peer
  start                                           启动隧道
  stop                                            停止隧道
  health                                          健康检查
  status                                          显示隧道状态
  keygen <名称>                                   生成密钥对

示例:
  $(basename "$0") setup-client
  $(basename "$0") setup-server cluster-a 10.100.0.10/24
  $(basename "$0") add-peer cluster-a <pubkey> 203.0.113.1 "10.100.0.10/32, 10.0.10.0/24"
  $(basename "$0") start
  $(basename "$0") health
EOF
}

main() {
    local cmd="${1:-help}"
    shift || true

    check_prerequisites

    case "$cmd" in
        setup-client) setup_client "$@" ;;
        setup-server) setup_server "$@" ;;
        add-peer)     add_peer "$@" ;;
        start)        start_tunnel ;;
        stop)         stop_tunnel ;;
        health)       health_check ;;
        status)       show_status ;;
        keygen)       generate_keypair "${1:?用法: keygen <名称>}" ;;
        help|--help|-h) usage ;;
        *) log_error "未知命令: ${cmd}"; usage; exit 1 ;;
    esac
}

main "$@"
