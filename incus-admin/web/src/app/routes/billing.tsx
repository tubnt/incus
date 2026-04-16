import { createFileRoute } from "@tanstack/react-router";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { useState } from "react";
import { toast } from "sonner";
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

interface VMCredentials {
  vm_name: string;
  ip: string;
  username: string;
  password: string;
}

const OS_IMAGES = [
  { value: "images:ubuntu/24.04/cloud", label: "Ubuntu 24.04 LTS" },
  { value: "images:ubuntu/22.04/cloud", label: "Ubuntu 22.04 LTS" },
  { value: "images:debian/12/cloud", label: "Debian 12" },
  { value: "images:rockylinux/9/cloud", label: "Rocky Linux 9" },
];

function BillingPage() {
  const { t } = useTranslation();
  const [credentials, setCredentials] = useState<VMCredentials | null>(null);

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

      {credentials && (
        <div className="border border-success/30 bg-success/10 rounded-lg p-4 mb-6">
          <h3 className="font-semibold mb-2">VM Created Successfully</h3>
          <div className="text-sm space-y-1 font-mono">
            <div>Name: {credentials.vm_name}</div>
            <div>IP: {credentials.ip || "assigning..."}</div>
            <div>Username: {credentials.username}</div>
            <div>Password: {credentials.password}</div>
          </div>
          <p className="text-xs text-muted-foreground mt-2">Save these credentials — the password will not be shown again.</p>
          <button onClick={() => setCredentials(null)}
            className="mt-3 px-4 py-2 bg-primary text-primary-foreground rounded text-sm">
            OK
          </button>
        </div>
      )}

      <div className="mb-8">
        <h2 className="text-lg font-semibold mb-3">{t("billing.products")}</h2>
        {products.length === 0 ? (
          <div className="border border-border rounded-lg p-4 text-center text-muted-foreground text-sm">{t("common.noData")}</div>
        ) : (
          <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
            {products.map((p) => (
              <ProductCard key={p.id} product={p} onCreated={setCredentials} />
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
                  <OrderRow key={o.id} order={o} onProvisioned={setCredentials} />
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

function ProductCard({ product: p, onCreated }: { product: Product; onCreated: (c: VMCredentials) => void }) {
  const { t } = useTranslation();
  const [osImage, setOsImage] = useState(OS_IMAGES[0]!.value);
  const [vmName, setVmName] = useState("");
  const [expanded, setExpanded] = useState(false);

  const orderMutation = useMutation({
    mutationFn: () => http.post<{ order: Order }>("/portal/orders", {
      product_id: p.id,
      vm_name: vmName || undefined,
      os_image: osImage,
    }),
    onSuccess: (data) => {
      queryClient.invalidateQueries({ queryKey: ["myOrders"] });
      payMutation.mutate(data.order.id);
    },
  });

  const payMutation = useMutation({
    mutationFn: (orderId: number) => http.post<VMCredentials & { status: string }>(`/portal/orders/${orderId}/pay`, {
      vm_name: vmName || undefined,
      os_image: osImage,
    }),
    onSuccess: (data) => {
      queryClient.invalidateQueries({ queryKey: ["myOrders"] });
      queryClient.invalidateQueries({ queryKey: ["myInvoices"] });
      queryClient.invalidateQueries({ queryKey: ["myServices"] });
      queryClient.invalidateQueries({ queryKey: ["currentUser"] });
      if (data.password) onCreated(data);
    },
  });

  const isPending = orderMutation.isPending || payMutation.isPending;
  const error = orderMutation.error || payMutation.error;

  return (
    <div className="border border-border rounded-lg bg-card p-4">
      <div className="font-semibold mb-1">{p.name}</div>
      <div className="text-xs text-muted-foreground mb-2">
        {p.cpu}C / {(p.memory_mb / 1024).toFixed(0)}G RAM / {p.disk_gb}G SSD
      </div>
      <div className="text-lg font-bold mb-3">${p.price_monthly.toFixed(2)}<span className="text-xs font-normal text-muted-foreground">/mo</span></div>

      {expanded ? (
        <div className="space-y-2 mb-3">
          <select value={osImage} onChange={(e) => setOsImage(e.target.value)}
            className="w-full px-2 py-1.5 text-xs rounded border border-border bg-card">
            {OS_IMAGES.map((img) => <option key={img.value} value={img.value}>{img.label}</option>)}
          </select>
          <input type="text" value={vmName} onChange={(e) => setVmName(e.target.value)}
            placeholder="VM name (optional)"
            className="w-full px-2 py-1.5 text-xs rounded border border-border bg-card" />
        </div>
      ) : null}

      <button
        onClick={() => {
          if (!expanded) { setExpanded(true); return; }
          orderMutation.mutate();
        }}
        disabled={isPending}
        className="w-full py-2 bg-primary text-primary-foreground rounded text-sm font-medium disabled:opacity-50"
      >
        {isPending ? "..." : expanded ? t("billing.pay") : t("billing.buy")}
      </button>
      {error && <div className="text-destructive text-xs mt-1">{(error as Error).message}</div>}
    </div>
  );
}

function OrderRow({ order: o, onProvisioned }: { order: Order; onProvisioned: (c: VMCredentials) => void }) {
  const { t } = useTranslation();
  const payMutation = useMutation({
    mutationFn: () => http.post<VMCredentials & { status: string }>(`/portal/orders/${o.id}/pay`),
    onSuccess: (data) => {
      queryClient.invalidateQueries({ queryKey: ["myOrders"] });
      queryClient.invalidateQueries({ queryKey: ["myInvoices"] });
      queryClient.invalidateQueries({ queryKey: ["currentUser"] });
      queryClient.invalidateQueries({ queryKey: ["myServices"] });
      if (data.password) onProvisioned(data);
    },
  });
  const cancelMutation = useMutation({
    mutationFn: () => http.post(`/portal/orders/${o.id}/cancel`, {}),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["myOrders"] });
      toast.success(t("billing.orderCancelled", "订单已取消"));
    },
    onError: () => toast.error(t("billing.cancelFailed", "取消失败")),
  });

  const colors: Record<string, string> = {
    pending: "bg-yellow-500/20 text-yellow-600",
    paid: "bg-success/20 text-success",
    active: "bg-success/20 text-success",
    provisioned: "bg-success/20 text-success",
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
          <div className="flex justify-end gap-1">
            <button
              onClick={() => payMutation.mutate()}
              disabled={payMutation.isPending}
              className="px-3 py-1 text-xs bg-primary text-primary-foreground rounded disabled:opacity-50"
            >
              {payMutation.isPending ? "..." : t("billing.pay")}
            </button>
            <button
              onClick={() => cancelMutation.mutate()}
              disabled={cancelMutation.isPending}
              className="px-3 py-1 text-xs border border-destructive/30 text-destructive rounded hover:bg-destructive/10 disabled:opacity-50"
            >
              {cancelMutation.isPending ? "..." : t("billing.cancel", "取消")}
            </button>
          </div>
        )}
        {payMutation.isError && <span className="text-destructive text-xs ml-2">{(payMutation.error as Error).message}</span>}
      </td>
    </tr>
  );
}
