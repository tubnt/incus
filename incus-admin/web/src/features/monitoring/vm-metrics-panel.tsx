import { useQuery } from "@tanstack/react-query";
import { http } from "@/shared/lib/http";

interface VMMetricData {
  cpu_user_pct: number;
  cpu_system_pct: number;
  cpu_idle_pct: number;
  mem_total_bytes: number;
  mem_used_bytes: number;
  mem_used_pct: number;
  disk_total_bytes: number;
  disk_used_bytes: number;
  disk_used_pct: number;
  net_rx_bytes: number;
  net_tx_bytes: number;
}

interface VMMetricsPanelProps {
  vmName: string;
  apiBase: "/portal" | "/admin";
  cluster?: string;
}

export function VMMetricsPanel({ vmName, apiBase, cluster }: VMMetricsPanelProps) {
  const params: Record<string, string> = {};
  if (cluster) params.cluster = cluster;

  const { data, isLoading, error } = useQuery({
    queryKey: ["vmMetrics", vmName, apiBase, cluster],
    queryFn: () =>
      http.get<{ metrics: VMMetricData | null }>(
        `${apiBase}/metrics/vm/${vmName}`,
        Object.keys(params).length > 0 ? params : undefined,
      ),
    refetchInterval: 30_000,
  });

  const m = data?.metrics;

  return (
    <div className="border-t border-border bg-card/50 p-4">
      {isLoading ? (
        <div className="text-xs text-muted-foreground">加载监控数据...</div>
      ) : error ? (
        <div className="text-xs text-destructive">
          获取数据失败: {(error as Error).message}
        </div>
      ) : !m ? (
        <div className="text-xs text-muted-foreground">暂无监控数据</div>
      ) : (
        <div className="grid grid-cols-2 lg:grid-cols-4 gap-3">
          <MetricGauge
            label="CPU"
            pct={m.cpu_user_pct + m.cpu_system_pct}
            detail={`user ${m.cpu_user_pct.toFixed(1)}% / sys ${m.cpu_system_pct.toFixed(1)}%`}
          />
          <MetricGauge
            label="内存"
            pct={m.mem_used_pct}
            detail={`${fmtBytes(m.mem_used_bytes)} / ${fmtBytes(m.mem_total_bytes)}`}
          />
          <MetricGauge
            label="磁盘"
            pct={m.disk_used_pct}
            detail={`${fmtBytes(m.disk_used_bytes)} / ${fmtBytes(m.disk_total_bytes)}`}
          />
          <div className="rounded-lg border border-border p-3">
            <div className="text-xs text-muted-foreground mb-1">网络</div>
            <div className="text-sm font-medium">
              ↓ {fmtBytes(m.net_rx_bytes)}
            </div>
            <div className="text-sm font-medium">
              ↑ {fmtBytes(m.net_tx_bytes)}
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

function MetricGauge({
  label,
  pct,
  detail,
}: {
  label: string;
  pct: number;
  detail: string;
}) {
  const color =
    pct > 90
      ? "bg-destructive"
      : pct > 70
        ? "bg-yellow-500"
        : "bg-success";

  return (
    <div className="rounded-lg border border-border p-3">
      <div className="flex items-center justify-between mb-1">
        <span className="text-xs text-muted-foreground">{label}</span>
        <span className="text-xs font-medium">{pct.toFixed(1)}%</span>
      </div>
      <div className="w-full h-2 bg-muted rounded-full overflow-hidden">
        <div
          className={`h-full rounded-full transition-all ${color}`}
          style={{ width: `${Math.min(pct, 100)}%` }}
        />
      </div>
      <div className="text-xs text-muted-foreground mt-1">{detail}</div>
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
