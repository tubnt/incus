import { createFileRoute } from "@tanstack/react-router";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { toast } from "sonner";
import { http } from "@/shared/lib/http";
import { queryClient } from "@/shared/lib/query-client";
import { VMMetricsPanel } from "@/features/monitoring/vm-metrics-panel";
import { SnapshotPanel } from "@/features/snapshots/snapshot-panel";

export const Route = createFileRoute("/vm-detail")({
  validateSearch: (search: Record<string, unknown>) => ({
    id: Number(search.id) || 0,
  }),
  component: UserVMDetailPage,
});

interface VMService {
  id: number;
  name: string;
  ip: string | null;
  status: string;
  cpu: number;
  memory_mb: number;
  disk_gb: number;
  os_image: string;
  node: string;
  password: string;
  created_at: string;
}

function UserVMDetailPage() {
  const { id } = Route.useSearch();
  const [tab, setTab] = useState<"overview" | "snapshots">("overview");

  const { data } = useQuery({
    queryKey: ["myService", id],
    queryFn: () => http.get<{ vm: VMService }>(`/portal/services/${id}`),
    enabled: id > 0,
  });

  const vm = data?.vm;

  const actionMutation = useMutation({
    mutationFn: (action: string) => http.post(`/portal/services/${id}/actions/${action}`),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["myService", id] }),
  });

  const resetPwdMutation = useMutation({
    mutationFn: () => http.post<{ password: string; username: string }>(`/portal/services/${id}/reset-password`, {}),
    onSuccess: (data) => {
      queryClient.invalidateQueries({ queryKey: ["myService", id] });
      toast.success(`密码已重置: ${data.password}`, { duration: 15000 });
    },
    onError: () => toast.error("密码重置失败"),
  });

  if (!vm) {
    return <div className="text-muted-foreground p-8">Loading...</div>;
  }

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <div>
          <h1 className="text-2xl font-bold font-mono">{vm.name}</h1>
          <div className="flex items-center gap-3 mt-1">
            <StatusBadge status={vm.status} />
            <span className="text-sm text-muted-foreground">
              {vm.cpu}C / {(vm.memory_mb / 1024).toFixed(0)}G RAM / {vm.disk_gb}G Disk
            </span>
          </div>
        </div>
        <div className="flex gap-2">
          {vm.status === "running" && (
            <>
              <a href={`/console?vm=${vm.name}&cluster=cn-sz-01&project=customers`}
                className="px-3 py-1.5 rounded text-xs font-medium bg-primary/20 text-primary hover:bg-primary/30">
                Console
              </a>
              <ActionBtn label="Stop" onClick={() => actionMutation.mutate("stop")} disabled={actionMutation.isPending} />
              <ActionBtn label="Restart" onClick={() => actionMutation.mutate("restart")} disabled={actionMutation.isPending} />
              <button
                onClick={() => { if (confirm("确认重置密码？新密码将在通知中显示。")) resetPwdMutation.mutate(); }}
                disabled={resetPwdMutation.isPending}
                className="px-3 py-1.5 rounded text-xs font-medium bg-warning/20 text-warning hover:bg-warning/30 disabled:opacity-50"
              >
                {resetPwdMutation.isPending ? "重置中..." : "重置密码"}
              </button>
            </>
          )}
          {vm.status === "stopped" && (
            <ActionBtn label="Start" onClick={() => actionMutation.mutate("start")} disabled={actionMutation.isPending} />
          )}
        </div>
      </div>

      <div className="grid grid-cols-2 md:grid-cols-4 gap-4 mb-6">
        <InfoCard label="IP" value={vm.ip || "—"} mono />
        <InfoCard label="Username" value="ubuntu" mono />
        <InfoCard label="Node" value={vm.node} />
        <InfoCard label="Created" value={new Date(vm.created_at).toLocaleDateString()} />
      </div>

      <div className="flex gap-1 mb-6 border-b border-border">
        {(["overview", "snapshots"] as const).map((t) => (
          <button key={t} onClick={() => setTab(t)}
            className={`px-4 py-2 text-sm font-medium border-b-2 transition ${tab === t ? "border-primary text-primary" : "border-transparent text-muted-foreground hover:text-foreground"}`}>
            {t === "overview" ? "Monitoring" : "Snapshots"}
          </button>
        ))}
      </div>

      {tab === "overview" && (
        <VMMetricsPanel vmName={vm.name} apiBase="/portal" />
      )}

      {tab === "snapshots" && (
        <SnapshotPanel vmName={vm.name} cluster="cn-sz-01" project="customers" apiBase="/portal" />
      )}
    </div>
  );
}

function InfoCard({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="border border-border rounded-lg bg-card p-3">
      <div className="text-xs text-muted-foreground">{label}</div>
      <div className={`text-sm font-medium mt-0.5 ${mono ? "font-mono" : ""}`}>{value}</div>
    </div>
  );
}

function StatusBadge({ status }: { status: string }) {
  const colors: Record<string, string> = {
    running: "bg-success/20 text-success",
    stopped: "bg-muted text-muted-foreground",
    creating: "bg-primary/20 text-primary",
    error: "bg-destructive/20 text-destructive",
  };
  return (
    <span className={`px-2 py-0.5 rounded text-xs font-medium ${colors[status] ?? "bg-muted text-muted-foreground"}`}>
      {status}
    </span>
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
