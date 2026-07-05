import type {ClusterNode} from "@/features/nodes/api";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { useJobQuery } from "@/features/jobs/api";
import { AIDiagnosePanel } from "@/features/jobs/components/ai-diagnose-panel";
import { JobProgress } from "@/features/jobs/components/job-progress";
import { useJobStream } from "@/features/jobs/use-job-stream";
import {

  downloadClusterEnvScript,
  nodeKeys,
  useAdminNodeDetailQuery,
  useAdminNodesQuery,
  useNodeEvacuateMutation,
  useNodeMaintenanceMutation,
  useNodeRestoreMutation,
  useRemoveNodeMutation
} from "@/features/nodes/api";
import { AlertBanner } from "@/features/nodes/components/alert-banner";
import { NodeTopologyStrip } from "@/features/nodes/components/node-topology-strip";
import { RebalancePanel } from "@/features/nodes/components/rebalance-panel";
import {
  PageContent,
  PageHeader,
  PageShell,
} from "@/shared/components/page/page-shell";
import { Button, buttonVariants } from "@/shared/components/ui/button";
import { Card } from "@/shared/components/ui/card";
import { useConfirm } from "@/shared/components/ui/confirm-dialog";
import { EmptyState } from "@/shared/components/ui/empty-state";
import { StatusPill } from "@/shared/components/ui/status";
import { formatError } from "@/shared/lib/http";
import { queryClient } from "@/shared/lib/query-client";
import { formatNodeMessage, formatNodeStatus, formatVmStatus, isStatus } from "@/shared/lib/status-i18n";

export const Route = createFileRoute("/admin/nodes")({
  component: NodesPage,
});

function NodesPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [selectedNode, setSelectedNode] = useState<string | null>(null);
  const [selectedCluster, setSelectedCluster] = useState<string>("");

  const { data, isLoading } = useAdminNodesQuery();

  // PLAN-037 / OPS-040：稳定 nodes 引用，避免 useMemo 依赖每渲染都新建。
  const nodes = useMemo(() => data?.nodes ?? [], [data]);

  // PLAN-037 / OPS-040：每集群一个 strip + RebalancePanel
  const clustersInPage = useMemo(() => {
    const set = new Set<string>();
    for (const n of nodes) set.add(n.cluster);
    return Array.from(set).sort();
  }, [nodes]);

  return (
    <PageShell>
      <PageHeader
        title={t("admin.nodes.title", "集群节点")}
        actions={
          <div className="flex gap-2">
            <Link
              to="/admin/node-join"
              className={buttonVariants({ variant: "primary", size: "sm" })}
            >
              {t("admin.nodes.joinWizard", "+ 加入节点")}
            </Link>
            {/* OPS-024 C2 + OPS-028 P3.4：下载 cluster-env.sh。改用 fetch
                走 stepUp 401 拦截 → 跳 OIDC，避免浏览器直链落到 step-up JSON
                响应裸页。下载后 ops 在 cluster bootstrap 节点放到
                cluster/configs/ 覆盖原 file。*/}
            {nodes.length > 0
              ? (
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => {
                      // OPS-028 M1：catch 防 unhandled rejection；step-up 跳转
                      // 不会到这里（已 redirect），其他错误显式 toast。
                      downloadClusterEnvScript(nodes[0]!.cluster).catch((err: unknown) => {
                        const msg = err instanceof Error ? err.message : String(err);
                        toast.error(
                          t("admin.nodes.downloadEnvScriptFailed", { defaultValue: "下载失败：{{msg}}", msg }),
                        );
                      });
                    }}
                  >
                    {t("admin.nodes.downloadEnvScript", "下载 cluster-env.sh")}
                  </Button>
                )
              : null}
            <Button
              variant="ghost"
              size="sm"
              onClick={() =>
                queryClient.invalidateQueries({ queryKey: nodeKeys.all })
              }
            >
              {t("common.refresh", "刷新")}
            </Button>
          </div>
        }
      />
      <PageContent>
        {isLoading ? (
          <div className="text-muted-foreground">
            {t("common.loading", "加载中...")}
          </div>
        ) : nodes.length === 0 ? (
          <EmptyState
            title={t("admin.nodes.empty", "未发现集群节点。请先添加集群连接。")}
          />
        ) : (
          <div className="space-y-3">
            {/* PLAN-039 / OPS-044：watchdog 持续不均衡告警 banner */}
            <AlertBanner />
            {/* PLAN-037 / OPS-040：每集群一个分布概览 strip。
                点击 chip 跳到 admin/vms?node=X，借用 admin/vms 的过滤+批量迁移。 */}
            {clustersInPage.map((c) => (
              <NodeTopologyStrip
                key={`topo-${c}`}
                clusterName={c}
                onNodeClick={(n) =>
                  navigate({ to: "/admin/vms", search: { node: n } })
                }
              />
            ))}
            {clustersInPage.map((c) => (
              <RebalancePanel key={`rebalance-${c}`} clusterName={c} />
            ))}
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
      </PageContent>
    </PageShell>
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
  const { t } = useTranslation();
  // Session-2 N-06 / PLAN-051 §2-I：归一比较，消除大小写敏感
  const statusKind = isStatus(node.status, "Online")
    ? "success"
    : isStatus(node.status, "Evacuated")
      ? "warning"
      : "error";

  return (
    <Card className="overflow-x-auto">
      <button
        type="button"
        onClick={onSelect}
        className="w-full px-4 py-3 flex items-center justify-between hover:bg-surface-2 transition-colors text-left"
      >
        <div className="flex items-center gap-4">
          <div>
            <div className="font-strong">{node.server_name}</div>
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
                  className="px-1.5 py-0.5 text-xs bg-surface-2 rounded"
                >
                  {role}
                </span>
              ))}
            </div>
          )}
          <StatusPill status={statusKind}>{formatNodeStatus(t, node.status)}</StatusPill>
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
    </Card>
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
  const [removeJobId, setRemoveJobId] = useState<number | null>(null);

  const { data, isLoading } = useAdminNodeDetailQuery(clusterName, nodeName);
  const evacuateMutation = useNodeEvacuateMutation(clusterName, nodeName);
  const restoreMutation = useNodeRestoreMutation(clusterName, nodeName);
  const removeMutation = useRemoveNodeMutation(clusterName);
  const maintenanceMutation = useNodeMaintenanceMutation(clusterName, nodeName);

  // PLAN-026 remove SSE 监听
  const removeStream = useJobStream(removeJobId);
  const removeJobQuery = useJobQuery(removeStream.terminal != null ? removeJobId : null);
  useEffect(() => {
    if (removeStream.terminal === "succeeded" && removeJobQuery.data?.job?.status === "succeeded") {
      toast.success(t("admin.nodes.remove.done", { defaultValue: "节点 {{name}} 已移除", name: nodeName }));
      // SSE 流终态后清理 jobId state，触发 useJobStream(null) 关流
      // eslint-disable-next-line react/set-state-in-effect
      setRemoveJobId(null);
    }
    if (removeStream.terminal === "failed" || removeStream.terminal === "partial") {
      const lastFailed = removeStream.steps.slice().reverse().find((s) => s.status === "failed");
      toast.error(lastFailed?.detail ?? t("admin.nodes.remove.failed", "移除失败"));
    }
  }, [removeStream.terminal, removeStream.steps, removeJobQuery.data, nodeName, t]);

  const instances = data?.instances ?? [];
  const nodeInfo = data?.node as Record<string, unknown> | undefined;

  // OPS-024 D2 软维护切换：scheduler.instance = manual / all
  // QA-007 BUG: 提到组件顶层避免 JSX 内 IIFE（react/unsupported-syntax）
  const schedConfig = (
    (nodeInfo?.config as Record<string, string> | undefined)?.["scheduler.instance"]
    ?? "all"
  ).toLowerCase();
  const isMaint = schedConfig === "manual";

  return (
    <div className="border-t border-border bg-surface-2/40 p-4">
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
                value={nodeInfo.status ? formatNodeStatus(t, String(nodeInfo.status)) : "-"}
              />
              <InfoItem
                label={t("admin.nodes.message", "消息")}
                value={nodeInfo.message ? formatNodeMessage(t, String(nodeInfo.message)) : "-"}
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
            {isStatus(nodeStatus, "Online") ? (
              <Button
                variant="outline"
                size="sm"
                onClick={async () => {
                  const ok = await confirm({
                    title: t("deleteConfirm.evacuateTitle"),
                    message: t("deleteConfirm.evacuateMessage", { node: nodeName }),
                    destructive: true,
                  });
                  // WP-F：成功/失败 toast 由 useNodeEvacuateMutation 的 meta 全局兜底。
                  if (ok) evacuateMutation.mutate();
                }}
                disabled={evacuateMutation.isPending}
              >
                {evacuateMutation.isPending
                  ? t("admin.evacuating")
                  : t("admin.enterMaintenance")}
              </Button>
            ) : isStatus(nodeStatus, "Evacuated") ? (
              <Button
                variant="outline"
                size="sm"
                onClick={() => restoreMutation.mutate()}
                disabled={restoreMutation.isPending}
              >
                {restoreMutation.isPending
                  ? t("admin.nodes.restoring", "恢复中...")
                  : t("admin.nodes.restore", "恢复节点")}
              </Button>
            ) : null}

            {(evacuateMutation.isError || restoreMutation.isError) && (
              <span className="text-xs text-status-error">
                {(
                  (evacuateMutation.error ?? restoreMutation.error) as Error
                )?.message ?? "操作失败"}
              </span>
            )}

            <Button
              variant="outline"
              size="sm"
              disabled={maintenanceMutation.isPending}
              onClick={async () => {
                const ok = await confirm({
                  title: isMaint
                    ? t("admin.nodes.maintenance.exitTitle", "退出维护模式")
                    : t("admin.nodes.maintenance.enterTitle", "进入维护模式"),
                  message: isMaint
                    ? t("admin.nodes.maintenance.exitMessage", {
                        defaultValue: "恢复 {{name}} 的常规调度（新建 VM 可放置到本节点）。",
                        name: nodeName,
                      })
                    : t("admin.nodes.maintenance.enterMessage", {
                        defaultValue: "把 {{name}} 设为维护模式：新建 VM 不再放置到此节点，但已有 VM 保留。要 evacuate 现有 VM 请用上方按钮。",
                        name: nodeName,
                      }),
                });
                if (!ok) return;
                maintenanceMutation.mutate(!isMaint, {
                  onError: (err) => toast.error(formatError(err)),
                });
              }}
            >
              {maintenanceMutation.isPending
                ? t("common.processing", "处理中...")
                : isMaint
                  ? t("admin.nodes.maintenance.exit", "退出维护")
                  : t("admin.nodes.maintenance.enter", "维护模式")}
            </Button>

            {/* PLAN-026 移除节点：destructive，先 evacuate VM 再 leave Incus / Ceph */}
            {removeJobId == null && (
              <Button
                variant="destructive"
                size="sm"
                onClick={async () => {
                  const ok = await confirm({
                    title: t("admin.nodes.remove.title", "移除节点"),
                    message: t("admin.nodes.remove.message", {
                      defaultValue: "确认从集群移除 {{name}}？将先 evacuate 所有 VM、移除 Ceph OSD 后再退出。该操作不可回滚。",
                      name: nodeName,
                    }),
                    destructive: true,
                    typeToConfirm: nodeName,
                    typeToConfirmLabel: t("admin.nodes.remove.typeNodeName", {
                      defaultValue: "请输入节点名 {{name}} 以确认",
                      name: nodeName,
                    }),
                  });
                  if (!ok) return;
                  removeMutation.mutate(
                    { nodeName },
                    {
                      onSuccess: (res) => {
                        if (res.job_id) {
                          setRemoveJobId(res.job_id);
                          toast.info(
                            t("admin.nodes.remove.enqueued", {
                              defaultValue: "已入队 job #{{id}}",
                              id: res.job_id,
                            }),
                          );
                        }
                      },
                      onError: (err) => toast.error(formatError(err)),
                    },
                  );
                }}
                disabled={removeMutation.isPending}
              >
                {removeMutation.isPending
                  ? t("common.processing", "处理中...")
                  : t("admin.nodes.remove.button", "移除节点")}
              </Button>
            )}
          </div>

          {/* 移除进度 */}
          {removeJobId != null && (
            <div className="space-y-2">
              <h4 className="text-sm font-strong">
                {t("admin.nodes.remove.progressTitle", "移除节点进度")}
              </h4>
              <JobProgress steps={removeStream.steps} />
              {/* PLAN-038 / OPS-041 Phase C Tier 3：失败时挂诊断面板 */}
              {(removeStream.terminal === "failed" || removeStream.terminal === "partial") && (
                <AIDiagnosePanel jobID={removeJobId} />
              )}
            </div>
          )}

          {/* 实例列表 */}
          <div>
            <h4 className="text-sm font-strong mb-2">
              {t("admin.nodes.instances", "节点实例")} ({instances.length})
            </h4>
            {instances.length === 0 ? (
              <div className="text-xs text-muted-foreground">
                {t("admin.nodes.noInstances", "该节点暂无实例")}
              </div>
            ) : (
              <div className="border border-border rounded overflow-hidden">
                <table className="w-full text-xs [&_tbody>tr]:transition-colors [&_tbody>tr]:hover:bg-surface-1">
                  <thead className="bg-surface-1 border-b border-border">
                    <tr>
                      <th className="text-left px-3 py-1.5 text-label font-emphasis text-text-tertiary">
                        {t("admin.nodes.instanceName", "实例名")}
                      </th>
                      <th className="text-left px-3 py-1.5 text-label font-emphasis text-text-tertiary">
                        {t("admin.nodes.instanceType", "类型")}
                      </th>
                      <th className="text-left px-3 py-1.5 text-label font-emphasis text-text-tertiary">
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
                          <StatusPill
                            status={inst.status === "Running" ? "success" : "disabled"}
                          >
                            {formatVmStatus(t, inst.status)}
                          </StatusPill>
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
      <div className="font-emphasis">{value}</div>
    </div>
  );
}
