#!/usr/bin/env bash
# =============================================================================
# Incus 集群初始化脚本
#
# 用法:
#   在 node1 上初始化集群:
#     ./setup-cluster.sh --init
#
#   在 node2-5 上加入集群:
#     ./setup-cluster.sh --join <token>
#
# 依赖: incus, jq
# =============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=../configs/cluster-env.sh
source "${SCRIPT_DIR}/../configs/cluster-env.sh"

# ----- 日志函数 -----
log_info()  { echo -e "\033[32m[INFO]\033[0m  $(date '+%H:%M:%S') $*"; }
log_warn()  { echo -e "\033[33m[WARN]\033[0m  $(date '+%H:%M:%S') $*"; }
log_error() { echo -e "\033[31m[ERROR]\033[0m $(date '+%H:%M:%S') $*" >&2; }
log_step()  { echo -e "\033[36m[STEP]\033[0m  $(date '+%H:%M:%S') $*"; }

# ----- 错误恢复 trap -----
cleanup_on_error() {
  local exit_code=$?
  if [[ $exit_code -ne 0 ]]; then
    log_error "脚本异常退出 (exit code: ${exit_code})"
    log_error "节点可能处于半初始化状态，恢复步骤："
    log_error "  1. 检查 incus 服务状态: systemctl status incus"
    log_error "  2. 如需重置本节点: incus admin shutdown && rm -rf /var/lib/incus/*"
    log_error "  3. 如已加入集群但状态异常: incus cluster remove --force ${LOCAL_NAME:-<节点名>}"
    log_error "  4. 修复问题后重新执行本脚本"
  fi
}
trap cleanup_on_error EXIT

# ----- 前置检查 -----
preflight_check() {
  log_step "执行前置检查..."

  # 检查 root 权限
  if [[ $EUID -ne 0 ]]; then
    log_error "请以 root 权限运行此脚本"
    exit 1
  fi

  # 检查 incus 是否已安装
  if ! command -v incus &>/dev/null; then
    log_error "未找到 incus 命令，请先安装 Incus"
    exit 1
  fi

  # 检查 jq
  if ! command -v jq &>/dev/null; then
    log_error "未找到 jq 命令，请先安装: apt install -y jq"
    exit 1
  fi

  # 识别当前节点（通过管理网 IP 匹配）
  LOCAL_NAME=""
  LOCAL_MGMT_IP=""
  LOCAL_ROLE=""
  for node_name in $(get_all_nodes); do
    local mgmt_ip
    mgmt_ip=$(get_node_field "$node_name" 3)
    if ip addr show | grep -q "inet ${mgmt_ip}/"; then
      LOCAL_NAME="$node_name"
      LOCAL_MGMT_IP="$mgmt_ip"
      # 角色映射：mon-mgr-osd → voter, osd → stand-by
      local raw_role
      raw_role=$(get_node_field "$node_name" 6)
      case "$raw_role" in
        mon-mgr-osd) LOCAL_ROLE="voter" ;;
        *)           LOCAL_ROLE="stand-by" ;;
      esac
      break
    fi
  done
  if [[ -z "$LOCAL_NAME" ]]; then
    log_error "无法识别当前节点（未匹配到管理网 IP），请检查网络配置"
    exit 1
  fi
  log_info "当前节点: ${LOCAL_NAME} (管理网: ${LOCAL_MGMT_IP}, 角色: ${LOCAL_ROLE})"

  # 检查管理网连通性（ping 其他节点）
  log_info "检查管理网连通性..."
  local fail=0
  for node_name in $(get_all_nodes); do
    local mgmt_ip
    mgmt_ip=$(get_node_field "$node_name" 3)
    if [[ "$mgmt_ip" == "$LOCAL_MGMT_IP" ]]; then
      continue
    fi
    if ! ping -c 1 -W 2 "$mgmt_ip" &>/dev/null; then
      log_warn "无法 ping 通 ${node_name} (${mgmt_ip})"
      fail=$((fail + 1))
    fi
  done
  if [[ $fail -gt 0 ]]; then
    log_warn "有 ${fail} 个节点管理网不通，集群操作可能失败"
  fi

  log_info "前置检查完成"
}

# ----- Token 安全读取与校验 -----
# 优先从 stdin/环境变量读取 token，避免暴露于 /proc/PID/cmdline
read_token_secure() {
  local token="${1:-}"

  # 优先级: 参数 > 环境变量 > stdin
  if [[ -z "$token" && -n "${INCUS_JOIN_TOKEN:-}" ]]; then
    token="$INCUS_JOIN_TOKEN"
    log_info "从环境变量 INCUS_JOIN_TOKEN 读取 token"
  fi
  if [[ -z "$token" && ! -t 0 ]]; then
    read -r token
    log_info "从 stdin 读取 token"
  fi
  if [[ -z "$token" ]]; then
    log_error "未提供 join token"
    log_error "用法: INCUS_JOIN_TOKEN=<token> $0 --join"
    log_error "  或: echo <token> | $0 --join"
    log_error "  或: $0 --join <token>"
    exit 1
  fi

  # 基础格式校验（Incus token 为 base64 编码的 JSON）
  if ! echo "$token" | base64 -d &>/dev/null; then
    log_error "token 格式无效（非合法 base64 编码），请检查 token 是否完整"
    exit 1
  fi

  printf '%s' "$token"
}

# ----- 集群初始化（node1） -----
do_init() {
  log_step "开始初始化 Incus 集群..."

  # 确认在 voter 节点上执行
  if [[ "$LOCAL_ROLE" != "voter" ]]; then
    log_error "集群初始化应在 voter 角色节点上执行（当前: ${LOCAL_ROLE}）"
    exit 1
  fi

  local bind_addr="${LOCAL_MGMT_IP}:${INCUS_CLUSTER_PORT}"

  # 生成 preseed 配置
  local preseed
  preseed=$(cat <<YAML
config:
  core.https_address: "${bind_addr}"
  cluster.https_address: "${bind_addr}"
cluster:
  server_name: "${LOCAL_NAME}"
  enabled: true
YAML
  )

  log_info "使用 preseed 初始化集群（绑定: ${bind_addr}）..."
  printf '%s\n' "$preseed" | incus admin init --preseed

  log_info "集群初始化完成，当前节点 ${LOCAL_NAME} 已成为首个成员"

  # 设置集群参数
  configure_cluster_params

  # 验证
  verify_cluster

  # 生成加入提示
  log_step "如需将其他节点加入集群，在本节点执行:"
  echo ""
  for node_name in $(get_all_nodes); do
    if [[ "$node_name" == "$LOCAL_NAME" ]]; then
      continue
    fi
    echo "  # 为 ${node_name} 生成 join token:"
    echo "  incus cluster add ${node_name}"
    echo ""
  done
  echo "  然后在目标节点执行:"
  echo "  ./setup-cluster.sh --join <token>"
}

# ----- 加入集群 -----
do_join() {
  local token
  token=$(read_token_secure "${1:-}")

  log_step "开始加入 Incus 集群..."

  local bind_addr="${LOCAL_MGMT_IP}:${INCUS_CLUSTER_PORT}"

  # 生成 preseed 配置（token 用单引号包裹防止 YAML 注入）
  local escaped_token
  escaped_token=$(printf '%s' "$token" | sed "s/'/''/g")
  local preseed
  preseed=$(cat <<YAML
config:
  core.https_address: "${bind_addr}"
  cluster.https_address: "${bind_addr}"
cluster:
  server_name: "${LOCAL_NAME}"
  enabled: true
  cluster_token: '${escaped_token}'
YAML
  )

  log_info "使用 preseed 加入集群（节点: ${LOCAL_NAME}, 绑定: ${bind_addr}）..."
  printf '%s\n' "$preseed" | incus admin init --preseed

  log_info "节点 ${LOCAL_NAME} 已成功加入集群"

  # 验证
  verify_cluster
}

# ----- 集群参数配置 -----
configure_cluster_params() {
  log_step "配置集群参数..."

  incus config set cluster.offline_threshold="${CLUSTER_OFFLINE_THRESHOLD}"
  log_info "cluster.offline_threshold = ${CLUSTER_OFFLINE_THRESHOLD}"

  incus config set cluster.healing_threshold="${CLUSTER_HEALING_THRESHOLD}"
  log_info "cluster.healing_threshold = ${CLUSTER_HEALING_THRESHOLD}"

  log_info "集群参数配置完成"
}

# ----- 集群验证 -----
verify_cluster() {
  log_step "验证集群状态..."

  echo ""
  incus cluster list
  echo ""

  # 检查节点状态
  local online_count
  online_count=$(incus cluster list --format json | jq '[.[] | select(.status == "Online")] | length')
  log_info "在线节点数: ${online_count}"

  local total_count
  total_count=$(incus cluster list --format json | jq 'length')
  if [[ "$online_count" -eq "$total_count" ]]; then
    log_info "所有节点均在线"
  else
    log_warn "存在离线节点（在线: ${online_count}/${total_count}）"
  fi
}

# ----- 使用帮助 -----
usage() {
  cat <<EOF
用法: $(basename "$0") [选项]

选项:
  --init              在当前节点初始化集群（首节点）
  --join [<token>]    使用 token 将当前节点加入已有集群
  -h, --help          显示此帮助信息

Token 传递方式（按安全性推荐排序）:
  1. 环境变量:  INCUS_JOIN_TOKEN=<token> $(basename "$0") --join
  2. 管道输入:  echo <token> | $(basename "$0") --join
  3. 命令参数:  $(basename "$0") --join <token>  (token 会暴露于进程列表)

示例:
  # 在 node1 上初始化:
  $(basename "$0") --init

  # 在 node1 上为 node2 生成 token:
  incus cluster add node2

  # 在 node2 上加入（推荐方式）:
  INCUS_JOIN_TOKEN=<token> $(basename "$0") --join
EOF
}

# ----- 主入口 -----
main() {
  if [[ $# -eq 0 ]]; then
    usage
    exit 1
  fi

  case "${1:-}" in
    --init)
      preflight_check
      do_init
      ;;
    --join)
      preflight_check
      do_join "${2:-}"
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      log_error "未知参数: $1"
      usage
      exit 1
      ;;
  esac
}

main "$@"
