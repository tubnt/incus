import { createFileRoute, Link } from "@tanstack/react-router";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { http } from "@/shared/lib/http";
import { queryClient } from "@/shared/lib/query-client";
import { SnapshotPanel } from "@/features/snapshots/snapshot-panel";
import { VMMetricsPanel } from "@/features/monitoring/vm-metrics-panel";

export const Route = createFileRoute("/admin/vms")({
  component: AllVMsPage,
});

interface IncusInstance {
  name: string;
  status: string;
  type: string;
  location: string;
  project: string;
  config: Record<string, string>;
  state?: {
    network?: Record<string, {
      addresses: Array<{ address: string; family: string; scope: string }>;
    }>;
  };
}

function AllVMsPage() {
  const { data: clustersData } = useQuery({
    queryKey: ["adminClusters"],
    queryFn: () => http.get<{ clusters: Array<{ name: string; display_name: string }> }>("/admin/clusters"),
  });

  const clusters = clustersData?.clusters ?? [];

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold">All VMs</h1>
        <Link to="/admin/create-vm" className="px-4 py-2 bg-primary text-primary-foreground rounded-md text-sm font-medium hover:opacity-90">
          + Create VM
        </Link>
      </div>
      {clusters.map((c) => (
        <ClusterVMs key={c.name} clusterName={c.name} displayName={c.display_name} />
      ))}
    </div>
  );
}

function ClusterVMs({ clusterName, displayName }: { clusterName: string; displayName: string }) {
  const { data, isLoading } = useQuery({
    queryKey: ["adminClusterVMs", clusterName],
    queryFn: () => http.get<{ vms: IncusInstance[]; count: number }>(`/admin/clusters/${clusterName}/vms`),
    refetchInterval: 10_000,
  });

  const vms = data?.vms ?? [];

  return (
    <div className="mb-8">
      <h2 className="text-lg font-semibold mb-3">{displayName} ({data?.count ?? 0} VMs)</h2>
      {isLoading ? (
        <div className="text-muted-foreground">Loading...</div>
      ) : vms.length === 0 ? (
        <div className="border border-border rounded-lg p-6 text-center text-muted-foreground">
          No VMs in this cluster.
        </div>
      ) : (
        <div className="border border-border rounded-lg overflow-hidden">
          <table className="w-full text-sm">
            <thead className="bg-muted/30">
              <tr>
                <th className="text-left px-4 py-2 font-medium">Name</th>
                <th className="text-left px-4 py-2 font-medium">Status</th>
                <th className="text-left px-4 py-2 font-medium">Node</th>
                <th className="text-left px-4 py-2 font-medium">Config</th>
                <th className="text-left px-4 py-2 font-medium">IP</th>
                <th className="text-right px-4 py-2 font-medium">Actions</th>
              </tr>
            </thead>
            <tbody>
              {vms.map((vm) => (
                <VMRow key={vm.name} vm={vm} clusterName={clusterName} />
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

const OS_IMAGES = [
  { value: "images:ubuntu/24.04/cloud", label: "Ubuntu 24.04 LTS" },
  { value: "images:ubuntu/22.04/cloud", label: "Ubuntu 22.04 LTS" },
  { value: "images:debian/12/cloud", label: "Debian 12" },
  { value: "images:rockylinux/9/cloud", label: "Rocky Linux 9" },
];

function VMRow({ vm, clusterName }: { vm: IncusInstance; clusterName: string }) {
  const [showSnaps, setShowSnaps] = useState(false);
  const [showMetrics, setShowMetrics] = useState(false);
  const [showReinstall, setShowReinstall] = useState(false);
  const ip = extractIP(vm);
  const project = vm.project || "default";

  const stateMutation = useMutation({
    mutationFn: (action: string) =>
      http.put(`/admin/vms/${vm.name}/state`, { action, cluster: clusterName, project }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["adminClusterVMs"] });
    },
  });

  const deleteMutation = useMutation({
    mutationFn: () =>
      http.delete(`/admin/vms/${vm.name}`, { cluster: clusterName, project }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["adminClusterVMs"] });
    },
  });

  const isActing = stateMutation.isPending || deleteMutation.isPending;

  return (
    <>
    <tr className="border-t border-border">
      <td className="px-4 py-2 font-mono">
        <a href={`/admin/vm-detail?name=${vm.name}&cluster=${clusterName}&project=${project}`}
          className="text-primary hover:underline">{vm.name}</a>
      </td>
      <td className="px-4 py-2">
        <StatusBadge status={vm.status} />
      </td>
      <td className="px-4 py-2">{vm.location}</td>
      <td className="px-4 py-2 text-muted-foreground">
        {vm.config?.["limits.cpu"] ?? "—"}C / {vm.config?.["limits.memory"] ?? "—"}
      </td>
      <td className="px-4 py-2 font-mono text-xs">{ip || "—"}</td>
      <td className="px-4 py-2 text-right">
        <div className="flex gap-1 justify-end">
          {vm.status === "Stopped" && (
            <ActionBtn label="Start" color="success" disabled={isActing}
              onClick={() => stateMutation.mutate("start")} />
          )}
          {vm.status === "Running" && (
            <>
              <a
                href={`/console?vm=${vm.name}&cluster=${clusterName}&project=${project}`}
                className="px-2 py-1 rounded text-xs font-medium bg-primary/20 text-primary hover:bg-primary/30"
              >
                Console
              </a>
              <ActionBtn label="Stop" color="muted" disabled={isActing}
                onClick={() => stateMutation.mutate("stop")} />
              <ActionBtn label="Restart" color="muted" disabled={isActing}
                onClick={() => stateMutation.mutate("restart")} />
            </>
          )}
          {vm.status === "Running" && (
            <ActionBtn label="Monitor" color="muted" disabled={false}
              onClick={() => setShowMetrics(!showMetrics)} />
          )}
          <ActionBtn label="Snaps" color="muted" disabled={false}
            onClick={() => setShowSnaps(!showSnaps)} />
          <ActionBtn label="Reinstall" color="muted" disabled={isActing}
            onClick={() => setShowReinstall(!showReinstall)} />
          <ActionBtn label="Delete" color="destructive" disabled={isActing}
            onClick={() => {
              if (confirm(`Delete ${vm.name}? This cannot be undone.`)) {
                deleteMutation.mutate();
              }
            }} />
        </div>
      </td>
    </tr>
    {showMetrics && vm.status === "Running" && (
      <tr>
        <td colSpan={6} className="p-0">
          <VMMetricsPanel vmName={vm.name} apiBase="/admin" cluster={clusterName} />
        </td>
      </tr>
    )}
    {showSnaps && (
      <tr>
        <td colSpan={6} className="p-0">
          <SnapshotPanel vmName={vm.name} cluster={clusterName} project={project} />
        </td>
      </tr>
    )}
    {showReinstall && (
      <tr>
        <td colSpan={6} className="p-0">
          <ReinstallPanel vmName={vm.name} cluster={clusterName} project={project}
            onDone={() => setShowReinstall(false)} />
        </td>
      </tr>
    )}
    </>
  );
}

function ReinstallPanel({ vmName, cluster, project, onDone }: {
  vmName: string; cluster: string; project: string; onDone: () => void;
}) {
  const [os, setOs] = useState(OS_IMAGES[0]!.value);

  const mutation = useMutation({
    mutationFn: () =>
      http.post<{ status: string; password: string; username: string }>(
        `/admin/vms/${vmName}/reinstall`,
        { cluster, project, os_image: os },
      ),
    onSuccess: (data) => {
      queryClient.invalidateQueries({ queryKey: ["adminClusterVMs"] });
      alert(`重装完成！\n用户名: ${data.username}\n新密码: ${data.password}`);
      onDone();
    },
  });

  return (
    <div className="p-4 bg-card/50 border-t border-border">
      <h4 className="font-medium text-sm mb-3">重装系统 — {vmName}</h4>
      <p className="text-xs text-destructive mb-3">
        警告: 重装将删除所有数据并重建 VM，IP 和配置保持不变。
      </p>
      <div className="flex items-center gap-3">
        <select
          value={os}
          onChange={(e) => setOs(e.target.value)}
          className="px-2 py-1 text-xs border border-border rounded bg-card"
        >
          {OS_IMAGES.map((img) => (
            <option key={img.value} value={img.value}>{img.label}</option>
          ))}
        </select>
        <button
          onClick={() => {
            if (confirm(`确认重装 ${vmName}？所有数据将丢失！`)) {
              mutation.mutate();
            }
          }}
          disabled={mutation.isPending}
          className="px-3 py-1 text-xs bg-destructive text-destructive-foreground rounded disabled:opacity-50"
        >
          {mutation.isPending ? "重装中..." : "确认重装"}
        </button>
        <button
          onClick={onDone}
          className="px-3 py-1 text-xs bg-muted/50 text-muted-foreground rounded"
        >
          取消
        </button>
        {mutation.isError && (
          <span className="text-xs text-destructive">{(mutation.error as Error).message}</span>
        )}
      </div>
    </div>
  );
}

function ActionBtn({ label, color, disabled, onClick }: {
  label: string; color: string; disabled: boolean; onClick: () => void;
}) {
  const colorMap: Record<string, string> = {
    success: "bg-success/20 text-success hover:bg-success/30",
    muted: "bg-muted/50 text-muted-foreground hover:bg-muted",
    destructive: "bg-destructive/20 text-destructive hover:bg-destructive/30",
  };
  return (
    <button
      onClick={onClick}
      disabled={disabled}
      className={`px-2 py-1 rounded text-xs font-medium disabled:opacity-50 ${colorMap[color] ?? colorMap.muted}`}
    >
      {label}
    </button>
  );
}

function extractIP(vm: IncusInstance): string {
  if (!vm.state?.network) return "";
  for (const [nic, data] of Object.entries(vm.state.network)) {
    if (nic === "lo") continue;
    for (const addr of data.addresses) {
      if (addr.family === "inet" && addr.scope === "global") {
        return addr.address;
      }
    }
  }
  return "";
}

function StatusBadge({ status }: { status: string }) {
  const colors: Record<string, string> = {
    Running: "bg-success/20 text-success",
    Stopped: "bg-muted text-muted-foreground",
    Error: "bg-destructive/20 text-destructive",
    Frozen: "bg-primary/20 text-primary",
  };
  return (
    <span className={`px-2 py-0.5 rounded text-xs font-medium ${colors[status] ?? "bg-muted text-muted-foreground"}`}>
      {status}
    </span>
  );
}
