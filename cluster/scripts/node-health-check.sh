#!/bin/bash
# ============================================================
# 节点健康检查（单节点）
# 用途：检查 Incus、Ceph OSD、网络、磁盘、nftables 状态
# 返回健康评分 (0-100) 和问题清单
# ============================================================
set -euo pipefail

# ==================== 颜色定义 ====================
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

# ==================== 默认配置 ====================
JSON_MODE=false
DISK_WARN_PCT=80
DISK_CRIT_PCT=90

# 网络检查目标（根据 PLAN-002 网络架构）
MGMT_TARGETS=()     # 管理网检查目标，留空跳过
CEPH_PUB_TARGETS=()  # Ceph Public 网络目标
CEPH_CLU_TARGETS=()  # Ceph Cluster 网络目标

# ==================== 帮助信息 ====================
usage() {
    cat << EOF
${CYAN}用法:${NC}
  $0 [选项]

${CYAN}选项:${NC}
  --json                   JSON 格式输出
  --mgmt-targets IP,...    管理网 ping 目标（逗号分隔）
  --ceph-pub-targets IP,...  Ceph Public 网络目标
  --ceph-clu-targets IP,...  Ceph Cluster 网络目标
  --disk-warn PCT          磁盘警告水位（默认 80%）
  --disk-crit PCT          磁盘严重水位（默认 90%）
  -h, --help               显示帮助

${CYAN}示例:${NC}
  $0                                           # 基本检查
  $0 --mgmt-targets 10.0.10.1,10.0.10.2       # 指定管理网目标
  $0 --json                                    # JSON 输出

EOF
    exit 0
}

# ==================== 参数解析 ====================
while [[ $# -gt 0 ]]; do
    case "$1" in
        --json)
            JSON_MODE=true; shift ;;
        --mgmt-targets)
            IFS=',' read -ra MGMT_TARGETS <<< "$2"; shift 2 ;;
        --ceph-pub-targets)
            IFS=',' read -ra CEPH_PUB_TARGETS <<< "$2"; shift 2 ;;
        --ceph-clu-targets)
            IFS=',' read -ra CEPH_CLU_TARGETS <<< "$2"; shift 2 ;;
        --disk-warn)
            [[ "${2:-}" =~ ^[0-9]+$ ]] || { echo "错误: --disk-warn 需要整数参数"; exit 1; }
            DISK_WARN_PCT="$2"; shift 2 ;;
        --disk-crit)
            [[ "${2:-}" =~ ^[0-9]+$ ]] || { echo "错误: --disk-crit 需要整数参数"; exit 1; }
            DISK_CRIT_PCT="$2"; shift 2 ;;
        -h|--help) usage ;;
        *) echo "未知选项: $1"; usage ;;
    esac
done

# ==================== 检查结果收集 ====================
SCORE=100
ISSUES=()
CHECKS=()

# 记录检查结果
# $1=分类 $2=名称 $3=状态(ok|warn|fail) $4=描述 $5=扣分
record_check() {
    local category="$1" name="$2" status="$3" detail="$4" penalty="${5:-0}"
    CHECKS+=("${category}|${name}|${status}|${detail}")
    if [[ "$status" == "fail" ]]; then
        SCORE=$((SCORE - penalty))
        ISSUES+=("[严重] ${category}: ${detail}")
    elif [[ "$status" == "warn" ]]; then
        SCORE=$((SCORE - penalty))
        ISSUES+=("[警告] ${category}: ${detail}")
    fi
}

# ==================== 检查项 ====================

# 1. Incus 服务状态
check_incus() {
    if ! command -v incus >/dev/null 2>&1; then
        record_check "Incus" "服务安装" "fail" "incus 命令未找到" 30
        return
    fi

    if systemctl is-active --quiet incus 2>/dev/null; then
        record_check "Incus" "服务运行" "ok" "incus 服务运行中"
    else
        record_check "Incus" "服务运行" "fail" "incus 服务未运行" 30
        return
    fi

    # 集群成员状态
    local member_status
    member_status=$(incus cluster list --format=json 2>/dev/null | python3 -c "
import json, sys, os
hostname = os.uname().nodename
data = json.loads(sys.stdin.read())
for node in data:
    if node.get('server_name','') == hostname:
        print(node.get('status','Unknown'))
        break
else:
    print('NotFound')
" 2>/dev/null || echo "Error")

    case "$member_status" in
        Online)
            record_check "Incus" "集群成员" "ok" "本节点集群状态: Online" ;;
        Evacuated)
            record_check "Incus" "集群成员" "warn" "本节点处于疏散状态" 10 ;;
        NotFound)
            record_check "Incus" "集群成员" "warn" "本节点不在集群中（可能是单机模式）" 0 ;;
        *)
            record_check "Incus" "集群成员" "fail" "本节点集群状态异常: ${member_status}" 20 ;;
    esac
}

# 2. Ceph OSD 状态（本节点）
check_ceph_osd() {
    if ! command -v ceph >/dev/null 2>&1; then
        record_check "Ceph" "命令" "warn" "ceph 命令未找到（可能非 Ceph 节点）" 0
        return
    fi

    local hostname
    hostname=$(hostname -s)

    # 获取本节点的 OSD 列表
    local osd_ids
    osd_ids=$(ceph osd tree --format=json 2>/dev/null | _NODE_HOSTNAME="$hostname" python3 -c "
import json, sys, os
data = json.loads(sys.stdin.read())
nodes = {n['id']: n for n in data.get('nodes', [])}
hostname = os.environ.get('_NODE_HOSTNAME', '')

# 找到本 host 节点
host_id = None
for n in data.get('nodes', []):
    if n.get('type') == 'host' and n.get('name') == hostname:
        host_id = n['id']
        break

if host_id is None:
    sys.exit(0)

# 列出子 OSD
host_node = nodes[host_id]
for child_id in host_node.get('children', []):
    child = nodes.get(child_id)
    if child and child.get('type') == 'osd':
        status = child.get('status', 'unknown')
        print(f'{child_id} {status}')
" 2>/dev/null || true)

    if [[ -z "$osd_ids" ]]; then
        record_check "Ceph" "OSD" "warn" "本节点无 OSD（可能未部署 Ceph 存储）" 0
        return
    fi

    local total=0 up=0 down=0
    while IFS=' ' read -r oid status; do
        ((total++)) || true
        if [[ "$status" == "up" ]]; then
            ((up++)) || true
        else
            ((down++)) || true
        fi
    done <<< "$osd_ids"

    if [[ "$down" -eq 0 ]]; then
        record_check "Ceph" "OSD 状态" "ok" "本节点 ${total} 个 OSD 全部 up"
    else
        record_check "Ceph" "OSD 状态" "fail" "本节点 ${down}/${total} 个 OSD down" $((down * 15))
    fi
}

# 3. 网络连通性
check_network() {
    local category="$1" label="$2"
    shift 2
    local targets=("$@")

    if [[ ${#targets[@]} -eq 0 ]]; then
        record_check "网络" "${label}" "ok" "${label}: 未配置检查目标，跳过"
        return
    fi

    local fail_count=0 total=${#targets[@]}
    local fail_list=""

    for target in "${targets[@]}"; do
        if ! ping -c 1 -W 2 "$target" >/dev/null 2>&1; then
            ((fail_count++)) || true
            fail_list="${fail_list} ${target}"
        fi
    done

    if [[ "$fail_count" -eq 0 ]]; then
        record_check "网络" "${label}" "ok" "${label}: ${total} 个目标全部可达"
    elif [[ "$fail_count" -lt "$total" ]]; then
        record_check "网络" "${label}" "warn" "${label}: ${fail_count}/${total} 不可达:${fail_list}" 10
    else
        record_check "网络" "${label}" "fail" "${label}: 全部 ${total} 个目标不可达" 20
    fi
}

# 4. 磁盘空间（系统盘）
check_disk() {
    local root_usage
    root_usage=$(df / --output=pcent 2>/dev/null | tail -1 | tr -d ' %')

    if [[ -z "$root_usage" ]]; then
        record_check "磁盘" "系统盘" "warn" "无法获取磁盘使用率" 5
        return
    fi

    if [[ "$root_usage" -ge "$DISK_CRIT_PCT" ]]; then
        record_check "磁盘" "系统盘" "fail" "系统盘使用率 ${root_usage}% (>=${DISK_CRIT_PCT}%)" 20
    elif [[ "$root_usage" -ge "$DISK_WARN_PCT" ]]; then
        record_check "磁盘" "系统盘" "warn" "系统盘使用率 ${root_usage}% (>=${DISK_WARN_PCT}%)" 10
    else
        record_check "磁盘" "系统盘" "ok" "系统盘使用率 ${root_usage}%"
    fi

    # 检查 /var（日志和 Ceph 数据常在此处）
    local var_usage
    var_usage=$(df /var --output=pcent 2>/dev/null | tail -1 | tr -d ' %')
    if [[ -n "$var_usage" ]] && [[ "$var_usage" -ge "$DISK_CRIT_PCT" ]]; then
        record_check "磁盘" "/var 分区" "fail" "/var 使用率 ${var_usage}% (>=${DISK_CRIT_PCT}%)" 15
    elif [[ -n "$var_usage" ]] && [[ "$var_usage" -ge "$DISK_WARN_PCT" ]]; then
        record_check "磁盘" "/var 分区" "warn" "/var 使用率 ${var_usage}% (>=${DISK_WARN_PCT}%)" 5
    fi
}

# 5. nftables 规则
check_nftables() {
    if ! command -v nft >/dev/null 2>&1; then
        record_check "防火墙" "nftables" "warn" "nft 命令未找到" 5
        return
    fi

    local ruleset_lines
    ruleset_lines=$(nft list ruleset 2>/dev/null | wc -l || echo 0)

    if [[ "$ruleset_lines" -le 1 ]]; then
        record_check "防火墙" "nftables 规则" "fail" "nftables 规则集为空，防火墙未加载" 15
    else
        record_check "防火墙" "nftables 规则" "ok" "nftables 已加载 (${ruleset_lines} 行规则)"
    fi

    # 检查 vm_filter 表是否存在（集群版防火墙）
    if nft list table bridge vm_filter >/dev/null 2>&1; then
        record_check "防火墙" "vm_filter 表" "ok" "bridge vm_filter 表已加载"
    else
        record_check "防火墙" "vm_filter 表" "warn" "bridge vm_filter 表未找到（集群防火墙可能未配置）" 5
    fi
}

# ==================== 执行所有检查 ====================
run_all_checks() {
    check_incus
    check_ceph_osd
    check_network "管理网" "管理网络" "${MGMT_TARGETS[@]+"${MGMT_TARGETS[@]}"}"
    check_network "Ceph Public" "Ceph Public" "${CEPH_PUB_TARGETS[@]+"${CEPH_PUB_TARGETS[@]}"}"
    check_network "Ceph Cluster" "Ceph Cluster" "${CEPH_CLU_TARGETS[@]+"${CEPH_CLU_TARGETS[@]}"}"
    check_disk
    check_nftables

    # 确保分数不低于 0
    [[ $SCORE -lt 0 ]] && SCORE=0
}

# ==================== JSON 输出 ====================
output_json() {
    run_all_checks

    # 通过环境变量传递数据，用 python3 json.dumps 安全生成 JSON
    local checks_str=""
    for check in "${CHECKS[@]}"; do
        checks_str+="${check}"$'\n'
    done

    local issues_str=""
    for issue in "${ISSUES[@]+"${ISSUES[@]}"}"; do
        issues_str+="${issue}"$'\n'
    done

    _CHECKS="$checks_str" _ISSUES="$issues_str" _SCORE="$SCORE" \
    _HOSTNAME="$(hostname)" _STATUS="$(score_to_status $SCORE)" \
    python3 -c "
import json, os
from datetime import datetime, timezone

checks = []
for line in os.environ.get('_CHECKS', '').strip().splitlines():
    if not line: continue
    parts = line.split('|', 3)
    if len(parts) == 4:
        checks.append({
            'category': parts[0],
            'name': parts[1],
            'status': parts[2],
            'detail': parts[3],
        })

issues = [l for l in os.environ.get('_ISSUES', '').strip().splitlines() if l]

result = {
    'timestamp': datetime.now(timezone.utc).strftime('%Y-%m-%dT%H:%M:%SZ'),
    'hostname': os.environ.get('_HOSTNAME', ''),
    'score': int(os.environ.get('_SCORE', '0')),
    'status': os.environ.get('_STATUS', ''),
    'checks': checks,
    'issues': issues,
}
print(json.dumps(result, indent=2, ensure_ascii=False))
"
}

# ==================== 评分转状态 ====================
score_to_status() {
    local score=$1
    if [[ $score -ge 90 ]]; then echo "健康"
    elif [[ $score -ge 70 ]]; then echo "警告"
    elif [[ $score -ge 50 ]]; then echo "异常"
    else echo "严重"
    fi
}

# ==================== 人类可读输出 ====================
output_human() {
    run_all_checks

    echo -e "\n${BOLD}═══════════════════════════════════════════════════${NC}"
    echo -e "${BOLD}              节点健康检查报告${NC}"
    echo -e "${BOLD}═══════════════════════════════════════════════════${NC}"
    echo -e "  主机: $(hostname)"
    echo -e "  时间: $(date '+%Y-%m-%d %H:%M:%S')"

    # 健康评分
    echo -e "\n${BOLD}── 健康评分 ──${NC}"
    local score_color="$GREEN"
    [[ $SCORE -lt 90 ]] && score_color="$YELLOW"
    [[ $SCORE -lt 70 ]] && score_color="$RED"

    local status_text
    status_text=$(score_to_status $SCORE)
    echo -e "  评分: ${score_color}${BOLD}${SCORE}/100${NC} (${status_text})"

    # 进度条
    local bar_len=40
    local filled=$((SCORE * bar_len / 100))
    local empty=$((bar_len - filled))
    local bar="${score_color}"
    for ((i=0; i<filled; i++)); do bar+="█"; done
    for ((i=0; i<empty; i++)); do bar+="░"; done
    bar+="${NC}"
    echo -e "  ${bar}"

    # 检查详情
    echo -e "\n${BOLD}── 检查详情 ──${NC}"
    for check in "${CHECKS[@]}"; do
        IFS='|' read -r category name status detail <<< "$check"
        case "$status" in
            ok)   echo -e "  ${GREEN}✓${NC} ${detail}" ;;
            warn) echo -e "  ${YELLOW}▲${NC} ${detail}" ;;
            fail) echo -e "  ${RED}✖${NC} ${detail}" ;;
        esac
    done

    # 问题清单
    if [[ ${#ISSUES[@]} -gt 0 ]]; then
        echo -e "\n${BOLD}── 问题清单 ──${NC}"
        for issue in "${ISSUES[@]}"; do
            echo -e "  ${RED}•${NC} ${issue}"
        done
    fi

    echo -e "\n${BOLD}═══════════════════════════════════════════════════${NC}"

    # 返回非零退出码表示有问题
    [[ $SCORE -lt 70 ]] && exit 1 || exit 0
}

# ==================== 主逻辑 ====================
if $JSON_MODE; then
    output_json
else
    output_human
fi
