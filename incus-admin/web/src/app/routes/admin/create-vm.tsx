import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { useClustersQuery } from "@/features/clusters/api";
import { ClusterPicker } from "@/features/clusters/cluster-picker";
import { ProjectPicker } from "@/features/projects/project-picker";
import { type AdminCreateVMResult, useAdminCreateVMMutation } from "@/features/vms/api";
import { DEFAULT_OS_IMAGE, OsImagePicker, getOsImageLabel } from "@/features/vms/os-image-picker";

export const Route = createFileRoute("/admin/create-vm")({
  component: CreateVMPage,
});

const PRESETS = [
  { label: "Small", cpu: 1, memory_mb: 1024, disk_gb: 25 },
  { label: "Medium", cpu: 2, memory_mb: 2048, disk_gb: 50 },
  { label: "Large", cpu: 4, memory_mb: 4096, disk_gb: 100 },
  { label: "XLarge", cpu: 8, memory_mb: 8192, disk_gb: 200 },
];

function CreateVMPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [preset, setPreset] = useState(1);
  const [osImage, setOsImage] = useState<string>(DEFAULT_OS_IMAGE);
  const [project, setProject] = useState("");

  const { data: clustersData } = useClustersQuery();
  const clusters = clustersData?.clusters ?? [];
  const [clusterName, setClusterName] = useState<string>("");

  useEffect(() => {
    if (!clusterName && clusters.length > 0) {
      setClusterName(clusters[0]!.name);
    }
  }, [clusterName, clusters]);

  const [result, setResult] = useState<AdminCreateVMResult | null>(null);
  const createMutation = useAdminCreateVMMutation(clusterName);

  const selected = PRESETS[preset]!;

  return (
    <div className="max-w-2xl">
      <h1 className="text-2xl font-bold mb-6">{t("admin.createVmTitle", { defaultValue: "Create VM" })}</h1>

      {result && (
        <div className="border border-success/30 bg-success/10 rounded-lg p-4 mb-6">
          <h3 className="font-semibold mb-2">{t("admin.vmCreated", { defaultValue: "VM Created Successfully" })}</h3>
          <div className="text-sm space-y-1 font-mono">
            <div>Name: {result.vm_name}</div>
            <div>IP: {result.ip}</div>
            <div>Username: {result.username}</div>
            <div>Password: {result.password}</div>
          </div>
          <p className="text-xs text-muted-foreground mt-2">{t("admin.savePwdHint", { defaultValue: "Save these credentials — the password will not be shown again." })}</p>
          <button onClick={() => { setResult(null); navigate({ to: "/admin/vms" }); }}
            className="mt-3 px-4 py-2 bg-primary text-primary-foreground rounded text-sm">
            {t("admin.goToAllVms", { defaultValue: "Go to All VMs" })}
          </button>
        </div>
      )}

      <div className="space-y-6">
        {clusters.length > 1 && (
          <div>
            <label className="block text-sm font-medium mb-2">{t("vm.cluster", { defaultValue: "Cluster" })}</label>
            <ClusterPicker
              value={clusterName}
              onChange={setClusterName}
              className="w-full px-3 py-2 rounded-md border border-border bg-card text-sm"
            />
          </div>
        )}

        <div>
          <label className="block text-sm font-medium mb-2">{t("vm.size", { defaultValue: "Size" })}</label>
          <div className="grid grid-cols-4 gap-2">
            {PRESETS.map((p, i) => (
              <button
                key={p.label}
                onClick={() => setPreset(i)}
                className={`p-3 rounded-lg border text-center text-sm transition ${
                  i === preset
                    ? "border-primary bg-primary/10 text-primary"
                    : "border-border hover:border-primary/50"
                }`}
              >
                <div className="font-semibold">{p.label}</div>
                <div className="text-xs text-muted-foreground mt-1">
                  {p.cpu}C / {(p.memory_mb / 1024).toFixed(0)}G / {p.disk_gb}G
                </div>
              </button>
            ))}
          </div>
        </div>

        <div>
          <label className="block text-sm font-medium mb-2">{t("vm.osImage", { defaultValue: "OS Image" })}</label>
          <OsImagePicker
            value={osImage}
            onChange={setOsImage}
            className="w-full px-3 py-2 rounded-md border border-border bg-card text-sm"
          />
        </div>

        <div>
          <label className="block text-sm font-medium mb-2">{t("vm.project", { defaultValue: "Project" })}</label>
          <ProjectPicker
            clusterName={clusterName}
            value={project}
            onChange={setProject}
          />
        </div>

        <div className="border border-border rounded-lg p-4 bg-card">
          <h3 className="font-medium mb-2">{t("common.summary", { defaultValue: "Summary" })}</h3>
          <div className="text-sm text-muted-foreground space-y-1">
            <div>Cluster: {clusters.find((c) => c.name === clusterName)?.display_name ?? "—"}</div>
            <div>Config: {selected.cpu} vCPU / {(selected.memory_mb / 1024).toFixed(0)} GB RAM / {selected.disk_gb} GB Disk</div>
            <div>OS: {getOsImageLabel(osImage)}</div>
            <div>Project: {project}</div>
            <div>IP: {t("admin.ipAuto", { defaultValue: "auto-assigned from pool" })}</div>
          </div>
        </div>

        {createMutation.isError && (
          <div className="text-destructive text-sm">
            {t("common.failed", { defaultValue: "Failed" })}: {(createMutation.error as Error).message}
          </div>
        )}

        <button
          onClick={() => createMutation.mutate(
            {
              cpu: selected.cpu,
              memory_mb: selected.memory_mb,
              disk_gb: selected.disk_gb,
              os_image: osImage,
              project,
            },
            { onSuccess: (data) => setResult(data) },
          )}
          disabled={createMutation.isPending || !clusterName || !project}
          className="w-full py-3 bg-primary text-primary-foreground rounded-md font-medium hover:opacity-90 disabled:opacity-50"
        >
          {createMutation.isPending ? t("admin.creatingVm", { defaultValue: "Creating VM..." }) : t("admin.createVmTitle", { defaultValue: "Create VM" })}
        </button>
      </div>
    </div>
  );
}
