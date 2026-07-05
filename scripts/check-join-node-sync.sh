#!/usr/bin/env bash
# =============================================================================
# CI gate：运维脚本"双副本"必须严格一致。
#
# 项目里每个下发到物理机的运维脚本存在两份：
#   - master：incus-admin/internal/sshexec/embedded/{scripts,configs}/*.sh
#             通过 Go //go:embed 打进二进制，admin 端 SSH 下发时用这份。
#   - mirror：cluster/{scripts,configs}/*.sh
#             供运维在物理机上直接 clone 仓库手动执行。
#
# 两份会漂移。历史事故（Session-2 F-03 / PLAN-051 §2-A / OPS-046）：cluster 副本
# 缺 apply-network.sh 的 VLAN-pub 子接口绑桥自检 → 运维手跑复现"VM 全断公网"；
# probe-node.sh 缺 PLAN-038 的 lspci/ethtool/numa 采集段 → ranker 评分退化。
#
# 本 gate 逐一 diff master 与 mirror；只要有一对漂移即 fail。
# master 永远是唯一事实来源（embed 进二进制的那份），mirror 必须与之一致。
#
# 覆盖范围自动推导：遍历 embedded/scripts/*.sh 与 embedded/configs/*.sh，
# 逐一映射到 cluster/scripts/ 与 cluster/configs/ 的同名文件。新增 embed 脚本
# 无需改本脚本即自动纳入检查。
#
# 说明：cluster/scripts/cluster-env.sh 是监控栈专用的另一个同名 env 文件
# （NODE_EXPORTER_PORT / PROMETHEUS_PORT 等），没有 embed 副本，不属双副本对，
# 本 gate 不检查它。集群主配置的双副本对是 embedded/configs/cluster-env.sh
# ↔ cluster/configs/cluster-env.sh。
#
# 用法：
#   bash scripts/check-join-node-sync.sh
#
# 退出码：
#   0 = 全部一致
#   1 = 至少一对漂移（CI 即视为失败）
#   2 = 目录/文件缺失（配置错误）
# =============================================================================
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
EMBED_ROOT="${REPO_ROOT}/incus-admin/internal/sshexec/embedded"

# 双副本子目录映射：master 子目录 -> mirror 子目录
# 每个 embedded 子目录里的 *.sh 都必须在对应 cluster 子目录里有一致的同名文件。
declare -a SUBDIRS=(
  "scripts:scripts"
  "configs:configs"
)

DRIFT=0
CHECKED=0

for pair in "${SUBDIRS[@]}"; do
  master_sub="${pair%%:*}"
  mirror_sub="${pair##*:}"
  master_dir="${EMBED_ROOT}/${master_sub}"
  mirror_dir="${REPO_ROOT}/cluster/${mirror_sub}"

  if [[ ! -d "${master_dir}" ]]; then
    echo "ERROR: master 目录不存在: ${master_dir}"
    exit 2
  fi

  shopt -s nullglob
  for master in "${master_dir}"/*.sh; do
    base="$(basename "${master}")"
    mirror="${mirror_dir}/${base}"

    if [[ ! -f "${mirror}" ]]; then
      echo "ERROR: mirror 缺失: cluster/${mirror_sub}/${base}"
      echo "       修复: cp '${master}' '${mirror}' && git add '${mirror}'"
      DRIFT=1
      continue
    fi

    CHECKED=$((CHECKED + 1))
    if diff -q "${master}" "${mirror}" > /dev/null; then
      echo "ok: cluster/${mirror_sub}/${base} 与 embedded 副本一致"
    else
      echo ""
      echo "ERROR: cluster/${mirror_sub}/${base} 与 embedded 副本漂移："
      diff -u "${master}" "${mirror}" | head -120
      echo ""
      echo "修复: cp '${master}' '${mirror}' && git add '${mirror}'"
      echo ""
      DRIFT=1
    fi
  done
  shopt -u nullglob
done

if [[ "${CHECKED}" -eq 0 ]]; then
  echo "ERROR: 没有检查到任何双副本脚本，检查 embed 目录布局是否变更"
  exit 2
fi

if [[ "${DRIFT}" -ne 0 ]]; then
  echo "双副本漂移检查失败：master 是 embedded 副本，cluster 必须与之一致。"
  exit 1
fi

echo ""
echo "ok: 全部 ${CHECKED} 对双副本脚本一致"
exit 0
