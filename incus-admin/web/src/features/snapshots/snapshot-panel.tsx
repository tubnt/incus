import { useMutation, useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { http } from "@/shared/lib/http";
import { queryClient } from "@/shared/lib/query-client";
import { useConfirm } from "@/shared/components/ui/confirm-dialog";
import { snapshotPath } from "./snapshot-utils";

interface SnapshotInfo {
  name: string;
  created_at: string;
  stateful: boolean;
}

interface SnapshotPanelProps {
  vmName: string;
  cluster: string;
  project: string;
  apiBase?: "/admin" | "/portal";
}

export function SnapshotPanel({ vmName, cluster, project, apiBase = "/admin" }: SnapshotPanelProps) {
  const { t } = useTranslation();
  const confirm = useConfirm();
  const [newName, setNewName] = useState("");

  const { data, isLoading } = useQuery({
    queryKey: ["snapshots", apiBase, vmName, cluster, project],
    queryFn: () =>
      http.get<{ snapshots: SnapshotInfo[] }>(snapshotPath(apiBase, vmName), {
        cluster,
        project,
      }),
  });

  const createMutation = useMutation({
    mutationFn: (name: string) =>
      http.post(snapshotPath(apiBase, vmName), { cluster, project, name: name || undefined }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["snapshots", apiBase, vmName] });
      setNewName("");
    },
  });

  const deleteMutation = useMutation({
    mutationFn: (snap: string) =>
      http.delete(snapshotPath(apiBase, vmName, snap), { cluster, project }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["snapshots", apiBase, vmName] }),
  });

  const restoreMutation = useMutation({
    mutationFn: (snap: string) =>
      http.post(`${snapshotPath(apiBase, vmName, snap)}/restore`, { cluster, project }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["snapshots", apiBase, vmName] }),
  });

  const snapshots = data?.snapshots ?? [];

  return (
    <div className="p-4 bg-card/50 border-t border-border">
      <div className="flex items-center justify-between mb-3">
        <h4 className="font-medium text-sm">Snapshots ({snapshots.length})</h4>
        <div className="flex gap-2">
          <input
            type="text"
            value={newName}
            onChange={(e) => setNewName(e.target.value)}
            placeholder="snapshot name (optional)"
            className="px-2 py-1 text-xs border border-border rounded bg-card w-48"
          />
          <button
            onClick={() => createMutation.mutate(newName)}
            disabled={createMutation.isPending}
            className="px-3 py-1 text-xs bg-primary text-primary-foreground rounded disabled:opacity-50"
          >
            {createMutation.isPending ? "Creating..." : "+ Snapshot"}
          </button>
        </div>
      </div>

      {isLoading ? (
        <div className="text-xs text-muted-foreground">Loading...</div>
      ) : snapshots.length === 0 ? (
        <div className="text-xs text-muted-foreground">No snapshots yet.</div>
      ) : (
        <div className="space-y-1">
          {snapshots.map((snap) => (
            <div key={snap.name} className="flex items-center justify-between px-3 py-2 rounded border border-border text-xs">
              <div>
                <span className="font-mono">{snap.name}</span>
                <span className="text-muted-foreground ml-2">
                  {new Date(snap.created_at).toLocaleString()}
                </span>
              </div>
              <div className="flex gap-1">
                <button
                  onClick={async () => {
                    const ok = await confirm({
                      title: t("snapshot.restoreTitle"),
                      message: t("snapshot.restoreMessage", { vm: vmName, name: snap.name }),
                      destructive: true,
                    });
                    if (ok) restoreMutation.mutate(snap.name);
                  }}
                  disabled={restoreMutation.isPending}
                  className="px-2 py-1 rounded bg-primary/20 text-primary hover:bg-primary/30 disabled:opacity-50"
                >
                  {t("snapshot.restore")}
                </button>
                <button
                  onClick={async () => {
                    const ok = await confirm({
                      title: t("snapshot.deleteTitle"),
                      message: t("snapshot.deleteMessage", { name: snap.name }),
                      destructive: true,
                    });
                    if (ok) deleteMutation.mutate(snap.name);
                  }}
                  disabled={deleteMutation.isPending}
                  className="px-2 py-1 rounded bg-destructive/20 text-destructive hover:bg-destructive/30 disabled:opacity-50"
                >
                  {t("common.delete")}
                </button>
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
