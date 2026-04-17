import { useMutation, useQuery } from "@tanstack/react-query";
import { http } from "@/shared/lib/http";
import { pageKeyPart, pageQueryString, type PageParams } from "@/shared/lib/pagination";
import { queryClient } from "@/shared/lib/query-client";

export interface Product {
  id: number;
  name: string;
  slug: string;
  cpu: number;
  memory_mb: number;
  disk_gb: number;
  bandwidth_tb: number;
  price_monthly: number;
  currency?: string;
  access: string;
  active: boolean;
  sort_order: number;
}

export type ProductFormData = Omit<Product, "id">;
// ProductPatch mirrors the backend PATCH DTO: every field is optional.
// Fields omitted from the payload are not touched on the server.
export type ProductPatch = Partial<ProductFormData>;

export const productKeys = {
  all: ["product"] as const,
  portalList: () => [...productKeys.all, "list", "portal"] as const,
  adminList: (params?: PageParams) =>
    [...productKeys.all, "list", "admin", pageKeyPart(params)] as const,
};

export function useProductsQuery() {
  return useQuery({
    queryKey: productKeys.portalList(),
    queryFn: () => http.get<{ products: Product[] }>("/portal/products"),
  });
}

export function useAdminProductsQuery(params?: PageParams) {
  return useQuery({
    queryKey: productKeys.adminList(params),
    queryFn: () =>
      http.get<{ products: Product[]; total?: number; limit?: number; offset?: number }>(
        `/admin/products${pageQueryString(params)}`,
      ),
  });
}

export function useCreateProductMutation() {
  return useMutation({
    mutationFn: (data: ProductFormData) => http.post("/admin/products", data),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: productKeys.all }),
  });
}

export function useUpdateProductMutation(productId: number) {
  return useMutation({
    mutationFn: (data: ProductPatch) =>
      http.put(`/admin/products/${productId}`, data),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: productKeys.all }),
  });
}
