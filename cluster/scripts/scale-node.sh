#!/bin/bash
# ============================================================
# Incus 集群节点扩缩容管理工具
# 用途：自动化添加/移除集群节点，含安全检查
# 前提：在集群现有节点上运行，需要 root 权限
# ============================================================
set -euo pipefail

# ==================== 配置区 ====================
MIN_CLUSTER_NODES=3          # 法定人数最小节点数
PROMETHEUS_CONFIG="/etc/prometheus/prometheus.yml"
PROMETHEUS_TARGETS_DIR="/etc/prometheus/targets.d"
FIREWALL_WHITELIST="/etc/nftables.d/cluster-whitelist.nft"
NOTIFY_WEBHOOK=""            # 通知 Webhook URL（可选）
JOIN_SCRIPT_PATH="$(dirname "$0")/join-node.sh"
UPDATE_TARGETS_SCRIPT="$(dirname "$0")/update-monitoring-targets.sh"
# ==================== 配置区结束 ==================

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

log()  { echo -e "${GREEN}[INFO]${NC} $*"; }
warn() { echo -e "${YELLOW}[WARN]${NC} $*"; }
err()  { echo -e "${RED}[ERR]${NC} $*"; exit 1; }
step() { echo -e "${CYAN}[STEP]${NC} $*"; }

usage() {
    cat <<EOF
用法: $(basename "$0") <模式> [选项]

模式:
  --add     <节点IP> [--name <名称>]    添加新节点到集群
  --remove  <节点名称>                  从集群移除节点
  --status                             显示集群扩缩容状态

选项:
  --name <名称>       指定新节点名称（--add 模式，默认自动生成）
  --force             跳过确认提示（谨慎使用）
  --no-notify         不发送通知
  -h, --help          显示此帮助

示例:
  $(basename "$0") --add 202.151.179.228 --name node04
  $(basename "$0") --remove node04
  $(basename "$0") --status
EOF
    exit 0
}

# ─── 参数解析 ────────────────────────────────────────────────

MODE=""
NODE_IP=""
NODE_NAME=""
FORCE=false
NO_NOTIFY=false

while [[ $# -gt 0 ]]; do
    case "$1" in
        --add)     MODE="add";    NODE_IP="${2:-}"; shift 2 || err "--add 需要指定节点 IP" ;;
        --remove)  MODE="remove"; NODE_NAME="${2:-}"; shift 2 || err "--remove 需要指定节点名称" ;;
        --status)  MODE="status"; shift ;;
        --name)    NODE_NAME="${2:-}"; shift 2 || err "--name 需要指定名称" ;;
        --force)   FORCE=true; shift ;;
        --no-notify) NO_NOTIFY=true; shift ;;
        -h|--help) usage ;;
        *)         err "未知参数: $1\n使用 --help 查看帮助" ;;
    esac
done

[ -z "$MODE" ] && usage

# ─── 输入校验 ────────────────────────────────────────────────

validate_ipv4() {
    local ip="$1"
    local IFS='.'
    read -ra octets <<< "$ip"
    [ "${#octets[@]}" -eq 4 ] || return 1
    for octet in "${octets[@]}"; do
        [[ "$octet" =~ ^[0-9]+$ ]] || return 1
        [ "$octet" -ge 0 ] && [ "$octet" -le 255 ] || return 1
    done
    return 0
}

if [ "$MODE" = "add" ] && [ -n "$NODE_IP" ]; then
    validate_ipv4 "$NODE_IP" || err "无效的 IPv4 地址: ${NODE_IP}"
fi

# 节点名称仅允许字母、数字、连字符（Incus 命名规范）
if [ -n "$NODE_NAME" ]; then
    [[ "$NODE_NAME" =~ ^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?$ ]] || err "无效的节点名称: ${NODE_NAME}（仅允许字母、数字、连字符）"
fi

# ─── 通用函数 ────────────────────────────────────────────────

get_cluster_members() {
    incus cluster list --format csv 2>/dev/null | awk -F',' '{print $1}'
}

get_cluster_member_count() {
    incus cluster list --format csv 2>/dev/null | wc -l
}

confirm_action() {
    local msg="$1"
    if [ "$FORCE" = true ]; then
        return 0
    fi
    echo -e "${YELLOW}[确认]${NC} ${msg}"
    read -rp "继续？(y/N): " answer
    [[ "$answer" =~ ^[Yy]$ ]] || { echo "已取消"; exit 0; }
}

send_notify() {
    if [ "$NO_NOTIFY" = true ] || [ -z "$NOTIFY_WEBHOOK" ]; then
        return 0
    fi
    local message="$1"
    local json_payload
    json_payload=$(jq -n --arg text "[Incus 集群] $message" '{text: $text}')
    curl -s -X POST "$NOTIFY_WEBHOOK" \
        -H 'Content-Type: application/json' \
        -d "$json_payload" \
        --max-time 10 || warn "通知发送失败"
}

# ─── 添加节点 ────────────────────────────────────────────────

add_node() {
    [ -z "$NODE_IP" ] && err "请指定要添加的节点 IP"

    step "1/5 检查前置条件"
    [ -f "$JOIN_SCRIPT_PATH" ] || err "找不到 join-node.sh: ${JOIN_SCRIPT_PATH}"
    ping -c 1 -W 3 "$NODE_IP" >/dev/null 2>&1 || err "节点 ${NODE_IP} 不可达"

    # 检查是否已在集群中
    if get_cluster_members | grep -qFx "$NODE_NAME" 2>/dev/null; then
        err "节点 ${NODE_NAME} 已在集群中"
    fi

    local current_count
    current_count=$(get_cluster_member_count)
    log "当前集群节点数: ${current_count}"

    confirm_action "将节点 ${NODE_IP} (${NODE_NAME:-自动命名}) 加入集群"

    step "2/5 调用 join-node.sh 加入集群"
    local join_args=("$NODE_IP")
    [ -n "$NODE_NAME" ] && join_args+=(--name "$NODE_NAME")
    bash "$JOIN_SCRIPT_PATH" "${join_args[@]}"

    # 获取实际节点名称（如果是自动生成的）
    if [ -z "$NODE_NAME" ]; then
        NODE_NAME=$(incus cluster list --format csv | tail -1 | awk -F',' '{print $1}')
        log "新节点名称: ${NODE_NAME}"
    fi

    step "3/5 更新监控配置"
    if [ -f "$UPDATE_TARGETS_SCRIPT" ]; then
        bash "$UPDATE_TARGETS_SCRIPT"
        log "Prometheus 采集目标已更新"
    else
        warn "未找到 update-monitoring-targets.sh，请手动更新监控配置"
    fi

    step "4/5 更新防火墙白名单"
    update_firewall_add "$NODE_IP"

    step "5/5 发送通知"
    local new_count
    new_count=$(get_cluster_member_count)
    send_notify "节点 ${NODE_NAME} (${NODE_IP}) 已加入集群，当前节点数: ${new_count}"

    echo ""
    log "========================================="
    log "节点添加完成"
    log "  名称: ${NODE_NAME}"
    log "  IP:   ${NODE_IP}"
    log "  集群节点数: ${new_count}"
    log "========================================="
}

# ─── 移除节点 ────────────────────────────────────────────────

remove_node() {
    [ -z "$NODE_NAME" ] && err "请指定要移除的节点名称"

    step "1/7 安全检查"
    local current_count
    current_count=$(get_cluster_member_count)
    log "当前集群节点数: ${current_count}"

    # 法定人数检查：移除后剩余节点必须 >= MIN_CLUSTER_NODES
    local remaining=$((current_count - 1))
    if [ "$remaining" -lt "$MIN_CLUSTER_NODES" ]; then
        err "安全拒绝：移除后剩余 ${remaining} 个节点，低于法定人数最低要求 (${MIN_CLUSTER_NODES})"
    fi

    # 检查节点是否存在
    if ! get_cluster_members | grep -qFx "$NODE_NAME"; then
        err "节点 ${NODE_NAME} 不在集群中"
    fi

    # 获取节点 IP（用于后续清理）
    local node_ip
    node_ip=$(incus cluster show "$NODE_NAME" 2>/dev/null | grep -oP 'https://\K[^:]+' | head -1 || echo "")

    step "2/7 检查节点上的 VM"
    local vm_list
    vm_list=$(incus list --target "$NODE_NAME" --format csv -c n 2>/dev/null || echo "")
    local vm_count=0
    if [ -n "$vm_list" ]; then
        vm_count=$(echo "$vm_list" | wc -l)
    fi

    if [ "$vm_count" -gt 0 ]; then
        log "节点上有 ${vm_count} 台 VM 需要疏散:"
        echo "$vm_list" | sed 's/^/    /'
    fi

    confirm_action "将从集群移除节点 ${NODE_NAME}（${vm_count} 台 VM 将被疏散）"

    step "3/7 疏散 VM"
    if [ "$vm_count" -gt 0 ]; then
        log "开始疏散 VM..."
        incus cluster evacuate "$NODE_NAME" --force
        log "VM 疏散完成"

        # 等待疏散完成
        local retry=0
        while [ $retry -lt 30 ]; do
            local remaining_vms
            remaining_vms=$(incus list --target "$NODE_NAME" --format csv -c n 2>/dev/null | wc -l)
            if [ "$remaining_vms" -eq 0 ]; then
                break
            fi
            log "等待 VM 迁移完成... (剩余 ${remaining_vms} 台)"
            sleep 10
            retry=$((retry + 1))
        done

        if [ $retry -ge 30 ]; then
            err "VM 疏散超时（5 分钟），请手动检查"
        fi
    else
        log "节点上无 VM，跳过疏散"
    fi

    step "4/7 移除 Ceph OSD"
    remove_ceph_osd "$NODE_NAME"

    step "5/7 移除 Incus 集群成员"
    incus cluster remove "$NODE_NAME" --force
    log "节点 ${NODE_NAME} 已从 Incus 集群移除"

    step "6/7 更新监控配置和防火墙"
    if [ -f "$UPDATE_TARGETS_SCRIPT" ]; then
        bash "$UPDATE_TARGETS_SCRIPT"
        log "Prometheus 采集目标已更新"
    fi
    if [ -n "$node_ip" ]; then
        update_firewall_remove "$node_ip"
    fi

    step "7/7 发送通知"
    local new_count
    new_count=$(get_cluster_member_count)
    send_notify "节点 ${NODE_NAME} 已从集群移除（疏散 ${vm_count} 台 VM），剩余节点: ${new_count}"

    echo ""
    log "========================================="
    log "节点移除完成"
    log "  名称: ${NODE_NAME}"
    log "  疏散 VM: ${vm_count} 台"
    log "  剩余节点数: ${new_count}"
    log "========================================="
}

# ─── Ceph OSD 清理 ───────────────────────────────────────────

remove_ceph_osd() {
    local target_node="$1"

    # 检查是否有 Ceph
    if ! command -v ceph &>/dev/null; then
        warn "未安装 ceph CLI，跳过 OSD 清理"
        return 0
    fi

    # 查找该节点上的 OSD（通过环境变量传递节点名，避免 shell→Python 注入）
    local osd_ids
    osd_ids=$(ceph osd tree --format json 2>/dev/null | \
        TARGET_NODE="$target_node" python3 -c "
import json, sys, os
data = json.load(sys.stdin)
target = os.environ['TARGET_NODE']
for node in data.get('nodes', []):
    if node.get('name') == target and node.get('type') == 'host':
        for child_id in node.get('children', []):
            print(child_id)
" 2>/dev/null || echo "")

    if [ -z "$osd_ids" ]; then
        log "节点 ${target_node} 上未找到 Ceph OSD"
        return 0
    fi

    log "找到 OSD: ${osd_ids}"

    for osd_id in $osd_ids; do
        log "正在移除 OSD.${osd_id}..."

        # 标记 out，让数据迁移
        ceph osd out "osd.${osd_id}" 2>/dev/null || true

        # 等待数据迁移（最长 10 分钟）
        log "等待 Ceph 数据迁移..."
        local retry=0
        while [ $retry -lt 60 ]; do
            local status
            status=$(ceph health --format json 2>/dev/null | python3 -c "
import json, sys
data = json.load(sys.stdin)
print(data.get('status', 'UNKNOWN'))
" 2>/dev/null || echo "UNKNOWN")

            if [ "$status" = "HEALTH_OK" ] || [ "$status" = "HEALTH_WARN" ]; then
                break
            fi
            sleep 10
            retry=$((retry + 1))
        done

        # 停止并清除 OSD
        ceph osd crush remove "osd.${osd_id}" 2>/dev/null || true
        ceph auth del "osd.${osd_id}" 2>/dev/null || true
        ceph osd rm "osd.${osd_id}" 2>/dev/null || true

        log "OSD.${osd_id} 已移除"
    done
}

# ─── 防火墙管理 ──────────────────────────────────────────────

update_firewall_add() {
    local ip="$1"
    if [ ! -f "$FIREWALL_WHITELIST" ]; then
        warn "防火墙白名单文件不存在: ${FIREWALL_WHITELIST}"
        return 0
    fi

    local escaped_ip="${ip//./\\.}"
    if grep -qP "(^|[,\s])${escaped_ip}([,\s]|$)" "$FIREWALL_WHITELIST" 2>/dev/null; then
        log "IP ${ip} 已在防火墙白名单中"
        return 0
    fi

    # 在 elements 块的末尾花括号前插入新 IP（仅匹配 set 块内缩进的 }）
    sed -i "/^[[:space:]]*elements[[:space:]]*=/{
        :loop
        /}/! { N; b loop }
        s/\([[:space:]]*\)}/\1    ${ip},\n\1}/
    }" "$FIREWALL_WHITELIST" 2>/dev/null || {
        warn "sed 插入失败，请手动编辑 ${FIREWALL_WHITELIST}"
        return 1
    }

    nft -f "$FIREWALL_WHITELIST" 2>/dev/null || warn "nftables 重载失败，请手动检查"
    log "防火墙白名单已添加: ${ip}"
}

update_firewall_remove() {
    local ip="$1"
    if [ ! -f "$FIREWALL_WHITELIST" ]; then
        return 0
    fi

    # 转义 IP 中的点，完整匹配避免误删（如 1.1 不会删 1.10）
    local escaped_ip="${ip//./\\.}"
    sed -i "/^[[:space:]]*${escaped_ip}[,[:space:]]*$/d" "$FIREWALL_WHITELIST" 2>/dev/null || true
    nft -f "$FIREWALL_WHITELIST" 2>/dev/null || warn "nftables 重载失败"
    log "防火墙白名单已移除: ${ip}"
}

# ─── 集群状态 ────────────────────────────────────────────────

show_status() {
    echo ""
    echo -e "${CYAN}═══════════════════════════════════════════${NC}"
    echo -e "${CYAN}         Incus 集群扩缩容状态               ${NC}"
    echo -e "${CYAN}═══════════════════════════════════════════${NC}"
    echo ""

    # 集群成员
    step "集群成员"
    incus cluster list 2>/dev/null || err "无法获取集群信息，确认已在集群节点上运行"
    echo ""

    local member_count
    member_count=$(get_cluster_member_count)
    log "总节点数: ${member_count}  |  法定人数最低: ${MIN_CLUSTER_NODES}  |  可移除: $((member_count - MIN_CLUSTER_NODES)) 个"
    echo ""

    # 各节点 VM 分布
    step "VM 分布"
    for member in $(get_cluster_members); do
        local count
        count=$(incus list --target "$member" --format csv -c n 2>/dev/null | wc -l)
        echo "  ${member}: ${count} 台 VM"
    done
    echo ""

    # Ceph 状态
    if command -v ceph &>/dev/null; then
        step "Ceph 状态"
        ceph status 2>/dev/null || warn "无法获取 Ceph 状态"
        echo ""
    fi

    # 资源使用
    step "资源概览"
    incus cluster list --format csv 2>/dev/null | while IFS=',' read -r name url roles arch status message; do
        echo "  ${name}: 状态=${status}"
    done
    echo ""
}

# ─── 主逻辑 ──────────────────────────────────────────────────

case "$MODE" in
    add)    add_node ;;
    remove) remove_node ;;
    status) show_status ;;
    *)      usage ;;
esac
