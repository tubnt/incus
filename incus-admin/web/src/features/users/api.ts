import { useMutation, useQuery } from "@tanstack/react-query";
import { http } from "@/shared/lib/http";
import { pageKeyPart, pageQueryString, type PageParams } from "@/shared/lib/pagination";
import { queryClient } from "@/shared/lib/query-client";
import type { User } from "@/shared/lib/auth";

export type { PageParams } from "@/shared/lib/pagination";

export interface Quota {
  max_vms: number;
  max_vcpus: number;
  max_ram_mb: number;
  max_disk_gb: number;
  max_ips: number;
  max_snapshots: number;
}

export interface QuotaUsage {
  vms: number;
  vcpus: number;
  ram_mb: number;
  disk_gb: number;
}

export const userKeys = {
  all: ["user"] as const,
  adminList: (params?: PageParams) =>
    [...userKeys.all, "list", "admin", pageKeyPart(params)] as const,
  quota: (userId: number) => [...userKeys.all, "quota", userId] as const,
  topupQuota: (userId: number) => [...userKeys.all, "topup-quota", userId] as const,
};

export interface TopUpQuota {
  used: number;
  limit: number;
  remaining: number;
  per_request_limit: number;
  window_hours: number;
}

export function useAdminUsersQuery(params?: PageParams) {
  return useQuery({
    queryKey: userKeys.adminList(params),
    queryFn: () =>
      http.get<{ users: User[]; total?: number; limit?: number; offset?: number }>(
        `/admin/users${pageQueryString(params)}`,
      ),
  });
}

export function useUpdateUserRoleMutation(userId: number) {
  return useMutation({
    mutationFn: (role: string) => http.put(`/admin/users/${userId}/role`, { role }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: userKeys.all }),
  });
}

export function useTopUpBalanceMutation(userId: number) {
  return useMutation({
    mutationFn: (amount: number) =>
      http.post(`/admin/users/${userId}/balance`, { amount, description: "Admin top-up" }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: userKeys.all });
      queryClient.invalidateQueries({ queryKey: userKeys.topupQuota(userId) });
    },
  });
}

export function useTopUpQuotaQuery(userId: number, enabled = true) {
  return useQuery({
    queryKey: userKeys.topupQuota(userId),
    queryFn: () => http.get<TopUpQuota>(`/admin/users/${userId}/topup-quota`),
    enabled: enabled && userId > 0,
    staleTime: 30_000,
  });
}

export function useUserQuotaQuery(userId: number) {
  return useQuery({
    queryKey: userKeys.quota(userId),
    queryFn: () => http.get<{ quota: Quota | null; usage: QuotaUsage }>(`/admin/users/${userId}/quota`),
  });
}

export function useUpdateUserQuotaMutation(userId: number) {
  return useMutation({
    mutationFn: (quota: Quota) => http.put(`/admin/users/${userId}/quota`, quota),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: userKeys.quota(userId) }),
  });
}
