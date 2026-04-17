import { createFileRoute } from "@tanstack/react-router";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { useClustersQuery } from "@/features/clusters/api";
import { ClusterPicker } from "@/features/clusters/cluster-picker";
import {
  type HANodeInfo,
  useHAStatusQuery,
  useHAEvacuateMutation,
} from "@/features/nodes/api";
import { useConfirm } from "@/shared/components/ui/confirm-dialog";

export const Route = createFileRoute("/admin/ha")({
  component: HAPage,
});

function HAPage() {
  const { t } = useTranslation();
  const { data: clustersData } = useClustersQuery();
  const clusters = clustersData?.clusters ?? [];
  const [clusterName, setClusterName] = useState<string>("");

  useEffect(() => {
    if (!clusterName && clusters.length > 0) {
      setClusterName(clusters[0]!.name);
    }
  }, [clusterName, clusters]);

  const { data: ha, isLoading } = useHAStatusQuery(clusterName);
  const evacuateMutation = useHAEvacuateMutation(clusterName);

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold">HA Failover</h1>
        <div className="flex items-center gap-3">
          {clusters.length > 1 && (
            <ClusterPicker value={clusterName} onChange={setClusterName} />
          )}
          <span className="text-xs text-muted-foreground">
            healing_threshold: {ha?.healing_threshold ?? "—"}s
          </span>
        </div>
      </div>

      {isLoading ? (
        <div className="text-muted-foreground">{t("common.loading")}</div>
      ) : !ha ? (
        <div className="border border-border rounded-lg p-6 text-center text-muted-foreground">
          No cluster configured.
        </div>
      ) : (
        <div className="space-y-6">
          <div className="grid grid-cols-3 gap-4">
            <div className="border border-border rounded-lg bg-card p-4">
              <div className="text-xs text-muted-foreground">HA Status</div>
              <div className="text-lg font-bold mt-1">
                {ha.ha_enabled ? (
                  <span className="text-success">Enabled</span>
                ) : (
                  <span className="text-destructive">Disabled</span>
                )}
              </div>
            </div>
            <div className="border border-border rounded-lg bg-card p-4">
              <div className="text-xs text-muted-foreground">Storage</div>
              <div className="text-lg font-bold mt-1">{ha.storage}</div>
            </div>
            <div className="border border-border rounded-lg bg-card p-4">
              <div className="text-xs text-muted-foreground">Nodes</div>
              <div className="text-lg font-bold mt-1">{ha.nodes.length}</div>
            </div>
          </div>

          <div className="border border-border rounded-lg overflow-hidden">
            <table className="w-full text-sm">
              <thead className="bg-muted/30">
                <tr>
                  <th className="text-left px-4 py-2 font-medium">Node</th>
                  <th className="text-left px-4 py-2 font-medium">Status</th>
                  <th className="text-left px-4 py-2 font-medium">Message</th>
                  <th className="text-right px-4 py-2 font-medium">Actions</th>
                </tr>
              </thead>
              <tbody>
                {ha.nodes.map((node) => (
                  <NodeRow
                    key={node.server_name}
                    node={node}
                    onEvacuate={() => evacuateMutation.mutate(node.server_name)}
                    pending={evacuateMutation.isPending}
                  />
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </div>
  );
}

function NodeRow({
  node,
  onEvacuate,
  pending,
}: {
  node: HANodeInfo;
  onEvacuate: () => void;
  pending: boolean;
}) {
  const { t } = useTranslation();
  const confirm = useConfirm();

  const isOnline = node.status === "Online" || node.status === "ONLINE";

  return (
    <tr className="border-t border-border">
      <td className="px-4 py-2 font-mono">{node.server_name}</td>
      <td className="px-4 py-2">
        <span className={`px-2 py-0.5 rounded text-xs font-medium ${isOnline ? "bg-success/20 text-success" : "bg-destructive/20 text-destructive"}`}>
          {node.status}
        </span>
      </td>
      <td className="px-4 py-2 text-muted-foreground text-xs">{node.message}</td>
      <td className="px-4 py-2 text-right">
        {isOnline && (
          <button
            onClick={async () => {
              const ok = await confirm({
                title: t("deleteConfirm.evacuateTitle"),
                message: t("deleteConfirm.evacuateMessage", { node: node.server_name }),
                destructive: true,
              });
              if (ok) onEvacuate();
            }}
            disabled={pending}
            className="px-3 py-1 text-xs bg-destructive/20 text-destructive rounded hover:bg-destructive/30 disabled:opacity-50"
          >
            {pending ? t("admin.evacuating") : t("admin.evacuate")}
          </button>
        )}
      </td>
    </tr>
  );
}
