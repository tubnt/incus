// @vitest-environment jsdom
import type { Product } from "@/features/products/api";
import { cleanup, render } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { PeriodPicker, productSupports } from "./period-picker";

afterEach(() => {
  // vitest 4 没启用 @testing-library 自动 cleanup；手动来一发避免 DOM 残留
  // 跨用例污染（多 render 同一 testid 会把 disabled 测试匹到上一轮 enabled 的）
  cleanup();
});

// 用最薄一层 mock react-i18next，避免拉起完整 i18n + http backend。
vi.mock("react-i18next", () => ({
  // mock factory 必须用真名 useTranslation 才能被消费者替换
  useTranslation: () => ({ // eslint-disable-line react/component-hook-factories
    t: (key: string, opts?: { defaultValue?: string }) =>
      opts?.defaultValue ?? key,
  }),
}));

function makeProduct(overrides: Partial<Product> = {}): Product {
  return {
    id: 1,
    name: "test",
    slug: "test",
    cpu: 1,
    memory_mb: 1024,
    disk_gb: 20,
    bandwidth_tb: 1,
    price_monthly: 30,
    price_daily: 1,
    period_supported: ["daily", "monthly"],
    currency: "USD",
    access: "public",
    active: true,
    sort_order: 0,
    ...overrides,
  };
}

describe("productSupports", () => {
  it("returns false when product is null", () => {
    expect(productSupports(null, "daily")).toBe(false);
    expect(productSupports(null, "monthly")).toBe(false);
  });

  it("defaults to monthly when period_supported is missing", () => {
    const p = makeProduct({ period_supported: undefined });
    expect(productSupports(p, "monthly")).toBe(true);
    expect(productSupports(p, "daily")).toBe(false);
  });

  it("respects the period_supported whitelist", () => {
    expect(productSupports(makeProduct({ period_supported: ["monthly"] }), "daily")).toBe(false);
    expect(productSupports(makeProduct({ period_supported: ["daily"] }), "monthly")).toBe(false);
    expect(productSupports(makeProduct({ period_supported: ["daily", "monthly"] }), "daily")).toBe(true);
  });
});

// base-ui Tooltip 在 disabled 分支下 clone children 进 trigger span，导致同一
// data-testid 在 DOM 出现两次（span 也被 setAttribute 了 testid）。我们要的是
// 真正的 <button>。
function firstButton(testId: string): HTMLButtonElement {
  const els = Array.from(document.querySelectorAll<HTMLElement>(`[data-testid="${testId}"]`));
  const btn = els.find((e) => e.tagName === "BUTTON");
  if (!btn) throw new Error(`no button with testid ${testId}; saw ${els.map((e) => e.tagName).join(",")}`);
  return btn as HTMLButtonElement;
}

describe("periodPicker", () => {
  it("renders both options and shows rates", () => {
    const onChange = vi.fn();
    render(
      <PeriodPicker
        product={makeProduct({ price_monthly: 30, price_daily: 1 })}
        value="monthly"
        onChange={onChange}
      />,
    );
    expect(firstButton("period-monthly")).toBeTruthy();
    expect(firstButton("period-daily")).toBeTruthy();
    expect(firstButton("period-monthly").textContent).toContain("30");
    expect(firstButton("period-daily").textContent).toContain("1");
  });

  it("disables the daily option when product does not support it", () => {
    render(
      <PeriodPicker
        product={makeProduct({ period_supported: ["monthly"], price_daily: null })}
        value="monthly"
        onChange={vi.fn()}
      />,
    );
    const btn = firstButton("period-daily");
    expect(btn.getAttribute("aria-disabled")).toBe("true");
    expect(btn.hasAttribute("disabled")).toBe(true);
  });

  it("calls onChange with daily when daily option is clicked", () => {
    const onChange = vi.fn();
    render(
      <PeriodPicker product={makeProduct()} value="monthly" onChange={onChange} />,
    );
    // dispatchEvent 比 .click() 更稳，避免 base-ui Tooltip 包裹层吞事件
    firstButton("period-daily").dispatchEvent(
      new MouseEvent("click", { bubbles: true, cancelable: true }),
    );
    expect(onChange).toHaveBeenCalledWith("daily");
  });

  it("does not call onChange when clicking a disabled option", () => {
    const onChange = vi.fn();
    render(
      <PeriodPicker
        product={makeProduct({ period_supported: ["monthly"] })}
        value="monthly"
        onChange={onChange}
      />,
    );
    firstButton("period-daily").dispatchEvent(
      new MouseEvent("click", { bubbles: true, cancelable: true }),
    );
    expect(onChange).not.toHaveBeenCalled();
  });
});
