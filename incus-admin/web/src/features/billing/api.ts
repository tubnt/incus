import { useMutation, useQuery } from "@tanstack/react-query";
import { http } from "@/shared/lib/http";
import { pageKeyPart, pageQueryString, type PageParams } from "@/shared/lib/pagination";
import { queryClient } from "@/shared/lib/query-client";
import { vmKeys } from "@/features/vms/api";

export interface Order {
  id: number;
  product_id: number;
  status: string;
  amount: number;
  currency?: string;
  expires_at: string | null;
  created_at: string;
}

export interface AdminOrder extends Order {
  user_id: number;
  cluster_id: number;
}

export interface Invoice {
  id: number;
  order_id: number;
  amount: number;
  currency?: string;
  status: string;
  due_at?: string | null;
  paid_at: string | null;
  created_at: string;
}

export interface AdminInvoice extends Invoice {
  user_id: number;
}

export interface VMCredentials {
  vm_name: string;
  ip: string;
  username: string;
  password: string;
}

export const orderKeys = {
  all: ["order"] as const,
  myList: () => [...orderKeys.all, "list", "my"] as const,
  adminList: (params?: PageParams) =>
    [...orderKeys.all, "list", "admin", pageKeyPart(params)] as const,
};

export const invoiceKeys = {
  all: ["invoice"] as const,
  myList: () => [...invoiceKeys.all, "list", "my"] as const,
  adminList: (params?: PageParams) =>
    [...invoiceKeys.all, "list", "admin", pageKeyPart(params)] as const,
};

export function useMyOrdersQuery() {
  return useQuery({
    queryKey: orderKeys.myList(),
    queryFn: () => http.get<{ orders: Order[] }>("/portal/orders"),
  });
}

export function useMyInvoicesQuery() {
  return useQuery({
    queryKey: invoiceKeys.myList(),
    queryFn: () => http.get<{ invoices: Invoice[] }>("/portal/invoices"),
  });
}

export function useCreateOrderMutation() {
  return useMutation({
    mutationFn: (params: {
      product_id: number;
      vm_name?: string;
      os_image?: string;
      cluster_id?: number;
      cluster_name?: string;
    }) => http.post<{ order: Order }>("/portal/orders", params),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: orderKeys.all }),
  });
}

export function usePayOrderMutation() {
  return useMutation({
    mutationFn: (params: { orderId: number; vm_name?: string; os_image?: string }) =>
      http.post<VMCredentials & { status: string }>(`/portal/orders/${params.orderId}/pay`, {
        vm_name: params.vm_name,
        os_image: params.os_image,
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: orderKeys.all });
      queryClient.invalidateQueries({ queryKey: invoiceKeys.all });
      queryClient.invalidateQueries({ queryKey: vmKeys.all });
      queryClient.invalidateQueries({ queryKey: ["currentUser"] });
    },
  });
}

export function useAdminOrdersQuery(params?: PageParams) {
  return useQuery({
    queryKey: orderKeys.adminList(params),
    queryFn: () =>
      http.get<{ orders: AdminOrder[]; total?: number; limit?: number; offset?: number }>(
        `/admin/orders${pageQueryString(params)}`,
      ),
    refetchInterval: 15_000,
  });
}

export function useAdminInvoicesQuery(params?: PageParams) {
  return useQuery({
    queryKey: invoiceKeys.adminList(params),
    queryFn: () =>
      http.get<{ invoices: AdminInvoice[]; total?: number; limit?: number; offset?: number }>(
        `/admin/invoices${pageQueryString(params)}`,
      ),
    refetchInterval: 30_000,
  });
}

export function useCancelOrderMutation() {
  return useMutation({
    mutationFn: (orderId: number) => http.post(`/portal/orders/${orderId}/cancel`, {}),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: orderKeys.all }),
  });
}
