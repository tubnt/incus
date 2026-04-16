import { createFileRoute } from "@tanstack/react-router";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { http } from "@/shared/lib/http";
import { queryClient } from "@/shared/lib/query-client";

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
  const [showAdd, setShowAdd] = useState(false);
  const { data, isLoading, refetch } = useQuery({
    queryKey: ["adminIPPools"],
    queryFn: () => http.get<{ pools: PoolInfo[] }>("/admin/ip-pools"),
  });

  const pools = data?.pools ?? [];

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold">IP Pools</h1>
        <button onClick={() => setShowAdd(!showAdd)}
          className="px-4 py-2 bg-primary text-primary-foreground rounded-md text-sm font-medium hover:opacity-90">
          {showAdd ? "Cancel" : "+ Add Pool"}
        </button>
      </div>

      {showAdd && <AddPoolForm onDone={() => { setShowAdd(false); refetch(); }} />}

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
                  <div className="h-full bg-primary rounded-full transition-all"
                    style={{ width: `${pool.total > 0 ? (pool.used / pool.total) * 100 : 0}%` }} />
                </div>
                <span className="text-sm text-muted-foreground whitespace-nowrap">
                  {pool.used} / {pool.total} used
                </span>
              </div>
              <div className="text-xs text-muted-foreground">Range: {pool.range}</div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function AddPoolForm({ onDone }: { onDone: () => void }) {
  const [form, setForm] = useState({ cluster: "", cidr: "", gateway: "", range: "", vlan: 0 });

  const { data: clustersData } = useQuery({
    queryKey: ["adminClusters"],
    queryFn: () => http.get<{ clusters: Array<{ name: string; display_name: string }> }>("/admin/clusters"),
  });
  const clusters = clustersData?.clusters ?? [];

  const mutation = useMutation({
    mutationFn: () => http.post("/admin/ip-pools", form),
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ["adminIPPools"] }); onDone(); },
  });

  const set = (k: string, v: string | number) => setForm({ ...form, [k]: v });

  return (
    <div className="border border-border rounded-lg bg-card p-4 mb-6">
      <h3 className="font-semibold mb-3">Add IP Pool</h3>
      <div className="grid grid-cols-2 gap-3 mb-4">
        <select value={form.cluster} onChange={(e) => set("cluster", e.target.value)}
          className="px-3 py-2 rounded border border-border bg-card text-sm">
          <option value="">Select cluster</option>
          {clusters.map((c) => <option key={c.name} value={c.name}>{c.display_name || c.name}</option>)}
        </select>
        <input type="number" placeholder="VLAN ID" value={form.vlan || ""} onChange={(e) => set("vlan", +e.target.value)}
          className="px-3 py-2 rounded border border-border bg-card text-sm" />
        <input placeholder="CIDR (e.g. 202.151.179.224/27)" value={form.cidr} onChange={(e) => set("cidr", e.target.value)}
          className="px-3 py-2 rounded border border-border bg-card text-sm" />
        <input placeholder="Gateway (e.g. 202.151.179.225)" value={form.gateway} onChange={(e) => set("gateway", e.target.value)}
          className="px-3 py-2 rounded border border-border bg-card text-sm" />
        <input placeholder="Range (e.g. 202.151.179.230-202.151.179.254)" value={form.range} onChange={(e) => set("range", e.target.value)}
          className="col-span-2 px-3 py-2 rounded border border-border bg-card text-sm" />
      </div>
      {mutation.isError && <div className="text-destructive text-sm mb-2">{(mutation.error as Error).message}</div>}
      <button onClick={() => mutation.mutate()} disabled={mutation.isPending || !form.cluster || !form.cidr}
        className="px-4 py-2 bg-primary text-primary-foreground rounded text-sm font-medium disabled:opacity-50">
        {mutation.isPending ? "Adding..." : "Add Pool"}
      </button>
    </div>
  );
}
