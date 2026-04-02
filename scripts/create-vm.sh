#!/bin/bash
# ============================================================
# Incus VM 创建脚本
# 用途：一键创建带公网 IP 的虚拟机
# 前提：已运行 setup-env.sh 完成环境初始化
# ============================================================
set -euo pipefail

# ==================== 默认配置 ====================
# 自动从网桥检测，也可手动覆盖
BRIDGE_NAME="br-pub"
GATEWAY=""                     # 留空自动检测
SUBNET_MASK=""                 # 留空自动检测
DNS_SERVERS="8.8.8.8,1.1.1.1"
PROFILE_NAME="vm-public"
SWAP_SIZE="8G"
CREDENTIAL_FILE="/root/.vm-credentials"

# 自动检测网关和子网掩码
auto_detect_network() {
    if [ -z "${GATEWAY}" ]; then
        GATEWAY=$(ip route show default | awk '{print $3}' | head -1)
        [ -z "${GATEWAY}" ] && err "无法检测网关，请在脚本中手动设置 GATEWAY"
    fi
    if [ -z "${SUBNET_MASK}" ]; then
        local cidr
        cidr=$(ip -4 addr show "${BRIDGE_NAME}" 2>/dev/null | awk '/inet /{print $2}' | head -1)
        if [ -z "${cidr}" ]; then
            # 如果网桥没有 IP，尝试从默认路由接口检测
            local iface
            iface=$(ip route show default | awk '{print $5}' | head -1)
            cidr=$(ip -4 addr show "${iface}" | awk '/inet /{print $2}' | head -1)
        fi
        SUBNET_MASK="/${cidr##*/}"
        [ "${SUBNET_MASK}" = "/" ] && err "无法检测子网掩码，请手动设置 SUBNET_MASK"
    fi
}
auto_detect_network

# ==================== 支持的镜像 ====================
# Linux cloud 镜像（支持 cloud-init 自动配置）
declare -A IMAGES=(
    ["ubuntu2404"]="images:ubuntu/24.04/cloud"
    ["ubuntu2204"]="images:ubuntu/22.04/cloud"
    ["debian12"]="images:debian/12/cloud"
    ["debian11"]="images:debian/11/cloud"
    ["centos9"]="images:centos/9-Stream/cloud"
    ["centos10"]="images:centos/10-Stream/cloud"
    ["rocky9"]="images:rockylinux/9/cloud"
    ["rocky10"]="images:rockylinux/10/cloud"
    ["alma9"]="images:almalinux/9/cloud"
    ["alma10"]="images:almalinux/10/cloud"
    ["fedora42"]="images:fedora/42/cloud"
    ["arch"]="images:archlinux/cloud"
)

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

log()  { echo -e "${GREEN}[INFO]${NC} $*"; }
warn() { echo -e "${YELLOW}[WARN]${NC} $*"; }
err()  { echo -e "${RED}[ERR]${NC} $*"; exit 1; }

# ==================== 帮助信息 ====================
usage() {
    cat << EOF
${CYAN}用法:${NC}
  $0 <虚拟机名> <公网IP> [镜像名]

${CYAN}参数:${NC}
  虚拟机名     例: vm-node03
  公网IP       例: 43.239.84.23
  镜像名       可选，默认 ubuntu2404

${CYAN}可用 Linux 镜像:${NC}
$(for key in $(echo "${!IMAGES[@]}" | tr ' ' '\n' | sort); do
    printf "  %-14s %s\n" "$key" "${IMAGES[$key]}"
done)

${CYAN}Windows 安装:${NC}
  $0 <虚拟机名> <公网IP> windows
  注: Windows 需手动通过 SPICE/VNC 安装，脚本会创建空 VM 并挂载 ISO

${CYAN}示例:${NC}
  $0 vm-node03 43.239.84.23                    # Ubuntu 24.04
  $0 vm-node04 43.239.84.24 debian12           # Debian 12
  $0 vm-node05 43.239.84.25 rocky9             # Rocky Linux 9
  $0 vm-web    43.239.84.26 ubuntu2204         # Ubuntu 22.04
  $0 vm-win01  43.239.84.27 windows            # Windows (手动安装)

EOF
    exit 0
}

# ==================== 参数解析 ====================
[ $# -lt 2 ] && usage
VM_NAME="$1"
VM_IP="$2"
IMAGE_KEY="${3:-ubuntu2404}"
WIN_ISO="${4:-}"

[ "$(id -u)" -ne 0 ] && err "请使用 root 执行此脚本"
command -v incus >/dev/null || err "Incus 未安装"

# 检查 VM 是否已存在
if incus info "${VM_NAME}" >/dev/null 2>&1; then
    err "虚拟机 ${VM_NAME} 已存在"
fi

# 检查 IP 格式
if ! echo "${VM_IP}" | grep -qP '^\d+\.\d+\.\d+\.\d+$'; then
    err "IP 格式错误: ${VM_IP}"
fi

# ==================== Windows VM ====================
create_windows_vm() {
    log "创建 Windows VM: ${VM_NAME} (${VM_IP})"

    # 创建空 VM
    incus init "${VM_NAME}" --empty --vm --profile "${PROFILE_NAME}" \
        -c security.secureboot=false \
        -c image.os=Windows

    # 绑定 IP + 安全过滤
    incus config device override "${VM_NAME}" eth0 \
        ipv4.address="${VM_IP}" \
        security.ipv4_filtering=true

    # 下载 virtio 驱动（如果不存在）
    local VIRTIO_ISO="/root/virtio-win.iso"
    if [ ! -f "${VIRTIO_ISO}" ]; then
        log "下载 virtio 驱动 ISO..."
        wget -q --show-progress -O "${VIRTIO_ISO}" \
            "https://fedorapeople.org/groups/virt/virtio-win/direct-downloads/stable-virtio/virtio-win.iso" \
            || warn "下载失败，请手动下载到 ${VIRTIO_ISO}"
    fi

    # 导入 Windows ISO（如果提供了路径）
    if [ -n "${WIN_ISO}" ] && [ -f "${WIN_ISO}" ]; then
        log "导入 Windows ISO..."
        incus storage volume import default "${WIN_ISO}" "${VM_NAME}-win-iso" --type=iso 2>/dev/null || true
        incus config device add "${VM_NAME}" install disk \
            pool=default source="${VM_NAME}-win-iso" boot.priority=10
    fi

    # 注册到宿主机防火墙
    if command -v vm-firewall >/dev/null 2>&1; then
        vm-firewall add-vm "${VM_IP}" 2>/dev/null || true
        vm-firewall save 2>/dev/null || true
    fi

    # 导入 virtio 驱动 ISO
    if [ -f "${VIRTIO_ISO}" ]; then
        log "导入 virtio 驱动 ISO..."
        incus storage volume import default "${VIRTIO_ISO}" "${VM_NAME}-virtio-iso" --type=iso 2>/dev/null || true
        incus config device add "${VM_NAME}" virtio disk \
            pool=default source="${VM_NAME}-virtio-iso" 2>/dev/null || true
    fi

    echo ""
    log "=========================================="
    log " Windows VM 已创建: ${VM_NAME}"
    log "=========================================="
    echo ""

    if [ -n "${WIN_ISO}" ] && [ -f "${WIN_ISO}" ]; then
        echo -e " ${CYAN}ISO 已挂载，可直接启动:${NC}"
        echo "   incus start ${VM_NAME}"
        echo "   incus console ${VM_NAME} --type=vga"
    else
        echo -e " ${YELLOW}还需要挂载 Windows ISO:${NC}"
        echo "   incus storage volume import default /path/to/windows.iso ${VM_NAME}-win-iso --type=iso"
        echo "   incus config device add ${VM_NAME} install disk pool=default source=${VM_NAME}-win-iso boot.priority=10"
        echo "   incus start ${VM_NAME}"
        echo "   incus console ${VM_NAME} --type=vga"
    fi
    echo ""
    echo -e " ${YELLOW}安装要点:${NC}"
    echo "   1. 磁盘选择界面 → 加载驱动 → 浏览 virtio CD → viostor/w11/amd64"
    echo "   2. 安装完成后在 Windows 内配置静态 IP:"
    echo "      IP: ${VM_IP}  掩码: 255.255.255.192  网关: ${GATEWAY}  DNS: ${DNS_SERVERS}"
    echo "   3. 安装 virtio CD 中全部驱动 (NetKVM, Balloon, vioserial)"
    echo "   4. 卸载 ISO:"
    echo "      incus config device remove ${VM_NAME} install"
    echo "      incus config device remove ${VM_NAME} virtio"
    echo ""
}

# ==================== Linux VM ====================
create_linux_vm() {
    local image="${IMAGES[$IMAGE_KEY]:-}"
    [ -z "$image" ] && err "未知镜像: ${IMAGE_KEY}，运行 $0 查看可用镜像"

    log "创建 Linux VM: ${VM_NAME} (${VM_IP})"
    log "镜像: ${image}"

    # 生成密码
    VM_PASS=$(openssl rand -base64 24)
    VM_HASH=$(echo "${VM_PASS}" | openssl passwd -6 -stdin)

    # 创建实例
    log "1/5 创建实例..."
    incus init "${image}" "${VM_NAME}" --vm --profile "${PROFILE_NAME}"

    # 绑定 IP + 安全过滤
    log "2/5 绑定 IP 和安全过滤..."
    incus config device override "${VM_NAME}" eth0 \
        ipv4.address="${VM_IP}" \
        security.ipv4_filtering=true

    # 配置网络
    log "3/5 配置 cloud-init 网络..."
    cat << NETEOF | incus config set "${VM_NAME}" cloud-init.network-config -
network:
  version: 2
  ethernets:
    all-en:
      match:
        name: "en*"
      dhcp4: false
      dhcp6: false
      addresses:
        - ${VM_IP}${SUBNET_MASK}
      routes:
        - to: default
          via: ${GATEWAY}
      nameservers:
        addresses: [${DNS_SERVERS}]
NETEOF

    # 配置用户
    log "4/5 配置 cloud-init 用户..."
    SSH_PUBKEY=$(cat /root/.ssh/id_ed25519.pub 2>/dev/null || echo "")

    # 根据发行版选择包和防火墙
    local ssh_svc="ssh"
    local pkg_list="curl,wget,vim,htop,ufw,openssh-server"
    local fw_type="ufw"  # ufw 或 firewalld

    case "${IMAGE_KEY}" in
        centos*|rocky*|alma*|fedora*)
            ssh_svc="sshd"
            pkg_list="curl,wget,vim-enhanced,htop,openssh-server,firewalld"
            fw_type="firewalld"
            ;;
        arch*)
            pkg_list="curl,wget,vim,htop,ufw,openssh"
            ;;
    esac

    cat << USEREOF | incus config set "${VM_NAME}" cloud-init.user-data -
#cloud-config
package_update: true
packages: [${pkg_list}]

swap:
  filename: /swapfile
  size: ${SWAP_SIZE}
  maxsize: ${SWAP_SIZE}

users:
  - name: ubuntu
    groups: sudo,wheel
    shell: /bin/bash
    sudo: ALL=(ALL) NOPASSWD:ALL
    lock_passwd: false
    ssh_authorized_keys:
      - ${SSH_PUBKEY}

chpasswd:
  expire: false
  users:
    - name: ubuntu
      password: "${VM_HASH}"
      type: hash
    - name: root
      password: "${VM_HASH}"
      type: hash

ssh_pwauth: true
disable_root: false

runcmd:
  - systemctl enable --now ${ssh_svc}
  - sed -i 's/^#\?PasswordAuthentication.*/PasswordAuthentication yes/' /etc/ssh/sshd_config
  - sed -i 's/^#\?PermitRootLogin.*/PermitRootLogin yes/' /etc/ssh/sshd_config
  - mkdir -p /root/.ssh && chmod 700 /root/.ssh
  - echo '${SSH_PUBKEY}' >> /root/.ssh/authorized_keys && chmod 600 /root/.ssh/authorized_keys
  - systemctl restart ${ssh_svc}
$(if [ "${fw_type}" = "ufw" ]; then
cat << 'UFWBLOCK'
  - ufw default deny incoming
  - ufw default allow outgoing
  - ufw allow 22/tcp
  - ufw --force enable
UFWBLOCK
else
cat << 'FWDBLOCK'
  - systemctl enable --now firewalld
  - firewall-cmd --permanent --add-service=ssh
  - firewall-cmd --permanent --set-default-zone=public
  - firewall-cmd --reload
FWDBLOCK
fi)
USEREOF

    # 注册到宿主机防火墙
    if command -v vm-firewall >/dev/null 2>&1; then
        vm-firewall add-vm "${VM_IP}" 2>/dev/null || true
        vm-firewall save 2>/dev/null || true
        log "   已注册到宿主机防火墙"
    fi

    # 启动
    log "5/5 启动虚拟机..."
    incus start "${VM_NAME}"

    # 保存凭据
    echo "${VM_NAME}: user=ubuntu password=${VM_PASS} ip=${VM_IP}" >> "${CREDENTIAL_FILE}"
    chmod 600 "${CREDENTIAL_FILE}"

    echo ""
    log "=========================================="
    log " VM 创建成功！"
    log "=========================================="
    echo ""
    echo -e " 名称:   ${CYAN}${VM_NAME}${NC}"
    echo -e " IP:     ${CYAN}${VM_IP}${NC}"
    echo -e " 镜像:   ${image}"
    echo -e " 用户名: ${CYAN}ubuntu${NC} / ${CYAN}root${NC}"
    echo -e " 密码:   ${CYAN}${VM_PASS}${NC}"
    echo ""
    echo -e " ${YELLOW}连接方式:${NC}"
    echo "   ssh ubuntu@${VM_IP}"
    echo "   ssh root@${VM_IP}"
    echo ""
    echo -e " ${YELLOW}注意: cloud-init 首次启动约需 1-3 分钟完成配置${NC}"
    echo -e " 检查状态: incus exec ${VM_NAME} -- cloud-init status"
    echo ""
}

# ==================== 主流程 ====================
if [ "${IMAGE_KEY}" = "windows" ]; then
    create_windows_vm
else
    create_linux_vm
fi
