import { useMutation, useQuery } from "@tanstack/react-query";
import { http } from "@/shared/lib/http";
import { queryClient } from "@/shared/lib/query-client";

// VMService mirrors the backend VMServiceDTO. The password field is
// intentionally absent — credentials are only returned in create/reset
// responses, never from list/detail endpoints.
export interface VMService {
  id: number;
  name: string;
  cluster: string;
  cluster_display_name: string;
  project: string;
  ip: string | null;
  status: string;
  cpu: number;
  memory_mb: number;
  disk_gb: number;
  os_image: string;
  node: string;
  created_at: string;
  updated_at: string;
}

export interface IncusInstance {
  name: string;
  status: string;
  type: string;
  location: string;
  project: string;
  config: Record<string, string>;
  ip?: string;
  state?: {
    network?: Record<string, {
      addresses: Array<{ address: string; family: string; scope: string }>;
    }>;
  };
}

export interface ClusterVMsResponse {
  vms: IncusInstance[];
  count: number;
  stale?: boolean;
  cached_at?: string;
  error?: string;
  warning?: string;
}

export interface IncusInstanceState {
  status: string;
  cpu?: { usage: number };
  memory?: { usage: number; usage_peak: number; total: number };
  network?: Record<string, {
    addresses: Array<{ address: string; family: string; scope: string }>;
  }>;
}

export interface IncusSnapshot {
  name: string;
  created_at: string;
  stateful?: boolean;
}

export interface AdminVMDetail {
  vm: IncusInstance;
  state: IncusInstanceState;
  snapshots: IncusSnapshot[];
  project: string;
  db: VMService | null;
}

// Cache key prefix so list + detail invalidate together:
//   ["vm", "list", "my"]      portal list
//   ["vm", "detail", id]      portal detail
//   ["vm", "list", "cluster", name]  admin cluster list
//   ["vm", "detail", "cluster", name, vmName, project]  admin single-vm detail
export const vmKeys = {
  all: ["vm"] as const,
  myList: () => [...vmKeys.all, "list", "my"] as const,
  myDetail: (id: number) => [...vmKeys.all, "detail", id] as const,
  clusterList: (clusterName: string) => [...vmKeys.all, "list", "cluster", clusterName] as const,
  clusterDetail: (clusterName: string, vmName: string, project?: string) =>
    [...vmKeys.all, "detail", "cluster", clusterName, vmName, project ?? ""] as const,
  gone: () => [...vmKeys.all, "gone"] as const,
};

/**
 * GoneVM is a DB row flagged by the PLAN-020 reconciler as 'gone' — the
 * Incus instance vanished out-of-band. Admin surfaces these for
 * investigation + force-delete. Payload shape matches the server's
 * internal/model.VM but drops fields the panel doesn't render.
 */
export interface GoneVM {
  id: number;
  name: string;
  cluster_id: number;
  user_id: number;
  ip: string | null;
  status: string;
  cpu: number;
  memory_mb: number;
  disk_gb: number;
  os_image: string;
  node: string;
  created_at: string;
  updated_at: string;
}

export function useGoneVMsQuery() {
  return useQuery({
    queryKey: vmKeys.gone(),
    queryFn: () => http.get<{ vms: GoneVM[]; count: number }>("/admin/vms/gone"),
    staleTime: 30_000,
  });
}

export function useForceDeleteGoneVMMutation() {
  return useMutation({
    mutationFn: (id: number) =>
      http.post<{ status: string; id: number }>(`/admin/vms/${id}/force-delete`, {}),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: vmKeys.gone() }),
  });
}

export function useMyVMsQuery() {
  return useQuery({
    queryKey: vmKeys.myList(),
    queryFn: () => http.get<{ vms: VMService[] }>("/portal/services"),
    refetchInterval: 15_000,
  });
}

export function useMyVMDetailQuery(id: number) {
  return useQuery({
    queryKey: vmKeys.myDetail(id),
    queryFn: () => http.get<{ vm: VMService }>(`/portal/services/${id}`),
    enabled: id > 0,
  });
}

export function useVMActionMutation(vmId: number) {
  return useMutation({
    mutationFn: (action: string) => http.post(`/portal/services/${vmId}/actions/${action}`),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: vmKeys.all }),
  });
}

export function useCreateVMMutation() {
  return useMutation({
    mutationFn: (params: { name?: string; cpu: number; memory_mb: number; disk_gb: number; os_image: string }) =>
      http.post("/portal/services", params),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: vmKeys.all }),
  });
}

export function useClusterVMsQuery(clusterName: string, refetchIntervalMs = 10_000) {
  return useQuery({
    queryKey: vmKeys.clusterList(clusterName),
    queryFn: () => http.get<ClusterVMsResponse>(`/admin/clusters/${clusterName}/vms`),
    enabled: !!clusterName,
    refetchInterval: refetchIntervalMs,
    retry: 1,
  });
}

export function useAdminVMDetailQuery(
  clusterName: string,
  vmName: string,
  project?: string,
  refetchIntervalMs = 10_000,
) {
  return useQuery({
    queryKey: vmKeys.clusterDetail(clusterName, vmName, project),
    queryFn: () => {
      const qs = project ? `?project=${encodeURIComponent(project)}` : "";
      return http.get<AdminVMDetail>(
        `/admin/clusters/${clusterName}/vms/${vmName}${qs}`,
      );
    },
    enabled: !!clusterName && !!vmName,
    refetchInterval: refetchIntervalMs,
    retry: 1,
  });
}

export function useVMStateMutation() {
  return useMutation({
    mutationFn: (params: { name: string; action: string; cluster: string; project: string }) =>
      http.put(`/admin/vms/${params.name}/state`, params),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: vmKeys.all }),
  });
}

export function useDeleteVMMutation() {
  return useMutation({
    mutationFn: (params: { name: string; cluster: string; project: string }) =>
      http.delete(`/admin/vms/${params.name}`, { cluster: params.cluster, project: params.project }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: vmKeys.all }),
  });
}

export function useMigrateVMMutation() {
  return useMutation({
    mutationFn: (params: { name: string; cluster: string; project: string; target_node: string }) =>
      http.post(`/admin/vms/${params.name}/migrate`, {
        cluster: params.cluster,
        project: params.project,
        target_node: params.target_node,
      }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: vmKeys.all }),
  });
}

export function useReinstallVMMutation() {
  return useMutation({
    mutationFn: (params: { name: string; cluster: string; project: string; os_image: string }) =>
      http.post<{ status: string; password: string; username: string }>(`/admin/vms/${params.name}/reinstall`, params),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: vmKeys.all }),
  });
}

export interface AdminCreateVMParams {
  cpu: number;
  memory_mb: number;
  disk_gb: number;
  os_image: string;
  project: string;
}

export interface AdminCreateVMResult {
  vm_name: string;
  ip: string;
  username: string;
  password: string;
}

export function useAdminCreateVMMutation(clusterName: string) {
  return useMutation({
    mutationFn: (params: AdminCreateVMParams) =>
      http.post<AdminCreateVMResult>(`/admin/clusters/${clusterName}/vms`, params),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: vmKeys.all }),
  });
}

export interface ResetPasswordResult {
  password: string;
  username: string;
}

export function useResetVMPasswordMutation(vmId: number) {
  return useMutation({
    mutationFn: () =>
      http.post<ResetPasswordResult>(`/portal/services/${vmId}/reset-password`, {}),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: vmKeys.myDetail(vmId) }),
  });
}

export function extractIP(vm: IncusInstance): string {
  if (!vm.state?.network) return "";
  for (const [nic, data] of Object.entries(vm.state.network)) {
    if (nic === "lo") continue;
    for (const addr of data.addresses) {
      if (addr.family === "inet" && addr.scope === "global") return addr.address;
    }
  }
  return "";
}
