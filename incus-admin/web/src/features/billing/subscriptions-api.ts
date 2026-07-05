import { useMutation, useQuery } from "@tanstack/react-query";
import { http } from "@/shared/lib/http";
import { queryClient } from "@/shared/lib/query-client";

// PLAN-054 / INFRA-013 vm_subscriptions —— 单 VM 计费订阅。
// status: active(绿) / suspended(余额不足挂起) / cancelled(已取消)。
// daily_rate / monthly_rate 二选一填，period 决定哪侧填。
export interface VMSubscription {
  id: number;
  vm_id: number;
  product_id: number;
  user_id: number;
  period: "daily" | "monthly";
  daily_rate?: number | null;
  monthly_rate?: number | null;
  paid_until: string;
  status: "active" | "suspended" | "cancelled";
  suspended_at?: string | null;
  grace_until?: string | null;
  created_at: string;
  updated_at: string;
}

export type SubscriptionStatus = VMSubscription["status"];

export const subscriptionKeys = {
  all: ["subscription"] as const,
  myList: (status?: string) =>
    [...subscriptionKeys.all, "list", "my", status ?? ""] as const,
  adminList: (status?: string, userId?: number) =>
    [...subscriptionKeys.all, "list", "admin", status ?? "", userId ?? 0] as const,
};

export function useMySubscriptionsQuery(status?: SubscriptionStatus) {
  const qs = status ? `?status=${encodeURIComponent(status)}` : "";
  return useQuery({
    queryKey: subscriptionKeys.myList(status),
    queryFn: () =>
      http.get<{ subscriptions: VMSubscription[] }>(`/portal/subscriptions${qs}`),
  });
}

export function useAdminSubscriptionsQuery(opts?: {
  status?: SubscriptionStatus | "";
  userId?: number;
}) {
  const params = new URLSearchParams();
  if (opts?.status) params.set("status", opts.status);
  if (opts?.userId && opts.userId > 0) params.set("user", String(opts.userId));
  const qs = params.toString() ? `?${params.toString()}` : "";
  return useQuery({
    queryKey: subscriptionKeys.adminList(opts?.status || undefined, opts?.userId),
    queryFn: () =>
      http.get<{ subscriptions: VMSubscription[] }>(`/admin/subscriptions${qs}`),
  });
}

export function useAdminReactivateSubscriptionMutation() {
  return useMutation({
    mutationFn: (subId: number) =>
      http.post<{ subscription: VMSubscription }>(
        `/admin/subscriptions/${subId}/reactivate`,
        {},
        {
          intent: {
            action: "subscription.reactivate",
            args: { subscription_id: subId },
            description: `订阅 #${subId} 手动恢复`,
          },
        },
      ),
    onSuccess: () =>
      queryClient.invalidateQueries({ queryKey: subscriptionKeys.all }),
  });
}

// runwayDaysFromNow —— 单条订阅基于 paid_until 的剩余整数天（不是基于余额）。
// 列表里给"剩余天数"列用：paid_until - NOW 的整数天，最小 0。
export function runwayDaysFromNow(paidUntil: string, now: Date = new Date()): number {
  const t = new Date(paidUntil).getTime();
  if (!Number.isFinite(t)) return 0;
  const diffMs = t - now.getTime();
  if (diffMs <= 0) return 0;
  return Math.floor(diffMs / 86_400_000);
}

// computeRunwayDays —— 余额预估剩余天数。
// 算法：runway = balance / Σ(active sub 折算成 daily 的费率)
// monthly sub 折算 daily = monthly_rate / 30（与 BillingPeriodDuration 一致）。
// balance <= 0 或无 active sub → null（调用方据此隐藏卡片）。
// 全 suspended/cancelled 也返 null（没消费速率可算）。
export function computeRunwayDays(
  balance: number,
  subs: readonly VMSubscription[],
): number | null {
  if (!Number.isFinite(balance) || balance <= 0) return null;
  let dailyBurn = 0;
  for (const s of subs) {
    if (s.status !== "active") continue;
    if (s.period === "daily" && typeof s.daily_rate === "number") {
      dailyBurn += s.daily_rate;
    } else if (s.period === "monthly" && typeof s.monthly_rate === "number") {
      dailyBurn += s.monthly_rate / 30;
    }
  }
  if (dailyBurn <= 0) return null;
  return balance / dailyBurn;
}
