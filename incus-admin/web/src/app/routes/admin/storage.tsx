import { createFileRoute } from "@tanstack/react-router";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import { http } from "@/shared/lib/http";
import { queryClient } from "@/shared/lib/query-client";
import { fmtBytes } from "@/shared/lib/utils";
import { useConfirm } from "@/shared/components/ui/confirm-dialog";

export const Route = createFileRoute("/admin/storage")({
  component: StoragePage,
});

interface CephStatus {
  health?: { status: string };
  osdmap?: { num_osds: number; num_up_osds: number; num_in_osds: number };
  pgmap?: {
    num_pgs: number;
    num_pools: number;
    data_bytes: number;
    bytes_used: number;
    bytes_avail: number;
    bytes_total: number;
    read_bytes_sec: number;
    write_bytes_sec: number;
    read_op_per_sec: number;
    write_op_per_sec: number;
  };
  error?: string;
}

interface OSDTree {
  nodes?: Array<{
    id: number;
    name: string;
    type: string;
    status?: string;
    crush_weight?: number;
    children?: number[];
  }>;
  error?: string;
}

// Ceph pool type values can arrive as number or string.
// Reference: Ceph OSD pool types — 1 = replicated, 3 = erasure-coded.
function poolTypeLabel(raw: unknown, t: (k: string) => string): string {
  const s = String(raw ?? "").toLowerCase();
  if (s === "1" || s === "replicated") return t("storage.poolType.replicated");
  if (s === "3" || s === "erasure" || s === "erasurecoded" || s === "ec") return t("storage.poolType.erasure");
  return s || "—";
}

function StoragePage() {
  const { t } = useTranslation();
  const { data: cephStatus } = useQuery({
    queryKey: ["cephStatus"],
    queryFn: () => http.get<CephStatus>("/admin/ceph/status"),
    refetchInterval: 30_000,
  });

  const { data: osdTree } = useQuery({
    queryKey: ["cephOsdTree"],
    queryFn: () => http.get<OSDTree>("/admin/ceph/osd-tree"),
    refetchInterval: 60_000,
  });

  const health = cephStatus?.health?.status ?? "UNKNOWN";
  const osdmap = cephStatus?.osdmap;
  const pgmap = cephStatus?.pgmap;
  const osds = osdTree?.nodes?.filter((n) => n.type === "osd") ?? [];
  const hosts = osdTree?.nodes?.filter((n) => n.type === "host") ?? [];
  const hasCeph = !cephStatus?.error;

  return (
    <div>
      <h1 className="text-2xl font-bold mb-6">{t("storage.title")}</h1>

      {!hasCeph ? (
        <div className="border border-border rounded-lg p-6 text-center text-muted-foreground">
          {t("storage.notConfigured")}
        </div>
      ) : (
        <>
          <div className="grid grid-cols-2 md:grid-cols-4 gap-4 mb-6">
            <StatCard label={t("storage.health")} value={health}
              color={health === "HEALTH_OK" ? "text-success" : health === "HEALTH_WARN" ? "text-yellow-500" : "text-destructive"} />
            <StatCard label={t("storage.osds")} value={osdmap ? `${osdmap.num_up_osds}/${osdmap.num_osds} up` : "—"} />
            <StatCard label={t("storage.pools")} value={String(pgmap?.num_pools ?? "—")} />
            <StatCard label={t("storage.pgs")} value={String(pgmap?.num_pgs ?? "—")} />
          </div>

          {pgmap && (
            <div className="grid grid-cols-2 md:grid-cols-4 gap-4 mb-6">
              <StatCard label={t("storage.capacity")} value={fmtBytes(pgmap.bytes_total)} />
              <StatCard label={t("storage.used")} value={`${fmtBytes(pgmap.bytes_used)} (${((pgmap.bytes_used / pgmap.bytes_total) * 100).toFixed(1)}%)`} />
              <StatCard label={t("storage.available")} value={fmtBytes(pgmap.bytes_avail)} />
              <StatCard label={t("storage.dataStored")} value={fmtBytes(pgmap.data_bytes)} />
            </div>
          )}

          {pgmap && pgmap.bytes_total > 0 && (pgmap.bytes_used / pgmap.bytes_total) > 0.8 && (
            <div className={`border rounded-lg p-4 mb-6 ${(pgmap.bytes_used / pgmap.bytes_total) > 0.9 ? "border-destructive/50 bg-destructive/10" : "border-warning/50 bg-warning/10"}`}>
              <div className={`font-semibold text-sm ${(pgmap.bytes_used / pgmap.bytes_total) > 0.9 ? "text-destructive" : "text-warning"}`}>
                {(pgmap.bytes_used / pgmap.bytes_total) > 0.9
                  ? `⚠ ${t("storage.warnOver90")}`
                  : `⚠ ${t("storage.warnOver80")}`}
              </div>
              <div className="text-xs text-muted-foreground mt-1">
                {t("storage.currentUsage")}: {((pgmap.bytes_used / pgmap.bytes_total) * 100).toFixed(1)}% — {fmtBytes(pgmap.bytes_used)} / {fmtBytes(pgmap.bytes_total)}
              </div>
            </div>
          )}

          {pgmap && (
            <div className="grid grid-cols-2 md:grid-cols-4 gap-4 mb-6">
              <StatCard label={t("storage.readIops")} value={`${pgmap.read_op_per_sec ?? 0}/s`} />
              <StatCard label={t("storage.writeIops")} value={`${pgmap.write_op_per_sec ?? 0}/s`} />
              <StatCard label={t("storage.readThroughput")} value={`${fmtBytes(pgmap.read_bytes_sec ?? 0)}/s`} />
              <StatCard label={t("storage.writeThroughput")} value={`${fmtBytes(pgmap.write_bytes_sec ?? 0)}/s`} />
            </div>
          )}

          {osds.length > 0 && (
            <div className="border border-border rounded-lg overflow-hidden mb-6">
              <div className="px-4 py-3 border-b border-border bg-muted/30">
                <h3 className="font-semibold text-sm">{t("storage.osdListTitle")} ({osds.length})</h3>
              </div>
              <table className="w-full text-sm">
                <thead className="bg-muted/20">
                  <tr>
                    <th className="text-left px-4 py-2 font-medium">OSD</th>
                    <th className="text-left px-4 py-2 font-medium">{t("storage.status")}</th>
                    <th className="text-right px-4 py-2 font-medium">{t("storage.weight")}</th>
                    <th className="text-right px-4 py-2 font-medium">{t("common.actions")}</th>
                  </tr>
                </thead>
                <tbody>
                  {osds.map((osd) => (
                    <OSDRow key={osd.id} osd={osd} />
                  ))}
                </tbody>
              </table>
            </div>
          )}

          <PoolSection />

          {hosts.length > 0 && (
            <div className="border border-border rounded-lg bg-card p-4">
              <h3 className="font-semibold text-sm mb-3">{t("storage.hostsTitle")} ({hosts.length})</h3>
              <div className="grid grid-cols-2 md:grid-cols-5 gap-3">
                {hosts.map((h) => (
                  <div key={h.id} className="border border-border rounded p-3 text-center">
                    <div className="font-mono text-sm">{h.name}</div>
                    <div className="text-xs text-muted-foreground">{t("storage.osdCount", { count: h.children?.length ?? 0 })}</div>
                  </div>
                ))}
              </div>
            </div>
          )}
        </>
      )}
    </div>
  );
}

interface CephPool {
  pool_name: string;
  pool_id: number;
  type: string | number;
  size: number;
  pg_num: number;
  application_metadata?: Record<string, Record<string, unknown>>;
}

function PoolSection() {
  const { t } = useTranslation();
  const confirm = useConfirm();
  const [showCreate, setShowCreate] = useState(false);
  const [newPool, setNewPool] = useState({ name: "", pg_num: 128, type: "replicated" });

  const { data: pools } = useQuery({
    queryKey: ["cephPools"],
    queryFn: () => http.get<CephPool[]>("/admin/ceph/pools"),
    refetchInterval: 60_000,
  });

  const createMutation = useMutation({
    mutationFn: () => http.post("/admin/ceph/pools", newPool),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["cephPools"] });
      toast.success(t("storage.poolCreatedToast", { name: newPool.name }));
      setShowCreate(false);
      setNewPool({ name: "", pg_num: 128, type: "replicated" });
    },
    onError: () => toast.error(t("storage.poolCreateFailed")),
  });

  const deleteMutation = useMutation({
    mutationFn: (name: string) => http.delete(`/admin/ceph/pools/${name}`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["cephPools"] });
      toast.success(t("storage.poolDeletedToast"));
    },
    onError: () => toast.error(t("storage.poolDeleteFailed")),
  });

  const poolList = Array.isArray(pools) ? pools : [];

  return (
    <div className="border border-border rounded-lg overflow-hidden mb-6">
      <div className="px-4 py-3 border-b border-border bg-muted/30 flex items-center justify-between">
        <h3 className="font-semibold text-sm">{t("storage.poolsTitle")} ({poolList.length})</h3>
        <button
          onClick={() => setShowCreate(!showCreate)}
          className="px-2 py-1 text-xs bg-primary/20 text-primary rounded hover:bg-primary/30"
        >
          {showCreate ? t("common.cancel") : t("storage.createPool")}
        </button>
      </div>

      {showCreate && (
        <div className="px-4 py-3 border-b border-border bg-card/50">
          <div className="flex gap-2 items-end">
            <div>
              <div className="text-xs text-muted-foreground mb-0.5">{t("storage.poolName")}</div>
              <input
                value={newPool.name}
                onChange={(e) => setNewPool({ ...newPool, name: e.target.value })}
                className="px-2 py-1 rounded border border-border bg-card text-xs w-40"
                placeholder="pool-name"
              />
            </div>
            <div>
              <div className="text-xs text-muted-foreground mb-0.5">{t("storage.poolPgNum")}</div>
              <input
                type="number"
                value={newPool.pg_num}
                onChange={(e) => setNewPool({ ...newPool, pg_num: +e.target.value })}
                className="px-2 py-1 rounded border border-border bg-card text-xs w-20"
              />
            </div>
            <div>
              <div className="text-xs text-muted-foreground mb-0.5">{t("storage.poolTypeLabel")}</div>
              <select
                value={newPool.type}
                onChange={(e) => setNewPool({ ...newPool, type: e.target.value })}
                className="px-2 py-1 rounded border border-border bg-card text-xs"
              >
                <option value="replicated">replicated</option>
                <option value="erasure">erasure</option>
              </select>
            </div>
            <button
              onClick={() => createMutation.mutate()}
              disabled={createMutation.isPending || !newPool.name}
              className="px-3 py-1 text-xs bg-primary text-primary-foreground rounded disabled:opacity-50"
            >
              {createMutation.isPending ? "..." : t("storage.createPoolSubmit")}
            </button>
          </div>
        </div>
      )}

      {poolList.length > 0 && (
        <table className="w-full text-sm">
          <thead className="bg-muted/20">
            <tr>
              <th className="text-left px-4 py-2 font-medium">Pool</th>
              <th className="text-left px-4 py-2 font-medium">{t("storage.poolTypeLabel")}</th>
              <th className="text-right px-4 py-2 font-medium">Size</th>
              <th className="text-right px-4 py-2 font-medium">PGs</th>
              <th className="text-left px-4 py-2 font-medium">Apps</th>
              <th className="text-right px-4 py-2 font-medium">{t("common.actions")}</th>
            </tr>
          </thead>
          <tbody>
            {poolList.map((p) => (
              <tr key={p.pool_id} className="border-t border-border">
                <td className="px-4 py-1.5 font-mono text-xs">{p.pool_name}</td>
                <td className="px-4 py-1.5 text-xs text-muted-foreground">{poolTypeLabel(p.type, t)}</td>
                <td className="px-4 py-1.5 text-right text-xs">{p.size}</td>
                <td className="px-4 py-1.5 text-right text-xs font-mono">{p.pg_num}</td>
                <td className="px-4 py-1.5 text-xs text-muted-foreground">
                  {p.application_metadata ? Object.keys(p.application_metadata).join(", ") : "-"}
                </td>
                <td className="px-4 py-1.5 text-right">
                  <button
                    onClick={async () => {
                      const ok = await confirm({
                        title: t("deleteConfirm.poolTitle"),
                        message: t("deleteConfirm.poolMessage", { name: p.pool_name }),
                        destructive: true,
                      });
                      if (ok) deleteMutation.mutate(p.pool_name);
                    }}
                    disabled={deleteMutation.isPending}
                    className="px-2 py-0.5 text-xs border border-destructive/30 text-destructive rounded hover:bg-destructive/10 disabled:opacity-50"
                  >
                    {t("common.delete")}
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}

function OSDRow({ osd }: { osd: { id: number; name: string; status?: string; crush_weight?: number } }) {
  const { t } = useTranslation();
  const confirm = useConfirm();
  const osdNum = String(osd.id);

  const outMutation = useMutation({
    mutationFn: () => http.post(`/admin/ceph/osd/${osdNum}/out`, {}),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["cephOsdTree"] });
      queryClient.invalidateQueries({ queryKey: ["cephStatus"] });
      toast.success(t("storage.osdOutToast", { id: osdNum }));
    },
    onError: () => toast.error(t("storage.osdOutFailed", { id: osdNum })),
  });

  const inMutation = useMutation({
    mutationFn: () => http.post(`/admin/ceph/osd/${osdNum}/in`, {}),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["cephOsdTree"] });
      queryClient.invalidateQueries({ queryKey: ["cephStatus"] });
      toast.success(t("storage.osdInToast", { id: osdNum }));
    },
    onError: () => toast.error(t("storage.osdInFailed", { id: osdNum })),
  });

  const isPending = outMutation.isPending || inMutation.isPending;

  return (
    <tr className="border-t border-border">
      <td className="px-4 py-1.5 font-mono text-xs">{osd.name}</td>
      <td className="px-4 py-1.5">
        <span className={`px-2 py-0.5 rounded text-xs font-medium ${osd.status === "up" ? "bg-success/20 text-success" : "bg-destructive/20 text-destructive"}`}>
          {osd.status ?? "unknown"}
        </span>
      </td>
      <td className="px-4 py-1.5 text-right font-mono text-xs">{osd.crush_weight?.toFixed(3) ?? "—"}</td>
      <td className="px-4 py-1.5 text-right">
        <div className="flex justify-end gap-1">
          <button
            onClick={async () => {
              const ok = await confirm({
                title: t("deleteConfirm.osdOutTitle"),
                message: t("deleteConfirm.osdOutMessage", { id: osdNum }),
                destructive: true,
              });
              if (ok) outMutation.mutate();
            }}
            disabled={isPending}
            className="px-2 py-0.5 text-xs border border-warning/30 text-warning rounded hover:bg-warning/10 disabled:opacity-50"
          >
            Out
          </button>
          <button
            onClick={() => inMutation.mutate()}
            disabled={isPending}
            className="px-2 py-0.5 text-xs border border-success/30 text-success rounded hover:bg-success/10 disabled:opacity-50"
          >
            In
          </button>
        </div>
      </td>
    </tr>
  );
}

function StatCard({ label, value, color }: { label: string; value: string; color?: string }) {
  return (
    <div className="border border-border rounded-lg bg-card p-4">
      <div className="text-xs text-muted-foreground">{label}</div>
      <div className={`text-lg font-bold mt-1 ${color ?? ""}`}>{value}</div>
    </div>
  );
}
