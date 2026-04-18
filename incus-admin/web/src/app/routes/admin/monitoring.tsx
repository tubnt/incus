import { createFileRoute } from "@tanstack/react-router";
import { useState } from "react";
import { type VMMetric, useMetricsOverviewQuery } from "@/features/monitoring/api";
import {
  BarChart,
  Bar,
  XAxis,
  YAxis,
  Tooltip,
  ResponsiveContainer,
  Cell,
} from "recharts";

export const Route = createFileRoute("/admin/monitoring")({
  component: MonitoringPage,
});

function MonitoringPage() {
  const { data, isLoading, error } = useMetricsOverviewQuery();

  const clusters = data?.clusters ?? [];
  const allVMs = clusters.flatMap((c) => c.vms ?? []);
  const hasWarning = data?.warning;
  const dbRunningTotal = clusters.reduce(
    (sum, c) => sum + (c.db_running_count ?? 0),
    0,
  );
  const drifted = allVMs.length === 0 && dbRunningTotal > 0;

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold">集群监控</h1>
        <span className="text-xs text-muted-foreground">
          每 30 秒自动刷新
        </span>
      </div>

      {hasWarning && (
        <div className="border border-warning/30 rounded-lg p-3 mb-4 text-sm text-warning">
          ⚠ {hasWarning}
        </div>
      )}

      {isLoading ? (
        <div className="grid grid-cols-2 lg:grid-cols-4 gap-4">
          {Array.from({ length: 4 }).map((_, i) => (
            <div key={i} className="border border-border rounded-lg bg-card p-4 space-y-2">
              <div className="h-3 w-16 animate-pulse rounded bg-muted" />
              <div className="h-6 w-24 animate-pulse rounded bg-muted" />
            </div>
          ))}
        </div>
      ) : error ? (
        <div className="border border-destructive/30 rounded-lg p-4 text-destructive text-sm">
          获取监控数据失败: {(error as Error).message}
        </div>
      ) : allVMs.length === 0 ? (
        <div className="border border-border rounded-lg p-6 text-center text-muted-foreground">
          {drifted ? (
            <>
              DB 记录当前集群有 {dbRunningTotal} 个 running VM，但 Incus 侧实时查询为 0。
              数据已漂移 —— 这些 VM 可能已在 Incus 端被直接删除，状态未同步回 DB。
              请管理员核对并执行状态同步。
            </>
          ) : (
            <>暂无 VM 监控数据（当前集群无运行中的 VM）。</>
          )}
        </div>
      ) : (
        <div className="space-y-6">
          <SummaryCards vms={allVMs} />
          <div className="grid grid-cols-1 xl:grid-cols-2 gap-6">
            <CPUChart vms={allVMs} />
            <MemoryChart vms={allVMs} />
            <DiskChart vms={allVMs} />
            <NetworkChart vms={allVMs} />
          </div>
          <VMTable vms={allVMs} />
        </div>
      )}
    </div>
  );
}

function SummaryCards({ vms }: { vms: VMMetric[] }) {
  const totalMem = vms.reduce((s, v) => s + v.mem_total_bytes, 0);
  const usedMem = vms.reduce((s, v) => s + v.mem_used_bytes, 0);
  const totalDisk = vms.reduce((s, v) => s + v.disk_total_bytes, 0);
  const usedDisk = vms.reduce((s, v) => s + v.disk_used_bytes, 0);
  const avgCPU =
    vms.length > 0
      ? vms.reduce((s, v) => s + v.cpu_user_pct + v.cpu_system_pct, 0) /
        vms.length
      : 0;

  const cards = [
    { label: "VM 数量", value: String(vms.length) },
    { label: "平均 CPU 使用", value: `${avgCPU.toFixed(1)}%` },
    {
      label: "总内存使用",
      value: `${fmtBytes(usedMem)} / ${fmtBytes(totalMem)}`,
    },
    {
      label: "总磁盘使用",
      value: `${fmtBytes(usedDisk)} / ${fmtBytes(totalDisk)}`,
    },
  ];

  return (
    <div className="grid grid-cols-2 lg:grid-cols-4 gap-4">
      {cards.map((c) => (
        <div
          key={c.label}
          className="border border-border rounded-lg bg-card p-4"
        >
          <div className="text-xs text-muted-foreground mb-1">{c.label}</div>
          <div className="text-lg font-semibold">{c.value}</div>
        </div>
      ))}
    </div>
  );
}

function CPUChart({ vms }: { vms: VMMetric[] }) {
  const chartData = vms.map((v) => ({
    name: v.name,
    user: +v.cpu_user_pct.toFixed(1),
    system: +v.cpu_system_pct.toFixed(1),
  }));

  return (
    <ChartCard title="CPU 使用率 (%)">
      <ResponsiveContainer width="100%" height={280}>
        <BarChart data={chartData} layout="vertical" margin={{ left: 80 }}>
          <XAxis type="number" domain={[0, 100]} tickFormatter={(v) => `${v}%`} />
          <YAxis type="category" dataKey="name" width={75} tick={{ fontSize: 11 }} />
          <Tooltip formatter={(v) => `${v}%`} />
          <Bar dataKey="user" stackId="cpu" fill="var(--color-primary)" name="User" />
          <Bar dataKey="system" stackId="cpu" fill="var(--color-destructive)" name="System" />
        </BarChart>
      </ResponsiveContainer>
    </ChartCard>
  );
}

function MemoryChart({ vms }: { vms: VMMetric[] }) {
  const chartData = vms.map((v) => ({
    name: v.name,
    used: +(v.mem_used_bytes / 1024 / 1024 / 1024).toFixed(2),
    total: +(v.mem_total_bytes / 1024 / 1024 / 1024).toFixed(2),
    pct: +v.mem_used_pct.toFixed(1),
  }));

  return (
    <ChartCard title="内存使用">
      <ResponsiveContainer width="100%" height={280}>
        <BarChart data={chartData} layout="vertical" margin={{ left: 80 }}>
          <XAxis type="number" tickFormatter={(v) => `${v} GB`} />
          <YAxis type="category" dataKey="name" width={75} tick={{ fontSize: 11 }} />
          <Tooltip formatter={(v) => `${v} GB`} />
          <Bar dataKey="total" fill="var(--color-muted)" name="总量" />
          <Bar dataKey="used" fill="var(--color-primary)" name="已用" />
        </BarChart>
      </ResponsiveContainer>
    </ChartCard>
  );
}

function DiskChart({ vms }: { vms: VMMetric[] }) {
  const chartData = vms
    .filter((v) => v.disk_total_bytes > 0)
    .map((v) => ({
      name: v.name,
      pct: +v.disk_used_pct.toFixed(1),
    }));

  return (
    <ChartCard title="磁盘使用率 (%)">
      <ResponsiveContainer width="100%" height={280}>
        <BarChart data={chartData} layout="vertical" margin={{ left: 80 }}>
          <XAxis type="number" domain={[0, 100]} tickFormatter={(v) => `${v}%`} />
          <YAxis type="category" dataKey="name" width={75} tick={{ fontSize: 11 }} />
          <Tooltip formatter={(v) => `${v}%`} />
          <Bar dataKey="pct" name="使用率">
            {chartData.map((_, i) => (
              <Cell
                key={i}
                fill={
                  chartData[i].pct > 90
                    ? "var(--color-destructive)"
                    : chartData[i].pct > 70
                      ? "#f59e0b"
                      : "var(--color-success)"
                }
              />
            ))}
          </Bar>
        </BarChart>
      </ResponsiveContainer>
    </ChartCard>
  );
}

function NetworkChart({ vms }: { vms: VMMetric[] }) {
  const chartData = vms
    .filter((v) => v.net_rx_bytes > 0 || v.net_tx_bytes > 0)
    .map((v) => ({
      name: v.name,
      rx: +(v.net_rx_bytes / 1024 / 1024).toFixed(1),
      tx: +(v.net_tx_bytes / 1024 / 1024).toFixed(1),
    }));

  if (chartData.length === 0) {
    return (
      <ChartCard title="网络流量">
        <div className="h-[280px] flex items-center justify-center text-muted-foreground text-sm">
          暂无网络数据
        </div>
      </ChartCard>
    );
  }

  return (
    <ChartCard title="网络流量 (MB)">
      <ResponsiveContainer width="100%" height={280}>
        <BarChart data={chartData} layout="vertical" margin={{ left: 80 }}>
          <XAxis type="number" tickFormatter={(v) => `${v} MB`} />
          <YAxis type="category" dataKey="name" width={75} tick={{ fontSize: 11 }} />
          <Tooltip formatter={(v) => `${v} MB`} />
          <Bar dataKey="rx" fill="var(--color-success)" name="接收" />
          <Bar dataKey="tx" fill="var(--color-primary)" name="发送" />
        </BarChart>
      </ResponsiveContainer>
    </ChartCard>
  );
}

function VMTable({ vms }: { vms: VMMetric[] }) {
  const [sort, setSort] = useState<"cpu" | "mem" | "disk">("cpu");

  const sorted = [...vms].sort((a, b) => {
    if (sort === "cpu")
      return (
        b.cpu_user_pct + b.cpu_system_pct - (a.cpu_user_pct + a.cpu_system_pct)
      );
    if (sort === "mem") return b.mem_used_pct - a.mem_used_pct;
    return b.disk_used_pct - a.disk_used_pct;
  });

  return (
    <div className="border border-border rounded-lg bg-card overflow-hidden">
      <div className="px-4 py-3 border-b border-border flex items-center justify-between">
        <h3 className="font-semibold text-sm">VM 资源明细</h3>
        <div className="flex gap-1">
          {(["cpu", "mem", "disk"] as const).map((s) => (
            <button
              key={s}
              onClick={() => setSort(s)}
              className={`px-2 py-1 text-xs rounded ${sort === s ? "bg-primary text-primary-foreground" : "bg-muted/50 text-muted-foreground hover:bg-muted"}`}
            >
              {s === "cpu" ? "CPU" : s === "mem" ? "内存" : "磁盘"}
            </button>
          ))}
        </div>
      </div>
      <div className="overflow-x-auto">
        <table className="w-full text-sm">
          <thead className="bg-muted/30">
            <tr>
              <th className="text-left px-4 py-2 font-medium">VM</th>
              <th className="text-right px-4 py-2 font-medium">CPU</th>
              <th className="text-right px-4 py-2 font-medium">内存</th>
              <th className="text-right px-4 py-2 font-medium">磁盘</th>
              <th className="text-right px-4 py-2 font-medium">网络 (RX/TX)</th>
            </tr>
          </thead>
          <tbody>
            {sorted.map((vm) => (
              <tr key={vm.name} className="border-t border-border">
                <td className="px-4 py-2 font-mono text-xs">{vm.name}</td>
                <td className="px-4 py-2 text-right">
                  <UsageBadge pct={vm.cpu_user_pct + vm.cpu_system_pct} />
                </td>
                <td className="px-4 py-2 text-right">
                  <UsageBadge pct={vm.mem_used_pct} />
                  <span className="text-xs text-muted-foreground ml-1">
                    {fmtBytes(vm.mem_used_bytes)}/{fmtBytes(vm.mem_total_bytes)}
                  </span>
                </td>
                <td className="px-4 py-2 text-right">
                  <UsageBadge pct={vm.disk_used_pct} />
                  <span className="text-xs text-muted-foreground ml-1">
                    {fmtBytes(vm.disk_used_bytes)}/{fmtBytes(vm.disk_total_bytes)}
                  </span>
                </td>
                <td className="px-4 py-2 text-right text-xs text-muted-foreground">
                  {fmtBytes(vm.net_rx_bytes)} / {fmtBytes(vm.net_tx_bytes)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function UsageBadge({ pct }: { pct: number }) {
  const color =
    pct > 90
      ? "bg-destructive/20 text-destructive"
      : pct > 70
        ? "bg-warning/20 text-warning"
        : "bg-success/20 text-success";
  return (
    <span className={`px-1.5 py-0.5 rounded text-xs font-medium ${color}`}>
      {pct.toFixed(1)}%
    </span>
  );
}

function ChartCard({
  title,
  children,
}: {
  title: string;
  children: React.ReactNode;
}) {
  return (
    <div className="border border-border rounded-lg bg-card overflow-hidden">
      <div className="px-4 py-3 border-b border-border">
        <h3 className="font-semibold text-sm">{title}</h3>
      </div>
      <div className="p-4">{children}</div>
    </div>
  );
}

function fmtBytes(bytes: number): string {
  if (bytes === 0) return "0";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.floor(Math.log(Math.abs(bytes)) / Math.log(1024));
  const val = bytes / Math.pow(1024, i);
  return `${val.toFixed(i > 1 ? 1 : 0)} ${units[i]}`;
}
