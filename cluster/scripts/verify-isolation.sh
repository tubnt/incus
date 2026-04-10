#!/usr/bin/env bash
# VM 隔离验证工具
# 检查 RFC1918 阻断、管理端口隔离、公网连通性、IP 过滤
set -euo pipefail

# ── 常量 ──────────────────────────────────────────────
# RFC1918 测试地址
RFC1918_ADDRS=(
    "10.0.0.1"
    "172.16.0.1"
    "192.168.1.1"
)

# 宿主机管理端口
MGMT_PORTS=(
    8443   # Incus API
    6443   # 可能的 K8s API
)

# 公网测试地址
PUBLIC_ADDRS=(
    "1.1.1.1"
    "8.8.8.8"
)

# 颜色
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
NC='\033[0m'

# ── 帮助 ──────────────────────────────────────────────
usage() {
    cat <<'EOF'
用法: verify-isolation.sh <VM名称> [选项]

参数:
  VM名称       要验证的 Incus 虚拟机名称

选项:
  --gateway <IP>   宿主机网关 IP（默认: 202.151.179.225）
  --verbose        显示详细输出
  --help           显示此帮助信息

验证项目:
  1. RFC1918 地址阻断 — VM 无法访问内网地址
  2. 管理端口隔离 — VM 无法访问宿主机管理端口
  3. 公网连通性 — VM 可正常访问公网
  4. IP 过滤 — VM 无法伪造源 IP
  5. 安全配置检查 — 确认三重过滤已启用

示例:
  verify-isolation.sh my-vm
  verify-isolation.sh my-vm --gateway 202.151.179.225 --verbose
EOF
    exit 0
}

# ── 日志 ──────────────────────────────────────────────
log()      { echo -e "[$(date '+%H:%M:%S')] $*"; }
pass()     { echo -e "  ${GREEN}[通过]${NC} $*"; PASS_COUNT=$((PASS_COUNT + 1)); }
fail()     { echo -e "  ${RED}[失败]${NC} $*"; FAIL_COUNT=$((FAIL_COUNT + 1)); }
warn()     { echo -e "  ${YELLOW}[警告]${NC} $*"; WARN_COUNT=$((WARN_COUNT + 1)); }
skip()     { echo -e "  ${YELLOW}[跳过]${NC} $*"; }

PASS_COUNT=0
FAIL_COUNT=0
WARN_COUNT=0

# ── 参数解析 ──────────────────────────────────────────
[[ "${1:-}" == "--help" ]] && usage
[[ $# -lt 1 ]] && { echo "错误: 参数不足" >&2; usage; }

VM_NAME="$1"
shift

GATEWAY_IP="202.151.179.225"
VERBOSE=false

while [[ $# -gt 0 ]]; do
    case "$1" in
        --gateway) GATEWAY_IP="$2"; shift 2 ;;
        --verbose) VERBOSE=true;    shift ;;
        --help)    usage ;;
        *)         echo "错误: 未知参数: $1" >&2; exit 1 ;;
    esac
done

# 校验网关 IP 格式（防止命令注入 + 无效地址）
if ! [[ "$GATEWAY_IP" =~ ^([0-9]{1,3})\.([0-9]{1,3})\.([0-9]{1,3})\.([0-9]{1,3})$ ]]; then
    echo "错误: 网关 IP 格式无效: ${GATEWAY_IP}" >&2
    exit 1
fi
for _i in 1 2 3 4; do
    if (( BASH_REMATCH[_i] > 255 )); then
        echo "错误: 网关 IP octet 超出范围: ${GATEWAY_IP}" >&2
        exit 1
    fi
done

# 检查 VM 是否存在且运行中
if ! incus info "$VM_NAME" &>/dev/null; then
    echo "错误: 虚拟机 '${VM_NAME}' 不存在" >&2
    exit 1
fi

VM_STATUS=$(incus list "$VM_NAME" -f csv -c s 2>/dev/null | head -1)
if [[ "$VM_STATUS" != "RUNNING" ]]; then
    echo "错误: 虚拟机 '${VM_NAME}' 未运行（当前状态: ${VM_STATUS}）" >&2
    exit 1
fi

# 辅助函数：在 VM 内执行命令
vm_exec() {
    incus exec "$VM_NAME" -- bash -c "$1" 2>/dev/null
}

# ── 测试 1：RFC1918 地址阻断 ──────────────────────────
log "测试 1: RFC1918 地址阻断"
for addr in "${RFC1918_ADDRS[@]}"; do
    if vm_exec "ping -c 1 -W 2 ${addr}" &>/dev/null; then
        fail "可以 ping 通 RFC1918 地址 ${addr}（应被阻断）"
    else
        pass "RFC1918 地址 ${addr} 已阻断"
    fi
done

# ── 测试 2：管理端口隔离 ──────────────────────────────
log "测试 2: 宿主机管理端口隔离"
for port in "${MGMT_PORTS[@]}"; do
    if vm_exec "timeout 2 bash -c '</dev/tcp/${GATEWAY_IP}/${port}'" &>/dev/null; then
        fail "可以访问宿主机管理端口 ${GATEWAY_IP}:${port}（应被阻断）"
    else
        pass "管理端口 ${GATEWAY_IP}:${port} 已隔离"
    fi
done

# ── 测试 3：公网连通性 ──────────────────────────────
log "测试 3: 公网连通性"
for addr in "${PUBLIC_ADDRS[@]}"; do
    if vm_exec "ping -c 2 -W 3 ${addr}" &>/dev/null; then
        pass "公网 ${addr} 可达"
    else
        fail "公网 ${addr} 不可达（应可正常访问）"
    fi
done

# DNS 解析测试
if vm_exec "host -W 3 example.com" &>/dev/null; then
    pass "DNS 解析正常"
else
    warn "DNS 解析失败，可能影响正常使用"
fi

# ── 测试 4：IP 过滤验证 ──────────────────────────────
log "测试 4: IP 过滤（源 IP 伪造防护）"

# 获取 VM 的已绑定 IP
VM_IP=$(incus config device show "$VM_NAME" | grep "ipv4.address:" | awk '{print $2}' || true)
if [[ -z "$VM_IP" ]]; then
    skip "无法获取 VM 绑定 IP，跳过 IP 过滤测试"
else
    # 尝试用伪造源 IP 发送数据包（需要 VM 内有 nmap 或 hping3）
    FAKE_IP="202.151.179.231"  # 范围外的 IP
    if vm_exec "command -v nmap" &>/dev/null; then
        if vm_exec "nmap -e enp5s0 -S ${FAKE_IP} -Pn --max-retries 0 -p 80 1.1.1.1" &>/dev/null; then
            # 检查是否收到回复（如果过滤生效，伪造包应被丢弃）
            warn "IP 伪造测试结果需人工确认（nmap 已执行但无法自动判定丢弃）"
        else
            pass "伪造源 IP 数据包发送失败（过滤可能生效）"
        fi
    else
        # 使用 ping 的 -I 选项尝试绑定不同源地址
        if vm_exec "ping -c 1 -W 2 -I ${FAKE_IP} 1.1.1.1" &>/dev/null; then
            fail "可以使用伪造 IP ${FAKE_IP} 发送数据包（IP 过滤未生效）"
        else
            pass "伪造源 IP ${FAKE_IP} 被阻止"
        fi
    fi
fi

# ── 测试 5：安全配置检查 ──────────────────────────────
log "测试 5: Incus 安全配置检查"
DEVICE_CONFIG=$(incus config device show "$VM_NAME" 2>/dev/null || true)

check_config() {
    local key="$1"
    local desc="$2"
    if echo "$DEVICE_CONFIG" | grep -q "${key}: \"true\""; then
        pass "${desc} 已启用"
    else
        fail "${desc} 未启用"
    fi
}

check_config "security.ipv4_filtering" "IPv4 过滤"
check_config "security.mac_filtering"  "MAC 过滤"
check_config "security.port_isolation" "端口隔离"

# ── 结果汇总 ──────────────────────────────────────────
echo ""
log "=========================================="
log "验证完成: ${VM_NAME}"
echo -e "  ${GREEN}通过: ${PASS_COUNT}${NC}"
echo -e "  ${RED}失败: ${FAIL_COUNT}${NC}"
echo -e "  ${YELLOW}警告: ${WARN_COUNT}${NC}"
log "=========================================="

if [[ $FAIL_COUNT -gt 0 ]]; then
    echo ""
    log "${RED}存在 ${FAIL_COUNT} 项失败，VM 隔离可能不完整！${NC}"
    exit 1
fi

if [[ $WARN_COUNT -gt 0 ]]; then
    log "${YELLOW}有 ${WARN_COUNT} 项警告，建议人工复查${NC}"
fi

exit 0
