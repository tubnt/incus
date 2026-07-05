import type {ClusterInfo, NodeInfo} from "@/features/clusters/api";
import { createFileRoute } from "@tanstack/react-router";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import {


  useAddClusterMutation,
  useClusterNodesQuery,
  useClustersQuery,
  useEvacuateNodeMutation,
  useRestoreNodeMutation
} from "@/features/clusters/api";
import {
  PageContent,
  PageHeader,
  PageShell,
} from "@/shared/components/page/page-shell";
import { Button } from "@/shared/components/ui/button";
import { Card } from "@/shared/components/ui/card";
import { useConfirm } from "@/shared/components/ui/confirm-dialog";
import { EmptyState } from "@/shared/components/ui/empty-state";
import { Input } from "@/shared/components/ui/input";
import {
  Sheet,
  SheetBody,
  SheetContent,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from "@/shared/components/ui/sheet";
import { StatusPill } from "@/shared/components/ui/status";
import { formatError } from "@/shared/lib/http";
import { formatNodeMessage, formatNodeStatus } from "@/shared/lib/status-i18n";
import { fmtBytes } from "@/shared/lib/utils";

export const Route = createFileRoute("/admin/clusters")({
  component: ClustersPage,
});

function ClustersPage() {
  const { t } = useTranslation();
  const { data, isLoading } = useClustersQuery();
  const clusters = data?.clusters ?? [];

  const [createOpen, setCreateOpen] = useState(false);

  return (
    <PageShell>
      <PageHeader
        title={t("nav.clusters")}
        actions={
          <Button variant="primary" onClick={() => setCreateOpen(true)}>
            {t("cluster.addCluster")}
          </Button>
        }
      />
      <PageContent>
        {isLoading ? (
          <div className="text-muted-foreground">{t("common.loading")}</div>
        ) : clusters.length === 0 ? (
          <EmptyState title={t("common.noData")} />
        ) : (
          <div className="space-y-6">
            {clusters.map((c) => (
              <ClusterCard key={c.name} cluster={c} />
            ))}
          </div>
        )}

        <Sheet open={createOpen} onOpenChange={(o) => { if (!o) setCreateOpen(false); }}>
          <SheetContent side="right" size="min(96vw, 32rem)">
            <AddClusterForm onDone={() => setCreateOpen(false)} />
          </SheetContent>
        </Sheet>
      </PageContent>
    </PageShell>
  );
}

function ClusterCard({ cluster }: { cluster: ClusterInfo }) {
  const { t } = useTranslation();
  const { data } = useClusterNodesQuery(cluster.name, 30_000);
  const nodes = data?.nodes ?? [];

  return (
    <Card className="overflow-hidden">
      <div className="p-4 flex items-center justify-between border-b border-border">
        <div>
          <h3 className="font-strong text-lg">{cluster.display_name || cluster.name}</h3>
          <div className="text-sm text-muted-foreground mt-1">
            {cluster.api_url} · {nodes.length} nodes
          </div>
        </div>
        <StatusPill status="success">{cluster.status}</StatusPill>
      </div>

      {nodes.length > 0 && (
        <div className="overflow-x-auto">
          <table className="w-full text-sm [&_tbody>tr]:transition-colors [&_tbody>tr]:hover:bg-surface-1">
            <thead className="bg-surface-1 border-b border-border">
              <tr>
                <th className="text-left px-4 py-2 text-label font-emphasis text-text-tertiary">{t("admin.nodes.colName", { defaultValue: "节点" })}</th>
                <th className="text-left px-4 py-2 text-label font-emphasis text-text-tertiary">{t("admin.nodes.colStatus", { defaultValue: "状态" })}</th>
                <th className="text-left px-4 py-2 text-label font-emphasis text-text-tertiary">{t("admin.nodes.colCpu", { defaultValue: "CPU" })}</th>
                <th className="text-left px-4 py-2 text-label font-emphasis text-text-tertiary">{t("admin.nodes.colMemory", { defaultValue: "内存" })}</th>
                <th className="text-left px-4 py-2 text-label font-emphasis text-text-tertiary">{t("admin.nodes.colFreePct", { defaultValue: "空闲" })}</th>
                <th className="text-right px-4 py-2 text-label font-emphasis text-text-tertiary">{t("vm.actions", { defaultValue: "操作" })}</th>
              </tr>
            </thead>
            <tbody>
              {nodes.map((n) => (
                <NodeRow key={n.server_name} node={n} clusterName={cluster.name} />
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Card>
  );
}

function NodeRow({ node: n, clusterName }: { node: NodeInfo; clusterName: string }) {
  const { t } = useTranslation();
  const confirm = useConfirm();
  const evacuateMutation = useEvacuateNodeMutation(clusterName);
  const restoreMutation = useRestoreNodeMutation(clusterName);

  const isOnline = n.status === "Online";
  const isEvacuated = n.status === "Evacuated" || n.message?.includes("evacuated");
  const acting = evacuateMutation.isPending || restoreMutation.isPending;

  const statusKind = isOnline ? "success" : isEvacuated ? "warning" : "error";

  return (
    <tr className="group/row border-t border-border">
      <td className="px-4 py-2 font-mono">{n.server_name}</td>
      <td className="px-4 py-2">
        <StatusPill status={statusKind}>{formatNodeStatus(t, n.status)}</StatusPill>
        {n.message && n.message !== "Fully operational" && (
          <span className="text-xs text-muted-foreground ml-2">{formatNodeMessage(t, n.message)}</span>
        )}
      </td>
      <td className="px-4 py-2">{n.cpu_total} cores</td>
      <td className="px-4 py-2">{fmtBytes(n.mem_used)} / {fmtBytes(n.mem_total)}</td>
      <td className="px-4 py-2">
        <div className="flex items-center gap-2">
          <div className="w-16 h-2 bg-muted rounded-full overflow-hidden">
            <div className="h-full bg-status-success rounded-full" style={{ width: `${(n.free_ratio * 100).toFixed(0)}%` }} />
          </div>
          <span className="text-xs text-muted-foreground">{(n.free_ratio * 100).toFixed(0)}%</span>
        </div>
      </td>
      <td className="px-4 py-2 text-right">
        <div className="flex gap-1 justify-end opacity-0 group-hover/row:opacity-100 group-focus-within/row:opacity-100 transition-opacity">
          {isOnline && (
            <Button
              variant="subtle"
              size="sm"
              onClick={async () => {
                const ok = await confirm({
                  title: t("deleteConfirm.evacuateTitle"),
                  message: t("deleteConfirm.evacuateMessage", { node: n.server_name }),
                  destructive: true,
                });
                // WP-F：成功/失败 toast 由 useEvacuateNodeMutation 的 meta 全局兜底。
                if (ok) evacuateMutation.mutate(n.server_name);
              }}
              disabled={acting}
            >
              {t("cluster.evacuate", { defaultValue: "Evacuate" })}
            </Button>
          )}
          {isEvacuated && (
            <Button
              variant="subtle"
              size="sm"
              onClick={() => restoreMutation.mutate(n.server_name)}
              disabled={acting}
            >
              {t("cluster.restore", { defaultValue: "Restore" })}
            </Button>
          )}
        </div>
      </td>
    </tr>
  );
}

const NAME_RE = /^[a-z][a-z0-9-]{1,31}$/;
// QA-007 BUG-05: `[^\s/]+` already consumes the optional ":port" segment,
// so the previous `(:\d{1,5})?` group was both unused and redundant. Path
// suffix is non-capturing — we only test, never extract the match.
const API_URL_RE = /^https?:\/\/[^\s/]+(?:\/.*)?$/;

function AddClusterForm({ onDone }: { onDone: () => void }) {
  const { t } = useTranslation();
  const [form, setForm] = useState({ name: "", display_name: "", api_url: "", cert_file: "", key_file: "" });
  const [touched, setTouched] = useState<Record<string, boolean>>({});

  const errors = useMemo(() => {
    const e: Record<string, string> = {};
    if (!form.name.trim()) e.name = t("cluster.requiredName");
    else if (!NAME_RE.test(form.name.trim())) e.name = t("cluster.invalidName");
    if (!form.api_url.trim()) e.api_url = t("cluster.requiredApiUrl");
    else if (!API_URL_RE.test(form.api_url.trim())) e.api_url = t("cluster.invalidApiUrl");
    return e;
  }, [form.name, form.api_url, t]);

  const isValid = Object.keys(errors).length === 0;

  const mutation = useAddClusterMutation();

  const set = (k: string, v: string) => setForm({ ...form, [k]: v });
  const markTouched = (k: string) => setTouched((p) => ({ ...p, [k]: true }));
  const submit = () => {
    setTouched({ name: true, api_url: true });
    if (!isValid) return;
    mutation.mutate(form, {
      onSuccess: onDone,
      onError: (err) => toast.error(formatError(err)),
    });
  };

  const fieldErr = (k: string) => (touched[k] ? errors[k] : undefined);
  const errCls = "mt-1 text-xs text-status-error";

  return (
    <>
      <SheetHeader>
        <SheetTitle>{t("cluster.addClusterTitle")}</SheetTitle>
      </SheetHeader>
      <SheetBody>
        <div className="grid grid-cols-2 gap-3">
          <div className="flex flex-col">
            <Input
              placeholder={t("cluster.namePlaceholder")}
              value={form.name}
              onChange={(e) => set("name", e.target.value)}
              onBlur={() => markTouched("name")}
              className={fieldErr("name") ? "border-status-error" : undefined}
            />
            {fieldErr("name") && <div className={errCls}>{fieldErr("name")}</div>}
          </div>
          <div className="flex flex-col">
            <Input
              placeholder={t("cluster.fieldDisplayName")}
              value={form.display_name}
              onChange={(e) => set("display_name", e.target.value)}
            />
          </div>
          <div className="col-span-2 flex flex-col">
            <Input
              placeholder={t("cluster.urlPlaceholder")}
              value={form.api_url}
              onChange={(e) => set("api_url", e.target.value)}
              onBlur={() => markTouched("api_url")}
              className={fieldErr("api_url") ? "border-status-error" : undefined}
            />
            {fieldErr("api_url") && <div className={errCls}>{fieldErr("api_url")}</div>}
          </div>
          <Input
            placeholder={t("cluster.fieldCert")}
            value={form.cert_file}
            onChange={(e) => set("cert_file", e.target.value)}
          />
          <Input
            placeholder={t("cluster.fieldKey")}
            value={form.key_file}
            onChange={(e) => set("key_file", e.target.value)}
          />
        </div>
      </SheetBody>
      <SheetFooter>
        <Button variant="ghost" onClick={onDone}>
          {t("common.cancel")}
        </Button>
        <Button
          variant="primary"
          onClick={submit}
          disabled={mutation.isPending || !isValid}
        >
          {mutation.isPending ? t("cluster.connecting") : t("cluster.addCluster")}
        </Button>
      </SheetFooter>
    </>
  );
}

