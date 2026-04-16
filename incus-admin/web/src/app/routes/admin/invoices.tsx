import { createFileRoute } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { http } from "@/shared/lib/http";
import { useTranslation } from "react-i18next";

export const Route = createFileRoute("/admin/invoices")({
  component: AdminInvoicesPage,
});

interface Invoice {
  id: number;
  order_id: number;
  user_id: number;
  amount: number;
  status: string;
  due_at: string | null;
  paid_at: string | null;
  created_at: string;
}

function AdminInvoicesPage() {
  const { t } = useTranslation();

  const { data, isLoading } = useQuery({
    queryKey: ["adminInvoices"],
    queryFn: () => http.get<{ invoices: Invoice[] }>("/admin/invoices"),
  });

  const invoices = data?.invoices ?? [];

  return (
    <div>
      <h1 className="text-2xl font-bold mb-6">
        {t("admin.invoices.title", "发票管理")}
      </h1>

      {isLoading ? (
        <div className="text-muted-foreground">
          {t("common.loading", "加载中...")}
        </div>
      ) : invoices.length === 0 ? (
        <div className="border border-border rounded-lg p-6 text-center text-muted-foreground">
          {t("admin.invoices.empty", "暂无发票记录")}
        </div>
      ) : (
        <div className="border border-border rounded-lg overflow-hidden">
          <table className="w-full text-sm">
            <thead className="bg-muted/30">
              <tr>
                <th className="text-left px-4 py-2 font-medium">ID</th>
                <th className="text-left px-4 py-2 font-medium">
                  {t("admin.invoices.orderId", "订单")}
                </th>
                <th className="text-left px-4 py-2 font-medium">
                  {t("admin.invoices.userId", "用户")}
                </th>
                <th className="text-right px-4 py-2 font-medium">
                  {t("admin.invoices.amount", "金额")}
                </th>
                <th className="text-left px-4 py-2 font-medium">
                  {t("admin.invoices.status", "状态")}
                </th>
                <th className="text-left px-4 py-2 font-medium">
                  {t("admin.invoices.dueAt", "到期日")}
                </th>
                <th className="text-left px-4 py-2 font-medium">
                  {t("admin.invoices.paidAt", "支付日")}
                </th>
                <th className="text-left px-4 py-2 font-medium">
                  {t("admin.invoices.createdAt", "创建时间")}
                </th>
              </tr>
            </thead>
            <tbody>
              {invoices.map((inv) => (
                <tr key={inv.id} className="border-t border-border">
                  <td className="px-4 py-2 font-mono text-xs">#{inv.id}</td>
                  <td className="px-4 py-2 font-mono text-xs">
                    #{inv.order_id}
                  </td>
                  <td className="px-4 py-2 text-xs">{inv.user_id}</td>
                  <td className="px-4 py-2 text-right font-mono">
                    ${inv.amount.toFixed(2)}
                  </td>
                  <td className="px-4 py-2">
                    <InvoiceStatusBadge status={inv.status} />
                  </td>
                  <td className="px-4 py-2 text-xs text-muted-foreground">
                    {inv.due_at ? new Date(inv.due_at).toLocaleDateString() : "-"}
                  </td>
                  <td className="px-4 py-2 text-xs text-muted-foreground">
                    {inv.paid_at
                      ? new Date(inv.paid_at).toLocaleDateString()
                      : "-"}
                  </td>
                  <td className="px-4 py-2 text-xs text-muted-foreground">
                    {new Date(inv.created_at).toLocaleDateString()}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

function InvoiceStatusBadge({ status }: { status: string }) {
  const colors: Record<string, string> = {
    paid: "bg-success/20 text-success",
    pending: "bg-warning/20 text-warning",
    overdue: "bg-destructive/20 text-destructive",
    cancelled: "bg-muted text-muted-foreground",
  };
  return (
    <span
      className={`px-2 py-0.5 rounded text-xs font-medium ${colors[status] ?? "bg-muted text-muted-foreground"}`}
    >
      {status}
    </span>
  );
}
