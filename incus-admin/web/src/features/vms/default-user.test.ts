import { describe, expect, it } from "vitest";
import { defaultUserForImage } from "./default-user";

// OPS-051 / PLAN-052 Q7：所有 Linux 统一 root 登录；Windows 仍 Administrator。
describe("defaultUserForImage", () => {
  it("returns root for all Linux cloud variants", () => {
    expect(defaultUserForImage("images:ubuntu/24.04/cloud")).toBe("root");
    expect(defaultUserForImage("images:debian/12/cloud")).toBe("root");
    expect(defaultUserForImage("images:rockylinux/9/cloud")).toBe("root");
    expect(defaultUserForImage("images:almalinux/9/cloud")).toBe("root");
    expect(defaultUserForImage("images:fedora/40/cloud")).toBe("root");
    expect(defaultUserForImage("images:archlinux/cloud")).toBe("root");
    expect(defaultUserForImage("images:alpine/3.19")).toBe("root");
  });

  it("returns Administrator for Windows aliases", () => {
    expect(defaultUserForImage("images:windows-server-2022")).toBe("Administrator");
    expect(defaultUserForImage("windows-11")).toBe("Administrator");
  });

  it("falls back to root for empty / unknown", () => {
    expect(defaultUserForImage("")).toBe("root");
    expect(defaultUserForImage(null)).toBe("root");
    expect(defaultUserForImage(undefined)).toBe("root");
    expect(defaultUserForImage("custom:my-image")).toBe("root");
  });
});
