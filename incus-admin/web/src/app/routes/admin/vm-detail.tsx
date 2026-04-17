import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import { http } from "@/shared/lib/http";
import { queryClient } from "@/shared/lib/query-client";
import { VMMetricsPanel } from "@/features/monitoring/vm-metrics-panel";
import { SnapshotPanel } from "@/features/snapshots/snapshot-panel";
import { useConfirm } from "@/shared/components/ui/confirm-dialog";

export const Route = createFileRoute("/admin/vm-detail")({
  validateSearch: (search: Record<string, unknown>) => ({
    name: (search.name as string) || "",
    cluster: (search.cluster as string) || "",
    project: (search.project as string) || "customers",
  }),
  component: VMDetailPage,
});

interface ClusterVMsResponse {
  vms: Array<{ name: string; status: string; project?: string }>;
  count: number;
}

function VMDetailPage() {
  const { t } = useTranslation();
  const { name, cluster, project } = Route.useSearch();
  const navigate = useNavigate();
  const confirm = useConfirm();
  const [tab, setTab] = useState<"overview" | "console" | "snapshots">("overview");
  const [migrateTarget, setMigrateTarget] = useState("");
  const [showMigrate, setShowMigrate] = useState(false);

  const { data: vmsData, isLoading: vmsLoading } = useQuery({
    queryKey: ["adminClusterVMs", cluster],
    queryFn: () => http.get<ClusterVMsResponse>(`/admin/clusters/${cluster}/vms`),
    enabled: !!cluster,
  });

  const exists = !vmsLoading && !!vmsData?.vms?.some((v) => v.name === name);

  const stateMutation = useMutation({
    mutationFn: (action: string) =>
      http.put(`/admin/vms/${name}/state`, { action, cluster, project }),
    onSuccess: (_data, action) => {
      queryClient.invalidateQueries({ queryKey: ["adminClusterVMs"] });
      toast.success(`${name}: ${action} submitted`);
    },
    onError: (_err, action) => toast.error(`${name}: ${action} failed`),
  });

  const migrateMutation = useMutation({
    mutationFn: (targetNode: string) =>
      http.post(`/admin/vms/${name}/migrate`, { cluster, project, target_node: targetNode }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["adminClusterVMs"] });
      toast.success(`${name} migrated`);
      setShowMigrate(false);
      setMigrateTarget("");
    },
    onError: () => toast.error(`${name} migration failed`),
  });

  const deleteMutation = useMutation({
    mutationFn: () => http.delete(`/admin/vms/${name}`, { cluster, project }),
    onSuccess: () => navigate({ to: "/admin/vms" }),
  });

  if (!name || !cluster) {
    return <div className="text-muted-foreground p-8">Missing vm name or cluster.</div>;
  }

  if (vmsLoading) {
    return <div className="text-muted-foreground p-8">{t("common.loading")}</div>;
  }

  if (!exists) {
    return (
      <div className="flex flex-col items-center justify-center py-20 gap-4">
        <div className="text-2xl font-semibold">{t("vm.notFoundTitle")}</div>
        <div className="text-sm text-muted-foreground">{t("vm.notFoundHint")}</div>
        <button
          onClick={() => navigate({ to: "/admin/vms" })}
          className="px-4 py-2 bg-primary text-primary-foreground rounded-md text-sm font-medium hover:opacity-90"
        >
          {t("vm.backToList")}
        </button>
      </div>
    );
  }

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <div>
          <h1 className="text-2xl font-bold font-mono">{name}</h1>
          <p className="text-sm text-muted-foreground">{cluster} / {project}</p>
        </div>
        <div className="flex gap-2">
          <a href={`/console?vm=${name}&cluster=${cluster}&project=${project}`}
            className="px-3 py-1.5 rounded text-xs font-medium bg-primary/20 text-primary hover:bg-primary/30">
            Console
          </a>
          <ActionBtn label={t("vm.start")} onClick={() => stateMutation.mutate("start")} disabled={stateMutation.isPending} />
          <ActionBtn label={t("vm.stop")} onClick={() => stateMutation.mutate("stop")} disabled={stateMutation.isPending} />
          <ActionBtn label={t("vm.restart")} onClick={() => stateMutation.mutate("restart")} disabled={stateMutation.isPending} />
          <button
            onClick={() => setShowMigrate(!showMigrate)}
            className="px-3 py-1.5 rounded text-xs font-medium bg-primary/10 text-primary hover:bg-primary/20"
          >
            {t("admin.migrate", "Migrate")}
          </button>
          <button
            onClick={async () => {
              const ok = await confirm({
                title: t("deleteConfirm.vmTitle"),
                message: t("deleteConfirm.vmMessage", { name }),
                destructive: true,
              });
              if (ok) deleteMutation.mutate();
            }}
            disabled={deleteMutation.isPending}
            className="px-3 py-1.5 rounded text-xs font-medium bg-destructive/20 text-destructive hover:bg-destructive/30 disabled:opacity-50"
          >
            {t("vm.delete")}
          </button>
        </div>
      </div>

      {showMigrate && (
        <div className="border border-border rounded-lg bg-card p-4 mb-4">
          <h3 className="font-semibold text-sm mb-2">{t("admin.migrateTitle", "Migrate to target node")}</h3>
          <div className="flex gap-2">
            <input
              type="text"
              placeholder={t("admin.targetNode", "Target node name")}
              value={migrateTarget}
              onChange={(e) => setMigrateTarget(e.target.value)}
              className="flex-1 px-3 py-2 rounded border border-border bg-card text-sm font-mono"
            />
            <button
              onClick={async () => {
                if (!migrateTarget) return;
                const ok = await confirm({
                  title: t("deleteConfirm.migrateTitle"),
                  message: t("deleteConfirm.migrateMessage", { name, target: migrateTarget }),
                });
                if (ok) migrateMutation.mutate(migrateTarget);
              }}
              disabled={migrateMutation.isPending || !migrateTarget}
              className="px-4 py-2 bg-primary text-primary-foreground rounded text-sm font-medium disabled:opacity-50"
            >
              {migrateMutation.isPending ? "..." : t("admin.migrateRun", "Migrate")}
            </button>
          </div>
        </div>
      )}

      <div className="flex gap-1 mb-6 border-b border-border">
        {(["overview", "console", "snapshots"] as const).map((tKey) => (
          <button key={tKey} onClick={() => setTab(tKey)}
            className={`px-4 py-2 text-sm font-medium border-b-2 transition ${tab === tKey ? "border-primary text-primary" : "border-transparent text-muted-foreground hover:text-foreground"}`}>
            {tKey === "overview" ? "Overview" : tKey === "console" ? "Console" : "Snapshots"}
          </button>
        ))}
      </div>

      {tab === "overview" && (
        <div className="space-y-6">
          <VMMetricsPanel vmName={name} apiBase="/admin" cluster={cluster} />
        </div>
      )}

      {tab === "console" && (
        <div className="border border-border rounded-lg overflow-hidden">
          <iframe
            src={`/console?vm=${name}&cluster=${cluster}&project=${project}`}
            className="w-full h-[500px] bg-black"
            title="VM Console"
          />
        </div>
      )}

      {tab === "snapshots" && (
        <SnapshotPanel vmName={name} cluster={cluster} project={project} />
      )}
    </div>
  );
}

function ActionBtn({ label, onClick, disabled }: { label: string; onClick: () => void; disabled: boolean }) {
  return (
    <button onClick={onClick} disabled={disabled}
      className="px-3 py-1.5 rounded text-xs font-medium bg-muted/50 text-muted-foreground hover:bg-muted disabled:opacity-50">
      {label}
    </button>
  );
}
