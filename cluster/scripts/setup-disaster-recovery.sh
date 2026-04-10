#!/usr/bin/env bash
set -euo pipefail

# ============================================================
# setup-disaster-recovery.sh — Ceph RBD 镜像（异地灾备）配置
# 功能：启用 pool 级别 RBD mirroring，添加 peer，使用 journal-based 模式
# ============================================================

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/../configs/cluster-env.sh"

# ---------- 默认参数 ----------
DR_POOL="${DR_POOL:-incus-pool}"
DR_MIRROR_MODE="${DR_MIRROR_MODE:-image}"
DR_REMOTE_CLUSTER="${DR_REMOTE_CLUSTER:-remote}"
DR_REMOTE_CLIENT="${DR_REMOTE_CLIENT:-client.rbd-mirror.remote}"
DR_REMOTE_MON_HOST="${DR_REMOTE_MON_HOST:-}"
DR_REMOTE_KEY="${DR_REMOTE_KEY:-}"
DR_BOOTSTRAP_TOKEN_FILE="${DR_BOOTSTRAP_TOKEN_FILE:-/tmp/rbd-mirror-bootstrap-token}"

# ---------- 日志工具 ----------
log_info()  { echo "[INFO]  $(date '+%Y-%m-%d %H:%M:%S') $*"; }
log_warn()  { echo "[WARN]  $(date '+%Y-%m-%d %H:%M:%S') $*" >&2; }
log_error() { echo "[ERROR] $(date '+%Y-%m-%d %H:%M:%S') $*" >&2; }

# ---------- 前置检查 ----------
preflight_check() {
    log_info "执行前置检查..."

    if ! ceph status &>/dev/null; then
        log_error "无法连接 Ceph 集群"
        exit 1
    fi

    if ! ceph osd pool ls | grep -q "^${DR_POOL}$"; then
        log_error "Pool '${DR_POOL}' 不存在"
        exit 1
    fi

    if ! command -v rbd &>/dev/null; then
        log_error "rbd 命令未安装"
        exit 1
    fi

    log_info "前置检查通过"
}

# ---------- 启用 pool 镜像 ----------
enable_pool_mirroring() {
    log_info "启用 pool '${DR_POOL}' 的 RBD 镜像功能..."

    # 启用镜像模式（image 级别 — 可按镜像单独控制）
    rbd mirror pool enable "${DR_POOL}" "${DR_MIRROR_MODE}"
    log_info "Pool 镜像模式已启用: ${DR_MIRROR_MODE}"

    # 验证状态
    rbd mirror pool info "${DR_POOL}"
}

# ---------- 生成 bootstrap token（主站点） ----------
generate_bootstrap_token() {
    log_info "生成 bootstrap token（用于远端集群注册）..."

    (umask 077 && rbd mirror pool peer bootstrap create \
        --site-name primary \
        "${DR_POOL}" > "${DR_BOOTSTRAP_TOKEN_FILE}")
    chmod 600 "${DR_BOOTSTRAP_TOKEN_FILE}"

    log_info "Bootstrap token 已保存到: ${DR_BOOTSTRAP_TOKEN_FILE}（权限 0600）"
    log_warn "请将此 token 文件安全传输到远端集群"
}

# ---------- 导入 bootstrap token（远端站点执行） ----------
import_bootstrap_token() {
    local token_file="${1:-${DR_BOOTSTRAP_TOKEN_FILE}}"

    if [[ ! -f "${token_file}" ]]; then
        log_error "Token 文件不存在: ${token_file}"
        exit 1
    fi

    log_info "导入 bootstrap token..."

    rbd mirror pool peer bootstrap import \
        --site-name secondary \
        --direction rx-only \
        "${DR_POOL}" "${token_file}"

    log_info "Bootstrap token 导入成功"
}

# ---------- 手动添加 peer（备选方式） ----------
add_mirror_peer() {
    log_info "添加镜像 peer: ${DR_REMOTE_CLUSTER}..."

    if [[ -z "${DR_REMOTE_MON_HOST}" ]]; then
        log_error "未设置 DR_REMOTE_MON_HOST，无法添加 peer"
        exit 1
    fi

    rbd mirror pool peer add "${DR_POOL}" \
        "${DR_REMOTE_CLIENT}@${DR_REMOTE_CLUSTER}" \
        --remote-mon-host "${DR_REMOTE_MON_HOST}" \
        ${DR_REMOTE_KEY:+--remote-key "${DR_REMOTE_KEY}"}

    log_info "Peer 已添加"
}

# ---------- 为镜像启用 journaling ----------
enable_journaling() {
    log_info "为 pool '${DR_POOL}' 中的镜像启用 journaling..."

    # 设置 pool 默认 feature 包含 journaling
    rbd feature enable "${DR_POOL}" journaling 2>/dev/null || true

    # 遍历现有镜像，逐个启用 journaling
    local images
    images=$(rbd ls "${DR_POOL}" 2>/dev/null || true)

    if [[ -n "${images}" ]]; then
        while IFS= read -r img; do
            log_info "  启用 journaling: ${DR_POOL}/${img}"
            rbd feature enable "${DR_POOL}/${img}" journaling 2>/dev/null || true
            # 启用镜像级别的 image mirror
            rbd mirror image enable "${DR_POOL}/${img}" journal 2>/dev/null || true
        done <<< "${images}"
    fi

    log_info "Journaling 配置完成"
}

# ---------- 部署 rbd-mirror 守护进程 ----------
deploy_rbd_mirror_daemon() {
    log_info "通过 cephadm 部署 rbd-mirror 守护进程..."

    if command -v cephadm &>/dev/null; then
        ceph orch apply rbd-mirror --placement=2
        log_info "rbd-mirror 守护进程已部署 (2 副本)"
    else
        log_warn "cephadm 不可用，请手动部署 rbd-mirror 守护进程"
    fi
}

# ---------- 验证镜像状态 ----------
verify_mirroring() {
    log_info "验证镜像状态..."

    echo "=== Pool 镜像信息 ==="
    rbd mirror pool info "${DR_POOL}"

    echo ""
    echo "=== Pool 镜像状态 ==="
    rbd mirror pool status "${DR_POOL}"

    echo ""
    echo "=== Peer 列表 ==="
    rbd mirror pool peer list "${DR_POOL}" --format=json | python3 -m json.tool 2>/dev/null || true

    log_info "验证完成"
}

# ---------- 使用说明 ----------
usage() {
    cat <<EOF
用法: $(basename "$0") <命令>

命令:
  setup-primary      在主站点配置镜像并生成 bootstrap token
  setup-secondary    在从站点导入 bootstrap token
  add-peer           手动添加镜像 peer
  verify             验证镜像状态

环境变量:
  DR_POOL                 目标 pool (默认: incus-pool)
  DR_MIRROR_MODE          镜像模式 (默认: image)
  DR_REMOTE_CLUSTER       远端集群名称 (默认: remote)
  DR_REMOTE_MON_HOST      远端 MON 地址
  DR_BOOTSTRAP_TOKEN_FILE Bootstrap token 文件路径

示例:
  # 主站点
  $(basename "$0") setup-primary

  # 从站点（将 token 文件复制过来后执行）
  $(basename "$0") setup-secondary

  # 检查状态
  $(basename "$0") verify
EOF
}

# ---------- 主入口 ----------
main() {
    local cmd="${1:-}"

    case "${cmd}" in
        setup-primary)
            preflight_check
            enable_pool_mirroring
            enable_journaling
            deploy_rbd_mirror_daemon
            generate_bootstrap_token
            verify_mirroring
            log_info "主站点配置完成。请将 ${DR_BOOTSTRAP_TOKEN_FILE} 传输到从站点。"
            ;;
        setup-secondary)
            preflight_check
            enable_pool_mirroring
            import_bootstrap_token "${2:-}"
            deploy_rbd_mirror_daemon
            verify_mirroring
            log_info "从站点配置完成。"
            ;;
        add-peer)
            preflight_check
            add_mirror_peer
            verify_mirroring
            ;;
        verify)
            preflight_check
            verify_mirroring
            ;;
        *)
            usage
            exit 1
            ;;
    esac
}

main "$@"
