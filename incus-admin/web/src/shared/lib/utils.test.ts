import { describe, expect, it } from "vitest";
import { fmtBytes } from "./utils";

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
