#!/usr/bin/env bash
# 集群环境变量 — 所有脚本引用此文件，不硬编码 IP

# ── 节点列表 ──────────────────────────────────────────
NODE_NAMES=(node1 node2 node3 node4 node5)
NODE_IPS=(202.151.179.226 202.151.179.227 202.151.179.228 202.151.179.229 202.151.179.230)
NODE_COUNT=${#NODE_IPS[@]}

# ── 网络 ──────────────────────────────────────────────
PUBLIC_SUBNET="202.151.179.224/27"
PUBLIC_GATEWAY="202.151.179.225"
MGMT_SUBNET="10.0.10.0/24"
CEPH_PUBLIC_SUBNET="10.0.20.0/24"
CEPH_CLUSTER_SUBNET="10.0.30.0/24"

# ── 服务端口 ──────────────────────────────────────────
NODE_EXPORTER_PORT=9100
CEPH_MGR_PROMETHEUS_PORT=9283
INCUS_METRICS_PORT=8444

# ── 监控栈 ──────────────────────────────────────────
MONITORING_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../monitoring" && pwd)"
PROMETHEUS_PORT=9090
GRAFANA_PORT=3000
LOKI_PORT=3100
ALERTMANAGER_PORT=9093
