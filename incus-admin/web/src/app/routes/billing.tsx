import { createFileRoute } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { useState } from "react";
import { toast } from "sonner";
import {
  type Order,
  type VMCredentials,
  useCancelOrderMutation,
  useCreateOrderMutation,
  useMyInvoicesQuery,
  useMyOrdersQuery,
  usePayOrderMutation,
} from "@/features/billing/api";
import { type Product, useProductsQuery } from "@/features/products/api";
import { DEFAULT_OS_IMAGE, OsImagePicker } from "@/features/vms/os-image-picker";
import { formatCurrency } from "@/shared/lib/utils";

export const Route = createFileRoute("/billing")({
  component: BillingPage,
});

function BillingPage() {
  const { t } = useTranslation();
  const [credentials, setCredentials] = useState<VMCredentials | null>(null);

  const { data: ordersData } = useMyOrdersQuery();
  const { data: invoicesData } = useMyInvoicesQuery();
  const { data: productsData } = useProductsQuery();

  const orders = ordersData?.orders ?? [];
  const invoices = invoicesData?.invoices ?? [];
  const products = productsData?.products ?? [];

  return (
    <div>
      <h1 className="text-2xl font-bold mb-6">{t("billing.title")}</h1>

      {credentials && (
        <div className="border border-success/30 bg-success/10 rounded-lg p-4 mb-6">
          <h3 className="font-semibold mb-2">{t("billing.vmCreatedTitle", { defaultValue: "VM Created Successfully" })}</h3>
          <div className="text-sm space-y-1 font-mono">
            <div>Name: {credentials.vm_name}</div>
            <div>IP: {credentials.ip || t("vm.assigning", { defaultValue: "assigning..." })}</div>
            <div>Username: {credentials.username}</div>
            <div>Password: {credentials.password}</div>
          </div>
          <p className="text-xs text-muted-foreground mt-2">{t("billing.saveCredentialsHint", { defaultValue: "Save these credentials — the password will not be shown again." })}</p>
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
                  <th className="text-left px-4 py-2 font-medium">{t("billing.paidAt", { defaultValue: "Paid At" })}</th>
                </tr>
              </thead>
              <tbody>
                {invoices.map((inv) => (
                  <tr key={inv.id} className="border-t border-border">
                    <td className="px-4 py-2">{inv.id}</td>
                    <td className="px-4 py-2">#{inv.order_id}</td>
                    <td className="px-4 py-2 text-right font-mono">{formatCurrency(inv.amount, inv.currency)}</td>
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
  const [osImage, setOsImage] = useState<string>(DEFAULT_OS_IMAGE);
  const [vmName, setVmName] = useState("");
  const [expanded, setExpanded] = useState(false);

  const payMutation = usePayOrderMutation();
  const orderMutation = useCreateOrderMutation();

  const submitOrder = () => {
    orderMutation.mutate(
      { product_id: p.id, vm_name: vmName || undefined, os_image: osImage },
      {
        onSuccess: (data) => {
          payMutation.mutate(
            { orderId: data.order.id, vm_name: vmName || undefined, os_image: osImage },
            { onSuccess: (res) => { if (res.password) onCreated(res); } },
          );
        },
      },
    );
  };

  const isPending = orderMutation.isPending || payMutation.isPending;
  const error = orderMutation.error || payMutation.error;

  return (
    <div className="border border-border rounded-lg bg-card p-4">
      <div className="font-semibold mb-1">{p.name}</div>
      <div className="text-xs text-muted-foreground mb-2">
        {p.cpu}C / {(p.memory_mb / 1024).toFixed(0)}G RAM / {p.disk_gb}G SSD
      </div>
      <div className="text-lg font-bold mb-3">{formatCurrency(p.price_monthly, p.currency)}<span className="text-xs font-normal text-muted-foreground">/mo</span></div>

      {expanded ? (
        <div className="space-y-2 mb-3">
          <OsImagePicker value={osImage} onChange={setOsImage} />
          <input type="text" value={vmName} onChange={(e) => setVmName(e.target.value)}
            placeholder={t("billing.vmNamePlaceholder", { defaultValue: "VM name (optional)" })}
            className="w-full px-2 py-1.5 text-xs rounded border border-border bg-card" />
        </div>
      ) : null}

      <button
        onClick={() => {
          if (!expanded) { setExpanded(true); return; }
          submitOrder();
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
  const payMutation = usePayOrderMutation();
  const cancelMutation = useCancelOrderMutation();

  const colors: Record<string, string> = {
    pending: "bg-warning/20 text-warning",
    paid: "bg-success/20 text-success",
    active: "bg-success/20 text-success",
    provisioned: "bg-success/20 text-success",
    expired: "bg-muted text-muted-foreground",
  };

  return (
    <tr className="border-t border-border">
      <td className="px-4 py-2">{o.id}</td>
      <td className="px-4 py-2 text-right font-mono">{formatCurrency(o.amount, o.currency)}</td>
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
              onClick={() => payMutation.mutate(
                { orderId: o.id },
                { onSuccess: (data) => { if (data.password) onProvisioned(data); } },
              )}
              disabled={payMutation.isPending}
              className="px-3 py-1 text-xs bg-primary text-primary-foreground rounded disabled:opacity-50"
            >
              {payMutation.isPending ? "..." : t("billing.pay")}
            </button>
            <button
              onClick={() => cancelMutation.mutate(o.id, {
                onSuccess: () => toast.success(t("billing.orderCancelled", { defaultValue: "订单已取消" })),
                onError: () => toast.error(t("billing.cancelFailed", { defaultValue: "取消失败" })),
              })}
              disabled={cancelMutation.isPending}
              className="px-3 py-1 text-xs border border-destructive/30 text-destructive rounded hover:bg-destructive/10 disabled:opacity-50"
            >
              {cancelMutation.isPending ? "..." : t("billing.cancel", { defaultValue: "取消" })}
            </button>
          </div>
        )}
        {payMutation.isError && <span className="text-destructive text-xs ml-2">{(payMutation.error as Error).message}</span>}
      </td>
    </tr>
  );
}
