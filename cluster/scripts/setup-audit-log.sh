#!/bin/bash
# ============================================================
# 审计日志配置脚本
# 用途：配置 Incus lifecycle 事件 + SSH 操作审计日志收集链路
# 链路：incus monitor → journald → Promtail → Loki
# ============================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SYSTEMD_DIR="/etc/systemd/system"
PROMTAIL_CONFIG_DIR="/etc/promtail"

log() {
    echo "[$(date -u '+%Y-%m-%dT%H:%M:%SZ')] $*"
}

err() {
    log "ERROR: $*" >&2
    exit 1
}

# ==================== Incus Lifecycle 监控服务 ====================

setup_lifecycle_monitor() {
    log "配置 Incus lifecycle 监控服务"

    local unit_src="${SCRIPT_DIR}/../configs/systemd/incus-lifecycle-monitor.service"
    local unit_dst="${SYSTEMD_DIR}/incus-lifecycle-monitor.service"

    if [[ ! -f "${unit_src}" ]]; then
        err "找不到 systemd unit 文件: ${unit_src}"
    fi

    cp "${unit_src}" "${unit_dst}"
    systemctl daemon-reload
    systemctl enable --now incus-lifecycle-monitor.service
    log "incus-lifecycle-monitor.service 已启用并启动"
}

# ==================== SSH 审计配置 ====================

setup_ssh_audit() {
    log "配置 SSH 操作审计"

    # 确保 sshd 日志级别足够记录操作
    local sshd_config="/etc/ssh/sshd_config"
    if grep -q "^LogLevel" "${sshd_config}" 2>/dev/null; then
        if ! grep -q "^LogLevel VERBOSE" "${sshd_config}"; then
            log "更新 sshd LogLevel 为 VERBOSE"
            sed -i 's/^LogLevel.*/LogLevel VERBOSE/' "${sshd_config}"
            systemctl reload sshd 2>/dev/null || systemctl reload ssh 2>/dev/null || true
        fi
    else
        log "添加 sshd LogLevel VERBOSE"
        echo "LogLevel VERBOSE" >> "${sshd_config}"
        systemctl reload sshd 2>/dev/null || systemctl reload ssh 2>/dev/null || true
    fi

    # 确保 pam_exec 或 auditd 记录 SSH 会话命令（如已安装 auditd）
    if command -v auditctl &>/dev/null; then
        log "配置 auditd 规则记录管理员命令"
        local audit_rule="-a always,exit -F arch=b64 -S execve -F uid=0 -k admin_cmd"
        if ! auditctl -l 2>/dev/null | grep -q "admin_cmd"; then
            auditctl ${audit_rule} 2>/dev/null || true
            # 持久化规则（幂等：仅在规则不存在时写入）
            local rules_file="/etc/audit/rules.d/admin-commands.rules"
            if [[ ! -f "${rules_file}" ]] || ! grep -qF "admin_cmd" "${rules_file}" 2>/dev/null; then
                echo "${audit_rule}" >> "${rules_file}" 2>/dev/null || true
            fi
            log "auditd 规则已添加"
        else
            log "auditd admin_cmd 规则已存在"
        fi
    else
        log "auditd 未安装，SSH 操作依赖 journald auth 日志收集"
    fi

    log "SSH 审计配置完成"
}

# ==================== Promtail 审计日志 Job ====================

setup_promtail_audit_job() {
    log "配置 Promtail 审计日志采集"

    mkdir -p "${PROMTAIL_CONFIG_DIR}"

    local promtail_snippet="${PROMTAIL_CONFIG_DIR}/audit-scrape.yml"

    cat > "${promtail_snippet}" << 'YAML'
# 审计日志采集配置片段 — 合并到 Promtail 主配置的 scrape_configs 中
# Incus lifecycle 事件（通过 incus-lifecycle-monitor.service 写入 journald）
- job_name: incus-lifecycle
  journal:
    matches: _SYSTEMD_UNIT=incus-lifecycle-monitor.service
    labels:
      job: incus-lifecycle
      __path__: ""
  relabel_configs:
    - source_labels: ['__journal__hostname']
      target_label: node

# SSH 登录/操作审计（auth 日志）
- job_name: ssh-audit
  journal:
    matches: _COMM=sshd
    labels:
      job: ssh-audit
      __path__: ""
  relabel_configs:
    - source_labels: ['__journal__hostname']
      target_label: node

# auditd 管理员命令审计（如已启用）
- job_name: admin-commands
  journal:
    matches: _TRANSPORT=audit
    labels:
      job: admin-audit
      __path__: ""
  relabel_configs:
    - source_labels: ['__journal__hostname']
      target_label: node
YAML

    log "Promtail 审计采集配置已写入: ${promtail_snippet}"
    log "请将其内容合并到 Promtail 主配置的 scrape_configs 段"
}

# ==================== 主流程 ====================

main() {
    log "========== 审计日志配置开始 =========="

    [[ "$(id -u)" -eq 0 ]] || err "需要 root 权限运行"

    setup_lifecycle_monitor
    setup_ssh_audit
    setup_promtail_audit_job

    log "========== 审计日志配置完成 =========="
    log ""
    log "审计链路概览:"
    log "  1. Incus lifecycle → journald (incus-lifecycle-monitor.service)"
    log "  2. SSH 操作 → journald (sshd VERBOSE + auditd)"
    log "  3. journald → Promtail → Loki (audit-scrape.yml)"
}

main "$@"
