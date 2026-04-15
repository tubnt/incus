import { createFileRoute } from "@tanstack/react-router";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { http } from "@/shared/lib/http";
import { queryClient } from "@/shared/lib/query-client";
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

const OS_IMAGES = [
  { value: "images:ubuntu/24.04/cloud", label: "Ubuntu 24.04 LTS" },
  { value: "images:debian/12/cloud", label: "Debian 12" },
  { value: "images:rockylinux/9/cloud", label: "Rocky Linux 9" },
];

const SIZES = [
  { label: "Small", cpu: 1, memory_mb: 1024, disk_gb: 25 },
  { label: "Medium", cpu: 2, memory_mb: 2048, disk_gb: 50 },
  { label: "Large", cpu: 4, memory_mb: 4096, disk_gb: 100 },
];

function MyVMs() {
  const [showCreate, setShowCreate] = useState(false);
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
        <button
          onClick={() => setShowCreate(!showCreate)}
          className="px-4 py-2 bg-primary text-primary-foreground rounded-md text-sm font-medium hover:opacity-90"
        >
          {showCreate ? "Cancel" : "+ Create VM"}
        </button>
      </div>

      {showCreate && <CreateVMForm onCreated={() => setShowCreate(false)} />}

      {isLoading ? (
        <div className="text-muted-foreground">Loading...</div>
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
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["myServices"] }),
  });

  return (
    <div className="border border-border rounded-lg bg-card overflow-hidden">
      <div className="p-4 flex items-center justify-between">
        <div>
          <div className="flex items-center gap-3">
            <span className="font-mono font-semibold">{vm.name}</span>
            <StatusBadge status={vm.status} />
          </div>
          <div className="text-sm text-muted-foreground mt-1">
            {vm.cpu}C / {(vm.memory_mb / 1024).toFixed(0)}G RAM / {vm.disk_gb}G Disk · {vm.os_image}
          </div>
          <div className="text-sm mt-2 space-y-0.5">
            <div>IP: <span className="font-mono">{vm.ip || "assigning..."}</span></div>
            <div>Username: <span className="font-mono">ubuntu</span></div>
            <div>Password: <span className="font-mono text-xs">{vm.password}</span></div>
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
        <SnapshotPanel vmName={vm.name} cluster="cn-sz-01" project="customers" />
      )}
    </div>
  );
}

function CreateVMForm({ onCreated }: { onCreated: () => void }) {
  const [size, setSize] = useState(1);
  const [os, setOs] = useState(OS_IMAGES[0]!.value);

  const mutation = useMutation({
    mutationFn: () => {
      const s = SIZES[size]!;
      return http.post("/portal/services", { cpu: s.cpu, memory_mb: s.memory_mb, disk_gb: s.disk_gb, os_image: os });
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["myServices"] });
      onCreated();
    },
  });

  return (
    <div className="border border-border rounded-lg bg-card p-4 mb-6">
      <h3 className="font-semibold mb-3">New Virtual Machine</h3>
      <div className="grid grid-cols-3 gap-2 mb-4">
        {SIZES.map((s, i) => (
          <button key={s.label} onClick={() => setSize(i)}
            className={`p-2 rounded border text-sm text-center ${i === size ? "border-primary bg-primary/10" : "border-border"}`}>
            <div className="font-medium">{s.label}</div>
            <div className="text-xs text-muted-foreground">{s.cpu}C / {(s.memory_mb/1024).toFixed(0)}G / {s.disk_gb}G</div>
          </button>
        ))}
      </div>
      <select value={os} onChange={(e) => setOs(e.target.value)}
        className="w-full px-3 py-2 mb-4 rounded border border-border bg-card text-sm">
        {OS_IMAGES.map((img) => <option key={img.value} value={img.value}>{img.label}</option>)}
      </select>
      {mutation.isError && <div className="text-destructive text-sm mb-2">{(mutation.error as Error).message}</div>}
      <button onClick={() => mutation.mutate()} disabled={mutation.isPending}
        className="w-full py-2 bg-primary text-primary-foreground rounded text-sm font-medium disabled:opacity-50">
        {mutation.isPending ? "Creating..." : "Create VM"}
      </button>
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
