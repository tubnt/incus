import { useMutation, useQuery } from "@tanstack/react-query";
import { Camera, Plus, RotateCcw, Trash2 } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { Button } from "@/shared/components/ui/button";
import { useConfirm } from "@/shared/components/ui/confirm-dialog";
import { Input } from "@/shared/components/ui/input";
import { Skeleton } from "@/shared/components/ui/skeleton";
import { http } from "@/shared/lib/http";
import { queryClient } from "@/shared/lib/query-client";
import { formatDateTime } from "@/shared/lib/utils";
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
      // WP-F：快照创建成功补 toast。
      toast.success(t("snapshot.created", { defaultValue: "快照已创建" }));
    },
    onError: (err: Error) =>
      toast.error(`${t("snapshot.create")}: ${err.message}`),
  });

  const deleteMutation = useMutation({
    mutationFn: (snap: string) =>
      http.delete(snapshotPath(apiBase, vmName, snap), { cluster, project }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["snapshots", apiBase, vmName] });
      toast.success(t("snapshot.deleted", { defaultValue: "快照已删除" }));
    },
    onError: (err: Error) =>
      toast.error(`${t("common.delete")}: ${err.message}`),
  });

  const restoreMutation = useMutation({
    mutationFn: (snap: string) =>
      http.post(`${snapshotPath(apiBase, vmName, snap)}/restore`, { cluster, project }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["snapshots", apiBase, vmName] });
      toast.success(t("snapshot.restored", { defaultValue: "已从快照恢复" }));
    },
    onError: (err: Error) =>
      toast.error(`${t("snapshot.restore")}: ${err.message}`),
  });

  const snapshots = data?.snapshots ?? [];

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h4 className="text-sm font-emphasis flex items-center gap-1.5">
          <Camera size={14} aria-hidden="true" />
          {t("snapshot.title", { defaultValue: "快照" })} ({snapshots.length})
        </h4>
        <div className="flex gap-2 items-center">
          <Input
            type="text"
            value={newName}
            onChange={(e) => setNewName(e.target.value)}
            placeholder={t("snapshot.namePlaceholder", { defaultValue: "可选名称" })}
            className="w-48 h-8"
          />
          <Button
            size="sm"
            variant="primary"
            disabled={createMutation.isPending}
            onClick={() => createMutation.mutate(newName)}
          >
            <Plus size={12} aria-hidden="true" />
            {createMutation.isPending
              ? t("snapshot.creating", { defaultValue: "创建中..." })
              : t("snapshot.create", { defaultValue: "新建快照" })}
          </Button>
        </div>
      </div>

      {isLoading ? (
        <Skeleton className="h-24 w-full" />
      ) : snapshots.length === 0 ? (
        <div className="text-caption text-text-tertiary border border-dashed border-border rounded-md p-4 text-center">
          {t("snapshot.empty", { defaultValue: "暂无快照" })}
        </div>
      ) : (
        <div className="space-y-1.5">
          {snapshots.map((snap) => (
            <div
              key={snap.name}
              className="flex items-center justify-between px-3 py-2 rounded-md border border-border bg-surface-1 text-sm"
            >
              <div>
                <span className="font-mono">{snap.name}</span>
                <span className="text-caption text-text-tertiary ml-2">
                  {formatDateTime(snap.created_at)}
                </span>
              </div>
              <div className="flex gap-1.5">
                <Button
                  size="sm"
                  variant="ghost"
                  disabled={restoreMutation.isPending}
                  onClick={async () => {
                    const ok = await confirm({
                      title: t("snapshot.restoreTitle"),
                      message: t("snapshot.restoreMessage", { vm: vmName, name: snap.name }),
                      destructive: true,
                    });
                    if (ok) restoreMutation.mutate(snap.name);
                  }}
                >
                  <RotateCcw size={12} aria-hidden="true" />
                  {t("snapshot.restore")}
                </Button>
                <Button
                  size="sm"
                  variant="destructive"
                  disabled={deleteMutation.isPending}
                  aria-label={t("snapshot.deleteAriaLabel", { name: snap.name, defaultValue: `Delete snapshot ${snap.name}` })}
                  data-testid={`delete-snapshot-${snap.name}`}
                  onClick={async () => {
                    const ok = await confirm({
                      title: t("snapshot.deleteTitle"),
                      message: t("snapshot.deleteMessage", { name: snap.name }),
                      destructive: true,
                    });
                    if (ok) deleteMutation.mutate(snap.name);
                  }}
                >
                  <Trash2 size={12} aria-hidden="true" />
                  {t("common.delete")}
                </Button>
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
