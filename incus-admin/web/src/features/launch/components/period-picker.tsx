import type { Product } from "@/features/products/api";
import { useTranslation } from "react-i18next";
import { Tooltip } from "@/shared/components/ui/tooltip";
import { cn, formatCurrency } from "@/shared/lib/utils";

export type Period = "daily" | "monthly";

// productSupports —— product.period_supported 是空/undefined 时按历史约定
// 当作 ['monthly']（兼容旧产品行）。
export function productSupports(product: Product | null, period: Period): boolean {
  if (!product) return false;
  const supported = product.period_supported && product.period_supported.length > 0
    ? product.period_supported
    : ["monthly"];
  return supported.includes(period);
}

/** PeriodPicker —— /launch ② 段选完套餐后，按月 / 按日 二选一。 */
export function PeriodPicker({
  product,
  value,
  onChange,
}: {
  product: Product | null;
  value: Period;
  onChange: (p: Period) => void;
}) {
  const { t } = useTranslation();
  const supportsDaily = productSupports(product, "daily");
  const supportsMonthly = productSupports(product, "monthly");
  return (
    <div className="grid grid-cols-1 sm:grid-cols-2 gap-2" role="radiogroup" aria-label={t("period.title", { defaultValue: "计费周期" })}>
      <PeriodOption
        period="monthly"
        active={value === "monthly"}
        disabled={!supportsMonthly}
        onSelect={() => onChange("monthly")}
        label={t("period.monthlyLabel", { defaultValue: "按月" })}
        priceLabel={
          product?.price_monthly != null
            ? formatCurrency(product.price_monthly, product.currency)
            : "—"
        }
        unitLabel={t("billing.perMonth", { defaultValue: "/ 月" })}
        hint={t("period.monthlyHint", {
          defaultValue: "一次扣 30 天费用，期间随时停机不退。",
        })}
      />
      <PeriodOption
        period="daily"
        active={value === "daily"}
        disabled={!supportsDaily}
        onSelect={() => onChange("daily")}
        label={t("period.dailyLabel", { defaultValue: "按日" })}
        priceLabel={
          product?.price_daily != null
            ? formatCurrency(product.price_daily, product.currency)
            : "—"
        }
        unitLabel={t("period.perDay", { defaultValue: "/ 天" })}
        hint={t("period.dailyHint", {
          defaultValue: "每天 UTC 00:00 自动扣 1 天，余额不足挂起。",
        })}
      />
    </div>
  );
}

function PeriodOption({
  period: _period,
  active,
  disabled,
  onSelect,
  label,
  priceLabel,
  unitLabel,
  hint,
}: {
  period: Period;
  active: boolean;
  disabled: boolean;
  onSelect: () => void;
  label: string;
  priceLabel: string;
  unitLabel: string;
  hint: string;
}) {
  const { t } = useTranslation();
  const btn = (
    <button
      type="button"
      role="radio"
      aria-checked={active}
      aria-disabled={disabled}
      disabled={disabled}
      onClick={() => {
        if (!disabled) onSelect();
      }}
      data-testid={`period-${_period}`}
      className={cn(
        "relative flex flex-col gap-2 p-3 rounded-lg border-2 text-left transition-colors w-full",
        disabled && "opacity-50 cursor-not-allowed",
        !disabled && (active
          ? "border-primary bg-primary/15 shadow-sm"
          : "border-border bg-surface-1 hover:bg-surface-2"),
      )}
    >
      <div className="font-strong text-body text-foreground">{label}</div>
      <div className="flex items-baseline gap-1">
        <span className="text-body-emphasis font-strong tabular-nums">{priceLabel}</span>
        <span className="text-caption text-text-tertiary">{unitLabel}</span>
      </div>
      <div className="text-caption text-text-tertiary">{hint}</div>
    </button>
  );
  if (disabled) {
    return (
      <Tooltip
        content={t("period.unsupportedTooltip", {
          defaultValue: "当前套餐不支持该计费周期。",
        })}
      >
        {btn}
      </Tooltip>
    );
  }
  return btn;
}
