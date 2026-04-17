import { describe, expect, it } from "vitest";
import { fmtBytes, formatCurrency } from "./utils";

describe("fmtBytes", () => {
  it("formats zero", () => {
    expect(fmtBytes(0)).toBe("0 B");
  });

  it("formats bytes", () => {
    expect(fmtBytes(512)).toBe("512 B");
  });

  it("formats KB", () => {
    expect(fmtBytes(1024)).toBe("1 KB");
  });

  it("formats MB", () => {
    expect(fmtBytes(1024 * 1024 * 5)).toBe("5.0 MB");
  });

  it("formats GB with decimal", () => {
    expect(fmtBytes(1024 * 1024 * 1024 * 2.5)).toBe("2.5 GB");
  });

  it("formats TB", () => {
    expect(fmtBytes(1024 * 1024 * 1024 * 1024)).toBe("1.0 TB");
  });
});

describe("formatCurrency", () => {
  it("uses USD by default", () => {
    const out = formatCurrency(9.9, undefined, "en-US");
    expect(out).toContain("9.90");
    expect(out).toContain("$");
  });

  it("respects currency param", () => {
    const out = formatCurrency(12.5, "CNY", "zh-CN");
    expect(out.replace(/\s/g, "")).toMatch(/(¥|CN¥|CNY).*12\.50/);
  });

  it("handles undefined currency as USD", () => {
    const out = formatCurrency(5, undefined, "en-US");
    expect(out).toBe("$5.00");
  });

  it("handles zero", () => {
    const out = formatCurrency(0, "USD", "en-US");
    expect(out).toBe("$0.00");
  });
});
