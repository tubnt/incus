#!/bin/bash
# setup-console-cert.sh — 签发 Console 代理独立证书（最小权限）
# Console 证书独立于 Paymenter 主证书，仅用于实例 console 操作
set -euo pipefail

# ── 颜色 ──
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; NC='\033[0m'
log()  { echo -e "${GREEN}[INFO]${NC} $*"; }
warn() { echo -e "${YELLOW}[WARN]${NC} $*"; }
err()  { echo -e "${RED}[ERR]${NC}  $*" >&2; exit 1; }

# ── 默认配置 ──
OUTPUT_DIR="."
CERT_CN="console-proxy"
CERT_DAYS=3650
PROJECT_NAME="customers"

usage() {
    cat <<EOF
${CYAN}setup-console-cert.sh${NC} — 签发 Console 代理独立证书

Console 证书独立于 Paymenter 主证书，遵循最小权限原则：
  - 受限到 customers project
  - 仅用于实例 console/exec 操作
  - 不可创建/删除/修改实例

用法:
  $(basename "$0") [选项]

选项:
  --output-dir <dir>   证书输出目录（默认: 当前目录）
  --cn <name>          证书 CN 名称（默认: ${CERT_CN}）
  --days <days>        证书有效期天数（默认: ${CERT_DAYS}）
  --project <name>     绑定的 Project（默认: ${PROJECT_NAME}）
  --help               显示此帮助信息

生成文件:
  <cn>.key             私钥（EC secp384r1）
  <cn>.crt             自签名证书（SHA-384）

示例:
  $(basename "$0") --output-dir /etc/incus/certs
  $(basename "$0") --cn console-proxy --days 365
EOF
    exit 0
}

# ── 参数解析 ──
while [[ $# -gt 0 ]]; do
    case "$1" in
        --output-dir) OUTPUT_DIR="$2";    shift 2 ;;
        --cn)         CERT_CN="$2";       shift 2 ;;
        --days)       CERT_DAYS="$2";     shift 2 ;;
        --project)    PROJECT_NAME="$2";  shift 2 ;;
        --help|-h)    usage ;;
        *)            err "未知参数: $1（使用 --help 查看帮助）" ;;
    esac
done

# ── 前置检查 ──
[[ "$(id -u)" -ne 0 ]] && err "请使用 root 执行此脚本"
command -v openssl >/dev/null || err "openssl 未安装"
command -v incus >/dev/null   || err "Incus 未安装"

# ── 创建输出目录 ──
mkdir -p "${OUTPUT_DIR}"

KEY_FILE="${OUTPUT_DIR}/${CERT_CN}.key"
CRT_FILE="${OUTPUT_DIR}/${CERT_CN}.crt"

# ── 检查已有证书 ──
if [[ -f "${KEY_FILE}" || -f "${CRT_FILE}" ]]; then
    err "证书文件已存在: ${KEY_FILE} 或 ${CRT_FILE}，请先移除或更换 --cn"
fi

# ── 生成证书 ──
log "生成 EC secp384r1 证书（CN=${CERT_CN}，有效期 ${CERT_DAYS} 天）"
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:secp384r1 \
    -sha384 -keyout "${KEY_FILE}" -out "${CRT_FILE}" -nodes \
    -days "${CERT_DAYS}" -subj "/CN=${CERT_CN}"

chmod 600 "${KEY_FILE}"
chmod 644 "${CRT_FILE}"

log "证书已生成:"
log "  私钥: ${KEY_FILE}"
log "  证书: ${CRT_FILE}"

# ── 添加到 Incus（受限证书） ──
log "将证书添加到 Incus（受限到 Project: ${PROJECT_NAME}）"
incus config trust add-certificate "${CRT_FILE}" \
    --projects "${PROJECT_NAME}" --restricted

log "验证信任证书列表:"
incus config trust list --format csv | grep "${CERT_CN}" || warn "未在信任列表中找到 ${CERT_CN}"

log "Console 代理证书签发完成"
log "  CN: ${CERT_CN}"
log "  Project: ${PROJECT_NAME}"
log "  用途: 实例 console/exec 操作"
log ""
warn "重要：Console 代理应用层需自行限制 API 调用范围"
warn "Incus 受限证书提供 project 级隔离，console-only 限制需在代理层实现"
