import { createFileRoute } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { ClusterPicker } from "@/features/clusters/cluster-picker";
import { useAddIPPoolMutation, useIPPoolsQuery } from "@/features/ip-pools/api";

export const Route = createFileRoute("/admin/ip-pools")({
  component: IPPoolsPage,
});

function IPPoolsPage() {
  const { t } = useTranslation();
  const [showAdd, setShowAdd] = useState(false);
  const { data, isLoading } = useIPPoolsQuery();
  const pools = data?.pools ?? [];

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold">{t("admin.ipPools.title", { defaultValue: "IP Pools" })}</h1>
        <button onClick={() => setShowAdd(!showAdd)}
          className="px-4 py-2 bg-primary text-primary-foreground rounded-md text-sm font-medium hover:opacity-90">
          {showAdd ? t("common.cancel", { defaultValue: "Cancel" }) : `+ ${t("admin.ipPools.add", { defaultValue: "Add Pool" })}`}
        </button>
      </div>

      {showAdd && <AddPoolForm onDone={() => setShowAdd(false)} />}

      {isLoading ? (
        <div className="text-muted-foreground">{t("common.loading")}</div>
      ) : pools.length === 0 ? (
        <div className="border border-border rounded-lg p-8 text-center text-muted-foreground">
          {t("admin.ipPools.empty", { defaultValue: "No IP pools configured." })}
        </div>
      ) : (
        <div className="space-y-4">
          {pools.map((pool, i) => (
            <div key={i} className="border border-border rounded-lg bg-card p-4">
              <div className="flex items-center justify-between mb-3">
                <div>
                  <h3 className="font-semibold">{pool.cluster_name}</h3>
                  <div className="text-sm text-muted-foreground">
                    {pool.cidr} · Gateway {pool.gateway} · VLAN {pool.vlan}
                  </div>
                </div>
                <div className="text-right">
                  <div className="text-2xl font-bold">{pool.available}</div>
                  <div className="text-xs text-muted-foreground">{t("admin.ipPools.available", { defaultValue: "available" })}</div>
                </div>
              </div>
              <div className="flex items-center gap-4 mb-2">
                <div className="flex-1 h-3 bg-muted rounded-full overflow-hidden">
                  <div className="h-full bg-primary rounded-full transition-all"
                    style={{ width: `${pool.total > 0 ? (pool.used / pool.total) * 100 : 0}%` }} />
                </div>
                <span className="text-sm text-muted-foreground whitespace-nowrap">
                  {pool.used} / {pool.total} {t("admin.ipPools.used", { defaultValue: "used" })}
                </span>
              </div>
              <div className="text-xs text-muted-foreground">{t("admin.ipPools.range", { defaultValue: "Range" })}: {pool.range}</div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function AddPoolForm({ onDone }: { onDone: () => void }) {
  const { t } = useTranslation();
  const [form, setForm] = useState({ cluster: "", cidr: "", gateway: "", range: "", vlan: 0 });

  const mutation = useAddIPPoolMutation();

  const set = (k: string, v: string | number) => setForm({ ...form, [k]: v });

  return (
    <div className="border border-border rounded-lg bg-card p-4 mb-6">
      <h3 className="font-semibold mb-3">{t("admin.ipPools.addTitle", { defaultValue: "Add IP Pool" })}</h3>
      <div className="grid grid-cols-2 gap-3 mb-4">
        <ClusterPicker
          value={form.cluster}
          onChange={(v) => set("cluster", v)}
          allowEmpty
          placeholder={t("admin.ipPools.selectCluster", { defaultValue: "Select cluster" })}
        />
        <input type="number" placeholder={t("admin.ipPools.vlanPlaceholder", { defaultValue: "VLAN ID" })} value={form.vlan || ""} onChange={(e) => set("vlan", +e.target.value)}
          className="px-3 py-2 rounded border border-border bg-card text-sm" />
        <input placeholder={t("admin.ipPools.cidrPlaceholder", { defaultValue: "CIDR (e.g. 202.151.179.224/27)" })} value={form.cidr} onChange={(e) => set("cidr", e.target.value)}
          className="px-3 py-2 rounded border border-border bg-card text-sm" />
        <input placeholder={t("admin.ipPools.gatewayPlaceholder", { defaultValue: "Gateway (e.g. 202.151.179.225)" })} value={form.gateway} onChange={(e) => set("gateway", e.target.value)}
          className="px-3 py-2 rounded border border-border bg-card text-sm" />
        <input placeholder={t("admin.ipPools.rangePlaceholder", { defaultValue: "Range (e.g. 202.151.179.230-202.151.179.254)" })} value={form.range} onChange={(e) => set("range", e.target.value)}
          className="col-span-2 px-3 py-2 rounded border border-border bg-card text-sm" />
      </div>
      {mutation.isError && <div className="text-destructive text-sm mb-2">{(mutation.error as Error).message}</div>}
      <button onClick={() => mutation.mutate(form, { onSuccess: onDone })} disabled={mutation.isPending || !form.cluster || !form.cidr}
        className="px-4 py-2 bg-primary text-primary-foreground rounded text-sm font-medium disabled:opacity-50">
        {mutation.isPending ? t("admin.ipPools.adding", { defaultValue: "Adding..." }) : t("admin.ipPools.add", { defaultValue: "Add Pool" })}
      </button>
    </div>
  );
}
