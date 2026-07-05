import type {
  SubscriptionStatus,
  VMSubscription,
} from "@/features/billing/subscriptions-api";
import { createFileRoute } from "@tanstack/react-router";
import { AlertTriangle, RotateCw } from "lucide-react";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import {
  useAdminReactivateSubscriptionMutation,
  useAdminSubscriptionsQuery,
} from "@/features/billing/subscriptions-api";
import { useAdminProductsQuery } from "@/features/products/api";
import { useAdminUsersQuery } from "@/features/users/api";
import {
  PageContent,
  PageHeader,
  PageShell,
} from "@/shared/components/page/page-shell";
import { Button } from "@/shared/components/ui/button";
import { Card, CardContent } from "@/shared/components/ui/card";
import { useConfirm } from "@/shared/components/ui/confirm-dialog";
import { EmptyState } from "@/shared/components/ui/empty-state";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/shared/components/ui/select";
import { Skeleton } from "@/shared/components/ui/skeleton";
import { StatusPill } from "@/shared/components/ui/status";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/shared/components/ui/table";
import { Tooltip } from "@/shared/components/ui/tooltip";
import { formatError } from "@/shared/lib/http";
import { cn, formatCurrency, formatDate, formatDateTime } from "@/shared/lib/utils";

// PLAN-054 L3-I: 现有 admin/alert-rules + admin/notify-channels 也用 `as never`
// 跨过 generated FileRoutesByPath（参考路由树的自检漏新路径 corner case）。
export const Route = createFileRoute("/admin/subscriptions" as never)({
  component: AdminSubscriptionsPage,
});

type StatusFilter = "all" | SubscriptionStatus;

function AdminSubscriptionsPage() {
  const { t } = useTranslation();
  const confirm = useConfirm();
  const [status, setStatus] = useState<StatusFilter>("all");
  const [userId, setUserId] = useState<number>(0);
  const reactivate = useAdminReactivateSubscriptionMutation();

  const subQuery = useAdminSubscriptionsQuery({
    status: status === "all" ? "" : status,
    userId: userId > 0 ? userId : undefined,
  });
  const usersQuery = useAdminUsersQuery({ limit: 200, offset: 0 });
  const productsQuery = useAdminProductsQuery({ limit: 200, offset: 0 });

  const subs = subQuery.data?.subscriptions ?? [];
  const userEmail = useMemo(() => {
    const m = new Map<number, string>();
    for (const u of usersQuery.data?.users ?? []) m.set(u.id, u.email);
    return m;
  }, [usersQuery.data]);
  const productName = useMemo(() => {
    const m = new Map<number, string>();
    for (const p of productsQuery.data?.products ?? []) m.set(p.id, p.name);
    return m;
  }, [productsQuery.data]);

  const onReactivate = async (sub: VMSubscription) => {
    const ok = await confirm({
      title: t("admin.subscriptions.reactivateTitle", {
        defaultValue: "恢复订阅",
      }),
      message: t("admin.subscriptions.reactivateMessage", {
        defaultValue: "确认手动恢复订阅 #{{id}}？paid_until 将重置为 NOW + 周期。",
        id: sub.id,
      }),
    });
    if (!ok) return;
    reactivate.mutate(sub.id, {
      onSuccess: () =>
        toast.success(
          t("admin.subscriptions.reactivateOk", {
            defaultValue: "订阅 #{{id}} 已恢复",
            id: sub.id,
          }),
        ),
      onError: (e) => toast.error(formatError(e)),
    });
  };

  return (
    <PageShell>
      <PageHeader
        title={t("admin.subscriptions.title", { defaultValue: "订阅管理" })}
        description={t("admin.subscriptions.description", {
          defaultValue: "所有用户的计费订阅。suspended 行可手动恢复（paid_until 重置）。",
        })}
      />
      <PageContent>
        <Card>
          <CardContent className="p-3 flex flex-wrap items-center gap-3">
            <div className="flex items-center gap-2">
              <span className="text-caption text-text-tertiary">
                {t("admin.subscriptions.filterStatus", { defaultValue: "状态" })}
              </span>
              <Select
                value={status}
                onValueChange={(v) => setStatus((v as StatusFilter) ?? "all")}
              >
                <SelectTrigger className="w-input-narrow">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="all">
                    {t("common.all", { defaultValue: "全部" })}
                  </SelectItem>
                  <SelectItem value="active">
                    {t("subscription.statusActive", { defaultValue: "活跃" })}
                  </SelectItem>
                  <SelectItem value="suspended">
                    {t("subscription.statusSuspended", { defaultValue: "已挂起" })}
                  </SelectItem>
                  <SelectItem value="cancelled">
                    {t("subscription.statusCancelled", { defaultValue: "已取消" })}
                  </SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="flex items-center gap-2">
              <span className="text-caption text-text-tertiary">
                {t("admin.subscriptions.filterUser", { defaultValue: "用户" })}
              </span>
              <Select
                value={String(userId)}
                onValueChange={(v) => setUserId(Number(v) || 0)}
              >
                <SelectTrigger className="w-input-medium">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="0">
                    {t("common.all", { defaultValue: "全部" })}
                  </SelectItem>
                  {(usersQuery.data?.users ?? []).map((u) => (
                    <SelectItem key={u.id} value={String(u.id)}>
                      {u.email}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </CardContent>
        </Card>

        {subQuery.isLoading ? (
          <Card>
            <CardContent className="p-4 space-y-2">
              <Skeleton className="h-4 w-full" />
              <Skeleton className="h-4 w-full" />
              <Skeleton className="h-4 w-3/4" />
            </CardContent>
          </Card>
        ) : subs.length === 0 ? (
          <EmptyState
            title={t("admin.subscriptions.empty", { defaultValue: "暂无订阅记录" })}
          />
        ) : (
          <Card className="overflow-x-auto">
            <Table>
              <TableHeader>
                <TableRow className="hover:bg-transparent">
                  <TableHead>#</TableHead>
                  <TableHead>
                    {t("admin.subscriptions.colUser", { defaultValue: "用户" })}
                  </TableHead>
                  <TableHead>
                    {t("admin.subscriptions.colProduct", { defaultValue: "套餐" })}
                  </TableHead>
                  <TableHead>
                    {t("admin.subscriptions.colPeriod", { defaultValue: "周期" })}
                  </TableHead>
                  <TableHead className="text-right">
                    {t("admin.subscriptions.colRate", { defaultValue: "单价" })}
                  </TableHead>
                  <TableHead>
                    {t("admin.subscriptions.colStatus", { defaultValue: "状态" })}
                  </TableHead>
                  <TableHead>
                    {t("admin.subscriptions.colPaidUntil", {
                      defaultValue: "已付至",
                    })}
                  </TableHead>
                  <TableHead className="text-right">
                    {t("vm.actions", { defaultValue: "操作" })}
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {subs.map((s) => (
                  <TableRow
                    key={s.id}
                    className={cn(s.status === "suspended" && "bg-status-error/8")}
                  >
                    <TableCell className="font-mono text-xs">#{s.id}</TableCell>
                    <TableCell className="text-xs">
                      {userEmail.get(s.user_id) ?? `#${s.user_id}`}
                    </TableCell>
                    <TableCell className="text-xs">
                      {productName.get(s.product_id) ?? `#${s.product_id}`}
                    </TableCell>
                    <TableCell>
                      <span className="inline-flex items-center rounded-pill border border-border bg-surface-2 px-2 py-0.5 text-label text-text-secondary">
                        {s.period === "daily"
                          ? t("subscription.periodDaily", { defaultValue: "按日" })
                          : t("subscription.periodMonthly", { defaultValue: "按月" })}
                      </span>
                    </TableCell>
                    <TableCell className="text-right font-mono tabular-nums">
                      {s.period === "daily" && s.daily_rate != null
                        ? formatCurrency(s.daily_rate, "USD")
                        : s.period === "monthly" && s.monthly_rate != null
                          ? formatCurrency(s.monthly_rate, "USD")
                          : "—"}
                    </TableCell>
                    <TableCell>
                      {s.status === "suspended" ? (
                        <Tooltip
                          content={
                            s.grace_until
                              ? t("subscription.suspendedTooltipWithGrace", {
                                  defaultValue:
                                    "余额不足已挂起，宽限至 {{time}}",
                                  time: formatDateTime(s.grace_until),
                                })
                              : t("subscription.suspendedTooltip", {
                                  defaultValue: "余额不足已挂起",
                                })
                          }
                        >
                          <StatusPill status="error" className="cursor-help">
                            <AlertTriangle size={10} aria-hidden="true" />
                            {t("subscription.statusSuspended", {
                              defaultValue: "已挂起",
                            })}
                          </StatusPill>
                        </Tooltip>
                      ) : s.status === "cancelled" ? (
                        <StatusPill status="disabled">
                          {t("subscription.statusCancelled", {
                            defaultValue: "已取消",
                          })}
                        </StatusPill>
                      ) : (
                        <StatusPill status="active">
                          {t("subscription.statusActive", { defaultValue: "活跃" })}
                        </StatusPill>
                      )}
                    </TableCell>
                    <TableCell className="text-xs text-text-tertiary">
                      {s.status === "cancelled" ? "—" : formatDate(s.paid_until)}
                    </TableCell>
                    <TableCell className="text-right">
                      {s.status !== "active" ? (
                        <Button
                          size="sm"
                          variant="primary"
                          disabled={reactivate.isPending}
                          onClick={() => onReactivate(s)}
                          data-testid={`reactivate-sub-${s.id}`}
                        >
                          <RotateCw size={12} aria-hidden="true" />
                          {t("admin.subscriptions.reactivate", {
                            defaultValue: "恢复",
                          })}
                        </Button>
                      ) : null}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </Card>
        )}
      </PageContent>
    </PageShell>
  );
}
