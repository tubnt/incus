#!/bin/bash
# ============================================================
# 自愈 Webhook 服务
# 用途：接收 Alertmanager webhook，根据告警类型执行自愈动作
# 依赖：socat, jq, systemctl, ceph (按需)
# ============================================================
set -euo pipefail

# ==================== 配置 ====================
LISTEN_PORT="${WEBHOOK_PORT:-9095}"
LISTEN_ADDR="${WEBHOOK_BIND:-127.0.0.1}"  # 绑定地址，默认仅本地
AUDIT_LOG="/var/log/self-healing/audit.log"
ALLOWED_IPS="${ALLOWED_IPS:-127.0.0.1}"  # 逗号分隔，Alertmanager IP 白名单
MAX_BODY_SIZE=1048576  # 最大请求体 1MB
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

# ==================== 工具函数 ====================
log() {
    local level="$1"; shift
    local ts
    ts=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
    echo "${ts} [${level}] $*" | tee -a "${AUDIT_LOG}"
}

audit() {
    local action="$1" alert="$2" result="$3" detail="${4:-}"
    local ts
    ts=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
    jq -n -c \
        --arg time "${ts}" \
        --arg action "${action}" \
        --arg alert "${alert}" \
        --arg result "${result}" \
        --arg detail "${detail}" \
        '{time:$time,action:$action,alert:$alert,result:$result,detail:$detail}' \
        >> "${AUDIT_LOG}"
}

check_ip() {
    local client_ip="$1"
    local allowed
    IFS=',' read -ra allowed <<< "${ALLOWED_IPS}"
    for ip in "${allowed[@]}"; do
        ip=$(echo "${ip}" | tr -d ' ')
        [[ "${client_ip}" == "${ip}" ]] && return 0
    done
    return 1
}

# ==================== 自愈动作 ====================

# OSD 进程崩溃 → 重启对应 OSD 服务
heal_osd_crash() {
    local osd_id="$1"
    if [[ ! "${osd_id}" =~ ^[0-9]+$ ]]; then
        log "ERROR" "无效的 OSD ID: ${osd_id}"
        audit "osd_restart" "CephOSDDown" "failed" "无效 OSD ID: ${osd_id}"
        return 1
    fi
    log "INFO" "正在重启 ceph-osd@${osd_id}"
    if systemctl restart "ceph-osd@${osd_id}"; then
        audit "osd_restart" "CephOSDDown" "success" "osd.${osd_id} 已重启"
    else
        audit "osd_restart" "CephOSDDown" "failed" "osd.${osd_id} 重启失败"
        return 1
    fi
}

# PG 降级 >5min → 执行 pg repair
heal_pg_degraded() {
    local pg_id="${1:-}"
    log "INFO" "正在修复降级 PG"
    if [[ -n "${pg_id}" && "${pg_id}" =~ ^[0-9]+\.[0-9a-f]+$ ]]; then
        if ceph pg repair "${pg_id}"; then
            audit "pg_repair" "CephPGDegraded" "success" "pg ${pg_id} 已提交修复"
        else
            audit "pg_repair" "CephPGDegraded" "failed" "pg ${pg_id} 修复命令失败"
            return 1
        fi
    else
        log "WARN" "未指定有效 PG ID，跳过自动修复"
        audit "pg_repair" "CephPGDegraded" "skipped" "无有效 PG ID"
    fi
}

# 宿主机磁盘 >85% → 清理 journald + apt 缓存
heal_disk_full() {
    local node="${1:-$(hostname)}"
    log "INFO" "节点 ${node} 磁盘空间不足，执行清理"
    if "${SCRIPT_DIR}/cleanup-disk.sh"; then
        audit "disk_cleanup" "HostDiskFull" "success" "节点 ${node} 清理完成"
    else
        audit "disk_cleanup" "HostDiskFull" "failed" "节点 ${node} 清理失败"
        return 1
    fi
}

# Incus 节点离线 → 仅记录（Incus 内置 auto-healing 处理冷启动）
heal_incus_offline() {
    local node="${1:-unknown}"
    log "INFO" "Incus 节点 ${node} 离线，Incus 内置 auto-healing 将处理 VM 冷启动"
    audit "incus_offline_log" "IncusNodeOffline" "logged" "节点 ${node}，依赖 Incus 内置自愈"
}

# ==================== 请求处理 ====================

handle_request() {
    local body="$1" client_ip="$2"

    # IP 白名单检查
    if ! check_ip "${client_ip}"; then
        log "WARN" "拒绝来自 ${client_ip} 的请求"
        echo "HTTP/1.1 403 Forbidden"
        echo ""
        return
    fi

    # 解析 Alertmanager JSON
    local alert_count
    alert_count=$(echo "${body}" | jq -r '.alerts | length' 2>/dev/null) || {
        log "ERROR" "JSON 解析失败"
        echo "HTTP/1.1 400 Bad Request"
        echo ""
        return
    }

    log "INFO" "收到 ${alert_count} 条告警，来源: ${client_ip}"

    local i=0
    while [ "${i}" -lt "${alert_count}" ]; do
        local alert_name alert_status instance osd_id pg_id
        alert_name=$(echo "${body}" | jq -r ".alerts[${i}].labels.alertname // \"unknown\"")
        alert_status=$(echo "${body}" | jq -r ".alerts[${i}].status // \"unknown\"")
        instance=$(echo "${body}" | jq -r ".alerts[${i}].labels.instance // \"unknown\"")

        log "INFO" "处理告警: ${alert_name} (状态: ${alert_status}, 实例: ${instance})"

        # 仅处理 firing 状态的告警
        if [[ "${alert_status}" != "firing" ]]; then
            log "INFO" "告警 ${alert_name} 状态为 ${alert_status}，跳过"
            i=$((i + 1))
            continue
        fi

        case "${alert_name}" in
            CephOSDDown|ceph_osd_down)
                osd_id=$(echo "${body}" | jq -r ".alerts[${i}].labels.ceph_daemon // \"\"" | grep -oP '\d+$' || true)
                if [[ -n "${osd_id}" ]]; then
                    heal_osd_crash "${osd_id}" || true
                else
                    log "WARN" "无法从告警中提取 OSD ID"
                    audit "osd_restart" "CephOSDDown" "skipped" "无法提取 OSD ID"
                fi
                ;;
            CephPGDegraded|ceph_pg_degraded)
                pg_id=$(echo "${body}" | jq -r ".alerts[${i}].labels.pg_id // \"\"")
                heal_pg_degraded "${pg_id}" || true
                ;;
            HostDiskFull|node_disk_full|NodeFilesystemSpaceFillingUp)
                heal_disk_full "${instance}" || true
                ;;
            IncusNodeOffline|incus_node_offline)
                heal_incus_offline "${instance}" || true
                ;;
            *)
                log "INFO" "未知告警类型: ${alert_name}，跳过"
                audit "unknown" "${alert_name}" "skipped" "无匹配自愈动作"
                ;;
        esac

        i=$((i + 1))
    done

    echo "HTTP/1.1 200 OK"
    echo "Content-Type: application/json"
    echo ""
    echo '{"status":"ok"}'
}

# ==================== HTTP 服务 ====================

serve_one() {
    # 读取 HTTP 请求
    local method path version
    read -r method path version || return 1

    # 读取 headers
    local content_length=0 line
    while IFS= read -r line; do
        line="${line%%$'\r'}"
        [[ -z "${line}" ]] && break
        if [[ "${line}" =~ ^[Cc]ontent-[Ll]ength:\ *([0-9]+) ]]; then
            content_length="${BASH_REMATCH[1]}"
        fi
    done

    # 读取 body（限制大小防 OOM）
    local body=""
    if [[ "${content_length}" -gt "${MAX_BODY_SIZE}" ]]; then
        echo "HTTP/1.1 413 Payload Too Large"
        echo ""
        return
    fi
    if [[ "${content_length}" -gt 0 ]]; then
        body=$(head -c "${content_length}")
    fi

    # 仅接受 POST /webhook
    if [[ "${method}" != "POST" || "${path}" != "/webhook" ]]; then
        echo "HTTP/1.1 404 Not Found"
        echo ""
        return
    fi

    handle_request "${body}" "${SOCAT_PEERADDR:-127.0.0.1}"
}

# ==================== 主入口 ====================

main() {
    mkdir -p "$(dirname "${AUDIT_LOG}")"
    log "INFO" "自愈 webhook 服务启动，监听端口 ${LISTEN_PORT}"
    log "INFO" "允许的 IP: ${ALLOWED_IPS}"

    exec socat "TCP-LISTEN:${LISTEN_PORT},bind=${LISTEN_ADDR},reuseaddr,fork" \
        SYSTEM:"bash \"$0\" --serve-one"
}

if [[ "${1:-}" == "--serve-one" ]]; then
    serve_one
else
    main "$@"
fi
