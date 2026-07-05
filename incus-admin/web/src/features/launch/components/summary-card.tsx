import type { Product } from "@/features/products/api";
import { Link } from "@tanstack/react-router";
import { ChevronRight } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useOsImageLabel } from "@/features/vms/os-image-picker";
import { Alert, AlertDescription } from "@/shared/components/ui/alert";
import { Card, CardContent } from "@/shared/components/ui/card";
import { cn, formatCurrency } from "@/shared/lib/utils";

/**
 * SummaryCard —— `/launch` 右栏 sticky 摘要：
 * 选择回顾 + 单价 hero（按周期切换 monthly/daily）+ 余额对比 + 不足时 inline alert。
 */
export function SummaryCard({
  product,
  osImage,
  vmName,
  balance,
  balanceCurrency,
  insufficient,
  period = "monthly",
  unitPrice,
}: {
  product: Product | null;
  osImage: string;
  vmName: string;
  balance: number;
  balanceCurrency: string | undefined;
  insufficient: boolean;
  period?: "daily" | "monthly";
  unitPrice?: number | null;
}) {
  const { t } = useTranslation();
  const osLabel = useOsImageLabel(osImage);
  // PLAN-054：unitPrice 缺失（product 不支持当前 period）时回退到 monthly 单价。
  const heroPrice = unitPrice ?? product?.price_monthly ?? null;
  const heroUnit =
    period === "daily"
      ? t("period.perDay", { defaultValue: "/ 天" })
      : t("billing.perMonth", { defaultValue: "/ 月" });
  const heroLabel =
    period === "daily"
      ? t("period.dailyLabel", { defaultValue: "按日" })
      : t("launch.priceMonthly", { defaultValue: "月费" });
  return (
    <Card>
      <CardContent className="flex flex-col gap-3 p-4">
        <h3 className="text-base font-emphasis text-foreground">
          {t("launch.summaryYourPick", { defaultValue: "你的选择" })}
        </h3>
        <div className="flex flex-col gap-2 text-sm">
          <SummaryRow label={t("launch.planTitle", { defaultValue: "套餐" })} value={product?.name ?? "—"} />
          <SummaryRow
            label={t("vm.config")}
            value={
              product
                ? `${product.cpu} vCPU / ${(product.memory_mb / 1024).toFixed(0)} GB / ${product.disk_gb} GB`
                : "—"
            }
          />
          <SummaryRow label={t("vm.osImage")} value={osLabel ?? "—"} />
          <SummaryRow
            label={t("launch.hostnameTitle", { defaultValue: "主机名" })}
            value={vmName || t("billing.vmNamePlaceholder", { defaultValue: "自动生成" })}
          />
        </div>

        <div className="rounded-md border border-border bg-surface-1 p-3 flex flex-col gap-1.5">
          <div className="text-caption text-text-tertiary font-emphasis">
            {heroLabel}
          </div>
          <div className="flex items-baseline gap-1.5">
            <span className="text-body-emphasis font-strong text-foreground tabular-nums">
              {heroPrice != null ? formatCurrency(heroPrice, product?.currency) : "—"}
            </span>
            <span className="text-caption text-text-tertiary">
              {heroUnit}
            </span>
          </div>
        </div>

        <div className="flex items-center justify-between text-caption">
          <span className="text-text-tertiary">
            {t("launch.balanceLabel", { defaultValue: "当前余额" })}
          </span>
          <span
            className={cn(
              "font-mono tabular-nums",
              insufficient ? "text-status-error" : "text-text-secondary",
            )}
          >
            {formatCurrency(balance, balanceCurrency)}
          </span>
        </div>

        {insufficient ? (
          <Alert variant="error">
            <AlertDescription className="flex items-center gap-2">
              <span>{t("launch.balanceInsufficient", { defaultValue: "余额不足，请先充值。" })}</span>
              <Link
                to="/tickets"
                search={{ subject: "topup" }}
                className="ml-auto inline-flex items-center gap-1 text-accent hover:text-accent-hover"
              >
                {t("billing.topupViaTicket", { defaultValue: "提工单充值" })}
                <ChevronRight size={12} aria-hidden="true" />
              </Link>
            </AlertDescription>
          </Alert>
        ) : null}
      </CardContent>
    </Card>
  );
}

function SummaryRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center justify-between gap-4 py-1.5 border-b border-border last:border-b-0">
      <span className="text-text-tertiary">{label}</span>
      <span className="font-emphasis text-right truncate">{value}</span>
    </div>
  );
}
