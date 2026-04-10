#!/usr/bin/env bash
# 滚动维护工具 — 逐节点 evacuate + update + restore
# 用于计划内维护：系统更新 / Incus 升级 / Ceph 升级
# 通过热迁移疏散 VM，确保用户无感
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "${SCRIPT_DIR}/../configs/cluster-env.sh"

# ── 常量 ──────────────────────────────────────────────────
NOOUT_TIMEOUT_HOURS=4
NOOUT_TIMEOUT_SEC=$((NOOUT_TIMEOUT_HOURS * 3600))

# ── 帮助 ──────────────────────────────────────────────────
usage() {
    cat <<EOF
用法: $(basename "$0") <node> [选项]

对集群节点执行滚动维护（evacuate + update + restore）。
热迁移所有 VM 到其他节点，执行系统更新，可选重启后恢复节点。

参数:
  <node>            目标节点名称

选项:
  --evacuate        仅疏散（热迁移所有 VM 到其他节点）
  --update          疏散 + 系统更新
  --restore         恢复节点（VM 可回迁）
  --all             全流程：疏散 → 更新 → 恢复
  --reboot          更新后重启节点（仅在 --update/--all 时有效）
  --help            显示帮助

安全约束:
  - 集群中同一时间只允许 1 个节点处于 evacuated 状态
  - 维护前检查 Ceph 健康状态（必须 HEALTH_OK 或 HEALTH_WARN）
  - Ceph noout 设置 ${NOOUT_TIMEOUT_HOURS} 小时超时保护

示例:
  $(basename "$0") node1 --evacuate
  $(basename "$0") node1 --update --reboot
  $(basename "$0") node1 --restore
  $(basename "$0") node1 --all --reboot
EOF
    exit 0
}

# ── 参数解析 ──────────────────────────────────────────────
NODE=""
DO_EVACUATE=false
DO_UPDATE=false
DO_RESTORE=false
DO_REBOOT=false

while [[ $# -gt 0 ]]; do
    case "$1" in
        --evacuate) DO_EVACUATE=true; shift ;;
        --update)   DO_EVACUATE=true; DO_UPDATE=true; shift ;;
        --restore)  DO_RESTORE=true; shift ;;
        --all)      DO_EVACUATE=true; DO_UPDATE=true; DO_RESTORE=true; shift ;;
        --reboot)   DO_REBOOT=true; shift ;;
        --help)     usage ;;
        -*)         log_error "未知选项: $1"; exit 1 ;;
        *)
            if [[ -z "$NODE" ]]; then
                NODE="$1"; shift
            else
                log_error "多余参数: $1"; exit 1
            fi
            ;;
    esac
done

if [[ -z "$NODE" ]]; then
    log_error "缺少节点名称"
    usage
fi

if ! $DO_EVACUATE && ! $DO_UPDATE && ! $DO_RESTORE; then
    log_error "请指定操作: --evacuate / --update / --restore / --all"
    usage
fi

# ── 前置检查 ──────────────────────────────────────────────
require_cmd incus || exit 1
require_cmd ceph  || exit 1

# 检查节点是否属于集群
check_node_in_cluster() {
    if ! incus cluster list --format csv | grep -q "^${NODE},"; then
        log_error "节点 ${NODE} 不在集群中"
        exit 1
    fi
    log_ok "节点 ${NODE} 存在于集群"
}

# 检查 Ceph 健康状态
check_ceph_health() {
    local health
    health=$(ceph health 2>/dev/null)
    case "$health" in
        HEALTH_OK*)
            log_ok "Ceph 状态: HEALTH_OK"
            ;;
        HEALTH_WARN*)
            log_warn "Ceph 状态: HEALTH_WARN — 继续维护"
            ;;
        *)
            log_error "Ceph 状态异常: ${health}"
            log_error "集群处于 degraded 状态，拒绝维护"
            exit 1
            ;;
    esac
}

# 检查是否有其他节点已被疏散（不允许并行维护）
check_no_parallel_maintenance() {
    local evacuated_count
    evacuated_count=$(incus cluster list --format csv | awk -F',' '{print tolower($4)}' | grep -c "evacuated" || true)

    # 如果是 restore 操作，允许当前节点已 evacuated
    if $DO_RESTORE && ! $DO_EVACUATE; then
        local other_evacuated
        other_evacuated=$(incus cluster list --format csv | awk -F',' 'tolower($4)=="evacuated"' | grep -cv "^${NODE}," || true)
        if [[ "$other_evacuated" -gt 0 ]]; then
            log_error "存在其他 evacuated 节点，不允许并行维护"
            exit 1
        fi
        return 0
    fi

    if [[ "$evacuated_count" -gt 0 ]]; then
        log_error "集群中已有 ${evacuated_count} 个节点处于 evacuated 状态"
        log_error "不允许同时维护超过 1 个节点，请先 restore 已疏散节点"
        incus cluster list --format csv | awk -F',' 'tolower($4)=="evacuated"' | while IFS=',' read -r name _ _ status _; do
            log_error "  已疏散节点: ${name}"
        done
        exit 1
    fi
    log_ok "无其他节点处于 evacuated 状态"
}

# ── 阶段 1: 疏散节点 ─────────────────────────────────────
do_evacuate() {
    log_info "========== 阶段 1: 疏散节点 ${NODE} =========="

    # 获取节点上的 VM 列表
    local vm_list
    vm_list=$(incus list --format csv -c nL 2>/dev/null | awk -F',' -v node="$NODE" '$2==node {print $1}')

    if [[ -z "$vm_list" ]]; then
        log_info "节点 ${NODE} 上没有 VM，直接疏散"
    else
        local vm_count
        vm_count=$(echo "$vm_list" | wc -l)
        log_info "节点 ${NODE} 上有 ${vm_count} 个 VM 需要迁移"
        echo "$vm_list" | while read -r vm; do
            log_info "  - ${vm}"
        done
    fi

    # 执行 evacuate（Incus 会自动热迁移所有 VM）
    log_info "执行 incus cluster evacuate ${NODE} ..."
    if ! incus cluster evacuate "$NODE"; then
        log_error "疏散失败: ${NODE}"
        exit 1
    fi
    log_ok "节点 ${NODE} 疏散完成"

    # 验证节点状态
    local node_status
    node_status=$(incus cluster list --format csv | grep "^${NODE}," | awk -F',' '{print $4}')
    if [[ "${node_status,,}" != "evacuated" ]]; then
        log_warn "节点状态预期为 evacuated，实际为: ${node_status}"
    fi

    # 对迁移后的 VM 发送 GARP
    if [[ -n "$vm_list" ]]; then
        log_info "为迁移后的 VM 发送 GARP ..."
        echo "$vm_list" | while read -r vm; do
            local vm_ip
            vm_ip=$(incus list "$vm" --format csv -c 4 2>/dev/null | grep -oP '\d+\.\d+\.\d+\.\d+' | head -1)
            if [[ -n "$vm_ip" ]]; then
                "${SCRIPT_DIR}/send-garp.sh" "$vm_ip" || log_warn "GARP 发送失败: ${vm} (${vm_ip})"
            else
                log_warn "无法获取 VM ${vm} 的 IP，跳过 GARP"
            fi
        done
        log_ok "GARP 发送完成"
    fi
}

# ── 阶段 2: 更新系统 ─────────────────────────────────────
do_update() {
    log_info "========== 阶段 2: 更新系统 ${NODE} =========="

    # 设置 Ceph noout
    log_info "设置 Ceph noout（防止 OSD 下线触发数据迁移）..."
    ceph osd set noout
    log_ok "Ceph noout 已设置"

    # 启动 noout 超时保护（后台）
    log_info "启动 noout 超时保护（${NOOUT_TIMEOUT_HOURS} 小时后自动 unset）..."
    (
        sleep "$NOOUT_TIMEOUT_SEC"
        if ceph osd dump 2>/dev/null | grep -q 'noout'; then
            ceph osd unset noout 2>/dev/null
            log_warn "noout 超时保护触发: 已自动 unset noout（${NOOUT_TIMEOUT_HOURS} 小时）"
        fi
    ) &
    local noout_guard_pid=$!

    # trap 保护：异常退出时清理 guard 进程并警告 noout 状态
    trap 'kill "$noout_guard_pid" 2>/dev/null || true; log_warn "do_update 异常退出，noout 仍处于设置状态，请手动执行: ceph osd unset noout"' ERR

    # 执行系统更新
    log_info "执行系统更新: apt update && apt upgrade -y"
    apt update && apt upgrade -y
    log_ok "系统更新完成"

    # 可选：重启节点
    if $DO_REBOOT; then
        log_info "重启节点 ${NODE} ..."
        # 取消 noout 超时保护进程（重启后会丢失）
        kill "$noout_guard_pid" 2>/dev/null || true

        reboot
        # 脚本到此结束，重启后需手动执行 --restore
        # noout 仍然有效，直到 restore 阶段手动 unset
    else
        log_info "跳过重启（未指定 --reboot）"
        if $DO_RESTORE; then
            # restore 紧接执行，由 restore 阶段 unset noout，guard 不再需要
            kill "$noout_guard_pid" 2>/dev/null || true
        else
            # 无 restore 跟进，保留 guard 作为超时安全网
            log_warn "noout 已设置且无 --restore 跟进，${NOOUT_TIMEOUT_HOURS} 小时后超时自动 unset"
            log_warn "请及时执行 --restore 取消 noout"
        fi
    fi
}

# ── 阶段 3: 恢复节点 ─────────────────────────────────────
do_restore() {
    log_info "========== 阶段 3: 恢复节点 ${NODE} =========="

    # restore 会触发 VM 回迁，先确认 Ceph 健康
    check_ceph_health

    # 恢复节点
    log_info "执行 incus cluster restore ${NODE} ..."
    if ! incus cluster restore "$NODE"; then
        log_error "恢复失败: ${NODE}"
        exit 1
    fi
    log_ok "节点 ${NODE} 已恢复"

    # 取消 Ceph noout
    log_info "取消 Ceph noout ..."
    ceph osd unset noout
    log_ok "Ceph noout 已取消"

    # 验证节点状态
    local node_status
    node_status=$(incus cluster list --format csv | grep "^${NODE}," | awk -F',' '{print $4}')
    log_info "节点 ${NODE} 当前状态: ${node_status}"

    # 验证 Ceph 健康
    local health
    health=$(ceph health 2>/dev/null)
    log_info "Ceph 状态: ${health}"
}

# ── 主流程 ────────────────────────────────────────────────
log_info "滚动维护开始: 节点=${NODE}"
log_info "操作: evacuate=${DO_EVACUATE} update=${DO_UPDATE} restore=${DO_RESTORE} reboot=${DO_REBOOT}"

check_node_in_cluster

if $DO_EVACUATE; then
    check_ceph_health
    check_no_parallel_maintenance
    do_evacuate
fi

if $DO_UPDATE; then
    do_update
fi

if $DO_RESTORE; then
    do_restore
fi

log_ok "滚动维护完成: ${NODE}"
