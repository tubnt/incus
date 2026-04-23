import { useQuery } from "@tanstack/react-query";
import { http } from "@/shared/lib/http";

export interface HealingEvacuatedVM {
  vm_id: number;
  name: string;
  from_node: string;
  to_node: string;
}

export type HealingTrigger = "manual" | "auto" | "chaos";
export type HealingStatus =
  | "in_progress"
  | "completed"
  | "failed"
  | "partial";

export interface HealingEvent {
  id: number;
  cluster_id: number;
  cluster_name: string;
  node_name: string;
  trigger: HealingTrigger;
  actor_id: number | null;
  evacuated_vms: HealingEvacuatedVM[] | null;
  started_at: string;
  completed_at?: string;
  duration_seconds?: number;
  status: HealingStatus;
  error?: string;
}

export interface HealingListResponse {
  items: HealingEvent[];
  total: number;
  limit: number;
  offset: number;
}

export interface HealingListFilter {
  cluster?: string;
  node?: string;
  trigger?: HealingTrigger | "";
  status?: HealingStatus | "";
  from?: string;
  to?: string;
  limit?: number;
  offset?: number;
}

export const healingKeys = {
  all: ["healing"] as const,
  list: (filter: HealingListFilter) =>
    [...healingKeys.all, "list", filter] as const,
  detail: (id: number) => [...healingKeys.all, "detail", id] as const,
};

function buildQuery(filter: HealingListFilter): string {
  const params = new URLSearchParams();
  if (filter.cluster) params.set("cluster", filter.cluster);
  if (filter.node) params.set("node", filter.node);
  if (filter.trigger) params.set("trigger", filter.trigger);
  if (filter.status) params.set("status", filter.status);
  if (filter.from) params.set("from", filter.from);
  if (filter.to) params.set("to", filter.to);
  if (filter.limit) params.set("limit", String(filter.limit));
  if (filter.offset) params.set("offset", String(filter.offset));
  const s = params.toString();
  return s ? `?${s}` : "";
}

export function useHealingEventsQuery(filter: HealingListFilter) {
  return useQuery({
    queryKey: healingKeys.list(filter),
    queryFn: () =>
      http.get<HealingListResponse>(`/admin/ha/events${buildQuery(filter)}`),
    refetchInterval: 30_000,
    staleTime: 10_000,
  });
}

export function useHealingEventDetailQuery(id: number | null) {
  return useQuery({
    queryKey: healingKeys.detail(id ?? 0),
    queryFn: () => http.get<HealingEvent>(`/admin/ha/events/${id}`),
    enabled: !!id,
  });
}
