import type { VMCredentials } from "@/features/billing/api";
import { CheckCircle2, ClipboardCopy, Download, Info, Plus } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { PageContent } from "@/shared/components/page/page-shell";
import { Button } from "@/shared/components/ui/button";
import { Card, CardContent } from "@/shared/components/ui/card";
import { SecretReveal } from "@/shared/components/ui/secret-reveal";

/** /launch 完成态：凭据 grid + 后续 CTA。
 *
 * UX-007 + OPS-051 / PLAN-052 Q7：
 *   - 凭据**明文显示**（私有云场景肩窥风险低于"复制不上"的功能挫败）
 *   - 「复制全部凭据」一键写入剪贴板（含 ssh 命令）
 *   - 「下载凭据」一次性 .txt（用户不依赖剪贴板的兜底）
 *   - 关页面后可在 `/vms/<id>` → 「初始凭据」走 step-up 重看
 */
export function DonePanel({
  credentials,
  onCreateAnother,
  onGoVms,
}: {
  credentials: VMCredentials;
  onCreateAnother: () => void;
  onGoVms: () => void;
}) {
  const { t } = useTranslation();
  const [copied, setCopied] = useState(false);

  const credentialsBlob = (): string => {
    return [
      `VM: ${credentials.vm_name}`,
      `IP: ${credentials.ip || "(分配中)"}`,
      `Username: ${credentials.username}`,
      `Password: ${credentials.password}`,
      ``,
      `ssh ${credentials.username}@${credentials.ip || "<IP>"}`,
    ].join("\n");
  };

  const copyAll = async () => {
    try {
      await navigator.clipboard.writeText(credentialsBlob());
      setCopied(true);
      toast.success(
        t("launch.credentialsCopied", { defaultValue: "凭据已复制" }),
      );
      window.setTimeout(setCopied, 1500, false);
    } catch {
      toast.error(
        t("launch.copyFailed", { defaultValue: "复制失败，请手动选中" }),
      );
    }
  };

  const download = () => {
    const lines = [
      `# IncusAdmin VM credentials`,
      `# Generated: ${new Date().toISOString()}`,
      `# 提示：保管好此文件，密码可在 VM 详情页 → 初始凭据 重新查看。`,
      ``,
      credentialsBlob(),
    ];
    const blob = new Blob([lines.join("\n")], { type: "text/plain;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    try {
      const a = document.createElement("a");
      a.href = url;
      a.download = `${credentials.vm_name}-credentials.txt`;
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      toast.success(t("launch.credentialsDownloaded", { defaultValue: "凭据已下载" }));
    } finally {
      URL.revokeObjectURL(url);
    }
  };

  return (
    <PageContent>
      <Card className="border-status-success/30 bg-status-success/8">
        <CardContent className="flex flex-col gap-4 p-5">
          <header className="flex items-center gap-2">
            <CheckCircle2 size={18} className="text-status-success" aria-hidden="true" />
            <h2 className="text-base font-emphasis text-status-success">
              {t("launch.done", { defaultValue: "云主机创建成功" })}
            </h2>
          </header>
          <p className="text-caption text-text-tertiary">
            {t("billing.saveCredentialsHint", {
              defaultValue: "请保存以下凭据。",
            })}
          </p>
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
            <SecretReveal
              label={t("vm.name")}
              value={credentials.vm_name}
              inline={false}
              alwaysReveal
            />
            <SecretReveal
              label={t("vm.ip")}
              value={credentials.ip || t("vm.assigning", { defaultValue: "分配中..." })}
              inline={false}
              alwaysReveal
            />
            <SecretReveal
              label={t("vm.username")}
              value={credentials.username}
              inline={false}
              alwaysReveal
            />
            <SecretReveal
              label={t("vm.password")}
              value={credentials.password}
              inline={false}
              alwaysReveal
            />
          </div>
          <div className="flex items-start gap-2 rounded-md border border-border bg-surface-1 px-3 py-2 text-caption text-text-secondary">
            <Info size={14} aria-hidden="true" className="mt-0.5 shrink-0 text-status-pending" />
            <span>
              {t("launch.credentialsRecoverableHint", {
                defaultValue:
                  "若关闭页面前未复制，可在 VM 详情页 → 「初始凭据」重新查看（需二次认证）。如果 SSH 暂时连不上，请等 1-2 分钟（cloud-init 正在装 SSH 服务）后再试。",
              })}
            </span>
          </div>
          <div className="flex flex-wrap items-center justify-end gap-2">
            <Button variant="primary" onClick={copyAll}>
              <ClipboardCopy size={14} aria-hidden="true" />
              {copied
                ? t("launch.credentialsCopied", { defaultValue: "已复制" })
                : t("launch.copyAll", { defaultValue: "复制全部凭据" })}
            </Button>
            <Button variant="ghost" onClick={download}>
              <Download size={14} aria-hidden="true" />
              {t("launch.downloadCredentials", { defaultValue: "下载凭据" })}
            </Button>
            <Button variant="ghost" onClick={onCreateAnother}>
              <Plus size={14} aria-hidden="true" />
              {t("launch.createAnother", { defaultValue: "再创建一台" })}
            </Button>
            <Button variant="ghost" onClick={onGoVms}>
              {t("launch.goVms", { defaultValue: "前往我的云主机" })}
            </Button>
          </div>
        </CardContent>
      </Card>
    </PageContent>
  );
}
