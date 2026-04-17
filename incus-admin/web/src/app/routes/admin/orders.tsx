import { createFileRoute } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useAdminOrdersQuery } from "@/features/billing/api";
import { Pagination } from "@/shared/components/ui/pagination";
import type { PageParams } from "@/shared/lib/pagination";
import { formatCurrency } from "@/shared/lib/utils";

export const Route = createFileRoute("/admin/orders")({
  component: AdminOrdersPage,
});

function AdminOrdersPage() {
  const { t } = useTranslation();
  const [page, setPage] = useState<PageParams>({ limit: 50, offset: 0 });
  const { data, isLoading } = useAdminOrdersQuery(page);
  const orders = data?.orders ?? [];
  const total = data?.total ?? orders.length;

  return (
    <div>
      <h1 className="text-2xl font-bold mb-6">{t("admin.ordersTitle")}</h1>

      {isLoading ? (
        <div className="text-muted-foreground">{t("common.loading")}</div>
      ) : orders.length === 0 ? (
        <div className="border border-border rounded-lg p-6 text-center text-muted-foreground">
          {t("common.noData")}
        </div>
      ) : (
        <>
        <div className="border border-border rounded-lg overflow-hidden">
          <table className="w-full text-sm">
            <thead className="bg-muted/30">
              <tr>
                <th className="text-left px-4 py-2 font-medium">#</th>
                <th className="text-left px-4 py-2 font-medium">{t("admin.orderUser")}</th>
                <th className="text-left px-4 py-2 font-medium">{t("admin.orderProduct")}</th>
                <th className="text-right px-4 py-2 font-medium">{t("admin.orderAmount")}</th>
                <th className="text-left px-4 py-2 font-medium">{t("admin.orderStatus")}</th>
                <th className="text-left px-4 py-2 font-medium">{t("admin.orderExpires")}</th>
                <th className="text-left px-4 py-2 font-medium">{t("admin.orderCreatedAt")}</th>
              </tr>
            </thead>
            <tbody>
              {orders.map((o) => (
                <tr key={o.id} className="border-t border-border">
                  <td className="px-4 py-2">{o.id}</td>
                  <td className="px-4 py-2 text-xs">#{o.user_id}</td>
                  <td className="px-4 py-2 text-xs">#{o.product_id}</td>
                  <td className="px-4 py-2 text-right font-mono">{formatCurrency(o.amount, o.currency)}</td>
                  <td className="px-4 py-2">
                    <OrderStatusBadge status={o.status} />
                  </td>
                  <td className="px-4 py-2 text-xs text-muted-foreground">
                    {o.expires_at ? new Date(o.expires_at).toLocaleDateString() : "—"}
                  </td>
                  <td className="px-4 py-2 text-xs text-muted-foreground">
                    {new Date(o.created_at).toLocaleString()}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <Pagination
          total={total}
          limit={page.limit}
          offset={page.offset}
          onChange={(limit, offset) => setPage({ limit, offset })}
          className="mt-3"
        />
        </>
      )}
    </div>
  );
}

function OrderStatusBadge({ status }: { status: string }) {
  const colors: Record<string, string> = {
    pending: "bg-warning/20 text-warning",
    paid: "bg-primary/20 text-primary",
    active: "bg-success/20 text-success",
    expired: "bg-muted text-muted-foreground",
    cancelled: "bg-destructive/20 text-destructive",
  };
  return (
    <span className={`px-2 py-0.5 rounded text-xs font-medium ${colors[status] ?? "bg-muted text-muted-foreground"}`}>
      {status}
    </span>
  );
}
