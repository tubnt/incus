#!/bin/bash
# ============================================================
# 集群配置备份
# 用途：导出 Incus、Ceph、nftables、netplan 配置到时间戳目录
# ============================================================
set -euo pipefail

# ==================== 颜色定义 ====================
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

log()  { echo -e "${GREEN}[INFO]${NC} $*"; }
warn() { echo -e "${YELLOW}[WARN]${NC} $*"; }
err()  { echo -e "${RED}[ERR]${NC} $*"; exit 1; }

# ==================== 默认配置 ====================
BACKUP_BASE="/var/backups/cluster"
COMPRESS=true

# ==================== 帮助信息 ====================
usage() {
    cat << EOF
${CYAN}用法:${NC}
  $0 [选项]

${CYAN}选项:${NC}
  -d, --dir DIR    备份根目录（默认 /var/backups/cluster）
  --no-compress    不压缩打包
  -h, --help       显示帮助

${CYAN}示例:${NC}
  $0                         # 备份到默认目录
  $0 -d /tmp/backup          # 备份到指定目录
  $0 --no-compress           # 不压缩

${CYAN}备份内容:${NC}
  - Incus 集群配置（全局配置 + profile + 网络 + 存储池 + 集群成员）
  - Ceph 配置（ceph.conf + 运行时配置 + crush map + auth）
  - nftables 规则集
  - netplan 配置

EOF
    exit 0
}

# ==================== 参数解析 ====================
while [[ $# -gt 0 ]]; do
    case "$1" in
        -d|--dir)     BACKUP_BASE="$2"; shift 2 ;;
        --no-compress) COMPRESS=false; shift ;;
        -h|--help)    usage ;;
        *)            echo "未知选项: $1"; usage ;;
    esac
done

# ==================== 前置检查 ====================
[[ "$(id -u)" -ne 0 ]] && err "请使用 root 执行此脚本"

# ==================== 创建备份目录 ====================
TIMESTAMP=$(date '+%Y%m%d_%H%M%S')
HOSTNAME=$(hostname -s)
BACKUP_DIR="${BACKUP_BASE}/${TIMESTAMP}_${HOSTNAME}"

mkdir -p "$BACKUP_DIR"
chmod 700 "$BACKUP_DIR"
log "备份目录: ${BACKUP_DIR}"

# 记录备份元信息
cat > "${BACKUP_DIR}/metadata.txt" << EOF
备份时间: $(date '+%Y-%m-%d %H:%M:%S %Z')
主机名: $(hostname)
内核版本: $(uname -r)
备份脚本: $0
EOF

BACKUP_OK=0
BACKUP_FAIL=0

# ==================== Incus 配置备份 ====================
backup_incus() {
    local incus_dir="${BACKUP_DIR}/incus"
    mkdir -p "$incus_dir"

    if ! command -v incus >/dev/null 2>&1; then
        warn "incus 未安装，跳过 Incus 备份"
        return
    fi

    log "导出 Incus 配置..."

    # 全局配置
    if incus config show > "${incus_dir}/global-config.yaml" 2>/dev/null; then
        log "  ✓ 全局配置"
        ((BACKUP_OK++)) || true
    else
        warn "  ✗ 全局配置导出失败"
        ((BACKUP_FAIL++)) || true
    fi

    # Profile 列表和详情
    local profiles
    profiles=$(incus profile list --format=csv -c n 2>/dev/null || true)
    if [[ -n "$profiles" ]]; then
        mkdir -p "${incus_dir}/profiles"
        while IFS= read -r profile; do
            if incus profile show "$profile" > "${incus_dir}/profiles/${profile}.yaml" 2>/dev/null; then
                log "  ✓ Profile: ${profile}"
                ((BACKUP_OK++)) || true
            else
                warn "  ✗ Profile: ${profile}"
                ((BACKUP_FAIL++)) || true
            fi
        done <<< "$profiles"
    fi

    # 网络配置
    local networks
    networks=$(incus network list --format=csv -c n 2>/dev/null || true)
    if [[ -n "$networks" ]]; then
        mkdir -p "${incus_dir}/networks"
        while IFS= read -r net; do
            if incus network show "$net" > "${incus_dir}/networks/${net}.yaml" 2>/dev/null; then
                log "  ✓ 网络: ${net}"
                ((BACKUP_OK++)) || true
            else
                warn "  ✗ 网络: ${net}"
                ((BACKUP_FAIL++)) || true
            fi
        done <<< "$networks"
    fi

    # 存储池
    local pools
    pools=$(incus storage list --format=csv -c n 2>/dev/null || true)
    if [[ -n "$pools" ]]; then
        mkdir -p "${incus_dir}/storage"
        while IFS= read -r pool; do
            if incus storage show "$pool" > "${incus_dir}/storage/${pool}.yaml" 2>/dev/null; then
                log "  ✓ 存储池: ${pool}"
                ((BACKUP_OK++)) || true
            else
                warn "  ✗ 存储池: ${pool}"
                ((BACKUP_FAIL++)) || true
            fi
        done <<< "$pools"
    fi

    # 集群成员列表
    if incus cluster list --format=yaml > "${incus_dir}/cluster-members.yaml" 2>/dev/null; then
        log "  ✓ 集群成员列表"
        ((BACKUP_OK++)) || true
    else
        warn "  ✗ 集群成员列表（可能非集群模式）"
        ((BACKUP_FAIL++)) || true
    fi

    # 实例列表（不含运行时状态，仅配置）
    if incus list --format=yaml > "${incus_dir}/instances-list.yaml" 2>/dev/null; then
        log "  ✓ 实例列表"
        ((BACKUP_OK++)) || true
    fi

    # Project 配置
    local projects
    projects=$(incus project list --format=csv -c n 2>/dev/null || true)
    if [[ -n "$projects" ]]; then
        mkdir -p "${incus_dir}/projects"
        while IFS= read -r proj; do
            if incus project show "$proj" > "${incus_dir}/projects/${proj}.yaml" 2>/dev/null; then
                log "  ✓ Project: ${proj}"
                ((BACKUP_OK++)) || true
            fi
        done <<< "$projects"
    fi

    # ACL 规则
    local acls
    acls=$(incus network acl list --format=csv -c n 2>/dev/null || true)
    if [[ -n "$acls" ]]; then
        mkdir -p "${incus_dir}/acls"
        while IFS= read -r acl; do
            if incus network acl show "$acl" > "${incus_dir}/acls/${acl}.yaml" 2>/dev/null; then
                log "  ✓ ACL: ${acl}"
                ((BACKUP_OK++)) || true
            fi
        done <<< "$acls"
    fi
}

# ==================== Ceph 配置备份 ====================
backup_ceph() {
    local ceph_dir="${BACKUP_DIR}/ceph"
    mkdir -p "$ceph_dir"

    if ! command -v ceph >/dev/null 2>&1; then
        warn "ceph 未安装，跳过 Ceph 备份"
        return
    fi

    log "导出 Ceph 配置..."

    # ceph.conf
    if [[ -f /etc/ceph/ceph.conf ]]; then
        cp /etc/ceph/ceph.conf "${ceph_dir}/ceph.conf"
        log "  ✓ ceph.conf"
        ((BACKUP_OK++)) || true
    else
        warn "  ✗ /etc/ceph/ceph.conf 不存在"
        ((BACKUP_FAIL++)) || true
    fi

    # 运行时配置 dump
    if ceph config dump > "${ceph_dir}/config-dump.txt" 2>/dev/null; then
        log "  ✓ 运行时配置 (config dump)"
        ((BACKUP_OK++)) || true
    else
        warn "  ✗ 运行时配置导出失败"
        ((BACKUP_FAIL++)) || true
    fi

    # CRUSH map（文本格式）
    if ceph osd crush dump > "${ceph_dir}/crush-dump.json" 2>/dev/null; then
        log "  ✓ CRUSH map"
        ((BACKUP_OK++)) || true
    else
        warn "  ✗ CRUSH map 导出失败"
        ((BACKUP_FAIL++)) || true
    fi

    # OSD tree
    if ceph osd tree > "${ceph_dir}/osd-tree.txt" 2>/dev/null; then
        log "  ✓ OSD tree"
        ((BACKUP_OK++)) || true
    fi

    # 集群状态快照
    if ceph status > "${ceph_dir}/cluster-status.txt" 2>/dev/null; then
        log "  ✓ 集群状态快照"
        ((BACKUP_OK++)) || true
    fi

    # 存储池配置
    local pools
    pools=$(ceph osd pool ls 2>/dev/null || true)
    if [[ -n "$pools" ]]; then
        mkdir -p "${ceph_dir}/pools"
        while IFS= read -r pool; do
            if ceph osd pool get "$pool" all > "${ceph_dir}/pools/${pool}.txt" 2>/dev/null; then
                log "  ✓ 存储池: ${pool}"
                ((BACKUP_OK++)) || true
            fi
        done <<< "$pools"
    fi

    # auth 导出（包含 keyring，注意安全）
    if ceph auth ls > "${ceph_dir}/auth-list.txt" 2>/dev/null; then
        chmod 600 "${ceph_dir}/auth-list.txt"
        log "  ✓ Auth 列表（已设置 600 权限）"
        ((BACKUP_OK++)) || true
    fi

    # Ceph keyring 文件
    for keyring in /etc/ceph/*.keyring; do
        if [[ -f "$keyring" ]]; then
            cp "$keyring" "${ceph_dir}/"
            chmod 600 "${ceph_dir}/$(basename "$keyring")"
            log "  ✓ $(basename "$keyring")"
            ((BACKUP_OK++)) || true
        fi
    done
}

# ==================== nftables 规则备份 ====================
backup_nftables() {
    local nft_dir="${BACKUP_DIR}/nftables"
    mkdir -p "$nft_dir"

    log "导出 nftables 规则..."

    if ! command -v nft >/dev/null 2>&1; then
        warn "nft 未安装，跳过 nftables 备份"
        return
    fi

    if nft list ruleset > "${nft_dir}/ruleset.nft" 2>/dev/null; then
        log "  ✓ 完整规则集"
        ((BACKUP_OK++)) || true
    else
        warn "  ✗ 规则集导出失败"
        ((BACKUP_FAIL++)) || true
    fi

    # 分表导出
    local tables
    tables=$(nft list tables 2>/dev/null || true)
    if [[ -n "$tables" ]]; then
        while IFS= read -r table_line; do
            # 格式: "table <family> <name>"
            local family name
            family=$(echo "$table_line" | awk '{print $2}')
            name=$(echo "$table_line" | awk '{print $3}')
            if [[ -n "$family" ]] && [[ -n "$name" ]]; then
                nft list table "$family" "$name" > "${nft_dir}/table_${family}_${name}.nft" 2>/dev/null || true
            fi
        done <<< "$tables"
    fi

    # 持久化配置文件
    if [[ -f /etc/nftables.conf ]]; then
        cp /etc/nftables.conf "${nft_dir}/nftables.conf"
        log "  ✓ /etc/nftables.conf"
        ((BACKUP_OK++)) || true
    fi
}

# ==================== netplan 配置备份 ====================
backup_netplan() {
    local net_dir="${BACKUP_DIR}/netplan"
    mkdir -p "$net_dir"

    log "导出 netplan 配置..."

    local found=false
    for f in /etc/netplan/*.yaml /etc/netplan/*.yml; do
        if [[ -f "$f" ]]; then
            cp "$f" "${net_dir}/"
            log "  ✓ $(basename "$f")"
            ((BACKUP_OK++)) || true
            found=true
        fi
    done

    if ! $found; then
        warn "  未找到 netplan 配置文件"
    fi

    # 当前网络状态快照
    ip addr show > "${net_dir}/ip-addr.txt" 2>/dev/null || true
    ip route show > "${net_dir}/ip-route.txt" 2>/dev/null || true
    bridge link show > "${net_dir}/bridge-link.txt" 2>/dev/null || true
}

# ==================== 执行备份 ====================
log "开始集群配置备份..."
echo ""

backup_incus
echo ""
backup_ceph
echo ""
backup_nftables
echo ""
backup_netplan

# ==================== 打包压缩 ====================
echo ""
if $COMPRESS; then
    ARCHIVE="${BACKUP_DIR}.tar.gz"
    log "打包压缩: ${ARCHIVE}"
    tar -czf "$ARCHIVE" -C "$(dirname "$BACKUP_DIR")" "$(basename "$BACKUP_DIR")"
    chmod 600 "$ARCHIVE"
    archive_size=$(du -sh "$ARCHIVE" 2>/dev/null | cut -f1)
    log "压缩包大小: ${archive_size}"

    # 保留原始目录，方便查看
    log "原始目录保留在: ${BACKUP_DIR}"
fi

# ==================== 备份摘要 ====================
echo ""
echo -e "${BOLD}═══════════════════════════════════════════════════${NC}"
echo -e "${BOLD}              备份完成摘要${NC}"
echo -e "${BOLD}═══════════════════════════════════════════════════${NC}"
echo -e "  主机: $(hostname)"
echo -e "  时间: $(date '+%Y-%m-%d %H:%M:%S')"
echo -e "  目录: ${BACKUP_DIR}"
$COMPRESS && echo -e "  压缩: ${ARCHIVE}"
echo -e "  成功: ${GREEN}${BACKUP_OK}${NC} 项"
[[ $BACKUP_FAIL -gt 0 ]] && echo -e "  失败: ${RED}${BACKUP_FAIL}${NC} 项"
echo -e "${BOLD}═══════════════════════════════════════════════════${NC}"

# 文件列表
log "备份文件列表:"
find "$BACKUP_DIR" -type f | sort | while IFS= read -r f; do
    echo "  $(du -sh "$f" 2>/dev/null | cut -f1)  ${f#"${BACKUP_DIR}"/}"
done

[[ $BACKUP_FAIL -gt 0 ]] && exit 1 || exit 0
