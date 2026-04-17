import { createFileRoute, Link } from "@tanstack/react-router";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import { http } from "@/shared/lib/http";
import { queryClient } from "@/shared/lib/query-client";
import { SnapshotPanel } from "@/features/snapshots/snapshot-panel";
import { VMMetricsPanel } from "@/features/monitoring/vm-metrics-panel";
import { useConfirm } from "@/shared/components/ui/confirm-dialog";

export const Route = createFileRoute("/admin/vms")({
  component: AllVMsPage,
});

interface IncusInstance {
  name: string;
  status: string;
  type: string;
  location: string;
  project: string;
  config: Record<string, string>;
  ip?: string;
  state?: {
    network?: Record<string, {
      addresses: Array<{ address: string; family: string; scope: string }>;
    }>;
  };
}

function AllVMsPage() {
  const { t } = useTranslation();
  const { data: clustersData } = useQuery({
    queryKey: ["adminClusters"],
    queryFn: () => http.get<{ clusters: Array<{ name: string; display_name: string }> }>("/admin/clusters"),
  });

  const clusters = clustersData?.clusters ?? [];

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold">{t("nav.allVms")}</h1>
        <Link to="/admin/create-vm" className="px-4 py-2 bg-primary text-primary-foreground rounded-md text-sm font-medium hover:opacity-90">
          {t("nav.createVm")}
        </Link>
      </div>
      {clusters.map((c) => (
        <ClusterVMs key={c.name} clusterName={c.name} displayName={c.display_name} />
      ))}
    </div>
  );
}

interface ClusterVMsResponse {
  vms: IncusInstance[];
  count: number;
  stale?: boolean;
  cached_at?: string;
  error?: string;
  warning?: string;
}

function ClusterVMs({ clusterName, displayName }: { clusterName: string; displayName: string }) {
  const { t } = useTranslation();
  const { data, isLoading, isError, error } = useQuery({
    queryKey: ["adminClusterVMs", clusterName],
    queryFn: () => http.get<ClusterVMsResponse>(`/admin/clusters/${clusterName}/vms`),
    refetchInterval: 15_000,
    retry: 1,
  });

  const vms = data?.vms ?? [];
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
        <div className="border border-border rounded-lg overflow-hidden">
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
              {vms.map((vm) => (
                <VMRow key={vm.name} vm={vm} clusterName={clusterName} />
              ))}
            </tbody>
          </table>
        </div>
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
  const ip = extractIP(vm);
  const project = vm.project || "customers";

  const stateMutation = useMutation({
    mutationFn: (action: string) =>
      http.put(`/admin/vms/${vm.name}/state`, { action, cluster: clusterName, project }),
    onSuccess: (_data, action) => {
      queryClient.invalidateQueries({ queryKey: ["adminClusterVMs"] });
      toast.success(`${vm.name}: ${action} ${t("vm.actionSubmitted")}`);
    },
    onError: (_err, action) => {
      toast.error(`${vm.name}: ${action} ${t("vm.actionFailed")}`);
    },
  });

  const deleteMutation = useMutation({
    mutationFn: () =>
      http.delete(`/admin/vms/${vm.name}`, { cluster: clusterName, project }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["adminClusterVMs"] });
      toast.success(`${vm.name} ${t("vm.deleted")}`);
    },
    onError: () => {
      toast.error(`${vm.name} ${t("vm.deleteFailed")}`);
    },
  });

  const isActing = stateMutation.isPending || deleteMutation.isPending;

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
              onClick={() => stateMutation.mutate("start")} />
          )}
          {vm.status === "Running" && (
            <>
              <a
                href={`/console?vm=${vm.name}&cluster=${clusterName}&project=${project}`}
                className="px-2 py-1 rounded text-xs font-medium bg-primary/20 text-primary hover:bg-primary/30"
              >
                {t("vm.console")}
              </a>
              <ActionBtn label={t("vm.stop")} color="muted" disabled={isActing}
                onClick={() => stateMutation.mutate("stop")} />
              <ActionBtn label={t("vm.restart")} color="muted" disabled={isActing}
                onClick={() => stateMutation.mutate("restart")} />
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
              if (ok) deleteMutation.mutate();
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

  const mutation = useMutation({
    mutationFn: () =>
      http.post<{ status: string; password: string; username: string }>(
        `/admin/vms/${vmName}/reinstall`,
        { cluster, project, os_image: os },
      ),
    onSuccess: (data) => {
      queryClient.invalidateQueries({ queryKey: ["adminClusterVMs"] });
      toast.success(t("vm.reinstallDone", { username: data.username, password: data.password }), { duration: 20_000 });
      onDone();
    },
    onError: (err) => toast.error((err as Error).message),
  });

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
            if (ok) mutation.mutate();
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

function extractIP(vm: IncusInstance): string {
  if (vm.ip) return vm.ip;
  if (!vm.state?.network) return "";
  for (const [nic, data] of Object.entries(vm.state.network)) {
    if (nic === "lo") continue;
    for (const addr of data.addresses) {
      if (addr.family === "inet" && addr.scope === "global") {
        return addr.address;
      }
    }
  }
  return "";
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
