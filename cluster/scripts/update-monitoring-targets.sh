#!/bin/bash
# ============================================================
# 更新 Prometheus 采集目标
# 用途：根据 Incus 集群当前成员自动生成 Prometheus 静态目标文件
# 前提：Prometheus 已配置 file_sd_configs 指向 targets 目录
# ============================================================
set -euo pipefail

# ==================== 配置区 ====================
TARGETS_DIR="/etc/prometheus/targets.d"
TARGETS_FILE="${TARGETS_DIR}/incus-cluster.json"

# 各服务的导出器端口
NODE_EXPORTER_PORT=9100
INCUS_METRICS_PORT=8444       # Incus 内置 metrics 端口
CEPH_EXPORTER_PORT=9283
# ==================== 配置区结束 ==================

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log()  { echo -e "${GREEN}[INFO]${NC} $*"; }
warn() { echo -e "${YELLOW}[WARN]${NC} $*"; }
err()  { echo -e "${RED}[ERR]${NC} $*"; exit 1; }

# ─── 获取集群成员 IP ─────────────────────────────────────────

get_member_ips() {
    incus cluster list --format csv 2>/dev/null | while IFS=',' read -r name url _rest; do
        # URL 格式：https://IP:8443
        local ip
        ip=$(echo "$url" | grep -oP 'https://\K[^:]+')
        if [ -n "$ip" ]; then
            echo "${name}|${ip}"
        fi
    done
}

# ─── 生成 Prometheus 目标文件 ─────────────────────────────────

generate_targets() {
    mkdir -p "$TARGETS_DIR"

    local members
    members=$(get_member_ips)

    if [ -z "$members" ]; then
        err "无法获取集群成员信息"
    fi

    log "当前集群成员:"
    echo "$members" | while IFS='|' read -r name ip; do
        echo "  ${name} -> ${ip}"
    done

    # 生成 JSON 目标文件（Prometheus file_sd_configs 格式）
    local json="["

    # Node Exporter 目标
    local node_targets=""
    while IFS='|' read -r name ip; do
        [ -n "$node_targets" ] && node_targets="${node_targets},"
        node_targets="${node_targets}\"${ip}:${NODE_EXPORTER_PORT}\""
    done <<< "$members"

    json="${json}
  {
    \"targets\": [${node_targets}],
    \"labels\": {
      \"job\": \"node-exporter\",
      \"cluster\": \"incus\"
    }
  },"

    # Incus Metrics 目标
    local incus_targets=""
    while IFS='|' read -r name ip; do
        [ -n "$incus_targets" ] && incus_targets="${incus_targets},"
        incus_targets="${incus_targets}\"${ip}:${INCUS_METRICS_PORT}\""
    done <<< "$members"

    json="${json}
  {
    \"targets\": [${incus_targets}],
    \"labels\": {
      \"job\": \"incus-metrics\",
      \"cluster\": \"incus\"
    }
  },"

    # Ceph Exporter 目标（仅 OSD 节点可能有）
    local ceph_targets=""
    while IFS='|' read -r name ip; do
        [ -n "$ceph_targets" ] && ceph_targets="${ceph_targets},"
        ceph_targets="${ceph_targets}\"${ip}:${CEPH_EXPORTER_PORT}\""
    done <<< "$members"

    json="${json}
  {
    \"targets\": [${ceph_targets}],
    \"labels\": {
      \"job\": \"ceph-exporter\",
      \"cluster\": \"incus\"
    }
  }
]"

    # 写入目标文件
    echo "$json" > "$TARGETS_FILE"
    log "目标文件已写入: ${TARGETS_FILE}"

    # 验证 JSON 格式
    if command -v python3 &>/dev/null; then
        python3 -m json.tool "$TARGETS_FILE" >/dev/null 2>&1 || {
            err "生成的 JSON 格式无效，请检查"
        }
        log "JSON 格式验证通过"
    fi

    # 通知 Prometheus 重载配置（如果运行中）
    reload_prometheus
}

# ─── 重载 Prometheus ─────────────────────────────────────────

reload_prometheus() {
    # 方式 1：发送 SIGHUP
    if pgrep -x prometheus >/dev/null 2>&1; then
        kill -HUP "$(pgrep -x prometheus)" 2>/dev/null && {
            log "Prometheus 已重载配置 (SIGHUP)"
            return 0
        }
    fi

    # 方式 2：通过 HTTP API 重载（需启用 --web.enable-lifecycle）
    if curl -s -X POST "http://localhost:9090/-/reload" --max-time 5 >/dev/null 2>&1; then
        log "Prometheus 已重载配置 (HTTP API)"
        return 0
    fi

    # 方式 3：Docker 容器内
    if docker ps --format '{{.Names}}' 2>/dev/null | grep -q prometheus; then
        local container
        container=$(docker ps --format '{{.Names}}' | grep prometheus | head -1)
        docker kill --signal=SIGHUP "$container" 2>/dev/null && {
            log "Prometheus 容器已重载配置: ${container}"
            return 0
        }
    fi

    warn "Prometheus 未运行或无法重载，file_sd_configs 会在下次 scrape 时自动加载"
}

# ─── 主逻辑 ──────────────────────────────────────────────────

log "开始更新 Prometheus 采集目标..."
generate_targets
log "完成"
