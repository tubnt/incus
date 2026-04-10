#!/bin/bash
# ============================================================
# Incus 集群状态面板
# 用途：查看节点状态、VM 分布、IP 池使用、集群配置
# ============================================================
set -euo pipefail

# ==================== 颜色定义 ====================
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

# ==================== 帮助信息 ====================
usage() {
    cat << EOF
${CYAN}用法:${NC}
  $0 [选项]

${CYAN}选项:${NC}
  --json           JSON 格式输出
  -h, --help       显示帮助

${CYAN}示例:${NC}
  $0               # 交互式面板
  $0 --json        # JSON 输出

EOF
    exit 0
}

JSON_MODE=false

while [[ $# -gt 0 ]]; do
    case "$1" in
        --json)  JSON_MODE=true; shift ;;
        -h|--help) usage ;;
        *) echo "未知选项: $1"; usage ;;
    esac
done

# ==================== 前置检查 ====================
command -v incus >/dev/null 2>&1 || { echo "错误: incus 命令未找到"; exit 1; }

# ==================== 数据采集 ====================
CLUSTER_JSON=""
INSTANCES_JSON=""

collect_data() {
    CLUSTER_JSON=$(incus cluster list --format=json 2>/dev/null || echo '[]')
    INSTANCES_JSON=$(incus list --format=json 2>/dev/null || echo '[]')
}

# ==================== 解析 VM 资源 ====================
# 用 python3 处理 JSON，通过环境变量传数据
run_py() {
    _CLUSTER="$CLUSTER_JSON" _INSTANCES="$INSTANCES_JSON" python3 -c "
import json, os, sys
cluster = json.loads(os.environ['_CLUSTER'])
instances = json.loads(os.environ['_INSTANCES'])
$1
"
}

# 解析单个 VM 的内存配额（返回 GB 浮点数）
# python helper 函数，内嵌到 run_py 调用中
PY_PARSE_MEM='
def parse_mem_gb(cfg):
    mem_str = str(cfg.get("limits.memory", "0"))
    digits = "".join(c for c in mem_str if c.isdigit())
    if not digits: return 0
    val = int(digits)
    if "GiB" in mem_str or "GB" in mem_str: return val
    if "MiB" in mem_str or "MB" in mem_str: return val / 1024
    return 0

def node_resources(node_name):
    vms = [i for i in instances if i.get("location","") == node_name]
    vcpu = 0; mem = 0
    for vm in vms:
        cfg = vm.get("config", {})
        try: vcpu += int(cfg.get("limits.cpu","0") or 0)
        except ValueError: pass
        mem += parse_mem_gb(cfg)
    return len(vms), vcpu, mem
'

# ==================== JSON 输出 ====================
output_json() {
    collect_data

    run_py "
${PY_PARSE_MEM}

nodes = []
for node in cluster:
    name = node.get('server_name', '?')
    vm_count, vcpu, mem = node_resources(name)
    nodes.append({
        'name': name,
        'status': node.get('status', 'Unknown'),
        'url': node.get('url', ''),
        'roles': node.get('roles', []),
        'vm_count': vm_count,
        'vcpu_allocated': vcpu,
        'memory_allocated_gb': round(mem, 1),
    })

ips = set()
for i in instances:
    net = (i.get('state') or {}).get('network') or {}
    for iname, iface in net.items():
        if iname == 'lo': continue
        for addr in iface.get('addresses', []):
            if addr.get('family') == 'inet' and addr.get('scope') == 'global':
                ips.add(addr['address'])

from datetime import datetime, timezone
result = {
    'timestamp': datetime.now(timezone.utc).strftime('%Y-%m-%dT%H:%M:%SZ'),
    'nodes': nodes,
    'total_vms': len([i for i in instances if i.get('type') == 'virtual-machine']),
    'total_containers': len([i for i in instances if i.get('type') == 'container']),
    'ip_pool': {'allocated': len(ips), 'addresses': sorted(ips)},
}
print(json.dumps(result, indent=2, ensure_ascii=False))
"
}

# ==================== 人类可读输出 ====================
output_human() {
    collect_data

    echo -e "\n${BOLD}═══════════════════════════════════════════════════${NC}"
    echo -e "${BOLD}              Incus 集群状态面板${NC}"
    echo -e "${BOLD}═══════════════════════════════════════════════════${NC}"
    echo -e "  时间: $(date '+%Y-%m-%d %H:%M:%S')"

    # 节点状态 + VM 分布
    echo -e "\n${BOLD}── 集群节点 ──${NC}"
    printf "  ${CYAN}%-16s %-12s %-6s %-8s %-10s %s${NC}\n" "节点" "状态" "VM数" "vCPU" "内存" "角色"

    run_py "
${PY_PARSE_MEM}

G='\033[0;32m'; Y='\033[1;33m'; R='\033[0;31m'; N='\033[0m'
tv=0; tc=0; tm=0

for node in cluster:
    name = node.get('server_name','?')
    status = node.get('status','Unknown')
    roles = ', '.join(node.get('roles',[])) or '-'
    vm_count, vcpu, mem = node_resources(name)
    tv += vm_count; tc += vcpu; tm += mem

    if status == 'Online':     s = f'{G}● Online{N}'
    elif status == 'Evacuated': s = f'{Y}▲ Evacuated{N}'
    else:                       s = f'{R}✖ {status}{N}'

    mem_s = f'{mem:.0f}G' if mem > 0 else '-'
    vcpu_s = str(vcpu) if vcpu > 0 else '-'
    print(f'  {name:<16s} {s:<23s} {vm_count:<6d} {vcpu_s:<8s} {mem_s:<10s} {roles}')

print(f'  ────────────────────────────────────────────────────────────')
print(f'  合计{\" \"*11} {\" \"*12} {tv:<6d} {tc:<8d} {tm:.0f}G')
"

    # IP 池使用情况
    echo -e "\n${BOLD}── IP 池使用 ──${NC}"

    run_py "
G='\033[0;32m'; Y='\033[1;33m'; N='\033[0m'

ips = {}
for i in instances:
    name = i.get('name','?')
    status = i.get('status','?')
    net = (i.get('state') or {}).get('network') or {}
    for iname, iface in net.items():
        if iname == 'lo': continue
        for addr in iface.get('addresses',[]):
            if addr.get('family') == 'inet' and addr.get('scope') == 'global':
                ips[addr['address']] = {'vm': name, 'status': status}

if ips:
    print(f'  已分配: {len(ips)} 个公网 IP')
    print(f'    {\"IP\":<18s} {\"VM\":<20s} 状态')
    for ip in sorted(ips):
        info = ips[ip]
        st = info['status']
        if st == 'Running':   s = f'{G}● Running{N}'
        elif st == 'Stopped': s = f'{Y}○ Stopped{N}'
        else:                  s = st
        print(f'  {ip:<18s} {info[\"vm\"]:<20s} {s}')
else:
    print('  暂无已分配 IP')
"

    # 集群配置
    echo -e "\n${BOLD}── 集群配置 ──${NC}"
    local config_keys=(
        "cluster.offline_threshold"
        "cluster.healing_threshold"
        "cluster.max_voters"
        "cluster.max_standby"
    )
    for key in "${config_keys[@]}"; do
        local val
        val=$(incus config get "$key" 2>/dev/null || echo "（未设置）")
        [[ -z "$val" ]] && val="（默认）"
        printf "  %-35s %s\n" "$key" "$val"
    done

    # 存储池
    echo -e "\n${BOLD}── 存储池 ──${NC}"
    incus storage list --format=table 2>/dev/null | sed 's/^/  /' || echo "  无法获取存储池信息"

    echo -e "\n${BOLD}═══════════════════════════════════════════════════${NC}"
}

# ==================== 主逻辑 ====================
if $JSON_MODE; then
    output_json
else
    output_human
fi
