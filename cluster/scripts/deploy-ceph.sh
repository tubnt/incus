#!/bin/bash
# ============================================================
# Ceph 集群部署脚本（cephadm 方式）
# 用途: 在 5 节点集群上部署 Ceph（MON/MGR/OSD）+ 存储池 + 接入 Incus
#
# 用法:
#   deploy-ceph.sh <步骤>
#
# 步骤:
#   preflight      - 前置检查（cephadm、网络、磁盘）
#   bootstrap      - 在 node1 上初始化 Ceph 集群
#   add-hosts      - 将 node2-5 添加到集群
#   deploy-mons    - 部署 MON + MGR（node1-3）
#   deploy-osds    - 部署 OSD（全部 5 节点，dmcrypt 加密）
#   create-pool    - 创建存储池（3 副本，host 故障域）
#   tune           - 性能调优（恢复限速、msgr2 TLS）
#   connect-incus  - 接入 Incus 存储
#   verify         - 验证集群状态
#   all            - 按顺序执行以上全部步骤
# ============================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/../configs/cluster-env.sh"

# ==================== 日志 ====================
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log()  { echo -e "${GREEN}[INFO]${NC} $*"; }
warn() { echo -e "${YELLOW}[WARN]${NC} $*"; }
err()  { echo -e "${RED}[ERR]${NC} $*" >&2; exit 1; }
step() { echo -e "\n${BLUE}========== $* ==========${NC}"; }

# 远程执行（通过管理网）
run_on() {
  local node="$1"; shift
  local ip
  ip=$(get_node_field "$node" 3)
  ssh -o StrictHostKeyChecking=no "${SSH_USER}@${ip}" "$@"
}

# 复制文件到远程节点
copy_to() {
  local node="$1" src="$2" dst="$3"
  local ip
  ip=$(get_node_field "$node" 3)
  scp -o StrictHostKeyChecking=no "$src" "${SSH_USER}@${ip}:${dst}"
}

# ==================== 前置检查 ====================
do_preflight() {
  step "前置检查"

  log "检查 cephadm 是否已安装..."
  if ! run_on "${CEPH_BOOTSTRAP_NODE}" "command -v cephadm >/dev/null 2>&1"; then
    warn "cephadm 未安装，正在安装..."
    run_on "${CEPH_BOOTSTRAP_NODE}" "
      apt-get update && apt-get install -y cephadm
    "
  fi
  log "cephadm 已就绪"

  log "检查 Ceph Public 网络连通性..."
  for node in $(get_all_nodes); do
    local ceph_ip
    ceph_ip=$(get_node_field "$node" 4)
    if ! run_on "${CEPH_BOOTSTRAP_NODE}" "ping -c 1 -W 2 ${ceph_ip}" >/dev/null 2>&1; then
      err "无法从 ${CEPH_BOOTSTRAP_NODE} ping 到 ${node} (${ceph_ip})"
    fi
    log "  ${node} (${ceph_ip}) 连通"
  done

  log "检查 Ceph Cluster 网络连通性..."
  for node in $(get_all_nodes); do
    local cluster_ip
    cluster_ip=$(get_node_field "$node" 5)
    if ! run_on "${CEPH_BOOTSTRAP_NODE}" "ping -c 1 -W 2 ${cluster_ip}" >/dev/null 2>&1; then
      err "无法从 ${CEPH_BOOTSTRAP_NODE} ping 到 ${node} Cluster 网络 (${cluster_ip})"
    fi
    log "  ${node} (${cluster_ip}) 连通"
  done

  log "检查各节点磁盘状态..."
  for node in $(get_osd_nodes); do
    log "  ${node} 可用磁盘:"
    run_on "$node" "lsblk -d -n -o NAME,SIZE,TYPE,MOUNTPOINT | grep -E '^sd|^nvme' | grep -v -E '/$|/boot'" || true
  done

  log "前置检查完成"
}

# ==================== Bootstrap ====================
do_bootstrap() {
  step "Bootstrap Ceph 集群（${CEPH_BOOTSTRAP_NODE}）"

  # 检查是否已经 bootstrap 过
  if run_on "${CEPH_BOOTSTRAP_NODE}" "test -f /etc/ceph/ceph.conf" 2>/dev/null; then
    warn "检测到 /etc/ceph/ceph.conf 已存在，跳过 bootstrap"
    warn "如需重新部署，请先清理: cephadm rm-cluster --fsid <fsid> --zap-osds"
    return 0
  fi

  log "执行 cephadm bootstrap..."
  run_on "${CEPH_BOOTSTRAP_NODE}" "
    cephadm bootstrap \
      --mon-ip ${CEPH_BOOTSTRAP_MON_IP} \
      --cluster-network ${CEPH_CLUSTER_NETWORK} \
      --allow-fqdn-hostname \
      --skip-monitoring-stack
  "

  log "Bootstrap 完成，集群已在 ${CEPH_BOOTSTRAP_NODE} 上初始化"
}

# ==================== 添加节点 ====================
do_add_hosts() {
  step "添加节点到 Ceph 集群"

  # 获取 SSH 公钥
  local pub_key
  pub_key=$(run_on "${CEPH_BOOTSTRAP_NODE}" "ceph cephadm get-pub-key")

  for node in $(get_all_nodes); do
    [[ "$node" == "${CEPH_BOOTSTRAP_NODE}" ]] && continue

    local ceph_pub_ip
    ceph_pub_ip=$(get_node_field "$node" 4)
    local mgmt_ip
    mgmt_ip=$(get_node_field "$node" 3)
    local role
    role=$(get_node_field "$node" 6)

    log "添加 ${node} (${ceph_pub_ip})..."

    # 分发 SSH 公钥（通过 stdin 传输，避免 shell 注入）
    printf '%s\n' "$pub_key" | run_on "$node" "
      mkdir -p /root/.ssh && chmod 700 /root/.ssh
      cat >> /root/.ssh/authorized_keys
      sort -u /root/.ssh/authorized_keys -o /root/.ssh/authorized_keys
    "

    # 确保目标节点有 cephadm
    if ! run_on "$node" "command -v cephadm >/dev/null 2>&1"; then
      run_on "$node" "apt-get update && apt-get install -y cephadm"
    fi

    # 添加到集群（使用 Ceph Public IP）
    local labels="_admin"
    if [[ "$role" == "mon-mgr-osd" ]]; then
      labels="_admin mon mgr osd"
    else
      labels="_admin osd"
    fi

    run_on "${CEPH_BOOTSTRAP_NODE}" "
      ceph orch host add ${node} ${ceph_pub_ip} --labels ${labels}
    "
    log "  ${node} 已添加（标签: ${labels}）"
  done

  log "等待节点就绪..."
  sleep 10
  run_on "${CEPH_BOOTSTRAP_NODE}" "ceph orch host ls"
  log "所有节点已添加"
}

# ==================== 部署 MON + MGR ====================
do_deploy_mons() {
  step "部署 MON + MGR（node1-3）"

  local mon_nodes
  mon_nodes=$(get_mon_nodes | tr '\n' ',' | sed 's/,$//')

  log "配置 MON 部署位置: ${mon_nodes}"
  run_on "${CEPH_BOOTSTRAP_NODE}" "
    ceph orch apply mon --placement='3 ${mon_nodes// /,}'
  "

  log "配置 MGR 部署位置: ${mon_nodes}"
  run_on "${CEPH_BOOTSTRAP_NODE}" "
    ceph orch apply mgr --placement='3 ${mon_nodes// /,}'
  "

  log "等待 MON/MGR 就绪..."
  local retries=0
  while [[ $retries -lt 30 ]]; do
    local mon_count
    mon_count=$(run_on "${CEPH_BOOTSTRAP_NODE}" "ceph mon stat -f json 2>/dev/null | python3 -c 'import sys,json; print(json.load(sys.stdin)[\"election_epoch\"])'" 2>/dev/null || echo "0")
    if [[ "$mon_count" -gt 0 ]]; then
      break
    fi
    sleep 5
    retries=$((retries + 1))
  done

  run_on "${CEPH_BOOTSTRAP_NODE}" "ceph -s"
  log "MON + MGR 部署完成"
}

# ==================== 部署 OSD ====================
do_deploy_osds() {
  step "部署 OSD（dmcrypt 加密）"

  local spec_file="${SCRIPT_DIR}/../configs/ceph-osd-spec.yaml"
  if [[ ! -f "$spec_file" ]]; then
    err "OSD spec 文件不存在: ${spec_file}"
  fi

  log "复制 OSD spec 到 bootstrap 节点..."
  copy_to "${CEPH_BOOTSTRAP_NODE}" "$spec_file" "/tmp/ceph-osd-spec.yaml"

  log "应用 OSD service spec（encrypted: true）..."
  run_on "${CEPH_BOOTSTRAP_NODE}" "
    ceph orch apply -i /tmp/ceph-osd-spec.yaml
    rm -f /tmp/ceph-osd-spec.yaml
  "

  log "等待 OSD 部署完成（预计 20 个 OSD: 5 节点 × 4 盘）..."
  local retries=0
  local expected_osds=20
  while [[ $retries -lt 60 ]]; do
    local osd_count
    osd_count=$(run_on "${CEPH_BOOTSTRAP_NODE}" "ceph osd stat -f json 2>/dev/null | python3 -c 'import sys,json; d=json.load(sys.stdin); print(d[\"num_up_osds\"])'" 2>/dev/null || echo "0")
    log "  已启动 OSD: ${osd_count}/${expected_osds}"
    if [[ "$osd_count" -ge "$expected_osds" ]]; then
      break
    fi
    sleep 10
    retries=$((retries + 1))
  done

  run_on "${CEPH_BOOTSTRAP_NODE}" "ceph osd tree"
  log "OSD 部署完成"
}

# ==================== 创建存储池 ====================
do_create_pool() {
  step "创建存储池 ${CEPH_POOL_NAME}"

  # 检查是否已存在
  if run_on "${CEPH_BOOTSTRAP_NODE}" "ceph osd pool ls | grep -q '^${CEPH_POOL_NAME}$'" 2>/dev/null; then
    warn "存储池 ${CEPH_POOL_NAME} 已存在，跳过创建"
  else
    log "创建 pool: ${CEPH_POOL_NAME}（PG 数: ${CEPH_POOL_PG_NUM}）"
    run_on "${CEPH_BOOTSTRAP_NODE}" "
      ceph osd pool create ${CEPH_POOL_NAME} ${CEPH_POOL_PG_NUM}
    "
  fi

  log "设置副本数: size=${CEPH_POOL_SIZE}, min_size=${CEPH_POOL_MIN_SIZE}"
  run_on "${CEPH_BOOTSTRAP_NODE}" "
    ceph osd pool set ${CEPH_POOL_NAME} size ${CEPH_POOL_SIZE}
    ceph osd pool set ${CEPH_POOL_NAME} min_size ${CEPH_POOL_MIN_SIZE}
  "

  log "设置 CRUSH 故障域为 host（chooseleaf_type=${CEPH_CRUSH_LEAF_TYPE}）"
  run_on "${CEPH_BOOTSTRAP_NODE}" "
    ceph osd pool set ${CEPH_POOL_NAME} crush_rule replicated_rule
    ceph osd crush rule dump replicated_rule | python3 -c '
import sys, json
rule = json.load(sys.stdin)
for step in rule[\"steps\"]:
    if step.get(\"op\") == \"chooseleaf_firstn\":
        if step[\"type\"] != ${CEPH_CRUSH_LEAF_TYPE}:
            print(\"NEED_FIX\")
' | grep -q NEED_FIX && {
      # 创建自定义 CRUSH rule 确保故障域为 host
      ceph osd crush rule create-replicated host-rule default host
      ceph osd pool set ${CEPH_POOL_NAME} crush_rule host-rule
    }
  " || true

  # 初始化 pool 为 RBD 类型
  run_on "${CEPH_BOOTSTRAP_NODE}" "
    ceph osd pool application enable ${CEPH_POOL_NAME} rbd || true
    rbd pool init ${CEPH_POOL_NAME} || true
  "

  log "存储池配置完成"
  run_on "${CEPH_BOOTSTRAP_NODE}" "ceph osd pool ls detail | grep ${CEPH_POOL_NAME}"
}

# ==================== 性能调优 ====================
do_tune() {
  step "Ceph 性能调优"

  log "设置恢复限速参数（有专用 10G Cluster 网络，适当放宽）..."
  run_on "${CEPH_BOOTSTRAP_NODE}" "
    # 恢复线程数
    ceph config set osd osd_recovery_max_active 5
    ceph config set osd osd_recovery_max_active_hdd 5
    ceph config set osd osd_recovery_max_active_ssd 10

    # 恢复优先级与限速
    ceph config set osd osd_recovery_op_priority 3
    ceph config set osd osd_max_backfills 3

    # 回填速率（SSD + 10G 专用网络可以更激进）
    ceph config set osd osd_recovery_sleep_ssd 0
  "

  log "启用 msgr2 TLS 加密..."
  run_on "${CEPH_BOOTSTRAP_NODE}" "
    # 确保 ms_cluster_mode 和 ms_service_mode 使用加密
    ceph config set global ms_cluster_mode 'secure crc'
    ceph config set global ms_service_mode 'secure crc'
    ceph config set global ms_client_mode 'secure crc'
  "

  log "其他调优..."
  run_on "${CEPH_BOOTSTRAP_NODE}" "
    # 启用 Prometheus 模块（供后续监控使用）
    ceph mgr module enable prometheus || true

    # PG autoscaler
    ceph config set global osd_pool_default_pg_autoscale_mode warn
  "

  log "性能调优完成"
}

# ==================== 接入 Incus ====================
do_connect_incus() {
  step "接入 Incus 存储"

  log "在各节点安装 ceph-common..."
  for node in $(get_all_nodes); do
    run_on "$node" "
      if ! dpkg -l ceph-common >/dev/null 2>&1; then
        apt-get update && apt-get install -y ceph-common
      fi
    "
    log "  ${node}: ceph-common 已就绪"
  done

  log "分发 Ceph 配置和密钥到各节点..."
  # 通过 scp 传输敏感文件，避免 shell 变量展开导致内容损坏或注入
  local tmp_conf tmp_keyring
  tmp_conf=$(mktemp)
  tmp_keyring=$(mktemp)
  trap "rm -f '$tmp_conf' '$tmp_keyring'" RETURN

  run_on "${CEPH_BOOTSTRAP_NODE}" "cat /etc/ceph/ceph.conf" > "$tmp_conf"
  run_on "${CEPH_BOOTSTRAP_NODE}" "cat /etc/ceph/ceph.client.admin.keyring" > "$tmp_keyring"
  chmod 600 "$tmp_keyring"

  for node in $(get_all_nodes); do
    [[ "$node" == "${CEPH_BOOTSTRAP_NODE}" ]] && continue
    run_on "$node" "mkdir -p /etc/ceph"
    copy_to "$node" "$tmp_conf" "/etc/ceph/ceph.conf"
    copy_to "$node" "$tmp_keyring" "/etc/ceph/ceph.client.admin.keyring"
    run_on "$node" "chmod 600 /etc/ceph/ceph.client.admin.keyring"
    log "  ${node}: 配置已分发"
  done

  log "创建 Incus 存储池: ${INCUS_STORAGE_NAME}..."
  # 检查是否已存在
  if incus storage show "${INCUS_STORAGE_NAME}" >/dev/null 2>&1; then
    warn "Incus 存储池 ${INCUS_STORAGE_NAME} 已存在，跳过创建"
  else
    incus storage create "${INCUS_STORAGE_NAME}" ceph \
      source="${CEPH_POOL_NAME}" \
      ceph.cluster_name="${CEPH_CLUSTER_NAME}" \
      ceph.user.name="${INCUS_CEPH_USER}"
    log "Incus 存储池 ${INCUS_STORAGE_NAME} 创建成功"
  fi
}

# ==================== 验证 ====================
do_verify() {
  step "集群状态验证"

  log "Ceph 集群状态:"
  run_on "${CEPH_BOOTSTRAP_NODE}" "ceph status"

  echo ""
  log "OSD 树:"
  run_on "${CEPH_BOOTSTRAP_NODE}" "ceph osd tree"

  echo ""
  log "存储池详情:"
  run_on "${CEPH_BOOTSTRAP_NODE}" "ceph osd pool ls detail"

  echo ""
  log "dmcrypt 加密状态:"
  run_on "${CEPH_BOOTSTRAP_NODE}" "ceph config-key ls | grep dm-crypt | head -5 && echo '...(dmcrypt 密钥已配置)'" 2>/dev/null || warn "无 dmcrypt 密钥（OSD 可能尚未部署）"

  echo ""
  log "Incus 存储列表:"
  incus storage list 2>/dev/null || warn "Incus 存储未配置或 incus 未安装"

  log "验证完成"
}

# ==================== 主入口 ====================
usage() {
  echo "用法: $0 <步骤>"
  echo ""
  echo "步骤:"
  echo "  preflight      前置检查"
  echo "  bootstrap      初始化 Ceph 集群"
  echo "  add-hosts      添加节点"
  echo "  deploy-mons    部署 MON + MGR"
  echo "  deploy-osds    部署 OSD"
  echo "  create-pool    创建存储池"
  echo "  tune           性能调优"
  echo "  connect-incus  接入 Incus"
  echo "  verify         验证状态"
  echo "  all            执行全部步骤"
  exit 1
}

main() {
  local action="${1:-}"
  [[ -z "$action" ]] && usage

  case "$action" in
    preflight)     do_preflight ;;
    bootstrap)     do_bootstrap ;;
    add-hosts)     do_add_hosts ;;
    deploy-mons)   do_deploy_mons ;;
    deploy-osds)   do_deploy_osds ;;
    create-pool)   do_create_pool ;;
    tune)          do_tune ;;
    connect-incus) do_connect_incus ;;
    verify)        do_verify ;;
    all)
      do_preflight
      do_bootstrap
      do_add_hosts
      do_deploy_mons
      do_deploy_osds
      do_create_pool
      do_tune
      do_connect_incus
      do_verify
      ;;
    *) usage ;;
  esac
}

main "$@"
