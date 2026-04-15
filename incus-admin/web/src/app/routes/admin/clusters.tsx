import { createFileRoute } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { http } from "@/shared/lib/http";

export const Route = createFileRoute("/admin/clusters")({
  component: ClustersPage,
});

interface ClusterInfo {
  name: string;
  display_name: string;
  api_url: string;
  nodes: number;
  status: string;
}

interface NodeInfo {
  server_name: string;
  status: string;
  message: string;
  cpu_total: number;
  mem_total: number;
  mem_used: number;
  mem_free: number;
  free_ratio: number;
}

function ClustersPage() {
  const { data, isLoading } = useQuery({
    queryKey: ["adminClusters"],
    queryFn: () => http.get<{ clusters: ClusterInfo[] }>("/admin/clusters"),
  });

  const clusters = data?.clusters ?? [];

  return (
    <div>
      <h1 className="text-2xl font-bold mb-6">Clusters</h1>
      {isLoading ? (
        <div className="text-muted-foreground">Loading...</div>
      ) : clusters.length === 0 ? (
        <div className="border border-border rounded-lg p-8 text-center text-muted-foreground">
          No clusters configured.
        </div>
      ) : (
        <div className="space-y-6">
          {clusters.map((c) => (
            <ClusterCard key={c.name} cluster={c} />
          ))}
        </div>
      )}
    </div>
  );
}

function ClusterCard({ cluster }: { cluster: ClusterInfo }) {
  const { data } = useQuery({
    queryKey: ["adminNodes", cluster.name],
    queryFn: () => http.get<{ nodes: NodeInfo[] }>(`/admin/clusters/${cluster.name}/nodes`),
  });

  const nodes = data?.nodes ?? [];

  return (
    <div className="border border-border rounded-lg bg-card overflow-hidden">
      <div className="p-4 flex items-center justify-between border-b border-border">
        <div>
          <h3 className="font-semibold text-lg">{cluster.display_name || cluster.name}</h3>
          <div className="text-sm text-muted-foreground mt-1">
            {cluster.api_url} · {nodes.length} nodes
          </div>
        </div>
        <span className="px-2 py-0.5 rounded text-xs font-medium bg-success/20 text-success">
          {cluster.status}
        </span>
      </div>

      {nodes.length > 0 && (
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead className="bg-muted/30">
              <tr>
                <th className="text-left px-4 py-2 font-medium">Node</th>
                <th className="text-left px-4 py-2 font-medium">Status</th>
                <th className="text-left px-4 py-2 font-medium">CPU</th>
                <th className="text-left px-4 py-2 font-medium">Memory</th>
                <th className="text-left px-4 py-2 font-medium">Free %</th>
              </tr>
            </thead>
            <tbody>
              {nodes.map((n) => (
                <tr key={n.server_name} className="border-t border-border">
                  <td className="px-4 py-2 font-mono">{n.server_name}</td>
                  <td className="px-4 py-2">
                    <span className={`px-2 py-0.5 rounded text-xs font-medium ${n.status === "Online" ? "bg-success/20 text-success" : "bg-destructive/20 text-destructive"}`}>
                      {n.status}
                    </span>
                  </td>
                  <td className="px-4 py-2">{n.cpu_total} cores</td>
                  <td className="px-4 py-2">
                    {formatBytes(n.mem_used)} / {formatBytes(n.mem_total)}
                  </td>
                  <td className="px-4 py-2">
                    <div className="flex items-center gap-2">
                      <div className="w-16 h-2 bg-muted rounded-full overflow-hidden">
                        <div
                          className="h-full bg-success rounded-full"
                          style={{ width: `${(n.free_ratio * 100).toFixed(0)}%` }}
                        />
                      </div>
                      <span className="text-xs text-muted-foreground">
                        {(n.free_ratio * 100).toFixed(0)}%
                      </span>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const gb = bytes / (1024 * 1024 * 1024);
  if (gb >= 1) return `${gb.toFixed(1)} GB`;
  const mb = bytes / (1024 * 1024);
  return `${mb.toFixed(0)} MB`;
}
