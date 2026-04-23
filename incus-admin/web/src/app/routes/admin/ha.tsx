import { Tabs } from "@base-ui-components/react/tabs";
import { Dialog } from "@base-ui-components/react/dialog";
import { createFileRoute } from "@tanstack/react-router";
import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useClustersQuery } from "@/features/clusters/api";
import { ClusterPicker } from "@/features/clusters/cluster-picker";
import {
  type HANodeInfo,
  useHAStatusQuery,
  useHAEvacuateMutation,
} from "@/features/nodes/api";
import {
  type HealingEvent,
  type HealingListFilter,
  type HealingStatus,
  type HealingTrigger,
  useHealingEventDetailQuery,
  useHealingEventsQuery,
} from "@/features/healing/api";
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

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold">HA Failover</h1>
        {clusters.length > 1 && (
          <ClusterPicker value={clusterName} onChange={setClusterName} />
        )}
      </div>

      <Tabs.Root defaultValue="status">
        <Tabs.List className="flex gap-1 border-b border-border mb-4">
          <Tabs.Tab
            value="status"
            className="px-4 py-2 text-sm font-medium text-muted-foreground hover:text-foreground aria-selected:text-foreground aria-selected:border-b-2 aria-selected:border-primary -mb-px transition-colors"
          >
            {t("ha.tabStatus")}
          </Tabs.Tab>
          <Tabs.Tab
            value="history"
            className="px-4 py-2 text-sm font-medium text-muted-foreground hover:text-foreground aria-selected:text-foreground aria-selected:border-b-2 aria-selected:border-primary -mb-px transition-colors"
          >
            {t("ha.tabHistory")}
          </Tabs.Tab>
        </Tabs.List>

        <Tabs.Panel value="status">
          <StatusPanel clusterName={clusterName} />
        </Tabs.Panel>
        <Tabs.Panel value="history">
          <HistoryPanel clusterName={clusterName} clusters={clusters.map((c) => c.name)} />
        </Tabs.Panel>
      </Tabs.Root>
    </div>
  );
}

function StatusPanel({ clusterName }: { clusterName: string }) {
  const { t } = useTranslation();
  const { data: ha, isLoading } = useHAStatusQuery(clusterName);
  const evacuateMutation = useHAEvacuateMutation(clusterName);

  if (isLoading) {
    return <div className="text-muted-foreground">{t("common.loading")}</div>;
  }
  if (!ha) {
    return (
      <div className="border border-border rounded-lg p-6 text-center text-muted-foreground">
        No cluster configured.
      </div>
    );
  }

  return (
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
      <div className="text-xs text-muted-foreground">
        healing_threshold: {ha.healing_threshold}s
      </div>

      <div className="border border-border rounded-lg overflow-x-auto">
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

function HistoryPanel({
  clusterName,
  clusters,
}: {
  clusterName: string;
  clusters: string[];
}) {
  const { t } = useTranslation();
  const [trigger, setTrigger] = useState<HealingTrigger | "">("");
  const [status, setStatus] = useState<HealingStatus | "">("");
  const [node, setNode] = useState<string>("");
  const [fromDate, setFromDate] = useState<string>("");
  const [toDate, setToDate] = useState<string>("");
  const [page, setPage] = useState(0);
  const [openId, setOpenId] = useState<number | null>(null);

  const limit = 25;
  const filter: HealingListFilter = useMemo(
    () => ({
      cluster: clusterName || undefined,
      node: node || undefined,
      trigger: trigger || undefined,
      status: status || undefined,
      from: fromDate || undefined,
      to: toDate || undefined,
      limit,
      offset: page * limit,
    }),
    [clusterName, node, trigger, status, fromDate, toDate, page],
  );

  const { data, isLoading } = useHealingEventsQuery(filter);
  const events = data?.items ?? [];
  const total = data?.total ?? 0;

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap gap-3 items-end">
        <FilterSelect
          label={t("ha.filterTrigger")}
          value={trigger}
          onChange={(v) => { setTrigger(v as HealingTrigger | ""); setPage(0); }}
          options={[
            { value: "", label: t("ha.filterAll") },
            { value: "manual", label: t("ha.triggerManual") },
            { value: "auto", label: t("ha.triggerAuto") },
            { value: "chaos", label: t("ha.triggerChaos") },
          ]}
        />
        <FilterSelect
          label={t("ha.filterStatus")}
          value={status}
          onChange={(v) => { setStatus(v as HealingStatus | ""); setPage(0); }}
          options={[
            { value: "", label: t("ha.filterAll") },
            { value: "in_progress", label: t("ha.statusInProgress") },
            { value: "completed", label: t("ha.statusCompleted") },
            { value: "failed", label: t("ha.statusFailed") },
            { value: "partial", label: t("ha.statusPartial") },
          ]}
        />
        <label className="flex flex-col gap-1 text-xs text-muted-foreground">
          {t("ha.filterNode")}
          <input
            value={node}
            onChange={(e) => { setNode(e.target.value); setPage(0); }}
            placeholder="node1"
            className="px-2 py-1 text-sm border border-border rounded bg-background w-32"
          />
        </label>
        <label className="flex flex-col gap-1 text-xs text-muted-foreground">
          {t("ha.filterFrom")}
          <input
            type="date"
            value={fromDate}
            onChange={(e) => { setFromDate(e.target.value); setPage(0); }}
            className="px-2 py-1 text-sm border border-border rounded bg-background"
          />
        </label>
        <label className="flex flex-col gap-1 text-xs text-muted-foreground">
          {t("ha.filterTo")}
          <input
            type="date"
            value={toDate}
            onChange={(e) => { setToDate(e.target.value); setPage(0); }}
            className="px-2 py-1 text-sm border border-border rounded bg-background"
          />
        </label>
        <span className="text-xs text-muted-foreground ml-auto">
          {t("ha.countTotal", { n: total })} · {clusters.length > 0 ? clusters.join(", ") : "—"}
        </span>
      </div>

      <div className="border border-border rounded-lg overflow-x-auto">
        <table className="w-full text-sm">
          <thead className="bg-muted/30">
            <tr>
              <th className="text-left px-3 py-2 font-medium">{t("ha.colTime")}</th>
              <th className="text-left px-3 py-2 font-medium">{t("ha.colCluster")}</th>
              <th className="text-left px-3 py-2 font-medium">{t("ha.colNode")}</th>
              <th className="text-left px-3 py-2 font-medium">{t("ha.colTrigger")}</th>
              <th className="text-left px-3 py-2 font-medium">{t("ha.colActor")}</th>
              <th className="text-right px-3 py-2 font-medium">{t("ha.colVMCount")}</th>
              <th className="text-left px-3 py-2 font-medium">{t("ha.colStatus")}</th>
              <th className="text-right px-3 py-2 font-medium">{t("ha.colDuration")}</th>
              <th className="text-right px-3 py-2 font-medium"></th>
            </tr>
          </thead>
          <tbody>
            {isLoading && (
              <tr>
                <td colSpan={9} className="px-3 py-6 text-center text-muted-foreground">
                  {t("common.loading")}
                </td>
              </tr>
            )}
            {!isLoading && events.length === 0 && (
              <tr>
                <td colSpan={9} className="px-3 py-6 text-center text-muted-foreground">
                  {t("ha.empty")}
                </td>
              </tr>
            )}
            {events.map((ev) => (
              <EventRow key={ev.id} event={ev} onOpen={() => setOpenId(ev.id)} />
            ))}
          </tbody>
        </table>
      </div>

      {total > limit && (
        <div className="flex items-center justify-end gap-2 text-sm">
          <button
            onClick={() => setPage((p) => Math.max(0, p - 1))}
            disabled={page === 0}
            className="px-3 py-1 border border-border rounded disabled:opacity-50"
          >
            {t("common.prev")}
          </button>
          <span className="text-xs text-muted-foreground">
            {page * limit + 1} – {Math.min((page + 1) * limit, total)} / {total}
          </span>
          <button
            onClick={() => setPage((p) => p + 1)}
            disabled={(page + 1) * limit >= total}
            className="px-3 py-1 border border-border rounded disabled:opacity-50"
          >
            {t("common.next")}
          </button>
        </div>
      )}

      <EventDetailDialog id={openId} onClose={() => setOpenId(null)} />
    </div>
  );
}

function FilterSelect({
  label,
  value,
  onChange,
  options,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  options: { value: string; label: string }[];
}) {
  return (
    <label className="flex flex-col gap-1 text-xs text-muted-foreground">
      {label}
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="px-2 py-1 text-sm border border-border rounded bg-background"
      >
        {options.map((o) => (
          <option key={o.value} value={o.value}>
            {o.label}
          </option>
        ))}
      </select>
    </label>
  );
}

const statusBadge: Record<HealingStatus, string> = {
  in_progress: "bg-primary/10 text-primary",
  completed: "bg-success/20 text-success",
  failed: "bg-destructive/20 text-destructive",
  partial: "bg-warning/20 text-warning",
};

const triggerBadge: Record<HealingTrigger, string> = {
  manual: "bg-muted text-foreground",
  auto: "bg-primary/10 text-primary",
  chaos: "bg-warning/20 text-warning",
};

function EventRow({
  event,
  onOpen,
}: {
  event: HealingEvent;
  onOpen: () => void;
}) {
  const { t } = useTranslation();
  const vmCount = event.evacuated_vms?.length ?? 0;

  return (
    <tr className="border-t border-border hover:bg-muted/30">
      <td className="px-3 py-2 font-mono text-xs whitespace-nowrap">
        {new Date(event.started_at).toLocaleString()}
      </td>
      <td className="px-3 py-2">{event.cluster_name || `#${event.cluster_id}`}</td>
      <td className="px-3 py-2 font-mono">{event.node_name}</td>
      <td className="px-3 py-2">
        <span className={`px-2 py-0.5 rounded text-xs font-medium ${triggerBadge[event.trigger]}`}>
          {t(`ha.trigger${capitalize(event.trigger)}`)}
        </span>
      </td>
      <td className="px-3 py-2 text-xs">
        {event.actor_id ? `#${event.actor_id}` : "—"}
      </td>
      <td className="px-3 py-2 text-right tabular-nums">{vmCount}</td>
      <td className="px-3 py-2">
        <span className={`px-2 py-0.5 rounded text-xs font-medium ${statusBadge[event.status]}`}>
          {t(`ha.status${capitalize(event.status.replace(/_/g, ""))}`)}
        </span>
      </td>
      <td className="px-3 py-2 text-right tabular-nums">
        {event.duration_seconds != null ? `${event.duration_seconds}s` : "—"}
      </td>
      <td className="px-3 py-2 text-right">
        <button
          onClick={onOpen}
          className="text-xs text-primary hover:underline"
        >
          {t("common.details")}
        </button>
      </td>
    </tr>
  );
}

function capitalize(s: string): string {
  if (!s) return s;
  return s.charAt(0).toUpperCase() + s.slice(1);
}

function EventDetailDialog({
  id,
  onClose,
}: {
  id: number | null;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const open = id != null;
  const { data: event, isLoading } = useHealingEventDetailQuery(id);

  return (
    <Dialog.Root open={open} onOpenChange={(next) => { if (!next) onClose(); }}>
      <Dialog.Portal>
        <Dialog.Backdrop className="fixed inset-0 bg-black/40 data-[starting-style]:opacity-0 data-[ending-style]:opacity-0 transition-opacity" />
        <Dialog.Popup className="fixed top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2 w-full max-w-2xl max-h-[85vh] overflow-y-auto bg-card border border-border rounded-lg shadow-xl p-6 data-[starting-style]:opacity-0 data-[ending-style]:opacity-0">
          <Dialog.Title className="text-lg font-bold mb-4">
            {t("ha.detailTitle", { id })}
          </Dialog.Title>
          {isLoading && <div className="text-muted-foreground">{t("common.loading")}</div>}
          {event && (
            <div className="space-y-4">
              <div className="grid grid-cols-2 gap-4 text-sm">
                <DetailField label={t("ha.colCluster")} value={event.cluster_name || `#${event.cluster_id}`} />
                <DetailField label={t("ha.colNode")} value={event.node_name} mono />
                <DetailField label={t("ha.colTrigger")} value={t(`ha.trigger${capitalize(event.trigger)}`)} />
                <DetailField label={t("ha.colStatus")} value={t(`ha.status${capitalize(event.status.replace(/_/g, ""))}`)} />
                <DetailField
                  label={t("ha.colTime")}
                  value={new Date(event.started_at).toLocaleString()}
                />
                <DetailField
                  label={t("ha.colDuration")}
                  value={event.duration_seconds != null ? `${event.duration_seconds}s` : "—"}
                />
                <DetailField
                  label={t("ha.colActor")}
                  value={event.actor_id ? `#${event.actor_id}` : "—"}
                />
              </div>
              {event.error && (
                <div className="border border-destructive/40 bg-destructive/5 rounded p-3 text-sm">
                  <div className="font-medium text-destructive mb-1">{t("ha.errorHeading")}</div>
                  <code className="text-xs break-all">{event.error}</code>
                </div>
              )}
              <div>
                <div className="text-sm font-medium mb-2">
                  {t("ha.evacuatedVMsHeading")} ({event.evacuated_vms?.length ?? 0})
                </div>
                {(event.evacuated_vms?.length ?? 0) === 0 ? (
                  <div className="text-xs text-muted-foreground">{t("ha.noVMsMoved")}</div>
                ) : (
                  <div className="border border-border rounded overflow-x-auto">
                    <table className="w-full text-xs">
                      <thead className="bg-muted/30">
                        <tr>
                          <th className="text-left px-3 py-1.5 font-medium">ID</th>
                          <th className="text-left px-3 py-1.5 font-medium">{t("ha.vmName")}</th>
                          <th className="text-left px-3 py-1.5 font-medium">{t("ha.vmFrom")}</th>
                          <th className="text-left px-3 py-1.5 font-medium">{t("ha.vmTo")}</th>
                        </tr>
                      </thead>
                      <tbody>
                        {event.evacuated_vms!.map((v) => (
                          <tr key={v.vm_id} className="border-t border-border">
                            <td className="px-3 py-1.5 font-mono">{v.vm_id}</td>
                            <td className="px-3 py-1.5 font-mono">{v.name}</td>
                            <td className="px-3 py-1.5 font-mono">{v.from_node}</td>
                            <td className="px-3 py-1.5 font-mono">{v.to_node}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                )}
              </div>
            </div>
          )}
          <div className="flex justify-end mt-6">
            <Dialog.Close className="px-4 py-2 text-sm border border-border rounded hover:bg-muted">
              {t("common.close")}
            </Dialog.Close>
          </div>
        </Dialog.Popup>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

function DetailField({
  label,
  value,
  mono,
}: {
  label: string;
  value: string;
  mono?: boolean;
}) {
  return (
    <div>
      <div className="text-xs text-muted-foreground">{label}</div>
      <div className={`text-sm mt-0.5 ${mono ? "font-mono" : ""}`}>{value}</div>
    </div>
  );
}
