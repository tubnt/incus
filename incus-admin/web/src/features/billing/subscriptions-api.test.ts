import type { VMSubscription } from "./subscriptions-api";
import { describe, expect, it } from "vitest";
import { computeRunwayDays, runwayDaysFromNow } from "./subscriptions-api";

function makeSub(overrides: Partial<VMSubscription>): VMSubscription {
  return {
    id: 1,
    vm_id: 1,
    product_id: 1,
    user_id: 1,
    period: "monthly",
    daily_rate: null,
    monthly_rate: null,
    paid_until: "2026-06-26T00:00:00Z",
    status: "active",
    suspended_at: null,
    grace_until: null,
    created_at: "2026-05-26T00:00:00Z",
    updated_at: "2026-05-26T00:00:00Z",
    ...overrides,
  };
}

describe("computeRunwayDays", () => {
  it("returns null when balance is zero", () => {
    expect(computeRunwayDays(0, [makeSub({ period: "daily", daily_rate: 1 })])).toBeNull();
  });

  it("returns null when balance is negative", () => {
    expect(computeRunwayDays(-5, [makeSub({ period: "daily", daily_rate: 1 })])).toBeNull();
  });

  it("returns null when no active subscriptions", () => {
    const subs = [
      makeSub({ status: "cancelled", period: "daily", daily_rate: 1 }),
      makeSub({ id: 2, status: "suspended", period: "monthly", monthly_rate: 30 }),
    ];
    expect(computeRunwayDays(100, subs)).toBeNull();
  });

  it("computes runway from daily sub only", () => {
    const subs = [makeSub({ period: "daily", daily_rate: 2 })];
    // balance=10, burn=2/day → 5 days
    expect(computeRunwayDays(10, subs)).toBe(5);
  });

  it("computes runway from monthly sub (folded to daily)", () => {
    const subs = [makeSub({ period: "monthly", monthly_rate: 30 })];
    // balance=10, monthly_rate=30 → daily=1, runway=10 days
    expect(computeRunwayDays(10, subs)).toBe(10);
  });

  it("sums daily + monthly subs into a single burn", () => {
    const subs = [
      makeSub({ period: "daily", daily_rate: 1 }),
      makeSub({ id: 2, period: "monthly", monthly_rate: 30 }),
    ];
    // daily=1 + 30/30=1 → 2/day; balance=20 → 10 days
    expect(computeRunwayDays(20, subs)).toBe(10);
  });

  it("ignores subs where rate is null on the chosen side", () => {
    const subs = [
      makeSub({ period: "daily", daily_rate: null }),
      makeSub({ id: 2, period: "monthly", monthly_rate: 30 }),
    ];
    expect(computeRunwayDays(30, subs)).toBe(30);
  });

  it("returns null when all active subs lack a usable rate", () => {
    const subs = [
      makeSub({ period: "daily", daily_rate: null }),
      makeSub({ id: 2, period: "monthly", monthly_rate: null }),
    ];
    expect(computeRunwayDays(100, subs)).toBeNull();
  });

  it("returns null when balance is NaN/Infinity", () => {
    expect(
      computeRunwayDays(Number.NaN, [makeSub({ period: "daily", daily_rate: 1 })]),
    ).toBeNull();
    expect(
      computeRunwayDays(Number.POSITIVE_INFINITY, [
        makeSub({ period: "daily", daily_rate: 1 }),
      ]),
    ).toBeNull();
  });
});

describe("runwayDaysFromNow", () => {
  const NOW = new Date("2026-05-26T12:00:00Z");

  it("returns 0 for already-past paid_until", () => {
    expect(runwayDaysFromNow("2026-05-20T00:00:00Z", NOW)).toBe(0);
  });

  it("returns 0 for exact-now paid_until", () => {
    expect(runwayDaysFromNow("2026-05-26T12:00:00Z", NOW)).toBe(0);
  });

  it("computes whole-day floor for future paid_until", () => {
    // 5 days + 1 hour later → 5 days
    expect(runwayDaysFromNow("2026-05-31T13:00:00Z", NOW)).toBe(5);
  });

  it("treats invalid date as 0", () => {
    expect(runwayDaysFromNow("not-a-date", NOW)).toBe(0);
  });
});
