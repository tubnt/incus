import { createFileRoute } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { http } from "@/shared/lib/http";

export const Route = createFileRoute("/admin/vms")({
  component: AllVMsPage,
});

interface IncusInstance {
  name: string;
  status: string;
  type: string;
  location: string;
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
      <h1 className="text-2xl font-bold mb-6">All VMs</h1>
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
                <th className="text-left px-4 py-2 font-medium">Type</th>
                <th className="text-left px-4 py-2 font-medium">Node</th>
                <th className="text-left px-4 py-2 font-medium">CPU / RAM</th>
                <th className="text-left px-4 py-2 font-medium">IP</th>
              </tr>
            </thead>
            <tbody>
              {vms.map((vm) => {
                const ip = extractIP(vm);
                return (
                  <tr key={vm.name} className="border-t border-border">
                    <td className="px-4 py-2 font-mono">{vm.name}</td>
                    <td className="px-4 py-2">
                      <StatusBadge status={vm.status} />
                    </td>
                    <td className="px-4 py-2 text-muted-foreground">{vm.type}</td>
                    <td className="px-4 py-2">{vm.location}</td>
                    <td className="px-4 py-2 text-muted-foreground">
                      {vm.config?.["limits.cpu"] ?? "—"}C / {vm.config?.["limits.memory"] ?? "—"}
                    </td>
                    <td className="px-4 py-2 font-mono text-xs">{ip || "—"}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </div>
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
  };
  return (
    <span className={`px-2 py-0.5 rounded text-xs font-medium ${colors[status] ?? "bg-muted text-muted-foreground"}`}>
      {status}
    </span>
  );
}
