import { createFileRoute } from "@tanstack/react-router";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { toast } from "sonner";
import { http } from "@/shared/lib/http";
import { queryClient } from "@/shared/lib/query-client";
import { CardSkeleton } from "@/shared/components/ui/skeleton";
import { VMMetricsPanel } from "@/features/monitoring/vm-metrics-panel";
import { SnapshotPanel } from "@/features/snapshots/snapshot-panel";

export const Route = createFileRoute("/vms")({
  component: MyVMs,
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


function MyVMs() {
  const { data, isLoading } = useQuery({
    queryKey: ["myServices"],
    queryFn: () => http.get<{ services: VMService[] }>("/portal/services"),
    refetchInterval: 15_000,
  });

  const services = data?.services ?? [];

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold">My VMs</h1>
        <a
          href="/billing"
          className="px-4 py-2 bg-primary text-primary-foreground rounded-md text-sm font-medium hover:opacity-90"
        >
          + Create VM
        </a>
      </div>

      {isLoading ? (
        <div className="space-y-3">{Array.from({ length: 3 }).map((_, i) => <CardSkeleton key={i} />)}</div>
      ) : services.length === 0 ? (
        <div className="border border-border rounded-lg p-8 text-center text-muted-foreground">
          No VMs yet. Create your first virtual machine.
        </div>
      ) : (
        <div className="space-y-4">
          {services.map((vm) => (
            <VMCard key={vm.id} vm={vm} />
          ))}
        </div>
      )}
    </div>
  );
}

function VMCard({ vm }: { vm: VMService }) {
  const [showMetrics, setShowMetrics] = useState(false);
  const [showSnaps, setShowSnaps] = useState(false);
  const actionMutation = useMutation({
    mutationFn: (action: string) =>
      http.post(`/portal/services/${vm.id}/actions/${action}`),
    onSuccess: (_data, action) => {
      queryClient.invalidateQueries({ queryKey: ["myServices"] });
      toast.success(`${vm.name}: ${action} 操作已提交`);
    },
    onError: (_err, action) => {
      toast.error(`${vm.name}: ${action} 操作失败`);
    },
  });

  return (
    <div className="border border-border rounded-lg bg-card overflow-hidden">
      <div className="p-4 flex items-center justify-between">
        <div>
          <div className="flex items-center gap-3">
            <a href={`/vm-detail?id=${vm.id}`} className="font-mono font-semibold text-primary hover:underline">{vm.name}</a>
            <StatusBadge status={vm.status} />
          </div>
          <div className="text-sm text-muted-foreground mt-1">
            {vm.cpu}C / {(vm.memory_mb / 1024).toFixed(0)}G RAM / {vm.disk_gb}G Disk · {vm.os_image}
          </div>
          <div className="text-sm mt-2 space-y-0.5">
            <div>IP: <span className="font-mono">{vm.ip || "assigning..."}</span></div>
            <div>Username: <span className="font-mono">ubuntu</span></div>
            <div>Password: <PasswordReveal value={vm.password} /></div>
            <div>Node: {vm.node} · Created: {new Date(vm.created_at).toLocaleDateString()}</div>
          </div>
        </div>
        <div className="flex flex-col gap-2">
          {vm.status === "running" && (
            <>
              <a href={`/console?vm=${vm.name}&cluster=cn-sz-01&project=customers`}
                className="px-3 py-1.5 rounded text-xs font-medium bg-primary/20 text-primary hover:bg-primary/30 text-center">
                Console
              </a>
              <ActionBtn label="Monitor" onClick={() => setShowMetrics(!showMetrics)} disabled={false} />
              <ActionBtn label="Snaps" onClick={() => setShowSnaps(!showSnaps)} disabled={false} />
              <ActionBtn label="Stop" onClick={() => actionMutation.mutate("stop")} disabled={actionMutation.isPending} />
              <ActionBtn label="Restart" onClick={() => actionMutation.mutate("restart")} disabled={actionMutation.isPending} />
            </>
          )}
          {vm.status === "stopped" && (
            <ActionBtn label="Start" onClick={() => actionMutation.mutate("start")} disabled={actionMutation.isPending} />
          )}
        </div>
      </div>
      {showMetrics && vm.status === "running" && (
        <VMMetricsPanel vmName={vm.name} apiBase="/portal" />
      )}
      {showSnaps && (
        <SnapshotPanel vmName={vm.name} cluster="cn-sz-01" project="customers" apiBase="/portal" />
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

function PasswordReveal({ value }: { value: string }) {
  const [show, setShow] = useState(false);
  if (!value) return <span className="text-muted-foreground text-xs">—</span>;
  return (
    <button onClick={() => setShow(!show)} className="font-mono text-xs hover:text-primary">
      {show ? value : "••••••••••••"}
    </button>
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
