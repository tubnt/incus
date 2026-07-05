import type {ProvisioningJobStep, StepStatus} from "../api";
import { useTranslation } from "react-i18next";
import { StatusDot } from "@/shared/components/ui/status";
import { cn } from "@/shared/lib/utils";
import {  stepLabelKey  } from "../api";

/**
 * 进度面板：按 step 顺序竖向铺开，左侧状态点 + 右侧标题 + 失败时附 detail。
 *
 * 设计 token（DESIGN.md 优先）：
 *   - 容器：bg-surface-1 border-border rounded-md，无投影（嵌入卡片）
 *   - step 标题：text-small font-emphasis text-foreground
 *   - detail：text-caption text-text-tertiary
 *   - failed detail：text-caption text-status-error
 *   - 状态点：StatusDot pulse=true（running/pending）
 *   - 全程禁止 hex 字面量、禁止 arbitrary value
 */
export function JobProgress({
  steps,
  className,
}: {
  steps: ProvisioningJobStep[];
  className?: string;
}) {
  const { t } = useTranslation();
  if (steps.length === 0) {
    return (
      <div
        className={cn(
          "rounded-md border border-border bg-surface-1 p-3 text-caption text-text-tertiary",
          className,
        )}
      >
        {t("jobs.queued", { defaultValue: "已排队，等待 worker 拾取..." })}
      </div>
    );
  }

  return (
    <ol
      className={cn(
        "flex flex-col gap-2 rounded-md border border-border bg-surface-1 p-3",
        className,
      )}
    >
      {steps.map((step) => {
        const labelKey = stepLabelKey[step.name];
        const label = labelKey
          ? t(labelKey, { defaultValue: step.name })
          : step.name;
        return (
          <li key={step.seq} className="flex items-start gap-3">
            <span className="mt-1.5">
              <StepDot status={step.status} />
            </span>
            <div className="flex-1 min-w-0">
              <div className="text-small font-emphasis text-foreground">{label}</div>
              {step.detail
                ? (
                    <div
                      className={cn(
                        "text-caption mt-0.5 break-words",
                        step.status === "failed"
                          ? "text-status-error"
                          : step.status === "warning"
                            ? "text-status-pending"
                            : "text-text-tertiary",
                      )}
                    >
                      {step.detail}
                    </div>
                  )
                : null}
            </div>
          </li>
        );
      })}
    </ol>
  );
}

function StepDot({ status }: { status: StepStatus }) {
  switch (status) {
    case "running":
      return <StatusDot status="pending" pulse />;
    case "succeeded":
      return <StatusDot status="success" />;
    case "failed":
      return <StatusDot status="error" />;
    case "warning":
      // OPS-051 / PLAN-052：smoke test 软失败用 pending 黄点（与 running 区分
      // 因为 warning 是终态 —— 不脉动）
      return <StatusDot status="pending" />;
    case "skipped":
      return <StatusDot status="disabled" />;
    case "pending":
    default:
      return <StatusDot status="disabled" />;
  }
}
