import type {ResetPasswordMode} from "@/features/vms/api";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { KeyRound, Play, RefreshCw, RotateCcw, ShieldCheck, ShieldX, Square, Terminal as TerminalIcon } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { useJobQuery } from "@/features/jobs/api";
import { AIDiagnosePanel } from "@/features/jobs/components/ai-diagnose-panel";
import { JobProgress } from "@/features/jobs/components/job-progress";
import { useJobStream } from "@/features/jobs/use-job-stream";
import { VMMetricsPanel } from "@/features/monitoring/vm-metrics-panel";
import { SnapshotPanel } from "@/features/snapshots/snapshot-panel";
import { DEFAULT_TEMPLATE_SLUG, TemplatePicker } from "@/features/templates/template-picker";
import {
  useMyVMDetailQuery,
  usePortalReinstallVMMutation,
  usePortalRescueEnterMutation,
  usePortalRescueExitMutation,
  useResetVMPasswordMutation,
  useViewInitialCredentialsMutation,
  useVMActionMutation,
} from "@/features/vms/api";
import { PortalFirewallPanel } from "@/features/vms/components/portal-firewall-panel";
import { defaultUserForImage } from "@/features/vms/default-user";
import {
  PageContent,
  PageHeader,
  PageShell,
} from "@/shared/components/page/page-shell";
import { Alert, AlertDescription } from "@/shared/components/ui/alert";
import { Button, buttonVariants } from "@/shared/components/ui/button";
import { Card, CardContent } from "@/shared/components/ui/card";
import { useConfirm } from "@/shared/components/ui/confirm-dialog";
import { EmptyState } from "@/shared/components/ui/empty-state";
import { SecretReveal } from "@/shared/components/ui/secret-reveal";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/shared/components/ui/select";
import {
  Sheet,
  SheetBody,
  SheetContent,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from "@/shared/components/ui/sheet";
import { Skeleton } from "@/shared/components/ui/skeleton";
import { StatusPill, vmStatusToKind } from "@/shared/components/ui/status";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/shared/components/ui/tabs";
import { formatError } from "@/shared/lib/http";
import { formatVmStatus } from "@/shared/lib/status-i18n";
import { cn, formatDate } from "@/shared/lib/utils";

export const Route = createFileRoute("/vm-detail")({
  validateSearch: (search: Record<string, unknown>) => ({
    id: Number(search.id) || 0,
  }),
  component: UserVMDetailPage,
});

function UserVMDetailPage() {
  const { t } = useTranslation();
  const confirm = useConfirm();
  const navigate = useNavigate();
  const { id } = Route.useSearch();
  const [reinstallOpen, setReinstallOpen] = useState(false);
  const [reinstallSlug, setReinstallSlug] = useState(DEFAULT_TEMPLATE_SLUG);
  const [reinstallJobId, setReinstallJobId] = useState<number | null>(null);
  const [resetPwdOpen, setResetPwdOpen] = useState(false);
  const [resetPwdMode, setResetPwdMode] = useState<ResetPasswordMode>("auto");
  // UX-007: 查看初始凭据（step-up gated）。点击 → mutation 触发，HttpError
  // step_up_required 已由 shared/lib/http 通用拦截 → 跳 OIDC → 回来 replay
  // → setInitialCredentials 弹 Sheet。
  const [initialCredsOpen, setInitialCredsOpen] = useState(false);
  const [initialCreds, setInitialCreds] = useState<{
    vm_name: string;
    ip: string | null;
    username: string;
    password: string;
    created_at: string;
  } | null>(null);

  const { data, isLoading } = useMyVMDetailQuery(id);
  const vm = data?.vm;

  const actionMutation = useVMActionMutation(id);
  const resetPwdMutation = useResetVMPasswordMutation(id);
  const viewInitialMutation = useViewInitialCredentialsMutation(id);
  const reinstallMutation = usePortalReinstallVMMutation(id);
  const rescueEnterMutation = usePortalRescueEnterMutation(id);
  const rescueExitMutation = usePortalRescueExitMutation(id);

  // PLAN-025：监听异步 reinstall job 进度
  const reinstallStream = useJobStream(reinstallJobId);
  const reinstallJobQuery = useJobQuery(
    reinstallStream.terminal != null ? reinstallJobId : null,
  );
  useEffect(() => {
    if (reinstallStream.terminal !== "succeeded") return;
    const result = reinstallJobQuery.data?.result;
    if (!result?.password) return;
    toast.success(
      t("vm.reinstallDone", {
        username: result.username ?? "root",
        password: result.password,
        defaultValue: "重装完成 · {{username}} / {{password}}",
      }),
      { duration: 30_000 },
    );
    // SSE 流终态后清理 jobId state，关流并关 dialog
    // eslint-disable-next-line react/set-state-in-effect
    setReinstallJobId(null);
    // eslint-disable-next-line react/set-state-in-effect
    setReinstallOpen(false);
  }, [reinstallStream.terminal, reinstallJobQuery.data, t]);
  useEffect(() => {
    if (reinstallStream.terminal === "failed" || reinstallStream.terminal === "partial") {
      const lastFailed = reinstallStream.steps.slice().reverse().find((s) => s.status === "failed");
      toast.error(
        lastFailed?.detail
        ?? t("vm.reinstallFailed", { defaultValue: "重装失败，VM 已标 error，请联系管理员。" }),
      );
      // SSE 失败终态后关流
      // eslint-disable-next-line react/set-state-in-effect
      setReinstallJobId(null);
    }
  }, [reinstallStream.terminal, reinstallStream.steps, t]);

  const runResetPwd = () =>
    resetPwdMutation.mutate(resetPwdMode, {
      onSuccess: (data) => {
        const ch = data.channel ?? "online";
        const note = data.fallback ? `${ch} (fallback)` : ch;
        toast.success(
          t("vm.passwordResetToastWithChannel", {
            password: data.password,
            channel: note,
            defaultValue: "新密码: {{password}} · 通道: {{channel}}",
          }),
          {
            duration: 15000,
            action: {
              label: t("vm.passwordCopy", { defaultValue: "复制" }),
              onClick: () => {
                navigator.clipboard
                  .writeText(data.password)
                  .then(() =>
                    toast.success(t("vm.passwordCopied", { defaultValue: "已复制到剪贴板" })),
                  )
                  .catch(() =>
                    toast.error(
                      t("vm.passwordCopyFailed", { defaultValue: "复制失败，请手动复制" }),
                    ),
                  );
              },
            },
          },
        );
        setResetPwdOpen(false);
      },
      onError: () => toast.error(t("vm.passwordResetFailed")),
    });

  const runReinstall = async () => {
    if (!vm) return;
    const ok = await confirm({
      title: t("deleteConfirm.reinstallTitle"),
      message: t("deleteConfirm.reinstallMessage", { name: vm.name }),
      destructive: true,
      typeToConfirm: vm.name,
      typeToConfirmLabel: t("confirmDialog.typeVmName", {
        defaultValue: "请输入 VM 名称 {{name}} 以确认",
        name: vm.name,
      }),
    });
    if (!ok) return;
    reinstallMutation.mutate(
      { template_slug: reinstallSlug },
      {
        onSuccess: (data) => {
          if (data.job_id) {
            // PLAN-025：异步路径，开始 SSE 监听；新密码会在 done 时通过 GET /jobs 拉回。
            setReinstallJobId(data.job_id);
            toast.info(
              t("vm.reinstallStarted", {
                defaultValue: "重装已开始，进度展示中...",
              }),
            );
            return;
          }
          if (data.password && data.username) {
            toast.success(
              t("vm.reinstallDone", {
                username: data.username,
                password: data.password,
                defaultValue: "重装完成 · {{username}} / {{password}}",
              }),
              { duration: 20_000 },
            );
            setReinstallOpen(false);
          }
        },
        onError: (err) => toast.error(formatError(err)),
      },
    );
  };

  const runRescueEnter = async () => {
    if (!vm) return;
    const ok = await confirm({
      title: t("vm.rescueEnterTitle", { defaultValue: "进入 Rescue 模式" }),
      message: t("vm.rescueEnterMessage", {
        name: vm.name,
        defaultValue: "确认让 {{name}} 进入 Rescue 模式？会先拍快照再停机。",
      }),
      destructive: true,
    });
    if (!ok) return;
    rescueEnterMutation.mutate(undefined, {
      onSuccess: (res) =>
        toast.success(
          t("vm.rescueEntered", {
            snap: res.snapshot,
            defaultValue: "已进入 Rescue；快照 {{snap}}",
          }),
          { duration: 15_000 },
        ),
      onError: (err) => toast.error(formatError(err)),
    });
  };

  const runRescueExit = async (restore: boolean) => {
    if (!vm) return;
    const ok = await confirm({
      title: t("vm.rescueExitTitle", { defaultValue: "退出 Rescue 模式" }),
      message: restore
        ? t("vm.rescueExitRestoreMessage", {
            name: vm.name,
            defaultValue: "确认退出 Rescue 并恢复快照？{{name}} 会回滚到进入前的状态。",
          })
        : t("vm.rescueExitMessage", {
            name: vm.name,
            defaultValue: "确认退出 Rescue？{{name}} 会直接启动（不恢复快照）。",
          }),
      destructive: restore,
    });
    if (!ok) return;
    rescueExitMutation.mutate(
      { restore, delete_snapshot: false },
      {
        onSuccess: () =>
          toast.success(
            restore
              ? t("vm.rescueExitedRestored", { defaultValue: "已恢复快照并启动" })
              : t("vm.rescueExited", { defaultValue: "已退出 Rescue" }),
          ),
        onError: (err) => toast.error(formatError(err)),
      },
    );
  };

  if (id > 0 && isLoading) {
    return (
      <PageShell>
        <PageHeader title={<Skeleton className="h-8 w-40" />} />
        <PageContent>
          <Skeleton className="h-32" />
        </PageContent>
      </PageShell>
    );
  }

  if (!vm) {
    return (
      <PageShell>
        <PageContent>
          <EmptyState
            title={t("vm.notFoundTitle")}
            description={t("vm.notFoundHint")}
            action={
              <Button variant="primary" onClick={() => navigate({ to: "/vms" })}>
                {t("vm.backToList")}
              </Button>
            }
          />
        </PageContent>
      </PageShell>
    );
  }

  const isRunning = vm.status === "running";
  const isStopped = vm.status === "stopped";

  return (
    <PageShell>
      <PageHeader
        title={<span className="font-mono">{vm.name}</span>}
        breadcrumbs={[{ label: t("vm.myVms", { defaultValue: "我的云主机" }), to: "/vms" }, { label: vm.name }]}
        meta={<StatusPill status={vmStatusToKind(vm.status)}>{formatVmStatus(t, vm.status)}</StatusPill>}
        description={`${vm.cpu}C · ${(vm.memory_mb / 1024).toFixed(0)}G RAM · ${vm.disk_gb}G Disk`}
        actions={
          <div className="flex flex-wrap items-center gap-1.5">
            {isRunning ? (
              <>
                <Link
                  to="/console"
                  search={{
                    vm: vm.name,
                    cluster: vm.cluster,
                    project: vm.project,
                    from: "portal",
                  }}
                  className={cn(buttonVariants({ variant: "primary", size: "sm" }))}
                >
                  <TerminalIcon size={12} aria-hidden="true" />
                  {t("vm.console")}
                </Link>
                <Button size="sm" variant="ghost" disabled={actionMutation.isPending} onClick={() => actionMutation.mutate("stop")}>
                  <Square size={12} aria-hidden="true" />
                  {t("vm.stop")}
                </Button>
                <Button size="sm" variant="ghost" disabled={actionMutation.isPending} onClick={() => actionMutation.mutate("restart")}>
                  <RefreshCw size={12} aria-hidden="true" />
                  {t("vm.restart")}
                </Button>
                <Button
                  size="sm"
                  variant="ghost"
                  disabled={viewInitialMutation.isPending}
                  title={t("vm.initialCredentialsHint", {
                    defaultValue: "查看创建时生成的初始 root 密码（需重新认证）。未来修改请用「重置密码」。",
                  })}
                  onClick={() =>
                    viewInitialMutation.mutate(undefined, {
                      onSuccess: (data) => {
                        setInitialCreds(data);
                        setInitialCredsOpen(true);
                      },
                      onError: (err) => {
                        const msg = formatError(err);
                        if (msg.includes("no_initial_password") || msg.includes("decrypt_failed")) {
                          toast.warning(
                            t("vm.initialCredentialsUnavailable", {
                              defaultValue: "无法读取初始凭据，请使用「重置密码」生成新密码。",
                            }),
                          );
                          return;
                        }
                        toast.error(msg);
                      },
                    })
                  }
                >
                  <KeyRound size={12} aria-hidden="true" />
                  {t("vm.initialCredentials", { defaultValue: "初始凭据" })}
                </Button>
                <Button size="sm" variant="ghost" disabled={resetPwdMutation.isPending} onClick={() => setResetPwdOpen(true)}>
                  {t("vm.passwordReset", { defaultValue: "重置密码" })}
                </Button>
              </>
            ) : null}
            {isStopped ? (
              <Button size="sm" variant="primary" disabled={actionMutation.isPending} onClick={() => actionMutation.mutate("start")}>
                <Play size={12} aria-hidden="true" />
                {t("vm.start")}
              </Button>
            ) : null}
            <Button size="sm" variant="ghost" disabled={reinstallMutation.isPending} onClick={() => setReinstallOpen(true)}>
              <RotateCcw size={12} aria-hidden="true" />
              {t("vm.reinstall")}
            </Button>
            {vm.status !== "rescue" ? (
              <Button size="sm" variant="ghost" disabled={rescueEnterMutation.isPending} onClick={runRescueEnter}>
                <ShieldCheck size={12} aria-hidden="true" />
                {t("vm.rescueEnter", { defaultValue: "Rescue" })}
              </Button>
            ) : (
              <Button size="sm" variant="ghost" disabled={rescueExitMutation.isPending} onClick={() => runRescueExit(false)}>
                <ShieldX size={12} aria-hidden="true" />
                {t("vm.rescueExit", { defaultValue: "Rescue 退出" })}
              </Button>
            )}
          </div>
        }
      />
      <PageContent>
        <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
          <InfoCard label={t("vm.ip", { defaultValue: "IP" })} value={vm.ip || "—"} mono />
          <InfoCard label={t("vm.username", { defaultValue: "用户名" })} value={defaultUserForImage(vm.os_image)} mono />
          <InfoCard label={t("vm.node", { defaultValue: "节点" })} value={vm.node} />
          <InfoCard label={t("vm.created", { defaultValue: "创建时间" })} value={formatDate(vm.created_at)} />
        </div>

        <Tabs defaultValue="overview">
          <TabsList>
            <TabsTrigger value="overview">{t("vm.monitor", { defaultValue: "监控" })}</TabsTrigger>
            <TabsTrigger value="snapshots">{t("vm.snapshots", { defaultValue: "快照" })}</TabsTrigger>
            <TabsTrigger value="firewall">{t("vm.firewall.tab", { defaultValue: "防火墙" })}</TabsTrigger>
          </TabsList>
          <TabsContent value="overview">
            <VMMetricsPanel vmName={vm.name} apiBase="/portal" />
          </TabsContent>
          <TabsContent value="snapshots">
            <SnapshotPanel vmName={vm.name} cluster={vm.cluster} project={vm.project} apiBase="/portal" />
          </TabsContent>
          <TabsContent value="firewall">
            <PortalFirewallPanel vmID={vm.id} />
          </TabsContent>
        </Tabs>
      </PageContent>

      {/* Reinstall Sheet */}
      <Sheet open={reinstallOpen} onOpenChange={(o) => { if (!o) setReinstallOpen(false); }}>
        <SheetContent side="right" size="min(96vw, 32rem)">
          <SheetHeader>
            <SheetTitle>
              {`${t("vm.reinstall")} · ${vm.name}`}
            </SheetTitle>
          </SheetHeader>
          <SheetBody className="space-y-4">
            {reinstallJobId
              ? (
                  /* PLAN-025：异步进行中，展示进度，禁用表单 */
                  <>
                    <Alert variant="info">
                      <AlertDescription>
                        {t("vm.reinstallStarted", { defaultValue: "重装已开始，进度展示中..." })}
                      </AlertDescription>
                    </Alert>
                    <JobProgress steps={reinstallStream.steps} />
                    {/* PLAN-038 / OPS-041 Phase C + pma-cr F4：reinstall 失败挂 AI 诊断 */}
                    {(reinstallStream.terminal === "failed" || reinstallStream.terminal === "partial") && (
                      <AIDiagnosePanel jobID={reinstallJobId} />
                    )}
                  </>
                )
              : (
                  <>
                    <Alert variant="error">
                      <AlertDescription>{t("vm.reinstallWarning")}</AlertDescription>
                    </Alert>
                    <div className="space-y-1.5">
                      <label className="text-sm font-emphasis">
                        {t("vm.targetTemplate", { defaultValue: "目标系统镜像" })}
                      </label>
                      <TemplatePicker
                        value={reinstallSlug}
                        onChange={setReinstallSlug}
                        className="h-9 w-full rounded-md border border-border bg-surface-1 px-3 text-sm focus:outline-none focus:border-ring"
                      />
                    </div>
                  </>
                )}
          </SheetBody>
          <SheetFooter>
            {reinstallJobId
              ? (
                  <Button variant="ghost" onClick={() => setReinstallOpen(false)}>
                    {t("billing.closeAndContinue", { defaultValue: "关闭抽屉（后台继续）" })}
                  </Button>
                )
              : (
                  <>
                    <Button variant="ghost" onClick={() => setReinstallOpen(false)}>
                      {t("common.cancel")}
                    </Button>
                    <Button variant="destructive" disabled={reinstallMutation.isPending} onClick={runReinstall}>
                      {reinstallMutation.isPending ? t("vm.reinstalling") : t("vm.reinstallConfirm")}
                    </Button>
                  </>
                )}
          </SheetFooter>
        </SheetContent>
      </Sheet>

      {/* UX-007 / 查看初始凭据 Sheet */}
      <Sheet open={initialCredsOpen} onOpenChange={(o) => { if (!o) setInitialCredsOpen(false); }}>
        <SheetContent side="right" size="min(96vw, 28rem)">
          <SheetHeader>
            <SheetTitle>
              {t("vm.initialCredentialsTitle", {
                name: vm.name,
                defaultValue: "{{name}} 初始凭据",
              })}
            </SheetTitle>
          </SheetHeader>
          <SheetBody className="space-y-4">
            <p className="text-caption text-muted-foreground">
              {t("vm.initialCredentialsHint", {
                defaultValue: "查看创建时生成的初始 root 密码（需重新认证）。未来修改请用「重置密码」。",
              })}
            </p>
            {initialCreds ? (
              <div className="grid grid-cols-1 gap-3">
                <SecretReveal
                  label={t("vm.username", { defaultValue: "用户名" })}
                  value={initialCreds.username}
                  inline={false}
                />
                <SecretReveal
                  label={t("vm.password", { defaultValue: "密码" })}
                  value={initialCreds.password}
                  inline={false}
                />
                {initialCreds.ip ? (
                  <SecretReveal
                    label={t("vm.ip", { defaultValue: "IP" })}
                    value={initialCreds.ip}
                    inline={false}
                    autoMaskMs={0}
                  />
                ) : null}
              </div>
            ) : null}
          </SheetBody>
          <SheetFooter>
            <Button variant="primary" onClick={() => setInitialCredsOpen(false)}>
              {t("common.close", { defaultValue: "关闭" })}
            </Button>
          </SheetFooter>
        </SheetContent>
      </Sheet>

      {/* Reset Password Sheet */}
      <Sheet open={resetPwdOpen} onOpenChange={(o) => { if (!o) setResetPwdOpen(false); }}>
        <SheetContent side="right" size="min(96vw, 28rem)">
          <SheetHeader>
            <SheetTitle>
              {t("vm.resetPwdHeading", { name: vm.name, defaultValue: "重置 {{name}} 密码" })}
            </SheetTitle>
          </SheetHeader>
          <SheetBody className="space-y-4">
            <p className="text-caption text-muted-foreground">
              {t("vm.resetPwdModeHint", {
                defaultValue: "auto：先 online 失败后回落 offline；online：guest-agent chpasswd；offline：cloud-init 重启",
              })}
            </p>
            <div className="space-y-1.5">
              <label className="text-sm font-emphasis">
                {t("vm.resetPwdMode", { defaultValue: "模式" })}
              </label>
              <Select value={resetPwdMode} onValueChange={(v) => setResetPwdMode(String(v) as ResetPasswordMode)}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="auto">auto</SelectItem>
                  <SelectItem value="online">online</SelectItem>
                  <SelectItem value="offline">offline</SelectItem>
                </SelectContent>
              </Select>
            </div>
          </SheetBody>
          <SheetFooter>
            <Button variant="ghost" onClick={() => setResetPwdOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button variant="primary" disabled={resetPwdMutation.isPending} onClick={runResetPwd}>
              {resetPwdMutation.isPending ? t("vm.passwordResetting") : t("vm.passwordReset")}
            </Button>
          </SheetFooter>
        </SheetContent>
      </Sheet>
    </PageShell>
  );
}

function InfoCard({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <Card>
      <CardContent className="p-3">
        <div className="text-caption text-text-tertiary">{label}</div>
        <div className={cn("text-sm font-emphasis mt-0.5", mono && "font-mono")}>{value}</div>
      </CardContent>
    </Card>
  );
}
