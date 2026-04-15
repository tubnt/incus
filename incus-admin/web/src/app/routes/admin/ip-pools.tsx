import { createFileRoute } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { http } from "@/shared/lib/http";

export const Route = createFileRoute("/admin/ip-pools")({
  component: IPPoolsPage,
});

interface PoolInfo {
  cluster_name: string;
  cidr: string;
  gateway: string;
  vlan: number;
  range: string;
  total: number;
  used: number;
  available: number;
}

function IPPoolsPage() {
  const { data, isLoading } = useQuery({
    queryKey: ["adminIPPools"],
    queryFn: () => http.get<{ pools: PoolInfo[] }>("/admin/ip-pools"),
  });

  const pools = data?.pools ?? [];

  return (
    <div>
      <h1 className="text-2xl font-bold mb-6">IP Pools</h1>
      {isLoading ? (
        <div className="text-muted-foreground">Loading...</div>
      ) : pools.length === 0 ? (
        <div className="border border-border rounded-lg p-8 text-center text-muted-foreground">
          No IP pools configured.
        </div>
      ) : (
        <div className="space-y-4">
          {pools.map((pool, i) => (
            <div key={i} className="border border-border rounded-lg bg-card p-4">
              <div className="flex items-center justify-between mb-3">
                <div>
                  <h3 className="font-semibold">{pool.cluster_name}</h3>
                  <div className="text-sm text-muted-foreground">
                    {pool.cidr} · Gateway {pool.gateway} · VLAN {pool.vlan}
                  </div>
                </div>
                <div className="text-right">
                  <div className="text-2xl font-bold">{pool.available}</div>
                  <div className="text-xs text-muted-foreground">available</div>
                </div>
              </div>

              <div className="flex items-center gap-4 mb-2">
                <div className="flex-1 h-3 bg-muted rounded-full overflow-hidden">
                  <div
                    className="h-full bg-primary rounded-full transition-all"
                    style={{ width: `${pool.total > 0 ? (pool.used / pool.total) * 100 : 0}%` }}
                  />
                </div>
                <span className="text-sm text-muted-foreground whitespace-nowrap">
                  {pool.used} / {pool.total} used
                </span>
              </div>

              <div className="text-xs text-muted-foreground">
                Range: {pool.range}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
