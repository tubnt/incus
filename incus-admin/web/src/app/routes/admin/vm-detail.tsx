import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useMutation } from "@tanstack/react-query";
import { useState } from "react";
import { toast } from "sonner";
import { http } from "@/shared/lib/http";
import { queryClient } from "@/shared/lib/query-client";
import { VMMetricsPanel } from "@/features/monitoring/vm-metrics-panel";
import { SnapshotPanel } from "@/features/snapshots/snapshot-panel";

export const Route = createFileRoute("/admin/vm-detail")({
  validateSearch: (search: Record<string, unknown>) => ({
    name: (search.name as string) || "",
    cluster: (search.cluster as string) || "",
    project: (search.project as string) || "default",
  }),
  component: VMDetailPage,
});

function VMDetailPage() {
  const { name, cluster, project } = Route.useSearch();
  const navigate = useNavigate();
  const [tab, setTab] = useState<"overview" | "console" | "snapshots">("overview");
  const [migrateTarget, setMigrateTarget] = useState("");
  const [showMigrate, setShowMigrate] = useState(false);

  const stateMutation = useMutation({
    mutationFn: (action: string) =>
      http.put(`/admin/vms/${name}/state`, { action, cluster, project }),
    onSuccess: (_data, action) => {
      queryClient.invalidateQueries({ queryKey: ["adminClusterVMs"] });
      toast.success(`${name}: ${action} 已提交`);
    },
    onError: (_err, action) => toast.error(`${name}: ${action} 失败`),
  });

  const migrateMutation = useMutation({
    mutationFn: (targetNode: string) =>
      http.post(`/admin/vms/${name}/migrate`, { cluster, project, target_node: targetNode }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["adminClusterVMs"] });
      toast.success(`${name} 迁移完成`);
      setShowMigrate(false);
      setMigrateTarget("");
    },
    onError: () => toast.error(`${name} 迁移失败`),
  });

  const deleteMutation = useMutation({
    mutationFn: () => http.delete(`/admin/vms/${name}`, { cluster, project }),
    onSuccess: () => navigate({ to: "/admin/vms" }),
  });

  if (!name || !cluster) {
    return <div className="text-muted-foreground p-8">Missing vm name or cluster.</div>;
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
          <ActionBtn label="Start" onClick={() => stateMutation.mutate("start")} disabled={stateMutation.isPending} />
          <ActionBtn label="Stop" onClick={() => stateMutation.mutate("stop")} disabled={stateMutation.isPending} />
          <ActionBtn label="Restart" onClick={() => stateMutation.mutate("restart")} disabled={stateMutation.isPending} />
          <button
            onClick={() => setShowMigrate(!showMigrate)}
            className="px-3 py-1.5 rounded text-xs font-medium bg-primary/10 text-primary hover:bg-primary/20"
          >
            迁移
          </button>
          <button
            onClick={() => { if (confirm(`Delete ${name}?`)) deleteMutation.mutate(); }}
            disabled={deleteMutation.isPending}
            className="px-3 py-1.5 rounded text-xs font-medium bg-destructive/20 text-destructive hover:bg-destructive/30 disabled:opacity-50"
          >
            Delete
          </button>
        </div>
      </div>

      {showMigrate && (
        <div className="border border-border rounded-lg bg-card p-4 mb-4">
          <h3 className="font-semibold text-sm mb-2">迁移到目标节点</h3>
          <div className="flex gap-2">
            <input
              type="text"
              placeholder="目标节点名称"
              value={migrateTarget}
              onChange={(e) => setMigrateTarget(e.target.value)}
              className="flex-1 px-3 py-2 rounded border border-border bg-card text-sm font-mono"
            />
            <button
              onClick={() => {
                if (migrateTarget && confirm(`确认将 ${name} 迁移到 ${migrateTarget}？`)) {
                  migrateMutation.mutate(migrateTarget);
                }
              }}
              disabled={migrateMutation.isPending || !migrateTarget}
              className="px-4 py-2 bg-primary text-primary-foreground rounded text-sm font-medium disabled:opacity-50"
            >
              {migrateMutation.isPending ? "迁移中..." : "执行迁移"}
            </button>
          </div>
        </div>
      )}

      <div className="flex gap-1 mb-6 border-b border-border">
        {(["overview", "console", "snapshots"] as const).map((t) => (
          <button key={t} onClick={() => setTab(t)}
            className={`px-4 py-2 text-sm font-medium border-b-2 transition ${tab === t ? "border-primary text-primary" : "border-transparent text-muted-foreground hover:text-foreground"}`}>
            {t === "overview" ? "Overview" : t === "console" ? "Console" : "Snapshots"}
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
