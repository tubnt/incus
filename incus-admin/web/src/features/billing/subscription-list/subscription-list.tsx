import type { VMSubscription } from "@/features/billing/subscriptions-api";
import { AlertTriangle, ListChecks } from "lucide-react";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { runwayDaysFromNow } from "@/features/billing/subscriptions-api";
import { useMyVMsQuery } from "@/features/vms/api";
import { EmptyState } from "@/shared/components/ui/empty-state";
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
import { cn, formatCurrency, formatDate, formatDateTime } from "@/shared/lib/utils";

const STATUS_PRIORITY: Record<VMSubscription["status"], number> = {
  suspended: 0,
  active: 1,
  cancelled: 2,
};

export function SubscriptionList({
  subscriptions,
  vmNameById,
  isLoading,
}: {
  subscriptions: readonly VMSubscription[];
  vmNameById?: Map<number, string>;
  isLoading?: boolean;
}) {
  const { t } = useTranslation();

  const sorted = useMemo(() => {
    return [...subscriptions].sort((a, b) => {
      const da = STATUS_PRIORITY[a.status] ?? 99;
      const db = STATUS_PRIORITY[b.status] ?? 99;
      if (da !== db) return da - db;
      return b.id - a.id;
    });
  }, [subscriptions]);

  if (isLoading) {
    return (
      <div className="rounded-lg border border-border bg-surface-1 p-6 text-center text-caption text-text-tertiary">
        {t("common.loading", { defaultValue: "加载中…" })}
      </div>
    );
  }

  if (sorted.length === 0) {
    return (
      <EmptyState
        icon={ListChecks}
        title={t("subscription.emptyTitle", { defaultValue: "暂无订阅" })}
        description={t("subscription.emptyDescription", {
          defaultValue: "新创建的云主机会自动出现在这里。",
        })}
      />
    );
  }

  return (
    <div className="rounded-lg border border-border bg-surface-1 overflow-x-auto">
      <Table>
        <TableHeader>
          <TableRow className="hover:bg-transparent">
            <TableHead>{t("subscription.colVm", { defaultValue: "VM" })}</TableHead>
            <TableHead>{t("subscription.colPeriod", { defaultValue: "周期" })}</TableHead>
            <TableHead className="text-right">
              {t("subscription.colRate", { defaultValue: "单价" })}
            </TableHead>
            <TableHead>{t("subscription.colStatus", { defaultValue: "状态" })}</TableHead>
            <TableHead>{t("subscription.colPaidUntil", { defaultValue: "已付至" })}</TableHead>
            <TableHead className="text-right">
              {t("subscription.colRunway", { defaultValue: "剩余天数" })}
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {sorted.map((s) => (
            <SubscriptionRow key={s.id} sub={s} vmName={vmNameById?.get(s.vm_id)} />
          ))}
        </TableBody>
      </Table>
    </div>
  );
}

function SubscriptionRow({
  sub,
  vmName,
}: {
  sub: VMSubscription;
  vmName?: string;
}) {
  const { t } = useTranslation();
  const rate =
    sub.period === "daily"
      ? sub.daily_rate ?? null
      : sub.monthly_rate ?? null;
  const rateLabel = rate != null ? formatCurrency(rate, "USD") : "—";
  const perLabel =
    sub.period === "daily"
      ? t("subscription.perDay", { defaultValue: "/ 天" })
      : t("subscription.perMonth", { defaultValue: "/ 月" });
  const days = runwayDaysFromNow(sub.paid_until);
  const isLow = sub.status === "active" && days < 7;

  return (
    <TableRow
      data-testid={`sub-row-${sub.id}`}
      className={cn(sub.status === "suspended" && "bg-status-error/8")}
    >
      <TableCell className="font-mono text-xs">
        {vmName ?? `#${sub.vm_id}`}
      </TableCell>
      <TableCell>
        <PeriodChip period={sub.period} />
      </TableCell>
      <TableCell className="text-right font-mono tabular-nums">
        {rateLabel}{" "}
        <span className="text-caption text-text-tertiary">{perLabel}</span>
      </TableCell>
      <TableCell>
        <SubscriptionStatusPill sub={sub} />
      </TableCell>
      <TableCell className="text-caption text-text-tertiary">
        {sub.status === "cancelled" ? "—" : formatDate(sub.paid_until)}
      </TableCell>
      <TableCell
        className={cn(
          "text-right font-mono tabular-nums",
          isLow && "text-status-warning",
        )}
      >
        {sub.status === "cancelled"
          ? "—"
          : t("subscription.daysValue", { defaultValue: "{{n}} 天", n: days })}
      </TableCell>
    </TableRow>
  );
}

function PeriodChip({ period }: { period: VMSubscription["period"] }) {
  const { t } = useTranslation();
  const label =
    period === "daily"
      ? t("subscription.periodDaily", { defaultValue: "按日" })
      : t("subscription.periodMonthly", { defaultValue: "按月" });
  return (
    <span className="inline-flex items-center rounded-pill border border-border bg-surface-2 px-2 py-0.5 text-label text-text-secondary">
      {label}
    </span>
  );
}

function SubscriptionStatusPill({ sub }: { sub: VMSubscription }) {
  const { t } = useTranslation();
  if (sub.status === "suspended") {
    const tip =
      sub.grace_until != null
        ? t("subscription.suspendedTooltipWithGrace", {
            defaultValue: "余额不足已挂起，宽限至 {{time}}",
            time: formatDateTime(sub.grace_until),
          })
        : t("subscription.suspendedTooltip", {
            defaultValue: "余额不足已挂起，请尽快充值",
          });
    return (
      <Tooltip content={tip}>
        <StatusPill status="error" className="cursor-help">
          <AlertTriangle size={10} aria-hidden="true" />
          {t("subscription.statusSuspended", { defaultValue: "已挂起" })}
        </StatusPill>
      </Tooltip>
    );
  }
  if (sub.status === "cancelled") {
    return (
      <StatusPill status="disabled">
        {t("subscription.statusCancelled", { defaultValue: "已取消" })}
      </StatusPill>
    );
  }
  return (
    <StatusPill status="active">
      {t("subscription.statusActive", { defaultValue: "活跃" })}
    </StatusPill>
  );
}

// VmNameMap —— 用 useMyVMsQuery 拉自己的 VM 列表，构造 id→name map 给列表用。
export function useMyVmNameMap(): Map<number, string> {
  const vmsQuery = useMyVMsQuery();
  return useMemo(() => {
    const m = new Map<number, string>();
    for (const v of vmsQuery.data?.vms ?? []) m.set(v.id, v.name);
    return m;
  }, [vmsQuery.data]);
}
