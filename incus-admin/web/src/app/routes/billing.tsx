import { createFileRoute } from "@tanstack/react-router";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { http } from "@/shared/lib/http";
import { queryClient } from "@/shared/lib/query-client";

export const Route = createFileRoute("/billing")({
  component: BillingPage,
});

interface Order {
  id: number;
  product_id: number;
  status: string;
  amount: number;
  expires_at: string | null;
  created_at: string;
}

interface Invoice {
  id: number;
  order_id: number;
  amount: number;
  status: string;
  paid_at: string | null;
  created_at: string;
}

interface Product {
  id: number;
  name: string;
  slug: string;
  cpu: number;
  memory_mb: number;
  disk_gb: number;
  price_monthly: number;
}

function BillingPage() {
  const { t } = useTranslation();
  const { data: ordersData } = useQuery({
    queryKey: ["myOrders"],
    queryFn: () => http.get<{ orders: Order[] }>("/portal/orders"),
  });

  const { data: invoicesData } = useQuery({
    queryKey: ["myInvoices"],
    queryFn: () => http.get<{ invoices: Invoice[] }>("/portal/invoices"),
  });

  const { data: productsData } = useQuery({
    queryKey: ["products"],
    queryFn: () => http.get<{ products: Product[] }>("/portal/products"),
  });

  const orders = ordersData?.orders ?? [];
  const invoices = invoicesData?.invoices ?? [];
  const products = productsData?.products ?? [];

  return (
    <div>
      <h1 className="text-2xl font-bold mb-6">{t("billing.title")}</h1>

      <div className="mb-8">
        <h2 className="text-lg font-semibold mb-3">{t("billing.products")}</h2>
        {products.length === 0 ? (
          <div className="border border-border rounded-lg p-4 text-center text-muted-foreground text-sm">{t("common.noData")}</div>
        ) : (
          <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
            {products.map((p) => (
              <ProductCard key={p.id} product={p} />
            ))}
          </div>
        )}
      </div>

      <div className="mb-8">
        <h2 className="text-lg font-semibold mb-3">{t("billing.orders")}</h2>
        {orders.length === 0 ? (
          <div className="border border-border rounded-lg p-4 text-center text-muted-foreground text-sm">{t("common.noData")}</div>
        ) : (
          <div className="border border-border rounded-lg overflow-hidden">
            <table className="w-full text-sm">
              <thead className="bg-muted/30">
                <tr>
                  <th className="text-left px-4 py-2 font-medium">#</th>
                  <th className="text-right px-4 py-2 font-medium">{t("billing.amount")}</th>
                  <th className="text-left px-4 py-2 font-medium">{t("billing.status")}</th>
                  <th className="text-left px-4 py-2 font-medium">{t("billing.expires")}</th>
                  <th className="text-right px-4 py-2 font-medium">{t("vm.actions")}</th>
                </tr>
              </thead>
              <tbody>
                {orders.map((o) => (
                  <OrderRow key={o.id} order={o} />
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      <div>
        <h2 className="text-lg font-semibold mb-3">{t("billing.invoices")}</h2>
        {invoices.length === 0 ? (
          <div className="border border-border rounded-lg p-4 text-center text-muted-foreground text-sm">{t("common.noData")}</div>
        ) : (
          <div className="border border-border rounded-lg overflow-hidden">
            <table className="w-full text-sm">
              <thead className="bg-muted/30">
                <tr>
                  <th className="text-left px-4 py-2 font-medium">#</th>
                  <th className="text-left px-4 py-2 font-medium">{t("billing.orders")}</th>
                  <th className="text-right px-4 py-2 font-medium">{t("billing.amount")}</th>
                  <th className="text-left px-4 py-2 font-medium">{t("billing.status")}</th>
                  <th className="text-left px-4 py-2 font-medium">Paid At</th>
                </tr>
              </thead>
              <tbody>
                {invoices.map((inv) => (
                  <tr key={inv.id} className="border-t border-border">
                    <td className="px-4 py-2">{inv.id}</td>
                    <td className="px-4 py-2">#{inv.order_id}</td>
                    <td className="px-4 py-2 text-right font-mono">${inv.amount.toFixed(2)}</td>
                    <td className="px-4 py-2">
                      <span className="px-2 py-0.5 rounded text-xs font-medium bg-success/20 text-success">{inv.status}</span>
                    </td>
                    <td className="px-4 py-2 text-xs text-muted-foreground">
                      {inv.paid_at ? new Date(inv.paid_at).toLocaleString() : "—"}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  );
}

function ProductCard({ product: p }: { product: Product }) {
  const { t } = useTranslation();
  const mutation = useMutation({
    mutationFn: () => http.post("/portal/orders", { product_id: p.id, cluster_id: 1 }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["myOrders"] }),
  });

  return (
    <div className="border border-border rounded-lg bg-card p-4">
      <div className="font-semibold mb-1">{p.name}</div>
      <div className="text-xs text-muted-foreground mb-2">
        {p.cpu}C / {(p.memory_mb / 1024).toFixed(0)}G RAM / {p.disk_gb}G SSD
      </div>
      <div className="text-lg font-bold mb-3">${p.price_monthly.toFixed(2)}<span className="text-xs font-normal text-muted-foreground">/mo</span></div>
      <button
        onClick={() => mutation.mutate()}
        disabled={mutation.isPending}
        className="w-full py-2 bg-primary text-primary-foreground rounded text-sm font-medium disabled:opacity-50"
      >
        {mutation.isPending ? "..." : t("billing.buy")}
      </button>
      {mutation.isError && <div className="text-destructive text-xs mt-1">{(mutation.error as Error).message}</div>}
    </div>
  );
}

function OrderRow({ order: o }: { order: Order }) {
  const { t } = useTranslation();
  const payMutation = useMutation({
    mutationFn: () => http.post(`/portal/orders/${o.id}/pay`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["myOrders"] });
      queryClient.invalidateQueries({ queryKey: ["myInvoices"] });
      queryClient.invalidateQueries({ queryKey: ["currentUser"] });
    },
  });

  const colors: Record<string, string> = {
    pending: "bg-yellow-500/20 text-yellow-600",
    paid: "bg-success/20 text-success",
    active: "bg-success/20 text-success",
    expired: "bg-muted text-muted-foreground",
  };

  return (
    <tr className="border-t border-border">
      <td className="px-4 py-2">{o.id}</td>
      <td className="px-4 py-2 text-right font-mono">${o.amount.toFixed(2)}</td>
      <td className="px-4 py-2">
        <span className={`px-2 py-0.5 rounded text-xs font-medium ${colors[o.status] ?? "bg-muted text-muted-foreground"}`}>{o.status}</span>
      </td>
      <td className="px-4 py-2 text-xs text-muted-foreground">
        {o.expires_at ? new Date(o.expires_at).toLocaleDateString() : "—"}
      </td>
      <td className="px-4 py-2 text-right">
        {o.status === "pending" && (
          <button
            onClick={() => payMutation.mutate()}
            disabled={payMutation.isPending}
            className="px-3 py-1 text-xs bg-primary text-primary-foreground rounded disabled:opacity-50"
          >
            {payMutation.isPending ? "..." : t("billing.pay")}
          </button>
        )}
        {payMutation.isError && <span className="text-destructive text-xs ml-2">{(payMutation.error as Error).message}</span>}
      </td>
    </tr>
  );
}
