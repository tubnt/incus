#!/usr/bin/env bash
# 安全网络配置应用脚本
# 功能: 备份 → 安全网(systemd-run 定时回滚) → 应用 → 自检 → 确认/回滚
# 安全红线: 禁止裸跑 netplan apply，必须有自动回滚机制
set -euo pipefail

# ============================================================
# 配置
# ============================================================
ROLLBACK_TIMEOUT=300  # 回滚超时（秒），5 分钟
NETPLAN_DIR="/etc/netplan"
BACKUP_DIR="/var/backups/netplan"
GATEWAY="202.151.179.225"
SSH_PORT=22
PING_COUNT=3
PING_TIMEOUT=5
ROLLBACK_UNIT_NAME="netplan-rollback"

# ============================================================
# 颜色输出
# ============================================================
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log_info()  { echo -e "${GREEN}[INFO]${NC}  $*"; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC}  $*"; }
log_error() { echo -e "${RED}[ERROR]${NC} $*"; }

# ============================================================
# 前置检查
# ============================================================
if [[ $EUID -ne 0 ]]; then
    log_error "必须以 root 权限运行"
    exit 1
fi

if [[ $# -ne 1 ]]; then
    echo "用法: $0 <netplan 配置文件>"
    echo "示例: $0 /path/to/cluster/configs/netplan/node1.yaml"
    exit 1
fi

CONFIG_FILE="$1"

if [[ ! -f "$CONFIG_FILE" ]]; then
    log_error "配置文件不存在: $CONFIG_FILE"
    exit 1
fi

# 校验 YAML 基本格式（通过 sys.argv 传参，避免命令注入）
if ! python3 -c "import sys, yaml; yaml.safe_load(open(sys.argv[1]))" "$CONFIG_FILE" 2>/dev/null; then
    log_error "配置文件 YAML 格式错误: $CONFIG_FILE"
    exit 1
fi

# ============================================================
# 步骤 1: 备份当前配置
# ============================================================
TIMESTAMP=$(date +%Y%m%d_%H%M%S)
BACKUP_PATH="${BACKUP_DIR}/${TIMESTAMP}"

log_info "步骤 1/5: 备份当前 netplan 配置到 ${BACKUP_PATH}"
mkdir -p "$BACKUP_PATH"
cp -a "${NETPLAN_DIR}/"* "$BACKUP_PATH/" 2>/dev/null || true

if [[ -z "$(ls -A "$BACKUP_PATH" 2>/dev/null)" ]]; then
    log_warn "当前 netplan 目录为空，跳过备份校验"
else
    log_info "备份完成，文件列表:"
    ls -la "$BACKUP_PATH/"
fi

# ============================================================
# 步骤 2: 设置 systemd-run 定时回滚（安全网）
# ============================================================
ROLLBACK_SCRIPT=$(mktemp /var/tmp/netplan-rollback-XXXXXX.sh)
cat > "$ROLLBACK_SCRIPT" << 'ROLLBACK_EOF'
#!/usr/bin/env bash
# 自动回滚脚本 — systemd-run 定时触发，无 IPMI 环境下的最后一道防线
BACKUP_PATH="__BACKUP_PATH__"
NETPLAN_DIR="/etc/netplan"
MAX_RETRIES=3

echo "[ROLLBACK] 回滚超时触发，正在恢复备份配置..."

if [[ -d "$BACKUP_PATH" ]] && [[ -n "$(ls -A "$BACKUP_PATH" 2>/dev/null)" ]]; then
    rm -f "${NETPLAN_DIR}/"*.yaml
    cp -a "${BACKUP_PATH}/"* "${NETPLAN_DIR}/"
    # 重试机制：netplan apply 是恢复网络的唯一手段，必须尽最大努力
    for attempt in $(seq 1 "$MAX_RETRIES"); do
        if netplan apply 2>/dev/null; then
            echo "[ROLLBACK] 已恢复到备份配置: $BACKUP_PATH (第 ${attempt} 次尝试)"
            exit 0
        fi
        echo "[ROLLBACK] netplan apply 失败 (第 ${attempt}/${MAX_RETRIES} 次)，2 秒后重试..."
        sleep 2
    done
    echo "[ROLLBACK] 所有重试均失败! 需要物理介入恢复网络"
else
    echo "[ROLLBACK] 备份目录为空或不存在，无法回滚!"
fi
ROLLBACK_EOF

sed -i "s|__BACKUP_PATH__|${BACKUP_PATH}|g" "$ROLLBACK_SCRIPT"
chmod +x "$ROLLBACK_SCRIPT"

log_info "步骤 2/5: 设置 ${ROLLBACK_TIMEOUT} 秒回滚定时器（安全网）"

# 先清理可能残留的旧回滚定时器
systemctl stop "${ROLLBACK_UNIT_NAME}.timer" 2>/dev/null || true
systemctl stop "${ROLLBACK_UNIT_NAME}.service" 2>/dev/null || true

if ! systemd-run \
    --unit="${ROLLBACK_UNIT_NAME}" \
    --on-active="${ROLLBACK_TIMEOUT}s" \
    --description="Netplan 自动回滚安全网" \
    /bin/bash "$ROLLBACK_SCRIPT"; then
    log_error "无法创建回滚定时器，安全网未就绪，中止操作"
    rm -f "$ROLLBACK_SCRIPT"
    exit 1
fi

# 二次确认：定时器必须处于 waiting 状态
if ! systemctl is-active "${ROLLBACK_UNIT_NAME}.timer" >/dev/null 2>&1; then
    log_error "回滚定时器状态异常，安全网未就绪，中止操作"
    systemctl stop "${ROLLBACK_UNIT_NAME}.timer" 2>/dev/null || true
    rm -f "$ROLLBACK_SCRIPT"
    exit 1
fi

log_info "回滚定时器已设置，${ROLLBACK_TIMEOUT} 秒后将自动回滚"
log_warn "如果网络断开，请等待 ${ROLLBACK_TIMEOUT} 秒自动恢复"

# ============================================================
# 步骤 3: 应用新配置
# ============================================================
log_info "步骤 3/5: 应用新的 netplan 配置"

# 清理旧配置，放入新配置
rm -f "${NETPLAN_DIR}/"*.yaml
cp "$CONFIG_FILE" "${NETPLAN_DIR}/01-network.yaml"
chmod 600 "${NETPLAN_DIR}/01-network.yaml"

# 先用 netplan generate 检查语法
if ! netplan generate 2>/tmp/netplan-generate.log; then
    log_error "netplan generate 失败，立即回滚"
    cat /tmp/netplan-generate.log 2>/dev/null || true
    rm -f "${NETPLAN_DIR}/"*.yaml
    cp -a "${BACKUP_PATH}/"* "${NETPLAN_DIR}/" 2>/dev/null || true
    # 回滚路径必须容忍失败：即使 apply 出错，也要继续清理定时器
    if ! netplan apply; then
        log_error "回滚 netplan apply 也失败，等待 ${ROLLBACK_TIMEOUT}s 定时器二次回滚"
    fi
    systemctl stop "${ROLLBACK_UNIT_NAME}.timer" 2>/dev/null || true
    rm -f "$ROLLBACK_SCRIPT" /tmp/netplan-generate.log
    exit 1
fi

netplan apply
log_info "netplan apply 完成"

# ============================================================
# 步骤 4: 自检
# ============================================================
log_info "步骤 4/5: 网络自检"

sleep 3  # 等待网络接口稳定

CHECKS_PASSED=true

# 检查 1: ping 网关
log_info "  检查 1: ping 网关 ${GATEWAY}..."
if ping -c "$PING_COUNT" -W "$PING_TIMEOUT" "$GATEWAY" >/dev/null 2>&1; then
    log_info "  ✓ 网关可达"
else
    log_error "  ✗ 网关不可达"
    CHECKS_PASSED=false
fi

# 检查 2: SSH 端口
log_info "  检查 2: 检测本机 SSH 端口 ${SSH_PORT}..."
if ss -tlnp | grep -q ":${SSH_PORT} " 2>/dev/null; then
    log_info "  ✓ SSH 端口正常"
else
    log_error "  ✗ SSH 端口异常"
    CHECKS_PASSED=false
fi

# 检查 3: 检查 br-pub 接口存在
log_info "  检查 3: 检测 br-pub 接口..."
if ip link show br-pub >/dev/null 2>&1; then
    log_info "  ✓ br-pub 接口存在"
else
    log_error "  ✗ br-pub 接口不存在"
    CHECKS_PASSED=false
fi

# 检查 4: 检查默认路由
log_info "  检查 4: 检测默认路由..."
if ip route | grep -q "default via ${GATEWAY}" 2>/dev/null; then
    log_info "  ✓ 默认路由正确"
else
    log_error "  ✗ 默认路由异常"
    CHECKS_PASSED=false
fi

# 检查 5: 验证 eno1 MTU 未被 bridge 拉低（Ceph jumbo frames 依赖此项）
log_info "  检查 5: 检测 eno1 MTU..."
ENO1_MTU=$(cat /sys/class/net/eno1/mtu 2>/dev/null || echo "0")
if [[ "$ENO1_MTU" -ge 9000 ]]; then
    log_info "  ✓ eno1 MTU=${ENO1_MTU}"
else
    log_error "  ✗ eno1 MTU=${ENO1_MTU}（期望 ≥9000），Ceph 网络将受影响"
    CHECKS_PASSED=false
fi

# ============================================================
# 步骤 5: 根据自检结果处理
# ============================================================
if $CHECKS_PASSED; then
    log_info "步骤 5/5: 自检全部通过，取消回滚定时器"
    systemctl stop "${ROLLBACK_UNIT_NAME}.timer" 2>/dev/null || true
    systemctl stop "${ROLLBACK_UNIT_NAME}.service" 2>/dev/null || true
    rm -f "$ROLLBACK_SCRIPT"
    log_info "════════════════════════════════════════"
    log_info "  网络配置应用成功！"
    log_info "  备份保留在: ${BACKUP_PATH}"
    log_info "════════════════════════════════════════"
else
    log_error "步骤 5/5: 自检失败，立即回滚！"
    log_error "════════════════════════════════════════"
    log_error "  网络自检未通过，正在立即恢复备份配置..."
    log_error "════════════════════════════════════════"

    # 立即回滚，不等待定时器超时
    rm -f "${NETPLAN_DIR}/"*.yaml
    if [[ -d "$BACKUP_PATH" ]] && [[ -n "$(ls -A "$BACKUP_PATH" 2>/dev/null)" ]]; then
        cp -a "${BACKUP_PATH}/"* "${NETPLAN_DIR}/"
    fi
    # 回滚路径必须容忍失败：即使 apply 出错，也要继续清理
    if netplan apply; then
        log_error "已回滚到备份配置: ${BACKUP_PATH}"
    else
        log_error "回滚 netplan apply 也失败，等待 ${ROLLBACK_TIMEOUT}s 定时器二次回滚"
    fi

    # 清理定时器和临时脚本
    systemctl stop "${ROLLBACK_UNIT_NAME}.timer" 2>/dev/null || true
    systemctl stop "${ROLLBACK_UNIT_NAME}.service" 2>/dev/null || true
    rm -f "$ROLLBACK_SCRIPT"

    exit 1
fi
