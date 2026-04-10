#!/usr/bin/env bash
# VM 创建辅助脚本
# 封装完整的 VM 创建流程：创建 → cloud-init → IP绑定 → 安全过滤 → 启动
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

# ── 常量 ──────────────────────────────────────────────
IP_PREFIX="202.151.179"
IP_MIN=232
IP_MAX=254
SUBNET_MASK=27
GATEWAY="${IP_PREFIX}.225"
DNS1="1.1.1.1"
DNS2="8.8.8.8"
STORAGE_POOL="ceph"
DEFAULT_DISK="20GiB"
DEFAULT_CPU=2
DEFAULT_MEM="2GiB"
# ── 帮助 ──────────────────────────────────────────────
usage() {
    cat <<'EOF'
用法: create-vm-helper.sh <VM名称> <IP地址> [选项]

参数:
  VM名称       虚拟机名称
  IP地址       公网 IP（范围 202.151.179.232-254）

选项:
  --target <节点>       指定目标节点（不指定则自动选择负载最低的节点）
  --image <镜像>        VM 镜像（默认: images:ubuntu/24.04）
  --cpu <数量>          CPU 核数（默认: 2）
  --memory <大小>       内存大小（默认: 2GiB）
  --disk <大小>         磁盘大小（默认: 20GiB）
  --bandwidth <限速>    带宽限速，如 100Mbit
  --ssh-key <路径>      SSH 公钥文件路径（默认: ~/.ssh/id_ed25519.pub）
  --password-file <路径> 包含 root 密码的文件（不指定则随机生成）
                        也可通过 VM_PASSWORD 环境变量传入
  --no-start            创建后不自动启动
  --help                显示此帮助信息

示例:
  create-vm-helper.sh web01 202.151.179.232
  create-vm-helper.sh db01 202.151.179.233 --target node2 --cpu 4 --memory 8GiB --disk 50GiB
  create-vm-helper.sh app01 202.151.179.234 --bandwidth 100Mbit --ssh-key ~/.ssh/prod.pub
EOF
    exit 0
}

# ── 日志 ──────────────────────────────────────────────
log()  { echo "[$(date '+%H:%M:%S')] $*"; }
err()  { echo "[$(date '+%H:%M:%S')] 错误: $*" >&2; }
die()  { err "$@"; exit 1; }

# ── 参数解析 ──────────────────────────────────────────
[[ "${1:-}" == "--help" ]] && usage
[[ $# -lt 2 ]] && { err "参数不足"; usage; }

VM_NAME="$1"
IP_ADDR="$2"
shift 2

TARGET=""
IMAGE="images:ubuntu/24.04"
CPU="$DEFAULT_CPU"
MEMORY="$DEFAULT_MEM"
DISK="$DEFAULT_DISK"
BANDWIDTH=""
SSH_KEY_FILE="${HOME}/.ssh/id_ed25519.pub"
PASSWORD=""
PASSWORD_FILE=""
AUTO_START=true

while [[ $# -gt 0 ]]; do
    case "$1" in
        --target)    TARGET="$2";       shift 2 ;;
        --image)     IMAGE="$2";        shift 2 ;;
        --cpu)       CPU="$2";          shift 2 ;;
        --memory)    MEMORY="$2";       shift 2 ;;
        --disk)      DISK="$2";         shift 2 ;;
        --bandwidth) BANDWIDTH="$2";    shift 2 ;;
        --ssh-key)        SSH_KEY_FILE="$2";  shift 2 ;;
        --password-file)  PASSWORD_FILE="$2"; shift 2 ;;
        --no-start)       AUTO_START=false;   shift ;;
        --help)      usage ;;
        *)           die "未知参数: $1" ;;
    esac
done

# ── 校验 ──────────────────────────────────────────────
validate_ip() {
    local ip="$1"
    if ! [[ "$ip" =~ ^${IP_PREFIX}\.([0-9]+)$ ]]; then
        die "IP 地址 ${ip} 不在允许的子网 ${IP_PREFIX}.0/${SUBNET_MASK} 内"
    fi
    local last_octet="${BASH_REMATCH[1]}"
    if (( last_octet < IP_MIN || last_octet > IP_MAX )); then
        die "IP 地址最后一段 ${last_octet} 不在允许范围 ${IP_MIN}-${IP_MAX} 内"
    fi
}

validate_ip "$IP_ADDR"

# 校验 VM 名称（仅允许小写字母、数字、连字符，禁止特殊字符破坏 YAML/命令）
if ! [[ "$VM_NAME" =~ ^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$ ]]; then
    die "VM 名称不合法（仅允许小写字母、数字和连字符，长度 1-63）: ${VM_NAME}"
fi

# 检查 VM 是否已存在
if incus info "$VM_NAME" &>/dev/null; then
    die "虚拟机 '${VM_NAME}' 已存在"
fi

# 检查 SSH 公钥
if [[ ! -f "$SSH_KEY_FILE" ]]; then
    die "SSH 公钥文件不存在: ${SSH_KEY_FILE}"
fi
SSH_PUBKEY=$(cat "$SSH_KEY_FILE")

# ── 步骤 1：选择目标节点 ──────────────────────────────
if [[ -z "$TARGET" ]]; then
    log "自动选择负载最低的节点..."
    # 按已运行 VM 数量选择最少的节点
    TARGET=$(incus cluster list -f csv -c n | while read -r node; do
        count=$(incus list --target "$node" -f csv -c n 2>/dev/null | wc -l)
        echo "${count} ${node}"
    done | sort -n | head -1 | awk '{print $2}')

    if [[ -z "$TARGET" ]]; then
        die "无法自动选择节点，请使用 --target 手动指定"
    fi
    log "选定节点: ${TARGET}"
else
    # 验证指定节点存在
    if ! incus cluster list -f csv -c n | grep -qx "$TARGET"; then
        die "节点 '${TARGET}' 不存在于集群中"
    fi
    log "使用指定节点: ${TARGET}"
fi

# ── 步骤 2：生成 cloud-init 配置 ──────────────────────
log "生成 cloud-init 配置..."

# 读取密码：优先 --password-file，其次 VM_PASSWORD 环境变量，最后随机生成
if [[ -n "$PASSWORD_FILE" ]]; then
    [[ -f "$PASSWORD_FILE" ]] || die "密码文件不存在: ${PASSWORD_FILE}"
    PASSWORD=$(<"$PASSWORD_FILE")
    [[ -n "$PASSWORD" ]] || die "密码文件为空: ${PASSWORD_FILE}"
elif [[ -n "${VM_PASSWORD:-}" ]]; then
    PASSWORD="$VM_PASSWORD"
else
    PASSWORD=$(openssl rand -base64 16)
    CRED_FILE="${HOME}/.incus-vm-creds/${VM_NAME}.cred"
    mkdir -p "${HOME}/.incus-vm-creds"
    chmod 700 "${HOME}/.incus-vm-creds"
    install -m 600 /dev/null "$CRED_FILE"
    echo "$PASSWORD" > "$CRED_FILE"
    log "已生成随机 root 密码，保存至: ${CRED_FILE}（权限 600）"
fi
PASSWORD_HASH=$(openssl passwd -6 -stdin <<< "$PASSWORD")
unset PASSWORD

# 网络配置
NETWORK_CONFIG=$(cat <<NETEOF
network:
  version: 2
  ethernets:
    enp5s0:
      addresses: [${IP_ADDR}/${SUBNET_MASK}]
      routes:
        - to: default
          via: ${GATEWAY}
      nameservers:
        addresses: [${DNS1}, ${DNS2}]
NETEOF
)

# 用户数据
USER_DATA=$(cat <<UDEOF
#cloud-config
hostname: ${VM_NAME}
manage_etc_hosts: true

users:
  - name: root
    lock_passwd: false
    hashed_passwd: ${PASSWORD_HASH}
    ssh_authorized_keys:
      - ${SSH_PUBKEY}

ssh_pwauth: false

package_update: true
packages:
  - qemu-guest-agent

runcmd:
  - systemctl enable --now qemu-guest-agent
UDEOF
)

# ── 步骤 3：创建 VM ──────────────────────────────────
log "创建虚拟机: ${VM_NAME} (节点: ${TARGET}, 镜像: ${IMAGE})..."
incus init "$IMAGE" "$VM_NAME" --vm \
    --target "$TARGET" \
    --storage "$STORAGE_POOL" \
    -c limits.cpu="$CPU" \
    -c limits.memory="$MEMORY" \
    -d root,size="$DISK"

# ── 步骤 4：注入 cloud-init ──────────────────────────
log "注入 cloud-init 配置..."
incus config set "$VM_NAME" cloud-init.user-data "$USER_DATA"
incus config set "$VM_NAME" cloud-init.network-config "$NETWORK_CONFIG"

# ── 步骤 5：绑定 IP + 安全过滤 ────────────────────────
log "绑定 IP 并启用安全过滤..."
BW_ARGS=()
if [[ -n "$BANDWIDTH" ]]; then
    BW_ARGS=(--bandwidth "$BANDWIDTH")
fi
bash "${SCRIPT_DIR}/bind-vm-ip.sh" "$VM_NAME" "$IP_ADDR" "${BW_ARGS[@]}"

# ── 步骤 6：启动 VM ──────────────────────────────────
if $AUTO_START; then
    log "启动虚拟机..."
    incus start "$VM_NAME"

    log "等待 VM 就绪..."
    for i in $(seq 1 30); do
        if incus exec "$VM_NAME" -- true &>/dev/null 2>&1; then
            log "VM 已就绪"
            break
        fi
        if [[ $i -eq 30 ]]; then
            err "警告: VM 启动超时（30秒），请手动检查"
        fi
        sleep 1
    done
else
    log "跳过启动（--no-start）"
fi

# ── 完成 ──────────────────────────────────────────────
log "=========================================="
log "VM 创建完成！"
log "  名称:   ${VM_NAME}"
log "  节点:   ${TARGET}"
log "  IP:     ${IP_ADDR}"
log "  CPU:    ${CPU}"
log "  内存:   ${MEMORY}"
log "  磁盘:   ${DISK}"
[[ -n "$BANDWIDTH" ]] && log "  带宽:   ${BANDWIDTH}"
log "  SSH:    ssh root@${IP_ADDR}"
log "=========================================="
