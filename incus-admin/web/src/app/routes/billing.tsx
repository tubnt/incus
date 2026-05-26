import type {Invoice, Order, VMCredentials} from "@/features/billing/api";
import { useQuery } from "@tanstack/react-query";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { CreditCard, FileText, Rocket, ShoppingCart } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import {
  useCancelOrderMutation,
  useMyInvoicesQuery,
  useMyOrdersQuery,
  usePayOrderMutation,
} from "@/features/billing/api";
import { InvoiceDetailDialog } from "@/features/billing/invoice-detail-dialog";
import {
  SubscriptionList,
  useMyVmNameMap,
} from "@/features/billing/subscription-list/subscription-list";
import {
  computeRunwayDays,
  useMySubscriptionsQuery,
} from "@/features/billing/subscriptions-api";
import { useProductsQuery } from "@/features/products/api";
import { useCommandActions } from "@/shared/components/command-palette/use-command-actions";
import {
  PageContent,
  PageHeader,
  PageShell,
} from "@/shared/components/page/page-shell";
import { Button, buttonVariants } from "@/shared/components/ui/button";
import { Card, CardContent } from "@/shared/components/ui/card";
import { EmptyState } from "@/shared/components/ui/empty-state";
import { SecretReveal } from "@/shared/components/ui/secret-reveal";
import { StatusPill } from "@/shared/components/ui/status";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/shared/components/ui/table";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/shared/components/ui/tabs";
import { fetchCurrentUser } from "@/shared/lib/auth";
import { formatError } from "@/shared/lib/http";
import { formatInvoiceStatus, formatOrderStatus } from "@/shared/lib/status-i18n";
import { cn, formatCurrency, formatDate, formatDateTime } from "@/shared/lib/utils";

export const Route = createFileRoute("/billing")({
  component: BillingPage,
});

type BillingTab = "orders" | "subscriptions" | "invoices";

function BillingPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [detailInvoice, setDetailInvoice] = useState<Invoice | null>(null);
  const [credentials, setCredentials] = useState<VMCredentials | null>(null);
  const [tab, setTab] = useState<BillingTab>("orders");

  const { data: user } = useQuery({ queryKey: ["currentUser"], queryFn: fetchCurrentUser });
  const { data: ordersData } = useMyOrdersQuery();
  const { data: invoicesData } = useMyInvoicesQuery();
  const { data: productsData } = useProductsQuery();
  const subscriptionsQuery = useMySubscriptionsQuery();
  const vmNameMap = useMyVmNameMap();

  const orders = ordersData?.orders ?? [];
  const invoices = invoicesData?.invoices ?? [];
  const products = productsData?.products ?? [];
  const subscriptions = subscriptionsQuery.data?.subscriptions ?? [];
  const balance = user?.balance ?? 0;
  const runwayDays = computeRunwayDays(balance, subscriptions);

  useCommandActions(
    () => [
      {
        id: "billing.topup",
        title: t("billing.topupViaTicket", { defaultValue: "提工单充值" }),
        icon: "CreditCard",
        perform: () =>
          navigate({ to: "/tickets", search: { subject: "topup" } }),
      },
    ],
    [navigate, t],
  );

  return (
    <PageShell>
      <PageHeader
        title={t("billing.title")}
        description={t("billing.descriptionAccount", {
          defaultValue: "余额、订单、发票。创建云主机请前往「创建云主机」页。",
        })}
        actions={
          <Link to="/launch" className={cn(buttonVariants({ variant: "primary" }))}>
            <Rocket size={14} aria-hidden="true" />
            {t("launch.title", { defaultValue: "创建云主机" })}
          </Link>
        }
      />
      <PageContent>
        <BalanceCard balance={balance} runwayDays={runwayDays} />

        <InvoiceDetailDialog
          invoice={detailInvoice}
          orders={orders}
          products={products}
          onClose={() => setDetailInvoice(null)}
        />

        {credentials ? (
          <Card className="border-status-success/30 bg-status-success/8">
            <CardContent className="p-4 space-y-3">
              <div className="flex items-center gap-2 text-status-success font-strong">
                <Rocket size={16} aria-hidden="true" />
                {t("billing.vmCreatedTitle", { defaultValue: "VM 创建成功" })}
              </div>
              <div className="space-y-2">
                <SecretReveal label="Name" value={credentials.vm_name} inline={false} />
                <SecretReveal
                  label="IP"
                  value={credentials.ip || t("vm.assigning", { defaultValue: "分配中..." })}
                  inline={false}
                  autoMaskMs={0}
                />
                <SecretReveal label="Username" value={credentials.username} inline={false} />
                <SecretReveal label="Password" value={credentials.password} inline={false} />
              </div>
              <div className="flex justify-end">
                <Button variant="ghost" onClick={() => setCredentials(null)}>
                  {t("common.ok", { defaultValue: "好的" })}
                </Button>
              </div>
            </CardContent>
          </Card>
        ) : null}

        <Tabs
          value={tab}
          onValueChange={(v) => setTab((v as BillingTab) ?? "orders")}
        >
          <TabsList>
            <TabsTrigger value="orders">
              {t("billing.orders", { defaultValue: "我的订单" })}
            </TabsTrigger>
            <TabsTrigger value="subscriptions">
              {t("subscription.tabTitle", { defaultValue: "订阅" })}
            </TabsTrigger>
            <TabsTrigger value="invoices">
              {t("billing.invoices", { defaultValue: "发票" })}
            </TabsTrigger>
          </TabsList>

          <TabsContent value="orders" className="space-y-3">
            {orders.length === 0 ? (
              <EmptyState icon={ShoppingCart} title={t("common.noData")} />
            ) : (
              <div className="rounded-lg border border-border bg-surface-1 overflow-x-auto">
                <Table>
                  <TableHeader>
                    <TableRow className="hover:bg-transparent">
                      <TableHead>#</TableHead>
                      <TableHead className="text-right">{t("billing.amount")}</TableHead>
                      <TableHead>{t("billing.status")}</TableHead>
                      <TableHead>{t("billing.expires")}</TableHead>
                      <TableHead className="text-right">{t("vm.actions")}</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {orders.map((o) => (
                      <OrderRow key={o.id} order={o} onProvisioned={setCredentials} />
                    ))}
                  </TableBody>
                </Table>
              </div>
            )}
          </TabsContent>

          <TabsContent value="subscriptions" className="space-y-3">
            <SubscriptionList
              subscriptions={subscriptions}
              vmNameById={vmNameMap}
              isLoading={subscriptionsQuery.isLoading}
            />
          </TabsContent>

          <TabsContent value="invoices" className="space-y-3">
            {invoices.length === 0 ? (
              <EmptyState icon={FileText} title={t("common.noData")} />
            ) : (
              <div className="rounded-lg border border-border bg-surface-1 overflow-x-auto">
                <Table>
                  <TableHeader>
                    <TableRow className="hover:bg-transparent">
                      <TableHead>#</TableHead>
                      <TableHead>{t("invoice.order", { defaultValue: "Order" })}</TableHead>
                      <TableHead className="text-right">{t("invoice.amount", { defaultValue: "Amount" })}</TableHead>
                      <TableHead>{t("invoice.status", { defaultValue: "Status" })}</TableHead>
                      <TableHead>{t("invoice.paidAt", { defaultValue: "支付时间" })}</TableHead>
                      <TableHead className="text-right">{t("vm.actions")}</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {invoices.map((inv) => (
                      <TableRow key={inv.id}>
                        <TableCell>{inv.id}</TableCell>
                        <TableCell>#{inv.order_id}</TableCell>
                        <TableCell className="text-right font-mono">
                          {formatCurrency(inv.amount, inv.currency)}
                        </TableCell>
                        <TableCell>
                          <StatusPill status="success">{formatInvoiceStatus(t, inv.status)}</StatusPill>
                        </TableCell>
                        <TableCell className="text-caption text-text-tertiary">
                          {inv.paid_at ? formatDateTime(inv.paid_at) : "—"}
                        </TableCell>
                        <TableCell className="text-right">
                          <Button
                            size="sm"
                            variant="ghost"
                            onClick={() => setDetailInvoice(inv)}
                          >
                            {t("invoice.detail", { defaultValue: "详情" })}
                          </Button>
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            )}
          </TabsContent>
        </Tabs>
      </PageContent>
    </PageShell>
  );
}

function BalanceCard({
  balance,
  runwayDays,
}: {
  balance: number;
  runwayDays: number | null;
}) {
  const { t } = useTranslation();
  const isLow = runwayDays != null && runwayDays < 7;
  return (
    <Card>
      <CardContent className="p-4 flex flex-col md:flex-row md:items-center md:justify-between gap-3">
        <div className="space-y-1">
          <div className="text-caption text-text-tertiary uppercase tracking-wide font-emphasis">
            {t("billing.balance", { defaultValue: "账户余额" })}
          </div>
          <div className="text-body-emphasis font-emphasis font-mono mt-1 tabular-nums">
            ${balance.toFixed(2)}
          </div>
          {runwayDays != null ? (
            <div
              className={cn(
                "text-caption font-emphasis",
                isLow ? "text-status-warning" : "text-text-secondary",
              )}
            >
              {t("billing.runwayHint", {
                defaultValue: "按当前消费速率可跑 ~{{n}} 天",
                n: Math.max(0, Math.floor(runwayDays)),
              })}
            </div>
          ) : null}
          <div className="text-caption text-text-tertiary">
            {t("billing.topupHint", {
              defaultValue: "当前尚未开放自助充值。如需充值请提交工单联系管理员。",
            })}
          </div>
        </div>
        <Link
          to="/tickets"
          search={{ subject: "topup" }}
          className={cn(buttonVariants({ variant: "primary" }))}
        >
          <CreditCard size={14} aria-hidden="true" />
          {t("billing.topupViaTicket", { defaultValue: "提工单充值" })}
        </Link>
      </CardContent>
    </Card>
  );
}

function OrderRow({
  order: o,
  onProvisioned,
}: {
  order: Order;
  onProvisioned: (c: VMCredentials) => void;
}) {
  const { t } = useTranslation();
  const payMutation = usePayOrderMutation();
  const cancelMutation = useCancelOrderMutation();

  const status = (() => {
    switch (o.status) {
      case "paid":
      case "active":
      case "provisioned":
        return "success" as const;
      case "pending":
        return "pending" as const;
      case "provisioning":
        return "pending" as const;
      case "expired":
        return "stale" as const;
      default:
        return "disabled" as const;
    }
  })();

  return (
    <TableRow>
      <TableCell>{o.id}</TableCell>
      <TableCell className="text-right font-mono tabular-nums">
        {formatCurrency(o.amount, o.currency)}
      </TableCell>
      <TableCell>
        <StatusPill status={status}>{formatOrderStatus(t, o.status)}</StatusPill>
      </TableCell>
      <TableCell className="text-caption text-text-tertiary">
        {o.expires_at ? formatDate(o.expires_at) : "—"}
      </TableCell>
      <TableCell className="text-right">
        {o.status === "pending" ? (
          <div className="flex justify-end gap-1.5">
            <Button
              size="sm"
              variant="primary"
              disabled={payMutation.isPending}
              onClick={() =>
                payMutation.mutate(
                  { orderId: o.id },
                  {
                    onSuccess: (data) => {
                      if (data.password && data.vm_name) {
                        onProvisioned({
                          vm_name: data.vm_name,
                          ip: data.ip ?? "",
                          username: data.username ?? "ubuntu",
                          password: data.password,
                        });
                      }
                    },
                  },
                )
              }
            >
              {payMutation.isPending ? "..." : t("billing.pay")}
            </Button>
            <Button
              size="sm"
              variant="outline"
              disabled={cancelMutation.isPending}
              onClick={() =>
                cancelMutation.mutate(o.id, {
                  onSuccess: () =>
                    toast.success(t("billing.orderCancelled", { defaultValue: "订单已取消" })),
                  onError: () =>
                    toast.error(t("billing.cancelFailed", { defaultValue: "取消失败" })),
                })
              }
            >
              {cancelMutation.isPending ? "..." : t("billing.cancel", { defaultValue: "取消" })}
            </Button>
          </div>
        ) : null}
        {payMutation.isError && o.status === "pending" ? (
          <span className="ml-2 text-caption text-status-error">
            {formatError(payMutation.error)}
          </span>
        ) : null}
      </TableCell>
    </TableRow>
  );
}
