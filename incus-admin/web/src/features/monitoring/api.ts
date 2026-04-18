import { useQuery } from "@tanstack/react-query";
import { http } from "@/shared/lib/http";

export interface VMMetric {
  name: string;
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

export interface ClusterMetrics {
  name: string;
  vms: VMMetric[];
  db_running_count?: number;
}

export const monitoringKeys = {
  all: ["monitoring"] as const,
  overview: () => [...monitoringKeys.all, "overview"] as const,
  health: () => [...monitoringKeys.all, "health"] as const,
  vm: (name: string, base: string, cluster?: string) =>
    [...monitoringKeys.all, "vm", name, base, cluster ?? ""] as const,
};

export function useHealthQuery() {
  return useQuery({
    queryKey: monitoringKeys.health(),
    queryFn: () => http.get<{ status: string }>("/health"),
  });
}

export function useMetricsOverviewQuery() {
  return useQuery({
    queryKey: monitoringKeys.overview(),
    queryFn: () =>
      http.get<{ clusters: ClusterMetrics[]; warning?: string }>("/admin/metrics/overview"),
    refetchInterval: 30_000,
    retry: 1,
  });
}

export function useVMMetricsQuery(vmName: string, base: "/portal" | "/admin", cluster?: string) {
  const params: Record<string, string> = {};
  if (cluster) params.cluster = cluster;

  return useQuery({
    queryKey: monitoringKeys.vm(vmName, base, cluster),
    queryFn: () =>
      http.get<{ metrics: VMMetric | null }>(
        `${base}/metrics/vm/${vmName}`,
        Object.keys(params).length > 0 ? params : undefined,
      ),
    refetchInterval: 30_000,
  });
}
