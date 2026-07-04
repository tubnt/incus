#!/usr/bin/env bash
# =============================================================================
# probe-node.sh — read-only inventory probe used by the admin add-node wizard.
#
# Behaviour requirements (PLAN-033 / OPS-039):
#   - **Idempotent and read-only**: no apt-get, no file writes, no service
#     toggles. Safe to run repeatedly.
#   - **No `jq` dependency**: emits raw JSON / text segments with section
#     markers; the admin process parses them with encoding/json + regex.
#   - **iproute2 compat**: `ip -j addr/link` work on iproute2 ≥ 4.13 (all
#     supported targets); `ip route` is emitted as text since `ip -j route`
#     only landed in iproute2 5.0.
#
# Output format (consumed by internal/service/nodeprobe):
#   ---<section>---
#   <raw output>
#
# Sections (in this order): hostname, os-release, kernel, cpu, mem,
# ip-link, ip-addr, ip-route, disks, incus-version, ceph-version,
# lspci-eth, ethtool, numa.
#
# PLAN-038 / OPS-041 Phase A 增段：lspci-eth (NIC PCI 列表) / ethtool
# (每 NIC 速率/驱动/link) / numa (NUMA topology)。这些都是 ranker 评分
# 输入，缺失时 ranker 退化为 0 中性值。
# =============================================================================
set -u

emit() {
    echo "---$1---"
}

# Best-effort: each section is independently fault-tolerant; missing tool
# emits an empty section so admin parser can degrade gracefully.
safe() { "$@" 2>/dev/null || true; }

emit hostname
safe hostname

emit os-release
safe cat /etc/os-release

emit kernel
safe uname -r

emit cpu
# `lscpu -J` requires util-linux >= 2.32 (Ubuntu 18.04+, RHEL 8+); fall back
# to plain `lscpu` text when missing.
if command -v lscpu >/dev/null 2>&1; then
    safe lscpu -J 2>/dev/null || safe lscpu
fi

emit mem
safe head -n 5 /proc/meminfo

emit ip-link
safe ip -j link show

emit ip-addr
safe ip -j addr show

emit ip-route
# ip -j route may not be supported (< iproute2 5.0); use plain text.
safe ip route show default

emit disks
safe lsblk -J -o NAME,SIZE,ROTA,MODEL,TYPE

emit incus-version
if command -v incus >/dev/null 2>&1; then
    safe incus --version
else
    echo MISSING
fi

emit ceph-version
if command -v ceph >/dev/null 2>&1; then
    safe ceph --version
else
    echo MISSING
fi

emit lspci-eth
# PLAN-038 / OPS-041 Phase A：lspci -mm 列出 ethernet 设备（NIC PCI ID + vendor + model）
# -mm 输出比 -nn 更紧凑，admin 端用空格分词解析。lspci 普遍存在，缺失则空段。
if command -v lspci >/dev/null 2>&1; then
    safe lspci -mm 2>/dev/null | grep -iE '\"(ethernet|network)' || true
fi

emit ethtool
# PLAN-038 / OPS-041 Phase A：每个非 lo 网卡的 ethtool 输出（speed/duplex/link
# detected）+ ethtool -i（driver/bus-info）。每段以 ===<ifname>=== 分隔；admin
# 端按 ifname 拆解。ethtool 大多 distro 默认有；缺失则空段。
if command -v ethtool >/dev/null 2>&1; then
    for nic in $(ip -o link show 2>/dev/null | awk -F': ' '$2 != "lo" {print $2}' | sed 's/@.*//' | sort -u); do
        echo "===${nic}==="
        safe ethtool "$nic" 2>/dev/null
        echo "---driver---"
        safe ethtool -i "$nic" 2>/dev/null
    done
fi

emit numa
# PLAN-038 / OPS-041 Phase A：NUMA 节点与 socket 数（用于 ranker NUMA-aware 提示）
safe lscpu 2>/dev/null | grep -E 'NUMA|^Socket' || true

emit end
