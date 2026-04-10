#!/bin/bash
# ============================================================
# 集群环境变量配置
# 所有集群脚本通过 source 此文件获取统一配置
# ============================================================

# ==================== 节点定义 ====================
# 格式: 节点名:公网IP:管理网IP:Ceph Public IP:Ceph Cluster IP:角色
# 角色: mon-mgr-osd = MON+MGR+OSD, osd = 仅OSD
CLUSTER_NODES=(
  "node1:202.151.179.226:10.0.10.1:10.0.20.1:10.0.30.1:mon-mgr-osd"
  "node2:202.151.179.227:10.0.10.2:10.0.20.2:10.0.30.2:mon-mgr-osd"
  "node3:202.151.179.228:10.0.10.3:10.0.20.3:10.0.30.3:mon-mgr-osd"
  "node4:202.151.179.229:10.0.10.4:10.0.20.4:10.0.30.4:osd"
  "node5:202.151.179.230:10.0.10.5:10.0.20.5:10.0.30.5:osd"
)

# ==================== 网络配置 ====================
PUBLIC_NETWORK="202.151.179.224/27"
PUBLIC_GATEWAY="202.151.179.225"
MGMT_NETWORK="10.0.10.0/24"
CEPH_PUBLIC_NETWORK="10.0.20.0/24"
CEPH_CLUSTER_NETWORK="10.0.30.0/24"

# 物理网卡
NIC_PRIMARY="eno1"      # 公网 + VLAN 10/20
NIC_CLUSTER="eno2"      # Ceph Cluster 直连

# VLAN
VLAN_MGMT=10
VLAN_CEPH_PUBLIC=20

# 网桥
BRIDGE_NAME="br-pub"

# ==================== Ceph 配置 ====================
CEPH_CLUSTER_NAME="ceph"
CEPH_POOL_NAME="incus-pool"
CEPH_POOL_PG_NUM=128
CEPH_POOL_SIZE=3
CEPH_POOL_MIN_SIZE=2
CEPH_CRUSH_LEAF_TYPE=1   # host 级故障域

# Bootstrap 节点（第一个 MON 节点）
CEPH_BOOTSTRAP_NODE="node1"
CEPH_BOOTSTRAP_MON_IP="10.0.20.1"

# OSD 加密
CEPH_OSD_ENCRYPTED=true

# ==================== Incus 配置 ====================
INCUS_STORAGE_NAME="ceph-pool"
INCUS_CEPH_USER="admin"

# ==================== 通用 ====================
DNS_SERVERS="1.1.1.1,8.8.8.8"
SSH_USER="root"

# ==================== 辅助函数 ====================

# 获取节点字段
# 用法: get_node_field "node1" 2  (返回公网IP)
# 字段: 1=名称 2=公网IP 3=管理网IP 4=Ceph Public IP 5=Ceph Cluster IP 6=角色
get_node_field() {
  local target="$1" field="$2"
  for node in "${CLUSTER_NODES[@]}"; do
    IFS=':' read -ra parts <<< "$node"
    if [[ "${parts[0]}" == "$target" ]]; then
      echo "${parts[$((field-1))]}"
      return 0
    fi
  done
  return 1
}

# 获取所有节点名
get_all_nodes() {
  for node in "${CLUSTER_NODES[@]}"; do
    echo "${node%%:*}"
  done
}

# 获取指定角色的节点
get_nodes_by_role() {
  local role="$1"
  for node in "${CLUSTER_NODES[@]}"; do
    IFS=':' read -ra parts <<< "$node"
    if [[ "${parts[5]}" == "$role" ]]; then
      echo "${parts[0]}"
    fi
  done
}

# 获取 MON 节点列表
get_mon_nodes() {
  get_nodes_by_role "mon-mgr-osd"
}

# 获取所有 OSD 节点列表（包括 mon-mgr-osd 节点）
get_osd_nodes() {
  for node in "${CLUSTER_NODES[@]}"; do
    IFS=':' read -ra parts <<< "$node"
    if [[ "${parts[5]}" == *"osd"* ]]; then
      echo "${parts[0]}"
    fi
  done
}
