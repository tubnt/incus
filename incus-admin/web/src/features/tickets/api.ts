import { useMutation, useQuery } from "@tanstack/react-query";
import { http } from "@/shared/lib/http";
import { pageKeyPart, pageQueryString, type PageParams } from "@/shared/lib/pagination";
import { queryClient } from "@/shared/lib/query-client";

export interface Ticket {
  id: number;
  user_id: number;
  subject: string;
  status: string;
  priority: string;
  created_at: string;
  updated_at: string;
}

export interface TicketMessage {
  id: number;
  ticket_id: number;
  user_id: number;
  body: string;
  is_staff: boolean;
  created_at: string;
}

export const ticketKeys = {
  all: ["ticket"] as const,
  myList: () => [...ticketKeys.all, "list", "my"] as const,
  adminList: (params?: PageParams) =>
    [...ticketKeys.all, "list", "admin", pageKeyPart(params)] as const,
  detail: (id: number) => [...ticketKeys.all, "detail", id] as const,
};

export function useMyTicketsQuery() {
  return useQuery({
    queryKey: ticketKeys.myList(),
    queryFn: () => http.get<{ tickets: Ticket[] }>("/portal/tickets"),
  });
}

export function useAdminTicketsQuery(params?: PageParams) {
  return useQuery({
    queryKey: ticketKeys.adminList(params),
    queryFn: () =>
      http.get<{ tickets: Ticket[]; total?: number; limit?: number; offset?: number }>(
        `/admin/tickets${pageQueryString(params)}`,
      ),
    refetchInterval: 15_000,
  });
}

export function useTicketDetailQuery(id: number, base: "/portal" | "/admin") {
  return useQuery({
    queryKey: ticketKeys.detail(id),
    queryFn: () => http.get<{ ticket: Ticket; messages: TicketMessage[] }>(`${base}/tickets/${id}`),
  });
}

export function useCreateTicketMutation() {
  return useMutation({
    mutationFn: (params: { subject: string; body: string; priority: string }) =>
      http.post("/portal/tickets", params),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ticketKeys.all }),
  });
}

export function useReplyTicketMutation(ticketId: number, base: "/portal" | "/admin") {
  return useMutation({
    mutationFn: (body: string) => http.post(`${base}/tickets/${ticketId}/messages`, { body }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ticketKeys.detail(ticketId) }),
  });
}

export function useUpdateTicketStatusMutation(ticketId: number) {
  return useMutation({
    mutationFn: (status: string) => http.put(`/admin/tickets/${ticketId}/status`, { status }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ticketKeys.all }),
  });
}
