import type { PayResponse, VMCredentials } from "@/features/billing/api";
import type { Period } from "@/features/launch/components/period-picker";
import { useQuery } from "@tanstack/react-query";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { Rocket } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { useCreateOrderMutation, usePayOrderMutation } from "@/features/billing/api";
import { useJobQuery } from "@/features/jobs/api";
import { useJobStream } from "@/features/jobs/use-job-stream";
import { DonePanel } from "@/features/launch/components/done-panel";
import { FailedPanel } from "@/features/launch/components/failed-panel";
import { FormSection } from "@/features/launch/components/form-section";
import { PeriodPicker, productSupports } from "@/features/launch/components/period-picker";
import { EmptyPlanHint, PlanCard, PlanSkeleton } from "@/features/launch/components/plan-card";
import { ProvisioningPanel } from "@/features/launch/components/provisioning-panel";
import { SSHKeyHint } from "@/features/launch/components/ssh-key-hint";
import { SummaryCard } from "@/features/launch/components/summary-card";
import { useProductsQuery } from "@/features/products/api";
import { useSSHKeysQuery } from "@/features/ssh-keys/api";
import { DEFAULT_OS_IMAGE, OsImagePicker } from "@/features/vms/os-image-picker";
import {
  PageContent,
  PageHeader,
  PageShell,
} from "@/shared/components/page/page-shell";
import { Alert, AlertDescription } from "@/shared/components/ui/alert";
import { Button, buttonVariants } from "@/shared/components/ui/button";
import { Input } from "@/shared/components/ui/input";
import { Label } from "@/shared/components/ui/label";
import { MobileBottomBar } from "@/shared/components/ui/mobile-bottom-bar";
import { fetchCurrentUser } from "@/shared/lib/auth";
import { formatError } from "@/shared/lib/http";
import { cn } from "@/shared/lib/utils";

export const Route = createFileRoute("/launch")({
  component: LaunchPage,
});

/** 后端 SafeNameRe 镜像，避免 cryptic 400 */
const VM_NAME_RE = /^[a-z0-9][\w.-]*$/i;

type Phase = "form" | "provisioning" | "done" | "failed";

function LaunchPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();

  // ── 表单状态 ─────────────────────────────────────────────
  const [productId, setProductId] = useState<number | null>(null);
  const [osImage, setOsImage] = useState<string>(DEFAULT_OS_IMAGE);
  const [vmName, setVmName] = useState("");
  // PLAN-054 / INFRA-013：默认 monthly（保留现有行为）；product 不支持时
  // 提交按钮 disable，由用户切到支持的那一档。
  const [period, setPeriod] = useState<Period>("monthly");

  // ── provisioning / 完成态 ──────────────────────────────
  const [credentials, setCredentials] = useState<VMCredentials | null>(null);
  const [pending, setPending] = useState<PayResponse | null>(null);
  const [asyncError, setAsyncError] = useState<string | null>(null);

  // ── 数据源 ─────────────────────────────────────────────
  const { data: productsData, isLoading: productsLoading } = useProductsQuery();
  const { data: user } = useQuery({ queryKey: ["currentUser"], queryFn: fetchCurrentUser });
  const sshKeysQuery = useSSHKeysQuery();
  const products = (productsData?.products ?? []).filter((p) => p.active);
  const sshKeyCount = sshKeysQuery.data?.keys?.length ?? 0;
  const balance = user?.balance ?? 0;

  // 默认选第一个 active 产品 —— derived，避免 useEffect+setState 触发额外 re-render
  const effectiveProductId = productId ?? (products[0]?.id ?? null);
  const product = products.find((p) => p.id === effectiveProductId) ?? null;
  const orderMutation = useCreateOrderMutation();
  const payMutation = usePayOrderMutation();

  // ── 异步进度流（与 PurchaseSheet 同协议） ──────────────────
  const stream = useJobStream(pending?.job_id ?? null);
  const jobQuery = useJobQuery(stream.terminal != null ? (pending?.job_id ?? null) : null);

  useEffect(() => {
    if (credentials) return;
    if (stream.terminal !== "succeeded") return;
    const result = jobQuery.data?.result;
    if (!result?.password || !result.vm_name) return;
    // SSE job 完成后从外部 query 数据写入，无 derived 替代方案
    // eslint-disable-next-line react/set-state-in-effect
    setCredentials({
      vm_name: result.vm_name,
      ip: result.ip ?? "",
      username: result.username ?? "ubuntu",
      password: result.password,
    });
  }, [credentials, stream.terminal, jobQuery.data]);

  useEffect(() => {
    if (asyncError) return;
    if (stream.terminal !== "failed" && stream.terminal !== "partial") return;
    const lastFailed = stream.steps.slice().reverse().find((s) => s.status === "failed");
    // SSE 流终态写入，无 derived 替代方案
    // eslint-disable-next-line react/set-state-in-effect
    setAsyncError(
      lastFailed?.detail
      ?? t("jobs.failedGeneric", { defaultValue: "VM 创建失败，已自动退款。" }),
    );
  }, [asyncError, stream.terminal, stream.steps, t]);

  // ── 派生态 ─────────────────────────────────────────────
  const phase: Phase = credentials
    ? "done"
    : asyncError
      ? "failed"
      : pending != null && stream.terminal == null
        ? "provisioning"
        : "form";

  const isSubmitting = orderMutation.isPending || payMutation.isPending;
  const error = orderMutation.error || payMutation.error || asyncError;

  const vmNameError =
    vmName !== "" && !VM_NAME_RE.test(vmName)
      ? t("billing.vmNameInvalid", {
          defaultValue: "VM 名称只能包含字母、数字、点 . 横杠 - 和下划线 _，且必须以字母或数字开头",
        })
      : "";

  // PLAN-054：当前周期对应单价；不支持时 displayed unit price 仍存在但提交 disable。
  const unitPrice =
    period === "daily"
      ? product?.price_daily ?? null
      : product?.price_monthly ?? null;
  const periodSupported = productSupports(product, period);
  const balanceInsufficient =
    product != null && unitPrice != null && balance < unitPrice;
  const submitDisabled =
    isSubmitting
    || !product
    || !!vmNameError
    || balanceInsufficient
    || !periodSupported
    || unitPrice == null;

  const submitOrder = () => {
    if (!product || vmNameError) return;
    if (!periodSupported || unitPrice == null) return;
    setAsyncError(null);
    setCredentials(null);
    setPending(null);
    orderMutation.mutate(
      { product_id: product.id, vm_name: vmName || undefined, os_image: osImage, period },
      {
        onSuccess: (data) => {
          payMutation.mutate(
            { orderId: data.order.id, vm_name: vmName || undefined, os_image: osImage },
            {
              onSuccess: (res) => {
                if (res.password && res.vm_name) {
                  setCredentials({
                    vm_name: res.vm_name,
                    ip: res.ip ?? "",
                    username: res.username ?? "ubuntu",
                    password: res.password,
                  });
                  return;
                }
                if (res.job_id) setPending(res);
              },
            },
          );
        },
      },
    );
  };

  const reset = () => {
    setOsImage(DEFAULT_OS_IMAGE);
    setVmName("");
    setCredentials(null);
    setPending(null);
    setAsyncError(null);
  };

  return (
    <PageShell className="pb-24 md:pb-0">
      <PageHeader
        title={t("launch.title", { defaultValue: "创建云主机" })}
        breadcrumbs={[
          { label: t("vm.myVms", { defaultValue: "我的云主机" }), to: "/vms" },
          { label: t("launch.title", { defaultValue: "创建云主机" }) },
        ]}
        description={t("launch.description", {
          defaultValue: "选择套餐与镜像，下单即扣余额并立即 provision。",
        })}
      />

      {phase === "done"
        ? (
            <DonePanel
              credentials={credentials!}
              onCreateAnother={reset}
              onGoVms={() => navigate({ to: "/vms" })}
            />
          )
        : phase === "provisioning"
          ? <ProvisioningPanel steps={stream.steps} ip={pending?.ip ?? null} />
          : phase === "failed"
            ? <FailedPanel error={asyncError ?? ""} onRetry={reset} jobID={pending?.job_id ?? undefined} />
            : (
                <PageContent>
                  <div className="grid grid-cols-1 lg:grid-cols-3 gap-6">
                    <div className="lg:col-span-2 flex flex-col gap-6">
                      <FormSection
                        index="1"
                        title={t("launch.planTitle", { defaultValue: "选择套餐" })}
                        hint={t("launch.planHint", {
                          defaultValue: "vCPU / 内存 / SSD 三件套；月价从余额扣款",
                        })}
                      >
                        {productsLoading
                          ? <PlanSkeleton />
                          : products.length === 0
                            ? <EmptyPlanHint />
                            : (
                                <div className="grid grid-cols-1 sm:grid-cols-3 gap-2">
                                  {products.map((p) => (
                                    <PlanCard
                                      key={p.id}
                                      product={p}
                                      active={p.id === effectiveProductId}
                                      onSelect={() => setProductId(p.id)}
                                    />
                                  ))}
                                </div>
                              )}
                      </FormSection>

                      <FormSection
                        index="2"
                        title={t("period.title", { defaultValue: "计费周期" })}
                        hint={t("period.hint", {
                          defaultValue: "按月：一次扣 30 天，停机不退；按日：每天 UTC 00:00 扣 1 天。",
                        })}
                      >
                        <PeriodPicker
                          product={product}
                          value={period}
                          onChange={setPeriod}
                        />
                      </FormSection>

                      <FormSection
                        index="3"
                        title={t("vm.osImage", { defaultValue: "系统镜像" })}
                        hint={t("admin.osImageHint", {
                          defaultValue: "搜索发行版或版本快速定位",
                        })}
                      >
                        <OsImagePicker
                          value={osImage}
                          onChange={setOsImage}
                          ariaLabel={t("vm.osImage", { defaultValue: "系统镜像" })}
                        />
                      </FormSection>

                      <FormSection
                        index="4"
                        title={t("launch.authTitle", { defaultValue: "认证" })}
                        hint={t("launch.authHint", {
                          defaultValue:
                            "已绑定的 SSH Key 会自动注入到 authorized_keys；首次登录密码会显示一次。",
                        })}
                      >
                        <SSHKeyHint count={sshKeyCount} loading={sshKeysQuery.isLoading} />
                      </FormSection>

                      <FormSection
                        index="5"
                        title={t("launch.hostnameTitle", { defaultValue: "主机名" })}
                        hint={t("launch.hostnameHint", {
                          defaultValue: "可选；留空由后端自动生成 vm-xxxxxx",
                        })}
                      >
                        <div className="flex flex-col gap-1.5">
                          <Label htmlFor="vm-name" className="sr-only">
                            {t("launch.hostnameTitle", { defaultValue: "主机名" })}
                          </Label>
                          <Input
                            id="vm-name"
                            type="text"
                            value={vmName}
                            onChange={(e) => setVmName(e.target.value)}
                            placeholder="vm-my-app-01"
                            aria-invalid={!!vmNameError}
                          />
                          {vmNameError ? (
                            <div className="text-status-error text-caption">{vmNameError}</div>
                          ) : null}
                        </div>
                      </FormSection>
                    </div>

                    {/* mobile 下隐藏：fixed bottom bar 已承担主 CTA，summary 在 form
                        段落底部反而与 bar 视觉挤迫；桌面端恢复 sticky 右栏 */}
                    <aside className="hidden lg:block lg:col-span-1">
                      <div className="lg:sticky lg:top-20 flex flex-col gap-3">
                        <SummaryCard
                          product={product}
                          osImage={osImage}
                          vmName={vmName}
                          balance={balance}
                          balanceCurrency={product?.currency}
                          insufficient={balanceInsufficient}
                          period={period}
                          unitPrice={unitPrice}
                        />

                        {error ? (
                          <Alert variant="error">
                            <AlertDescription>
                              {typeof error === "string" ? error : formatError(error)}
                            </AlertDescription>
                          </Alert>
                        ) : null}

                        <Button
                          variant="primary"
                          className="w-full hidden md:flex"
                          disabled={submitDisabled}
                          onClick={submitOrder}
                          data-testid="launch-submit"
                        >
                          <Rocket size={14} aria-hidden="true" />
                          {isSubmitting
                            ? t("launch.creating", { defaultValue: "创建中..." })
                            : t("launch.cta", { defaultValue: "立即创建" })}
                        </Button>
                      </div>
                    </aside>
                  </div>
                </PageContent>
              )}

      {phase === "form" ? (
        <MobileBottomBar>
          <Link
            to="/vms"
            className={cn(buttonVariants({ variant: "ghost" }), "flex-1")}
          >
            {t("common.cancel")}
          </Link>
          <Button
            variant="primary"
            className="flex-1"
            disabled={submitDisabled}
            onClick={submitOrder}
          >
            <Rocket size={14} aria-hidden="true" />
            {isSubmitting
              ? t("launch.creating", { defaultValue: "创建中..." })
              : t("launch.cta", { defaultValue: "立即创建" })}
          </Button>
        </MobileBottomBar>
      ) : null}
    </PageShell>
  );
}
