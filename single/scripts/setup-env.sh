#!/bin/bash
# ============================================================
# Incus VM 公网 IP 环境一键初始化脚本
# 用途：在全新宿主机上配置网桥、Profile、防火墙
# 前提：已安装 Incus，有公网 IP 段
# ============================================================
set -euo pipefail

# ==================== 配置区 ====================
# 方式一：手动填写（推荐懂网络的用户）
HOST_IP=""                     # 宿主机公网 IP，留空则自动检测
SUBNET_MASK=""                 # 子网掩码 CIDR（如 /26），留空则自动检测
GATEWAY=""                     # 网关，留空则自动检测
BRIDGE_NAME="br-pub"           # 网桥名称
PHYS_IFACE=""                  # 物理网卡名，留空则自动检测
DNS_SERVERS="8.8.8.8,1.1.1.1"  # DNS
PROFILE_NAME="vm-public"       # Incus Profile 名
VM_CPU="4"                     # 默认 CPU 核数
VM_MEM="8GiB"                  # 默认内存
VM_DISK="50GiB"                # 默认磁盘
# ==================== 配置区结束 ==================

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log()  { echo -e "${GREEN}[INFO]${NC} $*"; }
warn() { echo -e "${YELLOW}[WARN]${NC} $*"; }
err()  { echo -e "${RED}[ERR]${NC} $*"; exit 1; }

# ---------- 0. 自动检测网络参数 ----------
detect_network() {
    log "0. 检测网络参数..."

    # 自动检测物理网卡（第一个有默认路由的非虚拟网卡）
    if [ -z "${PHYS_IFACE}" ]; then
        PHYS_IFACE=$(ip route show default | awk '{print $5}' | head -1)
        [ -z "${PHYS_IFACE}" ] && err "无法检测物理网卡，请手动设置 PHYS_IFACE"
        log "   检测到网卡: ${PHYS_IFACE}"
    fi

    # 自动检测宿主机 IP 和掩码
    if [ -z "${HOST_IP}" ] || [ -z "${SUBNET_MASK}" ]; then
        local ip_cidr
        ip_cidr=$(ip -4 addr show "${PHYS_IFACE}" | awk '/inet /{print $2}' | head -1)
        [ -z "${ip_cidr}" ] && err "无法检测 ${PHYS_IFACE} 的 IP，请手动设置 HOST_IP"
        HOST_IP="${ip_cidr%%/*}"
        SUBNET_MASK="/${ip_cidr##*/}"
        log "   检测到 IP: ${HOST_IP}${SUBNET_MASK}"
    fi

    # 自动检测网关
    if [ -z "${GATEWAY}" ]; then
        GATEWAY=$(ip route show default dev "${PHYS_IFACE}" | awk '{print $3}' | head -1)
        [ -z "${GATEWAY}" ] && err "无法检测网关，请手动设置 GATEWAY"
        log "   检测到网关: ${GATEWAY}"
    fi

    # 显示最终配置
    log "   ─────────────────────────────────"
    log "   网卡:   ${PHYS_IFACE}"
    log "   IP:     ${HOST_IP}${SUBNET_MASK}"
    log "   网关:   ${GATEWAY}"
    log "   DNS:    ${DNS_SERVERS}"
    log "   网桥:   ${BRIDGE_NAME}"
    log "   ─────────────────────────────────"

    # 确认
    echo ""
    read -p "以上配置是否正确？(y/N) " -r confirm
    if [[ ! "${confirm}" =~ ^[Yy]$ ]]; then
        err "已取消，请修改脚本顶部配置区后重新运行"
    fi
}

# ---------- 1. 宿主机优化 ----------
setup_host() {
    log "1. 调整宿主机参数..."
    sysctl -w vm.swappiness=10 >/dev/null
    echo "vm.swappiness=10" > /etc/sysctl.d/99-vm-host.conf
    sysctl -p /etc/sysctl.d/99-vm-host.conf >/dev/null
    log "   swappiness = $(cat /proc/sys/vm/swappiness)"
}

# ---------- 2. 网桥配置 ----------
setup_bridge() {
    log "2. 配置网桥..."

    # 检查是否已配置
    if ip addr show ${BRIDGE_NAME} 2>/dev/null | grep -q "${HOST_IP}"; then
        log "   网桥已配置，跳过"
        return 0
    fi

    PHYS_MAC=$(ip link show ${PHYS_IFACE} | awk '/ether/{print $2}')
    log "   物理网卡 MAC: ${PHYS_MAC}"

    # 备份
    mkdir -p /root/netplan-backup
    cp /etc/netplan/*.yaml /root/netplan-backup/ 2>/dev/null || true

    # 写入新配置
    cat > /etc/netplan/01-bridge.yaml << NETPLAN
network:
  version: 2
  renderer: networkd
  ethernets:
    ${PHYS_IFACE}:
      dhcp4: false
      dhcp6: false
  bridges:
    ${BRIDGE_NAME}:
      macaddress: "${PHYS_MAC}"
      interfaces:
        - ${PHYS_IFACE}
      addresses:
        - "${HOST_IP}${SUBNET_MASK}"
      routes:
        - to: "default"
          via: "${GATEWAY}"
      nameservers:
        addresses: [${DNS_SERVERS}]
      parameters:
        stp: false
        forward-delay: 0
      dhcp4: false
      dhcp6: false
NETPLAN
    chmod 600 /etc/netplan/01-bridge.yaml

    # 语法预检
    # 临时移走旧配置，避免路由冲突
    mkdir -p /tmp/netplan-old
    for f in /etc/netplan/*.yaml; do
        [ "$(basename "$f")" = "01-bridge.yaml" ] && continue
        mv "$f" /tmp/netplan-old/
    done

    if ! netplan generate 2>&1; then
        # 回滚
        cp /tmp/netplan-old/*.yaml /etc/netplan/ 2>/dev/null
        err "netplan 语法检查失败，已回滚"
    fi

    # 写回滚脚本
    cat > /root/netplan-rollback.sh << 'ROLLBACK'
#!/bin/bash
cp /root/netplan-backup/*.yaml /etc/netplan/
mv /etc/netplan/01-bridge.yaml /root/01-bridge.yaml.bak 2>/dev/null
netplan apply
ROLLBACK
    chmod +x /root/netplan-rollback.sh

    # 设置安全网（5 分钟后自动回滚）
    systemd-run --on-active=300 --unit=netplan-rollback /root/netplan-rollback.sh 2>/dev/null || true
    log "   安全网已设置（5 分钟超时回滚）"

    # 应用
    netplan apply 2>/dev/null || true
    sleep 5

    # 自检
    local ok=false
    for i in $(seq 1 30); do
        if ping -c 1 -W 2 ${GATEWAY} >/dev/null 2>&1; then
            ok=true
            break
        fi
        sleep 2
    done

    if [ "$ok" = false ]; then
        warn "网关不可达，等待安全网回滚..."
        exit 1
    fi

    # 成功，取消安全网
    systemctl stop netplan-rollback.timer 2>/dev/null || true
    log "   网桥配置成功！安全网已取消"
}

# ---------- 3. Incus Profile ----------
setup_profile() {
    log "3. 创建 Incus Profile..."

    if incus profile show ${PROFILE_NAME} >/dev/null 2>&1; then
        log "   Profile ${PROFILE_NAME} 已存在，更新中..."
    else
        incus profile create ${PROFILE_NAME}
    fi

    cat << PROFILE | incus profile edit ${PROFILE_NAME}
config:
  limits.cpu: "${VM_CPU}"
  limits.memory: ${VM_MEM}
  security.secureboot: "true"
  security.protection.delete: "true"
description: "公网IP虚拟机模板 - ${VM_CPU}C/${VM_MEM}, 安全加固"
devices:
  eth0:
    name: eth0
    nictype: bridged
    parent: ${BRIDGE_NAME}
    security.mac_filtering: "true"
    security.port_isolation: "true"
    type: nic
  root:
    path: /
    pool: default
    size: ${VM_DISK}
    type: disk
name: ${PROFILE_NAME}
PROFILE

    log "   Profile 创建完成"
}

# ---------- 4. 宿主机防火墙 ----------
setup_firewall() {
    log "4. 配置宿主机防火墙..."

    cat > /etc/nftables.conf << 'NFTCONF'
#!/usr/sbin/nft -f
flush ruleset

table inet filter {
    chain input {
        type filter hook input priority 0; policy drop;
        iif "lo" accept
        ct state established,related accept
        ip protocol icmp accept
        ip6 nexthdr icmpv6 accept
        tcp dport 22 accept
        iif "br-pub" accept
        log prefix "nft-drop: " drop
    }
    chain forward {
        type filter hook forward priority 0; policy accept;
    }
    chain output {
        type filter hook output priority 0; policy accept;
    }
}
NFTCONF

    nft -f /etc/nftables.conf
    systemctl enable nftables 2>/dev/null || true
    log "   防火墙已配置并持久化"
}

# ---------- 5. SSH Key ----------
setup_ssh_key() {
    log "5. 检查 SSH 密钥..."
    if [ ! -f /root/.ssh/id_ed25519 ]; then
        ssh-keygen -t ed25519 -N "" -f /root/.ssh/id_ed25519 >/dev/null 2>&1
        log "   SSH 密钥已生成"
    else
        log "   SSH 密钥已存在"
    fi
}

# ==================== 主流程 ====================
main() {
    echo "=========================================="
    echo " Incus VM 公网 IP 环境初始化"
    echo "=========================================="

    [ "$(id -u)" -ne 0 ] && err "请使用 root 执行此脚本"
    command -v incus >/dev/null || err "Incus 未安装"

    detect_network
    setup_host
    setup_bridge
    setup_profile
    setup_firewall
    setup_ssh_key

    echo ""
    log "=========================================="
    log " 环境初始化完成！"
    log " 网桥: ${BRIDGE_NAME} (${HOST_IP}${SUBNET_MASK})"
    log " Profile: ${PROFILE_NAME}"
    log " 下一步: 使用 create-vm.sh 创建虚拟机"
    log "=========================================="
}

main "$@"
