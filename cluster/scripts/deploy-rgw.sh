#!/usr/bin/env bash
set -euo pipefail

# ============================================================
# deploy-rgw.sh — Ceph RGW (RADOS Gateway) 部署脚本
# 功能：通过 cephadm 部署 RGW 服务，配置 S3 endpoint，创建管理用户
# ============================================================

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/../configs/cluster-env.sh"

# ---------- 默认参数 ----------
RGW_REALM="${RGW_REALM:-default}"
RGW_ZONEGROUP="${RGW_ZONEGROUP:-default}"
RGW_ZONE="${RGW_ZONE:-default}"
RGW_PORT="${RGW_PORT:-7480}"
RGW_SSL_PORT="${RGW_SSL_PORT:-7443}"
RGW_ADMIN_USER="${RGW_ADMIN_USER:-admin}"
RGW_ADMIN_DISPLAY="${RGW_ADMIN_DISPLAY:-RGW Admin}"
RGW_SERVICE_NAME="${RGW_SERVICE_NAME:-rgw.default}"
RGW_SSL_CERT="${RGW_SSL_CERT:-/etc/ceph/rgw.pem}"
RGW_PLACEMENT="${RGW_PLACEMENT:-3}"

# ---------- 日志工具 ----------
log_info()  { echo "[INFO]  $(date '+%Y-%m-%d %H:%M:%S') $*"; }
log_warn()  { echo "[WARN]  $(date '+%Y-%m-%d %H:%M:%S') $*" >&2; }
log_error() { echo "[ERROR] $(date '+%Y-%m-%d %H:%M:%S') $*" >&2; }

# ---------- 前置检查 ----------
preflight_check() {
    log_info "执行前置检查..."

    if ! command -v cephadm &>/dev/null; then
        log_error "cephadm 未安装，请先安装 cephadm"
        exit 1
    fi

    if ! ceph status &>/dev/null; then
        log_error "无法连接 Ceph 集群，请检查配置"
        exit 1
    fi

    if ! command -v radosgw-admin &>/dev/null; then
        log_error "radosgw-admin 未安装"
        exit 1
    fi

    log_info "前置检查通过"
}

# ---------- 创建 Realm / Zonegroup / Zone ----------
setup_multisite() {
    log_info "配置 RGW 多站点结构 (Realm: ${RGW_REALM}, Zone: ${RGW_ZONE})..."

    # 创建 realm（如已存在则跳过）
    if ! radosgw-admin realm get --rgw-realm="${RGW_REALM}" &>/dev/null; then
        radosgw-admin realm create --rgw-realm="${RGW_REALM}" --default
        log_info "Realm '${RGW_REALM}' 已创建"
    else
        log_info "Realm '${RGW_REALM}' 已存在，跳过"
    fi

    # 创建 zonegroup
    if ! radosgw-admin zonegroup get --rgw-zonegroup="${RGW_ZONEGROUP}" &>/dev/null; then
        radosgw-admin zonegroup create --rgw-zonegroup="${RGW_ZONEGROUP}" \
            --rgw-realm="${RGW_REALM}" --master --default \
            --endpoints="https://${S3_ENDPOINT:-localhost}:${RGW_SSL_PORT}"
        log_info "Zonegroup '${RGW_ZONEGROUP}' 已创建"
    else
        log_info "Zonegroup '${RGW_ZONEGROUP}' 已存在，跳过"
    fi

    # 创建 zone
    if ! radosgw-admin zone get --rgw-zone="${RGW_ZONE}" &>/dev/null; then
        radosgw-admin zone create --rgw-zone="${RGW_ZONE}" \
            --rgw-zonegroup="${RGW_ZONEGROUP}" --rgw-realm="${RGW_REALM}" \
            --master --default \
            --endpoints="https://${S3_ENDPOINT:-localhost}:${RGW_SSL_PORT}"
        log_info "Zone '${RGW_ZONE}' 已创建"
    else
        log_info "Zone '${RGW_ZONE}' 已存在，跳过"
    fi

    # 提交 period
    radosgw-admin period update --commit
    log_info "多站点配置已提交"
}

# ---------- 配置 HTTPS ----------
setup_https() {
    log_info "配置 RGW HTTPS..."

    if [[ ! -f "${RGW_SSL_CERT}" ]]; then
        log_warn "SSL 证书文件 ${RGW_SSL_CERT} 不存在，生成自签名证书..."
        local tmp_key tmp_cert
        tmp_key=$(mktemp /tmp/rgw-key.XXXXXX.pem)
        tmp_cert=$(mktemp /tmp/rgw-cert.XXXXXX.pem)
        chmod 600 "${tmp_key}" "${tmp_cert}"
        openssl req -x509 -nodes -days 3650 \
            -newkey rsa:2048 \
            -keyout "${tmp_key}" \
            -out "${tmp_cert}" \
            -subj "/CN=${S3_ENDPOINT:-localhost}"
        cat "${tmp_cert}" "${tmp_key}" > "${RGW_SSL_CERT}"
        chmod 600 "${RGW_SSL_CERT}"
        rm -f "${tmp_key}" "${tmp_cert}"
        log_info "自签名证书已生成: ${RGW_SSL_CERT}"
    fi

    # 将证书导入 Ceph 配置
    ceph config set client.rgw rgw_frontends \
        "beast port=${RGW_PORT} ssl_port=${RGW_SSL_PORT} ssl_certificate=${RGW_SSL_CERT}"

    log_info "HTTPS 配置完成 (端口: ${RGW_SSL_PORT})"
}

# ---------- 通过 cephadm 部署 RGW 服务 ----------
deploy_rgw_service() {
    log_info "通过 cephadm 部署 RGW 服务..."

    # 生成服务规格文件
    local spec_file="/tmp/rgw-spec.yaml"
    cat > "${spec_file}" <<SPEC
service_type: rgw
service_id: ${RGW_SERVICE_NAME}
placement:
  count: ${RGW_PLACEMENT}
spec:
  rgw_realm: ${RGW_REALM}
  rgw_zone: ${RGW_ZONE}
  rgw_frontend_port: ${RGW_PORT}
  ssl: true
SPEC

    ceph orch apply -i "${spec_file}"
    rm -f "${spec_file}"

    log_info "等待 RGW 服务启动..."
    local retries=30
    while (( retries > 0 )); do
        if ceph orch ls --service-name="rgw.${RGW_SERVICE_NAME}" --format=json | \
           python3 -c "import sys,json; d=json.load(sys.stdin); exit(0 if d and d[0].get('status',{}).get('running',0)>0 else 1)" 2>/dev/null; then
            log_info "RGW 服务已启动"
            return 0
        fi
        log_info "等待 RGW 启动... (剩余 ${retries} 次)"
        sleep 10
        (( retries-- ))
    done

    log_error "RGW 服务启动超时"
    exit 1
}

# ---------- 创建管理用户 ----------
create_admin_user() {
    log_info "创建 RGW 管理用户: ${RGW_ADMIN_USER}..."

    if radosgw-admin user info --uid="${RGW_ADMIN_USER}" &>/dev/null; then
        log_info "管理用户 '${RGW_ADMIN_USER}' 已存在，跳过创建"
    else
        radosgw-admin user create \
            --uid="${RGW_ADMIN_USER}" \
            --display-name="${RGW_ADMIN_DISPLAY}" \
            --caps="buckets=*;users=*;usage=read;metadata=read;zone=read" \
            --admin
        log_info "管理用户创建完成"
    fi

    # 输出凭据信息
    local user_info
    user_info=$(radosgw-admin user info --uid="${RGW_ADMIN_USER}")
    local access_key secret_key
    access_key=$(echo "${user_info}" | python3 -c "import sys,json; print(json.load(sys.stdin)['keys'][0]['access_key'])")
    secret_key=$(echo "${user_info}" | python3 -c "import sys,json; print(json.load(sys.stdin)['keys'][0]['secret_key'])")

    # 凭据写入受限权限文件，不打印到日志/终端
    local cred_file="/etc/ceph/rgw-admin-credentials"
    (umask 077 && cat > "${cred_file}" <<CRED
RGW_ADMIN_ACCESS_KEY=${access_key}
RGW_ADMIN_SECRET_KEY=${secret_key}
CRED
    )
    log_info "管理用户凭据已保存到 ${cred_file}（权限 0600）"
    log_warn "请妥善保管凭据文件，不要泄露！"
}

# ---------- 验证 S3 连通性 ----------
verify_s3() {
    log_info "验证 S3 连通性..."

    local endpoint="https://${S3_ENDPOINT:-localhost}:${RGW_SSL_PORT}"

    # 获取管理员 access key
    local user_info access_key secret_key
    user_info=$(radosgw-admin user info --uid="${RGW_ADMIN_USER}")
    access_key=$(echo "${user_info}" | python3 -c "import sys,json; print(json.load(sys.stdin)['keys'][0]['access_key'])")
    secret_key=$(echo "${user_info}" | python3 -c "import sys,json; print(json.load(sys.stdin)['keys'][0]['secret_key'])")

    # 使用 aws cli 测试（如果可用）
    if command -v aws &>/dev/null; then
        local test_bucket="s3-connectivity-test-$(date +%s)"
        AWS_ACCESS_KEY_ID="${access_key}" \
        AWS_SECRET_ACCESS_KEY="${secret_key}" \
        aws s3 mb "s3://${test_bucket}" \
            --endpoint-url="${endpoint}" \
            --no-verify-ssl 2>/dev/null

        AWS_ACCESS_KEY_ID="${access_key}" \
        AWS_SECRET_ACCESS_KEY="${secret_key}" \
        aws s3 ls \
            --endpoint-url="${endpoint}" \
            --no-verify-ssl 2>/dev/null

        AWS_ACCESS_KEY_ID="${access_key}" \
        AWS_SECRET_ACCESS_KEY="${secret_key}" \
        aws s3 rb "s3://${test_bucket}" \
            --endpoint-url="${endpoint}" \
            --no-verify-ssl 2>/dev/null

        log_info "S3 连通性验证通过 (aws cli)"
    else
        # 回退到 curl 测试
        local http_code
        http_code=$(curl -sk -o /dev/null -w "%{http_code}" "${endpoint}")
        if [[ "${http_code}" == "200" || "${http_code}" == "403" ]]; then
            log_info "S3 endpoint 可达 (HTTP ${http_code})"
        else
            log_error "S3 endpoint 不可达 (HTTP ${http_code})"
            exit 1
        fi
    fi
}

# ---------- 主流程 ----------
main() {
    log_info "========== Ceph RGW 部署开始 =========="

    preflight_check
    setup_multisite
    setup_https
    deploy_rgw_service
    create_admin_user
    verify_s3

    log_info "========== Ceph RGW 部署完成 =========="
    log_info "S3 Endpoint: https://${S3_ENDPOINT:-localhost}:${RGW_SSL_PORT}"
}

main "$@"
