import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import { VMMetricsPanel } from "@/features/monitoring/vm-metrics-panel";
import { NodePicker } from "@/features/nodes/node-picker";
import { SnapshotPanel } from "@/features/snapshots/snapshot-panel";
import { useConfirm } from "@/shared/components/ui/confirm-dialog";
import {
  useAdminVMDetailQuery,
  useDeleteVMMutation,
  useMigrateVMMutation,
  useVMStateMutation,
} from "@/features/vms/api";

export const Route = createFileRoute("/admin/vm-detail")({
  validateSearch: (search: Record<string, unknown>) => ({
    name: (search.name as string) || "",
    cluster: (search.cluster as string) || "",
    project: (search.project as string) || "customers",
  }),
  component: VMDetailPage,
});

function VMDetailPage() {
  const { t } = useTranslation();
  const { name, cluster, project } = Route.useSearch();
  const navigate = useNavigate();
  const confirm = useConfirm();
  const [tab, setTab] = useState<"overview" | "console" | "snapshots">("overview");
  const [migrateTarget, setMigrateTarget] = useState("");
  const [showMigrate, setShowMigrate] = useState(false);

  const { data: detailData, isLoading: detailLoading, isError: detailError } = useAdminVMDetailQuery(
    cluster,
    name,
    project || undefined,
    15_000,
  );
  const resolvedProject = detailData?.project || project;
  const currentNode = detailData?.vm?.location ?? "";

  const stateMutation = useVMStateMutation();
  const migrateMutation = useMigrateVMMutation();
  const deleteMutation = useDeleteVMMutation();

  const runAction = (action: string) =>
    stateMutation.mutate(
      { name, action, cluster, project },
      {
        onSuccess: () => toast.success(`${name}: ${action} ${t("vm.actionSubmitted")}`),
        onError: () => toast.error(`${name}: ${action} ${t("vm.actionFailed")}`),
      },
    );

  const runMigrate = (target: string) =>
    migrateMutation.mutate(
      { name, cluster, project, target_node: target },
      {
        onSuccess: () => {
          toast.success(`${name} ${t("admin.migrated", { defaultValue: "migrated" })}`);
          setShowMigrate(false);
          setMigrateTarget("");
        },
        onError: () => toast.error(`${name} ${t("admin.migrateFailed", { defaultValue: "migration failed" })}`),
      },
    );

  const runDelete = () =>
    deleteMutation.mutate(
      { name, cluster, project },
      { onSuccess: () => navigate({ to: "/admin/vms" }) },
    );

  if (!name || !cluster) {
    return <div className="text-muted-foreground p-8">Missing vm name or cluster.</div>;
  }

  if (detailLoading) {
    return <div className="text-muted-foreground p-8">{t("common.loading")}</div>;
  }

  if (detailError || !detailData?.vm) {
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
          <p className="text-sm text-muted-foreground">
            {cluster} / {resolvedProject}
            {currentNode ? ` · ${currentNode}` : ""}
          </p>
        </div>
        <div className="flex gap-2">
          <a href={`/console?vm=${name}&cluster=${cluster}&project=${project}`}
            className="px-3 py-1.5 rounded text-xs font-medium bg-primary/20 text-primary hover:bg-primary/30">
            {t("vm.console")}
          </a>
          <ActionBtn label={t("vm.start")} onClick={() => runAction("start")} disabled={stateMutation.isPending} />
          <ActionBtn label={t("vm.stop")} onClick={() => runAction("stop")} disabled={stateMutation.isPending} />
          <ActionBtn label={t("vm.restart")} onClick={() => runAction("restart")} disabled={stateMutation.isPending} />
          <button
            onClick={() => setShowMigrate(!showMigrate)}
            className="px-3 py-1.5 rounded text-xs font-medium bg-primary/10 text-primary hover:bg-primary/20"
          >
            {t("admin.migrate", { defaultValue: "Migrate" })}
          </button>
          <button
            onClick={async () => {
              const ok = await confirm({
                title: t("deleteConfirm.vmTitle"),
                message: t("deleteConfirm.vmMessage", { name }),
                destructive: true,
              });
              if (ok) runDelete();
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
          <h3 className="font-semibold text-sm mb-2">{t("admin.migrateTitle", { defaultValue: "Migrate to target node" })}</h3>
          <div className="flex gap-2">
            <NodePicker
              clusterName={cluster}
              value={migrateTarget}
              onChange={setMigrateTarget}
              excludeNodes={currentNode ? [currentNode] : []}
              placeholder={t("admin.targetNode", { defaultValue: "Target node name" })}
              className="flex-1 font-mono"
            />
            <button
              onClick={async () => {
                if (!migrateTarget) return;
                const ok = await confirm({
                  title: t("deleteConfirm.migrateTitle"),
                  message: t("deleteConfirm.migrateMessage", { name, target: migrateTarget }),
                });
                if (ok) runMigrate(migrateTarget);
              }}
              disabled={migrateMutation.isPending || !migrateTarget}
              className="px-4 py-2 bg-primary text-primary-foreground rounded text-sm font-medium disabled:opacity-50"
            >
              {migrateMutation.isPending ? "..." : t("admin.migrateRun", { defaultValue: "Migrate" })}
            </button>
          </div>
        </div>
      )}

      <div className="flex gap-1 mb-6 border-b border-border">
        {(["overview", "console", "snapshots"] as const).map((tKey) => (
          <button key={tKey} onClick={() => setTab(tKey)}
            className={`px-4 py-2 text-sm font-medium border-b-2 transition ${tab === tKey ? "border-primary text-primary" : "border-transparent text-muted-foreground hover:text-foreground"}`}>
            {tKey === "overview"
              ? t("vm.tabOverview", { defaultValue: "Overview" })
              : tKey === "console"
              ? t("vm.console")
              : t("vm.snapshots")}
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
