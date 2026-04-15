import { createFileRoute, Link } from "@tanstack/react-router";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { http } from "@/shared/lib/http";
import { queryClient } from "@/shared/lib/query-client";
import { SnapshotPanel } from "@/features/snapshots/snapshot-panel";

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

function VMRow({ vm, clusterName }: { vm: IncusInstance; clusterName: string }) {
  const [showSnaps, setShowSnaps] = useState(false);
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
      <td className="px-4 py-2 font-mono">{vm.name}</td>
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
          <ActionBtn label="Snaps" color="muted" disabled={false}
            onClick={() => setShowSnaps(!showSnaps)} />
          <ActionBtn label="Delete" color="destructive" disabled={isActing}
            onClick={() => {
              if (confirm(`Delete ${vm.name}? This cannot be undone.`)) {
                deleteMutation.mutate();
              }
            }} />
        </div>
      </td>
    </tr>
    {showSnaps && (
      <tr>
        <td colSpan={6} className="p-0">
          <SnapshotPanel vmName={vm.name} cluster={clusterName} project={project} />
        </td>
      </tr>
    )}
    </>
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
