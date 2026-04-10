#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/cluster-env.sh"

# ── 颜色 ──
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

info()  { echo -e "${GREEN}[INFO]${NC} $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC} $*"; }
error() { echo -e "${RED}[ERROR]${NC} $*" >&2; }

# ── 前置检查 ──
check_prerequisites() {
    info "检查前置依赖..."

    if ! command -v docker &>/dev/null; then
        error "未安装 docker"
        exit 1
    fi

    if ! docker compose version &>/dev/null; then
        error "未安装 docker compose 插件"
        exit 1
    fi

    info "docker 和 docker compose 已就绪"
}

# ── 部署 node_exporter 到远程节点 ──
deploy_node_exporter() {
    local ip="$1"
    local name="$2"

    info "部署 node_exporter 到 ${name} (${ip})..."

    ssh -o StrictHostKeyChecking=no "root@${ip}" bash -s <<'REMOTE_SCRIPT'
# 如果已在运行则跳过
if systemctl is-active --quiet node_exporter 2>/dev/null; then
    echo "node_exporter 已在运行，跳过"
    exit 0
fi

NODE_EXPORTER_VERSION="1.8.1"
ARCH="$(dpkg --print-architecture 2>/dev/null || echo amd64)"

# 下载
if [ ! -f /usr/local/bin/node_exporter ]; then
    cd /tmp
    curl -fsSL "https://github.com/prometheus/node_exporter/releases/download/v${NODE_EXPORTER_VERSION}/node_exporter-${NODE_EXPORTER_VERSION}.linux-${ARCH}.tar.gz" \
        -o node_exporter.tar.gz
    tar xzf node_exporter.tar.gz
    cp "node_exporter-${NODE_EXPORTER_VERSION}.linux-${ARCH}/node_exporter" /usr/local/bin/
    chmod +x /usr/local/bin/node_exporter
    rm -rf node_exporter.tar.gz "node_exporter-${NODE_EXPORTER_VERSION}.linux-${ARCH}"
fi

# 创建 systemd 服务
cat > /etc/systemd/system/node_exporter.service <<'EOF'
[Unit]
Description=Prometheus Node Exporter
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=nobody
Group=nogroup
ExecStart=/usr/local/bin/node_exporter
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable --now node_exporter
echo "node_exporter 已部署并启动"
REMOTE_SCRIPT
}

# ── 检查 Incus 证书 ──
check_incus_cert() {
    local cert_dir="${MONITORING_DIR}/prometheus/incus-cert"
    if [ ! -f "${cert_dir}/metrics.crt" ] || [ ! -f "${cert_dir}/metrics.key" ]; then
        warn "未找到 Incus metrics 证书"
        warn "请参考 ${cert_dir}/README.md 生成并放置证书"
        warn "Prometheus 将无法采集 Incus 指标，但其他采集目标不受影响"
    else
        info "Incus metrics 证书已就位"
    fi
}

# ── 主流程 ──
main() {
    info "========== 监控栈部署 =========="

    check_prerequisites

    # 部署 node_exporter 到所有节点
    info "部署 node_exporter 到集群节点..."
    for i in "${!NODE_IPS[@]}"; do
        deploy_node_exporter "${NODE_IPS[$i]}" "${NODE_NAMES[$i]}" || {
            warn "node_exporter 部署到 ${NODE_NAMES[$i]} 失败，继续..."
        }
    done

    # 检查 Incus 证书
    check_incus_cert

    # 启动 Docker Compose 监控栈
    info "启动监控栈..."
    cd "${MONITORING_DIR}"
    docker compose up -d

    info "========== 部署完成 =========="
    info "Prometheus: http://localhost:${PROMETHEUS_PORT}"
    info "Grafana:    http://localhost:${GRAFANA_PORT}  (默认 admin/admin)"
    info "Loki:       http://localhost:${LOKI_PORT}"
    info "Alertmanager: http://localhost:${ALERTMANAGER_PORT}"
}

main "$@"
