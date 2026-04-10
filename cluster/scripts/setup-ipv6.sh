#!/bin/bash
# ============================================================
# IPv6 双栈网络配置脚本
# 用途：为 Incus 集群配置 IPv6 网络支持
# 前提：已完成 IPv4 网桥配置，已获得 IPv6 /48 前缀
# ============================================================
set -euo pipefail

# ==================== 配置区 ====================
BRIDGE_NAME="br-pub"                 # 网桥名称
IPV6_PREFIX=""                       # IPv6 /48 前缀（如 2001:db8:abcd::），留空则自动检测
IPV6_PREFIX_LEN="48"                 # 前缀长度
IPV6_GATEWAY=""                      # IPv6 网关，留空则自动检测
DNS_V6="2606:4700:4700::1111,2001:4860:4860::8888"  # IPv6 DNS
NETPLAN_DIR="/etc/netplan"
PROFILE_NAME="vm-public"
# ==================== 配置区结束 ==================

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log()  { echo -e "${GREEN}[INFO]${NC} $*"; }
warn() { echo -e "${YELLOW}[WARN]${NC} $*"; }
err()  { echo -e "${RED}[ERR]${NC} $*"; exit 1; }

# ---------- 0. 权限检查 ----------
[ "$(id -u)" -eq 0 ] || err "请使用 root 权限运行"

# ---------- 1. 检测 IPv6 参数 ----------
detect_ipv6() {
    log "1. 检测 IPv6 网络参数..."

    if [ -z "${IPV6_PREFIX}" ]; then
        # 从网桥或物理接口检测全局 IPv6 地址
        local ipv6_addr
        ipv6_addr=$(ip -6 addr show "${BRIDGE_NAME}" scope global 2>/dev/null \
            | awk '/inet6/{print $2}' | head -1)

        if [ -z "${ipv6_addr}" ]; then
            local iface
            iface=$(ip route show default | awk '{print $5}' | head -1)
            ipv6_addr=$(ip -6 addr show "${iface}" scope global 2>/dev/null \
                | awk '/inet6/{print $2}' | head -1)
        fi

        [ -z "${ipv6_addr}" ] && err "无法检测 IPv6 地址，请手动设置 IPV6_PREFIX"

        # 用 python3 ipaddress 模块规范化提取 /48 前缀（兼容所有缩写格式）
        IPV6_PREFIX=$(python3 -c "
import ipaddress, sys
addr = sys.argv[1].split('/')[0]
net = ipaddress.IPv6Network(addr + '/${IPV6_PREFIX_LEN}', strict=False)
print(str(net.network_address))
" "${ipv6_addr}") || err "IPv6 前缀提取失败"
        # 追加 :: 后缀便于后续拼接
        IPV6_PREFIX="${IPV6_PREFIX%::}::"
        log "   检测到 IPv6 前缀: ${IPV6_PREFIX}/${IPV6_PREFIX_LEN}"
    fi

    if [ -z "${IPV6_GATEWAY}" ]; then
        IPV6_GATEWAY=$(ip -6 route show default 2>/dev/null | awk '{print $3}' | head -1)
        [ -z "${IPV6_GATEWAY}" ] && warn "未检测到 IPv6 网关，使用链路本地网关"
        log "   IPv6 网关: ${IPV6_GATEWAY:-fe80::1}"
    fi
}

# ---------- 2. 配置网桥 IPv6 ----------
setup_bridge_ipv6() {
    log "2. 配置网桥 ${BRIDGE_NAME} IPv6 地址..."

    # 宿主机使用 /48 前缀的第一个 /64（::1 作为网关角色）
    local host_ipv6="${IPV6_PREFIX%::}:0:0:0:1/64"

    # 检查是否已配置
    if ip -6 addr show "${BRIDGE_NAME}" | grep -q "${IPV6_PREFIX%::}:0:0:0:1"; then
        log "   网桥 IPv6 已配置，跳过"
        return
    fi

    ip -6 addr add "${host_ipv6}" dev "${BRIDGE_NAME}" 2>/dev/null || true
    log "   已添加 ${host_ipv6} 到 ${BRIDGE_NAME}"
}

# ---------- 3. 更新 netplan 配置 ----------
update_netplan_ipv6() {
    log "3. 更新 netplan 配置..."

    # 查找现有的网桥 netplan 配置
    local netplan_file
    netplan_file=$(grep -rl "${BRIDGE_NAME}" "${NETPLAN_DIR}"/ 2>/dev/null | head -1)

    if [ -z "${netplan_file}" ]; then
        warn "未找到 ${BRIDGE_NAME} 的 netplan 配置，跳过 netplan 更新"
        warn "请手动在 netplan 中添加 IPv6 配置"
        return
    fi

    # 备份
    cp "${netplan_file}" "${netplan_file}.bak.$(date +%s)"

    # 检查是否已有 IPv6 配置
    if grep -q "addresses:.*${IPV6_PREFIX}" "${netplan_file}" 2>/dev/null; then
        log "   netplan 已包含 IPv6 配置，跳过"
        return
    fi

    local host_ipv6="${IPV6_PREFIX%::}:0:0:0:1/64"

    # 在网桥的 addresses 段追加 IPv6
    # 通过环境变量传递参数，避免 heredoc 注入
    export _NETPLAN_FILE="${netplan_file}"
    export _BRIDGE_NAME="${BRIDGE_NAME}"
    export _HOST_IPV6="${host_ipv6}"
    export _IPV6_GATEWAY="${IPV6_GATEWAY}"
    export _DNS_V6="${DNS_V6}"

    python3 << 'PYEOF'
import yaml, sys, os

netplan_file = os.environ["_NETPLAN_FILE"]
bridge_name = os.environ["_BRIDGE_NAME"]
host_ipv6 = os.environ["_HOST_IPV6"]
ipv6_gw = os.environ.get("_IPV6_GATEWAY") or "fe80::1"
dns_v6 = os.environ["_DNS_V6"]

with open(netplan_file, "r") as f:
    config = yaml.safe_load(f)

bridges = config.get("network", {}).get("bridges", {})
if bridge_name not in bridges:
    print(f"未在 netplan 中找到 {bridge_name} 配置")
    sys.exit(0)

br = bridges[bridge_name]
addresses = br.get("addresses", [])

if host_ipv6 not in addresses:
    addresses.append(host_ipv6)
    br["addresses"] = addresses

routes = br.get("routes", [])
ipv6_default = {"to": "::/0", "via": ipv6_gw}
if not any(r.get("to") == "::/0" for r in routes):
    routes.append(ipv6_default)
    br["routes"] = routes

ns = br.get("nameservers", {})
addrs = ns.get("addresses", [])
for dns in dns_v6.split(","):
    if dns not in addrs:
        addrs.append(dns)
ns["addresses"] = addrs
br["nameservers"] = ns

with open(netplan_file, "w") as f:
    yaml.dump(config, f, default_flow_style=False, allow_unicode=True)

print("netplan IPv6 配置已更新")
PYEOF

    unset _NETPLAN_FILE _BRIDGE_NAME _HOST_IPV6 _IPV6_GATEWAY _DNS_V6

    log "   netplan 配置已更新"
}

# ---------- 4. nftables IPv6 规则 ----------
setup_nftables_ipv6() {
    log "4. 配置 nftables IPv6 规则..."

    # 检查是否已有 ip6 表
    if nft list tables | grep -q "ip6 filter-v6"; then
        log "   ip6 filter-v6 表已存在，跳过"
        return
    fi

    nft -f - << 'NFTEOF'
table ip6 filter-v6 {
    chain input {
        type filter hook input priority 0; policy drop;

        # 允许已建立的连接
        ct state established,related accept

        # 允许环回
        iifname "lo" accept

        # ICMPv6（邻居发现、路由通告等必需协议）
        icmpv6 type {
            echo-request,
            echo-reply,
            nd-neighbor-solicit,
            nd-neighbor-advert,
            nd-router-solicit,
            nd-router-advert,
            mld-listener-query,
            mld-listener-report
        } accept

        # SSH
        tcp dport 22 accept

        # DHCPv6 客户端
        udp dport 546 accept
    }

    chain forward {
        type filter hook forward priority 0; policy drop;

        # 允许已建立的连接转发
        ct state established,related accept

        # 允许网桥转发
        iifname "br-pub" accept
        oifname "br-pub" accept

        # ICMPv6 转发
        icmpv6 type {
            echo-request,
            echo-reply,
            nd-neighbor-solicit,
            nd-neighbor-advert
        } accept
    }

    chain output {
        type filter hook output priority 0; policy accept;
    }
}
NFTEOF

    log "   ip6 filter-v6 规则已加载"

    # 仅导出 ip6 表到独立文件，避免覆盖 IPv4 规则
    mkdir -p /etc/nftables.d
    nft list table ip6 filter-v6 > /etc/nftables.d/ipv6.conf 2>/dev/null || true
    # 确保主配置包含 nftables.d 目录
    if ! grep -q 'include "/etc/nftables.d/' /etc/nftables.conf 2>/dev/null; then
        echo 'include "/etc/nftables.d/*.conf"' >> /etc/nftables.conf
    fi
    log "   规则已持久化到 /etc/nftables.d/ipv6.conf"
}

# ---------- 5. 启用 IPv6 转发 ----------
enable_ipv6_forwarding() {
    log "5. 启用 IPv6 转发..."

    if sysctl -n net.ipv6.conf.all.forwarding 2>/dev/null | grep -q "1"; then
        log "   IPv6 转发已启用"
        return
    fi

    sysctl -w net.ipv6.conf.all.forwarding=1 > /dev/null
    sysctl -w net.ipv6.conf.default.forwarding=1 > /dev/null

    # 持久化
    if ! grep -q "net.ipv6.conf.all.forwarding" /etc/sysctl.conf 2>/dev/null; then
        echo "net.ipv6.conf.all.forwarding=1" >> /etc/sysctl.conf
        echo "net.ipv6.conf.default.forwarding=1" >> /etc/sysctl.conf
    fi

    log "   IPv6 转发已启用并持久化"
}

# ---------- 6. 验证 ----------
verify_ipv6() {
    log "6. 验证 IPv6 配置..."

    echo ""
    echo "========== IPv6 配置摘要 =========="
    echo "前缀:    ${IPV6_PREFIX}/${IPV6_PREFIX_LEN}"
    echo "网关:    ${IPV6_GATEWAY:-fe80::1}"
    echo "DNS:     ${DNS_V6}"
    echo ""

    echo "--- 网桥 IPv6 地址 ---"
    ip -6 addr show "${BRIDGE_NAME}" scope global 2>/dev/null || echo "(无)"
    echo ""

    echo "--- IPv6 转发状态 ---"
    echo "all.forwarding = $(sysctl -n net.ipv6.conf.all.forwarding 2>/dev/null || echo '未知')"
    echo ""

    echo "--- nftables ip6 规则 ---"
    nft list tables | grep "ip6" || echo "(无 ip6 规则)"
    echo ""
    echo "==================================="

    log "IPv6 双栈配置完成"
    log "每个 VM 将从 ${IPV6_PREFIX}/${IPV6_PREFIX_LEN} 前缀中分配 /64 子网"
}

# ==================== 主流程 ====================
main() {
    log "开始 IPv6 双栈网络配置..."
    echo ""

    detect_ipv6
    setup_bridge_ipv6
    update_netplan_ipv6
    setup_nftables_ipv6
    enable_ipv6_forwarding
    verify_ipv6
}

main "$@"
