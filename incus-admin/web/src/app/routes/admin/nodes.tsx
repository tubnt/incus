import { createFileRoute } from "@tanstack/react-router";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { http } from "@/shared/lib/http";
import { queryClient } from "@/shared/lib/query-client";
import { useTranslation } from "react-i18next";
import { useConfirm } from "@/shared/components/ui/confirm-dialog";

export const Route = createFileRoute("/admin/nodes")({
  component: NodesPage,
});

interface ClusterNode {
  cluster: string;
  server_name: string;
  url: string;
  status: string;
  message: string;
  architecture: string;
  roles: string[];
  description: string;
}

interface NodeInstance {
  name: string;
  status: string;
  type: string;
  location: string;
}

function NodesPage() {
  const { t } = useTranslation();
  const [selectedNode, setSelectedNode] = useState<string | null>(null);
  const [selectedCluster, setSelectedCluster] = useState<string>("");

  const { data, isLoading } = useQuery({
    queryKey: ["adminNodes"],
    queryFn: () => http.get<{ nodes: ClusterNode[] }>("/admin/nodes"),
    refetchInterval: 15000,
  });

  const nodes = data?.nodes ?? [];

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold">{t("admin.nodes.title", "集群节点")}</h1>
        <div className="flex gap-2">
          <a
            href="/admin/node-join"
            className="px-3 py-1.5 text-sm bg-primary text-primary-foreground rounded hover:opacity-90"
          >
            {t("admin.nodes.joinWizard", "+ 加入节点")}
          </a>
          <button
            onClick={() =>
              queryClient.invalidateQueries({ queryKey: ["adminNodes"] })
            }
            className="px-3 py-1.5 text-sm border border-border rounded hover:bg-muted"
          >
            {t("common.refresh", "刷新")}
          </button>
        </div>
      </div>

      {isLoading ? (
        <div className="text-muted-foreground">
          {t("common.loading", "加载中...")}
        </div>
      ) : nodes.length === 0 ? (
        <div className="border border-border rounded-lg p-6 text-center text-muted-foreground">
          {t("admin.nodes.empty", "未发现集群节点。请先添加集群连接。")}
        </div>
      ) : (
        <div className="space-y-3">
          {nodes.map((node) => (
            <NodeCard
              key={`${node.cluster}-${node.server_name}`}
              node={node}
              isSelected={
                selectedNode === node.server_name &&
                selectedCluster === node.cluster
              }
              onSelect={() => {
                if (
                  selectedNode === node.server_name &&
                  selectedCluster === node.cluster
                ) {
                  setSelectedNode(null);
                } else {
                  setSelectedNode(node.server_name);
                  setSelectedCluster(node.cluster);
                }
              }}
            />
          ))}
        </div>
      )}
    </div>
  );
}

function NodeCard({
  node,
  isSelected,
  onSelect,
}: {
  node: ClusterNode;
  isSelected: boolean;
  onSelect: () => void;
}) {
  const statusColor =
    node.status === "Online"
      ? "bg-success/20 text-success"
      : node.status === "Evacuated"
        ? "bg-warning/20 text-warning"
        : "bg-destructive/20 text-destructive";

  return (
    <div className="border border-border rounded-lg overflow-hidden">
      <button
        type="button"
        onClick={onSelect}
        className="w-full px-4 py-3 flex items-center justify-between hover:bg-muted/20 transition-colors text-left"
      >
        <div className="flex items-center gap-4">
          <div>
            <div className="font-semibold">{node.server_name}</div>
            <div className="text-xs text-muted-foreground">
              {node.cluster} &middot; {node.url}
            </div>
          </div>
        </div>
        <div className="flex items-center gap-3">
          {node.roles && node.roles.length > 0 && (
            <div className="flex gap-1">
              {node.roles.map((role) => (
                <span
                  key={role}
                  className="px-1.5 py-0.5 text-xs bg-muted rounded"
                >
                  {role}
                </span>
              ))}
            </div>
          )}
          <span
            className={`px-2 py-0.5 rounded text-xs font-medium ${statusColor}`}
          >
            {node.status}
          </span>
          <span className="text-xs text-muted-foreground">
            {isSelected ? "▲" : "▼"}
          </span>
        </div>
      </button>

      {isSelected && (
        <NodeDetail
          nodeName={node.server_name}
          clusterName={node.cluster}
          nodeStatus={node.status}
        />
      )}
    </div>
  );
}

function NodeDetail({
  nodeName,
  clusterName,
  nodeStatus,
}: {
  nodeName: string;
  clusterName: string;
  nodeStatus: string;
}) {
  const { t } = useTranslation();
  const confirm = useConfirm();

  const { data, isLoading } = useQuery({
    queryKey: ["adminNodeDetail", clusterName, nodeName],
    queryFn: () =>
      http.get<{ node: Record<string, unknown>; instances: NodeInstance[] }>(
        `/admin/nodes/${nodeName}?cluster=${clusterName}`,
      ),
  });

  const evacuateMutation = useMutation({
    mutationFn: () =>
      http.post(`/admin/nodes/${nodeName}/evacuate?cluster=${clusterName}`, {}),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["adminNodes"] });
      queryClient.invalidateQueries({
        queryKey: ["adminNodeDetail", clusterName, nodeName],
      });
    },
  });

  const restoreMutation = useMutation({
    mutationFn: () =>
      http.post(`/admin/nodes/${nodeName}/restore?cluster=${clusterName}`, {}),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["adminNodes"] });
      queryClient.invalidateQueries({
        queryKey: ["adminNodeDetail", clusterName, nodeName],
      });
    },
  });

  const instances = data?.instances ?? [];
  const nodeInfo = data?.node as Record<string, unknown> | undefined;

  return (
    <div className="border-t border-border bg-muted/10 p-4">
      {isLoading ? (
        <div className="text-sm text-muted-foreground">
          {t("common.loading", "加载中...")}
        </div>
      ) : (
        <div className="space-y-4">
          {/* 节点信息 */}
          {nodeInfo && (
            <div className="grid grid-cols-2 md:grid-cols-4 gap-3 text-sm">
              <InfoItem
                label={t("admin.nodes.arch", "架构")}
                value={String(nodeInfo.architecture ?? "-")}
              />
              <InfoItem
                label={t("admin.nodes.status", "状态")}
                value={String(nodeInfo.status ?? "-")}
              />
              <InfoItem
                label={t("admin.nodes.message", "消息")}
                value={String(nodeInfo.message ?? "-")}
              />
              <InfoItem
                label={t("admin.nodes.roles", "角色")}
                value={
                  Array.isArray(nodeInfo.roles)
                    ? (nodeInfo.roles as string[]).join(", ") || "-"
                    : "-"
                }
              />
            </div>
          )}

          {/* 维护模式操作 */}
          <div className="flex items-center gap-3">
            {nodeStatus === "Online" ? (
              <button
                onClick={async () => {
                  const ok = await confirm({
                    title: t("deleteConfirm.evacuateTitle"),
                    message: t("deleteConfirm.evacuateMessage", { node: nodeName }),
                    destructive: true,
                  });
                  if (ok) evacuateMutation.mutate();
                }}
                disabled={evacuateMutation.isPending}
                className="px-3 py-1.5 text-sm border border-warning/50 text-warning rounded hover:bg-warning/10 disabled:opacity-50"
              >
                {evacuateMutation.isPending
                  ? t("admin.evacuating")
                  : t("admin.enterMaintenance")}
              </button>
            ) : nodeStatus === "Evacuated" ? (
              <button
                onClick={() => restoreMutation.mutate()}
                disabled={restoreMutation.isPending}
                className="px-3 py-1.5 text-sm border border-success/50 text-success rounded hover:bg-success/10 disabled:opacity-50"
              >
                {restoreMutation.isPending
                  ? t("admin.nodes.restoring", "恢复中...")
                  : t("admin.nodes.restore", "恢复节点")}
              </button>
            ) : null}

            {(evacuateMutation.isError || restoreMutation.isError) && (
              <span className="text-xs text-destructive">
                {(
                  (evacuateMutation.error ?? restoreMutation.error) as Error
                )?.message ?? "操作失败"}
              </span>
            )}
          </div>

          {/* 实例列表 */}
          <div>
            <h4 className="text-sm font-semibold mb-2">
              {t("admin.nodes.instances", "节点实例")} ({instances.length})
            </h4>
            {instances.length === 0 ? (
              <div className="text-xs text-muted-foreground">
                {t("admin.nodes.noInstances", "该节点暂无实例")}
              </div>
            ) : (
              <div className="border border-border rounded overflow-hidden">
                <table className="w-full text-xs">
                  <thead className="bg-muted/30">
                    <tr>
                      <th className="text-left px-3 py-1.5 font-medium">
                        {t("admin.nodes.instanceName", "实例名")}
                      </th>
                      <th className="text-left px-3 py-1.5 font-medium">
                        {t("admin.nodes.instanceType", "类型")}
                      </th>
                      <th className="text-left px-3 py-1.5 font-medium">
                        {t("admin.nodes.instanceStatus", "状态")}
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {instances.map((inst) => (
                      <tr
                        key={inst.name}
                        className="border-t border-border"
                      >
                        <td className="px-3 py-1.5 font-mono">
                          {inst.name}
                        </td>
                        <td className="px-3 py-1.5 text-muted-foreground">
                          {inst.type}
                        </td>
                        <td className="px-3 py-1.5">
                          <span
                            className={`px-1.5 py-0.5 rounded text-xs ${
                              inst.status === "Running"
                                ? "bg-success/20 text-success"
                                : "bg-muted text-muted-foreground"
                            }`}
                          >
                            {inst.status}
                          </span>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  );
}

function InfoItem({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <div className="text-xs text-muted-foreground">{label}</div>
      <div className="font-medium">{value}</div>
    </div>
  );
}
