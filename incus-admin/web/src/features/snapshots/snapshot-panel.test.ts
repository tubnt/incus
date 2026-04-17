import { describe, expect, it } from "vitest";
import { snapshotPath } from "./snapshot-utils";

describe("snapshotPath", () => {
  it("builds admin list path", () => {
    expect(snapshotPath("/admin", "vm-1")).toBe("/admin/vms/vm-1/snapshots");
  });

  it("builds portal list path (J-P2.1 regression)", () => {
    expect(snapshotPath("/portal", "vm-1")).toBe("/portal/vms/vm-1/snapshots");
  });

  it("builds admin single-snapshot path", () => {
    expect(snapshotPath("/admin", "vm-1", "snap-a")).toBe(
      "/admin/vms/vm-1/snapshots/snap-a",
    );
  });

  it("builds portal single-snapshot path", () => {
    expect(snapshotPath("/portal", "vm-1", "snap-a")).toBe(
      "/portal/vms/vm-1/snapshots/snap-a",
    );
  });
});
