import type {MyTrashedVM, VMService} from "@/features/vms/api";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import {
  Camera, ExternalLink, MoreHorizontal, Pause, Play, Plus, RefreshCw,
  RotateCcw, Server, Square, Terminal as TerminalIcon, Trash2,
} from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { VMMetricsPanel } from "@/features/monitoring/vm-metrics-panel";
import { SnapshotPanel } from "@/features/snapshots/snapshot-panel";
import {
  useMyTrashedQuery,
  useMyVMsQuery,
  useRestoreServiceMutation,
  useTrashServiceMutation,
  useVMActionMutation,
  vmKeys,
} from "@/features/vms/api";
import { defaultUserForImage } from "@/features/vms/default-user";
import { useCommandActions } from "@/shared/components/command-palette/use-command-actions";
import {
  PageContent,
  PageHeader,
  PageShell,
} from "@/shared/components/page/page-shell";
import { BatchToolbar } from "@/shared/components/ui/batch-toolbar";
import { Button, buttonVariants } from "@/shared/components/ui/button";
import { Card, CardContent } from "@/shared/components/ui/card";
import { Checkbox } from "@/shared/components/ui/checkbox";
import { useConfirm } from "@/shared/components/ui/confirm-dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/shared/components/ui/dropdown-menu";
import { EmptyState } from "@/shared/components/ui/empty-state";
import {
  Sheet,
  SheetBody,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/shared/components/ui/sheet";
import { CardSkeleton } from "@/shared/components/ui/skeleton";
import { StatusDot, vmStatusToKind } from "@/shared/components/ui/status";
import { formatError, http  } from "@/shared/lib/http";
import { queryClient } from "@/shared/lib/query-client";
import { cn } from "@/shared/lib/utils";

export const Route = createFileRoute("/vms")({
  component: MyVMs,
});

type SheetKind = "snapshots" | "metrics" | null;

// 批量动作 ≥ 3 台时强制 typed-confirm（NN/g 严重度分级；trash-undo 是兜底但
// 大批量误删的代价仍很高，让用户停一下）。
const BATCH_TYPED_CONFIRM_THRESHOLD = 3;

function MyVMs() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { data, isLoading } = useMyVMsQuery();
  const { data: trashedData } = useMyTrashedQuery();
  const services = useMemo(() => data?.vms ?? [], [data?.vms]);
  const trashedList = trashedData?.vms ?? [];

  const [sheetKind, setSheetKind] = useState<SheetKind>(null);
  const [sheetVM, setSheetVM] = useState<VMService | null>(null);
  // 高亮锁定到 VM ID，避免 useMyVMsQuery 后台 refetch 重排数组时
  // 数字索引指到不同 VM 的 race（用户按 Enter 跳错详情）
  const [hlVmId, setHlVmId] = useState<number | null>(null);
  const [selected, setSelected] = useState<Set<number>>(() => new Set());
  const listRef = useRef<HTMLDivElement>(null);

  const closeSheet = () => setSheetKind(null);

  const trashMutation = useTrashServiceMutation();
  // WP-F：回收站撤销的成功/失败 toast 由 useRestoreServiceMutation 的 meta
  // （successToast + globalErrorToast）在全局 MutationCache 统一兜底，批量 undo
  // 多次成功经稳定 toast id 折叠为一条，无需在每个调用点重复接 onSuccess/onError。
  const restoreMutation = useRestoreServiceMutation();
  const confirm = useConfirm();

  const toggleOne = (id: number) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const toggleAll = () => {
    if (selected.size === services.length && services.length > 0) {
      setSelected(new Set());
    } else {
      setSelected(new Set(services.map((v) => v.id)));
    }
  };

  // Linear 风键盘导航：j/k 上下、Enter 进详情、n 新建。表单/弹窗内禁用。
  useEffect(() => {
    if (services.length === 0) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.metaKey || e.ctrlKey || e.altKey) return;
      const target = e.target as HTMLElement | null;
      if (target) {
        const tag = target.tagName;
        if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") return;
        if (target.isContentEditable) return;
        if (target.closest("[role='dialog'], [cmdk-input]")) return;
      }
      const curIdx =
        hlVmId != null ? services.findIndex((v) => v.id === hlVmId) : -1;
      if (e.key === "j") {
        e.preventDefault();
        const next = Math.min(services.length - 1, (curIdx < 0 ? -1 : curIdx) + 1);
        setHlVmId(services[next]?.id ?? null);
      } else if (e.key === "k") {
        e.preventDefault();
        const next = Math.max(0, (curIdx < 0 ? services.length : curIdx) - 1);
        setHlVmId(services[next]?.id ?? null);
      } else if (e.key === "Enter" && hlVmId != null) {
        e.preventDefault();
        navigate({ to: "/vm-detail", search: { id: hlVmId } });
      } else if (e.key === "n" && !e.shiftKey) {
        e.preventDefault();
        navigate({ to: "/launch" });
      } else if (e.key === "x" && hlVmId != null) {
        // 'x' 切换当前高亮行的 selection（Gmail/Linear 风）
        e.preventDefault();
        toggleOne(hlVmId);
      } else if (e.key === "Escape") {
        if (selected.size > 0) {
          setSelected(new Set());
        } else {
          setHlVmId(null);
        }
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
    // toggleOne 是稳定引用（每次渲染重建但不影响订阅）；显式列依赖在
    // services / hlVmId / selected.size 变更时重订阅即可。
  }, [services, hlVmId, navigate, selected]);

  // 焦点滚动跟踪
  useEffect(() => {
    if (hlVmId == null) return;
    const el = listRef.current?.querySelector<HTMLElement>(`[data-vm-id='${hlVmId}']`);
    el?.scrollIntoView({ block: "nearest" });
  }, [hlVmId]);

  useCommandActions(
    () => [
      {
        id: "user.create-vm",
        title: t("vm.createVm", { defaultValue: "新建 VM" }),
        icon: "Plus",
        perform: () => navigate({ to: "/launch" }),
      },
    ],
    [navigate, t],
  );

  // 并发批量调单台 endpoint —— portal 暂未提供 bulk action endpoint（admin 有 /vms:batch
  // 但 portal 路径不一样），前端 Promise.all 即可。每个调用都会让 react-query 自动 invalidate。
  const runBatchAction = async (action: "start" | "stop" | "restart") => {
    const ids = Array.from(selected);
    if (ids.length === 0) return;
    if (ids.length >= BATCH_TYPED_CONFIRM_THRESHOLD) {
      const ok = await confirm({
        title: t("vm.batchConfirmTitle", { defaultValue: "批量操作" }),
        message: t("vm.batchConfirmMessage", {
          count: ids.length,
          action,
          defaultValue: "确认对 {{count}} 台 VM 执行 {{action}}？",
        }),
      });
      if (!ok) return;
    }
    const results = await Promise.allSettled(
      ids.map((id) =>
        http.post(`/portal/services/${id}/actions/${action}`),
      ),
    );
    const okCount = results.filter((r) => r.status === "fulfilled").length;
    const failCount = results.length - okCount;
    queryClient.invalidateQueries({ queryKey: vmKeys.all });
    if (failCount === 0) {
      toast.success(t("vm.batchOk", { defaultValue: "{{n}} 台已 {{action}}", n: okCount, action }));
    } else {
      toast.error(t("vm.batchPartial", {
        defaultValue: "{{ok}} 成功 / {{fail}} 失败",
        ok: okCount,
        fail: failCount,
      }));
    }
    setSelected(new Set());
  };

  const runBatchTrash = async () => {
    const ids = Array.from(selected);
    if (ids.length === 0) return;
    if (ids.length >= BATCH_TYPED_CONFIRM_THRESHOLD) {
      const ok = await confirm({
        title: t("vm.batchDeleteTitle", { defaultValue: "批量删除" }),
        message: t("vm.batchDeleteMessage", {
          count: ids.length,
          defaultValue: "将 {{count}} 台 VM 移入回收站？30 秒内可撤销。",
        }),
        destructive: true,
        typeToConfirm: "DELETE",
        typeToConfirmLabel: t("confirmDialog.typeDelete", {
          defaultValue: "请输入 DELETE 以确认",
        }),
      });
      if (!ok) return;
    }
    let okCount = 0;
    let failCount = 0;
    await Promise.all(
      ids.map((id) =>
        trashMutation
          .mutateAsync(id)
          .then(() => okCount++)
          .catch(() => failCount++),
      ),
    );
    if (okCount > 0) {
      toast.success(
        t("vm.trashedToast", {
          defaultValue: "{{n}} 台已移入回收站 · 30 秒内可撤销",
          n: okCount,
        }),
        {
          duration: 30_000,
          action: {
            label: t("vm.undoAll", { defaultValue: "全部撤销" }),
            onClick: () => {
              ids.forEach((id) => restoreMutation.mutate(id));
            },
          },
        },
      );
    }
    if (failCount > 0) {
      toast.error(t("vm.batchPartial", {
        defaultValue: "{{ok}} 成功 / {{fail}} 失败",
        ok: okCount,
        fail: failCount,
      }));
    }
    setSelected(new Set());
  };

  const runSingleTrash = (vm: VMService) => {
    trashMutation.mutate(vm.id, {
      onSuccess: () => {
        toast.success(
          t("vm.trashedOneToast", {
            defaultValue: "{{name}} 已移入回收站 · 30 秒内可撤销",
            name: vm.name,
          }),
          {
            duration: 30_000,
            action: {
              label: t("vm.undo", { defaultValue: "撤销" }),
              onClick: () => restoreMutation.mutate(vm.id),
            },
          },
        );
      },
      onError: (err) =>
        toast.error(`${vm.name}: ${formatError(err) || t("vm.deleteFailed", { defaultValue: "删除失败" })}`),
    });
  };

  const allSelected = services.length > 0 && selected.size === services.length;
  const someSelected = selected.size > 0 && !allSelected;

  return (
    <PageShell>
      <PageHeader
        title={t("vm.myVms", { defaultValue: "我的云主机" })}
        description={t("vm.myVmsDescription", {
          defaultValue: "管理你的虚拟机：启动 / 停止 / 控制台 / 快照。删除支持 30 秒撤销。",
        })}
        actions={
          <Link to="/launch" className={cn(buttonVariants({ variant: "primary" }))}>
            <Plus size={14} aria-hidden="true" />
            {t("vm.createVm", { defaultValue: "新建 VM" })}
          </Link>
        }
      />
      <PageContent>
        {trashedList.length > 0 ? (
          <TrashBanner
            items={trashedList}
            onRestore={(id) => restoreMutation.mutate(id)}
          />
        ) : null}
        {isLoading ? (
          <div className="space-y-3">
            {Array.from({ length: 3 }).map((_, i) => (
              // eslint-disable-next-line react/no-array-index-key -- skeleton 占位
              <CardSkeleton key={i} />
            ))}
          </div>
        ) : services.length === 0 ? (
          <EmptyState
            title={t("vm.noneYet", { defaultValue: "你还没有云主机" })}
            description={t("vm.noneYetHint", {
              defaultValue: "前往订单页选择套餐创建你的第一台 VM。",
            })}
            action={
              <Link to="/launch" className={cn(buttonVariants({ variant: "primary" }))}>
                <Plus size={14} aria-hidden="true" />
                {t("vm.createVm", { defaultValue: "新建 VM" })}
              </Link>
            }
          />
        ) : (
          <>
            {services.length > 1 ? (
              <div className="flex items-center gap-2 px-3 pb-2 text-caption text-text-tertiary">
                <Checkbox
                  checked={allSelected}
                  // base-ui 不在类型上暴露 indeterminate，runtime 仍接收（OPS-038）
                  {...(someSelected ? { indeterminate: true } as Record<string, unknown> : {})}
                  onCheckedChange={toggleAll}
                  aria-label={t("vm.selectAll", { defaultValue: "全选" })}
                />
                <span>
                  {selected.size > 0
                    ? t("vm.selectedHint", { defaultValue: "已选 {{n}}/{{total}}", n: selected.size, total: services.length })
                    : t("vm.selectHint", { defaultValue: "勾选以批量操作（按 x 切换当前行）" })}
                </span>
              </div>
            ) : null}
            <div className="flex flex-col gap-1.5" ref={listRef}>
              {services.map((vm) => (
                <VMCard
                  key={vm.id}
                  vm={vm}
                  highlighted={vm.id === hlVmId}
                  selected={selected.has(vm.id)}
                  onToggleSelect={() => toggleOne(vm.id)}
                  onOpenSheet={(kind) => {
                    setSheetVM(vm);
                    setSheetKind(kind);
                  }}
                  onTrash={() => runSingleTrash(vm)}
                />
              ))}
            </div>
          </>
        )}
      </PageContent>

      <BatchToolbar count={selected.size} onClear={() => setSelected(new Set())}>
        <Button size="sm" variant="ghost" onClick={() => runBatchAction("start")}>
          <Play size={12} aria-hidden="true" />
          {t("vm.start", { defaultValue: "启动" })}
        </Button>
        <Button size="sm" variant="ghost" onClick={() => runBatchAction("stop")}>
          <Square size={12} aria-hidden="true" />
          {t("vm.stop", { defaultValue: "停止" })}
        </Button>
        <Button size="sm" variant="ghost" onClick={() => runBatchAction("restart")}>
          <RefreshCw size={12} aria-hidden="true" />
          {t("vm.restart", { defaultValue: "重启" })}
        </Button>
        <Button size="sm" variant="destructive" onClick={runBatchTrash}>
          <Trash2 size={12} aria-hidden="true" />
          {t("vm.delete", { defaultValue: "删除" })}
        </Button>
      </BatchToolbar>

      {sheetVM ? (
        <Sheet open={sheetKind !== null} onOpenChange={(o) => { if (!o) closeSheet(); }}>
          <SheetContent side="right" size="min(96vw, 38rem)">
            <SheetHeader>
              <SheetTitle>
                {sheetKind === "snapshots"
                  ? t("vm.snapshots", { defaultValue: "快照" })
                  : t("vm.metrics", { defaultValue: "指标" })}
                {" · "}
                <span className="font-mono text-text-tertiary">{sheetVM.name}</span>
              </SheetTitle>
              <SheetDescription>
                {sheetKind === "snapshots"
                  ? t("vm.snapshotsHelpUser", {
                      defaultValue: "你可以创建快照、回滚或删除（删除不可恢复）。",
                    })
                  : t("vm.metricsHelp", {
                      defaultValue: "实时指标流，每 5s 刷新一次。",
                    })}
              </SheetDescription>
            </SheetHeader>
            <SheetBody>
              {sheetKind === "snapshots" ? (
                <SnapshotPanel
                  vmName={sheetVM.name}
                  cluster={sheetVM.cluster}
                  project={sheetVM.project}
                  apiBase="/portal"
                />
              ) : null}
              {sheetKind === "metrics" && sheetVM.status === "running" ? (
                <VMMetricsPanel vmName={sheetVM.name} apiBase="/portal" />
              ) : null}
              {sheetKind === "metrics" && sheetVM.status !== "running" ? (
                <div className="text-sm text-muted-foreground">
                  {t("vm.metricsRunningOnly", {
                    defaultValue: "仅运行中的 VM 才有实时指标。",
                  })}
                </div>
              ) : null}
            </SheetBody>
          </SheetContent>
        </Sheet>
      ) : null}
    </PageShell>
  );
}

/**
 * TrashBanner —— 即便用户刷新页面也能看到 30s 内可恢复的 VM。trash 列表来自
 * 后端 GET /portal/services/trashed（DB 是真理之源，不依赖 toast 存活）。
 */
function TrashBanner({
  items,
  onRestore,
}: {
  items: MyTrashedVM[];
  onRestore: (id: number) => void;
}) {
  const { t } = useTranslation();
  return (
    <div
      className={cn(
        "mb-3 flex flex-wrap items-center gap-2 rounded-md border border-border",
        "bg-surface-2 px-3 py-2 text-caption",
      )}
      role="status"
    >
      <RotateCcw size={14} aria-hidden="true" className="text-text-tertiary" />
      <span className="text-text-secondary">
        {t("vm.trashBannerLead", {
          defaultValue: "{{n}} 台 VM 在回收站",
          n: items.length,
        })}
      </span>
      <div className="ml-auto flex flex-wrap items-center gap-1.5">
        {items.map((it) => (
          <TrashCountdown key={it.id} item={it} onRestore={onRestore} />
        ))}
      </div>
    </div>
  );
}

// Session-2 F-70 / PLAN-051 §2-K：把 1s ticker setState 隔离到子组件，items
// 多时不再每秒整表重渲染。每个 Countdown 维护自己的 now，父级 banner 静态。
function TrashCountdown({
  item,
  onRestore,
}: {
  item: MyTrashedVM;
  onRestore: (id: number) => void;
}) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const tick = setInterval(() => setNow(Date.now()), 1_000);
    return () => clearInterval(tick);
  }, []);
  const trashedAtMs = Date.parse(item.trashed_at);
  const remaining = Math.max(
    0,
    Math.ceil((trashedAtMs + item.window_s * 1000 - now) / 1000),
  );
  return (
    <Button
      size="sm"
      variant="subtle"
      onClick={() => onRestore(item.id)}
      disabled={remaining <= 0}
    >
      <RotateCcw size={12} aria-hidden="true" />
      <span className="font-mono">{item.name}</span>
      <span className="text-text-tertiary">· {remaining}s</span>
    </Button>
  );
}

function VMCard({
  vm,
  highlighted,
  selected,
  onOpenSheet,
  onToggleSelect,
  onTrash,
}: {
  vm: VMService;
  highlighted: boolean;
  selected: boolean;
  onOpenSheet: (k: NonNullable<SheetKind>) => void;
  onToggleSelect: () => void;
  onTrash: () => void;
}) {
  const { t } = useTranslation();
  const actionMutation = useVMActionMutation(vm.id);

  const runAction = (action: string) =>
    actionMutation.mutate(action, {
      onSuccess: () =>
        toast.success(`${vm.name}: ${action} ${t("vm.actionSubmitted", { defaultValue: "已提交" })}`),
      onError: () =>
        toast.error(`${vm.name}: ${action} ${t("vm.actionFailed", { defaultValue: "失败" })}`),
    });

  const status = vmStatusToKind(vm.status);
  const isRunning = vm.status === "running";
  const isStopped = vm.status === "stopped";

  /*
   * Linear-style VM 行：
   *   - 主行：checkbox + 状态 dot + 名字（白、mono）+ 主操作（hover/always）+ overflow（hover 显现）
   *   - 副行：所有元数据浓缩成一行（灰、caption），用 · 分隔
   *   - hover 整行轻微提亮，强化"可点击"
   *   - selected 行左侧 accent indicator
   */
  return (
    <Card
      data-vm-id={vm.id}
      className={cn(
        "group/vm hover:bg-surface-secondary/40 transition-colors",
        highlighted && "bg-surface-secondary/40 ring-1 ring-accent/40",
        selected && "bg-surface-2 ring-1 ring-accent/60",
      )}
    >
      <CardContent className="p-3 flex flex-col gap-1">
        <div className="flex items-center gap-3 min-w-0">
          <Checkbox
            checked={selected}
            onCheckedChange={onToggleSelect}
            aria-label={t("vm.selectRow", { defaultValue: "选择 {{name}}", name: vm.name })}
          />
          <StatusDot status={status} pulse={status === "pending"} />
          <Link
            to="/vm-detail"
            search={{ id: vm.id }}
            className="text-body font-mono font-strong text-foreground hover:text-accent transition-colors truncate"
          >
            {vm.name}
          </Link>
          <span className="text-caption text-text-tertiary truncate hidden sm:inline">
            {vm.cluster_display_name || vm.cluster}
          </span>
          {/* PLAN-037 / OPS-040：把节点标签提到主行（之前在 caption 灰字里），让用户
              一眼就能看到自己的 VM 在哪台物理机。无 location 时回退占位（保持布局稳定）。 */}
          {vm.node ? (
            <span
              className={cn(
                "hidden sm:inline-flex items-center gap-1 px-1.5 py-0.5 rounded-pill border border-border",
                "bg-surface-1 text-caption font-mono text-text-secondary",
              )}
              aria-label={t("vm.locatedOnNode", { defaultValue: "运行在节点 {{node}}", node: vm.node })}
            >
              <Server size={10} aria-hidden="true" />
              {vm.node}
            </span>
          ) : null}
          <div className="ml-auto flex items-center gap-1.5 shrink-0">
            {isRunning ? (
              <Link
                to="/console"
                search={{
                  vm: vm.name,
                  cluster: vm.cluster,
                  project: vm.project,
                  from: "portal",
                }}
                className={cn(buttonVariants({ variant: "subtle", size: "sm" }))}
              >
                <TerminalIcon size={12} aria-hidden="true" />
                <span className="hidden sm:inline">{t("vm.console")}</span>
              </Link>
            ) : null}
            {isStopped ? (
              <Button
                size="sm"
                variant="primary"
                disabled={actionMutation.isPending}
                onClick={() => runAction("start")}
              >
                <Play size={12} aria-hidden="true" />
                <span className="hidden sm:inline">{t("vm.start")}</span>
              </Button>
            ) : null}
            <DropdownMenu>
            <DropdownMenuTrigger
              render={
                <button
                  type="button"
                  aria-label={t("vm.moreActions", { defaultValue: "更多操作" })}
                  className="inline-flex h-7 items-center gap-1 rounded-md border border-border bg-surface-1 px-2 text-xs font-emphasis text-foreground hover:bg-surface-2 transition-colors"
                >
                  <MoreHorizontal size={14} aria-hidden="true" />
                </button>
              }
            />
            <DropdownMenuContent align="end" className="min-w-row-actions-menu">
              {isRunning ? (
                <>
                  <DropdownMenuItem onClick={() => runAction("stop")}>
                    <Square size={14} aria-hidden="true" />
                    {t("vm.stop")}
                  </DropdownMenuItem>
                  <DropdownMenuItem onClick={() => runAction("restart")}>
                    <RefreshCw size={14} aria-hidden="true" />
                    {t("vm.restart")}
                  </DropdownMenuItem>
                  <DropdownMenuItem onClick={() => onOpenSheet("metrics")}>
                    <ExternalLink size={14} aria-hidden="true" />
                    {t("vm.monitor", { defaultValue: "监控" })}
                  </DropdownMenuItem>
                </>
              ) : null}
              <DropdownMenuItem onClick={() => onOpenSheet("snapshots")}>
                <Camera size={14} aria-hidden="true" />
                {t("vm.snapshots")}
              </DropdownMenuItem>
              {vm.status === "frozen" ? (
                <DropdownMenuItem onClick={() => runAction("unfreeze")}>
                  <Pause size={14} aria-hidden="true" />
                  {t("vm.unfreeze", { defaultValue: "解冻" })}
                </DropdownMenuItem>
              ) : null}
              <DropdownMenuItem
                render={
                  <Link
                    to="/vm-detail"
                    search={{ id: vm.id }}
                  >
                    <ExternalLink size={14} aria-hidden="true" />
                    {t("vm.detailLink", { defaultValue: "详情页（更多操作）" })}
                  </Link>
                }
              />
              <DropdownMenuSeparator />
              <DropdownMenuItem destructive onClick={onTrash}>
                <Trash2 size={14} aria-hidden="true" />
                {t("vm.delete", { defaultValue: "删除" })}
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
          </div>
        </div>
        {/* 副行：所有元数据浓缩到一行（灰、caption），用 · 分隔 */}
        <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5 text-caption text-text-tertiary pl-5">
          <span className="font-mono text-text-secondary">
            {vm.ip || t("vm.assigning", { defaultValue: "分配中..." })}
          </span>
          <span aria-hidden>·</span>
          <span>{vm.cpu}C / {(vm.memory_mb / 1024).toFixed(0)}G / {vm.disk_gb}G</span>
          <span aria-hidden>·</span>
          <span className="font-mono">{defaultUserForImage(vm.os_image)}@{vm.node}</span>
          <span aria-hidden>·</span>
          <span className="font-mono truncate">{vm.os_image}</span>
        </div>
      </CardContent>
    </Card>
  );
}
