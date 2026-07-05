import { useMutation, useQuery } from "@tanstack/react-query";
import { http } from "@/shared/lib/http";
import { queryClient } from "@/shared/lib/query-client";

export interface ClusterInfo {
  name: string;
  display_name: string;
  api_url: string;
  nodes: number;
  status: string;
}

export interface NodeInfo {
  server_name: string;
  url?: string;
  status: string;
  message: string;
  cpu_total: number;
  mem_total: number;
  mem_used: number;
  mem_free: number;
  free_ratio: number;
}

export interface AddClusterParams {
  name: string;
  display_name: string;
  api_url: string;
  cert_file: string;
  key_file: string;
  // PLAN-027 / INFRA-003 扩展字段（可选；老 form 不传等价于 cluster + 默认）
  ca_file?: string;
  kind?: "cluster" | "standalone";
  default_project?: string;
  storage_pool?: string;
  network?: string;
}

export const clusterKeys = {
  all: ["cluster"] as const,
  list: () => [...clusterKeys.all, "list"] as const,
  nodes: (name: string) => [...clusterKeys.all, "nodes", name] as const,
};

export function useClustersQuery() {
  return useQuery({
    queryKey: clusterKeys.list(),
    queryFn: () => http.get<{ clusters: ClusterInfo[] }>("/admin/clusters"),
  });
}

export function useClusterNodesQuery(clusterName: string, refetchIntervalMs?: number) {
  return useQuery({
    queryKey: clusterKeys.nodes(clusterName),
    queryFn: () => http.get<{ nodes: NodeInfo[] }>(`/admin/clusters/${clusterName}/nodes`),
    enabled: !!clusterName,
    refetchInterval: refetchIntervalMs,
  });
}

export function useEvacuateNodeMutation(clusterName: string) {
  return useMutation({
    mutationFn: (nodeName: string) =>
      http.post(`/admin/clusters/${clusterName}/nodes/${nodeName}/evacuate`),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: clusterKeys.all }),
    // WP-F：HA 疏散成功/失败统一由全局 MutationCache 兜底提示。
    meta: { globalErrorToast: true, successToast: "ha.evacuateStartedToast" },
  });
}

export function useRestoreNodeMutation(clusterName: string) {
  return useMutation({
    mutationFn: (nodeName: string) =>
      http.post(`/admin/clusters/${clusterName}/nodes/${nodeName}/restore`),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: clusterKeys.all }),
    meta: { globalErrorToast: true, successToast: "ha.nodeRestoredToast" },
  });
}

export function useAddClusterMutation() {
  return useMutation({
    mutationFn: (params: AddClusterParams) => http.post("/admin/clusters/add", params),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: clusterKeys.all }),
  });
}
