#!/usr/bin/env bash
# 批量迁移工具 — 疏散指定节点上的所有 VM
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "${SCRIPT_DIR}/../configs/cluster-env.sh"

usage() {
    cat <<EOF
用法: $(basename "$0") --from <node> [选项]

将指定节点上的所有 VM 逐个热迁移到负载最低的其他节点，
用于节点维护前的疏散操作。

选项:
  --from <node>      源节点名称（必填）
  --cold             使用冷迁移模式
  --dry-run          仅显示迁移计划，不实际执行
  --help             显示帮助

示例:
  $(basename "$0") --from node3
  $(basename "$0") --from node3 --dry-run
  $(basename "$0") --from node3 --cold
EOF
    exit 0
}

# ── 参数解析 ──────────────────────────────────────────────
SOURCE_NODE=""
MIGRATE_MODE="--live"
DRY_RUN=false

while [[ $# -gt 0 ]]; do
    case "$1" in
        --from)     SOURCE_NODE="$2"; shift 2 ;;
        --cold)     MIGRATE_MODE="--cold"; shift ;;
        --dry-run)  DRY_RUN=true; shift ;;
        --help)     usage ;;
        -*)         log_error "未知选项: $1"; exit 1 ;;
        *)          log_error "多余参数: $1"; exit 1 ;;
    esac
done

if [[ -z "$SOURCE_NODE" ]]; then
    log_error "缺少 --from 参数"
    usage
fi

# ── 前置检查 ──────────────────────────────────────────────
require_cmd incus || exit 1

# ── 获取源节点上的 VM 列表 ────────────────────────────────
log_info "获取 ${SOURCE_NODE} 上的 VM 列表..."
mapfile -t VM_LIST < <(incus list --format csv -c nL 2>/dev/null | awk -F, -v node="$SOURCE_NODE" '$2 == node {print $1}')

if [[ ${#VM_LIST[@]} -eq 0 ]]; then
    log_info "节点 ${SOURCE_NODE} 上没有 VM，无需迁移"
    exit 0
fi

log_info "找到 ${#VM_LIST[@]} 个 VM 需要迁移: ${VM_LIST[*]}"

# ── 获取负载最低的目标节点 ────────────────────────────────
# 统计每个节点上的 VM 数量，选择数量最少的节点
get_best_target() {
    local exclude="$1"
    local best_node=""
    local min_count=999999

    # 获取所有在线节点及其 VM 数量
    # 先一次性取回全量 VM 分布，避免 N+1 查询
    local vm_snapshot
    vm_snapshot=$(incus list --format csv -c nL 2>/dev/null || true)

    while IFS=, read -r node status; do
        node=$(echo "$node" | xargs)  # trim
        status=$(echo "$status" | xargs)
        # 只选择 ONLINE 状态的节点，排过滤掉 OFFLINE/EVACUATED
        if [[ "$node" == "$exclude" || "$status" != "ONLINE" ]]; then
            continue
        fi
        local count
        count=$(echo "$vm_snapshot" | awk -F, -v node="$node" '$2 == node' | wc -l)
        if [[ "$count" -lt "$min_count" ]]; then
            min_count=$count
            best_node=$node
        fi
    done < <(incus cluster list --format csv -c ns 2>/dev/null)

    if [[ -z "$best_node" ]]; then
        return 1
    fi
    echo "$best_node"
}

# ── 显示迁移计划 ──────────────────────────────────────────
echo ""
echo "═══════════════════════════════════════════════════"
echo "  批量迁移计划 — 疏散节点: ${SOURCE_NODE}"
echo "═══════════════════════════════════════════════════"
echo ""

# ── 执行迁移 ──────────────────────────────────────────────
TOTAL=${#VM_LIST[@]}
SUCCESS=0
FAILED=0
FAILED_VMS=()

for i in "${!VM_LIST[@]}"; do
    vm="${VM_LIST[$i]}"
    seq=$((i + 1))

    # 每次迁移前重新计算最佳目标节点（|| true 防止 set -e 下 return 1 直接退出脚本）
    target=$(get_best_target "$SOURCE_NODE") || true
    if [[ -z "$target" ]]; then
        log_error "无可用目标节点（所有其他节点均不可用或 OFFLINE）"
        exit 1
    fi

    echo "──────────────────────────────────────────────────"
    log_info "[${seq}/${TOTAL}] ${vm} → ${target}"

    if [[ "$DRY_RUN" == true ]]; then
        echo "  (dry-run) 将迁移 ${vm} 到 ${target}"
        SUCCESS=$((SUCCESS + 1))
        continue
    fi

    if "${SCRIPT_DIR}/migrate-vm.sh" "$vm" --target "$target" "$MIGRATE_MODE"; then
        SUCCESS=$((SUCCESS + 1))
    else
        FAILED=$((FAILED + 1))
        FAILED_VMS+=("$vm")
        log_error "迁移失败: ${vm}，继续处理下一个..."
    fi
done

# ── 汇总报告 ──────────────────────────────────────────────
echo ""
echo "═══════════════════════════════════════════════════"
echo "  批量迁移报告"
echo "═══════════════════════════════════════════════════"
echo "  源节点:   ${SOURCE_NODE}"
echo "  总计:     ${TOTAL} 个 VM"
echo "  成功:     ${SUCCESS}"
echo "  失败:     ${FAILED}"
if [[ ${#FAILED_VMS[@]} -gt 0 ]]; then
    echo "  失败列表: ${FAILED_VMS[*]}"
fi
if [[ "$DRY_RUN" == true ]]; then
    echo "  模式:     dry-run（未实际执行）"
fi
echo "═══════════════════════════════════════════════════"

if [[ $FAILED -gt 0 ]]; then
    exit 1
fi
