#!/bin/bash
# ============================================================
# VM 防火墙管理脚本（宿主机统一管控）
# 用法: vm-firewall <命令> [参数]
# ============================================================
set -euo pipefail

TABLE="bridge vm_filter"
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; NC='\033[0m'

usage() {
    cat << EOF
${CYAN}Incus VM 防火墙管理${NC}

${CYAN}用法:${NC}
  $0 status                           查看防火墙状态
  $0 list                             查看所有规则
  $0 open <端口>                      全局开放端口（所有 VM）
  $0 close <端口>                     全局关闭端口
  $0 open-for <VM_IP> <端口>          为指定 VM 开放端口
  $0 close-for <VM_IP> <端口>         为指定 VM 关闭端口
  $0 add-vm <VM_IP>                   注册新 VM IP
  $0 remove-vm <VM_IP>                移除 VM IP
  $0 save                             持久化规则到 /etc/nftables.conf

${CYAN}示例:${NC}
  $0 open 80                          所有 VM 开放 HTTP
  $0 open 443                         所有 VM 开放 HTTPS
  $0 open-for 43.239.84.21 3306       仅 vm-node01 开放 MySQL
  $0 close 80                         关闭全局 HTTP
  $0 add-vm 43.239.84.23              注册新 VM
  $0 save                             保存规则

EOF
    exit 0
}

[ $# -lt 1 ] && usage
CMD="$1"; shift

case "${CMD}" in
    status)
        echo -e "${CYAN}=== VM IP 集合 ===${NC}"
        nft list set ${TABLE} vm_ips 2>/dev/null | grep elements || echo "  (空)"
        echo ""
        echo -e "${CYAN}=== 全局开放端口 ===${NC}"
        nft list set ${TABLE} global_tcp_ports 2>/dev/null | grep elements || echo "  (空)"
        echo ""
        echo -e "${CYAN}=== 自定义规则 ===${NC}"
        nft list chain ${TABLE} forward 2>/dev/null | grep -E "dport|saddr|daddr" | grep -v "@" || echo "  (无)"
        echo ""
        echo -e "${CYAN}=== 统计 ===${NC}"
        nft list chain ${TABLE} forward 2>/dev/null | grep "counter" || echo "  无计数器"
        ;;

    list)
        nft list table ${TABLE} 2>/dev/null
        ;;

    open)
        [ $# -lt 1 ] && { echo "用法: $0 open <端口>"; exit 1; }
        PORT="$1"
        nft add element ${TABLE} global_tcp_ports "{ ${PORT} }"
        echo -e "${GREEN}已全局开放端口 ${PORT}${NC}"
        ;;

    close)
        [ $# -lt 1 ] && { echo "用法: $0 close <端口>"; exit 1; }
        PORT="$1"
        nft delete element ${TABLE} global_tcp_ports "{ ${PORT} }" 2>/dev/null \
            && echo -e "${GREEN}已关闭全局端口 ${PORT}${NC}" \
            || echo -e "${YELLOW}端口 ${PORT} 不在全局列表中${NC}"
        ;;

    open-for)
        [ $# -lt 2 ] && { echo "用法: $0 open-for <VM_IP> <端口>"; exit 1; }
        VM_IP="$1"; PORT="$2"
        nft add rule ${TABLE} forward ip daddr "${VM_IP}" tcp dport "${PORT}" accept \
            comment "\"custom: ${VM_IP}:${PORT}\""
        echo -e "${GREEN}已为 ${VM_IP} 开放端口 ${PORT}${NC}"
        ;;

    close-for)
        [ $# -lt 2 ] && { echo "用法: $0 close-for <VM_IP> <端口>"; exit 1; }
        VM_IP="$1"; PORT="$2"
        HANDLE=$(nft -a list chain ${TABLE} forward 2>/dev/null \
            | grep "daddr ${VM_IP}" | grep "dport ${PORT}" | grep -oP 'handle \K\d+' | head -1)
        if [ -n "${HANDLE}" ]; then
            nft delete rule ${TABLE} forward handle "${HANDLE}"
            echo -e "${GREEN}已关闭 ${VM_IP} 的端口 ${PORT}${NC}"
        else
            echo -e "${YELLOW}未找到 ${VM_IP}:${PORT} 的规则${NC}"
        fi
        ;;

    add-vm)
        [ $# -lt 1 ] && { echo "用法: $0 add-vm <VM_IP>"; exit 1; }
        VM_IP="$1"
        nft add element ${TABLE} vm_ips "{ ${VM_IP} }"
        echo -e "${GREEN}已注册 VM: ${VM_IP}${NC}"
        ;;

    remove-vm)
        [ $# -lt 1 ] && { echo "用法: $0 remove-vm <VM_IP>"; exit 1; }
        VM_IP="$1"
        nft delete element ${TABLE} vm_ips "{ ${VM_IP} }" 2>/dev/null \
            && echo -e "${GREEN}已移除 VM: ${VM_IP}${NC}" \
            || echo -e "${YELLOW}VM ${VM_IP} 不在集合中${NC}"
        ;;

    save)
        nft list ruleset > /etc/nftables.conf
        echo -e "${GREEN}规则已保存到 /etc/nftables.conf${NC}"
        ;;

    *)
        echo -e "${RED}未知命令: ${CMD}${NC}"
        usage
        ;;
esac
