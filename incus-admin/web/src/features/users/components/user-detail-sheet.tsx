import type { User } from "@/shared/lib/auth";
import { useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import {
  useTopUpBalanceMutation,
  useTopUpQuotaQuery,
  useUpdateUserRoleMutation,
} from "@/features/users/api";
import { QuotaEditor } from "@/features/users/components/quota-editor";
import { ShadowLoginDialog } from "@/features/users/components/shadow-login-dialog";
import { Button } from "@/shared/components/ui/button";
import { useConfirm } from "@/shared/components/ui/confirm-dialog";
import { Input } from "@/shared/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/shared/components/ui/select";
import {
  Sheet,
  SheetBody,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/shared/components/ui/sheet";
import { StatusPill } from "@/shared/components/ui/status";
import { formatError } from "@/shared/lib/http";
import { formatCurrency } from "@/shared/lib/utils";

// PLAN-034 P1-C: SaaS 业界主流 preset 金额（HighLevel/Flexprice 模式）。这里
// 按 ¥ 计价，对齐 admin 后台的 currency 主轴。Custom 落到自由输入框。
const TOP_UP_PRESETS = [10, 50, 200, 500] as const;

interface UserDetailSheetProps {
  user: User | null;
  open: boolean;
  onOpenChange: (next: boolean) => void;
}

/**
 * UserDetailSheet —— admin 单用户详情抽屉（PLAN-034 P1-C）。
 *
 * 替代 user-row.tsx 的"行内展开"模式：余额、快捷充值（preset）、配额编辑、
 * 角色切换、shadow login 全部在持久抽屉里，切换其他用户不会丢状态。
 *
 * 视觉：DESIGN.md SheetContent 默认 right side + radius-2xl + shadow-dialog。
 * 配色 / 字号 / 间距全部走 token。
 */
export function UserDetailSheet({ user, open, onOpenChange }: UserDetailSheetProps) {
  const { t } = useTranslation();
  const confirm = useConfirm();
  const [shadowOpen, setShadowOpen] = useState(false);
  const [customAmount, setCustomAmount] = useState("");

  // PLAN-034 P1-C-fix：关闭 transition 期间保留最后一个 user 引用，避免 base-ui
  // Sheet 还在跑 close 动画时父级把 user 清成 null 而立刻卸载 portal，导致
  // focus trap / body[inert] 残留、整个页面后续不响应（"关不掉"症状）。
  const lastUserRef = useRef<User | null>(user);
  if (user) lastUserRef.current = user;
  const display = user ?? lastUserRef.current;

  const userId = display?.id ?? 0;
  const roleMutation = useUpdateUserRoleMutation(userId);
  const topUpMutation = useTopUpBalanceMutation(userId);
  const { data: quota } = useTopUpQuotaQuery(userId, open && userId > 0);

  if (!display) return null;

  const customNum = Number.parseFloat(customAmount);
  // QA-009 N-10 / PLAN-051 §2-D 决策 D-15：单次充值硬上限（默认 $100k），
  // 兜底防止管理员误输 1e10。若需要给某用户更高，由 admin 改全局常量或拆多次。
  const SINGLE_TOPUP_MAX = 100000;
  const customExceedsHard = customNum > 0 && customNum > SINGLE_TOPUP_MAX;
  const customExceeds = customExceedsHard || (!!quota && customNum > 0 && customNum > quota.remaining);

  const performTopUp = async (amount: number) => {
    if (!(amount > 0)) return;
    if (quota && amount > quota.remaining) {
      toast.error(
        t("user.topup.quotaExceededHint", {
          defaultValue: "超出今日剩余额度（剩余 {{remaining}}）",
          remaining: formatCurrency(quota.remaining),
        }),
      );
      return;
    }
    const ok = await confirm({
      title: t("admin.topUpConfirmTitle", { defaultValue: "确认充值" }),
      message: t("admin.topUpConfirmMessage", {
        defaultValue: "确认给 {{email}} 充值 {{amount}}？",
        email: display.email,
        amount: formatCurrency(amount),
      }),
    });
    if (!ok) return;
    topUpMutation.mutate(amount, {
      onSuccess: () => {
        toast.success(
          t("admin.topUpSuccess", {
            defaultValue: "已充值 {{amount}} 给 {{email}}",
            email: display.email,
            amount: formatCurrency(amount),
          }),
        );
        setCustomAmount("");
      },
      onError: (e) => toast.error(formatError(e)),
    });
  };

  const changeRole = async (newRole: string) => {
    if (newRole === display.role) return;
    const isDowngrade = display.role === "admin" && newRole === "customer";
    if (isDowngrade) {
      const ok = await confirm({
        title: t("admin.roleDowngradeTitle", { defaultValue: "降级管理员？" }),
        message: t("admin.roleDowngradeMessage", {
          defaultValue: "确认将 {{email}} 从 admin 降级为 customer？该用户将失去所有管理权限。",
          email: display.email,
        }),
        destructive: true,
        typeToConfirm: display.email,
      });
      if (!ok) return;
    }
    // WP-F：改角色补成功/失败 toast（此前静默）。
    roleMutation.mutate(newRole, {
      onSuccess: () =>
        toast.success(t("admin.roleUpdated", { defaultValue: "角色已更新" })),
      onError: (e) => toast.error(formatError(e)),
    });
  };

  const startShadowLogin = async () => {
    const ok = await confirm({
      title: t("shadow.confirmTitle", { defaultValue: "Shadow Login 确认" }),
      message: t("shadow.confirmMessage", {
        defaultValue: "你将以 {{email}} 的身份登入。所有操作都会按 admin shadow 审计记录。继续吗？",
        email: display.email,
      }),
      destructive: true,
      typeToConfirm: display.email,
    });
    if (!ok) return;
    setShadowOpen(true);
  };

  return (
    <>
      <Sheet open={open} onOpenChange={onOpenChange}>
        <SheetContent side="right" size="min(96vw, 32rem)">
          <SheetHeader>
            <SheetTitle>
              <span className="font-mono">{display.email}</span>
            </SheetTitle>
            <SheetDescription>
              {t("admin.userSheet.description", {
                defaultValue: "用户余额 · 快捷充值 · 配额 · 高级操作",
              })}
            </SheetDescription>
          </SheetHeader>
          <SheetBody className="space-y-6">
            <section className="space-y-2">
              <header className="flex items-baseline justify-between">
                <h3 className="text-caption font-emphasis text-text-tertiary uppercase tracking-wide">
                  {t("admin.userSheet.balance", { defaultValue: "余额" })}
                </h3>
                <span className="text-h2 font-strong font-mono text-foreground">
                  {formatCurrency(display.balance)}
                </span>
              </header>
              <div className="flex flex-wrap gap-1.5">
                {TOP_UP_PRESETS.map((amt) => (
                  <Button
                    key={amt}
                    size="sm"
                    variant="subtle"
                    disabled={topUpMutation.isPending}
                    onClick={() => performTopUp(amt)}
                  >
                    {`+${formatCurrency(amt)}`}
                  </Button>
                ))}
              </div>
              <div className="flex items-center gap-2">
                <span className="text-caption text-text-tertiary">$</span>
                <Input
                  type="number"
                  inputMode="decimal"
                  step="0.01"
                  min="0"
                  max={SINGLE_TOPUP_MAX}
                  value={customAmount}
                  onChange={(e) => setCustomAmount(e.target.value)}
                  placeholder={t("admin.amount", { defaultValue: "自定义金额" })}
                  className="flex-1"
                />
                <Button
                  variant="primary"
                  size="sm"
                  disabled={
                    topUpMutation.isPending
                    || !(customNum > 0)
                    || customExceeds
                  }
                  onClick={() => performTopUp(customNum)}
                >
                  {topUpMutation.isPending
                    ? "..."
                    : t("admin.topUp", { defaultValue: "充值" })}
                </Button>
              </div>
              {quota ? (
                <div className="flex flex-wrap items-center gap-2 text-caption text-text-tertiary">
                  <span>
                    {t("user.topup.usedToday", {
                      defaultValue: "今日已用 {{used}} / 上限 {{limit}}",
                      used: formatCurrency(quota.used),
                      limit: formatCurrency(quota.limit),
                    })}
                  </span>
                  {customExceeds ? (
                    <StatusPill status="error">
                      {t("user.topup.quotaExceeded", {
                        defaultValue: "超出日额度（剩余 {{remaining}}）",
                        remaining: formatCurrency(quota.remaining),
                      })}
                    </StatusPill>
                  ) : null}
                </div>
              ) : null}
            </section>

            <section className="space-y-2">
              <h3 className="text-caption font-emphasis text-text-tertiary uppercase tracking-wide">
                {t("admin.role", { defaultValue: "角色" })}
              </h3>
              <Select
                value={display.role}
                onValueChange={(v) => changeRole(String(v))}
                disabled={roleMutation.isPending}
              >
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="customer">customer</SelectItem>
                  <SelectItem value="admin">admin</SelectItem>
                </SelectContent>
              </Select>
            </section>

            <section className="space-y-2">
              <h3 className="text-caption font-emphasis text-text-tertiary uppercase tracking-wide">
                {t("admin.quota", { defaultValue: "配额" })}
              </h3>
              <QuotaEditor userId={display.id} onClose={() => undefined} />
            </section>

            <section className="space-y-2">
              <h3 className="text-caption font-emphasis text-text-tertiary uppercase tracking-wide">
                {t("admin.userSheet.advanced", { defaultValue: "高级" })}
              </h3>
              <Button
                variant="destructive"
                size="sm"
                onClick={startShadowLogin}
                data-testid={`shadow-login-${display.id}`}
              >
                ⚠ {t("shadow.loginShort", { defaultValue: "Shadow 登入" })}
              </Button>
            </section>
          </SheetBody>
        </SheetContent>
      </Sheet>
      <ShadowLoginDialog
        open={shadowOpen}
        onOpenChange={setShadowOpen}
        user={display}
      />
    </>
  );
}
