#!/bin/bash
# ============================================================
# VM 防火墙管理脚本（宿主机统一管控）
# 用法: vm-firewall <命令> [参数]
# ============================================================
set -euo pipefail

BR_TABLE="bridge vm_filter"
INET_TABLE="inet host_filter"
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; NC='\033[0m'

usage() {
    cat << EOF
${CYAN}Incus VM 防火墙管理${NC}

${CYAN}端口管理:${NC}
  $0 open <端口>                      全局开放端口（所有 VM）
  $0 close <端口>                     全局关闭端口
  $0 open-for <VM_IP> <端口>          为指定 VM 开放端口
  $0 close-for <VM_IP> <端口>         为指定 VM 关闭端口

${CYAN}白名单（可访问 VM 全部端口）:${NC}
  $0 allow <IP或CIDR>                 添加白名单
  $0 deny <IP或CIDR>                  移除白名单
  $0 whitelist                        查看白名单

${CYAN}VM 管理:${NC}
  $0 add-vm <VM_IP>                   注册新 VM IP
  $0 remove-vm <VM_IP>                移除 VM IP

${CYAN}查看与持久化:${NC}
  $0 status                           查看防火墙状态
  $0 list                             查看完整规则
  $0 save                             持久化到 /etc/nftables.conf

${CYAN}示例:${NC}
  $0 open 80                          所有 VM 开放 HTTP
  $0 open-for 43.239.84.21 3306       仅某台 VM 开放 MySQL
  $0 allow 116.23.45.0/24             整个网段加白名单
  $0 allow 8.8.8.8                    单 IP 加白名单
  $0 deny 116.23.45.0/24              移除白名单
  $0 save                             保存（不 save 重启会丢失）

EOF
    exit 0
}

[ $# -lt 1 ] && usage
CMD="$1"; shift

case "${CMD}" in
    status)
        echo -e "${CYAN}=== VM IP ===${NC}"
        nft list set ${BR_TABLE} vm_ips 2>/dev/null | grep elements || echo "  (空)"
        echo -e "\n${CYAN}=== 全局端口 ===${NC}"
        nft list set ${BR_TABLE} global_tcp_ports 2>/dev/null | grep elements || echo "  (空)"
        echo -e "\n${CYAN}=== 白名单 ===${NC}"
        nft list set ${BR_TABLE} whitelist 2>/dev/null | grep elements || echo "  (空)"
        echo -e "\n${CYAN}=== 自定义规则 ===${NC}"
        nft -a list chain ${BR_TABLE} forward 2>/dev/null | grep "comment" || echo "  (无)"
        echo -e "\n${CYAN}=== 拦截统计 ===${NC}"
        nft list chain ${BR_TABLE} forward 2>/dev/null | grep "counter" || echo "  无"
        ;;

    list)
        echo -e "${CYAN}--- bridge 层（VM 流量过滤）---${NC}"
        nft list table ${BR_TABLE} 2>/dev/null
        echo -e "\n${CYAN}--- inet 层（宿主机保护）---${NC}"
        nft list table ${INET_TABLE} 2>/dev/null
        ;;

    open)
        [ $# -lt 1 ] && { echo "用法: $0 open <端口>"; exit 1; }
        nft add element ${BR_TABLE} global_tcp_ports "{ $1 }"
        echo -e "${GREEN}已全局开放端口 $1${NC}" ;;

    close)
        [ $# -lt 1 ] && { echo "用法: $0 close <端口>"; exit 1; }
        nft delete element ${BR_TABLE} global_tcp_ports "{ $1 }" 2>/dev/null \
            && echo -e "${GREEN}已关闭全局端口 $1${NC}" \
            || echo -e "${YELLOW}端口 $1 不在全局列表中${NC}" ;;

    open-for)
        [ $# -lt 2 ] && { echo "用法: $0 open-for <VM_IP> <端口>"; exit 1; }
        nft add rule ${BR_TABLE} forward ip daddr "$1" tcp dport "$2" accept \
            comment "\"custom: $1:$2\""
        echo -e "${GREEN}已为 $1 开放端口 $2${NC}" ;;

    close-for)
        [ $# -lt 2 ] && { echo "用法: $0 close-for <VM_IP> <端口>"; exit 1; }
        HANDLE=$(nft -a list chain ${BR_TABLE} forward 2>/dev/null \
            | grep "daddr $1" | grep "dport $2" | grep -oP 'handle \K\d+' | head -1)
        [ -n "${HANDLE}" ] \
            && { nft delete rule ${BR_TABLE} forward handle "${HANDLE}"; echo -e "${GREEN}已关闭 $1:$2${NC}"; } \
            || echo -e "${YELLOW}未找到 $1:$2 的规则${NC}" ;;

    allow)
        [ $# -lt 1 ] && { echo "用法: $0 allow <IP或CIDR>"; exit 1; }
        nft add element ${BR_TABLE} whitelist "{ $1 }"
        echo -e "${GREEN}已添加白名单: $1${NC}" ;;

    deny)
        [ $# -lt 1 ] && { echo "用法: $0 deny <IP或CIDR>"; exit 1; }
        nft delete element ${BR_TABLE} whitelist "{ $1 }" 2>/dev/null \
            && echo -e "${GREEN}已移除白名单: $1${NC}" \
            || echo -e "${YELLOW}$1 不在白名单中${NC}" ;;

    whitelist)
        echo -e "${CYAN}=== IP 白名单 ===${NC}"
        nft list set ${BR_TABLE} whitelist 2>/dev/null | grep elements || echo "  (空)"
        echo -e "\n白名单内的 IP 可访问所有 VM 的全部端口" ;;

    add-vm)
        [ $# -lt 1 ] && { echo "用法: $0 add-vm <VM_IP>"; exit 1; }
        nft add element ${BR_TABLE} vm_ips "{ $1 }" 2>/dev/null || true
        nft add element ${INET_TABLE} vm_ips "{ $1 }" 2>/dev/null || true
        echo -e "${GREEN}已注册 VM: $1${NC}" ;;

    remove-vm)
        [ $# -lt 1 ] && { echo "用法: $0 remove-vm <VM_IP>"; exit 1; }
        nft delete element ${BR_TABLE} vm_ips "{ $1 }" 2>/dev/null || true
        nft delete element ${INET_TABLE} vm_ips "{ $1 }" 2>/dev/null || true
        echo -e "${GREEN}已移除 VM: $1${NC}" ;;

    save)
        nft list ruleset > /etc/nftables.conf
        echo -e "${GREEN}已保存到 /etc/nftables.conf${NC}" ;;

    *)
        echo -e "${RED}未知命令: ${CMD}${NC}"
        usage ;;
esac
