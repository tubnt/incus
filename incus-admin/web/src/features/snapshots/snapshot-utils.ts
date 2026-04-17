export type SnapshotApiBase = "/admin" | "/portal";

export function snapshotPath(
  apiBase: SnapshotApiBase,
  vmName: string,
  snap?: string,
): string {
  const base = `${apiBase}/vms/${vmName}/snapshots`;
  return snap ? `${base}/${snap}` : base;
}
