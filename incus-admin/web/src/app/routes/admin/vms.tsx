import { createFileRoute, Link } from "@tanstack/react-router";
import { useMemo, useState } from "react";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import { SnapshotPanel } from "@/features/snapshots/snapshot-panel";
import { VMMetricsPanel } from "@/features/monitoring/vm-metrics-panel";
import { useConfirm } from "@/shared/components/ui/confirm-dialog";
import { useClustersQuery } from "@/features/clusters/api";
import { Pagination } from "@/shared/components/ui/pagination";
import {
  type GoneVM,
  type IncusInstance,
  extractIP,
  useClusterVMsQuery,
  useDeleteVMMutation,
  useForceDeleteGoneVMMutation,
  useGoneVMsQuery,
  useReinstallVMMutation,
  useVMStateMutation,
} from "@/features/vms/api";

export const Route = createFileRoute("/admin/vms")({
  component: AllVMsPage,
});

function AllVMsPage() {
  const { t } = useTranslation();
  const { data: clustersData } = useClustersQuery();
  const clusters = clustersData?.clusters ?? [];

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold">{t("nav.allVms")}</h1>
        <Link to="/admin/create-vm" className="px-4 py-2 bg-primary text-primary-foreground rounded-md text-sm font-medium hover:opacity-90">
          {t("nav.createVm")}
        </Link>
      </div>
      <GoneVMsPanel />
      {clusters.map((c) => (
        <ClusterVMs key={c.name} clusterName={c.name} displayName={c.display_name} />
      ))}
    </div>
  );
}

/**
 * GoneVMsPanel surfaces rows the PLAN-020 reconciler flagged as gone
 * (Incus instance disappeared out-of-band). Hidden entirely when count=0
 * so the normal admin VM page stays clean in a healthy cluster. Each row
 * exposes a "清理" action that calls /admin/vms/{id}/force-delete, which
 * soft-deletes the row and releases its IP.
 */
function GoneVMsPanel() {
  const { t } = useTranslation();
  const { data, isLoading } = useGoneVMsQuery();
  const forceDelete = useForceDeleteGoneVMMutation();
  const confirm = useConfirm();

  if (isLoading) return null;
  const goneVMs = data?.vms ?? [];
  if (goneVMs.length === 0) return null;

  const cleanup = async (vm: GoneVM) => {
    const ok = await confirm({
      title: t("vm.forceDeleteTitle", { defaultValue: "清理 Drift VM？" }),
      message: t("vm.forceDeleteMessage", {
        defaultValue: "将物理删除 DB 行并释放 IP {{ip}}。此操作不可撤销（原 VM 在 Incus 端已消失）。",
        ip: vm.ip ?? "(无)",
      }),
      destructive: true,
    });
    if (!ok) return;
    forceDelete.mutate(vm.id, {
      onSuccess: () => toast.success(t("vm.forceDeleted", { defaultValue: "已清理" }) + ` ${vm.name}`),
      onError: (err) => toast.error((err as Error).message),
    });
  };

  return (
    <div className="mb-6 border border-destructive/30 rounded-lg bg-destructive/5 p-4">
      <div className="flex items-center justify-between mb-3">
        <h2 className="text-base font-semibold text-destructive">
          ⚠️ Drift VMs ({goneVMs.length}) — status=gone
        </h2>
        <span className="text-xs text-muted-foreground">
          {t("vm.driftHint", {
            defaultValue: "Incus 端实例已消失，DB 残留；审计后可清理",
          })}
        </span>
      </div>
      <div className="overflow-x-auto">
        <table className="w-full text-sm">
          <thead className="bg-muted/30">
            <tr>
              <th className="text-left px-3 py-2 font-medium">ID</th>
              <th className="text-left px-3 py-2 font-medium">{t("vm.name")}</th>
              <th className="text-left px-3 py-2 font-medium">{t("vm.status")}</th>
              <th className="text-left px-3 py-2 font-medium">{t("vm.ip")}</th>
              <th className="text-left px-3 py-2 font-medium">{t("vm.node")}</th>
              <th className="text-left px-3 py-2 font-medium">
                {t("vm.markedGoneAt", { defaultValue: "标记时间" })}
              </th>
              <th className="text-right px-3 py-2 font-medium">{t("common.actions")}</th>
            </tr>
          </thead>
          <tbody>
            {goneVMs.map((vm) => (
              <tr key={vm.id} className="border-t border-border">
                <td className="px-3 py-2 text-xs text-muted-foreground">{vm.id}</td>
                <td className="px-3 py-2 font-mono">{vm.name}</td>
                <td className="px-3 py-2">
                  <span className="px-2 py-0.5 rounded text-xs font-medium bg-muted text-muted-foreground">
                    gone
                  </span>
                </td>
                <td className="px-3 py-2 font-mono text-xs">{vm.ip ?? "—"}</td>
                <td className="px-3 py-2 text-muted-foreground">{vm.node || "—"}</td>
                <td className="px-3 py-2 text-xs text-muted-foreground">
                  {new Date(vm.updated_at).toLocaleString()}
                </td>
                <td className="px-3 py-2 text-right">
                  <button
                    onClick={() => cleanup(vm)}
                    disabled={forceDelete.isPending}
                    className="px-2 py-1 rounded text-xs font-medium bg-destructive/20 text-destructive hover:bg-destructive/30 disabled:opacity-50"
                  >
                    {forceDelete.isPending
                      ? t("common.loading")
                      : t("vm.cleanup", { defaultValue: "清理" })}
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function ClusterVMs({ clusterName, displayName }: { clusterName: string; displayName: string }) {
  const { t } = useTranslation();
  const { data, isLoading, isError, error } = useClusterVMsQuery(clusterName, 15_000);
  const [limit, setLimit] = useState(20);
  const [offset, setOffset] = useState(0);

  const vms = data?.vms ?? [];
  const total = vms.length;
  const pageVms = useMemo(() => vms.slice(offset, offset + limit), [vms, offset, limit]);
  const isStale = data?.stale;

  return (
    <div className="mb-8">
      <div className="flex items-center gap-2 mb-3">
        <h2 className="text-lg font-semibold">{displayName} ({data?.count ?? 0} VMs)</h2>
        {isStale && (
          <span className="px-2 py-0.5 rounded text-xs bg-warning/20 text-warning">
            {t("vm.cachedAt", { time: data?.cached_at ? new Date(data.cached_at).toLocaleTimeString() : "" })}
          </span>
        )}
        {(data?.error || data?.warning) && !isStale && (
          <span className="px-2 py-0.5 rounded text-xs bg-destructive/20 text-destructive">
            {data?.error || data?.warning}
          </span>
        )}
      </div>
      {isError && (
        <div className="border border-destructive/30 rounded-lg p-4 mb-3 text-sm text-destructive">
          {t("vm.clusterConnectFailed")}: {(error as Error)?.message ?? t("vm.unknownError")}
        </div>
      )}
      {isLoading ? (
        <div className="border border-border rounded-lg p-4 space-y-2">
          {Array.from({ length: 3 }).map((_, i) => (
            <div key={i} className="h-10 animate-pulse rounded bg-muted" />
          ))}
        </div>
      ) : vms.length === 0 ? (
        <div className="border border-border rounded-lg p-6 text-center text-muted-foreground">
          {t("vm.noneInCluster")}
        </div>
      ) : (
        <>
          <div className="border border-border rounded-lg overflow-x-auto">
            <table className="w-full text-sm">
              <thead className="bg-muted/30">
                <tr>
                  <th className="text-left px-4 py-2 font-medium">{t("vm.name")}</th>
                  <th className="text-left px-4 py-2 font-medium">{t("vm.status")}</th>
                  <th className="text-left px-4 py-2 font-medium">{t("vm.node")}</th>
                  <th className="text-left px-4 py-2 font-medium">{t("vm.config")}</th>
                  <th className="text-left px-4 py-2 font-medium">{t("vm.ip")}</th>
                  <th className="text-right px-4 py-2 font-medium">{t("common.actions")}</th>
                </tr>
              </thead>
              <tbody>
                {pageVms.map((vm) => (
                  <VMRow key={vm.name} vm={vm} clusterName={clusterName} />
                ))}
              </tbody>
            </table>
          </div>
          {total > 20 && (
            <Pagination
              total={total}
              limit={limit}
              offset={offset}
              onChange={(l, o) => { setLimit(l); setOffset(o); }}
              className="mt-3"
            />
          )}
        </>
      )}
    </div>
  );
}

const OS_IMAGES = [
  { value: "images:ubuntu/24.04/cloud", label: "Ubuntu 24.04 LTS" },
  { value: "images:ubuntu/22.04/cloud", label: "Ubuntu 22.04 LTS" },
  { value: "images:debian/12/cloud", label: "Debian 12" },
  { value: "images:rockylinux/9/cloud", label: "Rocky Linux 9" },
];

function VMRow({ vm, clusterName }: { vm: IncusInstance; clusterName: string }) {
  const { t } = useTranslation();
  const confirm = useConfirm();
  const [showSnaps, setShowSnaps] = useState(false);
  const [showMetrics, setShowMetrics] = useState(false);
  const [showReinstall, setShowReinstall] = useState(false);
  const ip = vm.ip || extractIP(vm);
  const project = vm.project || "customers";

  const stateMutation = useVMStateMutation();
  const deleteMutation = useDeleteVMMutation();

  const isActing = stateMutation.isPending || deleteMutation.isPending;

  const runAction = (action: string) => {
    stateMutation.mutate(
      { name: vm.name, action, cluster: clusterName, project },
      {
        onSuccess: () => toast.success(`${vm.name}: ${action} ${t("vm.actionSubmitted")}`),
        onError: () => toast.error(`${vm.name}: ${action} ${t("vm.actionFailed")}`),
      },
    );
  };

  const runDelete = () => {
    deleteMutation.mutate(
      { name: vm.name, cluster: clusterName, project },
      {
        onSuccess: () => toast.success(`${vm.name} ${t("vm.deleted")}`),
        onError: () => toast.error(`${vm.name} ${t("vm.deleteFailed")}`),
      },
    );
  };

  return (
    <>
    <tr className="border-t border-border">
      <td className="px-4 py-2 font-mono">
        <a href={`/admin/vm-detail?name=${vm.name}&cluster=${clusterName}&project=${project}`}
          className="text-primary hover:underline">{vm.name}</a>
      </td>
      <td className="px-4 py-2">
        <StatusBadge status={vm.status} />
      </td>
      <td className="px-4 py-2">{vm.location}</td>
      <td className="px-4 py-2 text-muted-foreground">
        {vm.config?.["limits.cpu"] ?? "—"}C / {vm.config?.["limits.memory"] ?? "—"}
      </td>
      <td className="px-4 py-2 font-mono text-xs">{ip || "—"}</td>
      <td className="px-4 py-2 text-right">
        <div className="flex gap-1 justify-end">
          {vm.status === "Stopped" && (
            <ActionBtn label={t("vm.start")} color="success" disabled={isActing}
              onClick={() => runAction("start")} />
          )}
          {vm.status === "Running" && (
            <>
              <a
                href={`/console?vm=${vm.name}&cluster=${clusterName}&project=${project}&from=admin`}
                className="px-2 py-1 rounded text-xs font-medium bg-primary/20 text-primary hover:bg-primary/30"
              >
                {t("vm.console")}
              </a>
              <ActionBtn label={t("vm.stop")} color="muted" disabled={isActing}
                onClick={() => runAction("stop")} />
              <ActionBtn label={t("vm.restart")} color="muted" disabled={isActing}
                onClick={() => runAction("restart")} />
            </>
          )}
          {vm.status === "Running" && (
            <ActionBtn label={t("vm.monitor")} color="muted" disabled={false}
              onClick={() => setShowMetrics(!showMetrics)} />
          )}
          <ActionBtn label={t("vm.snapshots")} color="muted" disabled={false}
            onClick={() => setShowSnaps(!showSnaps)} />
          <ActionBtn label={t("vm.reinstall")} color="muted" disabled={isActing}
            onClick={() => setShowReinstall(!showReinstall)} />
          <ActionBtn label={t("vm.delete")} color="destructive" disabled={isActing}
            onClick={async () => {
              const ok = await confirm({
                title: t("deleteConfirm.vmTitle"),
                message: t("deleteConfirm.vmMessage", { name: vm.name }),
                destructive: true,
              });
              if (ok) runDelete();
            }} />
        </div>
      </td>
    </tr>
    {showMetrics && vm.status === "Running" && (
      <tr>
        <td colSpan={6} className="p-0">
          <VMMetricsPanel vmName={vm.name} apiBase="/admin" cluster={clusterName} />
        </td>
      </tr>
    )}
    {showSnaps && (
      <tr>
        <td colSpan={6} className="p-0">
          <SnapshotPanel vmName={vm.name} cluster={clusterName} project={project} />
        </td>
      </tr>
    )}
    {showReinstall && (
      <tr>
        <td colSpan={6} className="p-0">
          <ReinstallPanel vmName={vm.name} cluster={clusterName} project={project}
            onDone={() => setShowReinstall(false)} />
        </td>
      </tr>
    )}
    </>
  );
}

function ReinstallPanel({ vmName, cluster, project, onDone }: {
  vmName: string; cluster: string; project: string; onDone: () => void;
}) {
  const { t } = useTranslation();
  const confirm = useConfirm();
  const [os, setOs] = useState(OS_IMAGES[0]!.value);
  const mutation = useReinstallVMMutation();

  const run = () => {
    mutation.mutate(
      { name: vmName, cluster, project, os_image: os },
      {
        onSuccess: (data) => {
          toast.success(t("vm.reinstallDone", { username: data.username, password: data.password }), { duration: 20_000 });
          onDone();
        },
        onError: (err) => toast.error((err as Error).message),
      },
    );
  };

  return (
    <div className="p-4 bg-card/50 border-t border-border">
      <h4 className="font-medium text-sm mb-3">{t("vm.reinstallHeading", { name: vmName })}</h4>
      <p className="text-xs text-destructive mb-3">
        {t("vm.reinstallWarning")}
      </p>
      <div className="flex items-center gap-3">
        <select
          value={os}
          onChange={(e) => setOs(e.target.value)}
          className="px-2 py-1 text-xs border border-border rounded bg-card"
        >
          {OS_IMAGES.map((img) => (
            <option key={img.value} value={img.value}>{img.label}</option>
          ))}
        </select>
        <button
          onClick={async () => {
            const ok = await confirm({
              title: t("deleteConfirm.reinstallTitle"),
              message: t("deleteConfirm.reinstallMessage", { name: vmName }),
              destructive: true,
            });
            if (ok) run();
          }}
          disabled={mutation.isPending}
          className="px-3 py-1 text-xs bg-destructive text-destructive-foreground rounded disabled:opacity-50"
        >
          {mutation.isPending ? t("vm.reinstalling") : t("vm.reinstallConfirm")}
        </button>
        <button
          onClick={onDone}
          className="px-3 py-1 text-xs bg-muted/50 text-muted-foreground rounded"
        >
          {t("common.cancel")}
        </button>
      </div>
    </div>
  );
}

function ActionBtn({ label, color, disabled, onClick }: {
  label: string; color: string; disabled: boolean; onClick: () => void;
}) {
  const colorMap: Record<string, string> = {
    success: "bg-success/20 text-success hover:bg-success/30",
    muted: "bg-muted/50 text-muted-foreground hover:bg-muted",
    destructive: "bg-destructive/20 text-destructive hover:bg-destructive/30",
  };
  return (
    <button
      onClick={onClick}
      disabled={disabled}
      className={`px-2 py-1 rounded text-xs font-medium disabled:opacity-50 ${colorMap[color] ?? colorMap.muted}`}
    >
      {label}
    </button>
  );
}

function StatusBadge({ status }: { status: string }) {
  const colors: Record<string, string> = {
    Running: "bg-success/20 text-success",
    Stopped: "bg-muted text-muted-foreground",
    Error: "bg-destructive/20 text-destructive",
    Frozen: "bg-primary/20 text-primary",
  };
  return (
    <span className={`px-2 py-0.5 rounded text-xs font-medium ${colors[status] ?? "bg-muted text-muted-foreground"}`}>
      {status}
    </span>
  );
}
