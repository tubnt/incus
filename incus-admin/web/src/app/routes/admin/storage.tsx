import { createFileRoute } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { http } from "@/shared/lib/http";
import { fmtBytes } from "@/shared/lib/utils";

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

function StoragePage() {
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
      <h1 className="text-2xl font-bold mb-6">Storage (Ceph)</h1>

      {!hasCeph ? (
        <div className="border border-border rounded-lg p-6 text-center text-muted-foreground">
          Ceph SSH not configured. Set CEPH_SSH_HOST env variable to enable.
        </div>
      ) : (
        <>
          <div className="grid grid-cols-2 md:grid-cols-4 gap-4 mb-6">
            <StatCard label="Health" value={health}
              color={health === "HEALTH_OK" ? "text-success" : health === "HEALTH_WARN" ? "text-yellow-500" : "text-destructive"} />
            <StatCard label="OSDs" value={osdmap ? `${osdmap.num_up_osds}/${osdmap.num_osds} up` : "—"} />
            <StatCard label="Pools" value={String(pgmap?.num_pools ?? "—")} />
            <StatCard label="PGs" value={String(pgmap?.num_pgs ?? "—")} />
          </div>

          {pgmap && (
            <div className="grid grid-cols-2 md:grid-cols-4 gap-4 mb-6">
              <StatCard label="Total Capacity" value={fmtBytes(pgmap.bytes_total)} />
              <StatCard label="Used" value={`${fmtBytes(pgmap.bytes_used)} (${((pgmap.bytes_used / pgmap.bytes_total) * 100).toFixed(1)}%)`} />
              <StatCard label="Available" value={fmtBytes(pgmap.bytes_avail)} />
              <StatCard label="Data Stored" value={fmtBytes(pgmap.data_bytes)} />
            </div>
          )}

          {pgmap && (
            <div className="grid grid-cols-2 md:grid-cols-4 gap-4 mb-6">
              <StatCard label="Read IOPS" value={`${pgmap.read_op_per_sec ?? 0}/s`} />
              <StatCard label="Write IOPS" value={`${pgmap.write_op_per_sec ?? 0}/s`} />
              <StatCard label="Read Throughput" value={`${fmtBytes(pgmap.read_bytes_sec ?? 0)}/s`} />
              <StatCard label="Write Throughput" value={`${fmtBytes(pgmap.write_bytes_sec ?? 0)}/s`} />
            </div>
          )}

          {osds.length > 0 && (
            <div className="border border-border rounded-lg overflow-hidden mb-6">
              <div className="px-4 py-3 border-b border-border bg-muted/30">
                <h3 className="font-semibold text-sm">OSD List ({osds.length})</h3>
              </div>
              <table className="w-full text-sm">
                <thead className="bg-muted/20">
                  <tr>
                    <th className="text-left px-4 py-2 font-medium">OSD</th>
                    <th className="text-left px-4 py-2 font-medium">Status</th>
                    <th className="text-right px-4 py-2 font-medium">Weight</th>
                  </tr>
                </thead>
                <tbody>
                  {osds.map((osd) => (
                    <tr key={osd.id} className="border-t border-border">
                      <td className="px-4 py-1.5 font-mono text-xs">{osd.name}</td>
                      <td className="px-4 py-1.5">
                        <span className={`px-2 py-0.5 rounded text-xs font-medium ${osd.status === "up" ? "bg-success/20 text-success" : "bg-destructive/20 text-destructive"}`}>
                          {osd.status ?? "unknown"}
                        </span>
                      </td>
                      <td className="px-4 py-1.5 text-right font-mono text-xs">{osd.crush_weight?.toFixed(3) ?? "—"}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}

          {hosts.length > 0 && (
            <div className="border border-border rounded-lg bg-card p-4">
              <h3 className="font-semibold text-sm mb-3">Storage Hosts ({hosts.length})</h3>
              <div className="grid grid-cols-2 md:grid-cols-5 gap-3">
                {hosts.map((h) => (
                  <div key={h.id} className="border border-border rounded p-3 text-center">
                    <div className="font-mono text-sm">{h.name}</div>
                    <div className="text-xs text-muted-foreground">{h.children?.length ?? 0} OSDs</div>
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

function StatCard({ label, value, color }: { label: string; value: string; color?: string }) {
  return (
    <div className="border border-border rounded-lg bg-card p-4">
      <div className="text-xs text-muted-foreground">{label}</div>
      <div className={`text-lg font-bold mt-1 ${color ?? ""}`}>{value}</div>
    </div>
  );
}
