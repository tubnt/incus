import { useMutation, useQuery } from "@tanstack/react-query";
import { http } from "@/shared/lib/http";
import { queryClient } from "@/shared/lib/query-client";

export interface APIToken {
  id: number;
  name: string;
  token?: string;
  last_used_at: string | null;
  expires_at: string | null;
  created_at: string;
}

export const apiTokenKeys = {
  all: ["apiToken"] as const,
  list: () => [...apiTokenKeys.all, "list"] as const,
};

export function useAPITokensQuery() {
  return useQuery({
    queryKey: apiTokenKeys.list(),
    queryFn: () => http.get<{ tokens: APIToken[] }>("/portal/api-tokens"),
  });
}

export interface CreateTokenInput {
  name: string;
  /** TTL in hours; omit to use server default (24h). Server clamps to [1h, 90d]. */
  expiresInHours?: number;
}

export function useCreateAPITokenMutation() {
  return useMutation({
    mutationFn: (input: CreateTokenInput) =>
      http.post<{ token: APIToken }>("/portal/api-tokens", {
        name: input.name,
        expires_in_hours: input.expiresInHours,
      }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: apiTokenKeys.all }),
  });
}

export interface RenewTokenInput {
  id: number;
  expiresInHours?: number;
}

export function useRenewAPITokenMutation() {
  return useMutation({
    mutationFn: ({ id, expiresInHours }: RenewTokenInput) =>
      http.post<{ token: APIToken }>(`/portal/api-tokens/${id}/renew`, {
        expires_in_hours: expiresInHours,
      }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: apiTokenKeys.all }),
  });
}

export function useDeleteAPITokenMutation() {
  return useMutation({
    mutationFn: (id: number) => http.delete(`/portal/api-tokens/${id}`),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: apiTokenKeys.all }),
  });
}
